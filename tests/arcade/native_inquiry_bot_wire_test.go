package arcade_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/messaging"
)

func TestCanonicalNativeInquiryBotChoicesActualWireAndRetryIdentity(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, requester := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, requester)
	_, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, ownerSession := nativeNearbySession(t, app, owner)
	venue, _ := seedPublicArcade(t, app, requester.Id, arcadeSeed{Name: "Synthetic bot venue", Address: "Synthetic road", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{venue})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	a, err := app.FindRecordById("arcade", venue)
	if err != nil {
		t.Fatal(err)
	}
	a.Set("inquiry_settings", map[string]any{"mode": "internal", "loginRequired": true})
	if err := app.Save(a); err != nil {
		t.Fatal(err)
	}
	cfg := messaging.BotConfig{Enabled: true, Start: "start", Nodes: []messaging.BotNode{
		{ID: "start", Text: "Synthetic welcome", Choices: []messaging.BotChoice{{ID: "choice/a", Label: " Same label ", Next: "menu"}, {ID: "\u9078\u629e", Label: "Same label", Next: "menu"}}},
		{ID: "menu", Text: "Synthetic menu", Choices: []messaging.BotChoice{{ID: "next/\u9078\u629e", Label: "Continue", Next: "end"}}},
		{ID: "end", Text: "Synthetic terminal", Choices: []messaging.BotChoice{}},
	}}
	if _, err := messaging.SetArcadeBot(app, owner.Id, venue, cfg); err != nil {
		t.Fatal(err)
	}
	cases := map[string]any{}
	save := func(name, method, path string, request, response any, status int) {
		cases[name] = map[string]any{"method": method, "path": path, "request": request, "response": response, "status": status, "actor": requester.Id}
	}
	previewPath := "/user/inquiry-preview?context=arcade&inboxType=arcade&inboxId=" + venue
	before := nativeMessagingState(t, app)
	preview := nativeBulkRequest(t, app, "GET", previewPath, session.AccessToken, nil, 200).(map[string]any)
	// createdAt is request-time metadata, not a stable backend difference.
	webPreview := nativeBulkRequest(t, app, "GET", previewPath, web, nil, 200).(map[string]any)
	delete(webPreview, "createdAt")
	stable := map[string]any{}
	for k, v := range preview {
		if k != "createdAt" {
			stable[k] = v
		}
	}
	if !reflect.DeepEqual(stable, webPreview) || !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
		t.Fatal("preview parity/read-only changed")
	}
	save("preview", "GET", previewPath, nil, preview, 200)
	for locale, label := range map[string]string{"ko": "\ucc98\uc74c\uc73c\ub85c \ub3cc\uc544\uac00\uae30", "en": "Back to the start", "ja": "\u6700\u521d\u306b\u623b\u308b"} {
		web, requester := createAuthUser(t, app)
		_, session := nativeNearbySession(t, app, requester)
		input := map[string]any{"kind": "inquiry", "inbox": map[string]any{"type": "arcade", "id": venue}, "inquiryContext": "arcade", "botRevision": preview["revision"], "initialChoice": "choice/a", "initialMessage": map[string]any{"text": "Same label", "clientMessageId": "initial-" + locale}}
		stale := map[string]any{}
		for k, v := range input {
			stale[k] = v
		}
		stale["botRevision"] = "stale"
		before = nativeMessagingState(t, app)
		nativeBulkRequest(t, app, "POST", "/user/conversations", session.AccessToken, stale, 409)
		if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
			t.Fatal("stale preview left state")
		}
		room := nativeBulkRequest(t, app, "POST", "/user/conversations", session.AccessToken, input, 201).(map[string]any)
		if room["latestSequence"] != float64(3) {
			t.Fatal("initial choice transition count changed")
		}
		before = nativeMessagingState(t, app)
		if !reflect.DeepEqual(room, nativeBulkRequest(t, app, "POST", "/user/conversations", web, input, 200)) || !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
			t.Fatal("initial Web replay changed state")
		}
		changed := map[string]any{}
		for k, v := range input {
			changed[k] = v
		}
		changed["initialChoice"] = "\u9078\u629e"
		nativeBulkRequest(t, app, "POST", "/user/conversations", session.AccessToken, changed, 409)
		if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
			t.Fatal("same-label different initial choice reused key")
		}
		base := "/user/conversations/" + room["id"].(string)
		selection := map[string]any{"text": "Continue", "clientMessageId": "reply-" + locale, "actingAs": map[string]any{"type": "user", "id": requester.Id}, "botChoice": map[string]any{"messageId": room["lastMessage"].(map[string]any)["id"], "choiceId": "next/\u9078\u629e"}}
		nativeBulkRequest(t, app, "POST", base+"/messages", ownerSession.AccessToken, selection, 403)
		reply := nativeBulkRequest(t, app, "POST", base+"/messages", session.AccessToken, selection, 201).(map[string]any)
		before = nativeMessagingState(t, app)
		if !reflect.DeepEqual(reply, nativeBulkRequest(t, app, "POST", base+"/messages", web, selection, 200)) || !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
			t.Fatal("reply replay duplicated state")
		}
		altered := map[string]any{}
		for k, v := range selection {
			altered[k] = v
		}
		altered["botChoice"] = map[string]any{"messageId": "other-message", "choiceId": "next/\u9078\u629e"}
		nativeBulkRequest(t, app, "POST", base+"/messages", session.AccessToken, altered, 409)

		altered["botChoice"] = map[string]any{"messageId": room["lastMessage"].(map[string]any)["id"], "choiceId": "different-choice-id"}
		nativeBulkRequest(t, app, "POST", base+"/messages", session.AccessToken, altered, 409)
		if !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
			t.Fatal("same-label different bot identity changed state")
		}

		current := nativeBulkRequest(t, app, "GET", base, session.AccessToken, nil, 200).(map[string]any)
		last := current["lastMessage"].(map[string]any)
		if last["botCanRestart"] != true {
			t.Fatal("server restart capability lost")
		}
		restart := map[string]any{"text": label, "clientMessageId": "restart-" + locale, "actingAs": map[string]any{"type": "user", "id": requester.Id}, "botChoice": map[string]any{"messageId": last["id"], "choiceId": "__restart"}}
		result := nativeBulkRequest(t, app, "POST", base+"/messages", session.AccessToken, restart, 201).(map[string]any)
		before = nativeMessagingState(t, app)
		if !reflect.DeepEqual(result, nativeBulkRequest(t, app, "POST", base+"/messages", web, restart, 200)) || !reflect.DeepEqual(before, nativeMessagingState(t, app)) {
			t.Fatal("restart replay duplicated state")
		}
		save("initial-"+locale, "POST", "/user/conversations", input, room, 201)
		save("reply-"+locale, "POST", base+"/messages", selection, reply, 201)
		save("restart-"+locale, "POST", base+"/messages", restart, result, 201)
	}
	if dir := os.Getenv("MUSECAT_NATIVE_BOT_WIRE_FIXTURES"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		raw, err := json.MarshalIndent(cases, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "inquiry-bot-cases.json"), append(raw, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
