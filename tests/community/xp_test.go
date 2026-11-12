package community_test

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/xp"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
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
	if exp(actor.Id) != 1 {
		t.Fatal("cancelled like XP was re-awarded", exp(actor.Id))
	}
	ownerComment := socialCreate(t, app, ownerToken, posts[0], "", "", "owner comment")
	expectSocialStatus(t, app, "PUT", "/community/comment/like?id="+ownerComment["id"].(string), "", actorToken, 200)
	socialCreate(t, app, actorToken, posts[0], ownerComment["id"].(string), ownerComment["id"].(string), "reply")
	socialCreate(t, app, actorToken, posts[1], "", "", "another comment")
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+posts[1], "", actorToken, 200)
	if exp(actor.Id) != 4 {
		t.Fatal("shared reaction cap", exp(actor.Id))
	}
	expectSocialStatus(t, app, "DELETE", "/community/comment?id="+first["id"].(string), "", actorToken, 204)
	if exp(actor.Id) != 3 {
		t.Fatal("comment XP was not revoked")
	}
	socialCreate(t, app, actorToken, posts[1], "", "", "still capped after deletion")
	if exp(actor.Id) != 3 {
		t.Fatal("deletion refunded the daily cap")
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
	if exp(actor.Id) != 3 {
		t.Fatal("old like paid again across days")
	}
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+posts[2], "", actorToken, 200)
	if exp(actor.Id) != 4 {
		t.Fatal("new day did not reset cap", exp(actor.Id))
	}
	n, err := app.CountRecords("user_level_log", dbx.NewExp("user={:u} AND kind={:kind}", dbx.Params{"u": actor.Id, "kind": fmt.Sprintf("xp:community:reaction:like-post:%s", posts[0])}))
	if err != nil || n != 1 {
		t.Fatal("missing durable ledger", n, err)
	}
}

func TestCommunityMutationFeedbackOnlyReportsActualActorGrant(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	ownerToken, owner := createCommunityUser(t, app)
	actorToken, actor := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	create := func() map[string]any {
		r := achievementRequest(t, app, "POST", "", ownerToken, map[string]string{"flair": "achievement", "game_series": series.Id}, 1)
		if r.StatusCode != 201 {
			t.Fatal(r.StatusCode, decodeBody(t, r))
		}
		return decodeBody(t, r)
	}
	post := create()
	assertXPFeedback(t, post, 0, 2)
	id := post["id"].(string)
	if post["author"] != owner.Id {
		t.Fatal("post DTO was wrapped", post)
	}
	read := decodeBody(t, communityRequest(t, app, "GET", "/community/post?id="+id, "", actorToken))
	assertNoXPFeedback(t, read)
	like := func(method, token, path string) map[string]any {
		r := communityRequest(t, app, method, path, "", token)
		if r.StatusCode != 200 {
			t.Fatal(r.StatusCode, decodeBody(t, r))
		}
		return decodeBody(t, r)
	}
	path := "/community/post/like?id=" + id
	assertNoXPFeedback(t, like("PUT", ownerToken, path)) // self
	assertXPFeedback(t, like("PUT", actorToken, path), 0, 1)
	assertNoXPFeedback(t, like("PUT", actorToken, path)) // no-op
	assertNoXPFeedback(t, like("DELETE", actorToken, path))
	assertNoXPFeedback(t, like("PUT", actorToken, path)) // durable dedupe
	comment := socialCreate(t, app, actorToken, id, "", "", "a comment")
	assertXPFeedback(t, comment, 0, 1)
	if comment["author"] != actor.Id {
		t.Fatal("comment DTO was wrapped")
	}
	assertNoXPFeedback(t, socialCreate(t, app, ownerToken, id, "", "", "self comment"))
	assertXPFeedback(t, like("PUT", ownerToken, "/community/comment/like?id="+comment["id"].(string)), 2, 3)
	for i := 0; i < 3; i++ {
		result := socialCreate(t, app, actorToken, id, "", "", "cap comment")
		assertXPFeedback(t, result, 1+i, 2+i)
	}
	assertNoXPFeedback(t, socialCreate(t, app, actorToken, id, "", "", "capped comment"))
	for i := 0; i < 4; i++ {
		result := create()
		assertXPFeedback(t, result, 3+i*2, 5+i*2)
	}
	assertNoXPFeedback(t, create())
	// Public reads and unauthorized mutations never return mutation feedback.
	assertNoXPFeedback(t, decodeBody(t, communityRequest(t, app, "GET", "/community/comment?id="+comment["id"].(string), "", actorToken)))
	r := communityRequest(t, app, "PUT", path, "", "")
	if r.StatusCode != 401 {
		t.Fatal("unauthorized like", r.StatusCode)
	}
	assertNoXPFeedback(t, decodeBody(t, r))
}

