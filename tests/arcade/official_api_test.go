package arcade_test

import (
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"testing"
	"time"
)

func TestOfficialAPIManagementRetainsOwnershipAndRevocation(t *testing.T) {
	app := newArcadeTestApp(t)
	session, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	otherSession, _ := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	arcade, _ := seedPublicArcade(t, app, "", arcadeSeed{Name: "Official API", Address: "Seoul", Location: location{Lat: 37.5, Lon: 127}})
	addOwnedArcade(t, app, owner, arcade)
	key, keyID := issueBotKey(t, app, session, "Official integration")
	memo, _ := json.Marshal(map[string]any{"arcade": arcade, "document": noticeDocument("API memo")})
	botRequest(t, app, key, "PUT", "/bot/v1/arcade/memo", string(memo), 200)
	botRequest(t, app, key, "GET", "/bot/v1/arcade/memo?arcade="+arcade, "", 200)
	botRequest(t, app, key, "GET", "/bot/v1/arcade/analytics?arcade="+arcade, "", 200)
	flag := createFlagWithReactions(t, app, arcade, owner.Id, time.Now().UTC(), nil)
	resolved := botRequest(t, app, key, "POST", "/bot/v1/arcade/flag/reaction", fmt.Sprintf(`{"flag":%q,"reaction":"fixed","action":"add"}`, flag), 200)
	if resolved["solved"] != true {
		t.Fatal("official API did not resolve report")
	}

	other, _ := issueBotKey(t, app, otherSession, "Foreign integration")
	raw, _ := json.Marshal(policyNoticeBody(arcade, "required"))
	saved := botRequest(t, app, key, "POST", "/bot/v1/arcade/notice", string(raw), 200)
	notice := saved["id"].(string)
	botRequest(t, app, other, "POST", "/bot/v1/arcade/notice", string(raw), 403)
	botRequest(t, app, other, "PUT", "/bot/v1/arcade/notice", fmt.Sprintf(`{"id":%q,"title":"Foreign"}`, notice), 403)
	botRequest(t, app, other, "DELETE", "/bot/v1/arcade/notice", fmt.Sprintf(`{"id":%q}`, notice), 403)
	botRequest(t, app, key, "PUT", "/bot/v1/arcade/notice", fmt.Sprintf(`{"id":%q,"title":"Updated"}`, notice), 200)
	botRequest(t, app, key, "GET", "/bot/v1/arcade/notice?arcade="+arcade, "", 200)
	botRequest(t, app, key, "POST", "/bot/v1/arcade/notice", string(raw), 409)
	for _, path := range []string{"/arcade/report-settings", "/arcade/inquiry-settings"} {
		body := fmt.Sprintf(`{"arcade":%q,"mode":"internal"}`, arcade)
		botRequest(t, app, key, "PUT", "/bot/v1"+path, body, 200)
		botRequest(t, app, other, "PUT", "/bot/v1"+path, body, 403)
	}
	botRequest(t, app, key, "GET", "/bot/v1/arcade/report-settings?arcade="+arcade, "", 200)
	botRequest(t, app, other, "GET", "/bot/v1/arcade/report-settings?arcade="+arcade, "", 403)
	botRequest(t, app, key, "GET", "/bot/v1/user/arcade-inquiry-onboarding", "", 200)
	botRequest(t, app, key, "GET", "/bot/v1/user/inquiry-bots/arcade/"+arcade, "", 200)
	botRequest(t, app, other, "GET", "/bot/v1/user/inquiry-bots/arcade/"+arcade, "", 403)
	botRequest(t, app, key, "PUT", "/bot/v1/user/inquiry-bots/arcade/"+arcade, `{"enabled":false,"start":"","nodes":[]}`, 200)
	botRequest(t, app, key, "DELETE", "/bot/v1/arcade/notice", fmt.Sprintf(`{"id":%q}`, notice), 200)
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	botRequest(t, app, key, "PUT", "/bot/v1/arcade/inquiry-settings", fmt.Sprintf(`{"arcade":%q,"mode":"disabled"}`, arcade), 403)
	botRequest(t, app, session, "DELETE", "/user/bot-keys/"+keyID, "", 204)
	botRequest(t, app, key, "GET", "/bot/v1/user/conversations", "", 401)
}

func TestOfficialAPIInquiryRepliesRetainConversationPrivacy(t *testing.T) {
	app := newArcadeTestApp(t)
	session, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	otherSession, _ := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, requester := createAuthUserWithTags(t, app, []string{})
	arcade, _ := seedPublicArcade(t, app, "", arcadeSeed{Name: "API inquiries", Address: "Seoul", Location: location{Lat: 37.5, Lon: 127}})
	addOwnedArcade(t, app, owner, arcade)
	key, _ := issueBotKey(t, app, session, "Replies")
	other, _ := issueBotKey(t, app, otherSession, "Foreign")
	botRequest(t, app, key, "PUT", "/bot/v1/arcade/inquiry-settings", fmt.Sprintf(`{"arcade":%q,"mode":"internal"}`, arcade), 200)
	conversation, err := messaging.Create(app, requester.Id, messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "arcade", ID: arcade}, InitialMessage: messaging.SendInput{Text: "Opening hours?", ClientMessageID: "initial"}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/bot/v1/user/conversations/" + conversation.ID
	botRequest(t, app, key, "GET", "/bot/v1/user/conversations", "", 200)
	botRequest(t, app, key, "GET", path, "", 200)
	body := `{"text":"Open at ten","clientMessageId":"official-api-reply"}`
	first := botRequest(t, app, key, "POST", path+"/messages", body, 201)
	again := botRequest(t, app, key, "POST", path+"/messages", body, 200)
	if first["id"] != again["id"] {
		t.Fatal("retry duplicated reply")
	}
	botRequest(t, app, key, "GET", path+"/messages", "", 200)
	botRequest(t, app, key, "PUT", path+"/read", `{"throughSequence":1}`, 200)
	botRequest(t, app, key, "PATCH", path+"/inquiry", `{"status":"solved"}`, 200)
	for _, route := range []string{path, path + "/messages"} {
		botRequest(t, app, other, "GET", route, "", 404)
	}
	botRequest(t, app, other, "POST", path+"/messages", body, 404)
	botRequest(t, app, other, "PATCH", path+"/inquiry", `{"status":"solved"}`, 404)
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	botRequest(t, app, key, "GET", path, "", 404)
	botRequest(t, app, key, "POST", path+"/messages", `{"text":"Late reply","clientMessageId":"late"}`, 404)
}
