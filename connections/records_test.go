package connections_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/connections"
)

func dbxParams(connection string) dbx.Params { return dbx.Params{"c": connection} }

func envelope(service, sub, visibility string) map[string]any {
	return map[string]any{
		"spec": connections.RecordSpec, "service": service, "sub": sub, "visibility": visibility,
		"updated_at": time.Now().UTC().Format(time.RFC3339),
		"source":     map[string]any{"name": "Rhythm Info", "url": "https://rhythm.example/u/1"},
		"player":     map[string]any{"name": "PLAYER", "level": 85},
		"fields": []map[string]any{
			{"key": "rating", "label": map[string]string{"en": "Rating", "ko": "레이팅"}, "type": "decimal", "value": 16.42, "precision": 2},
			{"key": "plays", "label": map[string]string{"en": "Plays"}, "type": "integer", "value": 2310},
		},
		"sections": []map[string]any{{
			"key": "best", "label": map[string]string{"en": "Best"},
			"columns": []map[string]any{{"key": "title", "label": map[string]string{"en": "Song"}, "type": "string"}, {"key": "score", "label": map[string]string{"en": "Score"}, "type": "integer"}},
			"rows":    [][]any{{"Song A", 1009876}},
			"total":   30, "more_url": "https://rhythm.example/u/1/best",
		}},
	}
}

func status(code int, body any) *http.Response {
	data, _ := json.Marshal(body)
	return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data)))}
}

func TestRecordsFetchCacheVisibilityAndRefresh(t *testing.T) {
	f := newFixture(t)
	id := f.complete(f.start(f.token, "rhythm", "chunithm"))
	calls := 0
	f.mu.Lock()
	f.records = func(r *http.Request) *http.Response {
		calls++
		if r.Header.Get("Authorization") != "Bearer secret-access" && r.Header.Get("Authorization") != "Bearer secret-access-2" {
			return status(401, map[string]string{"error": "invalid_token"})
		}
		if r.URL.Path != "/records/chunithm" {
			return status(404, map[string]string{"error": "not_found"})
		}
		return status(200, envelope("chunithm", f.subject, "public"))
	}
	f.mu.Unlock()

	// Owner read fetches once, caches, and never exposes the subject.
	view := f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm", nil, 200)
	if view["status"] != connections.RecordOK || view["payload"] == nil || calls != 1 {
		t.Fatal("first read", view, calls)
	}
	payload := view["payload"].(map[string]any)
	if payload["sub"] != nil || payload["visibility"] != "public" || len(payload["fields"].([]any)) != 2 {
		t.Fatal("payload projection", payload)
	}
	f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm", nil, 200)
	if calls != 1 {
		t.Fatal("cache miss within freshness window")
	}
	// refresh=true is rate limited to once per minute.
	f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm?refresh=true", nil, 200)
	if calls != 1 {
		t.Fatal("refresh bypassed the minimum interval")
	}
	// Unknown service and foreign owner are 404.
	f.request(f.token, "GET", "/user/connections/"+id+"/records/sdvx", nil, 404)
	otherToken, other := f.newUser()
	f.request(otherToken, "GET", "/user/connections/"+id+"/records/chunithm", nil, 404)

	// Public profile shows nothing until the Musecat user publishes the service.
	if items := f.request("", "GET", "/user/records?user="+f.user.Id, nil, 200)["items"].([]any); len(items) != 0 {
		t.Fatal("private service published", items)
	}
	f.request(f.token, "PUT", "/user/connections/"+id+"/services/chunithm", map[string]string{"visibility": "banana"}, 400)
	f.request(otherToken, "PUT", "/user/connections/"+id+"/services/chunithm", map[string]string{"visibility": "public"}, 404)
	f.request(f.token, "PUT", "/user/connections/"+id+"/services/chunithm", map[string]string{"visibility": "public"}, 200)
	items := f.request("", "GET", "/user/records?user="+f.user.Id, nil, 200)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["provider_name"] != "rhythm" || items[0].(map[string]any)["service_name"] != "CHUNITHM" {
		t.Fatal("published record missing", items)
	}
	if len(f.request("", "GET", "/user/records?user="+other.Id, nil, 200)["items"].([]any)) != 0 {
		t.Fatal("records leaked to another user")
	}
	f.request("", "GET", "/user/records?user=nope", nil, 400)

	// Partner-side private overrides Musecat-side public; stale cache is refetched.
	f.mu.Lock()
	f.records = func(*http.Request) *http.Response { return status(403, map[string]string{"error": "records_private"}) }
	f.mu.Unlock()
	row, err := f.app.FindFirstRecordByFilter(connections.Records, "connection = {:c}", dbxParams(id))
	if err != nil {
		t.Fatal(err)
	}
	row.Set("fetched", time.Now().Add(-25*time.Hour))
	if err := f.app.Save(row); err != nil {
		t.Fatal(err)
	}
	view = f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm", nil, 200)
	if view["status"] != connections.RecordPrivate || view["payload"] != nil {
		t.Fatal("partner private not honored", view)
	}
	if len(f.request("", "GET", "/user/records?user="+f.user.Id, nil, 200)["items"].([]any)) != 0 {
		t.Fatal("private partner data still public")
	}

	// Expired access token is refreshed once and the new family is re-sealed.
	account, _ := f.app.FindRecordById(connections.Accounts, id)
	before := account.GetString("credentials")
	f.mu.Lock()
	f.records = func(r *http.Request) *http.Response {
		if r.Header.Get("Authorization") == "Bearer secret-access" {
			return status(401, map[string]string{"error": "invalid_token"})
		}
		return status(200, envelope("chunithm", f.subject, "public"))
	}
	f.mu.Unlock()
	row.Set("fetched", time.Now().Add(-25*time.Hour))
	_ = f.app.Save(row)
	view = f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm", nil, 200)
	if view["status"] != connections.RecordOK || f.refreshes != 1 {
		t.Fatal("refresh on 401", view["status"], view["error"], f.refreshes)
	}
	account, _ = f.app.FindRecordById(connections.Accounts, id)
	if account.GetString("credentials") == before || strings.Contains(account.GetString("credentials"), "secret-access-2") {
		t.Fatal("refreshed credentials not re-sealed")
	}

	// Subject mismatch is rejected as an invalid envelope, never stored as someone else's data.
	f.mu.Lock()
	f.records = func(*http.Request) *http.Response { return status(200, envelope("chunithm", "someone-else", "public")) }
	f.mu.Unlock()
	row.Set("fetched", time.Now().Add(-25*time.Hour))
	_ = f.app.Save(row)
	view = f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm", nil, 200)
	if view["status"] != connections.RecordUnavailable || view["error"] != "invalid_envelope" || view["payload"] != nil {
		t.Fatal("subject mismatch accepted", view)
	}
}

