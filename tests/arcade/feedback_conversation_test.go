package arcade_test

import (
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"testing"
)

func TestFeedbackConversationAccessRepliesAndNotifications(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	authorToken, author := createAuthUserWithTags(t, app, []string{})
	otherToken, _ := createAuthUserWithTags(t, app, []string{})
	staffToken, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	call := func(method, path, body, token string, status int) map[string]any {
		t.Helper()
		headers := map[string]string{}
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		res := executeJSONRequest(t, app, method, path, body, headers)
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != status {
			t.Fatalf("%s %s: %d want %d: %v", method, path, res.StatusCode, status, out)
		}
		return out
	}
	feedback := call("POST", "/support_feedback", `{"message":"Need help"}`, authorToken, 200)
	id := feedback["id"].(string)
	thread := call("GET", "/user/feedback/thread?id="+id, "", authorToken, 200)
	if thread["feedback"].(map[string]any)["message"] != "Need help" {
		t.Fatal(thread)
	}
	call("GET", "/user/feedback/thread?id="+id, "", otherToken, 404)
	call("GET", "/user/feedback/thread?id="+id, "", "", 401)
	reply := func(token, message string, status int) map[string]any {
		return call("POST", "/user/feedback/thread", fmt.Sprintf(`{"feedbackId":%q,"message":%q}`, id, message), token, status)
	}
	reply(otherToken, "stolen", 404)
	reply(authorToken, " ", 400)
	adminReply := reply(staffToken, "Please try again", 201)
	if adminReply["staff"] != true || adminReply["author"] != staff.Id {
		t.Fatal(adminReply)
	}
	unread := call("GET", "/user/notifications", "", authorToken, 200)
	if unread["unread"] != float64(1) {
		t.Fatal("author not notified", unread)
	}
	item := unread["items"].([]any)[0].(map[string]any)
	if item["kind"] != "message" || item["url"] != "/messages?conversation="+id {
		t.Fatal(item)
	}
	reply(authorToken, "It still fails", 201)
	moderatorInbox := call("GET", "/user/notifications", "", staffToken, 200)
	if moderatorInbox["unread"] != float64(1) {
		t.Fatal("staff not notified", moderatorInbox)
	}
	reply(staffToken, "Now fixed", 201)
	if got := call("GET", "/user/notifications", "", authorToken, 200); got["unread"] != float64(1) {
		t.Fatal(got)
	}
	if got := call("GET", "/user/notifications", "", otherToken, 200); got["unread"] != float64(0) {
		t.Fatal("private inquiry leaked", got)
	}
	thread = call("GET", "/user/feedback/thread?id="+id, "", authorToken, 200)
	if len(thread["items"].([]any)) != 3 {
		t.Fatal(thread)
	}
	feedbackRecord, _ := app.FindFirstRecordByFilter(messaging.Inquiries, "legacy_feedback_id={:id}", map[string]any{"id": id})
	if thread["feedback"].(map[string]any)["message"] != "Need help" || feedbackRecord.GetString("status") != "recognised" {
		t.Fatal("original/status damaged")
	}
	// The filter is not a status action: participants explicitly resolve/reopen.
	statusBody := func(status string) string { return fmt.Sprintf(`{"feedbackId":%q,"status":%q}`, id, status) }
	call("PATCH", "/user/feedback/thread", statusBody("solved"), otherToken, 404)
	call("PATCH", "/user/feedback/thread", statusBody("solved"), "", 401)
	call("PATCH", "/user/feedback/thread", statusBody("recognised"), authorToken, 400)
	call("PATCH", "/user/feedback/thread", statusBody("solved"), authorToken, 400)
	call("PATCH", "/user/feedback/thread", statusBody("solved"), staffToken, 200)
	feedbackRecord, _ = app.FindFirstRecordByFilter(messaging.Inquiries, "legacy_feedback_id={:id}", map[string]any{"id": id})
	if feedbackRecord.GetString("status") != "solved" {
		t.Fatal("staff could not resolve")
	}
	call("PATCH", "/user/feedback/thread", statusBody("waiting"), staffToken, 200)
	feedbackRecord, _ = app.FindFirstRecordByFilter(messaging.Inquiries, "legacy_feedback_id={:id}", map[string]any{"id": id})
	if feedbackRecord.GetString("status") != "waiting" {
		t.Fatal("staff could not reopen")
	}
	call("PATCH", "/user/feedback/thread", statusBody("solved"), staffToken, 200)
	call("PATCH", "/user/feedback/thread", statusBody("waiting"), authorToken, 200)
	// Role removal revokes private staff notifications and thread access.
	staff.Set("tags", []string{})
	staff.Set("tag", []string{})
	if err := app.Save(staff); err != nil {
		t.Fatal(err)
	}
	call("GET", "/user/feedback/thread?id="+id, "", staffToken, 404)
	call("PATCH", "/user/feedback/thread", statusBody("solved"), staffToken, 404)
	if got := call("GET", "/user/notifications", "", staffToken, 200); got["unread"] != float64(0) {
		t.Fatal("revoked staff saw inquiry", got)
	}
	// Migration is repeatable and keeps existing messages/read state.
	if err := messaging.EnsureSchema(app); err != nil {
		t.Fatal(err)
	}
	if thread = call("GET", "/user/feedback/thread?id="+id, "", authorToken, 200); len(thread["items"].([]any)) != 7 {
		t.Fatal("migration lost messages")
	}
	_ = author
}

