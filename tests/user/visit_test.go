package user_test

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/filesystem"
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
	if got["gained_exp"] != float64(5) || got["visited"] != true || got["first_visit_to_arcade"] != true {
		t.Fatalf("unexpected first visit: %#v", got)
	}
	visit, ok := got["visit"].(map[string]any)
	if !ok || visit["arcade"] != arcade.Id || visit["visit_day"] != "2026-07-01" {
		t.Fatalf("unexpected visit echo: %#v", got["visit"])
	}
	for _, sensitive := range []string{"id", "visited_at", "distance_meters", "accuracy_meters", "gained_exp", "last_visit_day", "first_visit_day"} {
		if _, ok := visit[sensitive]; ok {
			t.Fatalf("visit echo leaked %q: %#v", sensitive, visit)
		}
	}
	res = doUserRequest(t, app, http.MethodPost, "/arcade/visit", headers, body)
	_ = json.NewDecoder(res.Body).Decode(&got)
	if got["gained_exp"] != float64(0) || got["already_visited"] != true || got["first_visit_to_arcade"] != false {
		t.Fatalf("unexpected duplicate visit: %#v", got)
	}
	restore = userhandler.SetVisitNowForTest(func() time.Time { return time.Date(2026, 7, 1, 15, 1, 0, 0, time.UTC) })
	t.Cleanup(restore)
	res = doUserRequest(t, app, http.MethodPost, "/arcade/visit", headers, body)
	_ = json.NewDecoder(res.Body).Decode(&got)
	if got["gained_exp"] != float64(2) {
		t.Fatalf("unexpected revisit: %#v", got)
	}
	level, err := app.FindRecordById(userhandler.CollectionUserLevel, userRec.Id)
	if err != nil {
		t.Fatal(err)
	}
	if level.GetInt("exp") != 7 {
		t.Fatalf("exp=%d, want 7", level.GetInt("exp"))
	}
}