func TestParseEnvelopeRejectsMalformedInput(t *testing.T) {
	base := func() map[string]any { return envelope("chunithm", "sub-1", "public") }
	good, _ := json.Marshal(base())
	if _, err := connections.ParseEnvelope(good, "chunithm", "sub-1"); err != nil {
		t.Fatal("valid envelope rejected", err)
	}
	cases := map[string]func(map[string]any){
		"spec":           func(e map[string]any) { e["spec"] = "v0" },
		"service":        func(e map[string]any) { e["service"] = "sdvx" },
		"visibility":     func(e map[string]any) { e["visibility"] = "friends" },
		"updated_at":     func(e map[string]any) { e["updated_at"] = "yesterday" },
		"source missing": func(e map[string]any) { delete(e, "source") },
		"source http":    func(e map[string]any) { e["source"] = map[string]any{"name": "x", "url": "http://x"} },
		"field type":     func(e map[string]any) { e["fields"].([]map[string]any)[0]["type"] = "blob" },
		"field value":    func(e map[string]any) { e["fields"].([]map[string]any)[1]["value"] = "many" },
		"field label": func(e map[string]any) {
			e["fields"].([]map[string]any)[0]["label"] = map[string]string{"ko": "레이팅"}
		},
		"duplicate key":   func(e map[string]any) { e["fields"].([]map[string]any)[1]["key"] = "rating" },
		"too many fields": func(e map[string]any) { e["fields"] = make([]map[string]any, 25) },
		"row width":       func(e map[string]any) { e["sections"].([]map[string]any)[0]["rows"] = [][]any{{"only one"}} },
		"cell type":       func(e map[string]any) { e["sections"].([]map[string]any)[0]["rows"] = [][]any{{"Song", "high"}} },
		"too many cols": func(e map[string]any) {
			cols := []map[string]any{}
			for i := 0; i < 9; i++ {
				cols = append(cols, map[string]any{"key": fmt.Sprint("c", i), "label": map[string]string{"en": "C"}, "type": "string"})
			}
			e["sections"].([]map[string]any)[0]["columns"] = cols
			e["sections"].([]map[string]any)[0]["rows"] = [][]any{}
		},
	}
	for name, mutate := range cases {
		e := base()
		mutate(e)
		data, _ := json.Marshal(e)
		if _, err := connections.ParseEnvelope(data, "chunithm", "sub-1"); err == nil {
			t.Errorf("%s: malformed envelope accepted", name)
		}
	}
	if _, err := connections.ParseEnvelope(good, "chunithm", "other"); err == nil {
		t.Fatal("subject mismatch accepted")
	}
	if _, err := connections.ParseEnvelope(append(append([]byte{}, good...), []byte(` {}`)...), "chunithm", "sub-1"); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if _, err := connections.ParseEnvelope(append(good, make([]byte, 256<<10)...), "chunithm", "sub-1"); err == nil {
		t.Fatal("oversized envelope accepted")
	}
}

