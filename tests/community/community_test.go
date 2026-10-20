package community_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	community "github.com/ericbaek/musecat-backend-core/handlers/community"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testutil.CleanupGoldenDir()
	os.Exit(code)
}

type translatorFunc func(context.Context, community.SourceContent) (community.TranslationResult, error)

func (f translatorFunc) Translate(ctx context.Context, source community.SourceContent) (community.TranslationResult, error) {
	return f(ctx, source)
}

type fakeTranslator struct {
	result community.TranslationResult
	err    error
	calls  int
}

type staleLeaseTranslator struct {
	app          *tests.TestApp
	postID       string
	nestedResult community.TranslationResult
	firstResult  community.TranslationResult
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (f *fakeTranslator) Translate(_ context.Context, _ community.SourceContent) (community.TranslationResult, error) {
	f.calls++
	return f.result, f.err
}

func (f *staleLeaseTranslator) Translate(ctx context.Context, _ community.SourceContent) (community.TranslationResult, error) {
	record, err := f.app.FindRecordById(community.CollectionPost, f.postID)
	if err != nil {
		return nil, err
	}
	record.Set("translation_started_at", time.Now().UTC().Add(-11*time.Minute))
	if err := f.app.Save(record); err != nil {
		return nil, err
	}
	if _, err := community.RunDueTranslations(ctx, f.app, &fakeTranslator{result: f.nestedResult}, time.Now().UTC()); err != nil {
		return nil, err
	}
	return f.firstResult, nil
}

func newCommunityTestApp(tb testing.TB) *tests.TestApp {
	tb.Helper()
	app := testutil.NewTestApp(tb)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		community.RegisterRoutes(se)
		return se.Next()
	})
	return app
}

func createCommunityUser(tb testing.TB, app *tests.TestApp) (string, *core.Record) {
	tb.Helper()
	collection, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		tb.Fatalf("find user collection: %v", err)
	}
	record := core.NewRecord(collection)
	unique := time.Now().UnixNano()
	record.SetEmail(fmt.Sprintf("community_%d@example.com", unique))
	record.Set("username", fmt.Sprintf("community%d", unique))
	record.SetPassword("secret123")
	if err := app.Save(record); err != nil {
		tb.Fatalf("create user: %v", err)
	}
	token, err := record.NewAuthToken()
	if err != nil {
		tb.Fatalf("create auth token: %v", err)
	}
	return token, record
}

func communityRequest(tb testing.TB, app *tests.TestApp, method, target, body, token string) *http.Response {
	tb.Helper()
	router, err := apis.NewRouter(app)
	if err != nil {
		tb.Fatalf("create router: %v", err)
	}
	serveEvent := &core.ServeEvent{App: app, Router: router}
	if err := app.OnServe().Trigger(serveEvent, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		tb.Fatalf("register routes: %v", err)
	}
	mux, err := serveEvent.Router.BuildMux()
	if err != nil {
		tb.Fatalf("build router: %v", err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	mux.ServeHTTP(recorder, request)
	return recorder.Result()
}

func decodeBody(tb testing.TB, response *http.Response) map[string]any {
	tb.Helper()
	defer response.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		tb.Fatalf("decode response: %v", err)
	}
	return payload
}

func createCommunityPost(tb testing.TB, app *tests.TestApp, token string) (string, map[string]any) {
	tb.Helper()
	response := communityRequest(tb, app, http.MethodPost, "/community/post", `{"title":"기체 상태","body":"CHUNITHM SUN 기체가 2층에 있고 1크레딧은 1000원입니다."}`, token)
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		tb.Fatalf("create post status %d: %s", response.StatusCode, body)
	}
	payload := decodeBody(tb, response)
	id, _ := payload["id"].(string)
	if id == "" {
		tb.Fatalf("missing post id: %#v", payload)
	}
	return id, payload
}

