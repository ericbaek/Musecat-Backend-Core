package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func noticePolicyFixture(t *testing.T) (*tests.TestApp, string, map[string]string) {
	t.Helper()
	app := newArcadeTestApp(t)
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{Name: "Notice policy", Address: "1 Policy St", Location: location{Lat: 37.5, Lon: 127}})
	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	return app, arcadeID, map[string]string{"Authorization": "Bearer " + token}
}

func policyNoticeBody(arcade, mode string) map[string]any {
	return map[string]any{"arcade": arcade, "title": "  매장 안내  ", "type": "alert", "display_mode": mode, "document": noticeDocument("본문")}
}

func noticePolicyRequest(t *testing.T, app *tests.TestApp, headers map[string]string, method string, body map[string]any, status int) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	res := executeJSONRequest(t, app, method, "/arcade/notice", string(raw), headers)
	payload := decodeJSONMap(t, res)
	if res.StatusCode != status {
		t.Fatalf("%s expected %d, got %d: %#v", method, status, res.StatusCode, payload)
	}
	return payload
}

func TestArcadeNotice_DisplayLimitsAndSlotReuse(t *testing.T) {
	for _, spec := range []struct {
		mode  string
		limit int
	}{{"required", 1}, {"pinned", 3}} {
		t.Run(spec.mode, func(t *testing.T) {
			app, arcade, headers := noticePolicyFixture(t)
			ids := make([]string, 0, spec.limit)
			for i := 0; i < spec.limit; i++ {
				saved := noticePolicyRequest(t, app, headers, http.MethodPost, policyNoticeBody(arcade, spec.mode), 200)
				ids = append(ids, saved["id"].(string))
				if saved["title"] != "매장 안내" || saved["display_mode"] != spec.mode {
					t.Fatalf("invalid metadata: %#v", saved)
				}
			}
			overflow := noticePolicyRequest(t, app, headers, http.MethodPost, policyNoticeBody(arcade, spec.mode), 409)
			if overflow["code"] != "notice_display_limit_exceeded" || overflow["limit"] != float64(spec.limit) {
				t.Fatalf("invalid conflict: %#v", overflow)
			}
			noticePolicyRequest(t, app, headers, http.MethodPut, map[string]any{"id": ids[0], "title": "Changed"}, 200)
			normal := noticePolicyRequest(t, app, headers, http.MethodPost, policyNoticeBody(arcade, "normal"), 200)
			noticePolicyRequest(t, app, headers, http.MethodPut, map[string]any{"id": normal["id"], "display_mode": spec.mode, "title": "Blocked"}, 409)
			unchanged, err := app.FindRecordById("arcade_notice", normal["id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if unchanged.GetString("display_mode") != "normal" || unchanged.GetString("title") != "매장 안내" {
				t.Fatal("failed promotion mutated record")
			}
			noticePolicyRequest(t, app, headers, http.MethodPut, map[string]any{"id": ids[0], "display_mode": "normal"}, 200)
			noticePolicyRequest(t, app, headers, http.MethodPut, map[string]any{"id": normal["id"], "display_mode": spec.mode}, 200)
			noticePolicyRequest(t, app, headers, http.MethodDelete, map[string]any{"id": normal["id"]}, 200)
			noticePolicyRequest(t, app, headers, http.MethodPost, policyNoticeBody(arcade, spec.mode), 200)
		})
	}
}

func TestArcadeNotice_ExpiredDoesNotConsumeSlot(t *testing.T) {
	app, arcade, headers := noticePolicyFixture(t)
	expired := policyNoticeBody(arcade, "required")
	expired["until"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	old := noticePolicyRequest(t, app, headers, http.MethodPost, expired, 200)
	noticePolicyRequest(t, app, headers, http.MethodPost, policyNoticeBody(arcade, "required"), 200)
	noticePolicyRequest(t, app, headers, http.MethodPut, map[string]any{"id": old["id"], "until": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}, 409)
}

func TestArcadeNotice_PolicyValidation(t *testing.T) {
	app, arcade, headers := noticePolicyFixture(t)
	for _, title := range []any{nil, "", "  ", strings.Repeat("가", 5001)} {
		body := policyNoticeBody(arcade, "normal")
		body["title"] = title
		noticePolicyRequest(t, app, headers, http.MethodPost, body, 400)
	}
	for _, mode := range []string{"", "featured", "REQUIRED"} {
		noticePolicyRequest(t, app, headers, http.MethodPost, policyNoticeBody(arcade, mode), 400)
	}
	body := policyNoticeBody(arcade, "normal")
	delete(body, "display_mode")
	saved := noticePolicyRequest(t, app, headers, http.MethodPost, body, 200)
	if saved["display_mode"] != "normal" {
		t.Fatal("missing mode must default normal")
	}
	noticePolicyRequest(t, app, headers, http.MethodPut, map[string]any{"id": saved["id"], "title": " "}, 400)
}

func TestArcadeNotice_OfficialCanManageOnlyOwnAuthorlessLegacy(t *testing.T) {
	app, arcade, _ := noticePolicyFixture(t)
	token, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	addOwnedArcade(t, app, owner, arcade)
	headers := map[string]string{"Authorization": "Bearer " + token}
	legacy := seedNotice(t, app, arcade)
	noticePolicyRequest(t, app, headers, http.MethodPut, map[string]any{"id": legacy, "title": "Legacy repaired"}, 200)
	record, err := app.FindRecordById("arcade_notice", legacy)
	if err != nil {
		t.Fatal(err)
	}
	if record.GetString("createdBy") != "" {
		t.Fatal("legacy author was fabricated")
	}
	noticePolicyRequest(t, app, headers, http.MethodDelete, map[string]any{"id": legacy}, 200)
	foreignArcade, _ := seedPublicArcade(t, app, "", arcadeSeed{Name: "Other arcade", Address: "2 Policy St", Location: location{Lat: 37.6, Lon: 127}})
	foreign := seedNotice(t, app, foreignArcade)
	noticePolicyRequest(t, app, headers, http.MethodPut, map[string]any{"id": foreign, "title": "Blocked"}, 403)
	authored := seedNotice(t, app, arcade)
	_, other := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	record, err = app.FindRecordById("arcade_notice", authored)
	if err != nil {
		t.Fatal(err)
	}
	record.Set("createdBy", other.Id)
	if err = app.Save(record); err != nil {
		t.Fatal(err)
	}
	noticePolicyRequest(t, app, headers, http.MethodDelete, map[string]any{"id": authored}, 403)
}

func TestArcadeNotice_DisplayOrdering(t *testing.T) {
	app, arcade, headers := noticePolicyFixture(t)
	normal := noticePolicyRequest(t, app, headers, http.MethodPost, policyNoticeBody(arcade, "normal"), 200)
	pinned := policyNoticeBody(arcade, "pinned")
	pinned["priority"] = 2
	second := noticePolicyRequest(t, app, headers, http.MethodPost, pinned, 200)
	pinned["priority"] = 1
	first := noticePolicyRequest(t, app, headers, http.MethodPost, pinned, 200)
	required := noticePolicyRequest(t, app, headers, http.MethodPost, policyNoticeBody(arcade, "required"), 200)
	res := executeJSONRequest(t, app, http.MethodGet, "/arcade/notice?arcade="+arcade, "", nil)
	payload := decodeJSONMap(t, res)
	items := payload["items"].([]any)
	for i, want := range []any{required["id"], first["id"], second["id"], normal["id"]} {
		if items[i].(map[string]any)["id"] != want {
			t.Fatalf("wrong display order: %#v", items)
		}
	}
}

func TestArcadeNotice_ConcurrentCreateAndPromotionRespectLimits(t *testing.T) {
	for _, spec := range []struct {
		mode  string
		limit int
	}{{"required", 1}, {"pinned", 3}} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			t.Run(spec.mode+method, func(t *testing.T) {
				app, arcade, headers := noticePolicyFixture(t)
				const requests = 6
				bodies := make([][]byte, requests)
				for i := range bodies {
					body := policyNoticeBody(arcade, spec.mode)
					if method == http.MethodPut {
						normal := noticePolicyRequest(t, app, headers, http.MethodPost, policyNoticeBody(arcade, "normal"), 200)
						body = map[string]any{"id": normal["id"], "display_mode": spec.mode}
					}
					bodies[i], _ = json.Marshal(body)
				}
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
				start := make(chan struct{})
				statuses := make(chan int, requests)
				var wg sync.WaitGroup
				for i := range bodies {
					wg.Add(1)
					go func(i int) {
						defer wg.Done()
						<-start
						request := httptest.NewRequest(method, "/arcade/notice", strings.NewReader(string(bodies[i])))
						request.Header.Set("Content-Type", "application/json")
						request.Header.Set("Authorization", headers["Authorization"])
						recorder := httptest.NewRecorder()
						mux.ServeHTTP(recorder, request)
						statuses <- recorder.Code
					}(i)
				}
				close(start)
				wg.Wait()
				close(statuses)
				successes := 0
				for status := range statuses {
					if status == 200 {
						successes++
					} else if status != 409 {
						t.Errorf("unexpected concurrent status %d", status)
					}
				}
				if successes != spec.limit {
					t.Fatalf("%s %s expected %d successes got %d", method, spec.mode, spec.limit, successes)
				}
				records, err := app.FindRecordsByFilter("arcade_notice", fmt.Sprintf("display_mode='%s'", spec.mode), "", 0, 0)
				if err != nil {
					t.Fatal(err)
				}
				if len(records) != spec.limit {
					t.Fatalf("persisted count %d exceeds %d", len(records), spec.limit)
				}
			})
		}
	}
}
