package user_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"mime/multipart"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/tests"
)

type nativeSettingOperation struct{ name, path, body, contentType string }

func nativeSettingsOperations(t *testing.T, foreign, series, arcade string) []nativeSettingOperation {
	t.Helper()
	var profile bytes.Buffer
	writer := multipart.NewWriter(&profile)
	for key, value := range map[string]string{"nickname": " Canonical settings ", "bio": "First\r\nSecond", "user": foreign} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return []nativeSettingOperation{
		{"profile", "/user/profile", profile.String(), writer.FormDataContentType()},
		{"countries", "/user/countries", fmt.Sprintf(`{"countries":[" kr "],"country_mode":"manual","user":%q}`, foreign), "application/json"},
		{"game-preferences", "/user/game-preferences", fmt.Sprintf(`{"series":[" %s ","",%q],"series_public":false,"warp":true,"user":%q}`, series, series, foreign), "application/json"},
		{"visit-visibility", "/user/visit-visibility", fmt.Sprintf(`{"visit_visibility":"summary","visit_timeline_hidden":true,"user":%q}`, foreign), "application/json"},
		{"favorite-visibility", "/user/favorite-visibility", fmt.Sprintf(`{"favorite_visibility":"public","user":%q}`, foreign), "application/json"},
		{"favorite-order", "/arcade/favorite/order", fmt.Sprintf(`{"arcades":[" %s ",""],"user":%q}`, arcade, foreign), "application/json"},
	}
}

func nativeSettingsRequest(t *testing.T, app *tests.TestApp, op nativeSettingOperation, token string, status int) *http.Response {
	t.Helper()
	headers := map[string]string{"Content-Type": op.contentType}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	response := doUserRequest(t, app, "PUT", op.path, headers, op.body)
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != status {
		t.Fatalf("%s status %d want %d", op.name, response.StatusCode, status)
	}
	if len(token) == 43 && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("native settings response is cacheable")
	}
	return response
}

func TestCanonicalNativeSettingsMatchWebMultipartAndOwnerScope(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	foreignWeb, foreign := createAuthUser(t, app, true)
	token := nativeLoginIssue(t, app, web).AccessToken
	series := seedGameSeries(t, app, 99, "Settings", "Settings", "Settings")
	first := nativeEditorArcade(t, app, owner.Id)
	second := nativeEditorArcade(t, app, owner.Id)
	for _, credential := range []string{web, foreignWeb} {
		for _, arcade := range []string{first.Id, second.Id} {
			nativeReadRequest(t, app, "PUT", "/arcade/favorite", credential, map[string]any{"arcade": arcade, "favorited": true}, 200)
		}
	}
	foreignBefore := canonicalReadJSON(t, app, "/user?id="+foreign.Id, foreignWeb)
	for _, op := range nativeSettingsOperations(t, foreign.Id, series.Id, second.Id) {
		t.Run(op.name, func(t *testing.T) {
			actual := decodeJSON(t, nativeSettingsRequest(t, app, op, token, 200))
			expected := decodeJSON(t, nativeSettingsRequest(t, app, op, web, 200))
			if !reflect.DeepEqual(actual, expected) {
				t.Fatal("native settings wire differs from web")
			}
			switch op.name {
			case "profile":
				if actual["id"] != owner.Id || actual["nickname"] != "Canonical settings" || actual["bio"] != "First\nSecond" {
					t.Fatal("profile normalization or owner changed")
				}
			case "countries":
				if !reflect.DeepEqual(actual, map[string]any{"countries": []any{"KR"}, "country_mode": "manual", "primary_country": "KR"}) {
					t.Fatal("country projection changed")
				}
			case "game-preferences":
				if !reflect.DeepEqual(actual, map[string]any{"success": true, "series": []any{series.Id}, "series_public": false, "warp": true}) {
					t.Fatal("preference normalization changed")
				}
			case "visit-visibility":
				if !reflect.DeepEqual(actual, map[string]any{"visit_visibility": "summary", "visit_timeline_hidden": true}) {
					t.Fatal("timeline preference changed")
				}
			case "favorite-visibility":
				if !reflect.DeepEqual(actual, map[string]any{"favorite_visibility": "public"}) {
					t.Fatal("favorite visibility changed")
				}
			case "favorite-order":
				if !reflect.DeepEqual(actual, map[string]any{"arcades": []any{second.Id}}) {
					t.Fatal("favorite order echo changed")
				}
			}
			exportCanonicalReadFixture(t, "settings-"+op.name, actual)
		})
	}
	if !reflect.DeepEqual(foreignBefore, canonicalReadJSON(t, app, "/user?id="+foreign.Id, foreignWeb)) {
		t.Fatal("settings write crossed owner boundary")
	}
	rows, err := app.FindRecordsByFilter("arcade_favorite", "user={:user}", "sort_order", 0, 0, dbx.Params{"user": owner.Id})
	if err != nil || len(rows) != 2 || rows[0].GetString("arcade") != second.Id || rows[1].GetString("arcade") != first.Id {
		t.Fatal("omitted favorites were not appended", err)
	}
	for _, collection := range []string{"arcade_changelog", "user_level_log", "arcade_visit"} {
		if count, err := app.CountRecords(collection); err != nil || count != 0 {
			t.Fatal("settings changed history, XP or visits", err)
		}
	}
}

