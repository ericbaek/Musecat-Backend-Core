package usernotification_test

import (
	"testing"

	n "github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// The recovery alert is retired, but historic aime_recovery inbox rows stay
// readable by their owner and keep rendering in every supported locale.
func TestRetiredAimeInboxRowsStillRender(t *testing.T) {
	app := testutil.NewTestApp(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"aimehistowner01", "aimehistother01"} {
		_, err := app.DB().NewQuery("INSERT INTO user (id,email,tokenKey) VALUES ({:id},{:email},{:id})").Bind(dbx.Params{"id": id, "email": id + "@example.com"}).Execute()
		must(err)
	}
	for _, name := range []string{n.AimeState, n.AimeProbes, n.AimeSubscriptions} {
		if _, err := app.FindCollectionByNameOrId(name); err == nil {
			t.Fatalf("fresh Core bootstrap still creates %s", name)
		}
	}
	col, err := app.FindCollectionByNameOrId(n.Inbox)
	must(err)
	save := func(event string) *core.Record {
		row := core.NewRecord(col)
		row.Set("user", "aimehistowner01")
		row.Set("kind", n.AimeKind)
		row.Set("event_key", event)
		must(app.Save(row))
		return row
	}
	recovery := save("aime:20261106T000000.000000000")
	update := save(n.AimeStatusUpdateEvent)

	owner, err := app.FindRecordById("user", "aimehistowner01")
	must(err)
	other, err := app.FindRecordById("user", "aimehistother01")
	must(err)
	if !n.Visible(app, owner, recovery) || n.Visible(app, other, recovery) || n.Visible(app, nil, recovery) {
		t.Fatal("historic Aime rows must stay owner-only")
	}
	owner.Set("withdrawn", true)
	if n.Visible(app, owner, recovery) {
		t.Fatal("withdrawn owner still sees Aime row")
	}

	for _, locale := range []string{"ko", "en", "ja"} {
		r, u := n.AimeItem(recovery, locale), n.AimeItem(update, locale)
		if r["kind"] != n.AimeKind || r["title"] == "" || r["message"] == "" || r["url"] != "/" {
			t.Fatalf("recovery %s: %v", locale, r)
		}
		if u["title"] == "" || u["title"] == r["title"] || u["message"] == r["message"] {
			t.Fatalf("status update rendered as recovery %s: %v", locale, u)
		}
	}
	if n.Item(app, recovery)["title"] != n.AimeItem(recovery, "ko")["title"] {
		t.Fatal("Item does not route Aime rows")
	}
}
