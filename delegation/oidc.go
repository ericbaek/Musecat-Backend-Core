package delegation

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const OpenID = "openid"
const Profile = "profile"

type oidcKey struct {
	key *rsa.PrivateKey
	kid string
}

// ConfigureOIDC is startup-only configuration. Keys are base64 PKCS8 DER RSA
// private keys, newest first. Keep old keys published until issued ID tokens expire.
// Key material belongs in the executable's secret store, never in application rows.
func (m *Manager) ConfigureOIDC(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	if m == nil {
		return fmt.Errorf("OIDC requires OAuth issuer and consent configuration")
	}
	issuerURL, _ := url.Parse(m.issuer)
	if issuerURL.Path != "" && issuerURL.Path != "/" {
		return fmt.Errorf("OIDC requires an HTTPS origin issuer")
	}
	var encoded []string
	if len(raw) > 65536 || json.Unmarshal([]byte(raw), &encoded) != nil || len(encoded) < 1 || len(encoded) > 4 {
		return fmt.Errorf("invalid OIDC signing key configuration")
	}
	keys := make([]oidcKey, 0, len(encoded))
	seen := map[string]bool{}
	for _, value := range encoded {
		der, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			return fmt.Errorf("invalid OIDC signing key")
		}
		parsed, err := x509.ParsePKCS8PrivateKey(der)
		if err != nil {
			return fmt.Errorf("invalid OIDC signing key")
		}
		key, ok := parsed.(*rsa.PrivateKey)
		if !ok || key.N.BitLen() < 2048 || key.Validate() != nil {
			return fmt.Errorf("OIDC requires valid RSA keys of at least 2048 bits")
		}
		public, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
		sum := sha256.Sum256(public)
		kid := base64.RawURLEncoding.EncodeToString(sum[:])
		if seen[kid] {
			return fmt.Errorf("duplicate OIDC signing key")
		}
		seen[kid] = true
		keys = append(keys, oidcKey{key: key, kid: kid})
	}
	m.mu.Lock()
	m.oidcKeys = keys
	m.mu.Unlock()
	return nil
}
func (m *Manager) OIDCEnabled() bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.oidcKeys) > 0
}
func (m *Manager) JWKS() (map[string]any, error) {
	if !m.OIDCEnabled() {
		return nil, &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := []map[string]string{}
	for _, k := range m.oidcKeys {
		keys = append(keys, map[string]string{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": k.kid, "n": base64.RawURLEncoding.EncodeToString(k.key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.key.E)).Bytes())})
	}
	return map[string]any{"keys": keys}, nil
}
func (m *Manager) OIDCMetadata() (map[string]any, error) {
	if !m.OIDCEnabled() {
		return nil, &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	v := m.Metadata()
	v["jwks_uri"] = m.issuer + "/oauth/jwks"
	v["userinfo_endpoint"] = m.issuer + "/oauth/userinfo"
	// Subjects are scoped to each registered client; no cross-client account mapping.
	v["subject_types_supported"] = []string{"pairwise"}
	v["id_token_signing_alg_values_supported"] = []string{"RS256"}
	v["response_modes_supported"] = []string{"query"}
	v["claims_supported"] = []string{"iss", "sub", "aud", "exp", "iat", "nonce", "preferred_username", "nickname"}
	v["request_parameter_supported"] = false
	v["request_uri_parameter_supported"] = false
	v["claims_parameter_supported"] = false
	return v, nil
}
func (m *Manager) signIDToken(grant *core.Record, nonce string, lifetime int) (string, error) {
	if !m.OIDCEnabled() {
		return "", &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	m.mu.RLock()
	k := m.oidcKeys[0]
	m.mu.RUnlock()
	now := time.Now().UTC()
	claims := jwt.MapClaims{"iss": m.issuer, "sub": grant.GetString("subject"), "aud": grant.GetString("client"), "iat": now.Unix(), "exp": now.Add(time.Duration(lifetime) * time.Second).Unix()}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = k.kid
	return token.SignedString(k.key)
}

// UserInfo requires an explicitly approved openid scope. General account access
// never implies login permission or an ID token. No email or global user ID is exposed.
func (m *Manager) UserInfo(app core.App, raw string) (map[string]string, error) {
	u, sub, err := m.ResolveAccess(app, raw, OpenID)
	if err != nil {
		return nil, err
	}
	row, err := find(app, Tokens, "access_hash={:hash}", dbx.Params{"hash": hash(raw)})
	if err != nil {
		return nil, err
	}
	out := map[string]string{"sub": sub}
	if contains(row.GetStringSlice("scopes"), Profile) {
		out["preferred_username"] = u.GetString("username")
		if info, err := app.FindRecordById("user_info", u.Id); err == nil {
			out["nickname"] = info.GetString("nickname")
		}
	}
	return out, nil
}

// VerifyLoginGrant verifies a redeemed, live OpenID generation for conformance.
// A broad API grant alone is never evidence of a successful sign-in.
func (m *Manager) VerifyLoginGrant(app core.App, userID, clientID, id, generation string) error {
	g, err := app.FindRecordById(Grants, id)
	if err != nil || g.GetString("user") != userID || generation == "" || g.GetString("generation") != generation || !contains(g.GetStringSlice("scopes"), OpenID) {
		return oauthError("invalid_grant")
	}
	row, err := app.FindFirstRecordByFilter(Tokens, "grant={:g} && generation={:v} && refresh_used=false && refresh_expires>{:now}", dbx.Params{"g": id, "v": generation, "now": time.Now().UTC().Format("2006-01-02 15:04:05.000Z")})
	if err != nil {
		return oauthError("invalid_grant")
	}
	_, _, err = m.activeGrant(app, row, clientID)
	return err
}