func assertXPFeedback(t *testing.T, body map[string]any, previous, current int) {
	t.Helper()
	feedback, ok := body["xp_feedback"].(map[string]any)
	if !ok || feedback["previous_exp"] != float64(previous) || feedback["new_exp"] != float64(current) || feedback["diff_exp"] != float64(current-previous) {
		t.Fatalf("feedback want %d -> %d, got %#v", previous, current, body["xp_feedback"])
	}
}
func assertNoXPFeedback(t *testing.T, body map[string]any) {
	t.Helper()
	if _, ok := body["xp_feedback"]; ok {
		t.Fatalf("unexpected XP feedback %#v", body)
	}
}

func TestCommunityFeedbackAndRecordsRollbackWhenNotificationFails(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	ownerToken, _ := createCommunityUser(t, app)
	actorToken, actor := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	r := achievementRequest(t, app, "POST", "", ownerToken, map[string]string{"flair": "achievement", "game_series": series.Id}, 1)
	id := decodeBody(t, r)["id"].(string)
	app.OnRecordCreate("user_notification").BindFunc(func(e *core.RecordEvent) error { return fmt.Errorf("forced notification failure") })
	r = communityRequest(t, app, "POST", "/community/comment", fmt.Sprintf(`{"post":%q,"body":"rollback"}`, id), actorToken)
	if r.StatusCode < 400 {
		t.Fatal("expected transaction failure", r.StatusCode, decodeBody(t, r))
	}
	assertNoXPFeedback(t, decodeBody(t, r))
	for _, collection := range []string{"community_comment", "user_notification"} {
		count, err := app.CountRecords(collection)
		if err != nil || count != 0 {
			t.Fatal("partial commit", collection, count, err)
		}
	}
	exp, err := xp.LoadCurrentExp(app, actor.Id)
	if err != nil || exp != 0 {
		t.Fatal("XP did not roll back", exp, err)
	}
	count, err := app.CountRecords("user_level_log", dbx.NewExp("user={:u}", dbx.Params{"u": actor.Id}))
	if err != nil || count != 0 {
		t.Fatal("XP ledger did not roll back", count, err)
	}
}

