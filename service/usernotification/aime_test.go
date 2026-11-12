package usernotification_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	n "github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestAimeExplicitOneShotRecovery(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"aimesubscriber1", "aimenonsubscr01", "aimecancelled01"} {
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
	must(n.EnsureAimeSchema(app)) // idempotent and locked
	state, err := n.GetAimeStatus(app, "aimesubscriber1")
	must(err)
	if state.Status != "unknown" {
		t.Fatal(state)
	}
	must(recordAimeWithOperations(t, app, aimeObservation(200), now))
	if err := n.SetAimeSubscription(app, "aimesubscriber1", true); !errors.Is(err, n.ErrAimeNotBlocked) {
		t.Fatalf("healthy opt-in: %v", err)
	}
	must(recordAimeWithOperations(t, app, aimeObservation(403), now.Add(time.Minute)))
	must(n.SetAimeSubscription(app, "aimesubscriber1", true))
	must(n.SetAimeSubscription(app, "aimesubscriber1", true))
	must(n.SetAimeSubscription(app, "aimecancelled01", true))
	must(n.SetAimeSubscription(app, "aimecancelled01", false))
	for _, code := range []int{0, 500, 404, 429, 302, 403} {
		must(recordAimeWithOperations(t, app, aimeObservation(code), now.Add(2*time.Minute)))
		must(n.DrainAimeRecoveries(app))
		rows, err := app.FindRecordsByFilter(n.Inbox, "kind={:kind}", "", 0, 0, dbx.Params{"kind": n.AimeKind})
		must(err)
		if len(rows) != 0 {
			t.Fatalf("code %d notified", code)
		}
		state, err = n.GetAimeStatus(app, "aimesubscriber1")
		must(err)
		if !state.Subscribed {
			t.Fatal("lost pending opt-in")
		}
	}
	for _, minute := range []int{7, 12, 17, 22} {
		must(recordAimeWithOperations(t, app, aimeObservation(200), now.Add(time.Duration(minute)*time.Minute)))
	}
	must(n.DrainAimeRecoveries(app))
	must(n.DrainAimeRecoveries(app))
	rows, err := app.FindRecordsByFilter(n.Inbox, "kind={:kind}", "", 0, 0, dbx.Params{"kind": n.AimeKind})
	must(err)
	if len(rows) != 1 || rows[0].GetString("user") != "aimesubscriber1" {
		t.Fatalf("unexpected audience: %v", rows)
	}
	receipts, err := app.FindRecordsByFilter(n.Deliveries, "notification={:id}", "", 0, 0, dbx.Params{"id": rows[0].Id})
	must(err)
	if len(receipts) != 1 {
		t.Fatalf("deliveries=%d", len(receipts))
	}
	state, err = n.GetAimeStatus(app, "aimesubscriber1")
	must(err)
	if state.Subscribed || state.Status != "available" || !state.Completed || state.Confirmations != 3 {
		t.Fatal(state)
	}
	for _, locale := range []string{"ko", "en", "ja"} {
		item := n.ItemLocalized(app, rows[0], locale)
		if item["title"] == "" || item["url"] != "/" {
			t.Fatal(item)
		}
	}
	must(recordAimeWithOperations(t, app, aimeObservation(403), now.Add(20*time.Minute)))
	must(recordAimeWithOperations(t, app, aimeObservation(200), now.Add(25*time.Minute)))
	must(n.DrainAimeRecoveries(app))
	rows, err = app.FindRecordsByFilter(n.Inbox, "kind={:kind}", "", 0, 0, dbx.Params{"kind": n.AimeKind})
	must(err)
	if len(rows) != 1 {
		t.Fatal("previous consent reused for next outage")
	}
}

