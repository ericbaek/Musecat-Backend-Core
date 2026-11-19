package arcade_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/ericbaek/musecat-backend-core/arcadearea"
)

func assertArea(tb testing.TB, app core.App, arcadeID, admin1, city, status string, manual bool) {
	tb.Helper()
	a, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatal(err)
	}
	got := arcadearea.FromRecord(a)
	if got.Admin1 != admin1 || got.City != city || got.Status != status || a.GetBool(arcadearea.FieldManual) != manual {
		tb.Fatalf("area = %+v manual=%v, want admin1=%s city=%s status=%s manual=%v", got, a.GetBool(arcadearea.FieldManual), admin1, city, status, manual)
	}
	if status != "" && a.GetDateTime(arcadearea.FieldCheckedAt).IsZero() && !manual {
		tb.Fatal("area_checked_at not set")
	}
}

func TestArcadeAreaIsDerivedOnLink(t *testing.T) {
	app := newArcadeTestApp(t)
	_, u := createAuthUser(t, app)
	seoul, _ := seedArcade(t, app, u.Id, arcadeSeed{Name: "Seoul", Address: "Seoul", Location: location{Lat: 37.551, Lon: 126.849}})
	assertArea(t, app, seoul, "KR-11", "KR-11/서울특별시", "resolved", false)
	yongin, _ := seedArcade(t, app, u.Id, arcadeSeed{Name: "Yongin", Address: "Yongin", Location: location{Lat: 37.280, Lon: 127.115}})
	assertArea(t, app, yongin, "KR-41", "KR-41/용인시", "resolved", false)
	// A coordinate outside the stored country never borrows a foreign unit.
	foreign, _ := seedArcade(t, app, u.Id, arcadeSeed{Name: "Tokyo", Address: "Tokyo", Location: location{Lat: 35.69, Lon: 139.70}})
	assertArea(t, app, foreign, "", "", "unmapped", false)
	macau, _ := seedArcade(t, app, u.Id, arcadeSeed{Name: "Macau", Address: "Macau", Country: "MO", Timezone: "Asia/Macau", Location: location{Lat: 22.198, Lon: 113.543}})
	assertArea(t, app, macau, "MO", "MO/澳門", "resolved", false)
}

func TestBasicEditRecomputesAreaAndIgnoresCityID(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stale  bool
		status int
	}{{"valid", false, 200}, {"stale", true, 409}} {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			var arcadeID, basicID string
			scenario := tests.ApiScenario{Name: tc.name, Method: http.MethodPut, URL: "/arcade/basic", Headers: headers, ExpectedStatus: tc.status, ExpectedContent: []string{`"`}, TestAppFactory: newArcadeTestApp}
			scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				stubGeoLookup(tb)
				token, u := createAuthUser(tb, app)
				headers["Authorization"] = "Bearer " + token
				arcadeID, basicID = seedArcade(tb, app, u.Id, arcadeSeed{Name: "Suwon", Address: "Suwon", Location: location{Lat: 37.304, Lon: 127.010}})
				// Stale stored value without a basic change: only a new revision recomputes.
				a, _ := app.FindRecordById("arcade", arcadeID)
				a.Set(arcadearea.FieldCity, "KR-11/서울특별시")
				if err := app.Save(a); err != nil {
					tb.Fatal(err)
				}
				base := basicID
				if tc.stale {
					base = "stale"
				}
				scenario.Body = strings.NewReader(fmt.Sprintf(`{"arcade":%q,"location":{"lat":37.3045,"lon":127.0105},"city_id":"bogus","base_basic_id":%q}`, arcadeID, base))
			}
			scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
				a, _ := app.FindRecordById("arcade", arcadeID)
				if tc.status != 200 {
					if a.GetString("basic") != basicID {
						tb.Fatal("failed mutation persisted")
					}
					return
				}
				assertArea(tb, app, arcadeID, "KR-41", "KR-41/수원시", "resolved", false)
				b, _ := app.FindRecordById("arcade_basic", a.GetString("basic"))
				if b.GetString("city_id") != "" {
					tb.Fatal("deprecated city_id was written")
				}
				var body struct {
					CityID string `json:"city_id"`
					Area   struct {
						City string `json:"city"`
					} `json:"area"`
				}
				if err := json.NewDecoder(res.Body).Decode(&body); err != nil || body.CityID != "KR-41/수원시" || body.Area.City != body.CityID {
					tb.Fatalf("response area: %+v %v", body, err)
				}
			}
			scenario.Test(t)
		})
	}
}

