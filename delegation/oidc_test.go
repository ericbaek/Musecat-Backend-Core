package delegation_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/golang-jwt/jwt/v5"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

func oidcKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return key, base64.StdEncoding.EncodeToString(der)
}
func setupOIDC(t *testing.T, f *fixture) (*rsa.PrivateKey, string) {
	key, encoded := oidcKey(t)
	raw, _ := json.Marshal([]string{encoded})
	if err := f.m.ConfigureOIDC(string(raw)); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Reload(f.app, []delegation.Client{{ID: "signin", Name: "Partner sign-in", Secret: secret, RedirectURIs: []string{callback}, Scopes: []string{delegation.OpenID, delegation.Profile, "arcade:read"}}}); err != nil {
		t.Fatal(err)
	}
	return key, encoded
}
func signinBegin(f *fixture, scopes, nonce string) *httptest.ResponseRecorder {
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"client_id": {"signin"}, "redirect_uri": {callback}, "response_type": {"code"}, "response_mode": {"query"}, "scope": {scopes}, "state": {"bound-state"}, "nonce": {nonce}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	return f.req("GET", "/oauth/authorize?"+q.Encode(), "", "", "", false)
}
func signinCode(t *testing.T, f *fixture, scopes []string) string {
	t.Helper()
	response := signinBegin(f, strings.Join(scopes, " "), "nonce-bound-to-partner-session")
	check(t, response, 302)
	u, _ := url.Parse(response.Header().Get("Location"))
	h := u.Query().Get("request")
	if h == "" {
		t.Fatal("OIDC request rejected", u)
	}
	f.read(h, f.token, 200)
	u, _ = url.Parse(f.decision(h, f.token, true, scopes, 200))
	return u.Query().Get("code")
}
func TestOIDCLoginAndReadonlyResources(t *testing.T) {
	f := newFixture(t)
	key, _ := setupOIDC(t, f)
	metadata := check(t, f.req("GET", "/.well-known/openid-configuration", "", "", "", false), 200)
	if metadata["issuer"] != "https://api.musecat.example" || metadata["jwks_uri"] != "https://api.musecat.example/oauth/jwks" {
		t.Fatal(metadata)
	}
	jwks := check(t, f.req("GET", "/oauth/jwks", "", "", "", false), 200)
	public, _ := json.Marshal(jwks)
	if strings.Contains(string(public), `"d"`) || strings.Contains(string(public), "PRIVATE") {
		t.Fatal("private key exposed")
	}
	code := signinCode(t, f, []string{delegation.OpenID, delegation.Profile, "arcade:read"})
	input := delegation.TokenInput{GrantType: "authorization_code", Code: code, RedirectURI: callback, Verifier: verifier}
	if _, err := f.m.Exchange(f.app, "chuniinfo", input); err == nil {
		t.Fatal("another client redeemed code")
	}
	bad := input
	bad.Verifier = strings.Repeat("x", 43)
	if _, err := f.m.Exchange(f.app, "signin", bad); err == nil {
		t.Fatal("wrong PKCE accepted")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {callback}, "code_verifier": {verifier}}
	request := httptest.NewRequest("POST", "/oauth/token", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth("signin", secret)
	response := httptest.NewRecorder()
	f.mux.ServeHTTP(response, request)
	token := check(t, response, 200)
	idToken := token["id_token"].(string)
	parsed, err := jwt.Parse(idToken, func(tok *jwt.Token) (any, error) { return &key.PublicKey, nil }, jwt.WithValidMethods([]string{"RS256"}), jwt.WithIssuer("https://api.musecat.example"), jwt.WithAudience("signin"), jwt.WithExpirationRequired())
	if err != nil {
		t.Fatal(err)
	}
	claims := parsed.Claims.(jwt.MapClaims)
	if claims["nonce"] != "nonce-bound-to-partner-session" || parsed.Header["kid"] == nil {
		t.Fatal(claims)
	}
	if _, err = jwt.Parse(idToken, func(*jwt.Token) (any, error) { return &key.PublicKey, nil }, jwt.WithAudience("other")); err == nil {
		t.Fatal("wrong audience accepted")
	}
	at := token["access_token"].(string)
	grants, err := f.m.ListGrants(f.app, f.user.Id)
	if err != nil || len(grants) != 1 {
		t.Fatal(grants, err)
	}
	grant, _ := f.app.FindRecordById(delegation.Grants, grants[0].ID)
	if err := f.m.VerifyLoginGrant(f.app, f.user.Id, "signin", grant.Id, grant.GetString("generation")); err != nil {
		t.Fatal("login conformance evidence rejected", err)
	}
	info := f.access(at, "/oauth/userinfo", 200)
	if info["sub"] != claims["sub"] || info["preferred_username"] == nil || info["email"] != nil {
		t.Fatal(info, claims)
	}
	f.access(at, "/developer/v1/me", 200)
	f.access(at, "/developer/v1/arcades/nearby?lat=37.5&lon=127", 200)
	check(t, f.req("PUT", "/developer/v1/account/user/profile", at, `{}`, "application/json", false), 403)
	f.access(idToken, "/developer/v1/me", 401)
	// Refresh remains rotating, grants no new scope and need not return another ID token.
	next, err := f.m.Exchange(f.app, "signin", delegation.TokenInput{GrantType: "refresh_token", RefreshToken: token["refresh_token"].(string)})
	if err != nil || next.IDToken != "" || next.Scope != "arcade:read openid profile" {
		t.Fatal(next, err)
	}
	if err := f.m.RevokeToken(f.app, "signin", next.AccessToken); err != nil {
		t.Fatal(err)
	}
	f.access(at, "/oauth/userinfo", 401)
	if _, err := f.m.Exchange(f.app, "signin", input); err == nil {
		t.Fatal("code replay accepted")
	}
}
func TestOIDCExplicitScopeNonceAndKeyRotation(t *testing.T) {
	f := newFixture(t)
	check(t, f.req("GET", "/.well-known/openid-configuration", "", "", "", false), 503)
	_, first := setupOIDC(t, f)
	for _, nonce := range []string{"", "short", strings.Repeat("n", 513)} {
		response := signinBegin(f, "openid profile arcade:read", nonce)
		check(t, response, 302)
		u, _ := url.Parse(response.Header().Get("Location"))
		if u.Query().Get("error") != "invalid_request" {
			t.Fatal("bad nonce accepted", u)
		}
	}
	code := signinCode(t, f, []string{delegation.OpenID})
	tokens, err := f.m.Exchange(f.app, "signin", delegation.TokenInput{GrantType: "authorization_code", Code: code, RedirectURI: callback, Verifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	info := f.access(tokens.AccessToken, "/oauth/userinfo", 200)
	if len(info) != 1 {
		t.Fatal("profile leaked without profile scope", info)
	}
	f.access(tokens.AccessToken, "/developer/v1/arcades/nearby?lat=37.5&lon=127", 403)
	f.access(tokens.AccessToken, "/developer/v1/me", 403)
	// Existing API delegation does not implicitly become an OpenID authentication grant.
	old := f.exchange(f.authorize("profile:read"), verifier, 200)
	if old["id_token"] != nil {
		t.Fatal("legacy grant issued ID token")
	}
	f.access(old["access_token"].(string), "/oauth/userinfo", 403)
	_, second := oidcKey(t)
	raw, _ := json.Marshal([]string{second, first})
	if err := f.m.ConfigureOIDC(string(raw)); err != nil {
		t.Fatal(err)
	}
	jwks, _ := f.m.JWKS()
	if len(jwks["keys"].([]map[string]string)) != 2 {
		t.Fatal("rotation dropped old key")
	}
	if err := f.m.ConfigureOIDC(`["invalid"]`); err == nil {
		t.Fatal("invalid key accepted")
	}
	// A failing configuration must leave the working key set intact.
	if !f.m.OIDCEnabled() {
		t.Fatal("invalid configuration removed working keys")
	}
}

func TestProfilePictureWithExactScopeAndConfiguredOrigin(t *testing.T) {
	f := newFixture(t)
	setupOIDC(t, f)
	c, err := f.app.FindCollectionByNameOrId("user_info")
	if err != nil {
		t.Fatal(err)
	}
	info := core.NewRecord(c)
	info.Id = f.user.Id
	info.Set("nickname", "Visible nickname")
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	file, err := filesystem.NewFileFromBytes(png, "avatar.png")
	if err != nil {
		t.Fatal(err)
	}
	info.Set("avatar", file)
	if err = f.app.Save(info); err != nil {
		t.Fatal(err)
	}
	code := signinCode(t, f, []string{delegation.OpenID, delegation.Profile})
	tokens, err := f.m.Exchange(f.app, "signin", delegation.TokenInput{GrantType: "authorization_code", Code: code, RedirectURI: callback, Verifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	response := f.access(tokens.AccessToken, "/oauth/userinfo", 200)
	picture, ok := response["picture"].(string)
	if !ok || !strings.HasPrefix(picture, "https://api.musecat.example/api/files/") || !strings.HasSuffix(picture, ".png") {
		t.Fatal("missing canonical picture", response)
	}
	me := f.access(tokens.AccessToken, "/developer/v1/me", 200)
	if me["picture"] != picture {
		t.Fatal("identity picture differs", me)
	}
	if response["email"] != nil || response["id"] != nil {
		t.Fatal("private identity leaked", response)
	}
	code = signinCode(t, f, []string{delegation.OpenID})
	tokens, err = f.m.Exchange(f.app, "signin", delegation.TokenInput{GrantType: "authorization_code", Code: code, RedirectURI: callback, Verifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	if response = f.access(tokens.AccessToken, "/oauth/userinfo", 200); len(response) != 1 {
		t.Fatal("picture without profile scope", response)
	}
	info.Set("avatar", nil)
	if err = f.app.Save(info); err != nil {
		t.Fatal(err)
	}
	if f.m.ProfilePicture(info) != "" {
		t.Fatal("missing avatar invented")
	}
}
