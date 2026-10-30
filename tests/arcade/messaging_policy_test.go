package arcade_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestMessagingInquiryPermissionsAndOwnUnread(t *testing.T) {
	app := newArcadeTestApp(t)
	_, requester := createAuthUserWithTags(t, app, []string{})
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	c, err := messaging.Create(app, requester.Id, messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "team", ID: "musecat"}, InitialMessage: messaging.SendInput{Text: "help", ClientMessageID: "initial"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user       *core.Record
		identities []messaging.Identity
		statuses   []string
	}{
		{requester, []messaging.Identity{{Type: "user", ID: requester.Id}}, []string{"waiting"}},
		{staff, []messaging.Identity{{Type: "team", ID: "musecat"}}, []string{"waiting", "recognised", "solved"}},
	} {
		s, err := messaging.Get(app, tc.user.Id, c.ID)
		if err != nil || !reflect.DeepEqual(s.AllowedSenderIdentities, tc.identities) || !reflect.DeepEqual(s.AllowedStatusTransitions, tc.statuses) || !s.CanManageStatus {
			t.Fatalf("policy: %+v %v", s, err)
		}
		for _, status := range []string{"waiting", "recognised", "solved"} {
			_, err := messaging.SetStatus(app, tc.user.Id, c.ID, status)
			if (err == nil) != slices.Contains(tc.statuses, status) {
				t.Fatalf("%s status %s: %v", tc.user.Id, status, err)
			}
		}
	}
	if current, err := messaging.Get(app, requester.Id, c.ID); err != nil {
		t.Fatal(err)
	} else if _, err := messaging.SetRead(app, requester.Id, c.ID, current.LatestSequence); err != nil {
		t.Fatal(err)
	}
	// A requester who gains a responder role can choose either identity. Their own
	// inbox reply used to count as an unread incoming message for that same person.
	requester.Set("tags", []string{"moderator"})
	if err := app.Save(requester); err != nil {
		t.Fatal(err)
	}
	s, err := messaging.Get(app, requester.Id, c.ID)
	if err != nil || len(s.AllowedSenderIdentities) != 2 || len(s.AllowedStatusTransitions) != 3 {
		t.Fatal(s, err)
	}
	if _, _, err := messaging.Send(app, requester.Id, c.ID, messaging.SendInput{Text: "ambiguous", ClientMessageID: "ambiguous"}); err == nil {
		t.Fatal("dual-role sender was not required to choose")
	}
	if _, _, err := messaging.Send(app, requester.Id, c.ID, messaging.SendInput{Text: "self reply", ClientMessageID: "self", ActingAs: &messaging.Identity{Type: "team", ID: "musecat"}}); err != nil {
		t.Fatal(err)
	}
	s, err = messaging.Get(app, requester.Id, c.ID)
	if err != nil || s.UnreadCount != 0 {
		t.Fatalf("own reply unread: %+v %v", s, err)
	}
	if _, _, err := messaging.Send(app, staff.Id, c.ID, messaging.SendInput{Text: "another responder", ClientMessageID: "other"}); err != nil {
		t.Fatal(err)
	}
	s, err = messaging.Get(app, requester.Id, c.ID)
	if err != nil || s.UnreadCount != 1 {
		t.Fatal(s, err)
	}
	if _, err = messaging.SetRead(app, requester.Id, c.ID, s.LatestSequence); err != nil {
		t.Fatal(err)
	}
	s, err = messaging.Get(app, staff.Id, c.ID)
	if err != nil || s.LastReadSequence != 0 || s.UnreadCount != 1 {
		t.Fatalf("shared read cursor changed: %+v %v", s, err)
	}
}