func TestAdminAreaOverride(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	steps := []struct {
		body             string
		status           int
		admin1, city, st string
		manual           bool
	}{
		{`{"arcade":%q,"manual":true,"city":"JP-13/東京都区部"}`, 400, "KR-41", "KR-41/수원시", "resolved", false},
		{`{"arcade":%q,"manual":true,"admin1":"JP-13"}`, 400, "KR-41", "KR-41/수원시", "resolved", false},
		{`{"arcade":%q,"manual":true,"city":"KR-41/용인시"}`, 200, "KR-41", "KR-41/용인시", "resolved", true},
		{`{"arcade":%q,"manual":false}`, 200, "KR-41", "KR-41/수원시", "resolved", false},
	}
	for i, step := range steps {
		step := step
		scenario := tests.ApiScenario{Name: fmt.Sprint(i), Method: http.MethodPut, URL: "/admin/arcade/area", Headers: headers, ExpectedStatus: step.status, ExpectedContent: []string{`"`}, TestAppFactory: newArcadeTestApp}
		scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			token, u := createAuthUserWithTags(tb, app, []string{"moderator"})
			headers["Authorization"] = "Bearer " + token
			arcadeID, _ = seedArcade(tb, app, u.Id, arcadeSeed{Name: "Suwon", Address: "Suwon", Location: location{Lat: 37.304, Lon: 127.010}})
			if step.manual == false && step.status == 200 {
				a, _ := app.FindRecordById("arcade", arcadeID)
				arcadearea.Apply(a, arcadearea.Assignment{Admin1: "KR-41", City: "KR-41/용인시", Status: "resolved"}, a.GetDateTime("created").Time())
				a.Set(arcadearea.FieldManual, true)
				if err := app.Save(a); err != nil {
					tb.Fatal(err)
				}
			}
			scenario.Body = strings.NewReader(fmt.Sprintf(step.body, arcadeID))
		}
		scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
			assertArea(tb, app, arcadeID, step.admin1, step.city, step.st, step.manual)
		}
		scenario.Test(t)
	}
}

func TestAdminAreaRoutesRequireReviewer(t *testing.T) {
	headers := map[string]string{}
	scenario := tests.ApiScenario{Method: http.MethodGet, URL: "/admin/arcade/areas", Headers: headers, ExpectedStatus: 403, ExpectedContent: []string{`"error"`}, TestAppFactory: newArcadeTestApp}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		token, _ := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
	}
	scenario.Test(t)
}

func TestAdminAreaReviewList(t *testing.T) {
	headers := map[string]string{}
	var unmapped string
	scenario := tests.ApiScenario{Method: http.MethodGet, URL: "/admin/arcade/areas?status=unmapped", Headers: headers, ExpectedStatus: 200, ExpectedContent: []string{`"total":1`}, TestAppFactory: newArcadeTestApp}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		token, u := createAuthUserWithTags(tb, app, []string{"developer"})
		headers["Authorization"] = "Bearer " + token
		seedArcade(tb, app, u.Id, arcadeSeed{Name: "Seoul", Address: "Seoul", Location: location{Lat: 37.551, Lon: 126.849}})
		unmapped, _ = seedArcade(tb, app, u.Id, arcadeSeed{Name: "Sea", Address: "Sea", Location: location{Lat: 33.0, Lon: 124.0}})
	}
	scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
		requireBodyIDs(tb, res, []string{unmapped}, nil)
	}
	scenario.Test(t)
}