func TestRecordsPublicCacheExpiresAndProviderRemovalHidesIt(t *testing.T) {
	f := newFixture(t)
	id := f.complete(f.start(f.token, "rhythm", "chunithm"))
	f.records = func(*http.Request) *http.Response { return status(200, envelope("chunithm", f.subject, "public")) }
	f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm", nil, 200)
	f.request(f.token, "PUT", "/user/connections/"+id+"/services/chunithm", map[string]string{"visibility": "public"}, 200)
	row, _ := f.app.FindFirstRecordByFilter(connections.Records, "connection={:c}", dbxParams(id))
	row.Set("fetched", time.Now().Add(-25*time.Hour))
	if err := f.app.Save(row); err != nil {
		t.Fatal(err)
	}
	if items, err := f.manager.PublicRecords(f.app, f.user.Id); err != nil || len(items) != 0 {
		t.Fatal("expired public cache served", items, err)
	}
	row.Set("fetched", time.Now())
	if err := f.app.Save(row); err != nil {
		t.Fatal(err)
	}
	// Reloading a manager without this registration must hide an otherwise fresh cache.
	disabled, err := connections.NewManager(nil, []byte(strings.Repeat("k", 32)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if items, err := disabled.PublicRecords(f.app, f.user.Id); err != nil || len(items) != 0 {
		t.Fatal("disabled provider still published", items, err)
	}
}

func TestRecordsHonorDurableRetryAfter(t *testing.T) {
	f := newFixture(t)
	id := f.complete(f.start(f.token, "rhythm", "chunithm"))
	calls := 0
	f.records = func(*http.Request) *http.Response {
		calls++
		response := status(429, map[string]string{"error": "rate_limited"})
		response.Header.Set("Retry-After", "7200")
		return response
	}
	path := "/user/connections/" + id + "/records/chunithm?refresh=true"
	view := f.request(f.token, "GET", path, nil, 200)
	if view["error"] != "rate_limited" {
		t.Fatal(view)
	}
	row, _ := f.app.FindFirstRecordByFilter(connections.Records, "connection={:c}", dbxParams(id))
	if time.Until(row.GetDateTime("retry_at").Time()) < 119*time.Minute {
		t.Fatal("Retry-After lost", row)
	}
	// The normal one-minute refresh throttle has expired, but upstream backoff hasn't.
	row.Set("fetched", time.Now().Add(-2*time.Minute))
	if err := f.app.Save(row); err != nil {
		t.Fatal(err)
	}
	f.request(f.token, "GET", path, nil, 200)
	if calls != 1 {
		t.Fatal("upstream backoff bypassed", calls)
	}
	row.Set("retry_at", time.Now().Add(-time.Second))
	if err := f.app.Save(row); err != nil {
		t.Fatal(err)
	}
	f.request(f.token, "GET", path, nil, 200)
	if calls != 2 {
		t.Fatal("backoff never ended", calls)
	}
}

func TestRecordFetchCannotRecreateDisconnectedCache(t *testing.T) {
	f := newFixture(t)
	id := f.complete(f.start(f.token, "rhythm", "chunithm"))
	f.records = func(*http.Request) *http.Response {
		f.request(f.token, "DELETE", "/user/connections/"+id, nil, 204)
		return status(200, envelope("chunithm", f.subject, "public"))
	}
	// A late result must fail after the account is deleted, never recreate a cache row.
	_, err := f.manager.ReadRecord(t.Context(), f.app, connections.Actor{UserID: f.user.Id, Session: f.token, Check: func(core.App) error { return nil }}, id, "chunithm", true)
	if err == nil {
		t.Fatal("late result persisted")
	}
	rows, err := f.app.FindRecordsByFilter(connections.Records, "", "", 0, 0)
	if err != nil || len(rows) != 0 {
		t.Fatal("orphan cache recreated", rows, err)
	}
}

func TestPublicRefreshFetchesNewlyPublishedServiceWithoutPriorOwnerRead(t *testing.T) {
	f := newFixture(t)
	id := f.complete(f.start(f.token, "rhythm", "chunithm"))
	calls := 0
	f.records = func(*http.Request) *http.Response {
		calls++
		return status(200, envelope("chunithm", f.subject, "public"))
	}
	f.request(f.token, "PUT", "/user/connections/"+id+"/services/chunithm", map[string]string{"visibility": "public"}, 200)
	if err := f.manager.RefreshStalePublicRecords(t.Context(), f.app, 50); err != nil {
		t.Fatal(err)
	}
	items, err := f.manager.PublicRecords(f.app, f.user.Id)
	if err != nil || len(items) != 1 || calls != 1 {
		t.Fatal("first published cache never fetched", items, err, calls)
	}
	if err := f.manager.RefreshStalePublicRecords(t.Context(), f.app, 50); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("fresh cache refetched", calls)
	}
}
