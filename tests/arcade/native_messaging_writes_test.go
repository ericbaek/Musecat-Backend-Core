package arcade_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/pocketbase/tools/hook"
	"mime/multipart"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

var nativeMessagingWriteOps = []struct{ method, path string }{{"POST", "/user/conversations"}, {"POST", "/user/conversations/missing/messages"}, {"PUT", "/user/conversations/missing/read"}, {"PATCH", "/user/conversations/missing/inquiry"}}

func TestCanonicalNativeMessagingWritesShareDMPairReplayAndOwnReadCursor(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, a := createAuthUser(t, app)
	bWeb, b := createAuthUser(t, app)
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, a)
	_, peer := nativeNearbySession(t, app, b)
	_, staffSession := nativeNearbySession(t, app, staff)
	body := map[string]any{"kind": "dm", "peerUserId": b.Id, "createdBy": staff.Id, "future_field": true}
	room := nativeBulkRequest(t, app, "POST", "/user/conversations", s.AccessToken, body, 200).(map[string]any)
	if !reflect.DeepEqual(room, nativeBulkRequest(t, app, "POST", "/user/conversations", web, body, 200)) {
		t.Fatal("Web/Native unordered DM create differs")
	}
	exportNativeMessaging(t, "messaging-write-dm-create", room)
	id := room["id"].(string)
	base := "/user/conversations/" + id
	send := map[string]any{"text": "  Synthetic canonical message  ", "clientMessageId": "stable-dm-message", "senderUserId": staff.Id, "acting_type": "team"}
	beforeFailure := nativeMessagingState(t, app)
	subscription := messaging.Events(app).Subscribe(b.Id)
	defer messaging.Events(app).Unsubscribe(subscription)
	rejectedNotification := false
	app.OnRecordCreate("user_notification").Bind(&hook.Handler[*core.RecordEvent]{Id: "native-write-reject-notification", Func: func(e *core.RecordEvent) error {
		rejectedNotification = true
		return fmt.Errorf("synthetic notification rollback")
	}})
	nativeBulkRequest(t, app, "POST", base+"/messages", s.AccessToken, send, 400)
	app.OnRecordCreate("user_notification").Unbind("native-write-reject-notification")
	if !rejectedNotification || !reflect.DeepEqual(beforeFailure, nativeMessagingState(t, app)) || len(subscription.Events) != 0 {
		t.Fatal("notification failure left message/sequence/event state")
	}
	for _, collection := range []string{"user_notification", "user_notification_delivery"} {
		count, err := app.CountRecords(collection)
		if err != nil || count != 0 {
			t.Fatal("notification failure left an outbox row", err)
		}
	}
	first := nativeBulkRequest(t, app, "POST", base+"/messages", s.AccessToken, send, 201).(map[string]any)
	if first["senderUserId"] != a.Id || first["text"] != "Synthetic canonical message" || first["actingAs"].(map[string]any)["id"] != a.Id {
		t.Fatal("message actor or normalization changed")
	}
	exportNativeMessaging(t, "messaging-write-send", first)
	before := nativeMessagingState(t, app)
	replay := nativeBulkRequest(t, app, "POST", base+"/messages", web, send, 200)
	if !reflect.DeepEqual(first, replay) || !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("cross-client retry changed message/history")
	}
	exportNativeMessaging(t, "messaging-write-replay", replay)
	send["text"] = "Different content"
	nativeBulkRequest(t, app, "POST", base+"/messages", s.AccessToken, send, 409)
	if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("conflicting retry wrote domain state")
	}
	nativeBulkRequest(t, app, "POST", base+"/messages", staffSession.AccessToken, send, 404)
	read := nativeBulkRequest(t, app, "PUT", base+"/read", peer.AccessToken, map[string]any{"throughSequence": 1}, 200).(map[string]any)
	if read["conversationId"] != id || read["lastReadSequence"] != float64(1) || read["unreadCount"] != float64(0) {
		t.Fatal("own read receipt changed")
	}
	exportNativeMessaging(t, "messaging-write-read", read)
	if !reflect.DeepEqual(read, nativeBulkRequest(t, app, "PUT", base+"/read", bWeb, map[string]any{"throughSequence": 0}, 200)) {
		t.Fatal("read cursor was not monotonic")
	}
	before = nativeMessagingState(t, app)
	nativeBulkRequest(t, app, "PUT", base+"/read", peer.AccessToken, map[string]any{"throughSequence": 2}, 400)
	if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("future read cursor wrote state")
	}
	own := nativeBulkRequest(t, app, "GET", base, s.AccessToken, nil, 200).(map[string]any)
	if own["lastReadSequence"] != float64(0) || own["counterpartReadSequence"] != float64(1) {
		t.Fatal("peer read advanced own cursor")
	}
	nativeBulkRequest(t, app, "PUT", "/user/messaging/preferences", bWeb, map[string]any{"receive": "nobody"}, 200)
	// Existing idempotent receipt remains available; a new send is forbidden.
	send["text"] = "  Synthetic canonical message  "
	nativeBulkRequest(t, app, "POST", base+"/messages", s.AccessToken, send, 200)
	send["clientMessageId"] = "new-after-optout"
	before = nativeMessagingState(t, app)
	nativeBulkRequest(t, app, "POST", base+"/messages", s.AccessToken, send, 403)
	if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("opt-out denied send wrote state")
	}
}