func TestArcadeVisitWithoutUsernamePersistsWithoutXP(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, userRec := createAuthUser(t, app, true)
	userRec.Set("username", "")
	if err := app.Save(userRec); err != nil {
		t.Fatalf("failed to clear username: %v", err)
	}
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	restore := userhandler.SetVisitNowForTest(func() time.Time {
		return time.Date(2026, 7, 1, 14, 59, 0, 0, time.UTC)
	})
	t.Cleanup(restore)

	res := doUserRequest(t, app, http.MethodPost, "/arcade/visit", map[string]string{
		"Authorization": "Bearer " + token,
	}, `{"arcade":"`+arcade.Id+`","lat":37.5665,"lon":126.9780,"accuracy":100}`)
	if res.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("expected visit success without XP, got status %d: %s", res.StatusCode, body)
	}
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode visit response: %v", err)
	}
	if payload["visited"] != true || payload["gained_exp"] != float64(0) || payload["exp"] != float64(0) {
		t.Fatalf("unexpected username-less visit response: %#v", payload)
	}

	visits, err := app.FindRecordsByFilter(userhandler.CollectionArcadeVisit, "user={:user} && arcade={:arcade}", "", 0, 0, map[string]any{
		"user":   userRec.Id,
		"arcade": arcade.Id,
	})
	if err != nil {
		t.Fatalf("failed to load visit record: %v", err)
	}
	if len(visits) != 1 || visits[0].GetInt("gained_exp") != 0 {
		t.Fatalf("expected one visit with zero XP, got %#v", visits)
	}
	if _, err := app.FindRecordById(userhandler.CollectionUserLevel, userRec.Id); err == nil {
		t.Fatal("expected visit without username not to create user_level")
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
	arcadeA := seedVisitArcade(t, app, "Asia/Seoul")
	arcadeB := seedVisitArcade(t, app, "Asia/Tokyo")
	arcadeC := seedVisitArcade(t, app, "Asia/Seoul")
	setVisitArcadeProfileDetails(t, app, arcadeA, "Arcade A", "KR", 37.5665, 126.9780)
	setVisitArcadeProfileDetails(t, app, arcadeB, "Arcade B", "JP", 35.6762, 139.6503)
	setVisitArcadeProfileDetails(t, app, arcadeC, "Arcade C", "KR", 37.5512, 126.9882)
	firstPhotoID := seedVisitArcadePhoto(t, app, arcadeA.Id, userRec.Id, "first.png")
	secondPhotoID := seedVisitArcadePhoto(t, app, arcadeA.Id, userRec.Id, "second.png")
	linkVisitArcadePhotos(t, app, arcadeA, userRec.Id, []string{firstPhotoID, secondPhotoID})

	first := time.Date(2026, 7, 21, 1, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	third := second.Add(time.Hour)
	fourth := third.Add(time.Hour)
	seedProfileVisit(t, app, userRec.Id, arcadeA.Id, "2026-07-21", first)
	seedProfileVisit(t, app, userRec.Id, arcadeB.Id, "2026-07-21", second)
	seedProfileVisit(t, app, userRec.Id, arcadeA.Id, "2026-07-22", third)
	seedProfileVisit(t, app, userRec.Id, arcadeC.Id, "2026-07-22", fourth)

	headers := map[string]string{"Authorization": "Bearer " + token}
	res := doUserRequest(t, app, http.MethodGet, "/user?id="+userRec.Id, nil, "")
	var profile map[string]any
	_ = json.NewDecoder(res.Body).Decode(&profile)
	if profile["visit_stats"] == nil {
		t.Fatalf("summary visibility should expose visit stats: %#v", profile)
	}
	stats := profile["visit_stats"].(map[string]any)
	if stats["total_visits"] != float64(4) || stats["distinct_arcades"] != float64(3) {
		t.Fatalf("unexpected visit totals: %#v", stats)
	}
	wantDistance := testVisitDistance(37.5665, 126.9780, 35.6762, 139.6503) + testVisitDistance(35.6762, 139.6503, 37.5665, 126.9780) + testVisitDistance(37.5665, 126.9780, 37.5512, 126.9882)
	if got := stats["total_distance_meters"].(float64); math.Abs(got-wantDistance) > 0.001 {
		t.Fatalf("total_distance_meters=%v, want %v", got, wantDistance)
	}
	countries := stats["countries"].([]any)
	if len(countries) != 2 || countries[0].(map[string]any)["country"] != "KR" || countries[0].(map[string]any)["arcade_count"] != float64(2) || countries[1].(map[string]any)["country"] != "JP" || countries[1].(map[string]any)["arcade_count"] != float64(1) {
		t.Fatalf("unexpected country totals: %#v", countries)
	}
	arcades := stats["arcades"].([]any)
	if len(arcades) != 3 {
		t.Fatalf("expected three visited arcades: %#v", arcades)
	}
	for index, want := range []struct {
		arcade string
		name   string
		count  float64
		day    string
	}{
		{arcadeA.Id, "Arcade A", 2, "2026-07-22"},
		{arcadeC.Id, "Arcade C", 1, "2026-07-22"},
		{arcadeB.Id, "Arcade B", 1, "2026-07-21"},
	} {
		item := arcades[index].(map[string]any)
		if item["arcade"] != want.arcade || item["name"] != want.name || item["visit_count"] != want.count {
			t.Fatalf("unexpected summary arcade at %d: %#v", index, item)
		}
		if _, ok := item["visit_days"]; ok {
			t.Fatalf("summary must not expose every visit day: %#v", item)
		}
		if want.arcade == arcadeA.Id {
			if got := item["photo_url"]; got != "/arcade/photo/file?id="+firstPhotoID {
				t.Fatalf("expected first arcade photo URL, got %#v", item)
			}
		} else if _, ok := item["photo_url"]; ok {
			t.Fatalf("unexpected photo URL for arcade without photos: %#v", item)
		}
		for _, sensitive := range []string{"id", "visited_at", "distance_meters", "accuracy_meters", "gained_exp", "last_visit_day", "first_visit_day"} {
			if _, ok := item[sensitive]; ok {
				t.Fatalf("summary leaked %q: %#v", sensitive, item)
			}
		}
	}
	if _, ok := profile["visits"]; ok {
		t.Fatalf("profile should not expose a separate raw visit history: %#v", profile)
	}

	res = doUserRequest(t, app, http.MethodPut, "/user/visit-visibility", headers, `{"visit_visibility":"full"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("full visibility status=%d", res.StatusCode)
	}
	res = doUserRequest(t, app, http.MethodGet, "/user?id="+userRec.Id, nil, "")
	profile = map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&profile)
	fullArcades := profile["visit_stats"].(map[string]any)["arcades"].([]any)
	visitDays := fullArcades[0].(map[string]any)["visit_days"].([]any)
	if len(visitDays) != 2 || visitDays[0] != "2026-07-22" || visitDays[1] != "2026-07-21" {
		t.Fatalf("full visibility visit days = %#v", visitDays)
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
	if profile["visit_stats"] == nil {
		t.Fatalf("owner should receive private stats and history: %#v", profile)
	}
	ownerArcades := profile["visit_stats"].(map[string]any)["arcades"].([]any)
	if _, ok := ownerArcades[0].(map[string]any)["visit_days"]; !ok {
		t.Fatalf("owner should receive complete visit days: %#v", ownerArcades[0])
	}

	res = doUserRequest(t, app, http.MethodGet, "/user/visits", headers, "")
	var ownVisits map[string]any
	_ = json.NewDecoder(res.Body).Decode(&ownVisits)
	if ownVisits["stats"] == nil {
		t.Fatalf("expected profile-ready visit stats: %#v", ownVisits)
	}
	if _, ok := ownVisits["visits"]; ok {
		t.Fatalf("authenticated visit endpoint leaked raw history: %#v", ownVisits)
	}

	arcadeB.Set("public", false)
	if err := app.Save(arcadeB); err != nil {
		t.Fatalf("make arcade private: %v", err)
	}
	res = doUserRequest(t, app, http.MethodPut, "/user/visit-visibility", headers, `{"visit_visibility":"full"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("restore full visibility status=%d", res.StatusCode)
	}
	res = doUserRequest(t, app, http.MethodGet, "/user?id="+userRec.Id, nil, "")
	profile = map[string]any{}
	_ = json.NewDecoder(res.Body).Decode(&profile)
	privateSafeStats := profile["visit_stats"].(map[string]any)
	if privateSafeStats["total_visits"] != float64(3) || privateSafeStats["distinct_arcades"] != float64(2) {
		t.Fatalf("private arcade should be excluded from profile totals: %#v", privateSafeStats)
	}
	for _, raw := range privateSafeStats["arcades"].([]any) {
		if raw.(map[string]any)["arcade"] == arcadeB.Id {
			t.Fatalf("private arcade leaked through profile visits: %#v", raw)
		}
	}
}

func seedVisitArcadePhoto(tb testing.TB, app *tests.TestApp, arcadeID, createdBy, filename string) string {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("arcade_photo_atoms")
	if err != nil {
		tb.Fatalf("load photo atom collection: %v", err)
	}
	file, err := filesystem.NewFileFromBytes(pngFixtureBytes(), filename)
	if err != nil {
		tb.Fatalf("build photo file: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("public", true)
	rec.Set("createdBy", createdBy)
	rec.Set("photo", file)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("save photo atom: %v", err)
	}
	return rec.Id
}

func linkVisitArcadePhotos(tb testing.TB, app *tests.TestApp, arcade *core.Record, createdBy string, photoIDs []string) {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("arcade_photo")
	if err != nil {
		tb.Fatalf("load photo molecule collection: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("arcade", arcade.Id)
	rec.Set("createdBy", createdBy)
	rec.Set("photos", photoIDs)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("save photo molecule: %v", err)
	}
	arcade.Set("photo", rec.Id)
	if err := app.Save(arcade); err != nil {
		tb.Fatalf("link arcade photo molecule: %v", err)
	}
}

func setVisitArcadeProfileDetails(tb testing.TB, app *tests.TestApp, arcade *core.Record, name, country string, lat, lon float64) {
	tb.Helper()
	arcade.Set("country", country)
	if err := app.Save(arcade); err != nil {
		tb.Fatalf("set arcade country: %v", err)
	}
	basic, err := app.FindRecordById("arcade_basic", arcade.GetString("basic"))
	if err != nil {
		tb.Fatalf("load arcade basic: %v", err)
	}
	basic.Set("name", name)
	basic.Set("location", pbtypes.GeoPoint{Lat: lat, Lon: lon})
	if err := app.Save(basic); err != nil {
		tb.Fatalf("set arcade details: %v", err)
	}
}

func seedProfileVisit(tb testing.TB, app *tests.TestApp, userID, arcadeID, day string, visitedAt time.Time) {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("arcade_visit")
	if err != nil {
		tb.Fatalf("load arcade_visit: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("user", userID)
	rec.Set("arcade", arcadeID)
	rec.Set("visit_day", day)
	rec.Set("visited_at", visitedAt.Format(time.RFC3339))
	if err := app.Save(rec); err != nil {
		tb.Fatalf("save profile visit: %v", err)
	}
}

func testVisitDistance(lat1, lon1, lat2, lon2 float64) float64 {
	toRad := func(v float64) float64 { return v * math.Pi / 180 }
	dLat, dLon := toRad(lat2-lat1), toRad(lon2-lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 6371000 * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
