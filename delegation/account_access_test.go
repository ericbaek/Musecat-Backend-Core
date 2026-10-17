package delegation_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/pocketbase/pocketbase/core"
)

func (f *fixture) accountToken() string {
	f.t.Helper()
	// Missing scope defaults to the sole registered account connection permission.
	handle := f.begin("accountapp", callback, "")
	consent := f.read(handle, f.token, 200)
	if len(consent["requested_scopes"].([]any)) != 1 {
		f.t.Fatal("not a single connection grant")
	}
	target, err := url.Parse(f.decision(handle, f.token, true, []string{delegation.AccountAccess}, 200))
	if err != nil {
		f.t.Fatal(err)
	}
	out, err := f.m.Exchange(f.app, "accountapp", delegation.TokenInput{GrantType: "authorization_code", Code: target.Query().Get("code"), RedirectURI: callback, Verifier: verifier})
	if err != nil {
		f.t.Fatal(err)
	}
	if out.Scope != delegation.AccountAccess {
		f.t.Fatal("unexpected granted scope")
	}
	return out.AccessToken
}

func TestAccountConnectionReusesUserIdentityAndDomainWrites(t *testing.T) {
	f := newFixture(t)
	infoCollection, _ := f.app.FindCollectionByNameOrId("user_info")
	info := core.NewRecord(infoCollection)
	info.Id = f.user.Id
	if err := f.app.Save(info); err != nil {
		t.Fatal(err)
	}
	at := f.accountToken()
	me := f.access(at, "/developer/v1/account/user/me", 200)
	if me["id"] != f.user.Id {
		t.Fatal("wrong account actor")
	}
	check(t, f.req("PUT", "/developer/v1/account/user/visit-visibility", at, `{"visit_visibility":"full"}`, "application/json", false), 200)
	saved, _ := f.app.FindRecordById("user_info", f.user.Id)
	if saved.GetString("visit_visibility") != "full" {
		t.Fatal("account operation not persisted")
	}
	// A delegated token never becomes a first-party login session.
	f.access(at, "/user/me", 401)
	f.access(f.token, "/developer/v1/account/user/me", 401)
	narrow := f.exchange(f.authorize("profile:read"), verifier, 200)
	f.access(narrow["access_token"].(string), "/developer/v1/account/user/me", 403)
	if err := delegation.RevokeGrant(f.app, f.user.Id, firstAccountGrant(t, f)); err != nil {
		t.Fatal(err)
	}
	f.access(at, "/developer/v1/account/user/me", 401)
}
func firstAccountGrant(t *testing.T, f *fixture) string {
	rows, err := f.m.ListGrants(f.app, f.user.Id)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range rows {
		if g.ClientID == "accountapp" {
			return g.ID
		}
	}
	t.Fatal("account grant missing")
	return ""
}
func TestAccountConnectionCannotUseManagementEvenAsStaff(t *testing.T) {
	f := newFixture(t)
	f.user.Set("tags", []string{"moderator"})
	if err := f.app.Save(f.user); err != nil {
		t.Fatal(err)
	}
	at := f.accountToken()
	for _, op := range []string{"GET /moderation/user/bans", "POST /moderation/user/ban", "PUT /moderation/arcade/status", "POST /campaign", "PUT /campaign", "POST /campaign/end", "POST /subway/map", "DELETE /subway/map", "GET /subway/map/history", "POST /arcade/game/bulk_version", "GET /arcade/games", "GET /support_feedback", "PUT /support_feedback"} {
		method, path, _ := strings.Cut(op, " ")
		if delegation.AccountRouteAllowed(method, path) {
			t.Fatalf("management is delegated: %s", op)
		}
		check(t, f.req(method, "/developer/v1/account"+path, at, "{}", "application/json", false), 403)
	}
	for _, path := range []string{"/user/withdraw", "/user/signup", "/user/arcade-account/credentials", "/user/authorizations", "/user/connections/start", "/oauth/consent", "/api/collections/user/records", "/future/admin"} {
		if delegation.AccountRouteAllowed("POST", path) || delegation.AccountRouteAllowed("PUT", path) || delegation.AccountRouteAllowed("GET", path) {
			t.Fatal("security path delegated", path)
		}
		r := f.req("POST", "/developer/v1/account"+path, at, "{}", "application/json", false)
		if r.Code != 403 && r.Code != 404 && r.Code != 405 {
			t.Fatalf("security path %s returned %d", path, r.Code)
		}
	}
}
func TestAccountConnectionPreservesPrivateArcadeAndMultipartPolicy(t *testing.T) {
	f := newFixture(t)
	at := f.accountToken()
	_, other := f.newUser()
	collection, _ := f.app.FindCollectionByNameOrId("arcade")
	own := core.NewRecord(collection)
	own.Set("country", "KR")
	own.Set("public", false)
	own.Set("createdBy", f.user.Id)
	if err := f.app.Save(own); err != nil {
		t.Fatal(err)
	}
	foreign := core.NewRecord(collection)
	foreign.Set("country", "KR")
	foreign.Set("public", false)
	foreign.Set("createdBy", other.Id)
	if err := f.app.Save(foreign); err != nil {
		t.Fatal(err)
	}
	f.access(at, "/developer/v1/account/arcade?id="+foreign.Id, 404)
	f.access(at, "/developer/v1/account/arcade/draft?id="+foreign.Id, 404)
	for _, tc := range []struct {
		id     string
		status int
	}{{own.Id, 200}, {foreign.Id, 403}} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("arcade", tc.id)
		file, err := writer.CreateFormFile("photos", "test.png")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0, 0, 0, 0x0D, 0x49, 0x48, 0x44, 0x52, 0, 0, 0, 1})
		_ = writer.Close()
		req := httptest.NewRequest("POST", "/developer/v1/account/arcade/photo/upload", &body)
		req.Header.Set("Authorization", "Bearer "+at)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		rec := httptest.NewRecorder()
		f.mux.ServeHTTP(rec, req)
		out := check(t, rec, tc.status)
		if tc.status == 200 {
			summary := out["summary"].(map[string]any)
			if summary["success"] != float64(1) {
				encoded, _ := json.Marshal(out)
				t.Fatalf("upload failed: %s", encoded)
			}
		}
	}
}
