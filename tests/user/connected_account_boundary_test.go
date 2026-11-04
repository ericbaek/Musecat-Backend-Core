package user_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

func TestConnectedAccountAudiencePreservesPublicVisibility(t *testing.T) {
	app := newUserFetchTestApp(t)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		public := func(handler func(*core.RequestEvent) error) func(*core.RequestEvent) error {
			return func(re *core.RequestEvent) error {
				userhandler.UsePublicProfileAudience(re)
				return handler(re)
			}
		}
		group := se.Router.Group("/connected").Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
		group.GET("/user", public(userhandler.GetUserByID))
		group.GET("/user/me", public(userhandler.GetMe))
		group.PUT("/user/profile", public(userhandler.UpdateProfile))
		group.GET("/user/passport", public(userhandler.GetMyPassport))
		group.GET("/user/passport/stamps", public(userhandler.GetMyPassportStamps))
		group.GET("/user/visits", public(userhandler.GetMyVisits))
		return se.Next()
	})
	token, account := createAuthUser(t, app, true)
	account.Set("tags", []string{"moderator"})
	if err := app.Save(account); err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	response := doUserRequest(t, app, http.MethodPut, "/arcade/favorite", headers, `{"arcade":"`+arcade.Id+`","favorited":true}`)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("favorite setup: %d", response.StatusCode)
	}
	response.Body.Close()
	collection, err := app.FindCollectionByNameOrId("arcade_visit")
	if err != nil {
		t.Fatal(err)
	}
	visit := core.NewRecord(collection)
	visit.Set("user", account.Id)
	visit.Set("arcade", arcade.Id)
	visit.Set("visit_day", "2026-01-01")
	visit.Set("visited_at", "2026-01-01 10:00:00.000Z")
	if err := app.Save(visit); err != nil {
		t.Fatal(err)
	}
	info := ensureUserInfo(t, app, account.Id)
	info.Set("favorite_visibility", "private")
	info.Set("country_mode", "off")
	for _, visibility := range []string{"private", "summary", "full"} {
		info.Set("visit_visibility", visibility)
		if err := app.Save(info); err != nil {
			t.Fatal(err)
		}
		ordinary := decodeJSON(t, doUserRequest(t, app, http.MethodGet, "/user?id="+account.Id, nil, ""))
		for _, path := range []string{"/connected/user/me", "/connected/user?id=" + account.Id} {
			response := doUserRequest(t, app, http.MethodGet, path, headers, "")
			if response.StatusCode != http.StatusOK {
				t.Fatalf("%s: %d", path, response.StatusCode)
			}
			got := decodeJSON(t, response)
			for _, key := range []string{"favorite_arcades", "visit_stats", "series", "countries", "primary_country", "warp", "owns"} {
				actual, _ := json.Marshal(got[key])
				expected, _ := json.Marshal(ordinary[key])
				if string(actual) != string(expected) {
					t.Fatalf("%s %s %s differs from public projection", visibility, path, key)
				}
			}
		}
		want := http.StatusOK
		if visibility == "private" {
			want = http.StatusNotFound
		}
		for _, path := range []string{"/user/passport", "/user/passport/stamps", "/user/visits"} {
			response := doUserRequest(t, app, http.MethodGet, "/connected"+path, headers, "")
			if response.StatusCode != want {
				t.Fatalf("%s %s: %d, want %d", visibility, path, response.StatusCode, want)
			}
			payload := decodeJSON(t, response)
			raw, _ := json.Marshal(payload)
			if visibility == "summary" && (strings.Contains(string(raw), "2026-01-01") || strings.Contains(string(raw), "visit_dates")) {
				t.Fatalf("%s exposed private visit dates", path)
			}
		}
	}
	owner := decodeJSON(t, doUserRequest(t, app, http.MethodGet, "/user/me", headers, ""))
	if len(profileFavoriteArcadeIDs(t, owner)) != 1 {
		t.Fatal("first-party owner lost private favorites")
	}
	updated := doUserRequest(t, app, http.MethodPut, "/connected/user/profile", headers, `{"nickname":"Connected name"}`)
	if updated.StatusCode != http.StatusOK {
		t.Fatalf("connected profile mutation: %d", updated.StatusCode)
	}
	profile := decodeJSON(t, updated)
	if profile["id"] != account.Id || profile["nickname"] != "Connected name" || profile["favorite_arcades"] != nil || profile["warp"] != nil {
		t.Fatalf("mutation lost actor identity or returned private owner fields: %#v", profile)
	}
}

func TestConnectedAccountRestrictionRecheck(t *testing.T) {
	app := newUserFetchTestApp(t)
	_, account := createAuthUser(t, app, true)
	assertStatus := func(err error, status int) {
		t.Helper()
		var apiError *router.ApiError
		if !errors.As(err, &apiError) || apiError.Status != status {
			t.Fatalf("got %v, want status %d", err, status)
		}
	}
	assertStatus(userhandler.CheckActiveUser(app, nil), http.StatusUnauthorized)
	if err := app.RunInTransaction(func(tx core.App) error {
		fresh, err := tx.FindRecordById("user", account.Id)
		if err != nil {
			return err
		}
		return userhandler.CheckActiveUser(tx, fresh)
	}); err != nil {
		t.Fatal(err)
	}
	seedUserBanForUserTest(t, app, account.Id, hashNormalizedEmailForUserTest(account.Email()), "new restriction", time.Time{})
	assertStatus(userhandler.CheckActiveUser(app, account), http.StatusForbidden)
	_, withdrawn := createAuthUser(t, app, true)
	withdrawn.Set("withdrawn", true)
	assertStatus(userhandler.CheckActiveUser(app, withdrawn), http.StatusForbidden)
}
