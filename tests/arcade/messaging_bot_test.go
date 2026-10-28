package arcade_test

import (
	"encoding/json"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"strings"
	"testing"
)

func TestMessagingVerificationBotPreviewAndPersistedHistory(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUserWithTags(t, app, []string{})
	in := messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "team", ID: "musecat"}, InquiryContext: "arcadeOfficial.ko-KR", InitialMessage: messaging.SendInput{ClientMessageID: "verification"}}
	for range 3 {
		preview, err := messaging.PreviewInquiry(app, messaging.Principal{UserID: user.Id}, in)
		if err != nil || !strings.Contains(preview.Text, "게임제공업 등록증") {
			t.Fatal(preview, err)
		}
		in.BotRevision = preview.Revision
	}
	count, _ := app.CountRecords(messaging.Conversations)
	if count != 0 {
		t.Fatal("opening/reading guidance created empty rooms", count)
	}
	if _, err := messaging.Create(app, user.Id, in); err == nil {
		t.Fatal("empty first response created a room")
	}
	in.InitialMessage.Text = "인증 문의합니다."
	created, replay, err := messaging.CreateWithReplay(app, user.Id, in)
	if err != nil || replay || created.LatestSequence != 2 {
		t.Fatalf("create: %+v %v %v", created, replay, err)
	}
	again, replay, err := messaging.CreateWithReplay(app, user.Id, in)
	if err != nil || !replay || again.ID != created.ID {
		t.Fatal("first response retry duplicated the room", err)
	}
	body := `{"text":"별빛 게임센터입니다.","clientMessageId":"reply"}`
	response := executeJSONRequest(t, app, "POST", "/user/conversations/"+created.ID+"/messages", body, map[string]string{"Authorization": "Bearer " + token})
	response.Body.Close()
	if response.StatusCode != 201 {
		t.Fatal(response.StatusCode)
	}
	response = executeJSONRequest(t, app, "GET", "/user/conversations/"+created.ID+"/messages", "", map[string]string{"Authorization": "Bearer " + token})
	defer response.Body.Close()
	var page messaging.MessagePage
	if err = json.NewDecoder(response.Body).Decode(&page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 3 || !page.Items[0].Automated || page.Items[0].Sender.Type != "team" || !strings.Contains(page.Items[0].Text, "게임제공업 등록증") || page.Items[2].Text != "별빛 게임센터입니다." {
		t.Fatalf("persisted history: %+v", page)
	}
	normal, err := messaging.Create(app, user.Id, messaging.CreateInput{Kind: "inquiry", Inbox: in.Inbox, InitialMessage: messaging.SendInput{Text: "일반 문의", ClientMessageID: "normal"}})
	if err != nil || normal.LatestSequence != 1 || normal.LastMessage.Automated {
		t.Fatal("normal team inquiry must not activate verification bot", err)
	}
	bad := in
	bad.Inbox = messaging.Identity{Type: "arcade", ID: "wrong"}
	if _, err = messaging.PreviewInquiry(app, messaging.Principal{UserID: user.Id}, bad); err == nil {
		t.Fatal("verification preview accepted another inbox")
	}
}
func TestMessagingArcadeBotChoicesSnapshotRestartAndHandoff(t *testing.T) {
	app := newArcadeTestApp(t)
	_, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, requester := createAuthUserWithTags(t, app, []string{})
	_, outsider := createAuthUserWithTags(t, app, []string{})
	arcadeID, _ := seedPublicArcade(t, app, requester.Id, arcadeSeed{Name: "별빛 게임센터", Address: "주소", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{arcadeID})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	arcade, _ := app.FindRecordById("arcade", arcadeID)
	arcade.Set("inquiry_settings", map[string]any{"mode": "internal", "loginRequired": false})
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	config := messaging.BotConfig{Enabled: true, Start: "welcome", Nodes: []messaging.BotNode{
		{ID: "welcome", Text: "무엇을 도와드릴까요?", Choices: []messaging.BotChoice{{ID: "startLost", Label: " 분실물 문의 ", Next: "menu"}}},
		{ID: "menu", Text: "어떤 물건을 찾으시나요?", Choices: []messaging.BotChoice{{ID: "lost", Label: "소지품을 두고 왔어요", Next: "lost"}}},
		{ID: "lost", Text: "두고 간 물건과 방문 날짜를 알려 주세요.", Choices: []messaging.BotChoice{}},
	}}
	if _, err := messaging.SetArcadeBot(app, outsider.Id, arcadeID, config); err == nil {
		t.Fatal("non-owner changed bot")
	}
	if _, err := messaging.SetArcadeBot(app, owner.Id, arcadeID, config); err != nil {
		t.Fatal(err)
	}
	in := messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "arcade", ID: arcadeID}, InquiryContext: "arcade", InitialMessage: messaging.SendInput{ClientMessageID: "start", Text: "분실물 문의"}, InitialChoice: "startLost"}
	preview, err := messaging.PreviewInquiry(app, messaging.Principal{UserID: requester.Id}, in)
	if err != nil {
		t.Fatal(err)
	}
	in.BotRevision = preview.Revision
	stale := in
	stale.BotRevision = "outdated"
	if _, err = messaging.Create(app, requester.Id, stale); err == nil {
		t.Fatal("changed preview accepted")
	}
	n, _ := app.CountRecords(messaging.Conversations)
	if n != 0 {
		t.Fatal("rejected preview left an empty room")
	}
	room, err := messaging.Create(app, requester.Id, in)
	if err != nil || room.LatestSequence != 3 || room.LastMessage.Text != "어떤 물건을 찾으시나요?" {
		t.Fatal(room, err)
	}
	if replayed, replay, err := messaging.CreateWithReplay(app, requester.Id, in); err != nil || !replay || replayed.ID != room.ID {
		t.Fatal("initial choice retry duplicated the room", err)
	}
	config.Nodes[2].Text = "changed for future inquiries"
	if _, err = messaging.SetArcadeBot(app, owner.Id, arcadeID, config); err != nil {
		t.Fatal(err)
	}
	selection := messaging.SendInput{Text: "소지품을 두고 왔어요", ClientMessageID: "choice", BotChoice: &messaging.BotSelection{MessageID: room.LastMessage.ID, ChoiceID: "lost"}}
	if _, _, err = messaging.Send(app, requester.Id, room.ID, selection); err != nil {
		t.Fatal(err)
	}
	if _, replay, err := messaging.Send(app, requester.Id, room.ID, selection); err != nil || !replay {
		t.Fatal("choice retry duplicated reply", err)
	}
	got, err := messaging.Get(app, requester.Id, room.ID)
	if err != nil || got.LatestSequence != 5 || got.LastMessage.Text != "두고 간 물건과 방문 날짜를 알려 주세요." {
		t.Fatal("snapshot", got, err)
	}
	if !got.LastMessage.BotCanRestart {
		t.Fatal("terminal reply must allow returning to start")
	}
	restart := messaging.SendInput{Text: "처음으로 돌아가기", ClientMessageID: "restart", BotChoice: &messaging.BotSelection{MessageID: got.LastMessage.ID, ChoiceID: "__restart"}}
	if _, _, err = messaging.Send(app, requester.Id, room.ID, restart); err != nil {
		t.Fatal(err)
	}
	if _, replay, err := messaging.Send(app, requester.Id, room.ID, restart); err != nil || !replay {
		t.Fatal("restart retry duplicated replies", err)
	}
	got, err = messaging.Get(app, requester.Id, room.ID)
	if err != nil || got.LatestSequence != 7 || got.LastMessage.Text != "무엇을 도와드릴까요?" {
		t.Fatal("restart lost history", got, err)
	}
	selection.ClientMessageID = "stale"
	if _, _, err = messaging.Send(app, requester.Id, room.ID, selection); err == nil {
		t.Fatal("stale selection accepted")
	}
	if _, _, err = messaging.Send(app, requester.Id, room.ID, messaging.SendInput{Text: "검은 지갑을 두고 왔어요", ClientMessageID: "human"}); err != nil {
		t.Fatal(err)
	}
	got, err = messaging.Get(app, requester.Id, room.ID)
	if err != nil || got.LatestSequence != 8 || got.LastMessage.Automated || got.LastMessage.BotCanRestart {
		t.Fatal("human handoff", got, err)
	}
}
