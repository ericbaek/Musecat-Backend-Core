package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"
)

func TestRankings_MetricsAndVisibility(t *testing.T) {
	app := newArcadeTestApp(t)

	_, explorer := createAuthUser(t, app)
	_, privateVisitor := createAuthUser(t, app)
	_, photographer := createAuthUser(t, app)
	_, withdrawn := createAuthUser(t, app)
	setVisitVisibility(t, app, explorer.Id, "summary")
	withdrawn.Set("withdrawn", true)
	if err := app.Save(withdrawn); err != nil {
		t.Fatalf("failed to withdraw user: %v", err)
	}
	setVisitVisibility(t, app, privateVisitor.Id, "private")

	arcadeOne, _ := seedPublicArcade(t, app, explorer.Id, arcadeSeed{Name: "Ranking One", Address: "1 Rank St", Location: location{Lat: 37.5665, Lon: 126.978}})
	arcadeTwo, _ := seedPublicArcade(t, app, explorer.Id, arcadeSeed{Name: "Ranking Two", Address: "2 Rank St", Location: location{Lat: 35.1796, Lon: 129.0756}})
	now := time.Now().UTC()
	seedArcadeVisit(t, app, explorer.Id, arcadeOne, now.Add(-time.Hour))
	seedArcadeVisit(t, app, explorer.Id, arcadeTwo, now.Add(-2*time.Hour))
	seedArcadeVisit(t, app, explorer.Id, arcadeOne, now.Add(-48*time.Hour))
	seedArcadeVisit(t, app, privateVisitor.Id, arcadeOne, now.Add(-time.Hour))
	seedArcadeVisit(t, app, privateVisitor.Id, arcadeTwo, now.Add(-2*time.Hour))
	seedArcadeVisit(t, app, privateVisitor.Id, arcadeOne, now.Add(-48*time.Hour))
	setArcadeVisitGainedExp(t, app, explorer.Id, arcadeOne, now.Add(-time.Hour), 1)
	setArcadeVisitGainedExp(t, app, explorer.Id, arcadeTwo, now.Add(-2*time.Hour), 10)
	setArcadeVisitGainedExp(t, app, explorer.Id, arcadeOne, now.Add(-48*time.Hour), 1)
	setArcadeVisitGainedExp(t, app, privateVisitor.Id, arcadeOne, now.Add(-time.Hour), 1)
	setArcadeVisitGainedExp(t, app, privateVisitor.Id, arcadeTwo, now.Add(-2*time.Hour), 10)
	setArcadeVisitGainedExp(t, app, privateVisitor.Id, arcadeOne, now.Add(-48*time.Hour), 1)

	seedSupporterLedgerEntry(t, app, explorer.Id, "xp:rank-positive", 0, 15, now.Add(-time.Hour))
	seedSupporterLedgerEntry(t, app, explorer.Id, "xp:rank-reversal", 15, 10, now.Add(-30*time.Minute))
	seedSupporterLedgerEntry(t, app, withdrawn.Id, "xp:rank-withdrawn", 0, 50, now.Add(-time.Hour))
	seedUserLevelExp(t, app, explorer.Id, 40)
	seedUserLevelExp(t, app, photographer.Id, 120)
	seedUserLevelExp(t, app, withdrawn.Id, 999)

	publicPhoto := seedPhotoAtom(t, app, arcadeOne, photographer.Id, true)
	setRecordCreated(t, app, "arcade_photo_atoms", publicPhoto, now.Add(-time.Hour))
	privatePhoto := seedPhotoAtom(t, app, arcadeOne, photographer.Id, false)
	setRecordCreated(t, app, "arcade_photo_atoms", privatePhoto, now.Add(-time.Hour))
	oldPhoto := seedPhotoAtom(t, app, arcadeOne, photographer.Id, true)
	setRecordCreated(t, app, "arcade_photo_atoms", oldPhoto, now.Add(-8*24*time.Hour))

	assertRankingTop(t, app, "/rankings?metric=explorer&period=week", explorer.Id, 2)
	assertRankingTop(t, app, "/rankings?metric=visits&period=week", explorer.Id, 3)
	assertRankingTop(t, app, "/rankings?metric=xp&period=week", explorer.Id, 10)
	assertRankingTop(t, app, "/rankings?metric=level&period=all", photographer.Id, 16)
	assertRankingTop(t, app, "/rankings?metric=photographer&period=week", photographer.Id, 1)
	assertArcadeRankingTop(t, app, "/rankings?metric=arcade_visits&period=week", arcadeTwo, 20, 2)
	assertExplorerDistance(t, app, "/rankings?metric=explorer&period=week", explorer.Id)

	res := executeJSONRequest(t, app, http.MethodGet, "/rankings?metric=level&period=week", "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid level period to return 400, got %d", res.StatusCode)
	}
}