func TestAimeFanoutSurvivesLaterFailureAndBatchBoundary(t *testing.T) {
	app := testutil.NewTestApp(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	must(recordAimeWithOperations(t, app, aimeObservation(403), now))
	for i := 0; i < 102; i++ {
		id := fmt.Sprintf("aimeuser%07d", i)
		_, err := app.DB().NewQuery("INSERT INTO user(id,email,tokenKey) VALUES ({:id},{:email},{:id})").Bind(dbx.Params{"id": id, "email": id + "@example.com"}).Execute()
		must(err)
		must(n.SetAimeSubscription(app, id, true))
	}
	for _, minute := range []int{5, 10, 15, 20} {
		must(recordAimeWithOperations(t, app, aimeObservation(200), now.Add(time.Duration(minute)*time.Minute)))
	}
	must(n.DrainAimeRecoveries(app))
	rows, err := app.FindRecordsByFilter(n.Inbox, "kind={:kind}", "", 0, 0, dbx.Params{"kind": n.AimeKind})
	must(err)
	if len(rows) != 100 {
		t.Fatalf("first batch=%d", len(rows))
	}
	must(recordAimeWithOperations(t, app, aimeObservation(403), now.Add(20*time.Minute)))
	must(n.DrainAimeRecoveries(app))
	must(n.DrainAimeRecoveries(app))
	rows, err = app.FindRecordsByFilter(n.Inbox, "kind={:kind}", "", 0, 0, dbx.Params{"kind": n.AimeKind})
	must(err)
	if len(rows) != 102 {
		t.Fatalf("resumed batch=%d", len(rows))
	}
	for _, name := range []string{n.AimeState, n.AimeSubscriptions, n.AimeProbes} {
		col, err := app.FindCollectionByNameOrId(name)
		must(err)
		if col.ListRule != nil || col.ViewRule != nil || col.CreateRule != nil || col.UpdateRule != nil || col.DeleteRule != nil {
			t.Fatalf("raw access enabled for %s", name)
		}
	}
}

func TestAimeConsecutiveConfirmationsAreDurableSpacedAndTerminal(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Now().UTC()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := app.DB().NewQuery("INSERT INTO user(id,email,tokenKey) VALUES ('confirmuser0001','confirm@example.com','confirmuser0001')").Execute()
	must(err)
	observe := func(code, minute, want int, complete bool) {
		t.Helper()
		must(recordAimeWithOperations(t, app, aimeObservation(code), now.Add(time.Duration(minute)*time.Minute)))
		result, err := n.GetAimeStatus(app, "confirmuser0001")
		must(err)
		if result.Confirmations != want || result.Completed != complete {
			t.Fatalf("minute%d HTTP%d: confirmations=%d completed=%v", minute, code, result.Confirmations, result.Completed)
		}
	}
	observe(403, 0, 0, false)
	observe(200, 1, 0, false) // first candidate alerts operations, no user confirmation yet
	observe(200, 2, 0, false) // rapid restart cannot advance the run
	observe(200, 6, 1, false)
	must(n.SetAimeSubscription(app, "confirmuser0001", true))
	observe(200, 7, 1, false)
	observe(200, 11, 2, false)
	for i, failure := range []int{403, 0, 500, 429} {
		minute := 16 + i*20
		observe(failure, minute, 0, false)
		observe(200, minute+5, 0, false)
		observe(200, minute+10, 1, false)
		observe(200, minute+15, 2, false)
	}
	observe(200, 96, 3, true)
	must(n.DrainAimeRecoveries(app))
	observe(403, 101, 3, true)
	if err := n.SetAimeSubscription(app, "confirmuser0001", true); !errors.Is(err, n.ErrAimeNotBlocked) {
		t.Fatalf("completed campaign reopened: %v", err)
	}
	observe(200, 106, 3, true)
	must(n.DrainAimeRecoveries(app))
	rows, err := app.FindRecordsByFilter(n.Inbox, "kind={:kind}", "", 0, 0, dbx.Params{"kind": n.AimeKind})
	must(err)
	if len(rows) != 1 {
		t.Fatalf("one-off event count=%d", len(rows))
	}
}

func TestAimeUnobservedGapRestartsConfirmationRun(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Now().UTC()
	for _, sample := range []struct{ minute, status int }{{0, 403}, {5, 200}, {10, 200}, {30, 200}} {
		if err := recordAimeWithOperations(t, app, aimeObservation(sample.status), now.Add(time.Duration(sample.minute)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	state, err := n.GetAimeStatus(app, "")
	if err != nil {
		t.Fatal(err)
	}
	if state.Completed || state.Confirmations != 0 {
		t.Fatal(state)
	}
}

func TestAimeHistoryContainsOnlyRealRecentObservations(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Now().UTC()
	for i := 0; i < 300; i++ {
		code := 403
		if i%3 == 0 {
			code = 0
		}
		if err := recordAimeWithOperations(t, app, aimeObservation(code), now.Add(time.Duration(i-299)*5*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	state, err := n.GetAimeStatus(app, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.History) == 0 || len(state.History) > 288 {
		t.Fatalf("history length=%d", len(state.History))
	}
	for i, probe := range state.History {
		if probe.Status != "blocked" && probe.Status != "unknown" {
			t.Fatal("fabricated uptime", probe)
		}
		if i > 0 && probe.CheckedAt < state.History[i-1].CheckedAt {
			t.Fatal("history is not chronological")
		}
	}
	rows, err := app.FindRecordsByFilter(n.AimeProbes, "checked_at<{:cutoff}", "", 0, 0, dbx.Params{"cutoff": now.Add(-24 * time.Hour).Format("2006-01-02 15:04:05.000Z")})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("expired probes retained=%d", len(rows))
	}
}

// Classified fixture observations; HTTP status alone is not a real probe.
func aimeObservation(code int) n.AimeObservation {
	status := "unknown"
	if code == 403 {
		status = "blocked"
	} else if code >= 200 && code < 300 {
		status = "available"
	}
	return n.AimeObservation{Status: status, HTTPStatus: code}
}

// Simulate successful operational channels after the first candidate is saved.
func recordAimeWithOperations(t *testing.T, app core.App, observation n.AimeObservation, at time.Time) error {
	t.Helper()
	if err := n.RecordAimeProbe(app, observation, at); err != nil {
		return err
	}
	rows, err := app.FindRecordsByFilter(n.AimeState, "ops_candidate_at!=''", "", 1, 0)
	if err != nil {
		return err
	}
	if len(rows) == 1 {
		rows[0].Set("ops_telegram_sent", true)
		rows[0].Set("ops_discord_sent", true)
		return app.Save(rows[0])
	}
	return nil
}