func TestListArcadesFiltersByAdmin1(t *testing.T) {
	var seoul, suwon string
	scenario := tests.ApiScenario{Method: http.MethodGet, URL: "/arcades?admin1=KR-41,KR-26", ExpectedStatus: 200, ExpectedContent: []string{`"total":1`, `"admin1":"KR-41"`, `"city":"KR-41/수원시"`}, TestAppFactory: newArcadeTestApp}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		_, u := createAuthUser(tb, app)
		seoul, _ = seedArcade(tb, app, u.Id, arcadeSeed{Name: "Seoul", Address: "Seoul", Location: location{Lat: 37.551, Lon: 126.849}})
		suwon, _ = seedArcade(tb, app, u.Id, arcadeSeed{Name: "Suwon", Address: "Suwon", Location: location{Lat: 37.304, Lon: 127.010}})
		for _, id := range []string{seoul, suwon} {
			a, _ := app.FindRecordById("arcade", id)
			a.Set("public", true)
			if err := app.Save(a); err != nil {
				tb.Fatal(err)
			}
		}
	}
	scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
		requireBodyIDs(tb, res, []string{suwon}, []string{seoul})
	}
	scenario.Test(t)
}

func requireBodyIDs(tb testing.TB, res *http.Response, want, reject []string) {
	tb.Helper()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		tb.Fatal(err)
	}
	for _, id := range want {
		if !strings.Contains(string(raw), id) {
			tb.Fatalf("response missing %s: %s", id, raw)
		}
	}
	for _, id := range reject {
		if strings.Contains(string(raw), id) {
			tb.Fatalf("response unexpectedly has %s: %s", id, raw)
		}
	}
}

func TestAreaPlanBackfillsWithoutTouchingUpdated(t *testing.T) {
	app := newArcadeTestApp(t)
	_, u := createAuthUser(t, app)
	seoul, _ := seedArcade(t, app, u.Id, arcadeSeed{Name: "Seoul", Address: "Seoul", Location: location{Lat: 37.551, Lon: 126.849}})
	pinned, _ := seedArcade(t, app, u.Id, arcadeSeed{Name: "Pinned", Address: "Suwon", Location: location{Lat: 37.304, Lon: 127.010}})
	// Existing databases start empty; the pinned row must survive the backfill.
	if _, err := app.DB().NewQuery("UPDATE arcade SET admin1='', admin2='', admin2_name='', city='', area_status='', area_version=''").Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB().NewQuery("UPDATE arcade SET admin1='KR-41', city='KR-41/용인시', area_status='resolved', area_manual=1 WHERE id={:id}").Bind(map[string]any{"id": pinned}).Execute(); err != nil {
		t.Fatal(err)
	}
	before, _ := app.FindRecordById("arcade", seoul)
	plan, err := arcadearea.Plan(app)
	if err != nil {
		t.Fatal(err)
	}
	// Both rows are written: the pinned one keeps its area and gains the
	// dataset version and address check.
	written, err := arcadearea.ApplyPlan(app, plan, before.GetDateTime("created").Time())
	if err != nil || written != 2 {
		t.Fatalf("written=%d err=%v", written, err)
	}
	assertArea(t, app, seoul, "KR-11", "KR-11/서울특별시", "resolved", false)
	assertArea(t, app, pinned, "KR-41", "KR-41/용인시", "resolved", true)
	after, _ := app.FindRecordById("arcade", seoul)
	if after.GetString("updated") != before.GetString("updated") {
		t.Fatal("backfill bumped arcade.updated")
	}
	again, _ := arcadearea.Plan(app)
	if n, err := arcadearea.ApplyPlan(app, again, time.Time{}); err != nil || n != 0 {
		t.Fatalf("second run wrote %d (%v)", n, err)
	}
}