func assertArcadeRankingTop(t *testing.T, app *tests.TestApp, url, arcadeID string, score int64, visitCount int64) {
	t.Helper()
	res := executeJSONRequest(t, app, http.MethodGet, url, "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s: expected 200, got %d", url, res.StatusCode)
	}
	var payload struct {
		Entries []struct {
			Score int64 `json:"score"`
			Stats struct {
				VisitCount int64 `json:"visit_count"`
			} `json:"stats"`
			Arcade struct {
				ID string `json:"id"`
			} `json:"arcade"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("%s: decode response: %v", url, err)
	}
	if len(payload.Entries) == 0 || payload.Entries[0].Arcade.ID != arcadeID || payload.Entries[0].Score != score || payload.Entries[0].Stats.VisitCount != visitCount {
		t.Fatalf("%s: unexpected top entry: %#v", url, payload.Entries)
	}
}

func setArcadeVisitGainedExp(t *testing.T, app *tests.TestApp, userID, arcadeID string, ts time.Time, exp int64) {
	t.Helper()
	result, err := app.NonconcurrentDB().NewQuery(`
UPDATE arcade_visit
SET gained_exp={:exp}
WHERE user={:user} AND arcade={:arcade} AND visit_day={:visit_day}
`).Bind(dbx.Params{
		"exp":       exp,
		"user":      userID,
		"arcade":    arcadeID,
		"visit_day": ts.Format("2006-01-02"),
	}).Execute()
	if err != nil {
		t.Fatalf("failed to set visit XP: %v", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("failed to inspect visit XP update: %v", err)
	}
	if affected == 0 {
		t.Fatalf("no visit matched XP update for %s/%s at %s", userID, arcadeID, ts.Format(time.RFC3339Nano))
	}
}

func assertExplorerDistance(t *testing.T, app *tests.TestApp, url, userID string) {
	t.Helper()
	res := executeJSONRequest(t, app, http.MethodGet, url, "", nil)
	defer res.Body.Close()
	var payload struct {
		Entries []struct {
			Profile struct {
				ID string `json:"id"`
			} `json:"profile"`
			Stats struct {
				TravelDistanceKm int64 `json:"travel_distance_km"`
			} `json:"stats"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("%s: decode response: %v", url, err)
	}
	for _, entry := range payload.Entries {
		if entry.Profile.ID == userID {
			if entry.Stats.TravelDistanceKm <= 0 {
				t.Fatalf("%s: expected explorer distance, got %v", url, entry.Stats.TravelDistanceKm)
			}
			return
		}
	}
	t.Fatalf("%s: explorer %s not found", url, userID)
}

func TestRankings_ReturnsAuthenticatedViewerRankOutsideLeaderboard(t *testing.T) {
	app := newArcadeTestApp(t)

	viewerToken, viewer := createAuthUser(t, app)
	seedUserLevelExp(t, app, viewer.Id, 1)
	for index := 0; index < 101; index++ {
		_, higherRankedUser := createAuthUser(t, app)
		seedUserLevelExp(t, app, higherRankedUser.Id, 1_000+index)
	}

	guest := executeJSONRequest(t, app, http.MethodGet, "/rankings?metric=level&period=all", "", nil)
	defer guest.Body.Close()
	var guestPayload struct {
		Viewer *struct {
			Rank int `json:"rank"`
		} `json:"viewer"`
	}
	if err := json.NewDecoder(guest.Body).Decode(&guestPayload); err != nil {
		t.Fatalf("decode guest ranking response: %v", err)
	}
	if guestPayload.Viewer != nil {
		t.Fatalf("guest response must not include a viewer entry: %#v", guestPayload.Viewer)
	}

	response := executeJSONRequest(t, app, http.MethodGet, "/rankings?metric=level&period=all", "", map[string]string{
		"Authorization": "Bearer " + viewerToken,
	})
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", response.StatusCode)
	}
	var payload struct {
		Entries []struct {
			Profile struct {
				ID string `json:"id"`
			} `json:"profile"`
		} `json:"entries"`
		Viewer *struct {
			Rank    int `json:"rank"`
			Profile struct {
				ID string `json:"id"`
			} `json:"profile"`
		} `json:"viewer"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("decode authenticated ranking response: %v", err)
	}
	if len(payload.Entries) != 100 {
		t.Fatalf("expected public leaderboard to remain capped at 100 entries, got %d", len(payload.Entries))
	}
	for _, item := range payload.Entries {
		if item.Profile.ID == viewer.Id {
			t.Fatal("viewer must not be inserted into the top-100 leaderboard")
		}
	}
	if payload.Viewer == nil || payload.Viewer.Profile.ID != viewer.Id || payload.Viewer.Rank != 102 {
		t.Fatalf("unexpected viewer entry: %#v", payload.Viewer)
	}
}

func assertRankingTop(t *testing.T, app *tests.TestApp, url, userID string, score int64) {
	t.Helper()
	res := executeJSONRequest(t, app, http.MethodGet, url, "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("%s: expected 200, got %d", url, res.StatusCode)
	}
	var payload struct {
		Entries []struct {
			Rank    int   `json:"rank"`
			Score   int64 `json:"score"`
			Profile struct {
				ID string `json:"id"`
			} `json:"profile"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("%s: decode response: %v", url, err)
	}
	if len(payload.Entries) == 0 {
		t.Fatalf("%s: expected ranking entries", url)
	}
	first := payload.Entries[0]
	if first.Rank != 1 || first.Profile.ID != userID || first.Score != score {
		t.Fatalf("%s: unexpected top entry: %#v", url, first)
	}
}

func setRecordCreated(t *testing.T, app *tests.TestApp, table, id string, ts time.Time) {
	t.Helper()
	if _, err := app.NonconcurrentDB().NewQuery("UPDATE " + table + " SET created={:created} WHERE id={:id}").Bind(dbx.Params{
		"created": ts.UTC().Format(types.DefaultDateLayout),
		"id":      id,
	}).Execute(); err != nil {
		t.Fatalf("failed to set %s.created: %v", table, err)
	}
}