func TestCommunityPost_OriginalIsImmediateAndTranslationPublishesLater(t *testing.T) {
	t.Setenv("MUSECAT_COMMUNITY_TRANSLATIONS_PUBLIC", "false")
	app := newCommunityTestApp(t)
	token, _ := createCommunityUser(t, app)
	id, created := createCommunityPost(t, app, token)

	if created["body"] != "CHUNITHM SUN 기체가 2층에 있고 1크레딧은 1000원입니다." {
		t.Fatalf("expected immediate Korean body, got %#v", created["body"])
	}
	if created["translation_status"] != "pending" || created["translated"] != false {
		t.Fatalf("unexpected initial translation state: %#v", created)
	}

	before := communityRequest(t, app, http.MethodGet, "/community/post?id="+id+"&locale=ja-JP", "", "")
	beforePayload := decodeBody(t, before)
	if beforePayload["locale"] != "ko-KR" || beforePayload["translated"] != false {
		t.Fatalf("translations must remain hidden in phase one: %#v", beforePayload)
	}

	record, err := app.FindRecordById(community.CollectionPost, id)
	if err != nil {
		t.Fatalf("find post: %v", err)
	}
	now := time.Now().UTC()
	record.Set("translate_after", now.Add(-time.Second))
	if err := app.Save(record); err != nil {
		t.Fatalf("make post translation due: %v", err)
	}
	translator := &fakeTranslator{result: community.TranslationResult{
		"en-US": {Title: "Cabinet condition", Body: "The CHUNITHM SUN cabinet is on the second floor and one credit costs 1,000 won."},
		"ja-JP": {Title: "筐体の状態", Body: "CHUNITHM SUNの筐体は2階にあり、1クレジットは1,000ウォンです。"},
	}}
	processed, err := community.RunDueTranslations(context.Background(), app, translator, now)
	if err != nil || processed != 1 || translator.calls != 1 {
		t.Fatalf("translation worker got processed=%d calls=%d err=%v", processed, translator.calls, err)
	}
	record, _ = app.FindRecordById(community.CollectionPost, id)
	if record.GetString("translation_status") != "ready" {
		t.Fatalf("expected ready translation, got %q", record.GetString("translation_status"))
	}

	t.Setenv("MUSECAT_COMMUNITY_TRANSLATIONS_PUBLIC", "true")
	after := communityRequest(t, app, http.MethodGet, "/community/post?id="+id+"&locale=ja-JP", "", "")
	afterPayload := decodeBody(t, after)
	if afterPayload["locale"] != "ja-JP" || afterPayload["translated"] != true {
		t.Fatalf("expected Japanese translation after feature enablement: %#v", afterPayload)
	}
	if afterPayload["title"] != "筐体の状態" {
		t.Fatalf("unexpected Japanese title: %#v", afterPayload["title"])
	}
}

func TestCommunityPost_EditWindowAndDueTime(t *testing.T) {
	app := newCommunityTestApp(t)
	token, _ := createCommunityUser(t, app)
	id, _ := createCommunityPost(t, app, token)

	translator := &fakeTranslator{result: community.TranslationResult{
		"en-US": {Title: "Edited", Body: "Edited body"},
		"ja-JP": {Title: "修正", Body: "修正本文"},
	}}
	processed, err := community.RunDueTranslations(context.Background(), app, translator, time.Now().UTC())
	if err != nil || processed != 0 || translator.calls != 0 {
		t.Fatalf("post must not translate before five minutes: processed=%d calls=%d err=%v", processed, translator.calls, err)
	}

	update := communityRequest(t, app, http.MethodPut, "/community/post?id="+id, `{"title":"수정됨","body":"수정된 본문"}`, token)
	if update.StatusCode != http.StatusOK {
		t.Fatalf("expected edit within five minutes, got %d", update.StatusCode)
	}
	update.Body.Close()

	record, _ := app.FindRecordById(community.CollectionPost, id)
	record.Set("editable_until", time.Now().UTC().Add(-time.Second))
	if err := app.Save(record); err != nil {
		t.Fatalf("close edit window: %v", err)
	}
	lateUpdate := communityRequest(t, app, http.MethodPut, "/community/post?id="+id, `{"title":"늦은 수정","body":"늦은 본문"}`, token)
	if lateUpdate.StatusCode != http.StatusConflict {
		t.Fatalf("expected closed edit window conflict, got %d", lateUpdate.StatusCode)
	}
	lateUpdate.Body.Close()
}

