package arcade_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
)

func TestUnifiedGuestInquiriesUseCanonicalStorageAndAccountResponderProfiles(t *testing.T) {
	app := newArcadeTestApp(t)
	staffToken, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, otherStaff := createAuthUserWithTags(t, app, []string{"moderator"})
	for _, name := range []string{"support_feedback", "support_feedback_message"} {
		if _, err := app.FindCollectionByNameOrId(name); err == nil {
			t.Fatal("fresh schema retained legacy store", name)
		}
	}
	key := strings.Repeat("b", 64)
	call := func(method, path, body, guest, token string, want int) map[string]any {
		t.Helper()
		headers := map[string]string{}
		if guest != "" {
			headers[supportfeedback.GuestHeader] = guest
		}
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		res := executeJSONRequest(t, app, method, path, body, headers)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d want %d %v", method, path, res.StatusCode, want, out)
		}
		raw, _ := json.Marshal(out)
		if strings.Contains(string(raw), key) || strings.Contains(string(raw), supportfeedback.GuestKeyHash(key)) {
			t.Fatal("guest credential exposed")
		}
		return out
	}
	initial := `{"kind":"inquiry","inbox":{"type":"team","id":"musecat"},"initialMessage":{"text":"Guest question","clientMessageId":"guest-initial"}}`
	c := call("POST", "/guest/conversations", initial, key, "", 201)
	id := c["id"].(string)
	if again := call("POST", "/guest/conversations", initial, key, "", 200); again["id"] != id {
		t.Fatal("guest retry duplicated inquiry")
	}
	requester := c["requester"].(map[string]any)
	call("GET", "/guest/conversations/"+id, "", strings.Repeat("c", 64), "", 404)
	call("GET", "/guest/conversations/"+id, "", "", "", 401)
	call("POST", "/guest/conversations", fmt.Sprintf(`{"kind":"dm","peerUserId":%q}`, staff.Id), key, "", 403)
	call("POST", "/guest/conversations/"+id+"/messages", fmt.Sprintf(`{"text":"Forged","clientMessageId":"forged","actingAs":{"type":"team","id":"musecat"}}`), key, "", 403)
	call("PATCH", "/guest/conversations/"+id+"/inquiry", `{"status":"recognised"}`, key, "", 400)
	call("POST", "/user/conversations/"+id+"/messages", `{"text":"First real responder","clientMessageId":"operator-one"}`, "", staffToken, 201)
	if _, _, err := messaging.Send(app, otherStaff.Id, id, messaging.SendInput{Text: "Second real responder", ClientMessageID: "operator-two"}); err != nil {
		t.Fatal(err)
	}
	info := ensureUserInfo(t, app, staff.Id)
	info.Set("nickname", "Current operator profile")
	if err := app.Save(info); err != nil {
		t.Fatal(err)
	}
	page := call("GET", "/guest/conversations/"+id+"/messages", "", key, "", 200)
	items := page["items"].([]any)
	if len(items) != 3 {
		t.Fatal(page)
	}
	first := items[0].(map[string]any)
	if first["senderUserId"] != "" || first["sender"].(map[string]any)["id"] != requester["id"] {
		t.Fatal("guest became a synthetic account", first)
	}
	reply := items[1].(map[string]any)
	sender := reply["sender"].(map[string]any)
	if reply["senderUserId"] != staff.Id || sender["id"] != staff.Id || sender["name"] != "Current operator profile" || reply["actingAs"].(map[string]any)["type"] != "team" {
		t.Fatal("actual responder profile replaced by inbox or stale snapshot", reply)
	}
	if items[2].(map[string]any)["senderUserId"] != otherStaff.Id {
		t.Fatal("responders merged")
	}
	call("PUT", "/guest/conversations/"+id+"/read", `{"throughSequence":3}`, key, "", 200)
	call("POST", "/guest/conversations/"+id+"/messages", `{"text":"Follow-up","clientMessageId":"guest-followup"}`, key, "", 201)
	if got := call("GET", "/guest/conversations/"+id, "", key, "", 200); got["status"] != nil || got["unreadCount"] != float64(0) {
		t.Fatal("guest status/read policy", got)
	}
	// Old entrypoints project the same canonical inquiry, without another store.
	legacy := call("GET", "/guest/feedback/thread?id="+id, "", key, "", 200)
	if legacy["conversationId"] != id {
		t.Fatal("legacy adapter created another thread")
	}
	staff.Set("tags", []string{})
	if err := app.Save(staff); err != nil {
		t.Fatal(err)
	}
	call("GET", "/user/conversations/"+id, "", "", staffToken, 404)
}

func TestMessagingProfilesUseCurrentUserReferenceWithoutSnapshots(t *testing.T) {
	app := newArcadeTestApp(t)
	_, author := createAuthUserWithTags(t, app, []string{})
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	c, err := messaging.Create(app, author.Id, messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "team", ID: "musecat"}, InitialMessage: messaging.SendInput{Text: "Question", ClientMessageID: "initial"}})
	if err != nil {
		t.Fatal(err)
	}
	sent, _, err := messaging.Send(app, staff.Id, c.ID, messaging.SendInput{Text: "Answer", ClientMessageID: "answer"})
	if err != nil {
		t.Fatal(err)
	}
	row, err := app.FindRecordById(messaging.Messages, sent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sender_name", "sender_avatar", "sender_roles"} {
		if row.Collection().Fields.GetByName(name) != nil {
			t.Fatalf("unexpected profile snapshot field %s", name)
		}
	}
	info := ensureUserInfo(t, app, staff.Id)
	info.Set("nickname", "Updated public name")
	if err := app.Save(info); err != nil {
		t.Fatal(err)
	}
	history, err := messaging.History(app, author.Id, c.ID, 0, -1, 50)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := messaging.Get(app, author.Id, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []messaging.Message{history.Items[1], *summary.LastMessage} {
		if message.SenderUserID != staff.Id || message.Sender.ID != staff.Id || message.Sender.Name != "Updated public name" {
			t.Fatal(message)
		}
	}
	staff.Set("withdrawn", true)
	if err := app.Save(staff); err != nil {
		t.Fatal(err)
	}
	history, err = messaging.History(app, author.Id, c.ID, 0, -1, 50)
	if err != nil || history.Items[1].Sender.Name != "탈퇴한 사용자" {
		t.Fatal("withdrawn profile exposed", history, err)
	}
}

func TestMessagingUserReferenceDoesNotDeleteMessages(t *testing.T) {
	app := newArcadeTestApp(t)
	_, author := createAuthUserWithTags(t, app, []string{})
	_, peer := createAuthUserWithTags(t, app, []string{})
	room, err := messaging.Create(app, author.Id, messaging.CreateInput{Kind: "dm", PeerUserID: peer.Id})
	if err != nil {
		t.Fatal(err)
	}
	sent, _, err := messaging.Send(app, author.Id, room.ID, messaging.SendInput{Text: "Keep this message", ClientMessageID: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(author); err != nil {
		t.Fatal(err)
	}
	history, err := messaging.History(app, peer.Id, room.ID, 0, -1, 50)
	if err != nil || len(history.Items) != 1 || history.Items[0].ID != sent.ID || history.Items[0].Text != "Keep this message" || history.Items[0].Sender.Name != "탈퇴한 사용자" {
		t.Fatal("deleted account lost message or profile fallback", history, err)
	}
}
