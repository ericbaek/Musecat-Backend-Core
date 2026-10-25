package arcade_test

import (
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestArcadeSetupNoticeDurableAndCurrentRole(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	_, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, ordinary := createAuthUserWithTags(t, app, []string{})
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	id, _ := seedPublicArcade(t, app, ordinary.Id, arcadeSeed{Name: "Setup", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{id})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	var device *core.Record
	if err := app.RunInTransaction(func(tx core.App) error {
		var err error
		device, err = usernotification.UpsertDeviceTx(tx, owner.Id, "https://fcm.googleapis.com/fcm/send/setup", map[string]any{"endpoint": "setup"}, "ko")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	ensure := func(user *core.Record) {
		t.Helper()
		if err := app.RunInTransaction(func(tx core.App) error { return usernotification.EnsureArcadeSetupNoticeTx(tx, user) }); err != nil {
			t.Fatal(err)
		}
	}
	ensure(ordinary)
	ensure(staff)
	ensure(owner)
	rows, err := app.FindRecordsByFilter(usernotification.Inbox, "event_key={:event}", "", 0, 0, dbx.Params{"event": usernotification.ArcadeSetupEvent})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %d: %v", len(rows), err)
	}
	notice := rows[0]
	if notice.GetString("actor") != "" || notice.GetString("arcade") != "" {
		t.Fatal("notice must be account scoped")
	}
	item := usernotification.Item(app, notice)
	if item["url"] != "/account/arcade-dashboard/setup" || item["message"] != "새 관리자 대시보드를 설정해주세요" {
		t.Fatal(item)
	}
	deliveries, err := app.FindRecordsByFilter(usernotification.Deliveries, "notification={:notice}", "", 0, 0, dbx.Params{"notice": notice.Id})
	if err != nil || len(deliveries) != 1 || deliveries[0].GetString("device") != device.Id {
		t.Fatalf("deliveries %v %v", deliveries, err)
	}
	notice.Set("read", true)
	if err := app.Save(notice); err != nil {
		t.Fatal(err)
	}
	ensure(owner)
	again, _ := app.FindRecordById(usernotification.Inbox, notice.Id)
	if !again.GetBool("read") {
		t.Fatal("rerun reset read")
	}
	deliveries, _ = app.FindRecordsByFilter(usernotification.Deliveries, "notification={:notice}", "", 0, 0, dbx.Params{"notice": notice.Id})
	if len(deliveries) != 1 {
		t.Fatal("rerun duplicated delivery")
	}
	arcade, _ := app.FindRecordById("arcade", id)
	arcade.Set("inquiry_settings", map[string]any{"mode": "disabled"})
	arcade.Set("report_settings", map[string]any{"mode": "external", "contactType": "url", "contactValue": "https://example.com"})
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	if !usernotification.Visible(app, owner, notice) {
		t.Fatal("OFF hid setup notice")
	}
	owner.Set("tags", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	if usernotification.Visible(app, owner, notice) {
		t.Fatal("revoked owner retained notice")
	}
	owner.Set("tags", []string{"arcade_owner"})
	owner.Set("withdrawn", true)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	if usernotification.Visible(app, owner, notice) {
		t.Fatal("withdrawn owner retained notice")
	}
}
