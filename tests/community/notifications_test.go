package community_test

import (
	"fmt"
	notificationhandler "github.com/ericbaek/musecat-backend-core/handlers/user/notifications"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"strings"
	"testing"
	"time"
)

func TestAchievementNotificationGroupingAndViewing(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		g := se.Router.Group("/user/notifications").Bind(apis.RequireAuth("user"))
		g.GET("", notificationhandler.List)
		g.POST("/read", notificationhandler.Read)
		return se.Next()
	})
	ownerToken, owner := createCommunityUser(t, app)
	aToken, a := createCommunityUser(t, app)
	bToken, _ := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	postResponse := achievementRequest(t, app, "POST", "", ownerToken, map[string]string{"flair": "achievement", "game_series": series.Id}, 1)
	post := decodeBody(t, postResponse)["id"].(string)
	if _, err := usernotification.UpsertDeviceTx(app, owner.Id, "https://fcm.googleapis.com/fcm/send/community-test", map[string]string{"endpoint": "test"}, "ko"); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, token string) map[string]any {
		t.Helper()
		res := communityRequest(t, app, method, path, body, token)
		data := decodeBody(t, res)
		if res.StatusCode != 200 {
			t.Fatalf("%s %s: %d %#v", method, path, res.StatusCode, data)
		}
		return data
	}
	inbox := func(token string) map[string]any { return call("GET", "/user/notifications?locale=ko", "", token) }
	count := func(name string) int {
		n, err := app.CountRecords(name)
		if err != nil {
			t.Fatal(err)
		}
		return int(n)
	}
	call("PUT", "/community/post/like?id="+post, "", aToken)
	call("PUT", "/community/post/like?id="+post, "", aToken)
	call("PUT", "/community/post/like?id="+post, "", bToken)
	call("PUT", "/community/post/like?id="+post, "", ownerToken)
	box := inbox(ownerToken)
	items := box["items"].([]any)
	if len(items) != 1 || box["unread"] != float64(1) || !strings.Contains(items[0].(map[string]any)["message"].(string), "외 1명") || count(usernotification.Deliveries) != 0 {
		t.Fatal(box)
	}
	notice := items[0].(map[string]any)["id"].(string)
	call("POST", "/user/notifications/read", fmt.Sprintf(`{"id":%q}`, notice), ownerToken)
	call("DELETE", "/community/post/like?id="+post, "", aToken)
	call("PUT", "/community/post/like?id="+post, "", aToken)
	if count(usernotification.Inbox) != 2 || inbox(ownerToken)["unread"] != float64(0) {
		t.Fatal("re-like generated an alert", count(usernotification.Inbox), inbox(ownerToken))
	}
	call("DELETE", "/community/post/like?id="+post, "", bToken)
	if len(inbox(ownerToken)["items"].([]any)) != 1 {
		t.Fatal("unlike invalidated other actors")
	}
	c1 := socialCreate(t, app, aToken, post, "", "", "first")
	socialCreate(t, app, bToken, post, "", "", "second")
	box = inbox(ownerToken)
	if len(box["items"].([]any)) != 2 || box["unread"] != float64(1) || count(usernotification.Deliveries) != 2 {
		t.Fatal(box)
	}
	socialCreate(t, app, ownerToken, post, c1["id"].(string), c1["id"].(string), "reply")
	if got := inbox(aToken); len(got["items"].([]any)) != 1 || !strings.Contains(got["items"].([]any)[0].(map[string]any)["url"].(string), "?comment=") {
		t.Fatal(got)
	}
	socialCreate(t, app, aToken, post, c1["id"].(string), c1["id"].(string), "self reply")
	rows, _ := app.FindRecordsByFilter(usernotification.Inbox, "user={:u}", "", 0, 0, dbx.Params{"u": a.Id})
	if len(rows) != 1 {
		t.Fatal("self reply notified")
	}
	expectSocialStatus(t, app, "PUT", "/community/post/view?id="+post, `{"session":"tab-a"}`, "", 401)
	call("PUT", "/community/post/view?id="+post, `{"session":"tab-a"}`, ownerToken)
	call("PUT", "/community/post/view?id="+post, `{"session":"tab-b"}`, ownerToken)
	call("DELETE", "/community/post/view?id="+post, `{"session":"tab-a"}`, ownerToken)
	before := count(usernotification.Deliveries)
	socialCreate(t, app, bToken, post, "", "", "seen live")
	if count(usernotification.Deliveries) != before || inbox(ownerToken)["unread"] != float64(0) {
		t.Fatal("viewing must suppress push and mark events read")
	}
	views, _ := app.FindAllRecords(usernotification.CommunityViews)
	for _, v := range views {
		v.Set("expires", time.Now().Add(-time.Second))
		if err := app.Save(v); err != nil {
			t.Fatal(err)
		}
	}
	c2 := socialCreate(t, app, bToken, post, "", "", "after lease")
	if count(usernotification.Deliveries) != before+1 {
		t.Fatal("expired view suppressed delivery")
	}
	// Retraction is checked at read and delivery, not just at event creation.
	expectSocialStatus(t, app, "DELETE", "/community/comment?id="+c2["id"].(string), "", bToken, 204)
	for _, n := range mustCommunityNotices(t, app, owner.Id) {
		if n.GetString("community_comment") == c2["id"] && usernotification.Visible(app, owner, n) {
			t.Fatal("deleted source still visible")
		}
	}
}
func mustCommunityNotices(t *testing.T, app core.App, user string) []*core.Record {
	t.Helper()
	rows, err := app.FindRecordsByFilter(usernotification.Inbox, "user={:user}", "", 0, 0, dbx.Params{"user": user})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}
