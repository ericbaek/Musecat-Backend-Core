package user_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestArcadeFavoriteToggleIsIdempotent(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, userRec := createAuthUser(t, app, true)
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	headers := map[string]string{"Authorization": "Bearer " + token}

	for _, want := range []struct {
		body  string
		count int
	}{
		{`{"arcade":"` + arcade.Id + `","favorited":true}`, 1},
		{`{"arcade":"` + arcade.Id + `","favorited":true}`, 1},
		{`{"arcade":"` + arcade.Id + `","favorited":false}`, 0},
		{`{"arcade":"` + arcade.Id + `","favorited":false}`, 0},
	} {
		res := doUserRequest(t, app, http.MethodPut, "/arcade/favorite", headers, want.body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("favorite status = %d, want %d", res.StatusCode, http.StatusOK)
		}
		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			t.Fatalf("decode favorite response: %v", err)
		}
		if payload["arcade"] != arcade.Id {
			t.Fatalf("favorite response arcade = %#v, want %q", payload["arcade"], arcade.Id)
		}
		if payload["favorited"] != (want.count == 1) {
			t.Fatalf("favorite response state = %#v, want %t", payload["favorited"], want.count == 1)
		}
		favorites, err := app.FindRecordsByFilter("arcade_favorite", "user={:user}", "", 0, 0, map[string]any{"user": userRec.Id})
		if err != nil {
			t.Fatalf("load favorites: %v", err)
		}
		if len(favorites) != want.count {
			t.Fatalf("favorite count = %d, want %d", len(favorites), want.count)
		}
	}

	res := doUserRequest(t, app, http.MethodPut, "/arcade/favorite", headers, `{"arcade":"`+arcade.Id+`"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing favorited status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}
}

func TestFavoriteVisibilityControlsProfileArcades(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, userRec := createAuthUser(t, app, true)
	userInfo := ensureUserInfo(t, app, userRec.Id)
	userInfo.Set("favorite_visibility", "private")
	if err := app.Save(userInfo); err != nil {
		t.Fatalf("save private favorite visibility: %v", err)
	}
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	headers := map[string]string{"Authorization": "Bearer " + token}

	res := doUserRequest(t, app, http.MethodPut, "/arcade/favorite", headers, `{"arcade":"`+arcade.Id+`","favorited":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("add favorite status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	publicURL := "/user?id=" + userRec.Id
	res = doUserRequest(t, app, http.MethodGet, publicURL, nil, "")
	publicProfile := decodeJSON(t, res)
	if got := publicProfile["favorite_visibility"]; got != "private" {
		t.Fatalf("public profile favorite_visibility = %#v, want private", got)
	}
	if _, exists := publicProfile["favorite_arcades"]; exists {
		t.Fatalf("private favorites leaked through public profile: %#v", publicProfile["favorite_arcades"])
	}

	res = doUserRequest(t, app, http.MethodGet, "/user/me", headers, "")
	myProfile := decodeJSON(t, res)
	if got := myProfile["favorite_visibility"]; got != "private" {
		t.Fatalf("my profile favorite_visibility = %#v, want private", got)
	}
	if got := profileFavoriteArcadeIDs(t, myProfile); len(got) != 1 || got[0] != arcade.Id {
		t.Fatalf("owner favorites = %#v, want [%s]", got, arcade.Id)
	}

	res = doUserRequest(t, app, http.MethodPut, "/user/favorite-visibility", headers, `{"favorite_visibility":"public"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("make favorites public status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	res = doUserRequest(t, app, http.MethodGet, publicURL, nil, "")
	publicProfile = decodeJSON(t, res)
	if got := publicProfile["favorite_visibility"]; got != "public" {
		t.Fatalf("public profile favorite_visibility = %#v, want public", got)
	}
	if got := profileFavoriteArcadeIDs(t, publicProfile); len(got) != 1 || got[0] != arcade.Id {
		t.Fatalf("public profile favorites = %#v, want [%s]", got, arcade.Id)
	}

	res = doUserRequest(t, app, http.MethodPut, "/user/favorite-visibility", headers, `{"favorite_visibility":"invalid"}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid favorite visibility status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}

	privateArcade := seedVisitArcade(t, app, "Asia/Seoul")
	privateArcade.Set("public", false)
	if err := app.Save(privateArcade); err != nil {
		t.Fatalf("make favorite target private: %v", err)
	}
	res = doUserRequest(t, app, http.MethodPut, "/arcade/favorite", headers, `{"arcade":"`+privateArcade.Id+`","favorited":true}`)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("private arcade favorite status = %d, want %d", res.StatusCode, http.StatusNotFound)
	}
}

func profileFavoriteArcadeIDs(tb testing.TB, profile map[string]any) []string {
	tb.Helper()
	items, ok := profile["favorite_arcades"].([]any)
	if !ok {
		tb.Fatalf("expected favorite_arcades list, got %#v", profile["favorite_arcades"])
	}
	ids := make([]string, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			tb.Fatalf("expected favorite arcade object, got %#v", raw)
		}
		id, ok := item["id"].(string)
		if !ok {
			tb.Fatalf("favorite arcade id = %#v, want string", item["id"])
		}
		ids = append(ids, id)
	}
	return ids
}
