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
	if payload.Arcade != arcadeID {
		t.Fatalf("expected arcade %s, got %s", arcadeID, payload.Arcade)
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
