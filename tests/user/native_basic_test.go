package user_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/pocketbase/pocketbase/apis"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
)

func TestNativeBasicV1Compatibility(t *testing.T) {
	app := newUserFetchTestApp(t)
	web, u := createAuthUser(t, app, true)
	clients := []nativeauth.Client{{ID: "ios", Callback: "https://musecat.app/native/callback"}}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error { nativeauth.RegisterRoutes(e, clients); return e.Next() })
	// Use real HTTP bodies: PocketBase's rewind-on-EOF reader differs from
	// the strings.Reader used by direct handler tests.
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	event := &core.ServeEvent{App: app, Router: router}
	if err = app.OnServe().Trigger(event, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	verifier := strings.Repeat("n", 43)
	digest := sha256.Sum256([]byte(verifier))
	service := nativeauth.Service{Clients: clients}
	code, err := service.IssueCode(app, u, "ios", clients[0].Callback, base64.RawURLEncoding.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.Exchange(app, code, verifier, "ios", clients[0].Callback)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + credentials.AccessToken}
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	old, err := app.FindRecordById("arcade_basic", arcade.GetString("basic"))
	if err != nil {
		t.Fatal(err)
	}
	old.Set("nickname", []string{"Kept alias"})
	old.Set("subway_line", []string{"2"})
	old.Set("direction", "Kept directions")
	if err = app.Save(old); err != nil {
		t.Fatal(err)
	}
	path := "/native/v1/arcades/" + arcade.Id + "/basic"
	request := func(method, body string, status int) map[string]any {
		t.Helper()
		req, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		r, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		data, _ := io.ReadAll(r.Body)
		if r.StatusCode != status {
			t.Fatalf("%s: expected %d got %d %s", method, status, r.StatusCode, data)
		}
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return out
	}
	snapshot := request("GET", "", 200)
	if snapshot["revision"] != old.Id || snapshot["contract_version"] != float64(1) {
		t.Fatal(snapshot)
	}
	patch := func(revision, changes string) string {
		return `{"revision":"` + revision + `","changes":` + changes + `}`
	}
	for _, body := range []string{`{"changes":{"name":"x"}}`, patch(old.Id, `{"name":null}`), patch(old.Id, `{"future_field":"x"}`), patch(old.Id, `{"location":{"lat":1}}`), patch(old.Id, `{"name":" "}`), patch(old.Id, `{"nickname":"wrong"}`)} {
		request("PATCH", body, 400)
	}
	count := func(collection string) int64 {
		n, e := app.CountRecords(collection)
		if e != nil {
			t.Fatal(e)
		}
		return n
	}
	before := count("arcade_changelog")
	// A released older client knows only name: all other current server fields survive.
	saved := request("PATCH", patch(old.Id, `{"name":"Older client edit"}`), 200)
	current, err := app.FindRecordById("arcade_basic", saved["basic"].(string))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"nickname", "subway_line", "direction", "address", "location", "city_id"} {
		a, _ := json.Marshal(old.Get(field))
		b, _ := json.Marshal(current.Get(field))
		if string(a) != string(b) {
			t.Fatalf("omitted %s lost: %s -> %s", field, a, b)
		}
	}
	if count("arcade_changelog") != before+1 {
		t.Fatal("missing atomic history")
	}
	ledgerBefore := count("user_level_log")
	request("PATCH", patch(old.Id, `{"name":"Overwrite someone else"}`), 409)
	request("PATCH", patch(old.Id, `{"name":"Older client edit"}`), 409)
	if count("arcade_changelog") != before+1 || count("user_level_log") != ledgerBefore {
		t.Fatal("conflict mutated history or XP")
	}
	refreshed := request("GET", "", 200)
	request("PATCH", patch(refreshed["revision"].(string), `{"direction":"","nickname":[]}`), 200)
	// Web JWTs cannot use the native adapter.
	headers["Authorization"] = "Bearer " + web
	request("GET", "", 401)
	headers["Authorization"] = "Bearer " + credentials.AccessToken
	arcade, _ = app.FindRecordById("arcade", arcade.Id)
	arcade.Set("public", false)
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	request("GET", "", 403)
	request("PATCH", patch(refreshed["revision"].(string), `{"name":"Private overwrite"}`), 403)
}