func TestCanonicalNativeMessagingInquiryWritesKeepResponderStatusAndAtomicHistory(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, requester := createAuthUser(t, app)
	ownerWeb, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, outsider := createAuthUser(t, app)
	arcade, _ := seedPublicArcade(t, app, requester.Id, arcadeSeed{Name: "Synthetic messaging writes", Address: "Synthetic", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{arcade})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	venue, err := app.FindRecordById("arcade", arcade)
	if err != nil {
		t.Fatal(err)
	}
	venue.Set("inquiry_settings", map[string]any{"mode": "internal"})
	if err := app.Save(venue); err != nil {
		t.Fatal(err)
	}
	_, s := nativeNearbySession(t, app, requester)
	_, o := nativeNearbySession(t, app, owner)
	_, foreign := nativeNearbySession(t, app, outsider)
	body := map[string]any{"kind": "inquiry", "inbox": map[string]any{"type": "arcade", "id": arcade}, "initialMessage": map[string]any{"text": "Synthetic inquiry", "clientMessageId": "stable-first-inquiry"}}
	room := nativeBulkRequest(t, app, "POST", "/user/conversations", s.AccessToken, body, 201).(map[string]any)
	exportNativeMessaging(t, "messaging-write-inquiry-create", room)
	if !reflect.DeepEqual(room, nativeBulkRequest(t, app, "POST", "/user/conversations", web, body, 200)) {
		t.Fatal("cross-client inquiry create replay changed DTO")
	}
	id := room["id"].(string)
	base := "/user/conversations/" + id
	before := nativeMessagingState(t, app)
	for _, token := range []string{s.AccessToken, web} {
		nativeBulkRequest(t, app, "PATCH", base+"/inquiry", token, map[string]any{"status": "solved"}, 400)
	}
	if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("requester changed internal status")
	}
	solved := nativeBulkRequest(t, app, "PATCH", base+"/inquiry", o.AccessToken, map[string]any{"status": "solved"}, 200).(map[string]any)
	if solved["status"] != "solved" || solved["latestSequence"] != float64(2) || solved["canSend"] != true {
		t.Fatal("responder status history changed")
	}
	exportNativeMessaging(t, "messaging-write-status", solved)
	before = nativeMessagingState(t, app)
	if !reflect.DeepEqual(solved, nativeBulkRequest(t, app, "PATCH", base+"/inquiry", ownerWeb, map[string]any{"status": "solved"}, 200)) || !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("same-status action duplicated history")
	}
	followup := nativeBulkRequest(t, app, "POST", base+"/messages", s.AccessToken, map[string]any{"text": "Synthetic follow-up", "clientMessageId": "followup"}, 201).(map[string]any)
	if followup["statusChange"] != nil {
		t.Fatal("requester received internal status transition")
	}
	exportNativeMessaging(t, "messaging-write-requester-followup", followup)
	reply := nativeBulkRequest(t, app, "POST", base+"/messages", o.AccessToken, map[string]any{"text": "Synthetic official reply", "clientMessageId": "official-reply", "actingAs": map[string]any{"type": "arcade", "id": arcade}}, 201).(map[string]any)
	if reply["actingAs"].(map[string]any)["type"] != "arcade" || reply["statusChange"].(map[string]any)["to"] != "recognised" {
		t.Fatal("acting identity or responder transition changed")
	}
	exportNativeMessaging(t, "messaging-write-responder-reply", reply)
	for _, token := range []string{foreign.AccessToken} {
		nativeBulkRequest(t, app, "POST", base+"/messages", token, map[string]any{"text": "Denied", "clientMessageId": "foreign"}, 404)
	}
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	before = nativeMessagingState(t, app)
	nativeBulkRequest(t, app, "PATCH", base+"/inquiry", o.AccessToken, map[string]any{"status": "waiting"}, 404)
	nativeBulkRequest(t, app, "POST", base+"/messages", o.AccessToken, map[string]any{"text": "No longer owner", "clientMessageId": "former-owner"}, 404)
	if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("removed owner wrote private inquiry")
	}
}