func TestCanonicalNativeSettingsKeepEligibilityValidationAndLimits(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	token := nativeLoginIssue(t, app, web).AccessToken
	// Both credential kinds keep ordinary-account level gates and invalid-input errors.
	for _, credential := range []string{token, web} {
		nativeReadRequest(t, app, "PUT", "/user/countries", credential, map[string]any{"countries": []string{"KR", "JP"}, "country_mode": "manual"}, 403)
		nativeReadRequest(t, app, "PUT", "/user/profile", credential, map[string]any{"nickname": "Canonical", "background_position": map[string]any{"x": 25, "y": 75}}, 403)
		for path, body := range map[string]any{
			"/user/profile":             map[string]any{"nickname": ""},
			"/user/countries":           map[string]any{"countries": []string{"KR", "kr"}, "country_mode": "manual"},
			"/user/game-preferences":    map[string]any{"series": []string{"missing"}, "series_public": true},
			"/user/visit-visibility":    map[string]any{"visit_visibility": "invalid"},
			"/user/favorite-visibility": map[string]any{"favorite_visibility": "invalid"},
			"/arcade/favorite/order":    map[string]any{"arcades": []string{"notfavorited", "notfavorited"}},
		} {
			nativeReadRequest(t, app, "PUT", path, credential, body, 400)
		}
		for _, limit := range []struct {
			path string
			size int
		}{{"/user/profile", 35 << 20}, {"/user/game-preferences", 128 << 10}} {
			response := doUserRequest(t, app, "PUT", limit.path, map[string]string{"Authorization": "Bearer " + credential}, strings.Repeat(" ", limit.size+1))
			response.Body.Close()
			if response.StatusCode != 413 {
				t.Fatalf("%s body limit status %d", limit.path, response.StatusCode)
			}
		}
	}
	// Profile requests above the default 32 MiB limit must still reach the
	// existing 35 MiB route validator; game preferences accept their full bound.
	profileBody := `{"nickname":"","padding":"` + strings.Repeat("x", 32<<20) + `"}`
	response := doUserRequest(t, app, "PUT", "/user/profile", map[string]string{"Authorization": "Bearer " + token}, profileBody)
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("profile lost the 35 MiB override")
	}
	preferences := `{"series":[],"series_public":true}`
	response = doUserRequest(t, app, "PUT", "/user/game-preferences", map[string]string{"Authorization": "Bearer " + token}, preferences+strings.Repeat(" ", (128<<10)-len(preferences)))
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("preferences rejected its supported body bound")
	}
	setUserLevelForCountriesTest(t, app, owner.Id, userhandler.LevelBaseExp(15))
	nativeReadRequest(t, app, "PUT", "/user/countries", token, map[string]any{"countries": []string{"KR", "JP"}, "country_mode": "manual"}, 200)
	nativeReadRequest(t, app, "PUT", "/user/countries", token, map[string]any{"countries": []string{"KR", "JP"}, "country_mode": "off"}, 200)
	info, err := app.FindRecordById("user_info", owner.Id)
	if err != nil || !reflect.DeepEqual(info.GetStringSlice("countries"), []string{"KR", "JP"}) {
		t.Fatal("off mode destroyed stored country selection", err)
	}
	nativeReadRequest(t, app, "PUT", "/user/visit-visibility", token, map[string]any{"visit_visibility": "private", "visit_timeline_hidden": true}, 200)
	response = nativeReadRequest(t, app, "PUT", "/user/visit-visibility", token, map[string]any{"visit_visibility": "full"}, 200)
	if decodeJSON(t, response)["visit_timeline_hidden"] != true {
		t.Fatal("omission reset timeline preference")
	}
}