func TestCommunityTranslation_RetriesWithoutFailingOpen(t *testing.T) {
	app := newCommunityTestApp(t)
	token, _ := createCommunityUser(t, app)
	id, _ := createCommunityPost(t, app, token)
	record, _ := app.FindRecordById(community.CollectionPost, id)
	now := time.Now().UTC()
	record.Set("translate_after", now.Add(-time.Second))
	if err := app.Save(record); err != nil {
		t.Fatalf("make post due: %v", err)
	}

	translator := &fakeTranslator{err: fmt.Errorf("temporary provider failure")}
	processed, err := community.RunDueTranslations(context.Background(), app, translator, now)
	if err != nil || processed != 1 {
		t.Fatalf("recording provider failure should succeed: processed=%d err=%v", processed, err)
	}
	record, _ = app.FindRecordById(community.CollectionPost, id)
	if record.GetString("translation_status") != "failed" || record.GetInt("translation_attempts") != 1 {
		t.Fatalf("unexpected retry state: status=%q attempts=%d", record.GetString("translation_status"), record.GetInt("translation_attempts"))
	}
	if record.GetString("translation_error") == "" || !record.GetDateTime("translate_after").Time().UTC().After(now) {
		t.Fatalf("expected diagnostic and future retry time")
	}
	if translations := record.GetRaw("translations"); translations == nil {
		t.Fatalf("translation failure must not remove the original post")
	}
}

func TestCommunityTranslation_StaleLeaseCannotOverwriteNewClaim(t *testing.T) {
	app := newCommunityTestApp(t)
	token, _ := createCommunityUser(t, app)
	id, _ := createCommunityPost(t, app, token)
	record, _ := app.FindRecordById(community.CollectionPost, id)
	now := time.Now().UTC()
	record.Set("translate_after", now.Add(-time.Second))
	if err := app.Save(record); err != nil {
		t.Fatalf("make post due: %v", err)
	}

	nestedResult := community.TranslationResult{
		"en-US": {Title: "New claim", Body: "New claim translation"},
		"ja-JP": {Title: "新しい claim", Body: "新しい翻訳"},
	}
	firstResult := community.TranslationResult{
		"en-US": {Title: "Stale claim", Body: "Stale claim translation"},
		"ja-JP": {Title: "古い claim", Body: "古い翻訳"},
	}
	processed, err := community.RunDueTranslations(context.Background(), app, &staleLeaseTranslator{
		app: app, postID: id, nestedResult: nestedResult, firstResult: firstResult,
	}, now)
	if err != nil || processed != 1 {
		t.Fatalf("stale lease run got processed=%d err=%v", processed, err)
	}

	record, err = app.FindRecordById(community.CollectionPost, id)
	if err != nil {
		t.Fatalf("reload post: %v", err)
	}
	translations := decodeTranslationsForTest(t, record)
	if translations["en-US"].Title != "New claim" || record.GetInt("translation_attempts") != 2 {
		t.Fatalf("stale claim overwrote newer translation: translations=%#v attempts=%d", translations, record.GetInt("translation_attempts"))
	}
}

func decodeTranslationsForTest(t *testing.T, record *core.Record) community.TranslationResult {
	t.Helper()
	encoded, err := json.Marshal(record.Get("translations"))
	if err != nil {
		t.Fatalf("encode translations: %v", err)
	}
	var result community.TranslationResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		t.Fatalf("decode translations: %v", err)
	}
	return result
}

