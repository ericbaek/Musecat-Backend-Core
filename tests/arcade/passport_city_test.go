package arcade_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	arcadebasic "github.com/ericbaek/musecat-backend-core/handlers/arcade/basic"
)

func TestBasicCityChangeValidatesAndRecordsHistory(t *testing.T) {
	for _, tc := range []struct {
		name, country string
		stale         bool
		status        int
	}{{"valid", "KR", false, 200}, {"foreign", "JP", false, 400}, {"stale", "KR", true, 409}} {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			var arcadeID, basicID, cityID string
			scenario := tests.ApiScenario{Name: tc.name, Method: http.MethodPut, URL: "/arcade/basic", Headers: headers, ExpectedStatus: tc.status, ExpectedContent: []string{`"`}, TestAppFactory: newArcadeTestApp}
			scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				stubGeoLookup(tb)
				token, u := createAuthUser(tb, app)
				headers["Authorization"] = "Bearer " + token
				arcadeID, _ = seedArcade(tb, app, u.Id, arcadeSeed{Name: "City arcade", Address: "Seoul", Location: location{Lat: 37.5665, Lon: 126.978}})
				a, _ := app.FindRecordById("arcade", arcadeID)
				basicID = a.GetString("basic")
				coll, _ := app.FindCollectionByNameOrId("passport_city")
				c := core.NewRecord(coll)
				c.Set("source_id", "1835848")
				c.Set("name", "Seoul")
				c.Set("country", tc.country)
				c.Set("admin1", "11")
				if err := app.Save(c); err != nil {
					tb.Fatal(err)
				}
				cityID = c.Id
				base := basicID
				if tc.stale {
					base = "stale"
				}
				scenario.Body = strings.NewReader(fmt.Sprintf(`{"arcade":%q,"city_id":%q,"base_basic_id":%q}`, arcadeID, cityID, base))
			}
			scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, _ *http.Response) {
				a, _ := app.FindRecordById("arcade", arcadeID)
				b, _ := app.FindRecordById("arcade_basic", a.GetString("basic"))
				if tc.status == 200 {
					if b.GetString("city_id") != cityID || b.Id == basicID {
						tb.Fatal("city revision not selected")
					}
					records, err := app.FindRecordsByFilter("arcade_changelog", "arcade='"+arcadeID+"'", "-created", 0, 0)
					if err != nil {
						tb.Fatal(err)
					}
					found := false
					for _, r := range records {
						if strings.Contains(r.GetString("log"), "city_id") {
							found = true
						}
					}
					if !found {
						tb.Fatal("city history missing")
					}
				} else if b.Id != basicID {
					tb.Fatal("failed mutation persisted")
				}
			}
			scenario.Test(t)
		})
	}
}

func TestApplyCityAssignmentIsIdempotentAndSkipsXP(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	arcadeID, basicID := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Backfill arcade",
		Address:  "Sydney",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	coll, err := app.FindCollectionByNameOrId("passport_city")
	if err != nil {
		t.Fatal(err)
	}
	city := core.NewRecord(coll)
	city.Set("source_id", "2147714")
	city.Set("name", "Sydney")
	city.Set("country", "KR")
	city.Set("admin1", "11")
	if err := app.Save(city); err != nil {
		t.Fatal(err)
	}
	if err := arcadebasic.ApplyCityAssignment(app, arcadeID, basicID, city.Id, user.Id); err != nil {
		t.Fatal(err)
	}
	first, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatal(err)
	}
	firstBasic := first.GetString("basic")
	if firstBasic == basicID {
		t.Fatal("expected a new basic revision")
	}
	if err := arcadebasic.ApplyCityAssignment(app, arcadeID, basicID, city.Id, user.Id); err != nil {
		t.Fatalf("reapplying manifest should be a no-op: %v", err)
	}
	second, _ := app.FindRecordById("arcade", arcadeID)
	if second.GetString("basic") != firstBasic {
		t.Fatal("idempotent apply created another revision")
	}
	changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:arcade} && changed='basic'", "", 0, 0, map[string]any{"arcade": arcadeID})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("expected one correction changelog, got %d", len(changes))
	}
}
