package connections

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

// Service describes a game data service, not an authentication identity or partner API client.
type Service struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

type Provider struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Issuer           string `json:"issuer"`
	AuthorizationURL string `json:"authorization_url"`
	TokenURL         string `json:"token_url"`
	UserInfoURL      string `json:"userinfo_url"`
	SubjectField     string `json:"subject_field"`
	ClientID         string `json:"client_id"`
	ClientSecret     string `json:"client_secret"`
	RedirectURI      string `json:"redirect_uri"`
	// client_secret_basic or client_secret_post; no automatic authentication retries.
	ClientAuthMethod string    `json:"client_auth_method"`
	RequireIssuer    bool      `json:"require_issuer"`
	Scopes           []string  `json:"scopes"`
	Services         []Service `json:"services"`
}

type ProviderInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Issuer   string    `json:"issuer"`
	Services []Service `json:"services"`
}

type Manager struct {
	providers map[string]Provider
	aead      cipher.AEAD
	client    *http.Client
}

// ParseConfig parses executable-supplied configuration without reading the environment.
func ParseConfig(raw, encodedKey string) (*Manager, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var providers []Provider
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&providers); err != nil {
		return nil, fmt.Errorf("invalid external provider configuration")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("invalid external provider configuration")
	}
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("external connection key must be base64 encoded")
	}
	return NewManager(providers, key, nil)
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

func NewManager(providers []Provider, key []byte, client *http.Client) (*Manager, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("external connection key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	m := &Manager{providers: map[string]Provider{}, aead: aead}
	if client == nil {
		client = &http.Client{}
	}
	// Copy rather than mutating an injected shared client. Redirects cannot forward credentials.
	copyClient := *client
	copyClient.Timeout = 10 * time.Second
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	m.client = &copyClient
	issuers, callbacks := map[string]bool{}, map[string]bool{}
	for _, p := range providers {
		if !identifier.MatchString(p.ID) || p.Name == "" || p.ClientID == "" || p.ClientSecret == "" || len(p.Services) == 0 || p.SubjectField == "" {
			return nil, fmt.Errorf("incomplete external provider configuration")
		}
		if p.ClientAuthMethod != "client_secret_basic" && p.ClientAuthMethod != "client_secret_post" {
			return nil, fmt.Errorf("external provider requires an explicit client authentication method")
		}
		if _, ok := m.providers[p.ID]; ok || issuers[p.Issuer] || callbacks[p.RedirectURI] {
			return nil, fmt.Errorf("external providers must have unique IDs, issuers and callback URLs")
		}
		for _, endpoint := range []string{p.Issuer, p.AuthorizationURL, p.TokenURL, p.UserInfoURL, p.RedirectURI} {
			u, err := url.Parse(endpoint)
			if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" || u.RawQuery != "" {
				return nil, fmt.Errorf("external provider endpoints must be fixed HTTPS URLs without queries or fragments")
			}
		}
		ids := map[string]bool{}
		for _, s := range p.Services {
			if !identifier.MatchString(s.ID) || s.Name == "" || ids[s.ID] {
				return nil, fmt.Errorf("invalid external service configuration")
			}
			ids[s.ID] = true
			if !validScopes(s.Scopes) {
				return nil, fmt.Errorf("invalid external service scopes")
			}
		}
		if !validScopes(p.Scopes) {
			return nil, fmt.Errorf("invalid external provider scopes")
		}
		// Keep caller-owned slices from changing a live registry.
		data, _ := json.Marshal(p)
		var cloned Provider
		_ = json.Unmarshal(data, &cloned)
		p = cloned
		for i := range p.Services {
			if p.Services[i].Scopes == nil {
				p.Services[i].Scopes = []string{}
			}
		}
		m.providers[p.ID] = p
		issuers[p.Issuer] = true
		callbacks[p.RedirectURI] = true
	}
	return m, nil
}

func validScopes(scopes []string) bool {
	for _, s := range scopes {
		if s == "" || strings.ContainsAny(s, " \t\r\n") || len(s) > 256 {
			return false
		}
	}
	return len(scopes) <= 64
}

