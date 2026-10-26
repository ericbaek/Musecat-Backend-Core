package arcade_test

import (
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"strings"
	"testing"
)

func TestMessagingNotificationGroupsPreviewAndRead(t *testing.T) {
	app := newArcadeTestApp(t)
	_, sender := createAuthUserWithTags(t, app, []string{})
	token, recipient := createAuthUserWithTags(t, app, []string{})
	_, other := createAuthUserWithTags(t, app, []string{})
	c, err := messaging.Create(app, sender.Id, messaging.CreateInput{Kind: "dm", PeerUserID: recipient.Id})
	if err != nil {
		t.Fatal(err)
	}
	send := func(i int) string {
		t.Helper()
		m, _, err := messaging.Send(app, sender.Id, c.ID, messaging.SendInput{Text: fmt.Sprintf("내용 %d", i), ClientMessageID: fmt.Sprint(i)})
		if err != nil {
			t.Fatal(err)
		}
		n, err := app.FindFirstRecordByFilter(usernotification.Inbox, "user={:u} && message={:m}", dbx.Params{"u": recipient.Id, "m": m.ID})
		if err != nil {
			t.Fatal(err)
		}
		return n.Id
	}
	call := func(method, path, body string) map[string]any {
		t.Helper()
		res := executeJSONRequest(t, app, method, path, body, map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d %v", path, res.StatusCode, out)
		}
		return out
	}
	first := send(1)
	for i := 2; i <= 10; i++ {
		send(i)
	}
	// Same-timestamp random event IDs must not change latest-message selection.
	if _, err := app.DB().NewQuery("UPDATE user_notification SET created='2026-10-01 00:00:00.000Z' WHERE conversation={:c}").Bind(dbx.Params{"c": c.ID}).Execute(); err != nil {
		t.Fatal(err)
	}
	inbox := call("GET", "/user/notifications", "")
	items := inbox["items"].([]any)
	if len(items) != 1 || inbox["unread"] != float64(1) {
		t.Fatal(inbox)
	}
	latest := items[0].(map[string]any)
	if latest["message"] != "내용 10" || latest["title"] == "" || latest["title"] == "뮤즈캣" {
		t.Fatal(latest)
	}
	page := call("GET", "/user/notifications?before="+latest["id"].(string), "")
	if len(page["items"].([]any)) != 0 {
		t.Fatal("older group members reappear", page)
	}
	call("POST", "/user/notifications/read", fmt.Sprintf(`{"id":%q}`, first))
	if got := call("GET", "/user/notifications", ""); got["unread"] != float64(1) {
		t.Fatal("stale acknowledgement cleared newer messages", got)
	}
	if _, err := messaging.SetRead(app, recipient.Id, c.ID, 9); err != nil {
		t.Fatal(err)
	}
	if got := call("GET", "/user/notifications", ""); got["unread"] != float64(1) {
		t.Fatal("partial read cleared latest", got)
	}
	call("POST", "/user/notifications/read", fmt.Sprintf(`{"id":%q}`, latest["id"]))
	if got := call("GET", "/user/notifications", ""); got["unread"] != float64(0) {
		t.Fatal(got)
	}
	send(11)
	if _, err := messaging.SetRead(app, recipient.Id, c.ID, 11); err != nil {
		t.Fatal(err)
	}
	if got := call("GET", "/user/notifications", ""); got["unread"] != float64(0) {
		t.Fatal("conversation read did not clear notification", got)
	}
	otherC, err := messaging.Create(app, other.Id, messaging.CreateInput{Kind: "dm", PeerUserID: recipient.Id})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := messaging.Send(app, other.Id, otherC.ID, messaging.SendInput{Text: "다른 대화", ClientMessageID: "other"}); err != nil {
		t.Fatal(err)
	}
	if got := call("GET", "/user/notifications", ""); len(got["items"].([]any)) != 2 || got["unread"] != float64(1) {
		t.Fatal("different conversations merged", got)
	}
	n, err := app.FindFirstRecordByFilter(usernotification.Inbox, "user={:u} && conversation={:c}", dbx.Params{"u": recipient.Id, "c": otherC.ID})
	if err != nil {
		t.Fatal(err)
	}
	m, err := app.FindRecordById(messaging.Messages, n.GetString("message"))
	if err != nil {
		t.Fatal(err)
	}
	m.Set("text", strings.Repeat("한", 200))
	if err := app.Save(m); err != nil {
		t.Fatal(err)
	}
	item := messaging.NotificationItem(app, n)
	if len([]rune(item["message"].(string))) != 161 {
		t.Fatal("preview is not rune bounded", item)
	}
	m.Set("text", "")
	m.Set("legacy_photos", []string{"photo.jpg"})
	if err := app.Save(m); err != nil {
		t.Fatal(err)
	}
	if got := messaging.NotificationItemLocalized(app, n, "en"); got["message"] != "Sent a photo" {
		t.Fatal(got)
	}
}
