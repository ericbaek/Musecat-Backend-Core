package arcade_test

import (
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/tests"
)

type nativeRouteResult struct {
	status int
	cache  string
	body   any
}

func nativeRouteCall(t *testing.T, app *tests.TestApp, method, path, token string, input any) nativeRouteResult {
	t.Helper()
	raw := ""
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		raw = string(encoded)
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	response := executeJSONRequest(t, app, method, path, raw, headers)
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body any
	_ = json.Unmarshal(data, &body)
	return nativeRouteResult{status: response.StatusCode, cache: response.Header.Get("Cache-Control"), body: body}
}

func TestNativeSharedAccountRoutesMatchWebIdentity(t *testing.T) {
	app := nativeNearbyApp(t, true)
	staffWeb, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	web, member := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, session := nativeNearbySession(t, app, member)
	native := session.AccessToken
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{Name: "Native route auth", Address: "1 Native St", Location: location{Lat: 37.5, Lon: 127}})
	addOwnedArcade(t, app, member, arcadeID)
	call := func(method, path, token string, input any) nativeRouteResult {
		return nativeRouteCall(t, app, method, path, token, input)
	}
	expect := func(r nativeRouteResult, status int, label string) {
		t.Helper()
		if r.status != status {
			t.Fatalf("%s status=%d want=%d body=%v", label, r.status, status, r.body)
		}
		if !strings.Contains(r.cache, "no-store") {
			t.Fatalf("%s Native response is cacheable: %q", label, r.cache)
		}
	}

	// Reads return the same canonical payload for the Web JWT and the Native session.
	for _, path := range []string{
		"/arcades/updates/history",
		"/arcade/campaigns?id=" + arcadeID,
		"/arcade/analytics?arcade=" + arcadeID,
		"/user/inquiry-bots/arcade/" + arcadeID,
		"/user/feedback",
		"/user/bot-keys",
		"/community/posts",
		"/announcements",
	} {
		fromWeb := call("GET", path, web, nil)
		fromNative := call("GET", path, native, nil)
		expect(fromNative, fromWeb.status, "GET "+path)
		if fromWeb.status != 200 || !reflect.DeepEqual(fromWeb.body, fromNative.body) {
			t.Fatalf("GET %s differs between Web (%d) and Native: %v / %v", path, fromWeb.status, fromWeb.body, fromNative.body)
		}
	}

	// Daily check-in awards the same account once across both clients.
	checkIn := call("POST", "/user/check-in", native, map[string]any{})
	expect(checkIn, 200, "POST /user/check-in")
	if checkIn.body.(map[string]any)["checked_in"] != true {
		t.Fatal("Native check-in did not award the account", checkIn.body)
	}
	if again := call("POST", "/user/check-in", web, map[string]any{}); again.status != 200 || again.body.(map[string]any)["already_checked_in"] != true {
		t.Fatal("Web check-in after Native check-in must be idempotent for the same account", again.body)
	}

	// Inquiry bot configuration is saved through the owner guard.
	expect(call("PUT", "/user/inquiry-bots/arcade/"+arcadeID, native, map[string]any{"enabled": false, "start": "", "nodes": []any{}}), 200, "PUT inquiry bot")

	// Announcement unseen/view state belongs to the account, not the client.
	story := uploadAnnouncement(t, app, staffWeb, []string{"ko"})
	storyID := story["id"].(string)
	expect(call("GET", "/announcements/"+storyID, native, nil), 404, "GET draft announcement")
	announcementCall(t, app, "POST", "/moderation/announcements/"+storyID+"/publish", staffWeb, map[string]any{"locales": []string{"ko"}, "sendPush": false}, 200)
	unseen := call("GET", "/announcements/unseen", native, nil)
	expect(unseen, 200, "GET /announcements/unseen")
	if item, _ := unseen.body.(map[string]any)["announcement"].(map[string]any); item == nil || item["id"] != storyID {
		t.Fatal("Native unseen did not return the published story", unseen.body)
	}
	expect(call("GET", "/announcements/"+storyID, native, nil), 200, "GET announcement")
	expect(call("POST", "/announcements/"+storyID+"/view", native, map[string]any{}), 200, "POST announcement view")
	if after := call("GET", "/announcements/unseen", web, nil); after.status != 200 || after.body.(map[string]any)["announcement"] != nil {
		t.Fatal("Native view must mark the story seen for the Web session too", after.body)
	}
}

