package ranking

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"github.com/ericbaek/musecat-backend-core/service/xp"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

func seedRankingUser(t *testing.T, app core.App, name string, created time.Time, exp int) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(collection)
	user.SetEmail(name + "@example.test")
	user.Set("username", name)
	user.SetPassword("secret123")
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	if _, err := app.DB().NewQuery(`UPDATE user SET created={:created} WHERE id={:id}`).Bind(dbx.Params{
		"id": user.Id, "created": created.UTC().Format(types.DefaultDateLayout),
	}).Execute(); err != nil {
		t.Fatal(err)
	}
	collection, err = app.FindCollectionByNameOrId("user_level")
	if err != nil {
		t.Fatal(err)
	}
	level := core.NewRecord(collection)
	level.Set("user", user.Id)
	level.Set("exp", exp)
	if err := app.Save(level); err != nil {
		t.Fatal(err)
	}
	return user.Id
}

func TestLevelRankingCapBoundaries(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Date(2026, 11, 5, 12, 34, 56, 789000000, time.UTC)
	id := seedRankingUser(t, app, "boundaries", now, xp.LevelBaseExp(300))
	for _, tc := range []struct {
		name string
		age  time.Duration
		want int64
	}{
		{"signup", 0, 10},
		{"future signup", -24 * time.Hour, 10},
		{"first day", 24 * time.Hour, 11},
		{"before day 30", 30*24*time.Hour - time.Millisecond, 48},
		{"day 30", 30 * 24 * time.Hour, 50},
		{"before day 44", 44*24*time.Hour - time.Millisecond, 71},
		{"day 44", 44 * 24 * time.Hour, 73},
		{"before day 45", 45*24*time.Hour - time.Millisecond, 73},
		{"day 45", 45 * 24 * time.Hour, 75},
		{"day 46", 46 * 24 * time.Hour, 75},
		{"before first year", 365*24*time.Hour - time.Millisecond, 99},
		{"first year", 365 * 24 * time.Hour, 100},
		{"second year", 730 * 24 * time.Hour, 190},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := app.DB().NewQuery(`UPDATE user SET created={:created} WHERE id={:id}`).Bind(dbx.Params{"id": id, "created": now.Add(-tc.age).Format(types.DefaultDateLayout)}).Execute()
			if err != nil {
				t.Fatal(err)
			}
			entries, viewer, err := load(app, context.Background(), metricLevel, periodAll, now, id)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Score != tc.want || viewer == nil || viewer.Score != tc.want || viewer.Profile.Level != 300 {
				t.Fatalf("entries=%+v, viewer=%+v; want score=%d, original level=300", entries, viewer, tc.want)
			}
		})
	}
}

func TestLevelRankingMatchesXPThresholds(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Now().UTC()
	id := seedRankingUser(t, app, "thresholds", now.AddDate(-10, 0, 0), 1)
	// Check both sides of every threshold, including the round-to-nearest edges.
	for level := 1; level <= 200; level++ {
		for _, exp := range []int{xp.LevelBaseExp(level) - 1, xp.LevelBaseExp(level), xp.LevelBaseExp(level) + 1} {
			_, err := app.DB().NewQuery(`UPDATE user_level SET exp={:exp} WHERE user={:id}`).Bind(dbx.Params{"id": id, "exp": exp}).Execute()
			if err != nil {
				t.Fatal(err)
			}
			entries, _, err := load(app, context.Background(), metricLevel, periodAll, now, "")
			if err != nil {
				t.Fatal(err)
			}
			want := int64(xp.LevelFromExp(exp))
			if len(entries) != 1 || entries[0].Score != want || int64(entries[0].Profile.Level) != want {
				t.Fatalf("XP=%d: entries=%+v, want level %d", exp, entries, want)
			}
		}
	}
}