func TestMessagingArcadeNotificationMentionMatrix(t *testing.T) {
	app := newArcadeTestApp(t)
	_, requester := createAuthUserWithTags(t, app, []string{})
	_, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, staffOwner := createAuthUserWithTags(t, app, []string{"moderator", "arcade_owner"})
	_, moderator := createAuthUserWithTags(t, app, []string{"moderator"})
	_, developer := createAuthUserWithTags(t, app, []string{"developer"})
	_, revoked := createAuthUserWithTags(t, app, []string{})
	_, withdrawn := createAuthUserWithTags(t, app, []string{"moderator"})
	_, banned := createAuthUserWithTags(t, app, []string{"moderator"})
	_, emailBanned := createAuthUserWithTags(t, app, []string{"moderator"})
	arcadeID, _ := seedPublicArcade(t, app, requester.Id, arcadeSeed{Name: "Mention arcade", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	arcade, _ := app.FindRecordById("arcade", arcadeID)
	arcade.Set("inquiry_settings", map[string]any{"mode": "internal"})
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*core.Record{owner, staffOwner, revoked} {
		u.Set("owns", []string{arcadeID})
		if err := app.Save(u); err != nil {
			t.Fatal(err)
		}
	}
	withdrawn.Set("withdrawn", true)
	if err := app.Save(withdrawn); err != nil {
		t.Fatal(err)
	}
	seedUserBan(t, app, banned.Id, "", "restricted", time.Time{})
	seedUserBan(t, app, "emailban0000001", hashNormalizedEmailForTest(emailBanned.Email()), "restricted", time.Time{})
	for _, u := range []*core.Record{requester, owner, staffOwner, moderator, developer, banned, emailBanned, withdrawn} {
		if err := app.RunInTransaction(func(tx core.App) error {
			_, err := usernotification.UpsertDeviceTx(tx, u.Id, "https://fcm.googleapis.com/fcm/send/mention-"+u.Id, map[string]any{"endpoint": u.Id}, "ko")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	expectRecipients := func(messageID string, want ...string) {
		t.Helper()
		rows, err := app.FindRecordsByFilter("user_notification", "message={:m}", "", 0, 0, dbx.Params{"m": messageID})
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, n := range rows {
			got = append(got, n.GetString("user"))
			u, _ := app.FindRecordById("user", n.GetString("user"))
			if !messaging.NotificationDeliverable(app, u, n) {
				t.Fatal("fresh notification undeliverable", n.Id)
			}
			deliveries, err := app.FindRecordsByFilter("user_notification_delivery", "notification={:n}", "", 0, 0, dbx.Params{"n": n.Id})
			if err != nil || len(deliveries) != 1 {
				t.Fatalf("durable deliveries %d %v", len(deliveries), err)
			}
		}
		slices.Sort(got)
		slices.Sort(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("recipients %v want %v", got, want)
		}
	}
	staffStream := messaging.Events(app).Subscribe(moderator.Id)
	defer messaging.Events(app).Unsubscribe(staffStream)
	input := messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "arcade", ID: arcadeID}, InitialMessage: messaging.SendInput{Text: "ordinary @Musecat text", ClientMessageID: "plain"}}
	c, _, err := messaging.CreateWithReplay(app, requester.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	expectRecipients(c.LastMessage.ID, owner.Id, staffOwner.Id)
	if len(staffStream.Events) != 0 {
		t.Fatal("uninvited staff received inquiry realtime")
	}
	if _, err := messaging.Get(app, moderator.Id, c.ID); err != nil {
		t.Fatal("staff lost dashboard management access", err)
	}
	// Old queued alerts do not surface unmentioned inquiries in personal inboxes.
	col, _ := app.FindCollectionByNameOrId("user_notification")
	old := core.NewRecord(col)
	old.Set("user", moderator.Id)
	old.Set("kind", "message")
	old.Set("conversation", c.ID)
	old.Set("message", c.LastMessage.ID)
	if messaging.NotificationVisible(app, moderator, old) || messaging.NotificationDeliverable(app, moderator, old) {
		t.Fatal("old queued staff policy")
	}
	mention := messaging.SendInput{Text: "please help", ClientMessageID: "mention", Mentions: []string{"musecat", "musecat"}}
	m, _, err := messaging.Send(app, requester.Id, c.ID, mention)
	if err != nil || !reflect.DeepEqual(m.Mentions, []string{"musecat"}) {
		t.Fatal(m, err)
	}
	expectRecipients(m.ID, owner.Id, staffOwner.Id, moderator.Id, developer.Id)
	if len(staffStream.Events) == 0 {
		t.Fatal("invited staff did not receive inquiry realtime")
	}
	if _, err := messaging.Get(app, moderator.Id, c.ID); err != nil {
		t.Fatal("explicit mention did not grant team access", err)
	}
	mention.Mentions = []string{"musecat"}
	retry, replayed, err := messaging.Send(app, requester.Id, c.ID, mention)
	if err != nil || !replayed || retry.ID != m.ID {
		t.Fatal(retry, replayed, err)
	}
	expectRecipients(m.ID, owner.Id, staffOwner.Id, moderator.Id, developer.Id)
	mention.Mentions = nil
	if _, _, err = messaging.Send(app, requester.Id, c.ID, mention); err == nil {
		t.Fatal("mention change bypassed retry hash")
	}
	reply, _, err := messaging.Send(app, moderator.Id, c.ID, messaging.SendInput{Text: "reply", ClientMessageID: "reply"})
	if err != nil {
		t.Fatal(err)
	}
	expectRecipients(reply.ID, requester.Id)
	reply, _, err = messaging.Send(app, moderator.Id, c.ID, messaging.SendInput{Text: "escalation", ClientMessageID: "staff-mention", Mentions: []string{"musecat"}})
	if err != nil {
		t.Fatal(err)
	}
	expectRecipients(reply.ID, requester.Id, staffOwner.Id, developer.Id)
	// Revoke the official role: existing inbox history stays visible through staff
	// access, but an ordinary owner's queued alert no longer qualifies for push.
	rows, _ := app.FindRecordsByFilter("user_notification", "message={:m} && user={:u}", "", 0, 0, dbx.Params{"m": c.LastMessage.ID, "u": staffOwner.Id})
	staffOwner.Set("owns", []string{})
	if err := app.Save(staffOwner); err != nil {
		t.Fatal(err)
	}
	if !messaging.NotificationVisible(app, staffOwner, rows[0]) || messaging.NotificationDeliverable(app, staffOwner, rows[0]) {
		t.Fatal("revoked official push still delivers")
	}
	initial := messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "arcade", ID: arcadeID}, InitialMessage: messaging.SendInput{Text: "initial escalation", ClientMessageID: "create-mentioned", Mentions: []string{"musecat"}}}
	created, replayed, err := messaging.CreateWithReplay(app, requester.Id, initial)
	if err != nil || replayed {
		t.Fatal(created, replayed, err)
	}
	expectRecipients(created.LastMessage.ID, owner.Id, staffOwner.Id, moderator.Id, developer.Id)
	again, replayed, err := messaging.CreateWithReplay(app, requester.Id, initial)
	if err != nil || !replayed || again.ID != created.ID {
		t.Fatal(again, replayed, err)
	}
	expectRecipients(created.LastMessage.ID, owner.Id, staffOwner.Id, moderator.Id, developer.Id)
	team, err := messaging.Create(app, requester.Id, messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "team", ID: "musecat"}, InitialMessage: messaging.SendInput{Text: "team help", ClientMessageID: "team"}})
	if err != nil {
		t.Fatal(err)
	}
	expectRecipients(team.LastMessage.ID, staffOwner.Id, moderator.Id, developer.Id)
}

