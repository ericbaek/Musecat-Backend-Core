package user_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	pbtypes "github.com/pocketbase/pocketbase/tools/types"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

func seedVisitArcade(tb testing.TB, app *tests.TestApp, timezone string) *core.Record {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		tb.Fatalf("load arcade collection: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("country", "KR")
	rec.Set("public", true)
	rec.Set("closed", false)
	rec.Set("timezone", timezone)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("save arcade: %v", err)
	}
	basicColl, err := app.FindCollectionByNameOrId("arcade_basic")
	if err != nil {
		tb.Fatalf("load arcade_basic collection: %v", err)
	}
	basic := core.NewRecord(basicColl)
	basic.Set("arcade", rec.Id)
	basic.Set("name", "Visit Arcade")
	basic.Set("address", "Visit Address")
	basic.Set("location", pbtypes.GeoPoint{Lat: 37.5665, Lon: 126.9780})
	if err := app.Save(basic); err != nil {
		tb.Fatalf("save arcade basic: %v", err)
	}
	rec.Set("basic", basic.Id)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("link arcade basic: %v", err)
	}
	return rec
}

func TestArcadeVisitAwardsAndDeduplicatesByArcadeDay(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, userRec := createAuthUser(t, app, true)
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	restore := userhandler.SetVisitNowForTest(func() time.Time { return time.Date(2026, 7, 1, 14, 59, 0, 0, time.UTC) })
	t.Cleanup(restore)
	headers := map[string]string{"Authorization": "Bearer " + token}
	body := `{"arcade":"` + arcade.Id + `","lat":37.5665,"lon":126.9780,"accuracy":100}`
	res := doUserRequest(t, app, http.MethodPost, "/arcade/visit", headers, body)
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("first visit status = %d: %s", res.StatusCode, body)
	}
	var got map[string]any
	_ = json.NewDecoder(res.Body).Decode(&got)
	if got["gained_exp"] != float64(6) || got["visited"] != true {
		t.Fatalf("unexpected first visit: %#v", got)
	}
	res = doUserRequest(t, app, http.MethodPost, "/arcade/visit", headers, body)
	_ = json.NewDecoder(res.Body).Decode(&got)
	if got["gained_exp"] != float64(0) || got["already_visited"] != true {
		t.Fatalf("unexpected duplicate visit: %#v", got)
	}
	restore = userhandler.SetVisitNowForTest(func() time.Time { return time.Date(2026, 7, 1, 15, 1, 0, 0, time.UTC) })
	t.Cleanup(restore)
	res = doUserRequest(t, app, http.MethodPost, "/arcade/visit", headers, body)
	_ = json.NewDecoder(res.Body).Decode(&got)
	if got["gained_exp"] != float64(3) {
		t.Fatalf("unexpected revisit: %#v", got)
	}
	level, err := app.FindRecordById(userhandler.CollectionUserLevel, userRec.Id)
	if err != nil {
		t.Fatal(err)
	}
	if level.GetInt("exp") != 9 {
		t.Fatalf("exp=%d, want 9", level.GetInt("exp"))
	}
}

func TestArcadeVisitRejectsOutOfRangeAndIneligible(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, _ := createAuthUser(t, app, true)
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	headers := map[string]string{"Authorization": "Bearer " + token}
	for _, body := range []string{`{"arcade":"` + arcade.Id + `","lat":37.5665,"lon":126.9780,"accuracy":101}`, `{"arcade":"` + arcade.Id + `","lat":37.5700,"lon":126.9780,"accuracy":10}`} {
		if res := doUserRequest(t, app, http.MethodPost, "/arcade/visit", headers, body); res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusForbidden {
			t.Fatalf("status=%d", res.StatusCode)
		}
	}
	arcade.Set("closed", true)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	res := doUserRequest(t, app, http.MethodPost, "/arcade/visit", headers, `{"arcade":"`+arcade.Id+`","lat":37.5665,"lon":126.9780,"accuracy":10}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("closed status=%d", res.StatusCode)
	}
}

func TestVisitVisibilityControlsPublicProfileStats(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, userRec := createAuthUser(t, app, true)
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	headers := map[string]string{"Authorization": "Bearer " + token}
	res := doUserRequest(t, app, http.MethodPost, "/arcade/visit", headers, `{"arcade":"`+arcade.Id+`","lat":37.5665,"lon":126.9780,"accuracy":10}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("visit status=%d", res.StatusCode)
	}
	res = doUserRequest(t, app, http.MethodGet, "/user?id="+userRec.Id, nil, "")
	var profile map[string]any
	_ = json.NewDecoder(res.Body).Decode(&profile)
	if profile["visit_stats"] == nil {
		t.Fatalf("summary visibility should expose visit stats: %#v", profile)
	}
	stats := profile["visit_stats"].(map[string]any)
	arcades := stats["arcades"].([]any)
	if len(arcades) != 1 || arcades[0].(map[string]any)["arcade"] != arcade.Id || arcades[0].(map[string]any)["visit_count"] != float64(1) {
		t.Fatalf("summary should expose per-arcade visit count: %#v", stats)
	}
	res = doUserRequest(t, app, http.MethodPut, "/user/visit-visibility", headers, `{"visit_visibility":"private"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("visibility status=%d", res.StatusCode)
	}
	res = doUserRequest(t, app, http.MethodGet, "/user?id="+userRec.Id, nil, "")
	profile = map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&profile)
	if _, ok := profile["visit_stats"]; ok {
		t.Fatalf("private visibility leaked visit stats: %#v", profile)
	}
	res = doUserRequest(t, app, http.MethodGet, "/user/me", headers, "")
	profile = map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&profile)
	if profile["visit_stats"] == nil || profile["visits"] == nil {
		t.Fatalf("owner should receive private stats and history: %#v", profile)
	}
}
