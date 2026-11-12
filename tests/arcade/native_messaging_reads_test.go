package arcade_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func exportNativeMessaging(t *testing.T, name string, value any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_MESSAGING_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "email", "access_token", "refresh_token", "guest_key_hash"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+key+`"`) {
			t.Fatal("credential field in messaging DTO")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func nativeMessagingState(t *testing.T, app core.App) map[string]string {
	t.Helper()
	out := nativeCampaignState(t, app)
	for _, name := range []string{messaging.Conversations, messaging.Parties, messaging.Messages, messaging.Inquiries, messaging.Reads} {
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
func nativeMessagingPhoto(t *testing.T, app *tests.TestApp, path, token string, status int) []byte {
	t.Helper()
	r := executeJSONRequest(t, app, "GET", path, "", map[string]string{"Authorization": "Bearer " + token})
	defer r.Body.Close()
	if r.StatusCode != status {
		t.Fatal("messaging attachment status", r.StatusCode, status)
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if status == 200 && r.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("private attachment became cacheable")
	}
	return raw
}
func TestCanonicalNativeMessagingReadsMatchWebDMHistoryAndPrivateAttachment(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, a := createAuthUser(t, app)
	bWeb, b := createAuthUser(t, app)
	staffWeb, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, other := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, a)
	_, peer := nativeNearbySession(t, app, b)
	_, staffSession := nativeNearbySession(t, app, staff)
	_, otherSession := nativeNearbySession(t, app, other)
	c := nativeBulkRequest(t, app, "POST", "/user/conversations", web, map[string]any{"kind": "dm", "peerUserId": b.Id}, 200).(map[string]any)
	id := c["id"].(string)
	base := "/user/conversations/" + id
	otherConversation := nativeBulkRequest(t, app, "POST", "/user/conversations", web, map[string]any{"kind": "dm", "peerUserId": other.Id}, 200).(map[string]any)
	nativeBulkRequest(t, app, "POST", "/user/conversations/"+otherConversation["id"].(string)+"/messages", web, map[string]any{"text": "Other synthetic conversation", "clientMessageId": "other-first"}, 201)
	for _, text := range []string{"First synthetic message", "Second synthetic message"} {
		nativeBulkRequest(t, app, "POST", base+"/messages", web, map[string]any{"text": text, "clientMessageId": text}, 201)
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	_ = form.WriteField("text", "Synthetic photo")
	_ = form.WriteField("clientMessageId", "synthetic-photo")
	part, err := form.CreateFormFile("photos", "synthetic.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(pngFixtureBytes())
	_ = form.Close()
	r := executeRequest(t, app, "POST", base+"/messages", &body, map[string]string{"Authorization": "Bearer " + web, "Content-Type": form.FormDataContentType()})
	var message messaging.Message
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil || r.StatusCode != 201 {
		t.Fatal("synthetic attachment setup failed", err)
	}
	r.Body.Close()
	nativeBulkRequest(t, app, "PUT", base+"/read", bWeb, map[string]any{"throughSequence": 1}, 200)
	before := nativeMessagingState(t, app)
	for _, path := range []string{"/user/conversations?limit=1", base, base + "/messages?limit=1", base + "/messages?beforeSequence=3&limit=1", base + "/messages?afterSequence=0&limit=1"} {
		actual := nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 200)
		expected := nativeBulkRequest(t, app, "GET", path, web, nil, 200)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatal("Native/Web DM read DTO differs")
		}
	}
	detail := nativeBulkRequest(t, app, "GET", base, session.AccessToken, nil, 200).(map[string]any)
	if detail["counterpartReadSequence"] != float64(1) || detail["lastReadSequence"] != float64(0) {
		t.Fatal("DM read cursors conflated")
	}
	exportNativeMessaging(t, "messaging-dm", detail)
	list := nativeBulkRequest(t, app, "GET", "/user/conversations?limit=1", session.AccessToken, nil, 200)
	exportNativeMessaging(t, "messaging-list", list)
	cursor := list.(map[string]any)["nextCursor"]
	if cursor == nil {
		t.Fatal("list fixture did not exercise pagination")
	}
	second := nativeBulkRequest(t, app, "GET", "/user/conversations?limit=1&cursor="+cursor.(string), session.AccessToken, nil, 200).(map[string]any)
	if len(second["items"].([]any)) != 1 || second["items"].([]any)[0].(map[string]any)["id"] == list.(map[string]any)["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("conversation cursor did not advance")
	}

	for _, sample := range []struct{ name, query string }{{"messaging-history", "?limit=1"}, {"messaging-before", "?beforeSequence=3&limit=1"}, {"messaging-delta", "?afterSequence=0&limit=1"}} {
		exportNativeMessaging(t, sample.name, nativeBulkRequest(t, app, "GET", base+"/messages"+sample.query, session.AccessToken, nil, 200))
	}
	raw := nativeMessagingPhoto(t, app, message.Photos[0].URL, session.AccessToken, 200)
	if !bytes.Equal(raw, pngFixtureBytes()) || !bytes.Equal(raw, nativeMessagingPhoto(t, app, message.Photos[0].URL, bWeb, 200)) {
		t.Fatal("Native/Web attachment bytes differ")
	}
	if dir := os.Getenv("MUSECAT_CANONICAL_MESSAGING_FIXTURES"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "messaging-photo.png"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, token := range []string{staffWeb, staffSession.AccessToken, otherSession.AccessToken} {
		nativeBulkRequest(t, app, "GET", base, token, nil, 404)
		nativeBulkRequest(t, app, "GET", base+"/messages", token, nil, 404)
		nativeMessagingPhoto(t, app, message.Photos[0].URL, token, 404)
	}
	nativeMessagingPhoto(t, app, base+"/attachments/incorrect/"+message.Photos[0].Name, peer.AccessToken, 404)
	for _, query := range []string{"?beforeSequence=1&afterSequence=0", "?afterSequence=-1", "?limit=201", "?beforeSequence=3&limit=101"} {
		nativeBulkRequest(t, app, "GET", base+"/messages"+query, session.AccessToken, nil, 400)
	}
	if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("read-only messaging changed messages or read cursors")
	}
	nativeBulkRequest(t, app, "PUT", "/user/messaging/preferences", bWeb, map[string]any{"receive": "nobody"}, 200)
	blocked := nativeBulkRequest(t, app, "GET", base, session.AccessToken, nil, 200).(map[string]any)
	if blocked["canSend"] != false || len(blocked["allowedSenderIdentities"].([]any)) != 0 || !reflect.DeepEqual(blocked, nativeBulkRequest(t, app, "GET", base, web, nil, 200)) {
		t.Fatal("receive opt-out did not remove send capability equally")
	}
	nativeBulkRequest(t, app, "GET", base+"/messages", session.AccessToken, nil, 200)

}

func TestCanonicalNativeMessagingInquiryCapabilitiesGuestSeparationAndOwnership(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, requester := createAuthUser(t, app)
	ownerWeb, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	staffWeb, staff := createAuthUserWithTags(t, app, []string{"developer"})
	_, outsider := createAuthUser(t, app)
	arcade, _ := seedPublicArcade(t, app, requester.Id, arcadeSeed{Name: "Synthetic inquiry venue", Address: "Synthetic", Location: location{Lat: 37.5, Lon: 127}})
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
	_, st := nativeNearbySession(t, app, staff)
	_, foreign := nativeNearbySession(t, app, outsider)
	c := nativeBulkRequest(t, app, "POST", "/user/conversations", web, map[string]any{"kind": "inquiry", "inbox": map[string]any{"type": "arcade", "id": arcade}, "initialMessage": map[string]any{"text": "Synthetic inquiry", "clientMessageId": "inquiry-first"}}, 201).(map[string]any)
	id := c["id"].(string)
	base := "/user/conversations/" + id
	inquiryRecord, err := app.FindFirstRecordByFilter(messaging.Inquiries, "conversation={:c}", dbx.Params{"c": id})
	if err != nil {
		t.Fatal(err)
	}
	inquiryRecord.Set("legacy_feedback_id", "legacyinquiry001")
	if err := app.Save(inquiryRecord); err != nil {
		t.Fatal(err)
	}
	if resolved, err := messaging.LegacyConversation(app, "legacyinquiry001"); err != nil || resolved != id {
		t.Fatal("existing legacy mapping changed")
	}
	nativeBulkRequest(t, app, "POST", base+"/messages", ownerWeb, map[string]any{"text": "Synthetic responder reply", "clientMessageId": "owner-reply", "actingAs": map[string]any{"type": "arcade", "id": arcade}}, 201)
	for _, sample := range []struct {
		token, web, name string
		responder        bool
	}{{s.AccessToken, web, "messaging-inquiry-requester", false}, {o.AccessToken, ownerWeb, "messaging-inquiry-responder", true}, {st.AccessToken, staffWeb, "", true}} {
		actual := nativeBulkRequest(t, app, "GET", base, sample.token, nil, 200).(map[string]any)
		if !reflect.DeepEqual(actual, nativeBulkRequest(t, app, "GET", base, sample.web, nil, 200)) {
			t.Fatal("inquiry Web/Native DTO differs")
		}
		if actual["canSend"] != true || len(actual["allowedSenderIdentities"].([]any)) != 1 {
			t.Fatal("inquiry sender capabilities lost")
		}
		if sample.responder {
			if len(actual["allowedStatusTransitions"].([]any)) != 3 {
				t.Fatal("responder status capability lost")
			}
		} else if actual["status"] != nil || len(actual["allowedStatusTransitions"].([]any)) != 0 {
			t.Fatal("requester received internal status")
		}
		if sample.name != "" {
			exportNativeMessaging(t, sample.name, actual)
		}
	}
	resolverPath := "/user/feedback/thread?id=legacyinquiry001"
	resolver := nativeBulkRequest(t, app, "GET", resolverPath, s.AccessToken, nil, 200).(map[string]any)
	if resolver["conversationId"] != id || !reflect.DeepEqual(resolver, nativeBulkRequest(t, app, "GET", resolverPath, web, nil, 200)) {
		t.Fatal("official legacy resolver changed mapping or Web DTO")
	}
	exportNativeMessaging(t, "messaging-resolver", resolver)
	nativeBulkRequest(t, app, "GET", resolverPath, foreign.AccessToken, nil, 404)
	nativeBulkRequest(t, app, "GET", base, foreign.AccessToken, nil, 404)
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	nativeBulkRequest(t, app, "GET", base, o.AccessToken, nil, 404)
	nativeBulkRequest(t, app, "GET", base+"/messages", o.AccessToken, nil, 404)
	nativeBulkRequest(t, app, "GET", resolverPath, o.AccessToken, nil, 404)
	// Guest keys authorize only guest routes; Native identity never turns into a guest.
	key := strings.Repeat("7", 64)
	guest, _, err := messaging.CreateFor(app, messaging.Principal{GuestKey: key}, messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "team", ID: "musecat"}, InitialMessage: messaging.SendInput{Text: "Synthetic guest inquiry", ClientMessageID: "guest-first"}})
	if err != nil {
		t.Fatal(err)
	}
	nativeBulkRequest(t, app, "GET", "/user/conversations/"+guest.ID, s.AccessToken, nil, 404)
	r := executeJSONRequest(t, app, "GET", "/guest/conversations/"+guest.ID, "", map[string]string{"X-Feedback-Guest-Key": key})
	r.Body.Close()
	if r.StatusCode != 200 {
		t.Fatal("existing guest read changed")
	}
	r = executeJSONRequest(t, app, "GET", "/guest/conversations/"+guest.ID, "", map[string]string{"Authorization": "Bearer " + s.AccessToken})
	r.Body.Close()
	if r.StatusCode != 401 {
		t.Fatal("Native identity acquired guest credential")
	}
}

