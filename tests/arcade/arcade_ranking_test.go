package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

func TestArcadeContributionRanking_CombinesVisitsAndEdits(t *testing.T) {
	app := newArcadeTestApp(t)

	_, creator := createAuthUser(t, app)
	_, combined := createAuthUser(t, app)
	_, visitor := createAuthUser(t, app)
	_, editor := createAuthUser(t, app)
	_, privateVisitor := createAuthUser(t, app)
	setVisitVisibility(t, app, privateVisitor.Id, "private")

	arcadeID, _ := seedPublicArcade(t, app, creator.Id, arcadeSeed{
		Name:     "Contribution Ranking Arcade",
		Address:  "1 Ranking St",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	now := time.Now().UTC()

	seedArcadeVisit(t, app, combined.Id, arcadeID, now.Add(-time.Hour))
	setArcadeVisitGainedExp(t, app, combined.Id, arcadeID, now.Add(-time.Hour), 5)
	seedSupporterLedgerEntry(t, app, combined.Id, userhandler.ArcadeEditKind(arcadeID, "basic"), 0, 8, now.Add(-2*time.Hour))
	seedSupporterLedgerEntry(t, app, combined.Id, userhandler.ArcadeEditKind(arcadeID, "game"), 8, 12, now.Add(-time.Minute))
	seedSupporterLedgerEntry(t, app, combined.Id, userhandler.ArcadeEditKind(arcadeID, "memo"), 12, 7, now)

	seedArcadeVisit(t, app, visitor.Id, arcadeID, now.Add(-2*time.Hour))
	setArcadeVisitGainedExp(t, app, visitor.Id, arcadeID, now.Add(-2*time.Hour), 15)
	seedSupporterLedgerEntry(t, app, editor.Id, userhandler.ArcadeEditKind(arcadeID, "gtk"), 0, 12, now.Add(-3*time.Hour))

	seedArcadeVisit(t, app, privateVisitor.Id, arcadeID, now.Add(-3*time.Hour))
	setArcadeVisitGainedExp(t, app, privateVisitor.Id, arcadeID, now.Add(-3*time.Hour), 50)
	seedSupporterLedgerEntry(t, app, privateVisitor.Id, userhandler.ArcadeEditKind(arcadeID, "memo"), 0, 2, now.Add(-4*time.Hour))

	for index, score := range []int{11, 10, 9, 8} {
		_, filler := createAuthUser(t, app)
		seedSupporterLedgerEntry(t, app, filler.Id, userhandler.ArcadeEditKind(arcadeID, "hour"), 0, score, now.Add(time.Duration(index)*time.Minute))
	}

	res := executeJSONRequest(t, app, http.MethodGet, "/arcade/ranking?arcade="+arcadeID, "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var payload struct {
		Arcade  string `json:"arcade"`
		Metric  string `json:"metric"`
		Entries []struct {
			Rank    int   `json:"rank"`
			Score   int64 `json:"score"`
			Profile struct {
				ID string `json:"id"`
			} `json:"profile"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode arcade ranking response: %v", err)
	}
	if payload.Arcade != arcadeID || payload.Metric != "total" {
		t.Fatalf("unexpected ranking scope: arcade=%s metric=%s", payload.Arcade, payload.Metric)
	}
	if len(payload.Entries) != 5 {
		t.Fatalf("expected five entries, got %d", len(payload.Entries))
	}
	want := []struct {
		id    string
		score int64
	}{
		{combined.Id, 17},
		{visitor.Id, 15},
		{editor.Id, 12},
	}
	for index, expected := range want {
		got := payload.Entries[index]
		if got.Profile.ID != expected.id || got.Score != expected.score || got.Rank != index+1 {
			t.Fatalf("unexpected entry at %d: %#v", index, got)
		}
	}
	for _, item := range payload.Entries {
		if item.Profile.ID == privateVisitor.Id {
			t.Fatal("private visit XP must not be included in the public ranking")
		}
	}

	for _, tc := range []struct {
		metric    string
		wantID    string
		wantScore int64
		wantCount int
	}{
		{"total", combined.Id, 17, 5},
		{"edit", "", 12, 5},
		{"passport", visitor.Id, 15, 2},
	} {
		res := executeJSONRequest(t, app, http.MethodGet, "/arcade/ranking?arcade="+arcadeID+"&metric="+tc.metric, "", nil)
		var ranked struct {
			Metric  string `json:"metric"`
			Entries []struct {
				Rank    int   `json:"rank"`
				Score   int64 `json:"score"`
				Profile struct {
					ID string `json:"id"`
				} `json:"profile"`
			} `json:"entries"`
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			t.Fatalf("%s ranking status: %d", tc.metric, res.StatusCode)
		}
		if err := json.NewDecoder(res.Body).Decode(&ranked); err != nil {
			res.Body.Close()
			t.Fatalf("decode %s ranking: %v", tc.metric, err)
		}
		res.Body.Close()
		if ranked.Metric != tc.metric || len(ranked.Entries) != tc.wantCount || (tc.wantID != "" && ranked.Entries[0].Profile.ID != tc.wantID) || ranked.Entries[0].Score != tc.wantScore {
			t.Fatalf("unexpected %s ranking: %#v", tc.metric, ranked)
		}
		if tc.metric == "edit" {
			found := map[string]bool{}
			for _, item := range ranked.Entries {
				if item.Score == 12 && item.Rank == 1 {
					found[item.Profile.ID] = true
				}
			}
			if !found[combined.Id] || !found[editor.Id] {
				t.Fatalf("edit ranking missing tied editors: %#v", ranked.Entries)
			}
		}
	}

	invalidRes := executeJSONRequest(t, app, http.MethodGet, "/arcade/ranking?arcade="+arcadeID+"&metric=visits", "", nil)
	defer invalidRes.Body.Close()
	if invalidRes.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid metric to return 400, got %d", invalidRes.StatusCode)
	}

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatalf("load arcade for closed visibility check: %v", err)
	}
	arcade.Set("closed", true)
	if err := app.Save(arcade); err != nil {
		t.Fatalf("close arcade for visibility check: %v", err)
	}
	closedRes := executeJSONRequest(t, app, http.MethodGet, "/arcade/ranking?arcade="+arcadeID, "", nil)
	defer closedRes.Body.Close()
	if closedRes.StatusCode != http.StatusOK {
		t.Fatalf("expected public closed arcade to return 200, got %d", closedRes.StatusCode)
	}
}

func TestArcadeContributionRanking_HidesPrivateArcades(t *testing.T) {
	app := newArcadeTestApp(t)

	_, creator := createAuthUser(t, app)
	arcadeID, _ := seedArcade(t, app, creator.Id, arcadeSeed{
		Name:     "Private Ranking Arcade",
		Address:  "2 Ranking St",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})

	res := executeJSONRequest(t, app, http.MethodGet, "/arcade/ranking?arcade="+arcadeID, "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expected private arcade to return 404, got %d", res.StatusCode)
	}
}