func TestOpenAITranslator_RequestAndResponseContract(t *testing.T) {
	for _, effort := range []string{"none", "low"} {
		t.Run(effort, func(t *testing.T) {
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != "https://openai.test/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer test-key" {
					t.Fatal("incorrect OpenAI endpoint or authentication")
				}
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != "gpt-6-luna" || body["reasoning_effort"] != effort || body["max_completion_tokens"] != float64(16384) || body["store"] != false {
					t.Fatalf("incorrect model settings: %#v", body)
				}
				if _, ok := body["thinking"]; ok {
					t.Fatal("legacy provider payload retained")
				}
				format := body["response_format"].(map[string]any)
				if format["type"] != "json_schema" || format["json_schema"].(map[string]any)["strict"] != true {
					t.Fatal("strict JSON schema required")
				}
				messages, _ := json.Marshal(body["messages"])
				if !strings.Contains(string(messages), "maimai DX") || !strings.Contains(string(messages), "기체=machine cabinet") {
					t.Fatal("glossary missing")
				}
				translation := `{"en-US":{"title":"Cabinet condition","body":"CHUNITHM SUN costs 1000원."},"ja-JP":{"title":"筐体の状態","body":"CHUNITHM SUNは1000원です。"}}`
				encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"content": translation}}}})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(encoded)), Header: http.Header{}}, nil
			})}
			translator, err := community.NewConfiguredTranslator(community.TranslationConfig{APIKey: "test-key", BaseURL: "https://openai.test/v1", Model: "gpt-6-luna", ReasoningEffort: effort, Glossary: "maimai DX"}, client)
			if err != nil {
				t.Fatal(err)
			}
			result, err := translator.Translate(context.Background(), community.SourceContent{Title: "기체 상태", Body: "CHUNITHM SUN 1000원"})
			if err != nil || result["ja-JP"].Body == "" {
				t.Fatalf("translation failed: %v", err)
			}
		})
	}
}

func TestOpenAITranslator_RejectsIncompleteAndFailedResponses(t *testing.T) {
	for _, body := range []string{
		`{"choices":[]}`, `{"choices":[{"finish_reason":"length","message":{"content":"{}"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"refusal":"refused","content":"{}"}}]}`,
		`{"choices":[{"finish_reason":"stop","message":{"content":"{\"en-US\":{\"title\":\"\",\"body\":\"test\"}}"}}]}`,
	} {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		translator, _ := community.NewConfiguredTranslator(community.TranslationConfig{APIKey: "test"}, client)
		if _, err := translator.Translate(context.Background(), community.SourceContent{Body: "본문"}); err == nil {
			t.Fatalf("accepted incomplete response: %s", body)
		}
	}
}

func TestCommunityPost_AuthorMutationsAndDeletion(t *testing.T) {
	app := newCommunityTestApp(t)
	token, author := createCommunityUser(t, app)
	other, _ := createCommunityUser(t, app)
	id, _ := createCommunityPost(t, app, token)
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		for _, tc := range []struct {
			token  string
			status int
		}{{"", 401}, {other, 403}} {
			res := communityRequest(t, app, method, "/community/post?id="+id, `{"body":"changed"}`, tc.token)
			if res.StatusCode != tc.status {
				t.Fatalf("%s unauthorized mutation: got %d want %d", method, res.StatusCode, tc.status)
			}
			res.Body.Close()
		}
	}
	record, _ := app.FindRecordById(community.CollectionPost, id)
	deadline := record.GetString("editable_until")
	res := communityRequest(t, app, http.MethodPut, "/community/post?id="+id, `{"title":"수정","body":"수정된 본문","flair":"achievement"}`, token)
	if res.StatusCode != 200 {
		t.Fatalf("update: %d", res.StatusCode)
	}
	res.Body.Close()
	record, _ = app.FindRecordById(community.CollectionPost, id)
	if record.GetString("editable_until") != deadline || record.GetString("body") != "수정된 본문" {
		t.Fatal("update must preserve fixed edit deadline")
	}
	record.Set("editable_until", time.Now().Add(-time.Hour))
	record.Set("translate_after", time.Now().Add(-time.Hour))
	record.Set("translations", map[string]any{"en-US": map[string]string{"body": "cached"}})
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		res = communityRequest(t, app, http.MethodDelete, "/community/post?id="+id, "", token)
		if res.StatusCode != 204 {
			t.Fatalf("author deletion must be idempotent after edit deadline: %d", res.StatusCode)
		}
		res.Body.Close()
	}
	res = communityRequest(t, app, http.MethodGet, "/community/post?id="+id, "", "")
	if res.StatusCode != 404 {
		t.Fatalf("deleted post visible: %d", res.StatusCode)
	}
	res.Body.Close()
	res = communityRequest(t, app, http.MethodGet, "/community/posts?author="+author.Id+"&flair=achievement", "", "")
	payload := decodeBody(t, res)
	if payload["total"] != float64(0) {
		t.Fatal("deleted post remains in gallery/count")
	}
	res = communityRequest(t, app, http.MethodPut, "/community/post?id="+id, `{"body":"resurrect"}`, token)
	if res.StatusCode != 404 {
		t.Fatalf("deleted post can be edited: %d", res.StatusCode)
	}
	res.Body.Close()
	record, _ = app.FindRecordById(community.CollectionPost, id)
	if len(record.GetStringSlice("photos")) != 0 || len(record.GetString("translations")) > 2 {
		t.Fatal("deleted public media/cache retained")
	}
	translator := &fakeTranslator{}
	count, err := community.RunDueTranslations(context.Background(), app, translator, time.Now())
	if err != nil || count != 0 || translator.calls != 0 {
		t.Fatal("deleted post must not translate")
	}
	for _, target := range []string{"/community/post", "/community/post?id=missing"} {
		res = communityRequest(t, app, http.MethodDelete, target, "", token)
		expected := 400
		if strings.Contains(target, "id=") {
			expected = 404
		}
		if res.StatusCode != expected {
			t.Fatalf("delete %s returned %d", target, res.StatusCode)
		}
		res.Body.Close()
	}
}

