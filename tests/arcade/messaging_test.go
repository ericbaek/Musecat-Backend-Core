package arcade_test

import (
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/router"
	"strings"
	"sync"
	"testing"
)

func TestMessagingDMPrivacyIdempotencyAndRead(t *testing.T) {
	app := newArcadeTestApp(t)
	aToken, a := createAuthUserWithTags(t, app, []string{})
	bToken, b := createAuthUserWithTags(t, app, []string{})
	staffToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	otherToken, _ := createAuthUserWithTags(t, app, []string{})
	call := func(method, path, body, token string, want int) map[string]any {
		t.Helper()
		res := executeJSONRequest(t, app, method, path, body, map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s status%d want%d: %v", method, path, res.StatusCode, want, out)
		}
		return out
	}
	call("POST", "/user/conversations", fmt.Sprintf(`{"kind":"dm","peerUserId":%q}`, b.Id), aToken, 403)
	call("PUT", "/user/messaging/preferences", `{"receive":"everyone"}`, bToken, 200)
	call("PUT", "/user/messaging/preferences", `{"receive":"everyone"}`, aToken, 200)
	c := call("POST", "/user/conversations", fmt.Sprintf(`{"kind":"dm","peerUserId":%q}`, b.Id), aToken, 200)
	id := c["id"].(string)
	for _, token := range []string{staffToken, otherToken} {
		call("GET", "/user/conversations/"+id, "", token, 404)
		call("POST", "/user/conversations/"+id+"/messages", `{"text":"stolen","clientMessageId":"bad"}`, token, 404)
	}
	body := `{"text":"hello","clientMessageId":"one"}`
	first := call("POST", "/user/conversations/"+id+"/messages", body, aToken, 201)
	again := call("POST", "/user/conversations/"+id+"/messages", body, aToken, 200)
	if first["id"] != again["id"] {
		t.Fatal("duplicate retry")
	}
	call("POST", "/user/conversations/"+id+"/messages", `{"text":"changed","clientMessageId":"one"}`, aToken, 409)
	if got := call("GET", "/user/conversations/"+id, "", bToken, 200); got["unreadCount"] != float64(1) {
		t.Fatal(got)
	}
	call("PUT", "/user/conversations/"+id+"/read", `{"throughSequence":2}`, bToken, 400)
	call("PUT", "/user/conversations/"+id+"/read", `{"throughSequence":1}`, bToken, 200)
	call("PUT", "/user/conversations/"+id+"/read", `{"throughSequence":0}`, bToken, 200)
	if got := call("GET", "/user/conversations/"+id, "", bToken, 200); got["lastReadSequence"] != float64(1) || got["unreadCount"] != float64(0) {
		t.Fatal(got)
	}
	list := call("GET", "/user/conversations", "", aToken, 200)
	if len(list["items"].([]any)) != 1 {
		t.Fatal(list)
	}
	call("PUT", "/user/messaging/blocks/"+a.Id, "", bToken, 204)
	call("POST", "/user/conversations/"+id+"/messages", `{"text":"blocked","clientMessageId":"two"}`, aToken, 403)
	call("GET", "/user/conversations/"+id+"/messages?afterSequence=0", "", aToken, 200)
	call("DELETE", "/user/messaging/blocks/"+a.Id, "", bToken, 204)
	call("PUT", "/user/messaging/preferences", `{"receive":"nobody"}`, bToken, 200)
	call("POST", "/user/conversations/"+id+"/messages", `{"text":"denied","clientMessageId":"two"}`, aToken, 403)
	call("POST", "/user/messages/"+first["id"].(string)+"/report", `{"reason":"abuse"}`, bToken, 201)
	call("POST", "/user/messages/"+first["id"].(string)+"/report", `{"reason":"abuse"}`, otherToken, 404)
	if err := messaging.EnsureSchema(app); err != nil {
		t.Fatal("schema repeat", err)
	}
}
func TestMessagingPairRaceAndRollback(t *testing.T) {
	app := newArcadeTestApp(t)
	_, a := createAuthUserWithTags(t, app, []string{})
	_, b := createAuthUserWithTags(t, app, []string{})
	for _, id := range []string{a.Id, b.Id} {
		if err := messaging.Preference(app, id, "everyone"); err != nil {
			t.Fatal(err)
		}
	}
	ids := make(chan string, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, pair := range [][2]string{{a.Id, b.Id}, {b.Id, a.Id}} {
		wg.Add(1)
		go func(pair [2]string) {
			defer wg.Done()
			s, err := messaging.Create(app, pair[0], messaging.CreateInput{Kind: "dm", PeerUserID: pair[1]})
			ids <- s.ID
			errs <- err
		}(pair)
	}
	wg.Wait()
	if e := <-errs; e != nil {
		t.Fatal(e)
	}
	if e := <-errs; e != nil {
		t.Fatal(e)
	}
	id := <-ids
	if id != <-ids {
		t.Fatal("pair duplicated")
	}
	sub := messaging.Events(app).Subscribe(b.Id)
	defer messaging.Events(app).Unsubscribe(sub)
	app.OnRecordCreate(messaging.Messages).Bind(&hook.Handler[*core.RecordEvent]{Id: "reject-message", Func: func(e *core.RecordEvent) error { return fmt.Errorf("forced rollback") }})
	if _, _, err := messaging.Send(app, a.Id, id, messaging.SendInput{Text: "must rollback", ClientMessageID: "rollback"}); err == nil {
		t.Fatal("expected rollback")
	}
	app.OnRecordCreate(messaging.Messages).Unbind("reject-message")
	s, err := messaging.Get(app, a.Id, id)
	if err != nil || s.LatestSequence != 0 {
		t.Fatal(s, err)
	}
	select {
	case event := <-sub.Events:
		t.Fatalf("rollback event: %+v", event)
	default:
	}
}

func TestMessagingInquiry(t *testing.T) {
	app := newArcadeTestApp(t)
	_, a := createAuthUserWithTags(t, app, []string{})
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	c, err := messaging.Create(app, a.Id, messaging.CreateInput{Kind: "inquiry", Inbox: messaging.Identity{Type: "team", ID: "musecat"}, InitialMessage: messaging.SendInput{Text: "need help", ClientMessageID: "create"}})
	if err != nil {
		t.Fatalf("create: %+v", err)
	}
	m, _, err := messaging.Send(app, staff.Id, c.ID, messaging.SendInput{Text: "answer", ClientMessageID: "answer"})
	if err != nil {
		t.Fatal(err)
	}
	if m.ActingAs.Type != "team" || m.SenderUserID != staff.Id {
		t.Fatal(m)
	}
	s, err := messaging.Get(app, a.Id, c.ID)
	if err != nil || s.UnreadCount != 1 {
		t.Fatal(s, err)
	}
	staff.Set("tags", []string{})
	staff.Set("tag", []string{})
	if err = app.Save(staff); err != nil {
		t.Fatal(err)
	}
	if _, err = messaging.Get(app, staff.Id, c.ID); err == nil {
		t.Fatal("revoked staff access")
	}
}

func TestMessagingSequenceDeltaAndAudienceIsolation(t *testing.T) {
	app := newArcadeTestApp(t)
	_, a := createAuthUserWithTags(t, app, []string{})
	_, b := createAuthUserWithTags(t, app, []string{})
	_, outsider := createAuthUserWithTags(t, app, []string{"moderator"})
	_ = messaging.Preference(app, a.Id, "everyone")
	_ = messaging.Preference(app, b.Id, "everyone")
	c, err := messaging.Create(app, a.Id, messaging.CreateInput{Kind: "dm", PeerUserID: b.Id})
	if err != nil {
		t.Fatal(err)
	}
	hub := messaging.Events(app)
	recipient := hub.Subscribe(b.Id)
	unrelated := hub.Subscribe(outsider.Id)
	defer hub.Unsubscribe(recipient)
	defer hub.Unsubscribe(unrelated)
	for i := 1; i <= 5; i++ {
		if _, _, err := messaging.Send(app, a.Id, c.ID, messaging.SendInput{Text: fmt.Sprint(i), ClientMessageID: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	if len(recipient.Events) == 0 || len(unrelated.Events) != 0 {
		t.Fatal("conversation traffic leaked into unrelated subscriber queues")
	}
	// Populate committed history without bypassing API rate policies in a live request.
	err = app.RunInTransaction(func(tx core.App) error {
		col, e := tx.FindCollectionByNameOrId(messaging.Messages)
		if e != nil {
			return e
		}
		row, e := tx.FindRecordById(messaging.Conversations, c.ID)
		if e != nil {
			return e
		}
		for i := 6; i <= 205; i++ {
			m := core.NewRecord(col)
			m.Set("conversation", c.ID)
			m.Set("sequence", i)
			m.Set("sender_user", a.Id)
			m.Set("acting_type", "user")
			m.Set("acting_id", a.Id)
			m.Set("client_message_id", fmt.Sprint(i))
			m.Set("text", fmt.Sprint(i))
			if e = tx.Save(m); e != nil {
				return e
			}
			row.Set("last_sequence", i)
			row.Set("last_message", m.Id)
			row.Set("last_message_at", m.Get("created"))
		}
		return tx.Save(row)
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := messaging.History(app, b.Id, c.ID, 0, 0, 200)
	if err != nil || len(page.Items) != 200 || page.Items[0].Sequence != 1 || page.NextAfterSequence == nil || *page.NextAfterSequence != 200 {
		t.Fatal(page, err)
	}
	next, err := messaging.History(app, b.Id, c.ID, 0, *page.NextAfterSequence, 200)
	if err != nil || len(next.Items) != 5 || next.Items[0].Sequence != 201 || next.NextAfterSequence != nil {
		t.Fatal(next, err)
	}
	latest, err := messaging.History(app, b.Id, c.ID, 0, -1, 50)
	if err != nil || latest.Items[0].Sequence != 156 || latest.NextBeforeSequence == nil {
		t.Fatal(latest, err)
	}
	previous, err := messaging.History(app, b.Id, c.ID, *latest.NextBeforeSequence, -1, 50)
	if err != nil || previous.Items[len(previous.Items)-1].Sequence != 155 {
		t.Fatal(previous, err)
	}
}

func TestMessagingInquiryCreateReplayHTTP(t *testing.T) {
	app := newArcadeTestApp(t)
	token, _ := createAuthUserWithTags(t, app, []string{})
	body := `{"kind":"inquiry","inbox":{"type":"team","id":"musecat"},"initialMessage":{"text":"help","clientMessageId":"initial-retry"}}`
	var id string
	for i, want := range []int{201, 200} {
		res := executeJSONRequest(t, app, "POST", "/user/conversations", body, map[string]string{"Authorization": "Bearer " + token})
		var got messaging.Summary
		err := json.NewDecoder(res.Body).Decode(&got)
		res.Body.Close()
		if res.StatusCode != want || err != nil {
			t.Fatal(res.StatusCode, want, err)
		}
		if i == 0 {
			id = got.ID
		} else if id != got.ID {
			t.Fatal("inquiry retry duplicated")
		}
	}
	res := executeJSONRequest(t, app, "POST", "/user/conversations", strings.Replace(body, "help", "different", 1), map[string]string{"Authorization": "Bearer " + token})
	defer res.Body.Close()
	if res.StatusCode != 409 {
		t.Fatal("changed payload", res.StatusCode)
	}
}

func TestMessagingSchemaValidatesExistingContract(t *testing.T) {
	app := newArcadeTestApp(t)
	if err := messaging.EnsureSchema(app); err != nil {
		t.Fatal("valid schema was not idempotent", err)
	}
	collection, err := app.FindCollectionByNameOrId(messaging.Messages)
	if err != nil {
		t.Fatal(err)
	}
	rule := "id!=''"
	collection.ListRule = &rule
	if err = app.Save(collection); err != nil {
		t.Fatal(err)
	}
	if err = messaging.EnsureSchema(app); err == nil {
		t.Fatal("schema accepted raw public message access")
	}
	collection.ListRule = nil
	if err = app.Save(collection); err != nil {
		t.Fatal(err)
	}
	if err = messaging.EnsureSchema(app); err != nil {
		t.Fatal(err)
	}
}

func TestMessagingBlockSaveFailureIsNotAcknowledged(t *testing.T) {
	app := newArcadeTestApp(t)
	_, a := createAuthUserWithTags(t, app, []string{})
	_, b := createAuthUserWithTags(t, app, []string{})
	sub := messaging.Events(app).Subscribe(a.Id)
	defer messaging.Events(app).Unsubscribe(sub)
	app.OnRecordCreate(messaging.Blocks).Bind(&hook.Handler[*core.RecordEvent]{Id: "reject-block", Func: func(e *core.RecordEvent) error { return fmt.Errorf("forced rollback") }})
	if err := messaging.Block(app, a.Id, b.Id, false); err == nil {
		t.Fatal("failed block acknowledged")
	}
	select {
	case event := <-sub.Events:
		t.Fatalf("failed block published %+v", event)
	default:
	}
}

func TestMessagingCreationQuotaCountsOnlyInitiator(t *testing.T) {
	app := newArcadeTestApp(t)
	_, recipient := createAuthUserWithTags(t, app, []string{})
	if err := messaging.Preference(app, recipient.Id, "everyone"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		_, sender := createAuthUserWithTags(t, app, []string{})
		if _, err := messaging.Create(app, sender.Id, messaging.CreateInput{Kind: "dm", PeerUserID: recipient.Id}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 11; i++ {
		_, peer := createAuthUserWithTags(t, app, []string{})
		if err := messaging.Preference(app, peer.Id, "everyone"); err != nil {
			t.Fatal(err)
		}
		summary, err := messaging.Create(app, recipient.Id, messaging.CreateInput{Kind: "dm", PeerUserID: peer.Id})
		if i < 10 {
			if err != nil {
				t.Fatal("incoming conversations consumed creation quota", err)
			}
			record, _ := app.FindRecordById(messaging.Conversations, summary.ID)
			if record.GetString("created_by") != recipient.Id {
				t.Fatal("creator missing")
			}
		} else {
			if err == nil || router.ToApiError(err).Status != 429 {
				t.Fatal("own creation quota missing", err)
			}
		}
	}
}