func TestCanonicalNativeMessagingPreviewIsWebEquivalentAndReadOnly(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, user := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, user)
	before := nativeMessagingState(t, app)
	path := "/user/inquiry-preview?context=arcadeOfficial.ko-KR&inboxType=team&inboxId=musecat"
	out := nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 200).(map[string]any)
	webOut := nativeBulkRequest(t, app, "GET", path, web, nil, 200).(map[string]any)
	comparable := func(v map[string]any) map[string]any {
		result := map[string]any{}
		for k, value := range v {
			if k != "createdAt" {
				result[k] = value
			}
		}
		return result
	}
	if !reflect.DeepEqual(comparable(out), comparable(webOut)) {
		t.Fatal("Native/Web preview differs")
	}
	exportNativeMessaging(t, "messaging-preview", out)
	if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("preview created a conversation/message")
	}
}

func TestCanonicalNativeMessagingRejectsBadExpiredInactiveAndDisabledSessions(t *testing.T) {
	paths := []string{"/user/feedback/thread?id=missing", "/user/conversations", "/user/conversations/missing", "/user/conversations/missing/messages", "/user/conversations/missing/attachments/missing/file.png", "/user/inquiry-preview"}
	app := nativeNearbyApp(t, true)
	web, user := createAuthUser(t, app)
	service, session := nativeNearbySession(t, app, user)
	reject := func(token string, status int) {
		for _, path := range paths {
			nativeBulkRequest(t, app, "GET", path, token, nil, status)
		}
	}
	reject("", 401)
	reject(strings.Repeat("z", 43), 401)
	for _, path := range paths {
		r := executeJSONRequest(t, app, "GET", path, "", map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
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
				reject(session.AccessToken, 401)
			}
		}
	}
	user.Set("withdrawn", true)
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	reject(session.AccessToken, 401)
	user.Set("withdrawn", false)
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	seedUserBan(t, app, user.Id, "", "native_messaging_fixture", time.Now().Add(time.Hour))
	reject(session.AccessToken, 403)
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	reject(session.AccessToken, 401)
	disabled := nativeNearbyApp(t, false)
	disabledWeb, actor := createAuthUser(t, disabled)
	s, issued := nativeNearbySession(t, disabled, actor)
	if got, err := s.Authenticate(disabled, issued.AccessToken); err != nil || got.Id != actor.Id {
		t.Fatal("invalid disabled same-DB fixture")
	}
	for _, path := range paths {
		r := executeJSONRequest(t, disabled, "GET", path, "", map[string]string{"Authorization": "Bearer " + issued.AccessToken})
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal("disabled Native adapter authenticated")
		}
	}
	nativeBulkRequest(t, disabled, "GET", "/user/conversations", disabledWeb, nil, 200)
}

