package usernotification_test

import (
	"testing"
	"time"

	n "github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestAimeStatusUpdatePreservesSubscriptionsAndRecovery(t *testing.T) {
	app := testutil.NewTestApp(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"aimeupdateuser1", "aimeupdateuser2", "aimeupdateother"} {
		_, err := app.DB().NewQuery("INSERT INTO user (id,email,tokenKey) VALUES ({:id},{:email},{:id})").Bind(dbx.Params{"id": id, "email": id + "@example.com"}).Execute()
		must(err)
		col, err := app.FindCollectionByNameOrId(n.Devices)
		must(err)
		device := core.NewRecord(col)
		device.Set("user", id)
		device.Set("endpoint", "https://fcm.googleapis.com/"+id)
		device.Set("subscription", map[string]any{"endpoint": "https://fcm.googleapis.com/" + id})
		must(app.Save(device))
	}
	now := time.Now().UTC()
	must(n.RecordAimeProbe(app, n.AimeObservation{Status: "blocked", HTTPStatus: 503}, now))
	must(n.SetAimeSubscription(app, "aimeupdateuser1", true))
	must(n.SetAimeSubscription(app, "aimeupdateuser2", true))
	before, err := app.FindAllRecords(n.AimeSubscriptions)
	must(err)
	stateBefore, err := n.GetAimeStatus(app, "aimeupdateuser1")
	must(err)
	queued, err := n.QueueAimeStatusUpdate(app)
	must(err)
	if queued != 2 {
		t.Fatalf("queued=%d", queued)
	}
	rows, err := app.FindRecordsByFilter(n.Inbox, "event_key={:event}", "user", 0, 0, dbx.Params{"event": n.AimeStatusUpdateEvent})
	must(err)
	if len(rows) != 2 {
		t.Fatalf("inbox=%d", len(rows))
	}
	for _, row := range rows {
		must(app.RunInTransaction(func(tx core.App) error { row.Set("read", true); return tx.Save(row) }))
		for _, locale := range []string{"ko", "en", "ja"} {
			item := n.AimeItem(row, locale)
			if item["title"] == n.AimeItem(core.NewRecord(row.Collection()), locale)["title"] || item["message"] == "" {
				t.Fatal("update rendered as recovery", item)
			}
		}
	}
	must(n.SetAimeSubscription(app, "aimeupdateother", true))
	queued, err = n.QueueAimeStatusUpdate(app)
	must(err)
	if queued != 0 {
		t.Fatalf("repeat queued=%d", queued)
	}
	deliveries, err := app.FindAllRecords(n.Deliveries)
	must(err)
	if len(deliveries) != 2 {
		t.Fatalf("deliveries=%d", len(deliveries))
	}
	for _, sub := range before {
		current, err := app.FindRecordById(n.AimeSubscriptions, sub.Id)
		must(err)
		if current.GetString("incident") != sub.GetString("incident") {
			t.Fatal("subscription changed")
		}
	}
	stateAfter, err := n.GetAimeStatus(app, "aimeupdateuser1")
	must(err)
	if !stateAfter.Subscribed || stateAfter.Status != stateBefore.Status || stateAfter.Completed || stateAfter.Confirmations != 0 {
		t.Fatal("recovery state changed", stateAfter)
	}
	for _, row := range rows {
		current, err := app.FindRecordById(n.Inbox, row.Id)
		must(err)
		if !current.GetBool("read") {
			t.Fatal("read reset")
		}
	}
	// The separate terminal recovery still reaches all remaining subscribers.
	for _, minute := range []int{5, 10, 15, 20} {
		must(recordAimeWithOperations(t, app, n.AimeObservation{Status: "available", HTTPStatus: 200}, now.Add(time.Duration(minute)*time.Minute)))
	}
	must(n.DrainAimeRecoveries(app))
	recovery, err := app.FindRecordsByFilter(n.Inbox, "event_key!={:event} && kind={:kind}", "", 0, 0, dbx.Params{"event": n.AimeStatusUpdateEvent, "kind": n.AimeKind})
	must(err)
	if len(recovery) != 3 {
		t.Fatalf("recovery inbox=%d", len(recovery))
	}
}