func TestStaleAreasRecomputeInBatches(t *testing.T) {
	app := newArcadeTestApp(t)
	_, u := createAuthUser(t, app)
	ids := []string{}
	for i := 0; i < 5; i++ {
		id, _ := seedArcade(t, app, u.Id, arcadeSeed{Name: fmt.Sprintf("Seoul %d", i), Address: "서울특별시 강서구", Location: location{Lat: 37.551, Lon: 126.849}})
		ids = append(ids, id)
	}
	if _, err := app.DB().NewQuery("UPDATE arcade SET city='', area_status='', area_version='older'").Execute(); err != nil {
		t.Fatal(err)
	}
	// A plan taken before a concurrent edit must not overwrite that edit.
	stale, err := arcadearea.StalePlan(app, 1)
	if err != nil || len(stale) != 1 {
		t.Fatalf("stale plan %d %v", len(stale), err)
	}
	if _, err := app.DB().NewQuery("UPDATE arcade SET country='JP' WHERE id={:id}").Bind(map[string]any{"id": stale[0].Arcade}).Execute(); err != nil {
		t.Fatal(err)
	}
	if n, err := arcadearea.ApplyPlan(app, stale, time.Now()); err != nil || n != 0 {
		t.Fatalf("guarded apply wrote %d (%v)", n, err)
	}
	if _, err := app.DB().NewQuery("UPDATE arcade SET country='KR' WHERE id={:id}").Bind(map[string]any{"id": stale[0].Arcade}).Execute(); err != nil {
		t.Fatal(err)
	}
	batches := 0
	written, err := arcadearea.RecomputeStale(context.Background(), app, 2, 0, func() { batches++ })
	if err != nil || written != 5 || batches != 3 {
		t.Fatalf("written=%d batches=%d err=%v", written, batches, err)
	}
	for _, id := range ids {
		assertArea(t, app, id, "KR-11", "KR-11/서울특별시", "resolved", false)
	}
	if left, _ := arcadearea.StalePlan(app, 10); len(left) != 0 {
		t.Fatalf("%d rows still stale", len(left))
	}
}

func TestAddressMismatchIsFlaggedForReview(t *testing.T) {
	app := newArcadeTestApp(t)
	_, u := createAuthUser(t, app)
	wrong, _ := seedArcade(t, app, u.Id, arcadeSeed{Name: "Wrong", Address: "서울특별시 강남구 테헤란로 1", Location: location{Lat: 37.304, Lon: 127.010}})
	right, _ := seedArcade(t, app, u.Id, arcadeSeed{Name: "Right", Address: "경기도 수원시 팔달구", Location: location{Lat: 37.304, Lon: 127.010}})
	for id, want := range map[string]bool{wrong: true, right: false} {
		a, _ := app.FindRecordById("arcade", id)
		if got := arcadearea.FromRecord(a); got.Mismatch != want || got.Admin1 != "KR-41" {
			t.Errorf("%s: %+v, want mismatch=%v", id, got, want)
		}
	}
}

func TestAdminAreaReviewListsMismatches(t *testing.T) {
	headers := map[string]string{}
	var wrong, right string
	scenario := tests.ApiScenario{Method: http.MethodGet, URL: "/admin/arcade/areas?status=mismatch", Headers: headers, ExpectedStatus: 200, ExpectedContent: []string{`"total":1`, `"area_mismatch":true`, `"address_admin1":["KR-11"]`}, TestAppFactory: newArcadeTestApp}
	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		token, u := createAuthUserWithTags(tb, app, []string{"developer"})
		headers["Authorization"] = "Bearer " + token
		wrong, _ = seedArcade(tb, app, u.Id, arcadeSeed{Name: "Wrong", Address: "서울특별시 강남구", Location: location{Lat: 37.304, Lon: 127.010}})
		right, _ = seedArcade(tb, app, u.Id, arcadeSeed{Name: "Right", Address: "경기도 수원시", Location: location{Lat: 37.304, Lon: 127.010}})
	}
	scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
		requireBodyIDs(tb, res, []string{wrong}, []string{right})
	}
	scenario.Test(t)
}