func TestCanonicalNativeSettingsRejectInvalidSessionsAndUnselectedCapabilities(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	credentials := nativeLoginIssue(t, app, web)
	ops := nativeSettingsOperations(t, "foreign", "missingseries", "missingarcade")
	reject := func(token string, status int) {
		t.Helper()
		for _, op := range ops {
			nativeSettingsRequest(t, app, op, token, status)
		}
	}
	reject("", 401)
	reject(strings.Repeat("z", 43), 401)
	for _, op := range ops {
		// Core receives web JWTs in Authorization, not browser cookies. An invalid
		// explicit bearer must not fall back to a cookie identity.
		for _, authorization := range []string{"", "Bearer " + strings.Repeat("z", 43), "Bearer malformed"} {
			response := doUserRequest(t, app, "PUT", op.path, map[string]string{"Cookie": "pb_auth=" + web, "Authorization": authorization, "Content-Type": op.contentType}, op.body)
			response.Body.Close()
			if response.StatusCode != 401 {
				t.Fatal("cookie or invalid bearer bypassed authentication")
			}
		}
	}
	for _, route := range []struct{ method, path string }{{"DELETE", "/user/notifications/device"}, {"PUT", "/moderation/arcade/edit-report"}, {"POST", "/user/notifications/device"}, {"POST", "/api/collections/user/auth-refresh"}} {
		response := doUserRequest(t, app, route.method, route.path, map[string]string{"Authorization": "Bearer " + credentials.AccessToken}, "{}")
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("settings authentication widened an unselected capability")
		}
	}
	for _, change := range []struct{ deny, restore string }{{"access_expires=0", "access_expires=unixepoch()+600"}, {"expires=0", "expires=unixepoch()+3600"}, {"client='removed-client'", "client='app.musecat.native.dev'"}} {
		for i, sql := range []string{change.deny, change.restore} {
			if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + sql + " WHERE user_id={:user}").Bind(dbx.Params{"user": owner.Id}).Execute(); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				reject(credentials.AccessToken, 401)
			}
		}
	}
	owner.Set("withdrawn", true)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	reject(credentials.AccessToken, 401)
	owner.Set("withdrawn", false)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	seedUserBanForUserTest(t, app, owner.Id, hashNormalizedEmailForUserTest(owner.Email()), "native_settings_fixture", time.Now().Add(time.Hour))
	reject(credentials.AccessToken, 403)
	reject(web, 403)
	nativeLoginRequest(t, app, "POST", "/native/session/revoke", "", map[string]string{"refresh_token": credentials.RefreshToken}, 204)
	reject(credentials.AccessToken, 401)
	disabled := nativeReadApp(t, false)
	disabledWeb, disabledOwner := createAuthUser(t, disabled, true)
	if err := nativeauth.InstallSchema(disabled); err != nil {
		t.Fatal(err)
	}
	service := nativeauth.Service{Clients: nativeLoginClients}
	verifier := strings.Repeat("n", 43)
	challenge := sha256.Sum256([]byte(verifier))
	code, err := service.IssueCode(disabled, disabledOwner, nativeLoginClients[0].ID, nativeLoginClients[0].Callback, base64.RawURLEncoding.EncodeToString(challenge[:]))
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Exchange(disabled, code, verifier, nativeLoginClients[0].ID, nativeLoginClients[0].Callback)
	if err != nil {
		t.Fatal(err)
	}
	if actor, err := service.Authenticate(disabled, issued.AccessToken); err != nil || actor.Id != disabledOwner.Id {
		t.Fatal("disabled binding fixture must contain a valid same-database session", err)
	}
	nativeSettingsRequest(t, disabled, ops[0], disabledWeb, 200)
	for _, op := range ops {
		response := doUserRequest(t, disabled, "PUT", op.path, map[string]string{"Authorization": "Bearer " + issued.AccessToken, "Content-Type": op.contentType}, op.body)
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("disabled native binding authorized settings")
		}
	}

	info, err := app.FindRecordById("user_info", owner.Id)
	if err != nil || info.GetString("nickname") == "Canonical settings" {
		t.Fatal("denied settings request persisted changes", err)
	}
}
