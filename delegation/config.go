// Package delegation implements Musecat-issued, scope-limited OAuth grants.
package delegation

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/pocketbase/pocketbase/core"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const (
	Registrations = "oauth_client_registration"
	Grants        = "oauth_grant"
	Requests      = "oauth_authorization_request"
	Codes         = "oauth_authorization_code"
	Tokens        = "oauth_delegated_token"
	AccessSeconds = 600
)

var scopeLabels = map[string]string{
	"profile:read": "Read your basic profile", "arcade:read": "Read public arcade information",
	"arcade:report:create": "Create arcade fault reports", "arcade:report:react": "React to arcade fault reports",
	"campaign:read": "Read arcade campaigns", "campaign:respond": "Submit location-based campaign responses",
}
var identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)
var pkce = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)
var verifierPattern = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

type Client struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Secret       string   `json:"secret"`
	RedirectURIs []string `json:"redirect_uris"`
	Scopes       []string `json:"scopes"`
}
type registeredClient struct {
	Client
	secretHash  string
	fingerprint string
}
type Manager struct {
	issuer     string
	consentURL string
	clients    map[string]registeredClient
	checkUser  func(core.App, *core.Record) error
}
type OAuthError struct {
	Code   string
	Status int
}

func (e *OAuthError) Error() string { return e.Code }
func oauthError(code string) error  { return &OAuthError{Code: code, Status: 400} }
func hash(v string) string          { s := sha256.Sum256([]byte(v)); return hex.EncodeToString(s[:]) }
func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure randomness unavailable")
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func same(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func validHTTPS(raw string) bool {
	if len(raw) > 2048 {
		return false
	}
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" && u.RawQuery == "" && !u.ForceQuery
}
func uniqueScopes(values []string, allowed []string) ([]string, error) {
	if len(values) == 0 || len(values) > len(scopeLabels) {
		return nil, oauthError("invalid_scope")
	}
	seen := map[string]bool{}
	result := []string{}
	for _, s := range values {
		if scopeLabels[s] == "" || seen[s] || !contains(allowed, s) {
			return nil, oauthError("invalid_scope")
		}
		seen[s] = true
		result = append(result, s)
	}
	sort.Strings(result)
	return result, nil
}
func contains(values []string, v string) bool {
	for _, s := range values {
		if s == v {
			return true
		}
	}
	return false
}
func NewManager(issuer, consentURL string, clients []Client, check func(core.App, *core.Record) error) (*Manager, error) {
	if len(clients) > 128 {
		return nil, fmt.Errorf("too many OAuth clients")
	}
	if !validHTTPS(issuer) || !validHTTPS(consentURL) || check == nil {
		return nil, fmt.Errorf("invalid OAuth issuer, consent URL, or account policy")
	}
	origin, _ := url.Parse(issuer)
	if origin.Path != "" && origin.Path != "/" {
		return nil, fmt.Errorf("OAuth issuer must be an origin")
	}
	m := &Manager{issuer: strings.TrimSuffix(issuer, "/"), consentURL: consentURL, clients: map[string]registeredClient{}, checkUser: check}
	for _, c := range clients {
		c.RedirectURIs = append([]string(nil), c.RedirectURIs...)
		if !identifier.MatchString(c.ID) || c.Name == "" || len(c.Name) > 128 || len(c.Description) > 1024 || len(c.Secret) < 32 || len(c.Secret) > 256 || len(c.RedirectURIs) == 0 || len(c.RedirectURIs) > 16 {
			return nil, fmt.Errorf("invalid OAuth client configuration")
		}
		if _, ok := m.clients[c.ID]; ok {
			return nil, fmt.Errorf("duplicate OAuth client")
		}
		seen := map[string]bool{}
		for _, u := range c.RedirectURIs {
			if !validHTTPS(u) || seen[u] {
				return nil, fmt.Errorf("invalid OAuth redirect URI")
			}
			seen[u] = true
		}
		all := []string{}
		for s := range scopeLabels {
			all = append(all, s)
		}
		scopes, e := uniqueScopes(c.Scopes, all)
		if e != nil {
			return nil, fmt.Errorf("invalid OAuth client scopes")
		}
		c.Scopes = scopes
		encoded, _ := json.Marshal(struct {
			Issuer string
			Client Client
		}{m.issuer, c})
		rc := registeredClient{Client: c, secretHash: hash(c.Secret), fingerprint: hash(string(encoded))}
		rc.Secret = ""
		m.clients[c.ID] = rc
	}
	return m, nil
}

// ParseConfig is used only at executable composition roots; no ambient env reads.
func ParseConfig(raw, issuer, consentURL string, check func(core.App, *core.Record) error) (*Manager, error) {
	if strings.TrimSpace(raw) == "" {
		if issuer != "" || consentURL != "" {
			return nil, fmt.Errorf("OAuth clients are required")
		}
		return nil, nil
	}
	var clients []Client
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&clients); err != nil {
		return nil, fmt.Errorf("invalid OAuth clients JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, fmt.Errorf("invalid OAuth clients JSON")
	}
	return NewManager(issuer, consentURL, clients, check)
}
func (m *Manager) AuthenticateClient(id, secret string) error {
	if m == nil {
		return &OAuthError{Code: "temporarily_unavailable", Status: 503}
	}
	c, ok := m.clients[id]
	if !ok || !same(c.secretHash, hash(secret)) {
		return &OAuthError{Code: "invalid_client", Status: 401}
	}
	return nil
}
func (m *Manager) Metadata() map[string]any {
	if m == nil {
		return nil
	}
	scopes := []string{}
	for s := range scopeLabels {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)
	return map[string]any{"issuer": m.issuer, "authorization_endpoint": m.issuer + "/oauth/authorize", "token_endpoint": m.issuer + "/oauth/token", "revocation_endpoint": m.issuer + "/oauth/revoke", "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"client_secret_basic"}, "code_challenge_methods_supported": []string{"S256"}, "authorization_response_iss_parameter_supported": true, "scopes_supported": scopes}
}