func TestCanonicalNativeMessagingWritesPreserveUnicodePhotosAndBodyLimits(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, a := createAuthUser(t, app)
	_, b := createAuthUser(t, app)
	_, s := nativeNearbySession(t, app, a)
	room := nativeBulkRequest(t, app, "POST", "/user/conversations", s.AccessToken, map[string]any{"kind": "dm", "peerUserId": b.Id}, 200).(map[string]any)
	base := "/user/conversations/" + room["id"].(string)
	nativeBulkRequest(t, app, "POST", base+"/messages", s.AccessToken, map[string]any{"text": strings.Repeat("🧩", 4000), "clientMessageId": "unicode-boundary"}, 201)
	before := nativeMessagingState(t, app)
	nativeBulkRequest(t, app, "POST", base+"/messages", s.AccessToken, map[string]any{"text": strings.Repeat("🧩", 4001), "clientMessageId": "unicode-over"}, 400)
	if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("oversized Unicode wrote state")
	}
	upload := func(token string, count int, status int) any {
		t.Helper()
		var buf bytes.Buffer
		form := multipart.NewWriter(&buf)
		_ = form.WriteField("text", "Synthetic multipart")
		_ = form.WriteField("clientMessageId", "multipart-stable")
		for range count {
			part, err := form.CreateFormFile("photos", "synthetic.png")
			if err != nil {
				t.Fatal(err)
			}
			_, _ = part.Write(pngFixtureBytes())
		}
		_ = form.Close()
		r := executeRequest(t, app, "POST", base+"/messages", &buf, map[string]string{"Authorization": "Bearer " + token, "Content-Type": form.FormDataContentType()})
		defer r.Body.Close()
		if r.StatusCode != status {
			t.Fatal("multipart messaging status", r.StatusCode, status)
		}
		var out any
		if err := json.NewDecoder(r.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	first := upload(s.AccessToken, 1, 201)
	if !reflect.DeepEqual(first, upload(web, 1, 200)) {
		t.Fatal("Native/Web multipart replay differs")
	}
	exportNativeMessaging(t, "messaging-write-multipart", first)
	before = nativeMessagingState(t, app)
	upload(s.AccessToken, 4, 400)
	if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("invalid attachment count wrote state")
	}
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.OnServe().Trigger(&core.ServeEvent{App: app, Router: router}, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct {
		method, path string
		limit        int64
	}{{"POST", "/user/conversations", 46048576}, {"POST", base + "/messages", 46048576}, {"PUT", base + "/read", 4096}, {"PATCH", base + "/inquiry", 4096}} {
		for _, extra := range []int64{0, 1} {
			req := httptest.NewRequest(op.method, op.path, strings.NewReader("{}"))
			req.ContentLength = op.limit + extra
			req.Header.Set("Authorization", "Bearer "+s.AccessToken)
			req.Header.Set("Content-Type", "application/json")
			r := httptest.NewRecorder()
			mux.ServeHTTP(r, req)
			if extra == 1 && r.Code != 413 || extra == 0 && r.Code == 413 {
				t.Fatal("existing Messaging body limit changed")
			}
		}
	}
}

func TestCanonicalNativeMessagingWritesRejectInvalidInactiveAndDisabledSessions(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, user := createAuthUser(t, app)
	service, s := nativeNearbySession(t, app, user)
	reject := func(token string, status int) {
		for _, op := range nativeMessagingWriteOps {
			nativeBulkRequest(t, app, op.method, op.path, token, map[string]any{}, status)
		}
	}
	reject("", 401)
	reject(strings.Repeat("z", 43), 401)
	for _, op := range nativeMessagingWriteOps {
		r := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal("invalid bearer fell back to cookie")
		}
	}
	for _, change := range []struct{ deny, restore string }{{"access_expires=0", "access_expires=unixepoch()+600"}, {"expires=0", "expires=unixepoch()+3600"}, {"client='removed-client'", "client='app.musecat.native.dev'"}} {
		for i, sql := range []string{change.deny, change.restore} {
			if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + sql + " WHERE user_id={:user}").Bind(dbx.Params{"user": user.Id}).Execute(); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				reject(s.AccessToken, 401)
			}
		}
	}
	user.Set("withdrawn", true)
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	reject(s.AccessToken, 401)
	user.Set("withdrawn", false)
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	seedUserBan(t, app, user.Id, "", "native_messaging_write", time.Now().Add(time.Hour))
	reject(s.AccessToken, 403)
	if err := service.Revoke(app, s.RefreshToken); err != nil {
		t.Fatal(err)
	}
	reject(s.AccessToken, 401)
	disabled := nativeNearbyApp(t, false)
	disabledWeb, owner := createAuthUser(t, disabled)
	ds, issued := nativeNearbySession(t, disabled, owner)
	if got, err := ds.Authenticate(disabled, issued.AccessToken); err != nil || got.Id != owner.Id {
		t.Fatal("disabled fixture is not a valid same-DB session")
	}
	for _, op := range nativeMessagingWriteOps {
		r := executeJSONRequest(t, disabled, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + issued.AccessToken})
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal("disabled adapter accepted messaging write")
		}
	}
	nativeBulkRequest(t, disabled, "POST", "/user/conversations", disabledWeb, map[string]any{}, 400)
}

