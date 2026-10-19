package delegation_test

import (
	"github.com/ericbaek/musecat-backend-core/delegation"
	"net/url"
	"testing"
)

func TestDevelopmentAudienceConsentTokensAndTesterRemoval(t *testing.T) {
	f := newFixture(t)
	foreignToken, foreign := f.newUser()
	client := delegation.Client{ID: "audience_app", Name: "Dev", Secret: secret, RedirectURIs: []string{callback}, Scopes: allScopes, Development: true, AllowedUserIDs: []string{f.user.Id}}
	if err := f.m.Reload(f.app, []delegation.Client{client}); err != nil {
		t.Fatal(err)
	}
	h := f.begin("audience_app", callback, "profile:read")
	f.read(h, foreignToken, 403)
	f.decision(h, foreignToken, true, []string{"profile:read"}, 403)
	h = f.begin("audience_app", callback, "profile:read")
	f.read(h, f.token, 200)
	target, _ := url.Parse(f.decision(h, f.token, true, []string{"profile:read"}, 200))
	token, err := f.m.Exchange(f.app, "audience_app", delegation.TokenInput{GrantType: "authorization_code", Code: target.Query().Get("code"), RedirectURI: callback, Verifier: verifier})
	if err != nil {
		t.Fatal(err)
	}
	f.access(token.AccessToken, "/developer/v1/me", 200)
	client.AllowedUserIDs = []string{foreign.Id}
	if err := f.m.Reload(f.app, []delegation.Client{client}); err != nil {
		t.Fatal(err)
	}
	f.access(token.AccessToken, "/developer/v1/me", 401)
	if _, err := f.m.Exchange(f.app, "audience_app", delegation.TokenInput{GrantType: "refresh_token", RefreshToken: token.RefreshToken}); err == nil {
		t.Fatal("removed tester refreshed")
	}
	client.AllowedUserIDs = []string{f.user.Id}
	if err := f.m.Reload(f.app, []delegation.Client{client}); err != nil {
		t.Fatal(err)
	}
	f.access(token.AccessToken, "/developer/v1/me", 401)
}
