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

	arcadeOne, _ := seedArcade(t, app, explorer.Id, arcadeSeed{Name: "Ranking One", Address: "1 Rank St", Location: location{Lat: 37.5665, Lon: 126.978}})
	arcadeTwo, _ := seedArcade(t, app, explorer.Id, arcadeSeed{Name: "Ranking Two", Address: "2 Rank St", Location: location{Lat: 37.5666, Lon: 126.9781}})
	now := time.Now().UTC()
	seedArcadeVisit(t, app, explorer.Id, arcadeOne, now.Add(-time.Hour))
	seedArcadeVisit(t, app, explorer.Id, arcadeTwo, now.Add(-2*time.Hour))
	seedArcadeVisit(t, app, explorer.Id, arcadeOne, now.Add(-48*time.Hour))
	seedArcadeVisit(t, app, privateVisitor.Id, arcadeOne, now.Add(-time.Hour))
	seedArcadeVisit(t, app, privateVisitor.Id, arcadeTwo, now.Add(-2*time.Hour))
	seedArcadeVisit(t, app, privateVisitor.Id, arcadeOne, now.Add(-48*time.Hour))

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

	res := executeJSONRequest(t, app, http.MethodGet, "/rankings?metric=level&period=week", "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid level period to return 400, got %d", res.StatusCode)
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
