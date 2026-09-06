package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/tests"
	pbtypes "github.com/pocketbase/pocketbase/tools/types"
)

func TestListPhotoCampaignTargetsMissingAndStale(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)

	missingID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Missing Photos", Address: "A", Nickname: []string{"missing"}, Location: location{Lat: 37.5, Lon: 127.0}})
	setArcadeVisibility(t, app, missingID, true, false)

	staleID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Stale Photos", Address: "B", Nickname: []string{"stale"}, Location: location{Lat: 37.51, Lon: 127.01}})
	setArcadeVisibility(t, app, staleID, true, false)
	staleAtomID := seedPhotoAtom(t, app, staleID, user.Id, true)
	setPhotoAtomCreated(t, app, staleAtomID, time.Now().UTC().AddDate(0, -6, 0).Add(-time.Minute))
	seedPhotoMolecule(t, app, staleID, user.Id, []string{staleAtomID})

	currentID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Current Photos", Address: "C", Nickname: []string{"current"}, Location: location{Lat: 37.52, Lon: 127.02}})
	setArcadeVisibility(t, app, currentID, true, false)
	currentAtomID := seedPhotoAtom(t, app, currentID, user.Id, true)
	seedPhotoMolecule(t, app, currentID, user.Id, []string{currentAtomID})

	closedID, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Closed Photos", Address: "D", Nickname: []string{"closed"}, Location: location{Lat: 37.53, Lon: 127.03}})
	setArcadeVisibility(t, app, closedID, true, true)

	res := executeJSONRequest(t, app, http.MethodGet, "/campaign/photo?per_page=10", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.StatusCode)
	}
	defer res.Body.Close()
	var payload struct {
		Total int `json:"total"`
		Items []struct {
			Arcade struct {
				ID string `json:"id"`
			} `json:"arcade"`
			Status string `json:"photo_status"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Total != 2 || len(payload.Items) != 2 {
		t.Fatalf("expected missing and stale targets, got total=%d items=%d payload=%#v", payload.Total, len(payload.Items), payload)
	}
	if payload.Items[0].Arcade.ID != missingID || payload.Items[0].Status != "missing" {
		t.Fatalf("expected missing target first, got %#v", payload.Items[0])
	}
	if payload.Items[1].Arcade.ID != staleID || payload.Items[1].Status != "stale" {
		t.Fatalf("expected stale target second, got %#v", payload.Items[1])
	}
}

func TestListPhotoCampaignTargetsNearestToRequestedLocation(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)

	farID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Far Photos",
		Address:  "Far",
		Location: location{Lat: 37.7, Lon: 127.2},
	})
	setArcadeVisibility(t, app, farID, true, false)
	nearID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Near Photos",
		Address:  "Near",
		Location: location{Lat: 37.5, Lon: 127.0},
	})
	setArcadeVisibility(t, app, nearID, true, false)

	res := executeJSONRequest(t, app, http.MethodGet, "/campaign/photo?per_page=10&lat=37.5&lon=127", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", res.StatusCode)
	}
	defer res.Body.Close()
	var payload struct {
		Items []struct {
			Arcade struct {
				ID string `json:"id"`
			} `json:"arcade"`
		} `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Items) != 2 || payload.Items[0].Arcade.ID != nearID {
		t.Fatalf("expected nearest target first, got %#v", payload.Items)
	}
}

func setPhotoAtomCreated(tb testing.TB, app *tests.TestApp, atomID string, at time.Time) {
	tb.Helper()
	atom, err := app.FindRecordById("arcade_photo_atoms", atomID)
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := app.NonconcurrentDB().NewQuery("UPDATE arcade_photo_atoms SET created={:created} WHERE id={:id}").Bind(dbx.Params{
		"created": at.UTC().Format(pbtypes.DefaultDateLayout),
		"id":      atom.Id,
	}).Execute(); err != nil {
		tb.Fatal(err)
	}
}