func TestArcadeInquiryOfficialAccountIsolationAndAlerts(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	authorToken, author := createAuthUserWithTags(t, app, []string{})
	ownerToken, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	otherToken, other := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	staffToken, _ := createAuthUserWithTags(t, app, []string{"developer"})
	arcadeID, _ := seedPublicArcade(t, app, author.Id, arcadeSeed{Name: "Inquiry arcade", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{arcadeID})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	// This conversation fixture explicitly enables reception for its official owner.
	configuredArcade, _ := app.FindRecordById("arcade", arcadeID)
	configuredArcade.Set("inquiry_settings", map[string]any{"mode": "internal"})
	if err := app.Save(configuredArcade); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, token string, want int) map[string]any {
		t.Helper()
		headers := map[string]string{}
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		res := executeJSONRequest(t, app, method, path, body, headers)
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s status=%d want=%d out=%v", method, path, res.StatusCode, want, out)
		}
		return out
	}
	createBody := fmt.Sprintf(`{"message":"Arcade inquiry","arcade":%q}`, arcadeID)
	inquiry := call("POST", "/support_feedback", createBody, authorToken, 200)
	id := inquiry["id"].(string)
	ownerInbox := call("GET", "/user/notifications", "", ownerToken, 200)
	if ownerInbox["unread"] != float64(1) || ownerInbox["items"].([]any)[0].(map[string]any)["kind"] != "message" {
		t.Fatal("official account did not receive new inquiry", ownerInbox)
	}
	call("GET", "/user/feedback/thread?id="+id, "", otherToken, 404)
	call("GET", "/user/feedback/thread?id="+id, "", ownerToken, 200)
	if got := call("GET", "/user/feedback?arcade="+arcadeID, "", ownerToken, 200); len(got["items"].([]any)) != 1 {
		t.Fatal("dashboard inbox missing", got)
	}
	if got := call("GET", "/user/feedback?arcade="+arcadeID, "", staffToken, 200); len(got["items"].([]any)) != 1 {
		t.Fatal("staff dashboard missing", got)
	}
	if got := call("GET", "/user/feedback?arcade="+arcadeID, "", otherToken, 200); len(got["items"].([]any)) != 0 {
		t.Fatal("cross-arcade inbox leak", got)
	}
	reply := call("POST", "/user/feedback/thread", fmt.Sprintf(`{"feedbackId":%q,"message":"Official answer"}`, id), ownerToken, 201)
	if reply["official"] != true || reply["staff"] != false {
		t.Fatal("official role not derived", reply)
	}
	if got := call("GET", "/user/notifications", "", authorToken, 200); got["unread"] != float64(1) {
		t.Fatal("author not notified", got)
	}
	call("POST", "/user/feedback/thread", fmt.Sprintf(`{"feedbackId":%q,"message":"Follow up"}`, id), authorToken, 201)
	if got := call("GET", "/user/notifications", "", ownerToken, 200); got["unread"] != float64(1) {
		t.Fatal("official followup alert missing", got)
	}
	statusBody := fmt.Sprintf(`{"feedbackId":%q,"status":"solved"}`, id)
	call("PATCH", "/user/feedback/thread", statusBody, otherToken, 404)
	call("PATCH", "/user/feedback/thread", statusBody, ownerToken, 200)
	record, _ := app.FindFirstRecordByFilter(messaging.Inquiries, "legacy_feedback_id={:id}", map[string]any{"id": id})
	if record.GetString("status") != "solved" {
		t.Fatal("official could not resolve")
	}
	// A follow-up after official resolution reopens the conversation and alerts responders.
	x, y := elliptic.P256().ScalarBaseMult([]byte{1})
	key := base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y))
	deviceAuth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	call("PUT", "/user/notifications/device", fmt.Sprintf(`{"subscription":{"endpoint":"https://fcm.googleapis.com/fcm/send/inquiry-owner","keys":{"p256dh":%q,"auth":%q}},"locale":"en"}`, key, deviceAuth), ownerToken, 200)
	followup := call("POST", "/user/feedback/thread", fmt.Sprintf(`{"feedbackId":%q,"message":"Question after resolution"}`, id), authorToken, 201)
	record, _ = app.FindFirstRecordByFilter(messaging.Inquiries, "legacy_feedback_id={:id}", map[string]any{"id": id})
	if record.GetString("status") != "waiting" {
		t.Fatal("requester follow-up did not reopen resolved inquiry")
	}
	inbox := call("GET", "/user/notifications", "", ownerToken, 200)
	if inbox["unread"] != float64(1) {
		t.Fatal("resolved follow-up did not alert official", inbox)
	}
	notification, err := app.FindFirstRecordByFilter(usernotification.Inbox, "user={:user} && message={:message}", map[string]any{"user": owner.Id, "message": followup["id"]})
	if err != nil || notification.GetString("kind") != "message" {
		t.Fatal("missing follow-up notification", err)
	}
	deliveries, err := app.FindRecordsByFilter(usernotification.Deliveries, "notification={:notification}", "", 0, 0, map[string]any{"notification": notification.Id})
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("official device follow-up delivery count=%d err=%v", len(deliveries), err)
	}
	call("PATCH", "/user/feedback/thread", fmt.Sprintf(`{"feedbackId":%q,"status":"waiting"}`, id), ownerToken, 200)
	// Owning another arcade never grants access to general MUSECAT inquiries.
	general := call("POST", "/support_feedback", `{"message":"Private MUSECAT inquiry"}`, authorToken, 200)
	call("GET", "/user/feedback/thread?id="+general["id"].(string), "", ownerToken, 404)
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	call("GET", "/user/feedback/thread?id="+id, "", ownerToken, 404)
	call("PATCH", "/user/feedback/thread", statusBody, ownerToken, 404)
	if got := call("GET", "/user/notifications", "", ownerToken, 200); got["unread"] != float64(0) {
		t.Fatal("former official account retained alerts", got)
	}
	_ = other
}
