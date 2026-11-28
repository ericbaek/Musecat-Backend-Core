package user_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
)

func TestCanonicalNativeFavoriteMatchesWebAndKeepsAccountScope(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	otherWeb, other := createAuthUser(t, app, true)
	native := nativeLoginIssue(t, app, web).AccessToken
	foreign := nativeLoginIssue(t, app, otherWeb).AccessToken
	arcade := nativeEditorArcade(t, app, owner.Id)
	for _, step := range []struct {
		token string
		state bool
	}{{native, true}, {web, true}, {foreign, true}, {native, false}, {web, false}} {
		// A supplied owner cannot redirect a favorite write to another account.
		body := map[string]any{"arcade": arcade.Id, "favorited": step.state, "user": other.Id}
		response := nativeReadRequest(t, app, "PUT", "/arcade/favorite", step.token, body, 200)
		var receipt map[string]any
		if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(receipt, map[string]any{"arcade": arcade.Id, "favorited": step.state}) {
			t.Fatal("favorite changed canonical receipt")
		}
	}
	rows, err := app.FindRecordsByFilter("arcade_favorite", "arcade={:arcade}", "", 0, 0, dbx.Params{"arcade": arcade.Id})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].GetString("user") != other.Id {
		t.Fatal("favorite retries or removal crossed account scope")
	}
	for _, body := range []map[string]any{{"arcade": arcade.Id}, {"arcade": "", "favorited": true}, {"arcade": arcade.Id, "favorited": "true"}} {
		nativeReadRequest(t, app, "PUT", "/arcade/favorite", native, body, 400)
	}
	arcade.Set("public", false)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{arcade.Id, "missingarcade00"} {
		nativeReadRequest(t, app, "PUT", "/arcade/favorite", native, map[string]any{"arcade": id, "favorited": true}, 404)
	}
}

func TestCanonicalNativeFavoriteRejectsInvalidInactiveAndRevokedSessions(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	credentials := nativeLoginIssue(t, app, web)
	arcade := nativeEditorArcade(t, app, owner.Id)
	body := map[string]any{"arcade": arcade.Id, "favorited": true}
	for _, token := range []string{"", strings.Repeat("z", 43)} {
		nativeReadRequest(t, app, "PUT", "/arcade/favorite", token, body, 401)
	}
	if _, err := app.DB().NewQuery("UPDATE native_auth_session SET client='removed-client' WHERE user_id={:user}").Bind(dbx.Params{"user": owner.Id}).Execute(); err != nil {
		t.Fatal(err)
	}
	nativeReadRequest(t, app, "PUT", "/arcade/favorite", credentials.AccessToken, body, 401)
	if _, err := app.DB().NewQuery("UPDATE native_auth_session SET client={:client} WHERE user_id={:user}").Bind(dbx.Params{"user": owner.Id, "client": nativeLoginClients[0].ID}).Execute(); err != nil {
		t.Fatal(err)
	}
	seedUserBanForUserTest(t, app, owner.Id, hashNormalizedEmailForUserTest(owner.Email()), "native_favorite_fixture", time.Now().Add(time.Hour))
	nativeReadRequest(t, app, "PUT", "/arcade/favorite", credentials.AccessToken, body, 403)
	nativeLoginRequest(t, app, "POST", "/native/session/revoke", "", map[string]string{"refresh_token": credentials.RefreshToken}, 204)
	nativeReadRequest(t, app, "PUT", "/arcade/favorite", credentials.AccessToken, body, 401)
	rows, err := app.FindAllRecords("arcade_favorite")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatal("denied favorite requests persisted changes")
	}
	disabled := nativeReadApp(t, false)
	disabledWeb, disabledOwner := createAuthUser(t, disabled, true)
	disabledArcade := nativeEditorArcade(t, disabled, disabledOwner.Id)
	disabledBody := map[string]any{"arcade": disabledArcade.Id, "favorited": true}
	nativeReadRequest(t, disabled, "PUT", "/arcade/favorite", disabledWeb, disabledBody, 200)
	response := doUserRequest(t, disabled, "PUT", "/arcade/favorite", map[string]string{"Authorization": "Bearer " + credentials.AccessToken}, `{}`)
	defer response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("disabled native client authorized favorite")
	}
}
