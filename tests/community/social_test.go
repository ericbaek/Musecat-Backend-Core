package community_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/pocketbase/pocketbase/apis"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	community "github.com/ericbaek/musecat-backend-core/handlers/community"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func socialCreate(t *testing.T, app *tests.TestApp, token, post, parent, target, body string) map[string]any {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"post": post, "parent": parent, "reply_to": target, "body": body})
	res := communityRequest(t, app, http.MethodPost, "/community/comment", string(data), token)
	result := decodeBody(t, res)
	if res.StatusCode != 201 {
		t.Fatalf("create comment: %d %#v", res.StatusCode, result)
	}
	return result
}
func socialPost(t *testing.T, app *tests.TestApp, token string) string {
	res := communityRequest(t, app, http.MethodPost, "/community/post", `{"body":"게시글"}`, token)
	data := decodeBody(t, res)
	if res.StatusCode != 201 {
		t.Fatalf("post: %d %#v", res.StatusCode, data)
	}
	return data["id"].(string)
}
func expectSocialStatus(t *testing.T, app *tests.TestApp, method, path, body, token string, status int) {
	t.Helper()
	res := communityRequest(t, app, method, path, body, token)
	defer res.Body.Close()
	if res.StatusCode != status {
		t.Fatalf("%s %s: got %d want %d", method, path, res.StatusCode, status)
	}
}
func TestSocialAuthorPolicyAndThread(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, author := createCommunityUser(t, app)
	other, _ := createCommunityUser(t, app)
	post := socialPost(t, app, token)
	for _, body := range []string{`{"post":"` + post + `","body":" "}`, `{"post":"` + post + `","body":"` + strings.Repeat("가", 2001) + `"}`} {
		expectSocialStatus(t, app, "POST", "/community/comment", body, token, 400)
	}
	expectSocialStatus(t, app, "POST", "/community/comment", `{"post":"`+post+`","body":"hello"}`, "", 401)
	root := socialCreate(t, app, token, post, "", "", "댓글")
	id := root["id"].(string)
	deadline := root["editable_until"]
	reply := socialCreate(t, app, other, post, id, id, "답글")
	replyID := reply["id"].(string)
	nested := socialCreate(t, app, token, post, id, replyID, "답글에 답하기")
	if nested["parent"] != id || nested["reply_to"] != replyID {
		t.Fatal(nested)
	}
	another := socialPost(t, app, token)
	expectSocialStatus(t, app, "POST", "/community/comment", fmt.Sprintf(`{"post":%q,"reply_to":%q,"body":"invalid"}`, another, id), token, 400)
	expectSocialStatus(t, app, "PUT", "/community/comment?id="+id, `{"body":"수정"}`, other, 403)
	res := communityRequest(t, app, "PUT", "/community/comment?id="+id, `{"body":"수정"}`, token)
	edited := decodeBody(t, res)
	if res.StatusCode != 200 || edited["editable_until"] != deadline || edited["edited_at"] == "" {
		t.Fatal(edited)
	}
	record, _ := app.FindRecordById(community.CollectionComment, id)
	record.Set("editable_until", time.Now().Add(-time.Second))
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	expectSocialStatus(t, app, "PUT", "/community/comment?id="+id, `{"body":"expired"}`, token, 409)
	expectSocialStatus(t, app, "DELETE", "/community/comment?id="+id, "", other, 403)
	expectSocialStatus(t, app, "DELETE", "/community/comment?id="+id, "", token, 204)
	expectSocialStatus(t, app, "DELETE", "/community/comment?id="+id, "", token, 204)
	res = communityRequest(t, app, "GET", "/community/comments?post="+post, "", "")
	roots := decodeBody(t, res)["items"].([]any)
	deleted := roots[0].(map[string]any)
	if len(roots) != 1 || deleted["deleted"] != true || deleted["body"] != "" || deleted["author"] != "" || deleted["reply_count"] != float64(2) {
		t.Fatal(roots)
	}
	expectSocialStatus(t, app, "POST", "/community/comment", fmt.Sprintf(`{"post":%q,"reply_to":%q,"body":"blocked"}`, post, id), token, 404)
	socialCreate(t, app, token, post, id, replyID, "active reply under deleted root")
	expectSocialStatus(t, app, "PUT", "/community/comment/like?id="+id, "", token, 404)
	author.Set("withdrawn", true)
	if err := app.Save(author); err != nil {
		t.Fatal(err)
	}
	expectSocialStatus(t, app, "POST", "/community/comment", fmt.Sprintf(`{"post":%q,"body":"blocked"}`, post), token, 403)
	expectSocialStatus(t, app, "PUT", "/community/post/like?id="+post, "", token, 403)
}
func TestSocialLikesAreIdempotentAndPersonalized(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	other, _ := createCommunityUser(t, app)
	post := socialPost(t, app, token)
	root := socialCreate(t, app, token, post, "", "", "댓글")
	id := root["id"].(string)
	for _, path := range []string{"/community/post/like?id=" + post, "/community/comment/like?id=" + id} {
		expectSocialStatus(t, app, "PUT", path, "", "", 401)
		for i := 0; i < 2; i++ {
			res := communityRequest(t, app, "PUT", path, "", token)
			data := decodeBody(t, res)
			if data["like_count"] != float64(1) || data["liked"] != true {
				t.Fatal(data)
			}
		}
		expectSocialStatus(t, app, "PUT", path, "", other, 200)
		for i := 0; i < 2; i++ {
			res := communityRequest(t, app, "DELETE", path, "", token)
			data := decodeBody(t, res)
			if data["like_count"] != float64(1) || data["liked"] != false {
				t.Fatal(data)
			}
		}
	}
	for _, input := range []struct {
		token string
		liked bool
	}{{"", false}, {token, false}, {other, true}} {
		res := communityRequest(t, app, "GET", "/community/posts", "", input.token)
		data := decodeBody(t, res)["items"].([]any)[0].(map[string]any)
		if data["like_count"] != float64(1) || data["liked"] != input.liked || data["comment_count"] != float64(1) {
			t.Fatal(data)
		}
		res = communityRequest(t, app, "GET", "/community/comments?post="+post, "", input.token)
		data = decodeBody(t, res)["items"].([]any)[0].(map[string]any)
		if data["liked"] != input.liked {
			t.Fatal(data)
		}
	}
	// Simultaneous retry requests still leave one row per user and target.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			expectSocialStatus(t, app, "PUT", "/community/post/like?id="+post, "", token, 200)
		}()
	}
	wg.Wait()
	res := communityRequest(t, app, "GET", "/community/post?id="+post, "", token)
	if decodeBody(t, res)["like_count"] != float64(2) {
		t.Fatal("concurrent like count")
	}
	expectSocialStatus(t, app, "DELETE", "/community/post?id="+post, "", token, 204)
	for _, path := range []string{"/community/comments?post=" + post, "/community/comment?id=" + id} {
		expectSocialStatus(t, app, "GET", path, "", "", 404)
	}
	expectSocialStatus(t, app, "PUT", "/community/comment/like?id="+id, "", other, 404)
}
func TestSocialCursorAndDeletedRootDisappearance(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	post := socialPost(t, app, token)
	for i := 0; i < 23; i++ {
		socialCreate(t, app, token, post, "", "", fmt.Sprint(i))
	}
	res := communityRequest(t, app, "GET", "/community/comments?post="+post, "", "")
	first := decodeBody(t, res)
	if len(first["items"].([]any)) != 20 || first["next_cursor"] == "" {
		t.Fatal(first)
	}
	socialCreate(t, app, token, post, "", "", "new insertion")
	res = communityRequest(t, app, "GET", "/community/comments?post="+post+"&cursor="+url.QueryEscape(first["next_cursor"].(string)), "", "")
	second := decodeBody(t, res)
	if len(second["items"].([]any)) != 3 || second["next_cursor"] != "" {
		t.Fatal(second)
	}
	seen := map[string]bool{}
	for _, item := range first["items"].([]any) {
		seen[item.(map[string]any)["id"].(string)] = true
	}
	for _, item := range second["items"].([]any) {
		if seen[item.(map[string]any)["id"].(string)] {
			t.Fatal("duplicate cursor result")
		}
	}
	expectSocialStatus(t, app, "GET", "/community/comments?post="+post+"&cursor=bad", "", "", 400)
	root := socialCreate(t, app, token, post, "", "", "deleted root")
	id := root["id"].(string)
	reply := socialCreate(t, app, token, post, id, id, "reply")
	expectSocialStatus(t, app, "DELETE", "/community/comment?id="+id, "", token, 204)
	expectSocialStatus(t, app, "DELETE", "/community/comment?id="+reply["id"].(string), "", token, 204)
	res = communityRequest(t, app, "GET", "/community/comments?post="+post, "", "")
	for _, item := range decodeBody(t, res)["items"].([]any) {
		if item.(map[string]any)["id"] == id {
			t.Fatal("empty deleted root remains visible")
		}
	}
}
func TestSocialCommentTranslation(t *testing.T) {
	t.Setenv("MUSECAT_COMMUNITY_TRANSLATIONS_PUBLIC", "true")
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	post := socialPost(t, app, token)
	comment := socialCreate(t, app, token, post, "", "", "응원합니다")
	id := comment["id"].(string)
	translator := &fakeTranslator{result: community.TranslationResult{"en-US": {Body: "Cheering you on!"}, "ja-JP": {Body: "応援します"}}}
	_, err := community.RunDueTranslations(context.Background(), app, translator, time.Now().Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	res := communityRequest(t, app, "GET", "/community/comment?id="+id+"&locale=en-US", "", "")
	data := decodeBody(t, res)
	if data["translated"] != true || data["body"] != "Cheering you on!" {
		t.Fatal(data)
	}
	expectSocialStatus(t, app, "PUT", "/community/comment?id="+id, `{"body":"업데이트"}`, token, 200)
	res = communityRequest(t, app, "GET", "/community/comment?id="+id+"&locale=en-US", "", "")
	data = decodeBody(t, res)
	if data["translated"] != false || data["body"] != "업데이트" {
		t.Fatal(data)
	}
	_, err = community.RunDueTranslations(context.Background(), app, translatorFunc(func(_ context.Context, source community.SourceContent) (community.TranslationResult, error) {
		if source.Body == "업데이트" {
			expectSocialStatus(t, app, "DELETE", "/community/comment?id="+id, "", token, 204)
		}
		return translator.result, nil
	}), time.Now().Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	record, _ := app.FindRecordById(community.CollectionComment, id)
	if record.GetString("status") != "deleted" || record.GetString("body") != "" {
		t.Fatal("translation resurrected deleted comment")
	}
}

func TestSocialBannedUserCannotParticipate(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, user := createCommunityUser(t, app)
	post := socialPost(t, app, token)
	comment := socialCreate(t, app, token, post, "", "", "before ban")
	col, _ := app.FindCollectionByNameOrId("user_ban")
	ban := core.NewRecord(col)
	ban.Id = user.Id
	ban.Set("reason", "local policy test")
	if err := app.Save(ban); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"PUT", "DELETE"} {
		expectSocialStatus(t, app, method, "/community/post/like?id="+post, "", token, 403)
		expectSocialStatus(t, app, method, "/community/comment/like?id="+comment["id"].(string), "", token, 403)
	}
	expectSocialStatus(t, app, "POST", "/community/comment", fmt.Sprintf(`{"post":%q,"body":"blocked"}`, post), token, 403)
	expectSocialStatus(t, app, "PUT", "/community/comment?id="+comment["id"].(string), `{"body":"blocked"}`, token, 403)
	expectSocialStatus(t, app, "DELETE", "/community/comment?id="+comment["id"].(string), "", token, 403)
}
func TestSocialCommentTranslationRetryAndDeletedPostRace(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	post := socialPost(t, app, token)
	comment := socialCreate(t, app, token, post, "", "", "retry comment")
	id := comment["id"].(string)
	now := time.Now().UTC().Add(6 * time.Minute).Truncate(time.Millisecond)
	_, err := community.RunDueTranslations(context.Background(), app, &fakeTranslator{err: fmt.Errorf("provider unavailable")}, now)
	if err != nil {
		t.Fatal(err)
	}
	record, _ := app.FindRecordById(community.CollectionComment, id)
	if record.GetString("translation_status") != "failed" || record.GetInt("translation_attempts") != 1 {
		t.Fatal("retry state missing")
	}
	record.Set("translation_status", "processing")
	record.Set("translation_started_at", now.Add(-11*time.Minute))
	record.Set("translate_after", now.Add(-time.Minute))
	if err = app.Save(record); err != nil {
		t.Fatal(err)
	}
	_, err = community.RunDueTranslations(context.Background(), app, translatorFunc(func(_ context.Context, source community.SourceContent) (community.TranslationResult, error) {
		if source.Body == "retry comment" {
			expectSocialStatus(t, app, "DELETE", "/community/post?id="+post, "", token, 204)
		}
		return community.TranslationResult{"en-US": {Body: "Translated"}, "ja-JP": {Body: "翻訳"}}, nil
	}), now)
	if err != nil {
		t.Fatal(err)
	}
	record, _ = app.FindRecordById(community.CollectionComment, id)
	if record.GetString("translation_status") == "ready" {
		t.Fatal("translation published after parent deletion")
	}
	expectSocialStatus(t, app, "GET", "/community/comment?id="+id, "", "", 404)
}

func commentPhotoRequest(t *testing.T, app *tests.TestApp, method, id, token, post, parent, body string, files [][]byte) *http.Response {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	_ = writer.WriteField("post", post)
	_ = writer.WriteField("parent", parent)
	_ = writer.WriteField("body", body)
	for i, data := range files {
		part, err := writer.CreateFormFile("photos", fmt.Sprintf("play%d.png", i))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	_ = writer.Close()
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	event := &core.ServeEvent{App: app, Router: router}
	if err = app.OnServe().Trigger(event, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := event.Router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	path := "/community/comment"
	if id != "" {
		path += "?id=" + id
	}
	req := httptest.NewRequest(method, path, &payload)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	return recorder.Result()
}
func TestSocialCommentPhotos(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	post := socialPost(t, app, token)
	root := socialCreate(t, app, token, post, "", "", "이미지 답글")
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	photo := imageBytes.Bytes()
	response := commentPhotoRequest(t, app, "POST", "", token, post, root["id"].(string), "", [][]byte{photo, photo, photo})
	data := decodeBody(t, response)
	if response.StatusCode != 201 || len(data["photos"].([]any)) != 3 || data["translation_status"] != "ready" {
		t.Fatalf("photo-only reply: %d %#v", response.StatusCode, data)
	}
	id := data["id"].(string)
	response = communityRequest(t, app, "PUT", "/community/comment?id="+id, `{"body":"본문 추가"}`, token)
	data = decodeBody(t, response)
	if response.StatusCode != 200 || len(data["photos"].([]any)) != 3 {
		t.Fatal("text edit discarded photos", data)
	}
	expectSocialStatus(t, app, "PUT", "/community/comment?id="+id, `{"body":"","keep_photos":[]}`, token, 400)
	expectSocialStatus(t, app, "PUT", "/community/comment?id="+id, `{"body":"text","keep_photos":["another-user.png"]}`, token, 400)
	response = commentPhotoRequest(t, app, "PUT", id, token, post, "", "fourth", [][]byte{photo})
	response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("fourth photo accepted")
	}
	for _, files := range [][][]byte{{photo, photo, photo, photo}, {[]byte("fake image")}, {bytes.Repeat([]byte("x"), community.MaxPostPhotoBytes+1)}} {
		response = commentPhotoRequest(t, app, "POST", "", token, post, "", "bad", files)
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("invalid comment image accepted: %d", response.StatusCode)
		}
	}
	expectSocialStatus(t, app, "PUT", "/community/comment?id="+id, `{"body":"사진 삭제","keep_photos":[]}`, token, 200)
	record, _ := app.FindRecordById(community.CollectionComment, id)
	if len(record.GetStringSlice("photos")) != 0 {
		t.Fatal("removed comment photos retained")
	}
	response = commentPhotoRequest(t, app, "PUT", id, token, post, "", "다시 첨부", [][]byte{photo})
	data = decodeBody(t, response)
	if response.StatusCode != 200 || len(data["photos"].([]any)) != 1 {
		t.Fatal(data)
	}
	expectSocialStatus(t, app, "DELETE", "/community/comment?id="+id, "", token, 204)
	record, _ = app.FindRecordById(community.CollectionComment, id)
	if len(record.GetStringSlice("photos")) != 0 {
		t.Fatal("deleted comment retains public media")
	}
}

func TestSocialPostDeletionDropsCommentMedia(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	post := socialPost(t, app, token)
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	response := commentPhotoRequest(t, app, "POST", "", token, post, "", "photo", [][]byte{img.Bytes()})
	data := decodeBody(t, response)
	if response.StatusCode != 201 {
		t.Fatal(data)
	}
	id := data["id"].(string)
	filename := data["photos"].([]any)[0].(string)
	expectSocialStatus(t, app, "GET", "/api/files/community_comment/"+id+"/"+filename, "", "", 200)
	expectSocialStatus(t, app, "DELETE", "/community/post?id="+post, "", token, 204)
	record, _ := app.FindRecordById(community.CollectionComment, id)
	if len(record.GetStringSlice("photos")) != 0 || record.GetString("status") != "deleted" || record.GetString("body") != "" {
		t.Fatal("deleted post retains public comment data")
	}
	expectSocialStatus(t, app, "GET", "/api/files/community_comment/"+id+"/"+filename, "", "", 404)
}
