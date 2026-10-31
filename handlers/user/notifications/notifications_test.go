package notifications

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type inboxQueryApp struct {
	core.App
	queries, sourceReads int
	inboxQueries         []*dbx.SelectQuery
}

func (app *inboxQueryApp) FindRecordsByFilter(collection any, filter, sort string, limit, offset int, params ...dbx.Params) ([]*core.Record, error) {
	if collection == usernotification.Inbox {
		if limit <= 0 || limit > inboxBatchSize || offset != 0 {
			return nil, fmt.Errorf("unbounded inbox query: %d/%d", limit, offset)
		}
		app.queries++
	}
	rows, err := app.App.FindRecordsByFilter(collection, filter, sort, limit, offset, params...)
	return rows, err
}
func (app *inboxQueryApp) RecordQuery(collection any) *dbx.SelectQuery {
	query := app.App.RecordQuery(collection)
	if collection == usernotification.Inbox {
		app.queries++
		app.inboxQueries = append(app.inboxQueries, query)
	}
	return query
}
func (app *inboxQueryApp) FindRecordById(collection any, id string, expand ...func(*dbx.SelectQuery) error) (*core.Record, error) {
	app.sourceReads++
	return app.App.FindRecordById(collection, id, expand...)
}
func TestInboxBoundedPagingAndCurrentVisibility(t *testing.T) {
	base := testutil.NewTestApp(t)
	defer base.Cleanup()
	defer testutil.CleanupGoldenDir()
	app := &inboxQueryApp{App: base}
	col, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(col)
	user.Id = "inboxrecipient1"
	user.Set("tags", []string{})
	exec := func(sql string, params dbx.Params) {
		t.Helper()
		if _, err := app.DB().NewQuery(sql).Bind(params).Execute(); err != nil {
			t.Fatal(err)
		}
	}
	// Raw fixtures isolate reads from unrelated aggregate mutation hooks.
	for _, id := range []string{"visiblearcade01", "privatearcade01", "externalarcade1"} {
		settings := `{"mode":"internal"}`
		if id == "externalarcade1" {
			settings = `{"mode":"external"}`
		}
		exec("INSERT INTO arcade (id, public, report_settings) VALUES ({:id}, {:public}, {:settings})", dbx.Params{"id": id, "public": id != "privatearcade01", "settings": settings})
	}
	insert := func(n int, arcade, kind string, read bool) {
		t.Helper()
		exec("INSERT INTO user_notification (id, user, kind, arcade, read, event_key, created) VALUES ({:id}, {:user}, {:kind}, {:arcade}, {:read}, {:id}, {:created})", dbx.Params{"id": fmt.Sprintf("notice%09d", n), "user": user.Id, "kind": kind, "arcade": arcade, "read": read, "created": "2026-10-01 00:00:00.000Z"})
	}
	// Sparse visibility crosses batches; equal timestamps require id tie-breaking.
	for i := 0; i < 250; i++ {
		arcade := "privatearcade01"
		if i%10 == 0 {
			arcade = "visiblearcade01"
		}
		insert(i, arcade, "report_reaction", i%2 == 0)
	}
	insert(300, "externalarcade1", "report_reaction", false)
	insert(301, "visiblearcade01", "report_created", false)
	rows, unread, next, err := listInbox(app, user, "")
	if err != nil || len(rows) != 20 || unread != 0 || next != "notice000000050" {
		t.Fatalf("first page len=%d unread=%d next=%q err=%v", len(rows), unread, next, err)
	}
	for i, row := range rows {
		if want := fmt.Sprintf("notice%09d", 240-i*10); row.Id != want {
			t.Fatalf("item %d=%s want %s", i, row.Id, want)
		}
	}
	if app.sourceReads != 4 {
		t.Fatalf("shared sources: got %d permission reads want 4", app.sourceReads)
	}
	rows, unread, next, err = listInbox(app, user, next)
	if err != nil || len(rows) != 5 || unread != 0 || next != "" || rows[0].Id != "notice000000040" {
		t.Fatalf("second page len=%d unread=%d next=%q err=%v", len(rows), unread, next, err)
	}
	user.Set("tags", []string{"arcade_owner"})
	user.Set("owns", []string{"privatearcade01", "visiblearcade01"})
	rows, unread, _, err = listInbox(app, user, "")
	if err != nil || len(rows) != 20 || rows[0].Id != "notice000000301" || unread != 126 {
		t.Fatalf("owner visibility len=%d unread=%d err=%v", len(rows), unread, err)
	}
	user.Set("tags", []string{})
	insert(302, "visiblearcade01", "report_reaction", false)
	exec("UPDATE user_notification SET user='anotheruser0001' WHERE id='notice000000302'", nil)
	for _, cursor := range []string{"missing", "notice000000302"} {
		rows, unread, next, err = listInbox(app, user, cursor)
		if err != nil || len(rows) != 0 || unread != 0 || next != "" {
			t.Fatalf("missing cursor: %v %d %q %v", rows, unread, next, err)
		}
	}
	// Already-read history must not be fetched for unread counting.
	exec("DELETE FROM user_notification", nil)
	for i := 0; i < 1000; i++ {
		insert(i, "visiblearcade01", "report_reaction", true)
	}
	app.queries, app.sourceReads = 0, 0
	app.inboxQueries = nil
	rows, unread, next, err = listInbox(app, user, "")
	if err != nil || len(rows) != 20 || unread != 0 || next == "" {
		t.Fatalf("dense history len=%d unread=%d next=%q err=%v", len(rows), unread, next, err)
	}
	if app.queries != 2 || app.sourceReads != 1 {
		t.Fatalf("history amplified polling: queries=%d sources=%d", app.queries, app.sourceReads)
	}
	for _, query := range app.inboxQueries {
		sql := query.Build().SQL()
		if !strings.Contains(sql, "LIMIT 100") || strings.Contains(sql, "OFFSET") {
			t.Fatalf("unbounded inbox query: %s", sql)
		}
	}
	// Feedback shares the current access check across reply/created event kinds,
	// but never carries staff authorization into the next polling request.
	exec("DELETE FROM user_notification", nil)
	exec("INSERT INTO user (id,username,tags) VALUES ('inboxrecipient1','recipient','[\"moderator\"]')", nil)
	exec("INSERT INTO messaging_conversation (id,kind) VALUES ('inquiry00000001','inquiry')", nil)
	exec("INSERT INTO messaging_inquiry (id,conversation,requester,inbox_type,inbox_id) VALUES ('inquirycase0001','inquiry00000001','anotheruser0001','team','musecat')", nil)
	for i, kind := range []string{"message", "message"} {
		insert(i, "", kind, false)
	}
	exec("UPDATE user_notification SET conversation='inquiry00000001'", nil)
	user.Set("tags", []string{"moderator"})
	app.sourceReads = 0
	rows, unread, _, err = listInbox(app, user, "")
	if err != nil || len(rows) != 1 || unread != 1 || app.sourceReads != 2 {
		t.Fatalf("feedback staff access: len=%d unread=%d sources=%d err=%v", len(rows), unread, app.sourceReads, err)
	}
	user.Set("tags", []string{})
	exec("UPDATE user SET tags='[]' WHERE id='inboxrecipient1'", nil)
	rows, unread, _, err = listInbox(app, user, "")
	if err != nil || len(rows) != 0 || unread != 0 {
		t.Fatalf("revoked feedback access: len=%d unread=%d err=%v", len(rows), unread, err)
	}

}
