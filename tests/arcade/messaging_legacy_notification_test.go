package arcade_test

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestLegacyArcadeNotificationsMutePlatformStaffAndQueuedPushes(t *testing.T) {
	app := newArcadeTestApp(t)
	_, requester := createAuthUserWithTags(t, app, []string{})
	_, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, staffOwner := createAuthUserWithTags(t, app, []string{"moderator", "arcade_owner"})
	staffToken, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	arcadeID, _ := seedPublicArcade(t, app, requester.Id, arcadeSeed{Name: "Guest inquiry", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	for _, u := range []*core.Record{owner, staffOwner} {
		u.Set("owns", []string{arcadeID})
		if err := app.Save(u); err != nil {
			t.Fatal(err)
		}
	}
	arcade, _ := app.FindRecordById("arcade", arcadeID)
	arcade.Set("inquiry_settings", map[string]any{"mode": "internal"})
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	res := executeJSONRequest(t, app, "POST", "/support_feedback", fmt.Sprintf(`{"message":"Guest help","arcade":%q}`, arcadeID), nil)
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode, body)
	}
	feedbackID := body["id"].(string)
	rows, err := app.FindRecordsByFilter("user_notification", "feedback={:f}", "", 0, 0, dbx.Params{"f": feedbackID})
	if err != nil || len(rows) != 2 {
		t.Fatalf("guest recipients %d %v", len(rows), err)
	}
	for _, n := range rows {
		if n.GetString("user") != owner.Id && n.GetString("user") != staffOwner.Id {
			t.Fatal("staff-only guest notification", n.Id)
		}
		user, _ := app.FindRecordById("user", n.GetString("user"))
		if !usernotification.FeedbackNotificationDeliverable(app, user, n) {
			t.Fatal("official guest notification suppressed")
		}
	}
	col, _ := app.FindCollectionByNameOrId("user_notification")
	queued := core.NewRecord(col)
	queued.Set("user", staff.Id)
	queued.Set("kind", "feedback_created")
	queued.Set("feedback", feedbackID)
	queued.Set("event_key", "legacy-queued")
	if err := app.Save(queued); err != nil {
		t.Fatal(err)
	}
	if usernotification.FeedbackNotificationDeliverable(app, staff, queued) {
		t.Fatal("old staff-only guest alert delivers")
	}
	// The suppressed queued row is still visible in the existing notification inbox.
	inbox := executeJSONRequest(t, app, "GET", "/user/notifications", "", map[string]string{"Authorization": "Bearer " + staffToken})
	var listed map[string]any
	_ = json.NewDecoder(inbox.Body).Decode(&listed)
	inbox.Body.Close()
	if inbox.StatusCode != 200 || len(listed["items"].([]any)) != 1 {
		t.Fatal(listed)
	}
	feedback, _ := app.FindRecordById("support_feedback", feedbackID)
	feedback.Set("createdBy", requester.Id)
	if err := app.Save(feedback); err != nil {
		t.Fatal(err)
	}
	messages, _ := app.FindCollectionByNameOrId(supportfeedback.Messages)
	makeReply := func(author string, staffReply bool) *core.Record {
		t.Helper()
		m := core.NewRecord(messages)
		m.Set("feedback", feedbackID)
		m.Set("author", author)
		m.Set("staff", staffReply)
		m.Set("message", "legacy reply")
		if err := app.Save(m); err != nil {
			t.Fatal(err)
		}
		if err := app.RunInTransaction(func(tx core.App) error { return usernotification.FeedbackReplyTx(tx, feedback, m) }); err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := makeReply(requester.Id, false)
	replies, err := app.FindRecordsByFilter("user_notification", "feedback_message={:m}", "", 0, 0, dbx.Params{"m": m.Id})
	if err != nil || len(replies) != 2 {
		t.Fatalf("legacy requester recipients %d %v", len(replies), err)
	}
	for _, n := range replies {
		u, _ := app.FindRecordById("user", n.GetString("user"))
		if !usernotification.FeedbackNotificationDeliverable(app, u, n) {
			t.Fatal("legacy owner follow-up suppressed")
		}
	}
	m = makeReply(staff.Id, true)
	replies, err = app.FindRecordsByFilter("user_notification", "feedback_message={:m}", "", 0, 0, dbx.Params{"m": m.Id})
	if err != nil || len(replies) != 1 || replies[0].GetString("user") != requester.Id || !usernotification.FeedbackNotificationDeliverable(app, requester, replies[0]) {
		t.Fatal("legacy staff reply requester policy", replies, err)
	}
	seedUserBan(t, app, requester.Id, "", "restricted", time.Time{})
	if usernotification.FeedbackNotificationDeliverable(app, requester, replies[0]) {
		t.Fatal("banned requester queued delivery")
	}
}