func TestNativeSharedAccountRoutesFailClosedAndKeepUnselectedScope(t *testing.T) {
	for _, mode := range []string{"invalid", "expired", "revoked", "banned", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			app := nativeNearbyApp(t, mode != "disabled")
			web, member := createAuthUserWithTags(t, app, []string{"arcade_owner"})
			service, session := nativeNearbySession(t, app, member)
			arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{Name: "Native denied", Address: "2 Native St", Location: location{Lat: 37.5, Lon: 127}})
			addOwnedArcade(t, app, member, arcadeID)
			token := session.AccessToken
			status := 401
			switch mode {
			case "invalid":
				token = strings.Repeat("z", 43)
			case "expired":
				if _, err := app.DB().NewQuery("UPDATE native_auth_session SET access_expires=0 WHERE user_id={:id}").Bind(dbx.Params{"id": member.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if err := service.Revoke(app, session.RefreshToken); err != nil {
					t.Fatal(err)
				}
			case "banned":
				seedUserBan(t, app, member.Id, "", "native route auth fixture", time.Now().Add(time.Hour))
				status = 403
			}
			headers := map[string]string{"Authorization": "Bearer " + token}
			if mode == "invalid" {
				// An invalid Native bearer never falls back to a Web cookie.
				headers["Cookie"] = "pb_auth=" + web
			}
			for _, op := range []struct{ method, path string }{
				{"POST", "/user/check-in"}, {"GET", "/announcements/unseen"}, {"GET", "/arcades/updates/history"},
				{"GET", "/arcade/campaigns?id=" + arcadeID}, {"GET", "/user/inquiry-bots/arcade/" + arcadeID},
				{"PUT", "/user/inquiry-bots/arcade/" + arcadeID}, {"GET", "/user/feedback"}, {"POST", "/user/feedback/thread"},
				{"GET", "/user/bot-keys"}, {"POST", "/user/bot-keys"},
				{"POST", "/community/post"}, {"PUT", "/community/post/like"},
			} {
				want := status
				res := executeJSONRequest(t, app, op.method, op.path, "{}", headers)
				res.Body.Close()
				if res.StatusCode != want {
					t.Fatalf("%s %s status=%d want=%d", op.method, op.path, res.StatusCode, want)
				}
			}
			if mode != "disabled" {
				return
			}
			// Guest reads keep working, and an unconfigured Native bearer grants nothing.
			for _, path := range []string{"/announcements", "/community/posts", "/arcade/analytics?arcade=" + arcadeID} {
				res := executeJSONRequest(t, app, "GET", path, "", nil)
				res.Body.Close()
				if res.StatusCode != 200 {
					t.Fatalf("guest GET %s status=%d", path, res.StatusCode)
				}
			}
		})
	}

	// Valid sessions still cannot reach unreleased external connections, grants,
	// Joint, web-push devices,
	// signup, raw collections, delegated account routes or duplicate /native URLs.
	app := nativeNearbyApp(t, true)
	_, member := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, member)
	for _, op := range []struct {
		method, path string
		want         int
	}{
		{"GET", "/user/connections", 404}, {"DELETE", "/user/connections/missingconnection", 404},
		{"POST", "/user/connections/start", 404}, {"POST", "/user/connections/complete", 404}, {"GET", "/user/authorizations", 404},
		{"GET", "/user/joint", 404}, {"POST", "/user/joint/begin", 404}, {"POST", "/user/signup", 401},
		{"PUT", "/arcade/report-subscription", 401}, {"PUT", "/user/notifications/device", 401},
		{"GET", "/user/me", 401}, {"POST", "/user/joint/lookup", 404},
		{"GET", "/developer/v1/account/user/notifications", 404},
		{"POST", "/native/user/check-in", 404}, {"GET", "/native/announcements/unseen", 404},
	} {
		res := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		res.Body.Close()
		if res.StatusCode != op.want {
			t.Fatalf("unselected %s %s status=%d want=%d", op.method, op.path, res.StatusCode, op.want)
		}
	}
}