func TestCommunityFeedbackUsesTransactionalBalanceAndKSTCapBoundary(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	_, actor := createCommunityUser(t, app)
	before := time.Date(2026, 1, 1, 14, 59, 0, 0, time.UTC)
	award := func(event string, now time.Time) *xp.ExpFeedback {
		var feedback *xp.ExpFeedback
		err := app.RunInTransaction(func(tx core.App) error {
			var err error
			feedback, err = xp.AwardCommunityWithFeedbackTx(tx, actor.Id, "reaction", event, now)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		return feedback
	}
	for i := 0; i < 5; i++ {
		f := award(fmt.Sprint(i), before)
		if f == nil || f.PreviousExp != i || f.NewExp != i+1 {
			t.Fatalf("transaction balance %#v", f)
		}
		if _, err := app.DB().NewQuery("UPDATE user_level_log SET created='2026-01-01 14:59:00.000Z' WHERE user={:u}").Bind(dbx.Params{"u": actor.Id}).Execute(); err != nil {
			t.Fatal(err)
		}
	}
	if f := award("capped", before); f != nil {
		t.Fatalf("cap feedback %#v", f)
	}
	after := before.Add(time.Minute)
	if f := award("0", after); f != nil {
		t.Fatalf("durable duplicate %#v", f)
	}
	if f := award("next-day", after); f == nil || f.PreviousExp != 5 || f.NewExp != 6 {
		t.Fatalf("KST boundary %#v", f)
	}
}

func TestAchievementDeletionRevokesVisibleActivityXPOnce(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	ownerToken, owner := createCommunityUser(t, app)
	actorToken, actor := createCommunityUser(t, app)
	replierToken, replier := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	r := achievementRequest(t, app, "POST", "", ownerToken, map[string]string{"flair": "achievement", "game_series": series.Id}, 1)
	if r.StatusCode != 201 {
		t.Fatal(r.StatusCode, decodeBody(t, r))
	}
	postID := decodeBody(t, r)["id"].(string)
	comment := socialCreate(t, app, actorToken, postID, "", "", "comment")
	commentID := comment["id"].(string)
	socialCreate(t, app, replierToken, postID, commentID, commentID, "reply")
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+postID, "", actorToken, 200)
	expectSocialStatus(t, app, "PUT", "/community/comment/like?id="+commentID, "", replierToken, 200)
	assertXP := func(user string, want int) {
		t.Helper()
		got, err := xp.LoadCurrentExp(app, user)
		if err != nil || got != want {
			t.Fatalf("user %s XP: got %d want %d, error %v", user, got, want, err)
		}
	}
	assertXP(owner.Id, 2)
	assertXP(actor.Id, 2)
	assertXP(replier.Id, 2)
	// Parent comment deletion retains the visible reply, but revokes its own
	// grant and all likes whose target is no longer visible.
	for range 2 {
		expectSocialStatus(t, app, "DELETE", "/community/comment?id="+commentID, "", actorToken, 204)
	}
	assertXP(actor.Id, 1)
	assertXP(replier.Id, 1)
	for range 2 {
		expectSocialStatus(t, app, "DELETE", "/community/post?id="+postID, "", ownerToken, 204)
	}
	assertXP(owner.Id, 0)
	assertXP(actor.Id, 0)
	assertXP(replier.Id, 0)
	var totals struct {
		Total int `db:"total"`
	}
	if err := app.DB().NewQuery("SELECT SUM(diff_exp) AS total FROM user_level_log WHERE kind LIKE 'xp:community-revoke:%'").One(&totals); err != nil {
		t.Fatal(err)
	}
	if totals.Total != -6 {
		t.Fatalf("reversal ledger: %d", totals.Total)
	}
}

func TestDeletedAchievementDoesNotRefundDailyCap(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, user := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	for range 6 {
		r := achievementRequest(t, app, "POST", "", token, map[string]string{"flair": "achievement", "game_series": series.Id}, 1)
		if r.StatusCode != 201 {
			t.Fatal(r.StatusCode, decodeBody(t, r))
		}
		id := decodeBody(t, r)["id"].(string)
		expectSocialStatus(t, app, "DELETE", "/community/post?id="+id, "", token, 204)
	}
	got, err := xp.LoadCurrentExp(app, user.Id)
	if err != nil || got != 0 {
		t.Fatalf("XP %d, error %v", got, err)
	}
	count, err := app.CountRecords("user_level_log", dbx.HashExp{"user": user.Id})
	if err != nil || count != 10 {
		t.Fatalf("expected five grants and five reversals, got %d, error %v", count, err)
	}
}

func TestAchievementRevocationRollsBackWithDeletionFailure(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, user := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	r := achievementRequest(t, app, "POST", "", token, map[string]string{"flair": "achievement", "game_series": series.Id}, 1)
	id := decodeBody(t, r)["id"].(string)
	app.OnRecordCreate("user_level_log").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetInt("diff_exp") < 0 {
			return fmt.Errorf("forced reversal failure")
		}
		return e.Next()
	})
	expectSocialStatus(t, app, "DELETE", "/community/post?id="+id, "", token, 502)
	post, err := app.FindRecordById("community_post", id)
	if err != nil || post.GetString("status") != "active" {
		t.Fatal("post deletion did not roll back", err)
	}
	got, err := xp.LoadCurrentExp(app, user.Id)
	if err != nil || got != 2 {
		t.Fatal("XP did not roll back", got, err)
	}
}