func TestCommunityDeletion_DuringTranslationCannotRepublish(t *testing.T) {
	app := newCommunityTestApp(t)
	token, _ := createCommunityUser(t, app)
	id, _ := createCommunityPost(t, app, token)
	translator := translatorFunc(func(context.Context, community.SourceContent) (community.TranslationResult, error) {
		response := communityRequest(t, app, http.MethodDelete, "/community/post?id="+id, "", token)
		if response.StatusCode != 204 {
			t.Fatalf("delete during translation: %d", response.StatusCode)
		}
		response.Body.Close()
		return community.TranslationResult{"en-US": {Body: "cached"}, "ja-JP": {Body: "cached"}}, nil
	})
	_, err := community.RunDueTranslations(context.Background(), app, translator, time.Now().Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	record, _ := app.FindRecordById(community.CollectionPost, id)
	if record.GetString("status") != "deleted" || record.GetString("translation_status") == "ready" {
		t.Fatal("translation republished a deleted post")
	}
	response := communityRequest(t, app, http.MethodGet, "/community/post?id="+id, "", "")
	if response.StatusCode != 404 {
		t.Fatal("post visible after translation completion")
	}
	response.Body.Close()
}

func TestCommunitySchemaAndCronAreLocked(t *testing.T) {
	app := newCommunityTestApp(t)
	collection, err := app.FindCollectionByNameOrId(community.CollectionPost)
	if err != nil {
		t.Fatalf("find community_post: %v", err)
	}
	if collection.ListRule != nil || collection.ViewRule != nil || collection.CreateRule != nil || collection.UpdateRule != nil || collection.DeleteRule != nil {
		t.Fatalf("community_post raw REST rules must be locked")
	}
	community.RegisterTranslationCron(app, &fakeTranslator{})
	found := false
	for _, job := range app.Cron().Jobs() {
		if job.Id() == community.TranslationCronJobID && job.Expression() == community.TranslationCronExprUTC {
			found = true
		}
	}
	if !found {
		t.Fatalf("community translation cron was not registered")
	}
}

func TestCommunityPosts_GameFilterBeforePagination(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	collection, err := app.FindCollectionByNameOrId("game_series")
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, name := range []string{"First", "Second", "Other"} {
		record := core.NewRecord(collection)
		record.Set("en", name)
		if err := app.Save(record); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, record.Id)
		response := communityRequest(t, app, http.MethodPost, "/community/post", fmt.Sprintf(`{"body":"제목 없이 작성한 테스트 원문","game_series":%q}`, record.Id), token)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create status %d", response.StatusCode)
		}
		payload := decodeBody(t, response)
		if payload["title"] != "" {
			t.Fatalf("expected titleless post: %#v", payload)
		}
	}
	target := "/community/posts?game_series=" + ids[0] + "&game_series=" + ids[1] + "&per_page=1"
	for page := 1; page <= 2; page++ {
		response := communityRequest(t, app, http.MethodGet, target+fmt.Sprintf("&page=%d", page), "", "")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("list status %d", response.StatusCode)
		}
		payload := decodeBody(t, response)
		if payload["total"] != float64(2) || payload["last_page"] != float64(2) {
			t.Fatalf("wrong filtered count: %#v", payload)
		}
		items := payload["items"].([]any)
		if len(items) != 1 || items[0].(map[string]any)["game_series"] == ids[2] {
			t.Fatalf("wrong filtered page: %#v", payload)
		}
	}
	response := communityRequest(t, app, http.MethodGet, "/community/posts?game_series=", "", "")
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty series status %d", response.StatusCode)
	}
	response.Body.Close()
}

