package arcade_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestMessagingPlazaMembershipHistoryAndPrivacy(t *testing.T) {
	app := newArcadeTestApp(t)
	aToken, a := createAuthUserWithTags(t, app, []string{})
	bToken, b := createAuthUserWithTags(t, app, []string{})
	call := func(method, path, body, token string, want int) map[string]any {
		t.Helper()
		res := executeJSONRequest(t, app, method, path, body, map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s: status %d, want %d: %v", method, path, res.StatusCode, want, out)
		}
		return out
	}
	call("POST", "/user/plaza", "", "", 401)
	first := call("POST", "/user/plaza", "", aToken, 200)
	second := call("POST", "/user/plaza", "", bToken, 200)
	if first["id"] != messaging.PlazaID || first["id"] != second["id"] || first["kind"] != "plaza" || second["canSend"] != true {
		t.Fatal("members must share one plaza without joining", first, second)
	}
	path := "/user/conversations/" + messaging.PlazaID
	call("GET", path+"/messages", "", bToken, 200)
	if n, _ := app.CountRecords(messaging.Parties); n != 0 {
		t.Fatal("plaza must not materialize membership", n)
	}
	// Private DM policies must not gate participation in the shared public room.
	call("PUT", "/user/messaging/preferences", `{"receive":"nobody"}`, bToken, 200)
	call("PUT", "/user/messaging/blocks/"+a.Id, "", bToken, 204)
	sub := messaging.Events(app).SubscribePlaza(b.Id)
	defer messaging.Events(app).Unsubscribe(sub)
	privateSub := messaging.Events(app).Subscribe(a.Id)
	defer messaging.Events(app).Unsubscribe(privateSub)
	body := `{"text":"hello plaza","clientMessageId":"plaza-one"}`
	sent := call("POST", path+"/messages", body, aToken, 201)
	retry := call("POST", path+"/messages", body, aToken, 200)
	if sent["id"] != retry["id"] {
		t.Fatal("retry duplicated a public message")
	}
	select {
	case event := <-sub.Events:
		if event.ID != messaging.PlazaID || event.Name != "conversation.changed" || event.LatestSequence != 1 {
			t.Fatal("wrong public invalidation", event)
		}
	default:
		t.Fatal("a member who never joined must receive plaza invalidation")
	}
	select {
	case event := <-sub.Events:
		t.Fatal("retry must not broadcast twice", event)
	default:
	}
	select {
	case event := <-privateSub.Events:
		t.Fatal("member outside plaza received a plaza event", event)
	default:
	}
	call("POST", path+"/messages", `{"text":"changed","clientMessageId":"plaza-one"}`, aToken, 409)
	call("POST", path+"/messages", fmt.Sprintf(`{"text":"spoof","clientMessageId":"spoof","actingAs":{"type":"user","id":%q}}`, b.Id), aToken, 403)
	call("POST", path+"/messages", `{"text":"mention","clientMessageId":"mention","mentions":["musecat"]}`, aToken, 400)
	call("POST", path+"/messages", `{"text":"hello back","clientMessageId":"plaza-two"}`, bToken, 201)
	// A newly created member can read all earlier history immediately.
	cToken, _ := createAuthUserWithTags(t, app, []string{})
	history := call("GET", path+"/messages?limit=1", "", cToken, 200)
	if len(history["items"].([]any)) != 1 || history["nextBeforeSequence"] != float64(2) {
		t.Fatal("public history must use existing sequence pagination", history)
	}
	call("GET", path+"/messages?beforeSequence=2", "", cToken, 200)
	call("PUT", path+"/read", `{"throughSequence":2}`, cToken, 400)
	call("POST", "/user/messages/"+sent["id"].(string)+"/report", `{"reason":"abuse"}`, cToken, 201)
	call("PATCH", path+"/inquiry", `{"status":"solved"}`, aToken, 400)
	call("POST", "/user/conversations", `{"kind":"plaza"}`, aToken, 400)
	list, err := messaging.ListConversations(app, a.Id, url.Values{})
	if err != nil || len(list.Items) != 0 {
		t.Fatal("plaza must stay out of private inbox", list, err)
	}
	if n, _ := app.CountRecords("user_notification", dbx.NewExp("kind='message'")); n != 0 {
		t.Fatal("public messages must not fan out inbox/push notifications", n)
	}
	guest := messaging.Principal{GuestKey: "0123456789abcdef0123456789abcdef"}
	if _, err := messaging.GetFor(app, guest, messaging.PlazaID); err == nil {
		t.Fatal("guest inquiry credentials read the plaza")
	}
	if _, _, err := messaging.SendFor(app, guest, messaging.PlazaID, messaging.SendInput{Text: "guest", ClientMessageID: "guest"}); err == nil {
		t.Fatal("guest inquiry credentials wrote to the plaza")
	}
	b.Set("withdrawn", true)
	if err := app.Save(b); err != nil {
		t.Fatal(err)
	}
	call("POST", "/user/plaza", "", bToken, 403)
	call("GET", path+"/messages", "", bToken, 403)
	call("POST", path+"/messages", `{"text":"withdrawn","clientMessageId":"withdrawn"}`, bToken, 403)
}

