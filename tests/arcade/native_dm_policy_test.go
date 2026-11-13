package arcade_test

import (
	"encoding/json"
	"github.com/pocketbase/pocketbase/tests"
	"reflect"
	"strings"
	"testing"
	"time"

	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

var nativeDMPolicyOps = []struct {
	method, path string
	first        int
}{
	{"GET", "/user/messaging/preferences", 200}, {"PUT", "/user/messaging/preferences", 400},
	{"GET", "/user/messaging/blocks", 200}, {"PUT", "/user/messaging/blocks/missing", 404},
	{"DELETE", "/user/messaging/blocks/missing", 204}, {"GET", "/user/messaging/recipients", 400},
	{"POST", "/user/messages/missing/report", 400},
}

func nativeDMPolicyRequest(t *testing.T, app *tests.TestApp, method, path, token string, body any, status int) any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	r := executeJSONRequest(t, app, method, path, string(raw), headers)
	defer r.Body.Close()
	if r.StatusCode != status {
		t.Fatalf("%s %s status %d want %d", method, path, r.StatusCode, status)
	}
	if len(token) == 43 && !strings.HasPrefix(path, "/native/") && status != 401 && !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("selected Native response cacheable")
	}
	if status == 204 {
		return nil
	}
	var value any
	if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func nativeDMPolicyState(t *testing.T, app core.App) map[string]string {
	out := nativeMessagingState(t, app)
	for _, name := range []string{messaging.Preferences, messaging.Blocks, messaging.Reports} {
		rows, err := app.FindAllRecords(name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = string(raw)
	}
	return out
}
func TestCanonicalNativeDMPolicyWebInteropAndParticipantACL(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, a := createAuthUser(t, app)
	peerWeb, b := createAuthUser(t, app)
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, a)
	_, peer := nativeNearbySession(t, app, b)
	_, outsider := nativeNearbySession(t, app, staff)
	call := func(method, path, token string, body any, status int) any {
		return nativeDMPolicyRequest(t, app, method, path, token, body, status)
	}
	prefs := call("GET", "/user/messaging/preferences", s.AccessToken, nil, 200)
	if !reflect.DeepEqual(prefs, call("GET", "/user/messaging/preferences", web, nil, 200)) {
		t.Fatal("Web/Native preferences differ")
	}
	exportNativeMessaging(t, "dm-preferences", prefs)
	for _, receive := range []string{"nobody", "everyone"} {
		got := call("PUT", "/user/messaging/preferences", s.AccessToken, map[string]any{"receive": receive, "user": b.Id}, 200)
		if !reflect.DeepEqual(got, call("GET", "/user/messaging/preferences", web, nil, 200)) {
			t.Fatal("preference save/readback differs")
		}
		if call("GET", "/user/messaging/preferences", peerWeb, nil, 200).(map[string]any)["receive"] != "everyone" {
			t.Fatal("spoofed preference owner")
		}
	}
	users := call("GET", "/user/messaging/recipients?q="+b.GetString("username")+"&limit=20", s.AccessToken, nil, 200)
	if !reflect.DeepEqual(users, call("GET", "/user/messaging/recipients?q="+b.GetString("username")+"&limit=20", web, nil, 200)) {
		t.Fatal("recipient Web/Native mismatch")
	}
	exportNativeMessaging(t, "dm-recipients", users)
	room := call("POST", "/user/conversations", s.AccessToken, map[string]any{"kind": "dm", "peerUserId": b.Id}, 200).(map[string]any)
	id := room["id"].(string)
	message := call("POST", "/user/conversations/"+id+"/messages", s.AccessToken, map[string]any{"text": "Synthetic DM safety", "clientMessageId": "dm-policy-first"}, 201).(map[string]any)
	mid := message["id"].(string)
	before := nativeDMPolicyState(t, app)
	call("POST", "/user/messages/"+mid+"/report", outsider.AccessToken, map[string]any{"reason": "Not a participant"}, 404)
	call("GET", "/user/conversations/"+id, outsider.AccessToken, nil, 404)
	if !reflect.DeepEqual(before, nativeDMPolicyState(t, app)) {
		t.Fatal("staff bypass mutated private DM")
	}
	report := call("POST", "/user/messages/"+mid+"/report", peer.AccessToken, map[string]any{"reason": "  Synthetic policy reason  ", "reporter": a.Id}, 201)
	before = nativeDMPolicyState(t, app)
	if !reflect.DeepEqual(report, call("POST", "/user/messages/"+mid+"/report", peerWeb, map[string]any{"reason": "Changed retry reason"}, 200)) || !reflect.DeepEqual(before, nativeDMPolicyState(t, app)) {
		t.Fatal("report repeat changed evidence")
	}
	exportNativeMessaging(t, "dm-report", report)
	call("PUT", "/user/messaging/blocks/"+a.Id, peer.AccessToken, nil, 204)
	before = nativeDMPolicyState(t, app)
	call("PUT", "/user/messaging/blocks/"+a.Id, peerWeb, nil, 204)
	if !reflect.DeepEqual(before, nativeDMPolicyState(t, app)) {
		t.Fatal("repeat block changed state")
	}
	blocks := call("GET", "/user/messaging/blocks", peer.AccessToken, nil, 200)
	if !reflect.DeepEqual(blocks, call("GET", "/user/messaging/blocks", peerWeb, nil, 200)) || len(blocks.(map[string]any)["items"].([]any)) != 1 {
		t.Fatal("blocks owner/readback mismatch")
	}
	exportNativeMessaging(t, "dm-blocks", blocks)
	if call("GET", "/user/conversations/"+id, s.AccessToken, nil, 200).(map[string]any)["canSend"] != false {
		t.Fatal("block did not affect canonical capability")
	}
	call("POST", "/user/conversations/"+id+"/messages", s.AccessToken, map[string]any{"text": "Denied after block", "clientMessageId": "blocked-new"}, 403)
	call("DELETE", "/user/messaging/blocks/"+a.Id, peer.AccessToken, nil, 204)
	call("DELETE", "/user/messaging/blocks/"+a.Id, peerWeb, nil, 204)
	exportNativeMessaging(t, "dm-blocks-empty", call("GET", "/user/messaging/blocks", peer.AccessToken, nil, 200))
}
func TestCanonicalNativeDMPolicyRejectsBadInputAndRollsBackSave(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, a := createAuthUser(t, app)
	_, b := createAuthUser(t, app)
	_, s := nativeNearbySession(t, app, a)
	before := nativeDMPolicyState(t, app)
	for _, receive := range []any{"future", nil, 42} {
		nativeDMPolicyRequest(t, app, "PUT", "/user/messaging/preferences", s.AccessToken, map[string]any{"receive": receive}, 400)
	}
	nativeDMPolicyRequest(t, app, "PUT", "/user/messaging/blocks/"+a.Id, s.AccessToken, nil, 400)
	for _, q := range []string{"q=x", "q=" + strings.Repeat("x", 101), "q=valid&limit=21"} {
		nativeDMPolicyRequest(t, app, "GET", "/user/messaging/recipients?"+q, s.AccessToken, nil, 400)
	}
	nativeDMPolicyRequest(t, app, "POST", "/user/messages/missing/report", s.AccessToken, map[string]any{"reason": strings.Repeat("x", 2001)}, 400)
	if !reflect.DeepEqual(before, nativeDMPolicyState(t, app)) {
		t.Fatal("invalid policy input mutated state")
	}
	for _, name := range []string{messaging.Preferences, messaging.Blocks} {
		app.OnRecordCreate(name).Bind(&hook.Handler[*core.RecordEvent]{Id: "dm-policy-fail-" + name, Func: func(e *core.RecordEvent) error { return fmt.Errorf("synthetic save failure") }})
		app.OnRecordUpdate(name).Bind(&hook.Handler[*core.RecordEvent]{Id: "dm-policy-fail-" + name, Func: func(e *core.RecordEvent) error { return fmt.Errorf("synthetic save failure") }})
	}
	nativeDMPolicyRequest(t, app, "PUT", "/user/messaging/preferences", s.AccessToken, map[string]any{"receive": "nobody"}, 400)
	nativeDMPolicyRequest(t, app, "PUT", "/user/messaging/blocks/"+b.Id, s.AccessToken, nil, 400)
	if !reflect.DeepEqual(before, nativeDMPolicyState(t, app)) {
		t.Fatal("failed save left policy state")
	}
}
func TestCanonicalNativeDMPolicyLifetimeAndDisabledBoundary(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, a := createAuthUser(t, app)
	service, s := nativeNearbySession(t, app, a)
	reject := func(token string, status int) {
		before := nativeDMPolicyState(t, app)
		for _, op := range nativeDMPolicyOps {
			nativeDMPolicyRequest(t, app, op.method, op.path, token, map[string]any{}, status)
		}
		if !reflect.DeepEqual(before, nativeDMPolicyState(t, app)) {
			t.Fatal("denied credential mutated state")
		}
	}
	reject("", 401)
	reject(strings.Repeat("z", 43), 401)
	for _, op := range nativeDMPolicyOps {
		r := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal("bad Native bearer fell back to cookie")
		}
	}
	for _, change := range []struct{ deny, restore string }{{"access_expires=0", "access_expires=unixepoch()+600"}, {"expires=0", "expires=unixepoch()+3600"}, {"client='removed-client'", "client='app.musecat.native.dev'"}} {
		for i, sql := range []string{change.deny, change.restore} {
			if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + sql + " WHERE user_id={:u}").Bind(dbx.Params{"u": a.Id}).Execute(); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				reject(s.AccessToken, 401)
			}
		}
	}
	a.Set("withdrawn", true)
	if err := app.Save(a); err != nil {
		t.Fatal(err)
	}
	reject(s.AccessToken, 401)
	a.Set("withdrawn", false)
	if err := app.Save(a); err != nil {
		t.Fatal(err)
	}
	seedUserBan(t, app, a.Id, "", "native_dm_policy", time.Now().Add(time.Hour))
	reject(s.AccessToken, 403)
	if err := service.Revoke(app, s.RefreshToken); err != nil {
		t.Fatal(err)
	}
	reject(s.AccessToken, 401)
	disabled := nativeNearbyApp(t, false)
	jwt, u := createAuthUser(t, disabled)
	svc, ds := nativeNearbySession(t, disabled, u)
	if got, err := svc.Authenticate(disabled, ds.AccessToken); err != nil || got.Id != u.Id {
		t.Fatal("disabled fixture must prove valid same-DB Native credential")
	}
	for _, op := range nativeDMPolicyOps {
		nativeDMPolicyRequest(t, disabled, op.method, op.path, ds.AccessToken, map[string]any{}, 401)
	}
	nativeDMPolicyRequest(t, disabled, "GET", "/user/messaging/preferences", jwt, nil, 200)
}
func TestCanonicalNativeDMPolicyRatesAndNoAliases(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, a := createAuthUser(t, app)
	_, s := nativeNearbySession(t, app, a)
	for _, op := range nativeDMPolicyOps {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: op.method + " " + op.path, MaxRequests: 1, Duration: 60}}}
		nativeDMPolicyRequest(t, app, op.method, op.path, s.AccessToken, map[string]any{}, op.first)
		nativeDMPolicyRequest(t, app, op.method, op.path, s.AccessToken, map[string]any{}, 429)
		nativeDMPolicyRequest(t, app, op.method, "/native"+op.path, s.AccessToken, map[string]any{}, 404)
	}
	app.Settings().RateLimits = core.RateLimitsConfig{}
	nativeDMPolicyRequest(t, app, "GET", "/moderation/announcements", s.AccessToken, nil, 401)
	nativeDMPolicyRequest(t, app, "PUT", "/user/inquiry-bots/arcade/missing", s.AccessToken, map[string]any{}, 401)
}

func TestCanonicalNativeDMPolicyBodyLimits(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, a := createAuthUser(t, app)
	_, s := nativeNearbySession(t, app, a)
	for _, op := range []struct {
		path  string
		limit int
	}{{"/user/messaging/preferences", 4096}, {"/user/messages/missing/report", 8192}} {
		method := "POST"
		if strings.Contains(op.path, "preferences") {
			method = "PUT"
		}
		for _, extra := range []int{0, 1} {
			body := `{"future":"` + strings.Repeat("x", op.limit-len(`{"future":""}`)+extra) + `"}`
			r := executeJSONRequest(t, app, method, op.path, body, map[string]string{"Authorization": "Bearer " + s.AccessToken})
			r.Body.Close()
			expected := 400
			if extra == 1 {
				expected = 413
			}
			if r.StatusCode != expected {
				t.Fatal("canonical policy body boundary changed", r.StatusCode, expected)
			}
		}
	}
}
