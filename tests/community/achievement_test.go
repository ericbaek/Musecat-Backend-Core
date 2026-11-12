package community_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	community "github.com/ericbaek/musecat-backend-core/handlers/community"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func achievementSeries(t *testing.T, app *tests.TestApp) *core.Record {
	t.Helper()
	if series, err := app.FindFirstRecordByFilter("game_series", ""); err == nil {
		return series
	}
	collection, err := app.FindCollectionByNameOrId("game_series")
	if err != nil {
		t.Fatal(err)
	}
	series := core.NewRecord(collection)
	series.Set("en", "Achievement test")
	if err := app.Save(series); err != nil {
		t.Fatal(err)
	}
	return series
}

func achievementRequest(t *testing.T, app *tests.TestApp, method, id, token string, fields map[string]string, count int) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < count; i++ {
		file, err := writer.CreateFormFile("photos", fmt.Sprintf("achievement%d.png", i))
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 3+i, 3))); err != nil {
			t.Fatal(err)
		}
	}
	writer.Close()
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	se := &core.ServeEvent{App: app, Router: router}
	if err := app.OnServe().Trigger(se, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := se.Router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, "/community/post?id="+id, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	return recorder.Result()
}

func TestAchievementPhotosOptionalBodyAndEdit(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	other, _ := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	fields := map[string]string{"flair": "achievement", "game_series": series.Id}
	res := achievementRequest(t, app, "POST", "", token, fields, 2)
	data := decodeBody(t, res)
	if res.StatusCode != 201 || data["body"] != "" || data["translation_status"] != "ready" {
		t.Fatalf("photo-only create: %d %#v", res.StatusCode, data)
	}
	id := data["id"].(string)
	deadline := data["editable_until"]
	photos := data["photos"].([]any)
	fields["photo_order"] = fmt.Sprintf(`[{"name":%q},{"upload":0}]`, photos[1])
	res = achievementRequest(t, app, "PUT", id, token, fields, 1)
	data = decodeBody(t, res)
	if res.StatusCode != 200 || data["photos"].([]any)[0] != photos[1] || len(data["photos"].([]any)) != 2 || data["editable_until"] != deadline {
		t.Fatalf("mixed order: %d %#v", res.StatusCode, data)
	}
	retained := data["photos"]
	for _, order := range []string{`[]`, `[{"name":"foreign.png"}]`, fmt.Sprintf(`[{"name":%q},{"name":%q}]`, photos[1], photos[1]), `[{"upload":2}]`, `[{"name":"a","upload":0}]`} {
		fields["photo_order"] = order
		res = achievementRequest(t, app, "PUT", id, token, fields, 0)
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("invalid manifest accepted: %s %d", order, res.StatusCode)
		}
	}
	saved, _ := app.FindRecordById(community.CollectionPost, id)
	b, _ := json.Marshal(retained)
	a, _ := json.Marshal(saved.GetStringSlice("photos"))
	if !bytes.Equal(a, b) {
		t.Fatal("failed update changed photos")
	}
	fields["photo_order"] = fmt.Sprintf(`[{"name":%q}]`, photos[1])
	res = achievementRequest(t, app, "PUT", id, other, fields, 0)
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatal("foreign edit accepted")
	}
	fields["body"] = "new caption"
	res = achievementRequest(t, app, "PUT", id, token, fields, 0)
	data = decodeBody(t, res)
	if res.StatusCode != 200 || data["translation_status"] != "pending" {
		t.Fatal("caption did not queue translation", data)
	}
	saved, _ = app.FindRecordById(community.CollectionPost, id)
	saved.Set("editable_until", time.Now().Add(-time.Minute))
	if err := app.Save(saved); err != nil {
		t.Fatal(err)
	}
	res = achievementRequest(t, app, "PUT", id, token, fields, 0)
	res.Body.Close()
	if res.StatusCode != 409 {
		t.Fatal("expired edit accepted")
	}
	res = communityRequest(t, app, "DELETE", "/community/post?id="+id, "", token)
	res.Body.Close()
	if res.StatusCode != 204 {
		t.Fatal("expired delete blocked")
	}
	delete(fields, "photo_order")
	delete(fields, "body")
	for _, f := range []map[string]string{{"flair": "achievement"}, {"flair": "achievement", "game_series": "missing"}, {"flair": "chitchat", "game_series": series.Id}} {
		res = achievementRequest(t, app, "POST", "", token, f, 1)
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatalf("invalid achievement accepted: %#v", f)
		}
	}
	worker := &fakeTranslator{}
	if _, err := community.RunDueTranslations(context.Background(), app, worker, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if worker.calls != 0 {
		t.Fatal("empty/deleted post called translator")
	}
}

func TestAchievementPopularBeforePagination(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	other, _ := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	fields := map[string]string{"flair": "achievement", "game_series": series.Id}
	ids := []string{}
	for i := 0; i < 3; i++ {
		res := achievementRequest(t, app, "POST", "", token, fields, 1)
		data := decodeBody(t, res)
		if res.StatusCode != 201 {
			t.Fatal(data)
		}
		ids = append(ids, data["id"].(string))
	}
	res := communityRequest(t, app, "PUT", "/community/post/like?id="+ids[0], "", other)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal("like failed")
	}
	for i := 0; i < 2; i++ {
		res = communityRequest(t, app, "GET", fmt.Sprintf("/community/posts?flair=achievement&has_images=true&sort=popular&per_page=1&page=%d", i+1), "", "")
		data := decodeBody(t, res)
		if res.StatusCode != 200 || data["total"] != float64(3) {
			t.Fatal(data)
		}
		if i == 0 && data["items"].([]any)[0].(map[string]any)["id"] != ids[0] {
			t.Fatal("popular sort after pagination", data)
		}
	}
	res = communityRequest(t, app, "GET", "/community/posts?sort=invalid", "", "")
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal("invalid sort accepted")
	}
}

func TestAchievementGameFacetsIgnorePaginationAndGameFilter(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, author := createCommunityUser(t, app)
	other, _ := createCommunityUser(t, app)
	first := achievementSeries(t, app)
	collection, _ := app.FindCollectionByNameOrId("game_series")
	second := core.NewRecord(collection)
	second.Set("en", "Second game")
	if err := app.Save(second); err != nil {
		t.Fatal(err)
	}
	for _, game := range []string{first.Id, second.Id} {
		res := achievementRequest(t, app, "POST", "", token, map[string]string{"game_series": game, "flair": "achievement"}, 1)
		data := decodeBody(t, res)
		if res.StatusCode != 201 {
			t.Fatal(data)
		}
	}
	res := achievementRequest(t, app, "POST", "", other, map[string]string{"game_series": first.Id, "flair": "achievement"}, 1)
	res.Body.Close()
	res = communityRequest(t, app, "GET", "/community/posts?author="+author.Id+"&flair=achievement&has_images=true&per_page=1&game_series="+first.Id, "", "")
	data := decodeBody(t, res)
	if res.StatusCode != 200 {
		t.Fatal(data)
	}
	games := data["game_series_ids"].([]any)
	if len(games) != 2 || data["total"] != float64(1) || len(data["items"].([]any)) != 1 {
		t.Fatal("facets were paginated or game-filtered", data)
	}
}