func TestCanonicalNativeMessagingReadsPreserveRateLimitsAndExcludeWritesEvents(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, user := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, user)
	for _, op := range []struct {
		path   string
		status int
	}{{"/user/feedback/thread?id=missing", 404}, {"/user/conversations", 200}, {"/user/conversations/missing", 404}, {"/user/conversations/missing/messages", 404}, {"/user/conversations/missing/attachments/missing/file.png", 404}, {"/user/inquiry-preview", 400}} {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "GET " + strings.Split(op.path, "?")[0], MaxRequests: 1, Duration: 60}}}
		nativeBulkRequest(t, app, "GET", op.path, session.AccessToken, nil, op.status)
		nativeBulkRequest(t, app, "GET", op.path, session.AccessToken, nil, 429)
		r := executeJSONRequest(t, app, "GET", "/native"+op.path, "", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatal("Native messaging alias added")
		}
	}
	for _, op := range []struct {
		method, path string
		status       int
	}{{"POST", "/user/conversations", 401}, {"POST", "/user/conversations/missing/messages", 401}, {"PUT", "/user/conversations/missing/read", 401}, {"PATCH", "/user/conversations/missing/inquiry", 401}, {"GET", "/user/events", 401}, {"GET", "/user/messaging/preferences", 401}, {"GET", "/api/collections/messaging_message/records", 403}} {
		r := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		r.Body.Close()
		if r.StatusCode != op.status {
			t.Fatal("read adapter widened unrelated scope")
		}
	}
}