func TestCanonicalNativeMessagingWritesKeepRateLimitsAndExcludeUnrelatedProtocols(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, user := createAuthUser(t, app)
	_, s := nativeNearbySession(t, app, user)
	for _, op := range nativeMessagingWriteOps {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: op.method + " " + op.path, MaxRequests: 1, Duration: 60}}}
		initial := 400

		if op.method == "PATCH" {
			initial = 404
		}
		nativeBulkRequest(t, app, op.method, op.path, s.AccessToken, map[string]any{}, initial)
		nativeBulkRequest(t, app, op.method, op.path, s.AccessToken, map[string]any{}, 429)
		r := executeJSONRequest(t, app, op.method, "/native"+op.path, "{}", map[string]string{"Authorization": "Bearer " + s.AccessToken})
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatal("Native Messaging write alias added")
		}
	}
	for _, op := range []struct {
		method, path string
		status       int
	}{{"GET", "/moderation/messages/reports", 401}, {"PUT", "/user/messaging/preferences", 401}, {"POST", "/user/messages/missing/report", 401}, {"GET", "/api/collections/messaging_message/records", 403}} {
		r := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + s.AccessToken})
		r.Body.Close()
		if r.StatusCode != op.status {
			t.Fatal("write adapter widened unrelated scope")
		}
	}
}

func TestCanonicalNativeMessagingWritesRequireExplicitIdentityWhenRequesterAlsoResponds(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	arcade, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Synthetic dual identity", Address: "Synthetic", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{arcade})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	venue, err := app.FindRecordById("arcade", arcade)
	if err != nil {
		t.Fatal(err)
	}
	venue.Set("inquiry_settings", map[string]any{"mode": "internal"})
	if err := app.Save(venue); err != nil {
		t.Fatal(err)
	}
	_, session := nativeNearbySession(t, app, owner)
	room := nativeBulkRequest(t, app, "POST", "/user/conversations", session.AccessToken, map[string]any{"kind": "inquiry", "inbox": map[string]any{"type": "arcade", "id": arcade}, "initialMessage": map[string]any{"text": "Synthetic owner inquiry", "clientMessageId": "dual-first"}}, 201).(map[string]any)
	if len(room["allowedSenderIdentities"].([]any)) != 2 {
		t.Fatal("server lost dual sender capabilities")
	}
	base := "/user/conversations/" + room["id"].(string)
	before := nativeMessagingState(t, app)
	nativeBulkRequest(t, app, "POST", base+"/messages", session.AccessToken, map[string]any{"text": "Must choose", "clientMessageId": "dual-missing"}, 400)
	if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("ambiguous sender wrote state")
	}
	for _, identity := range []map[string]any{{"type": "user", "id": owner.Id}, {"type": "arcade", "id": arcade}} {
		body := map[string]any{"text": "Explicit synthetic identity", "clientMessageId": "dual-" + identity["type"].(string), "actingAs": identity}
		result := nativeBulkRequest(t, app, "POST", base+"/messages", session.AccessToken, body, 201).(map[string]any)
		if result["senderUserId"] != owner.Id || result["actingAs"].(map[string]any)["type"] != identity["type"] || !reflect.DeepEqual(result, nativeBulkRequest(t, app, "POST", base+"/messages", web, body, 200)) {
			t.Fatal("Native/Web explicit sender identity differs")
		}
	}
}
