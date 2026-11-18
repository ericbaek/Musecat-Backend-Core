package community_test

import (
	"net/http"
	"testing"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestPostLikesAreVisibleOnlyToTheAuthor(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, owner := createCommunityUser(t, app)
	fan, fanUser := createCommunityUser(t, app)
	stranger, _ := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	res := achievementRequest(t, app, "POST", "", token, map[string]string{"flair": "achievement", "game_series": series.Id}, 1)
	post := decodeBody(t, res)["id"].(string)
	for _, liker := range []string{token, fan} {
		expectSocialStatus(t, app, "PUT", "/community/post/like?id="+post, "", liker, 200)
	}

	path := "/community/post/likes?id=" + post
	expectSocialStatus(t, app, "GET", path, "", "", 401)
	expectSocialStatus(t, app, "GET", path, "", stranger, 403)
	expectSocialStatus(t, app, "GET", path, "", fan, 403)
	expectSocialStatus(t, app, "GET", "/community/post/likes?id=missing", "", token, 404)

	res = communityRequest(t, app, "GET", path, "", token)
	if res.StatusCode != 200 {
		t.Fatalf("author list: %d %#v", res.StatusCode, decodeBody(t, res))
	}
	if res.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("liker list must not be shared-cached")
	}
	data := decodeBody(t, res)
	items := data["items"].([]any)
	// The author's own like is not listed.
	if len(items) != 1 || items[0].(map[string]any)["user"] != fanUser.Id {
		t.Fatalf("likers: %#v (owner %s)", items, owner.Id)
	}
	if data["next_cursor"] != "" {
		t.Fatal("unexpected next cursor", data)
	}

	expectSocialStatus(t, app, "DELETE", "/community/post?id="+post, "", token, 204)
	expectSocialStatus(t, app, "GET", path, "", token, 404)
}

func TestPostLikesPaginateNewestFirst(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	res := achievementRequest(t, app, "POST", "", token, map[string]string{"flair": "achievement", "game_series": series.Id}, 1)
	post := decodeBody(t, res)["id"].(string)
	for range 31 {
		liker, _ := createCommunityUser(t, app)
		expectSocialStatus(t, app, "PUT", "/community/post/like?id="+post, "", liker, 200)
	}
	seen := map[string]bool{}
	cursor := ""
	for page := 0; page < 2; page++ {
		path := "/community/post/likes?id=" + post
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		data := decodeBody(t, communityRequest(t, app, "GET", path, "", token))
		for _, item := range data["items"].([]any) {
			id := item.(map[string]any)["user"].(string)
			if seen[id] {
				t.Fatal("duplicate liker across pages", id)
			}
			seen[id] = true
		}
		cursor, _ = data["next_cursor"].(string)
		if page == 0 && cursor == "" {
			t.Fatal("expected a second page")
		}
	}
	if len(seen) != 31 || cursor != "" {
		t.Fatalf("got %d likers, cursor %q", len(seen), cursor)
	}
	expectSocialStatus(t, app, "GET", "/community/post/likes?id="+post+"&cursor=bad!", "", token, 400)
}

func TestReceivedLikeCountOnGalleryAndActivity(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/user/activity", userhandler.GetUserActivity)
		return se.Next()
	})
	token, owner := createCommunityUser(t, app)
	fan, _ := createCommunityUser(t, app)
	other, _ := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	fields := map[string]string{"flair": "achievement", "game_series": series.Id}
	ids := []string{}
	for range 3 {
		res := achievementRequest(t, app, "POST", "", token, fields, 1)
		ids = append(ids, decodeBody(t, res)["id"].(string))
	}
	// Two likes from others on post 0, one on post 1, a self-like on post 2.
	for _, like := range []struct{ token, post string }{
		{fan, ids[0]}, {other, ids[0]}, {fan, ids[1]}, {token, ids[2]},
	} {
		expectSocialStatus(t, app, "PUT", "/community/post/like?id="+like.post, "", like.token, 200)
	}
	// Likes on removed posts no longer count.
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+ids[2], "", fan, 200)
	if _, err := app.DB().NewQuery("UPDATE community_post SET status='deleted' WHERE id={:id}").Bind(dbx.Params{"id": ids[2]}).Execute(); err != nil {
		t.Fatal(err)
	}

	gallery := decodeBody(t, communityRequest(t, app, http.MethodGet, "/community/posts?flair=achievement&author="+owner.Id+"&game_series="+series.Id+"&per_page=1", "", ""))
	if gallery["author_received_like_count"] != float64(3) {
		t.Fatalf("gallery received likes: %#v", gallery["author_received_like_count"])
	}
	feed := decodeBody(t, communityRequest(t, app, http.MethodGet, "/community/posts?flair=achievement", "", ""))
	if _, ok := feed["author_received_like_count"]; ok {
		t.Fatal("feed without an author must not report received likes")
	}
	activity := decodeBody(t, communityRequest(t, app, http.MethodGet, "/user/activity?id="+owner.Id, "", ""))
	if activity["received_like_count"] != float64(3) {
		t.Fatalf("activity received likes: %#v", activity["received_like_count"])
	}
}
