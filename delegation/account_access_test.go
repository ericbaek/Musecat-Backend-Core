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

// Bulk enumeration is intentionally absent from both external transports.
func TestExternalArcadeListIsNotAvailable(t *testing.T) {
	f := newFixture(t)
	at := f.accountToken()
	for _, method := range []string{"GET", "HEAD"} {
		for _, suffix := range []string{"", "?page=1&per_page=100", "/"} {
			r := f.req(method, "/developer/v1/arcades"+suffix, at, "", "", false)
			if r.Code != 404 && r.Code != 405 {
				t.Fatalf("external list returned %d", r.Code)
			}
			r = f.req(method, "/developer/v1/account/arcades"+suffix, at, "", "", false)
			if r.Code != 403 && r.Code != 404 {
				t.Fatalf("account list returned %d", r.Code)
			}
		}
	}
	// First-party discovery and bounded external nearby discovery remain available.
	f.access("", "/arcades", 200)
	f.access(at, "/developer/v1/arcades/nearby?lat=37.5&lon=127", 200)
}

func TestConnectedProfileUsesPublicVisibility(t *testing.T) {
	f := newFixture(t)
	save := func(collection string, fields map[string]any) *core.Record {
		c, err := f.app.FindCollectionByNameOrId(collection)
		if err != nil {
			t.Fatal(err)
		}
		r := core.NewRecord(c)
		for k, v := range fields {
			if k == "id" {
				r.Id = v.(string)
			} else {
				r.Set(k, v)
			}
		}
		if err := f.app.Save(r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	a := save("arcade", map[string]any{"public": true, "country": "KR"})
	b := save("arcade_basic", map[string]any{"arcade": a.Id, "name": "Visibility test", "address": "Test"})
	a.Set("basic", b.Id)
	if err := f.app.Save(a); err != nil {
		t.Fatal(err)
	}
	save("arcade_favorite", map[string]any{"user": f.user.Id, "arcade": a.Id})
	save("arcade_visit", map[string]any{"user": f.user.Id, "arcade": a.Id, "visit_day": "2026-01-01", "visited_at": "2026-01-01 10:00:00.000Z"})
	info := save("user_info", map[string]any{"id": f.user.Id, "favorite_visibility": "private", "visit_visibility": "private", "country_mode": "off"})
	// A connected staff account must not inherit the staff profile override.
	f.user.Set("tags", []string{"moderator"})
	if err := f.app.Save(f.user); err != nil {
		t.Fatal(err)
	}
	at := f.accountToken()
	for _, visibility := range []string{"private", "summary", "full"} {
		info.Set("visit_visibility", visibility)
		info.Set("favorite_visibility", map[bool]string{true: "public", false: "private"}[visibility == "full"])
		if err := f.app.Save(info); err != nil {
			t.Fatal(err)
		}
		public := f.access("", "/user?id="+f.user.Id, 200)
		for _, path := range []string{"/developer/v1/account/user/me", "/developer/v1/account/user?id=" + f.user.Id} {
			got := f.access(at, path, 200)
			for _, key := range []string{"favorite_arcades", "visit_stats", "series", "countries", "primary_country", "warp", "owns"} {
				x, _ := json.Marshal(got[key])
				y, _ := json.Marshal(public[key])
				if string(x) != string(y) {
					t.Fatalf("%s %s mismatch %s", visibility, path, key)
				}
			}
		}
		status := 200
		if visibility == "private" {
			status = 404
		}
		for _, path := range []string{"/user/passport", "/user/passport/stamps", "/user/visits"} {
			response := f.req("GET", "/developer/v1/account"+path, at, "", "", false)
			check(t, response, status)
			if visibility == "summary" && (strings.Contains(response.Body.String(), "2026-01-01") || strings.Contains(response.Body.String(), "visit_dates")) {
				t.Fatalf("summary exposed dates: %s", path)
			}
			f.access(f.token, path, 200)
		}
	}
	info.Set("favorite_visibility", "private")
	if err := f.app.Save(info); err != nil {
		t.Fatal(err)
	}
	response := check(t, f.req("PUT", "/developer/v1/account/user/profile", at, `{"nickname":"Visibility test","bio":"Public projection"}`, "application/json", false), 200)
	if response["favorite_arcades"] != nil {
		t.Fatal("profile mutation leaked private favorites")
	}
}
