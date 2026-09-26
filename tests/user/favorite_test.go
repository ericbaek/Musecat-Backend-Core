package user_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
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

func TestArcadeFavoriteOrder(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, _ := createAuthUser(t, app, true)
	headers := map[string]string{"Authorization": "Bearer " + token}

	arcade1 := seedVisitArcade(t, app, "Asia/Seoul")
	arcade2 := seedVisitArcade(t, app, "Asia/Seoul")
	arcade3 := seedVisitArcade(t, app, "Asia/Seoul")

	// Favorite them in order: 1, 2, 3
	for _, arc := range []*core.Record{arcade1, arcade2, arcade3} {
		res := doUserRequest(t, app, http.MethodPut, "/arcade/favorite", headers, `{"arcade":"`+arc.Id+`","favorited":true}`)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("add favorite %s status = %d, want %d", arc.Id, res.StatusCode, http.StatusOK)
		}
	}

	// Verify order on profile is [1, 2, 3]
	res := doUserRequest(t, app, http.MethodGet, "/user/me", headers, "")
	myProfile := decodeJSON(t, res)
	gotIDs := profileFavoriteArcadeIDs(t, myProfile)
	if len(gotIDs) != 3 || gotIDs[0] != arcade1.Id || gotIDs[1] != arcade2.Id || gotIDs[2] != arcade3.Id {
		t.Fatalf("initial favorite order = %#v, want [%s, %s, %s]", gotIDs, arcade1.Id, arcade2.Id, arcade3.Id)
	}

	// Reorder to [3, 1, 2]
	reorderBody := fmt.Sprintf(`{"arcades":["%s","%s","%s"]}`, arcade3.Id, arcade1.Id, arcade2.Id)
	res = doUserRequest(t, app, http.MethodPut, "/arcade/favorite/order", headers, reorderBody)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("reorder status = %d, want %d", res.StatusCode, http.StatusOK)
	}

	// Verify updated order on profile
	res = doUserRequest(t, app, http.MethodGet, "/user/me", headers, "")
	myProfile = decodeJSON(t, res)
	gotIDs = profileFavoriteArcadeIDs(t, myProfile)
	if len(gotIDs) != 3 || gotIDs[0] != arcade3.Id || gotIDs[1] != arcade1.Id || gotIDs[2] != arcade2.Id {
		t.Fatalf("reordered favorite order = %#v, want [%s, %s, %s]", gotIDs, arcade3.Id, arcade1.Id, arcade2.Id)
	}

	// Reorder with a duplicate arcade should return 400
	for _, body := range []string{`{}`, `{"arcades":null}`, `{"arcades":[3]}`} {
		res = doUserRequest(t, app, http.MethodPut, "/arcade/favorite/order", headers, body)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid body %s status = %d", body, res.StatusCode)
		}
	}
	res = doUserRequest(t, app, http.MethodPut, "/arcade/favorite/order", nil, reorderBody)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous reorder status = %d", res.StatusCode)
	}

	dupBody := fmt.Sprintf(`{"arcades":["%s","%s"]}`, arcade1.Id, arcade1.Id)
	res = doUserRequest(t, app, http.MethodPut, "/arcade/favorite/order", headers, dupBody)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate arcade reorder status = %d, want %d", res.StatusCode, http.StatusBadRequest)
	}

	// Reorder with an unfavorited arcade should return 400
	unfavArcade := seedVisitArcade(t, app, "Asia/Seoul")
	unfavBody := fmt.Sprintf(`{"arcades":["%s"]}`, unfavArcade.Id)
	res = doUserRequest(t, app, http.MethodPut, "/arcade/favorite/order", headers, unfavBody)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("unfavorited arcade reorder status = %d, want %d", res.StatusCode, http.StatusBadRequest)
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