func TestMessagingPlazaConcurrentOpen(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUserWithTags(t, app, []string{})
	errs := make(chan error, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			summary, err := messaging.OpenPlaza(app, user.Id)
			if err == nil && summary.ID != messaging.PlazaID {
				err = fmt.Errorf("unexpected plaza %s", summary.ID)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := app.CountRecords(messaging.Conversations); n != 1 {
		t.Fatal("concurrent opens created multiple rooms", n)
	}
	// Unknown room records must never gain all-member access from kind alone.
	collection, _ := app.FindCollectionByNameOrId(messaging.Conversations)
	rogue := core.NewRecord(collection)
	rogue.Set("kind", "plaza")
	if err := app.Save(rogue); err != nil {
		t.Fatal(err)
	}
	if _, err := messaging.Get(app, user.Id, rogue.Id); err == nil {
		t.Fatal("noncanonical plaza granted all-member access")
	}
}

func TestMessagingPlazaBoundedHistoryAndSummaryQueries(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUserWithTags(t, app, []string{})
	if _, err := messaging.OpenPlaza(app, user.Id); err != nil {
		t.Fatal(err)
	}
	if err := app.RunInTransaction(func(tx core.App) error {
		col, err := tx.FindCollectionByNameOrId(messaging.Messages)
		if err != nil {
			return err
		}
		c, err := tx.FindRecordById(messaging.Conversations, messaging.PlazaID)
		if err != nil {
			return err
		}
		for i := 1; i <= 125; i++ {
			m := core.NewRecord(col)
			m.Set("conversation", c.Id)
			m.Set("sequence", i)
			m.Set("sender_user", user.Id)
			m.Set("acting_type", "user")
			m.Set("acting_id", user.Id)
			m.Set("client_message_id", fmt.Sprint(i))
			m.Set("text", fmt.Sprintf("message %d", i))
			if err := tx.Save(m); err != nil {
				return err
			}
			c.Set("last_message", m.Id)
			c.Set("last_message_at", m.GetString("created"))
		}
		c.Set("last_sequence", 125)
		return tx.Save(c)
	}); err != nil {
		t.Fatal(err)
	}
	// Summaries must not scan messages/reads/parties, even after history accumulates.
	var mu sync.Mutex
	queries := []string{}
	for _, builder := range []dbx.Builder{app.ConcurrentDB(), app.NonconcurrentDB()} {
		db := builder.(*dbx.DB)
		old := db.QueryLogFunc
		db.QueryLogFunc = func(_ context.Context, _ time.Duration, q string, _ *sql.Rows, _ error) {
			mu.Lock()
			queries = append(queries, q)
			mu.Unlock()
		}
		t.Cleanup(func() { db.QueryLogFunc = old })
	}
	summary, err := messaging.OpenPlaza(app, user.Id)
	if err != nil || summary.LatestSequence != 125 || summary.LastMessage != nil {
		t.Fatal(summary, err)
	}
	mu.Lock()
	captured := append([]string{}, queries...)
	mu.Unlock()
	for _, q := range captured {
		for _, table := range []string{messaging.Messages, messaging.Parties, messaging.Reads, messaging.Preferences, messaging.Blocks} {
			if strings.Contains(q, table) {
				t.Fatalf("plaza summary queried %s: %s", table, q)
			}
		}
	}
	latest, err := messaging.History(app, user.Id, messaging.PlazaID, 0, -1, 50)
	if err != nil || len(latest.Items) != 50 || latest.Items[0].Sequence != 76 || latest.Items[49].Sequence != 125 || latest.NextBeforeSequence == nil || *latest.NextBeforeSequence != 76 {
		t.Fatal(latest, err)
	}
	older, err := messaging.History(app, user.Id, messaging.PlazaID, *latest.NextBeforeSequence, -1, 50)
	if err != nil || len(older.Items) != 50 || older.Items[0].Sequence != 26 || older.Items[49].Sequence != 75 {
		t.Fatal(older, err)
	}
	delta, err := messaging.History(app, user.Id, messaging.PlazaID, 0, 120, 50)
	if err != nil || len(delta.Items) != 5 || delta.Items[0].Sequence != 121 {
		t.Fatal(delta, err)
	}
	var plan []struct {
		Detail string `db:"detail"`
	}
	err = app.DB().NewQuery("EXPLAIN QUERY PLAN SELECT id FROM messaging_message WHERE conversation={:c} AND sequence<76 ORDER BY sequence DESC LIMIT 51").Bind(dbx.Params{"c": messaging.PlazaID}).All(&plan)
	if err != nil {
		t.Fatal(err)
	}
	indexed := false
	for _, step := range plan {
		if strings.Contains(step.Detail, "idx_messaging_sequence") {
			indexed = true
		}
	}
	if !indexed {
		t.Fatal("history pagination did not use the conversation/sequence index", plan)
	}
	for _, check := range []struct{ index, query string }{
		{"idx_messaging_sender_time", "SELECT count(*) FROM messaging_message WHERE sender_user={:u} AND created>='2026-11-05'"},
		{"idx_messaging_sender_time", "SELECT count(*) FROM messaging_message WHERE sender_user='' AND sender_guest={:u} AND created>='2026-11-05'"},
		{"idx_messaging_retry", "SELECT id FROM messaging_message WHERE conversation={:c} AND sender_user={:u} AND sender_guest='' AND client_message_id='125' AND (sender_user!='' OR sender_guest!='')"},
	} {
		index, query := check.index, check.query
		plan = nil
		if err := app.DB().NewQuery("EXPLAIN QUERY PLAN " + query).Bind(dbx.Params{"u": user.Id, "c": messaging.PlazaID}).All(&plan); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, step := range plan {
			if strings.Contains(step.Detail, index) {
				found = true
			}
		}
		if !found {
			t.Fatalf("query missed %s: %v", index, plan)
		}
	}
}