func TestMessagingMentionsHTTPValidationAndMultipart(t *testing.T) {
	app := newArcadeTestApp(t)
	token, requester := createAuthUserWithTags(t, app, []string{})
	_, peer := createAuthUserWithTags(t, app, []string{})
	_ = messaging.Preference(app, peer.Id, "everyone")
	dm, err := messaging.Create(app, requester.Id, messaging.CreateInput{Kind: "dm", PeerUserID: peer.Id})
	if err != nil {
		t.Fatal(err)
	}
	arcadeID, _ := seedPublicArcade(t, app, requester.Id, arcadeSeed{Name: "Photo mention", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	call := func(path, body string, want int) {
		t.Helper()
		res := executeJSONRequest(t, app, "POST", path, body, map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s status %d want %d", body, res.StatusCode, want)
		}
	}
	call("/user/conversations/"+dm.ID+"/messages", `{"text":"hi","clientMessageId":"invalid","mentions":["musecat"]}`, 400)
	call("/user/conversations", `{"kind":"dm","peerUserId":"`+peer.Id+`","initialMessage":{"mentions":["musecat"]}}`, 400)
	call("/user/conversations", `{"kind":"inquiry","inbox":{"type":"team","id":"musecat"},"initialMessage":{"text":"hi","clientMessageId":"invalid","mentions":["musecat"]}}`, 400)
	for _, mentions := range []string{`["unknown"]`, `[1]`, `"musecat"`, `[` + strings.Repeat(`"musecat",`, 16) + `"musecat"]`} {
		call("/user/conversations", fmt.Sprintf(`{"kind":"inquiry","inbox":{"type":"arcade","id":%q},"initialMessage":{"text":"hi","clientMessageId":"invalid","mentions":%s}}`, arcadeID, mentions), 400)
	}
	upload := func(path, key, mentions string, create bool, want int) map[string]any {
		t.Helper()
		var buf bytes.Buffer
		form := multipart.NewWriter(&buf)
		_ = form.WriteField("clientMessageId", key)
		_ = form.WriteField("mentions", mentions)
		if create {
			_ = form.WriteField("kind", "inquiry")
			_ = form.WriteField("inboxType", "arcade")
			_ = form.WriteField("inboxId", arcadeID)
		}
		f, _ := form.CreateFormFile("photos", "photo.gif")
		_, _ = f.Write(append([]byte("GIF89a\x01\x00\x01\x00"), make([]byte, 64)...))
		_ = form.Close()
		res := executeRequest(t, app, "POST", path, &buf, map[string]string{"Authorization": "Bearer " + token, "Content-Type": form.FormDataContentType()})
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("upload status %d want %d: %v", res.StatusCode, want, out)
		}
		return out
	}
	first := upload("/user/conversations", "photo-create", `["musecat"]`, true, 201)
	id := first["id"].(string)
	if got := first["lastMessage"].(map[string]any)["mentions"].([]any); len(got) != 1 || got[0] != "musecat" {
		t.Fatal(first)
	}
	replay := upload("/user/conversations", "photo-create", `["musecat","musecat"]`, true, 200)
	if replay["id"] != id {
		t.Fatal("photo create duplicated")
	}
	upload("/user/conversations/"+id+"/messages", "photo-reply", `["musecat"]`, false, 201)
	upload("/user/conversations/"+id+"/messages", "bad", `[1]`, false, 400)
}

func TestMessagingNotificationFanoutBatchesRecipientReads(t *testing.T) {
	app := newArcadeTestApp(t)
	_, requester := createAuthUserWithTags(t, app, []string{})
	for i := 0; i < 8; i++ {
		_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
		if err := app.RunInTransaction(func(tx core.App) error {
			_, err := usernotification.UpsertDeviceTx(tx, staff.Id, fmt.Sprintf("https://fcm.googleapis.com/fcm/send/batch-%d", i), map[string]any{"endpoint": fmt.Sprint(i)}, "en")
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	queries := []string{}
	capture := func(_ context.Context, _ time.Duration, query string, _ *sql.Rows, _ error) {
		mu.Lock()
		queries = append(queries, query)
		mu.Unlock()
	}
	for _, builder := range []dbx.Builder{app.ConcurrentDB(), app.NonconcurrentDB()} {
		db := builder.(*dbx.DB)
		old := db.QueryLogFunc
		db.QueryLogFunc = capture
		t.Cleanup(func() { db.QueryLogFunc = old })
	}
	c, err := messaging.Create(app, requester.Id, messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "team", ID: "musecat"}, InitialMessage: messaging.SendInput{Text: "batch", ClientMessageID: "batch"}})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	captured := append([]string{}, queries...)
	mu.Unlock()
	bans, deviceLists := 0, 0
	for _, q := range captured {
		lower := strings.ToLower(q)
		if strings.Contains(lower, `from `+"`user_ban`") || strings.Contains(lower, `from "user_ban"`) {
			bans++
		}
		if strings.Contains(lower, "row_number() over (partition by user") {
			deviceLists++
		}
	}
	// Two sender checks (mutation + returned summary) plus one recipient-ban batch;
	// increasing the recipient count must never add a ban lookup for every user.
	if bans != 3 || deviceLists != 1 {
		t.Fatalf("fanout queries: bans=%d deviceLists=%d SQL=%v", bans, deviceLists, captured)
	}
	rows, err := app.FindRecordsByFilter("user_notification", "message={:m}", "", 0, 0, dbx.Params{"m": c.LastMessage.ID})
	if err != nil || len(rows) != 8 {
		t.Fatalf("lost durable recipients %d %v", len(rows), err)
	}
}
