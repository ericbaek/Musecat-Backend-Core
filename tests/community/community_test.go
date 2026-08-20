package community_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	community "github.com/ericbaek/musecat-backend-core/handlers/community"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

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

func TestGeminiTranslator_RequestAndResponseContract(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/models/gemini-2.5-flash-lite:generateContent" {
			t.Errorf("unexpected Gemini path %q", request.URL.Path)
		}
		if request.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("missing API key header")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode Gemini request: %v", err)
		}
		instruction, _ := json.Marshal(body["system_instruction"])
		if !strings.Contains(string(instruction), "기체=machine cabinet") || !strings.Contains(string(instruction), "maimai DX") {
			t.Errorf("expected built-in and custom glossary in prompt: %s", instruction)
		}
		translation := `{"en-US":{"title":"Arcade tip","body":"The maimai DX cabinet is on the second floor."},"ja-JP":{"title":"ゲームセンターのヒント","body":"maimai DXの筐体は2階にあります。"}}`
		encoded, _ := json.Marshal(map[string]any{
			"candidates": []any{
				map[string]any{
					"content": map[string]any{
						"parts": []any{map[string]any{"text": translation}},
					},
				},
			},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(encoded))),
			Request:    request,
		}, nil
	})}

	translator, err := community.NewGeminiTranslator(community.TranslationConfig{
		APIKey: "test-key", BaseURL: "https://translation.test", Model: "gemini-2.5-flash-lite", Glossary: "maimai DX: preserve official spelling",
	}, client)
	if err != nil {
		t.Fatalf("create Gemini translator: %v", err)
	}
	result, err := translator.Translate(context.Background(), community.SourceContent{Title: "오락실 팁", Body: "maimai DX 기체는 2층에 있습니다."})
	if err != nil {
		t.Fatalf("translate: %v", err)
	}
	if result["en-US"].Title != "Arcade tip" || result["ja-JP"].Body == "" {
		t.Fatalf("unexpected translation result: %#v", result)
	}
}

func TestDeepSeekTranslator_RequestAndResponseContract(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.deepseek.test/chat/completions" {
			t.Errorf("unexpected DeepSeek URL %q", request.URL.String())
		}
		if request.Header.Get("Authorization") != "Bearer deepseek-test-key" {
			t.Errorf("missing DeepSeek bearer token")
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode DeepSeek request: %v", err)
		}
		if body["model"] != "deepseek-v4-flash" {
			t.Errorf("unexpected DeepSeek model %#v", body["model"])
		}
		thinking, _ := body["thinking"].(map[string]any)
		if thinking["type"] != "disabled" {
			t.Errorf("expected non-thinking mode: %#v", body["thinking"])
		}
		if body["max_tokens"] != float64(16384) {
			t.Errorf("unexpected DeepSeek output limit %#v", body["max_tokens"])
		}
		format, _ := body["response_format"].(map[string]any)
		if format["type"] != "json_object" {
			t.Errorf("expected JSON output mode: %#v", body["response_format"])
		}
		translation := `{"en-US":{"title":"Cabinet condition","body":"The CHUNITHM SUN cabinet is on the second floor."},"ja-JP":{"title":"筐体の状態","body":"CHUNITHM SUNの筐体は2階にあります。"}}`
		encoded, _ := json.Marshal(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"content": translation}}},
		})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(string(encoded))),
			Request:    request,
		}, nil
	})}
	translator, err := community.NewConfiguredTranslator(community.TranslationConfig{
		Provider: "deepseek", APIKey: "deepseek-test-key", BaseURL: "https://api.deepseek.test", Model: "deepseek-v4-flash",
	}, client)
	if err != nil {
		t.Fatalf("create DeepSeek translator: %v", err)
	}
	result, err := translator.Translate(context.Background(), community.SourceContent{Title: "기체 상태", Body: "CHUNITHM SUN 기체는 2층에 있습니다."})
	if err != nil {
		t.Fatalf("translate with DeepSeek: %v", err)
	}
	if result["en-US"].Title != "Cabinet condition" || result["ja-JP"].Body == "" {
		t.Fatalf("unexpected DeepSeek translation result: %#v", result)
	}
}

func TestTranslationConfig_DeepSeekDefaults(t *testing.T) {
	t.Setenv("MUSECAT_TRANSLATION_PROVIDER", "deepseek")
	t.Setenv("DEEPSEEK_API_KEY", "deepseek-env-key")
	t.Setenv("MUSECAT_TRANSLATION_API_KEY", "")
	t.Setenv("MUSECAT_TRANSLATION_BASE_URL", "")
	t.Setenv("MUSECAT_TRANSLATION_MODEL", "")
	config := community.TranslationConfigFromEnv()
	if config.Provider != "deepseek" || config.APIKey != "deepseek-env-key" || config.BaseURL != "https://api.deepseek.com" || config.Model != "deepseek-v4-flash" {
		t.Fatalf("unexpected DeepSeek defaults: %#v", config)
	}
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
	community.RegisterTranslationCron(app)
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
