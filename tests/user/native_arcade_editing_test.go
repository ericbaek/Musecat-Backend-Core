package user_test

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func nativeEditorArcade(t *testing.T, app *tests.TestApp, owner string) *core.Record {
	t.Helper()
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	arcade.Set("createdBy", owner)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	return arcade
}

func editorState(t *testing.T, app *tests.TestApp, arcadeID, ownerID, section string) map[string]any {
	t.Helper()
	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := app.FindRecordById("user_level", ownerID)
	if err != nil {
		t.Fatal(err)
	}
	state := map[string]any{"revision": arcade.GetString(section), "exp": owner.Get("exp")}
	for _, collection := range []string{"arcade_" + section, "arcade_changelog", "user_level_log"} {
		records, err := app.FindAllRecords(collection)
		if err != nil {
			t.Fatal(err)
		}
		state[collection] = len(records)
	}
	return state
}

func TestCanonicalArcadeSectionsRejectStaleNativeAndWebWritesAtomically(t *testing.T) {
	for _, section := range []string{"sns", "hour", "gtk"} {
		t.Run(section, func(t *testing.T) {
			app := nativeReadApp(t, true)
			web, owner := createAuthUser(t, app, true)
			native := nativeLoginIssue(t, app, web).AccessToken
			arcade := nativeEditorArcade(t, app, owner.Id)
			body := map[string]any{"arcade": arcade.Id, "base_" + section + "_id": ""}
			switch section {
			case "sns":
				body[section] = []any{map[string]any{"type": "website", "link": "https://example.com"}}
			case "gtk":
				body[section] = []any{map[string]any{"type": "ATM", "bool": true}}
			case "hour":
				body["Monday"] = map[string]any{"start": 1000, "end": 2200}
				body["note"] = "Initial hours"
			}
			path := "/arcade/" + section
			nativeReadRequest(t, app, "PUT", path, native, body, http.StatusOK)
			first := editorState(t, app, arcade.Id, owner.Id, section)
			nativeReadRequest(t, app, "PUT", path, web, body, http.StatusConflict)
			if !reflect.DeepEqual(first, editorState(t, app, arcade.Id, owner.Id, section)) {
				t.Fatal("stale web write changed revision, history or XP")
			}
			body["base_"+section+"_id"] = first["revision"]
			nativeReadRequest(t, app, "PUT", path, web, body, http.StatusOK)
			second := editorState(t, app, arcade.Id, owner.Id, section)
			nativeReadRequest(t, app, "PUT", path, native, body, http.StatusConflict)
			if !reflect.DeepEqual(second, editorState(t, app, arcade.Id, owner.Id, section)) {
				t.Fatal("stale native write changed revision, history or XP")
			}
			// Omission remains backwards compatible; both updated clients send a base.
			delete(body, "base_"+section+"_id")
			nativeReadRequest(t, app, "PUT", path, web, body, http.StatusOK)
		})
	}
}

func TestCanonicalArcadeNativeReadAndEditingAuthorization(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	otherWeb, _ := createAuthUser(t, app, true)
	native := nativeLoginIssue(t, app, web).AccessToken
	other := nativeLoginIssue(t, app, otherWeb).AccessToken
	arcade := nativeEditorArcade(t, app, owner.Id)
	path := "/arcade?id=" + arcade.Id + "&expand=basic,hour,sns,gtk,game"
	read := func(token string) map[string]any {
		r := nativeReadRequest(t, app, "GET", path, token, nil, 200)
		var result map[string]any
		if err := json.NewDecoder(r.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	if !reflect.DeepEqual(read(native), read(web)) {
		t.Fatal("native read changed canonical detail wire")
	}
	nativeReadRequest(t, app, "GET", path, "", nil, 200)
	for _, token := range []string{"", strings.Repeat("z", 43)} {
		for _, section := range []string{"basic", "sns", "hour", "gtk", "game"} {
			nativeReadRequest(t, app, "PUT", "/arcade/"+section, token, map[string]any{"arcade": arcade.Id}, 401)
		}
	}
	// Private reads remain isolated to the existing draft surface.
	arcade.Set("public", false)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	nativeReadRequest(t, app, "GET", path, native, nil, 404)
	draft := "/arcade/draft?id=" + arcade.Id + "&expand=basic,hour,sns,gtk,game"
	nativeReadRequest(t, app, "GET", draft, native, nil, 200)
	nativeReadRequest(t, app, "GET", draft, other, nil, 404)
	for _, token := range []string{native, web} {
		nativeReadRequest(t, app, "PUT", "/arcade/basic", token, map[string]any{"arcade": arcade.Id, "base_basic_id": "stale", "name": "Changed name"}, 409)
	}
	for _, section := range []string{"basic", "sns", "hour", "gtk"} {
		body := map[string]any{"arcade": arcade.Id}
		if section == "sns" || section == "gtk" {
			body[section] = []any{}
		}
		nativeReadRequest(t, app, "PUT", "/arcade/"+section, other, body, 403)
	}
	// Domain adapters are deliberately not aliases and do not authorize raw REST.
	nativeReadRequest(t, app, "GET", "/native/arcade?id="+arcade.Id, native, nil, 404)
	r := doUserRequest(t, app, "PUT", "/arcade/report-settings", map[string]string{"Authorization": "Bearer " + native}, `{}`)
	defer r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("native credentials authorized an unselected operation")
	}
}

func TestCanonicalArcadeNativeSessionsRespectBanRevocationAndConfiguration(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	credentials := nativeLoginIssue(t, app, web)
	arcade := nativeEditorArcade(t, app, owner.Id)
	paths := []string{"/arcade?id=" + arcade.Id, "/arcade/draft?id=" + arcade.Id}
	seedUserBanForUserTest(t, app, owner.Id, hashNormalizedEmailForUserTest(owner.Email()), "native_editor_fixture", time.Now().Add(time.Hour))
	for _, path := range paths {
		nativeReadRequest(t, app, "GET", path, credentials.AccessToken, nil, 403)
	}
	for _, section := range []string{"basic", "sns", "hour", "gtk", "game"} {
		nativeReadRequest(t, app, "PUT", "/arcade/"+section, credentials.AccessToken, map[string]any{}, 403)
	}
	nativeLoginRequest(t, app, "POST", "/native/session/revoke", "", map[string]string{"refresh_token": credentials.RefreshToken}, 204)
	for _, path := range paths {
		nativeReadRequest(t, app, "GET", path, credentials.AccessToken, nil, 401)
	}
	for _, section := range []string{"basic", "sns", "hour", "gtk", "game"} {
		nativeReadRequest(t, app, "PUT", "/arcade/"+section, credentials.AccessToken, map[string]any{}, 401)
	}
	disabled := nativeReadApp(t, false)
	disabledWeb, disabledOwner := createAuthUser(t, disabled, true)
	disabledArcade := nativeEditorArcade(t, disabled, disabledOwner.Id)
	nativeReadRequest(t, disabled, "GET", "/arcade?id="+disabledArcade.Id, disabledWeb, nil, 200)
	for _, section := range []string{"basic", "sns", "hour", "gtk", "game"} {
		r := doUserRequest(t, disabled, "PUT", "/arcade/"+section, map[string]string{"Authorization": "Bearer " + strings.Repeat("a", 43)}, `{}`)
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatalf("disabled native editing %s status %d", section, r.StatusCode)
		}
	}
}
