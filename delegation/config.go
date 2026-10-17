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
	"sync"
)

const (
	Registrations = "oauth_client_registration"
	Grants        = "oauth_grant"
	Requests      = "oauth_authorization_request"
	Codes         = "oauth_authorization_code"
	Tokens        = "oauth_delegated_token"
	AccessSeconds = 600
	AccountAccess = "account:access"
)

var scopeLabels = map[string]string{
	AccountAccess:  "Connect your account for general user features; management and security settings excluded",
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
	// SecretHash is the hex SHA-256 of an already generated secret. Dynamic
	// registrations (Developer Center) supply it instead of Secret so plaintext
	// never leaves the one-time generation response.
	SecretHash string `json:"-"`
}
type registeredClient struct {
	Client
	secretHash  string
	fingerprint string
}

// Manager keeps a static client set from the executable configuration plus a
// dynamic set supplied through Reload. Lookups are lock-protected so approved
// Developer Center applications activate without a restart.
type Manager struct {
	issuer     string
	consentURL string
	static     []Client
	mu         sync.RWMutex
	clients    map[string]registeredClient
	checkUser  func(core.App, *core.Record) error
}

func (m *Manager) lookup(id string) (registeredClient, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.clients[id]
	return c, ok
}
func (m *Manager) snapshot() map[string]registeredClient {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]registeredClient, len(m.clients))
	for k, v := range m.clients {
		out[k] = v
	}
	return out
}

// ClientInfo is the public projection of a registered client; no secret material.
type ClientInfo struct {
	ID, Name, Description string
	RedirectURIs, Scopes  []string
}

// ConsentOrigin is the HTTPS origin of the configured consent application, i.e. the
// Musecat web deployment. Executables may use it to derive callback URLs.
func (m *Manager) ConsentOrigin() string {
	if m == nil {
		return ""
	}
	u, err := url.Parse(m.consentURL)
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (m *Manager) Client(id string) (ClientInfo, bool) {
	if m == nil {
		return ClientInfo{}, false
	}
	c, ok := m.lookup(id)
	if !ok {
		return ClientInfo{}, false
	}
	return ClientInfo{ID: c.ID, Name: c.Name, Description: c.Description, RedirectURIs: append([]string(nil), c.RedirectURIs...), Scopes: append([]string(nil), c.Scopes...)}, true
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
	if !validHTTPS(issuer) || !validHTTPS(consentURL) || check == nil {
		return nil, fmt.Errorf("invalid OAuth issuer, consent URL, or account policy")
	}
	origin, _ := url.Parse(issuer)
	if origin.Path != "" && origin.Path != "/" {
		return nil, fmt.Errorf("OAuth issuer must be an origin")
	}
	m := &Manager{issuer: strings.TrimSuffix(issuer, "/"), consentURL: consentURL, static: append([]Client(nil), clients...), checkUser: check}
	built, err := buildClients(m.issuer, clients, nil)
	if err != nil {
		return nil, err
	}
	m.clients = built
	return m, nil
}

// Reload replaces the dynamic client set while keeping the static executable set.
// It validates the merged registry before swapping and then synchronizes persistent
// registration epochs, so a changed or removed client invalidates its grants exactly as
// a configuration change at startup does. A nil Manager (OAuth disabled) rejects reloads.
func (m *Manager) Reload(app core.App, dynamic []Client) error {
	if m == nil {
		return fmt.Errorf("OAuth delegation is not configured")
	}
	built, err := buildClients(m.issuer, m.static, dynamic)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.clients = built
	m.mu.Unlock()
	return SynchronizeClients(app, m)
}

// Check validates a prospective dynamic set against the static set without swapping.
func (m *Manager) Check(dynamic []Client) error {
	if m == nil {
		return fmt.Errorf("OAuth delegation is not configured")
	}
	_, err := buildClients(m.issuer, m.static, dynamic)
	return err
}

func buildClients(issuer string, static, dynamic []Client) (map[string]registeredClient, error) {
	if len(static)+len(dynamic) > 128 {
		return nil, fmt.Errorf("too many OAuth clients")
	}
	out := map[string]registeredClient{}
	for _, c := range append(append([]Client(nil), static...), dynamic...) {
		c.RedirectURIs = append([]string(nil), c.RedirectURIs...)
		secretHash := c.SecretHash
		if c.Secret != "" {
			if len(c.Secret) < 32 || len(c.Secret) > 256 {
				return nil, fmt.Errorf("invalid OAuth client configuration")
			}
			secretHash = hash(c.Secret)
		}
		if !identifier.MatchString(c.ID) || c.Name == "" || len(c.Name) > 128 || len(c.Description) > 1024 || len(secretHash) != 64 || len(c.RedirectURIs) == 0 || len(c.RedirectURIs) > 16 {
			return nil, fmt.Errorf("invalid OAuth client configuration")
		}
		if _, ok := out[c.ID]; ok {
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
		// The fingerprint covers the secret hash rather than the plaintext so static and
		// dynamic registrations with identical material produce identical epochs.
		c.Secret = secretHash
		c.SecretHash = ""
		encoded, _ := json.Marshal(struct {
			Issuer string
			Client Client
		}{issuer, c})
		rc := registeredClient{Client: c, secretHash: secretHash, fingerprint: hash(string(encoded))}
		rc.Secret = ""
		out[c.ID] = rc
	}
	return out, nil
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
	c, ok := m.lookup(id)
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
