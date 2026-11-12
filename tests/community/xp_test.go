package community_test

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/xp"
	"github.com/pocketbase/dbx"
	"testing"
	"time"
)

func TestAchievementXPSeparateDailyCapsAndDurableLikes(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	ownerToken, owner := createCommunityUser(t, app)
	actorToken, actor := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	posts := []string{}
	for range 7 {
		r := achievementRequest(t, app, "POST", "", ownerToken, map[string]string{"flair": "achievement", "game_series": series.Id}, 1)
		if r.StatusCode != 201 {
			t.Fatal(r.StatusCode, decodeBody(t, r))
		}
		posts = append(posts, decodeBody(t, r)["id"].(string))
	}
	exp := func(user string) int {
		n, err := xp.LoadCurrentExp(app, user)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if exp(owner.Id) != 10 {
		t.Fatal("post cap", exp(owner.Id))
	}
	first := socialCreate(t, app, actorToken, posts[0], "", "", "comment")
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+posts[0], "", actorToken, 200)
	expectSocialStatus(t, app, "DELETE", "/community/post/like?id="+posts[0], "", actorToken, 200)
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+posts[0], "", actorToken, 200)
	if exp(actor.Id) != 2 {
		t.Fatal("duplicate like XP", exp(actor.Id))
	}
	ownerComment := socialCreate(t, app, ownerToken, posts[0], "", "", "owner comment")
	expectSocialStatus(t, app, "PUT", "/community/comment/like?id="+ownerComment["id"].(string), "", actorToken, 200)
	socialCreate(t, app, actorToken, posts[0], ownerComment["id"].(string), ownerComment["id"].(string), "reply")
	socialCreate(t, app, actorToken, posts[1], "", "", "another comment")
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+posts[1], "", actorToken, 200)
	if exp(actor.Id) != 5 {
		t.Fatal("shared reaction cap", exp(actor.Id))
	}
	expectSocialStatus(t, app, "DELETE", "/community/comment?id="+first["id"].(string), "", actorToken, 204)
	if exp(actor.Id) != 5 {
		t.Fatal("deletion reset XP")
	}
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+posts[0], "", ownerToken, 200)
	if exp(owner.Id) != 10 {
		t.Fatal("self interaction XP")
	}
	// Move the previous day's ledger without changing durable event identities.
	now := time.Now().UTC()
	yesterday := now.Add(-24 * time.Hour).Format("2006-01-02 15:04:05.000Z")
	if _, err := app.DB().NewQuery("UPDATE user_level_log SET created={:day} WHERE user={:user} AND kind LIKE 'xp:community:%'").Bind(dbx.Params{"day": yesterday, "user": actor.Id}).Execute(); err != nil {
		t.Fatal(err)
	}
	expectSocialStatus(t, app, "DELETE", "/community/post/like?id="+posts[0], "", actorToken, 200)
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+posts[0], "", actorToken, 200)
	if exp(actor.Id) != 5 {
		t.Fatal("old like paid again across days")
	}
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+posts[2], "", actorToken, 200)
	if exp(actor.Id) != 6 {
		t.Fatal("new day did not reset cap", exp(actor.Id))
	}
	n, err := app.CountRecords("user_level_log", dbx.NewExp("user={:u} AND kind={:kind}", dbx.Params{"u": actor.Id, "kind": fmt.Sprintf("xp:community:reaction:like-post:%s", posts[0])}))
	if err != nil || n != 1 {
		t.Fatal("missing durable ledger", n, err)
	}
}
