package arcade_test

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/pocketbase/dbx"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

var managedAccountPaths = []string{"/moderation/user/test-account", "/moderation/user/arcade-account"}

func managedAccountInput(arcade string) map[string]any {
	v := map[string]any{"email": " QA" + rand.Text() + "@example.invalid ", "username": "QA" + rand.Text()[:10], "password": " " + rand.Text() + " ", "nickname": " QA provisioned ", "tags": []string{"developer"}, "verified": true, "owns": []string{"spoof"}}
	if arcade != "" {
		v["arcadeId"] = " " + arcade + " "
	}
	return v
}
func managedAccountRequest(t *testing.T, app *tests.TestApp, path, token, cookie string, v any, want int) map[string]any {
	t.Helper()
	return nativeBanRequest(t, app, "POST", path, token, cookie, v, want)
}
func managedAccountProof(t *testing.T, app *tests.TestApp, v, result map[string]any, official bool) {
	t.Helper()
	id, ok := result["id"].(string)
	if !ok || id == "" {
		t.Fatal("missing account id")
	}
	u, err := app.FindRecordById("user", id)
	if err != nil {
		t.Fatal("created account missing")
	}
	p, err := app.FindRecordById("user_info", id)
	if err != nil {
		t.Fatal("created profile missing")
	}
	if !u.ValidatePassword(v["password"].(string)) || u.Email() != strings.ToLower(strings.TrimSpace(v["email"].(string))) || u.GetString("username") != v["username"] || u.Verified() {
		t.Fatal("credential normalization or verification contract changed")
	}
	if result["email"] != u.Email() || result["username"] != u.GetString("username") || result["nickname"] != "QA provisioned" || result["password"] != nil || result["token"] != nil {
		t.Fatal("receipt contract changed")
	}
	if p.GetString("nickname") != "QA provisioned" || p.GetString("country_mode") != "auto" || p.GetString("visit_visibility") != "summary" || p.GetString("favorite_visibility") != "private" {
		t.Fatal("profile defaults changed")
	}
	if official {
		arcade := strings.TrimSpace(v["arcadeId"].(string))
		if !reflect.DeepEqual(u.GetStringSlice("tags"), []string{"arcade_owner"}) || !reflect.DeepEqual(u.GetStringSlice("owns"), []string{arcade}) || result["arcadeId"] != arcade {
			t.Fatal("official fixed role/ownership changed")
		}
		rows, err := app.FindRecordsByFilter(usernotification.Inbox, "user={:user} && event_key={:event}", "", 0, 0, map[string]any{"user": id, "event": usernotification.ArcadeSetupEvent})
		if err != nil || len(rows) != 1 {
			t.Fatal("official setup notification missing")
		}
	} else if len(u.GetStringSlice("tags")) != 0 || len(u.GetStringSlice("owns")) != 0 || result["arcadeId"] != nil {
		t.Fatal("ordinary account escalated")
	}
	if !reflect.DeepEqual(nativeBanFamilies(t, app, id), []int{0, 0}) {
		t.Fatal("creation issued a Native session")
	}
	dir := os.Getenv("MUSECAT_CANONICAL_MANAGED_ACCOUNT_FIXTURES")
	if dir != "" {
		projection := map[string]any{"id": id, "username": result["username"], "nickname": result["nickname"]}
		name := "managed-test-account"
		if official {
			projection["arcadeId"] = result["arcadeId"]
			name = "managed-arcade-account"
		}
		raw, err := json.MarshalIndent(projection, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
func TestCanonicalNativeManagedAccountsActualWebInterop(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, actor := createAuthUserWithTags(t, app, []string{"moderator"})
	_, session := nativeNearbySession(t, app, actor)
	arcade, _ := seedArcade(t, app, actor.Id, arcadeSeed{Name: "Provisioning QA", Address: "Synthetic road", Location: location{Lat: 37.56, Lon: 126.97}})
	setArcadeVisibility(t, app, arcade, true, true)
	families := nativeBanFamilies(t, app, actor.Id)
	beforeXP := nativeSubwayState(t, app)
	for i, path := range managedAccountPaths {
		for _, token := range []string{session.AccessToken, web} {
			target := ""
			if i == 1 {
				target = arcade
			}
			v := managedAccountInput(target)
			result := managedAccountRequest(t, app, path, token, "", v, 201)
			managedAccountProof(t, app, v, result, i == 1)
			other := web
			if token == web {
				other = session.AccessToken
			}
			managedAccountRequest(t, app, path, other, "", v, 409)
		}
	}
	if !reflect.DeepEqual(families, nativeBanFamilies(t, app, actor.Id)) || beforeXP != nativeSubwayState(t, app) {
		t.Fatal("creation replaced caller session or changed domain XP/history")
	}
}
func TestCanonicalNativeManagedAccountRolesAndVisibility(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, owner := createAuthUser(t, app)
	arcade, _ := seedArcade(t, app, owner.Id, arcadeSeed{Name: "Role QA", Address: "Synthetic road", Location: location{Lat: 37.56, Lon: 126.97}})
	setArcadeVisibility(t, app, arcade, true, false)
	for _, tags := range [][]string{nil, {"supporter"}, {"founding_supporter"}, {"arcade_owner"}, {"designer"}, {"developer"}, {"moderator"}} {
		web, u := createAuthUserWithTags(t, app, tags)
		_, s := nativeNearbySession(t, app, u)
		for _, token := range []string{web, s.AccessToken} {
			for i, path := range managedAccountPaths {
				want := 403
				if len(tags) > 0 && (tags[0] == "developer" || tags[0] == "moderator" || i == 1 && tags[0] == "designer") {
					want = 201
				}
				target := ""
				if i == 1 {
					target = arcade
				}
				managedAccountRequest(t, app, path, token, "", managedAccountInput(target), want)
			}
		}
	}
	web, staff := createAuthUserWithTags(t, app, []string{"developer"})
	_, s := nativeNearbySession(t, app, staff)
	for _, token := range []string{web, s.AccessToken} {
		managedAccountRequest(t, app, managedAccountPaths[1], token, "", managedAccountInput(""), 400)
		managedAccountRequest(t, app, managedAccountPaths[1], token, "", managedAccountInput("missing"), 404)
	}
	setArcadeVisibility(t, app, arcade, false, false)
	managedAccountRequest(t, app, managedAccountPaths[1], s.AccessToken, "", managedAccountInput(arcade), 404)
}
func TestCanonicalNativeManagedAccountAtomicProfileFailure(t *testing.T) {
	for i, path := range managedAccountPaths {
		t.Run(path, func(t *testing.T) {
			app := nativeNearbyApp(t, true)
			_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
			_, s := nativeNearbySession(t, app, staff)
			target := ""
			if i == 1 {
				target, _ = seedArcade(t, app, staff.Id, arcadeSeed{Name: "Rollback QA", Address: "Synthetic road", Location: location{Lat: 37.56, Lon: 126.97}})
				setArcadeVisibility(t, app, target, true, false)
			}
			before := nativeGrantStatusState(t, app)
			v := managedAccountInput(target)
			app.OnRecordCreate("user_info").BindFunc(func(e *core.RecordEvent) error { return errors.New("synthetic profile failure") })
			managedAccountRequest(t, app, path, s.AccessToken, "", v, 502)
			if _, err := app.FindAuthRecordByEmail("user", strings.TrimSpace(v["email"].(string))); err == nil {
				t.Fatal("partial account persisted")
			}
			if before != nativeGrantStatusState(t, app) {
				t.Fatal("failed creation changed domain state")
			}
		})
	}
}

func TestCanonicalNativeManagedAccountLifetimesAndScope(t *testing.T) {
	for _, mode := range []string{"disabled", "revoked", "expired", "family-expired", "removed-client", "account-key", "withdrawn", "ban"} {
		t.Run(mode, func(t *testing.T) {
			app := nativeNearbyApp(t, mode != "disabled")
			web, u := createAuthUserWithTags(t, app, []string{"moderator"})
			service, s := nativeNearbySession(t, app, u)
			if actor, err := service.Authenticate(app, s.AccessToken); err != nil || actor.Id != u.Id {
				t.Fatal("fixture Native credential invalid")
			}
			switch mode {
			case "revoked":
				if err := service.Revoke(app, s.RefreshToken); err != nil {
					t.Fatal(err)
				}
			case "account-key":
				u.RefreshTokenKey()
				if err := app.Save(u); err != nil {
					t.Fatal(err)
				}
			case "withdrawn":
				u.Set("withdrawn", true)
				if err := app.Save(u); err != nil {
					t.Fatal(err)
				}
			case "ban":
				seedUserBan(t, app, u.Id, "", "Synthetic provisioning ban", time.Now().Add(time.Hour))
			case "expired", "family-expired", "removed-client":
				clause := map[string]string{"expired": "access_expires=0", "family-expired": "expires=0", "removed-client": "client='removed'"}[mode]
				if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + clause + " WHERE user_id={:user}").Bind(dbx.Params{"user": u.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
			}
			before := nativeGrantStatusState(t, app)
			want := 401
			if mode == "ban" {
				want = 403
			}
			for _, path := range managedAccountPaths {
				managedAccountRequest(t, app, path, s.AccessToken, "", managedAccountInput(""), want)
			}
			if before != nativeGrantStatusState(t, app) {
				t.Fatal("inactive credential changed account state")
			}
			if mode == "disabled" {
				managedAccountRequest(t, app, managedAccountPaths[0], web, "", managedAccountInput(""), 201)
				managedAccountRequest(t, app, managedAccountPaths[1], web, "", managedAccountInput(""), 400)
			}
		})
	}
	app := nativeNearbyApp(t, true)
	web, u := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, u)
	before := nativeGrantStatusState(t, app)
	for _, path := range managedAccountPaths {
		for _, token := range []string{"", strings.Repeat("z", 43)} {
			managedAccountRequest(t, app, path, token, web, managedAccountInput(""), 401)
		}
		managedAccountRequest(t, app, "/native"+path, s.AccessToken, "", nil, 404)
	}
	for _, path := range []string{"/user/signup", "/user/withdraw"} {
		managedAccountRequest(t, app, path, s.AccessToken, "", nil, 401)
	}
	if before != nativeGrantStatusState(t, app) {
		t.Fatal("invalid bearer or alias changed accounts")
	}
}
func TestCanonicalNativeManagedAccountBodyRateAndNotificationRollback(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, u := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, u)
	before := nativeGrantStatusState(t, app)
	for _, path := range managedAccountPaths {
		for _, size := range []int{16 << 10, (16 << 10) + 1} {
			body := "{}" + strings.Repeat(" ", size-2)
			want := 400
			if size > 16<<10 {
				want = 413
			}
			res := executeJSONRequest(t, app, "POST", path, body, map[string]string{"Authorization": "Bearer " + s.AccessToken})
			res.Body.Close()
			if res.StatusCode != want {
				t.Fatalf("body size status%d want%d", res.StatusCode, want)
			}
		}
	}
	app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "POST " + managedAccountPaths[0], MaxRequests: 1, Duration: 60}, {Label: "POST " + managedAccountPaths[1], MaxRequests: 1, Duration: 60}}}
	for _, path := range managedAccountPaths {
		managedAccountRequest(t, app, path, s.AccessToken, "", nil, 400)
		managedAccountRequest(t, app, path, s.AccessToken, "", nil, 429)
	}
	if before != nativeGrantStatusState(t, app) {
		t.Fatal("rejected body/quota changed accounts")
	}
	isolated := nativeNearbyApp(t, true)
	_, staff := createAuthUserWithTags(t, isolated, []string{"designer"})
	_, session := nativeNearbySession(t, isolated, staff)
	arcade, _ := seedArcade(t, isolated, staff.Id, arcadeSeed{Name: "Notification rollback QA", Address: "Synthetic road", Location: location{Lat: 37.56, Lon: 126.97}})
	setArcadeVisibility(t, isolated, arcade, true, false)
	snapshot := nativeGrantStatusState(t, isolated)
	v := managedAccountInput(arcade)
	isolated.OnRecordCreate(usernotification.Inbox).BindFunc(func(e *core.RecordEvent) error { return errors.New("synthetic notification failure") })
	managedAccountRequest(t, isolated, managedAccountPaths[1], session.AccessToken, "", v, 502)
	if _, err := isolated.FindAuthRecordByEmail("user", strings.TrimSpace(v["email"].(string))); err == nil {
		t.Fatal("notification failure retained account")
	}
	if snapshot != nativeGrantStatusState(t, isolated) {
		t.Fatal("notification failure changed account state")
	}
}