func (m *Manager) Providers() []ProviderInfo {
	out := []ProviderInfo{}
	if m == nil {
		return out
	}
	for _, p := range m.providers {
		services := slices.Clone(p.Services)
		for i := range services {
			services[i].Scopes = slices.Clone(services[i].Scopes)
		}
		out = append(out, ProviderInfo{p.ID, p.Name, p.Issuer, services})
	}
	slices.SortFunc(out, func(a, b ProviderInfo) int { return strings.Compare(a.ID, b.ID) })
	return out
}

func (p Provider) oauth(scopes []string) *oauth2.Config {
	style := oauth2.AuthStyleInHeader
	if p.ClientAuthMethod == "client_secret_post" {
		style = oauth2.AuthStyleInParams
	}
	return &oauth2.Config{ClientID: p.ClientID, ClientSecret: p.ClientSecret, RedirectURL: p.RedirectURI, Scopes: scopes, Endpoint: oauth2.Endpoint{AuthURL: p.AuthorizationURL, TokenURL: p.TokenURL, AuthStyle: style}}
}

func fingerprint(value any) string { data, _ := json.Marshal(value); return digest(string(data)) }
func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func randomToken() string {
	var value [32]byte
	_, _ = rand.Read(value[:])
	return base64.RawURLEncoding.EncodeToString(value[:])
}
func (m *Manager) seal(value any, purpose string) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(m.aead.Seal(nonce, nonce, data, []byte(purpose))), nil
}
func (m *Manager) open(value, purpose string, target any) error {
	data, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil || len(data) < m.aead.NonceSize() {
		return fmt.Errorf("invalid encrypted connection data")
	}
	nonce := data[:m.aead.NonceSize()]
	data, err = m.aead.Open(nil, nonce, data[m.aead.NonceSize():], []byte(purpose))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

type credentials struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

func (m *Manager) exchange(ctx context.Context, p Provider, code, verifier string, requested []string) (string, credentials, []string, error) {
	empty := credentials{}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, oauth2.HTTPClient, m.client)
	token, err := p.oauth(requested).Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil || token.AccessToken == "" || !strings.EqualFold(token.Type(), "Bearer") {
		return "", empty, nil, fmt.Errorf("provider token exchange failed")
	}
	// RFC 6749: an omitted scope means the requested scope, not an empty grant.
	granted := requested
	if value := token.Extra("scope"); value != nil {
		text, ok := value.(string)
		if !ok {
			return "", empty, nil, fmt.Errorf("invalid granted scopes")
		}
		granted = strings.Fields(text)
	}
	actual := []string{}
	for _, scope := range requested {
		if slices.Contains(granted, scope) {
			actual = append(actual, scope)
		}
	}
	for _, scope := range p.Scopes {
		if !slices.Contains(actual, scope) {
			return "", empty, nil, fmt.Errorf("required identity scope denied")
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.UserInfoURL, nil)
	if err != nil {
		return "", empty, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token.AccessToken)
	req.Header.Set("Accept", "application/json")
	response, err := m.client.Do(req)
	if err != nil {
		return "", empty, nil, fmt.Errorf("provider identity lookup failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", empty, nil, fmt.Errorf("provider identity lookup failed")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 {
		return "", empty, nil, fmt.Errorf("invalid provider identity")
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(data, &body); err != nil {
		return "", empty, nil, fmt.Errorf("invalid provider identity")
	}
	var subject string
	if err := json.Unmarshal(body[p.SubjectField], &subject); err != nil || strings.TrimSpace(subject) == "" || len(subject) > 1024 {
		return "", empty, nil, fmt.Errorf("invalid provider subject")
	}
	if raw, ok := body["iss"]; ok {
		var issuer string
		if json.Unmarshal(raw, &issuer) != nil || issuer != p.Issuer {
			return "", empty, nil, fmt.Errorf("invalid identity issuer")
		}
	}
	return subject, credentials{token.AccessToken, token.RefreshToken, token.Expiry}, actual, nil
}