func communityPhotoRequest(t *testing.T, app *tests.TestApp, token, flair string, files [][]byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	writer.WriteField("body", "이미지 업적 테스트")
	writer.WriteField("flair", flair)
	for i, data := range files {
		part, err := writer.CreateFormFile("photos", fmt.Sprintf("photo%d.png", i))
		if err != nil {
			t.Fatal(err)
		}
		part.Write(data)
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
	req := httptest.NewRequest(http.MethodPost, "/community/post", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	mux.ServeHTTP(recorder, req)
	return recorder.Result()
}

func TestCommunityPost_MultiplePhotosAndAchievementGallery(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, user := createCommunityUser(t, app)
	otherToken, _ := createCommunityUser(t, app)
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	files := make([][]byte, 8)
	for i := range files {
		files[i] = img.Bytes()
	}
	response := communityPhotoRequest(t, app, token, "achievement", files)
	payload := decodeBody(t, response)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create: %d %#v", response.StatusCode, payload)
	}
	if len(payload["photos"].([]any)) != 8 {
		t.Fatalf("missing photos: %#v", payload)
	}
	id := payload["id"].(string)
	// Text-only achievements, other categories, and another author's images must not leak into this gallery.
	for _, input := range []struct {
		token, flair string
		files        [][]byte
	}{{token, "achievement", nil}, {token, "chitchat", files[:1]}, {otherToken, "achievement", files[:1]}} {
		res := communityPhotoRequest(t, app, input.token, input.flair, input.files)
		data := decodeBody(t, res)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("fixture: %#v", data)
		}
	}
	response = communityRequest(t, app, http.MethodGet, "/community/posts?author="+user.Id+"&flair=achievement&has_images=true&per_page=1", "", "")
	payload = decodeBody(t, response)
	if response.StatusCode != 200 || payload["total"] != float64(1) || payload["items"].([]any)[0].(map[string]any)["id"] != id {
		t.Fatalf("gallery: %d %#v", response.StatusCode, payload)
	}
	response = communityRequest(t, app, http.MethodPut, "/community/post?id="+id, `{"body":"사진은 그대로 유지"}`, token)
	payload = decodeBody(t, response)
	if response.StatusCode != 200 || len(payload["photos"].([]any)) != 8 {
		t.Fatalf("edit: %d %#v", response.StatusCode, payload)
	}
	files = append(files, img.Bytes())
	for _, invalid := range [][][]byte{files, {[]byte("not an image")}, {bytes.Repeat([]byte("x"), community.MaxPostPhotoBytes+1)}} {
		response = communityPhotoRequest(t, app, token, "achievement", invalid)
		response.Body.Close()
		if response.StatusCode != 400 {
			t.Fatalf("invalid upload accepted: %d", response.StatusCode)
		}
	}
	response = communityRequest(t, app, http.MethodDelete, "/community/post?id="+id, "", token)
	if response.StatusCode != 204 {
		t.Fatalf("delete image post: %d", response.StatusCode)
	}
	response.Body.Close()
	record, _ := app.FindRecordById(community.CollectionPost, id)
	if len(record.GetStringSlice("photos")) != 0 {
		t.Fatal("deleted post retains public photos")
	}
	response = communityRequest(t, app, http.MethodGet, "/community/posts?author="+user.Id+"&flair=achievement&has_images=true", "", "")
	if decodeBody(t, response)["total"] != float64(0) {
		t.Fatal("deleted images remain in achievement gallery")
	}

}