func TestLevelRankingOrdersAndTiesByCappedLevel(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Now().UTC()
	for i, tc := range []struct{ ageDays, level int }{{0, 100}, {365, 30}, {365, 30}, {0, 50}} {
		id := seedRankingUser(t, app, fmt.Sprintf("rank%d", i), now.Add(-time.Duration(tc.ageDays)*24*time.Hour), xp.LevelBaseExp(tc.level))
		// Different XP within the same level must not break a tied rank.
		if i == 2 {
			_, err := app.DB().NewQuery(`UPDATE user_level SET exp=exp+1 WHERE user={:id}`).Bind(dbx.Params{"id": id}).Execute()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	entries, _, err := load(app, context.Background(), metricLevel, periodAll, now, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("entries=%+v", entries)
	}
	for i, want := range []struct {
		score       int64
		rank, level int
	}{{30, 1, 30}, {30, 1, 30}, {10, 3, 100}, {10, 3, 50}} {
		got := entries[i]
		if got.Score != want.score || got.Rank != want.rank || got.Profile.Level != want.level {
			t.Fatalf("entry %d: score=%d rank=%d level=%d; want %+v", i, got.Score, got.Rank, got.Profile.Level, want)
		}
	}
}

func TestXPRankingReturnsActualPeriodLevelGain(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	id := seedRankingUser(t, app, "growth", now.AddDate(-2, 0, 0), 1000)
	collection, err := app.FindCollectionByNameOrId("user_level_log")
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct{ days, previous, current int }{{400, 0, 100}, {200, 100, 300}, {60, 300, 500}, {10, 500, 800}, {1, 800, 1000}} {
		log := core.NewRecord(collection)
		log.Set("user", id)
		log.Set("kind", fmt.Sprintf("xp:period-growth:%d", i))
		log.Set("previous_exp", tc.previous)
		log.Set("new_exp", tc.current)
		log.Set("diff_exp", tc.current-tc.previous)
		if err := app.Save(log); err != nil {
			t.Fatal(err)
		}
		_, err := app.DB().NewQuery(`UPDATE user_level_log SET created={:created} WHERE id={:id}`).Bind(dbx.Params{"id": log.Id, "created": now.Add(-time.Duration(tc.days) * 24 * time.Hour).Format(types.DefaultDateLayout)}).Execute()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		period period
		xp     int64
		gain   int
	}{{periodWeek, 200, 7}, {periodMonth, 500, 20}, {periodHalfYear, 700, 30}, {periodYear, 900, 46}, {periodAll, 1000, 60}} {
		t.Run(string(tc.period), func(t *testing.T) {
			entries, viewer, err := load(app, context.Background(), metricXP, tc.period, now, id)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || viewer == nil {
				t.Fatalf("missing entries/viewer: %+v %+v", entries, viewer)
			}
			for _, got := range []entry{entries[0], *viewer} {
				stats, ok := got.Stats.(*xpRankingStats)
				if !ok || stats.LevelChange != tc.gain || got.Score != tc.xp || got.Profile.Level != 60 {
					t.Fatalf("score=%d profile.level=%d stats=%+v; want XP=%d, gain=%d, original level=60", got.Score, got.Profile.Level, got.Stats, tc.xp, tc.gain)
				}
			}
		})
	}
}

func TestXPRankingUsesNetXPAndAllowsZeroLevelGain(t *testing.T) {
	app := testutil.NewTestApp(t)
	now := time.Now().UTC()
	collection, err := app.FindCollectionByNameOrId("user_level_log")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name          string
		current, gain int
		changes       [][2]int
	}{
		{"no-level-up", 1000, 0, [][2]int{{999, 1000}}},
		{"net-reversal", 900, 4, [][2]int{{800, 1000}, {1000, 900}}},
	} {
		id := seedRankingUser(t, app, tc.name, now.AddDate(-1, 0, 0), tc.current)
		for i, change := range tc.changes {
			log := core.NewRecord(collection)
			log.Set("user", id)
			log.Set("kind", fmt.Sprintf("xp:net:%d", i))
			log.Set("previous_exp", change[0])
			log.Set("new_exp", change[1])
			log.Set("diff_exp", change[1]-change[0])
			if err := app.Save(log); err != nil {
				t.Fatal(err)
			}
		}
		_, viewer, err := load(app, context.Background(), metricXP, periodWeek, now, id)
		if err != nil {
			t.Fatal(err)
		}
		if viewer == nil {
			t.Fatal("missing XP viewer")
		}
		stats, ok := viewer.Stats.(*xpRankingStats)
		if !ok || stats.LevelChange != tc.gain {
			t.Fatalf("stats=%+v; want gain=%d", viewer.Stats, tc.gain)
		}
	}
}
