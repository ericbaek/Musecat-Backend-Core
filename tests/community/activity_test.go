package community_test

import (
	"testing"
	"time"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/dbx"
)

func TestAchievementActivityCountsStoredRowsIndependentOfXPCapAndTimezone(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, owner := createCommunityUser(t, app)
	_, other := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	ids := []string{}
	for range 10 {
		r := achievementRequest(t, app, "POST", "", token, map[string]string{"flair": "achievement", "game_series": series.Id}, 1)
		if r.StatusCode != 201 {
			t.Fatal(r.StatusCode, decodeBody(t, r))
		}
		ids = append(ids, decodeBody(t, r)["id"].(string))
	}
	// Existing rows before this release contribute at their original creation day,
	// not XP ledger day or update day. SQL fixtures avoid filesystem requirements.
	created := "2026-01-02 13:30:00.000Z" // Jan 3 in Sydney, Jan 2 in UTC.
	for _, id := range ids {
		_, err := app.DB().NewQuery("UPDATE community_post SET created={:created} WHERE id={:id}").Bind(dbx.Params{"created": created, "id": id}).Execute()
		if err != nil {
			t.Fatal(err)
		}
	}
	for query, id := range map[string]string{
		"UPDATE community_post SET status='deleted' WHERE id={:id}": ids[7],
		"UPDATE community_post SET flair='tip' WHERE id={:id}":      ids[8],
		"UPDATE community_post SET photos='[]' WHERE id={:id}":      ids[9],
	} {
		if _, err := app.DB().NewQuery(query).Bind(dbx.Params{"id": id}).Execute(); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 1, 3, 12, 0, 0, 0, time.UTC)
	for _, zone := range []string{"UTC", "Australia/Sydney"} {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		result, err := userhandler.BuildUserActivity(app, &userhandler.Profile{ID: owner.Id}, loc, zone, 2, now)
		if err != nil {
			t.Fatal(err)
		}
		if result.Totals.AchievementCount != 7 || result.Totals.TotalCount != 7 || result.Totals.MaxDailyCount != 7 {
			t.Fatalf("counts %+v", result.Totals)
		}
		target := "2026-01-02"
		if zone == "Australia/Sydney" {
			target = "2026-01-03"
		}
		for _, day := range result.Days {
			want := 0
			if day.Date == target {
				want = 7
			}
			if day.AchievementCount != want || day.TotalCount != want {
				t.Fatalf("%s %+v", zone, day)
			}
		}
	}
	result, err := userhandler.BuildUserActivity(app, &userhandler.Profile{ID: other.Id}, time.UTC, "UTC", 2, now)
	if err != nil || result.Totals.AchievementCount != 0 {
		t.Fatalf("other author: %+v %v", result, err)
	}
}
