package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"
)

func TestListArcadeUpdates_DefaultLimitAndGrouping(t *testing.T) {
	app := newArcadeTestApp(t)

	_, user1 := createAuthUser(t, app)
	_, user2 := createAuthUser(t, app)

	base := time.Date(2030, 3, 24, 20, 20, 15, 0, time.UTC)

	arcadeA, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "Arcade A",
		Address:  "Address A",
		Location: location{Lat: 37.1, Lon: 127.1},
		Country:  "KR",
	})
	arcadeB, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "Arcade B",
		Address:  "Address B",
		Location: location{Lat: 37.2, Lon: 127.2},
		Country:  "KR",
	})
	arcadeC, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "Arcade C",
		Address:  "Address C",
		Location: location{Lat: 35.2, Lon: 129.2},
		Country:  "JP",
	})
	arcadeD, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "Arcade D",
		Address:  "Address D",
		Location: location{Lat: 36.2, Lon: 128.2},
		Country:  "KR",
	})
	arcadeE, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "Arcade E",
		Address:  "Address E",
		Location: location{Lat: 36.3, Lon: 128.3},
		Country:  "KR",
	})
	arcadeF, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "Arcade F",
		Address:  "Address F",
		Location: location{Lat: 36.4, Lon: 128.4},
		Country:  "KR",
	})
	arcadeG, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "Arcade G",
		Address:  "Address G",
		Location: location{Lat: 36.5, Lon: 128.5},
		Country:  "KR",
	})
	arcadePrivate, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "Arcade Private",
		Address:  "Address P",
		Location: location{Lat: 35.1, Lon: 127.1},
		Country:  "KR",
	})
	arcadeClosed, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "Arcade Closed",
		Address:  "Address X",
		Location: location{Lat: 35.0, Lon: 127.0},
		Country:  "KR",
	})

	setArcadeVisibilityAndUpdated(t, app, arcadeA, true, false, base)
	setArcadeVisibilityAndUpdated(t, app, arcadeB, true, false, base.Add(-4*time.Minute))
	setArcadeVisibilityAndUpdated(t, app, arcadeC, true, false, base.Add(-5*time.Minute))
	setArcadeVisibilityAndUpdated(t, app, arcadeD, true, false, base.Add(-6*time.Minute))
	setArcadeVisibilityAndUpdated(t, app, arcadeE, true, false, base.Add(-7*time.Minute))
	setArcadeVisibilityAndUpdated(t, app, arcadeF, true, false, base.Add(-9*time.Minute))
	setArcadeVisibilityAndUpdated(t, app, arcadeG, true, false, base.Add(-10*time.Minute))
	setArcadeVisibilityAndUpdated(t, app, arcadePrivate, false, false, base.Add(-12*time.Minute))
	setArcadeVisibilityAndUpdated(t, app, arcadeClosed, true, true, base.Add(-13*time.Minute))

	seedArcadeChangelog(t, app, arcadeA, "photo", user1.Id, base)
	seedArcadeChangelog(t, app, arcadeA, "game", user1.Id, base.Add(-1*time.Minute))
	seedArcadeChangelog(t, app, arcadeA, "game", user1.Id, base.Add(-2*time.Minute))
	seedArcadeChangelog(t, app, arcadeA, "hour", user2.Id, base.Add(-3*time.Minute))
	seedArcadeChangelog(t, app, arcadeB, "sns", user2.Id, base.Add(-4*time.Minute))
	seedArcadeChangelog(t, app, arcadeC, "gtk", user1.Id, base.Add(-5*time.Minute))
	seedArcadeChangelog(t, app, arcadeD, "basic", user1.Id, base.Add(-6*time.Minute))
	seedArcadeChangelog(t, app, arcadeE, "photo", user2.Id, base.Add(-7*time.Minute))
	seedArcadeChangelog(t, app, arcadeE, "game", user1.Id, base.Add(-8*time.Minute))
	seedArcadeChangelog(t, app, arcadeF, "hour", user1.Id, base.Add(-9*time.Minute))
	seedArcadeChangelog(t, app, arcadeG, "sns", user2.Id, base.Add(-10*time.Minute))
	seedArcadeChangelog(t, app, arcadeA, "sns", user1.Id, base.Add(-11*time.Minute))
	seedArcadeChangelog(t, app, arcadePrivate, "game", user1.Id, base.Add(-12*time.Minute))
	seedArcadeChangelog(t, app, arcadeClosed, "photo", user2.Id, base.Add(-13*time.Minute))

	createFlagWithReactions(t, app, arcadeA, user1.Id, base.Add(-3*time.Minute-30*time.Second), []reactionSeed{
		{reaction: "fixed", createdAt: base.Add(-3*time.Minute - 45*time.Second)},
	})

	res := executeJSONRequest(t, app, http.MethodGet, "/arcades/updates", "", nil)
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var payload []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(payload) != 5 {
		t.Fatalf("expected 5 arcade update entries, got %d", len(payload))
	}

	expectedOrder := []string{"Arcade A", "Arcade B", "Arcade C", "Arcade D", "Arcade E"}
	for i, expectedName := range expectedOrder {
		if got := payload[i]["name"]; got != expectedName {
			t.Fatalf("expected payload[%d].name=%q, got %v", i, expectedName, got)
		}
	}

	entryA := payload[0]
	partsA := stringSliceFromAny(t, entryA["updated_parts"])
	assertStringSlicesEqual(t, partsA, []string{"hour", "game", "photo", "flag", "flag_reaction"})

	changesA := mapSliceFromAny(t, entryA["changes"])
	if len(changesA) != 6 {
		t.Fatalf("expected 6 changes for Arcade A, got %d", len(changesA))
	}
	assertChangeRow(t, changesA[0], "photo", user1.Id)
	assertChangeRow(t, changesA[1], "game", user1.Id)
	assertChangeRow(t, changesA[2], "game", user1.Id)
	assertChangeRow(t, changesA[3], "hour", user2.Id)
	assertChangeRow(t, changesA[4], "flag", user1.Id)
	assertChangeRow(t, changesA[5], "flag_reaction", user1.Id)

	if got := entryA["block_started_at"]; got != changesA[0]["created"] {
		t.Fatalf("expected block_started_at to match newest change, got %v vs %v", got, changesA[0]["created"])
	}
	if got := entryA["block_ended_at"]; got != changesA[5]["created"] {
		t.Fatalf("expected block_ended_at to match oldest change, got %v vs %v", got, changesA[5]["created"])
	}
	if got := entryA["updated"]; got != changesA[0]["created"] {
		t.Fatalf("expected updated to match newest change, got %v vs %v", got, changesA[0]["created"])
	}

	entryE := payload[4]
	partsE := stringSliceFromAny(t, entryE["updated_parts"])
	assertStringSlicesEqual(t, partsE, []string{"game", "photo"})
	changesE := mapSliceFromAny(t, entryE["changes"])
	if len(changesE) != 2 {
		t.Fatalf("expected 2 changes for Arcade E, got %d", len(changesE))
	}
	assertChangeRow(t, changesE[0], "photo", user2.Id)
	assertChangeRow(t, changesE[1], "game", user1.Id)

	for _, entry := range payload {
		name, _ := entry["name"].(string)
		if name == "Arcade Private" || name == "Arcade Closed" || name == "Arcade F" || name == "Arcade G" {
			t.Fatalf("unexpected arcade in default limit response: %s", name)
		}
	}
}

func TestListArcadeUpdates_CountryFilterAndCustomLimit(t *testing.T) {
	app := newArcadeTestApp(t)

	_, user1 := createAuthUser(t, app)
	_, user2 := createAuthUser(t, app)

	base := time.Date(2030, 3, 25, 9, 0, 0, 0, time.UTC)

	kr1, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "KR 1",
		Address:  "KR Address 1",
		Location: location{Lat: 37.55, Lon: 126.97},
		Country:  "KR",
	})
	kr2, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "KR 2",
		Address:  "KR Address 2",
		Location: location{Lat: 35.18, Lon: 129.07},
		Country:  "KR",
	})
	jp1, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "JP 1",
		Address:  "JP Address 1",
		Location: location{Lat: 35.68, Lon: 139.69},
		Country:  "JP",
	})
	kr3, _ := seedArcade(t, app, user1.Id, arcadeSeed{
		Name:     "KR 3",
		Address:  "KR Address 3",
		Location: location{Lat: 35.87, Lon: 128.60},
		Country:  "KR",
	})

	setArcadeVisibilityAndUpdated(t, app, kr1, true, false, base)
	setArcadeVisibilityAndUpdated(t, app, kr2, true, false, base.Add(-1*time.Minute))
	setArcadeVisibilityAndUpdated(t, app, jp1, true, false, base.Add(-2*time.Minute))
	setArcadeVisibilityAndUpdated(t, app, kr3, true, false, base.Add(-3*time.Minute))

	seedArcadeChangelog(t, app, kr1, "game", user1.Id, base)
	seedArcadeChangelog(t, app, kr2, "hour", user2.Id, base.Add(-1*time.Minute))
	seedArcadeChangelog(t, app, jp1, "photo", user1.Id, base.Add(-2*time.Minute))
	seedArcadeChangelog(t, app, kr3, "sns", user2.Id, base.Add(-3*time.Minute))

	res := executeJSONRequest(t, app, http.MethodGet, "/arcades/updates?country=KR&limit=2", "", nil)
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var payload []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(payload) != 2 {
		t.Fatalf("expected 2 KR entries, got %d", len(payload))
	}
	if got := payload[0]["name"]; got != "KR 1" {
		t.Fatalf("expected first KR arcade to be KR 1, got %v", got)
	}
	if got := payload[1]["name"]; got != "KR 2" {
		t.Fatalf("expected second KR arcade to be KR 2, got %v", got)
	}
	for _, entry := range payload {
		if got := entry["country"]; got != "KR" {
			t.Fatalf("expected country filter to keep only KR, got %v", got)
		}
	}
}

func TestListArcadeUpdates_InvalidLimit(t *testing.T) {
	app := newArcadeTestApp(t)

	res := executeJSONRequest(t, app, http.MethodGet, "/arcades/updates?limit=0", "", nil)
	defer res.Body.Close()

	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if got := payload["error"]; got != "invalid 'limit' value; expected positive integer" {
		t.Fatalf("unexpected error payload: %v", payload)
	}
}

func TestListArcadeUpdates_IncludesOnlyPublicVisits(t *testing.T) {
	app := newArcadeTestApp(t)

	_, summaryUser := createAuthUser(t, app)
	_, fullUser := createAuthUser(t, app)
	_, privateUser := createAuthUser(t, app)
	base := time.Date(2030, 3, 26, 9, 0, 0, 0, time.UTC)

	summaryArcade, _ := seedArcade(t, app, summaryUser.Id, arcadeSeed{Name: "Summary visit", Address: "A", Location: location{Lat: 37.1, Lon: 127.1}, Country: "KR"})
	fullArcade, _ := seedArcade(t, app, fullUser.Id, arcadeSeed{Name: "Full visit", Address: "B", Location: location{Lat: 37.2, Lon: 127.2}, Country: "KR"})
	privateArcade, _ := seedArcade(t, app, privateUser.Id, arcadeSeed{Name: "Private visit", Address: "C", Location: location{Lat: 37.3, Lon: 127.3}, Country: "KR"})

	setArcadeVisibilityAndUpdated(t, app, summaryArcade, true, false, base)
	setArcadeVisibilityAndUpdated(t, app, fullArcade, true, false, base)
	setArcadeVisibilityAndUpdated(t, app, privateArcade, true, false, base)
	setVisitVisibility(t, app, summaryUser.Id, "summary")
	setVisitVisibility(t, app, fullUser.Id, "full")
	setVisitVisibility(t, app, privateUser.Id, "private")
	seedArcadeVisit(t, app, summaryUser.Id, summaryArcade, base)
	seedArcadeVisit(t, app, fullUser.Id, fullArcade, base.Add(-time.Minute))
	seedArcadeVisit(t, app, privateUser.Id, privateArcade, base.Add(-2*time.Minute))

	res := executeJSONRequest(t, app, http.MethodGet, "/arcades/updates", "", nil)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", res.StatusCode)
	}

	var payload []map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if len(payload) != 2 {
		t.Fatalf("expected 2 public visit entries, got %d: %#v", len(payload), payload)
	}
	expectedUsers := map[string]string{
		"Summary visit": summaryUser.Id,
		"Full visit":    fullUser.Id,
	}
	for _, entry := range payload {
		name, _ := entry["name"].(string)
		expectedUser, ok := expectedUsers[name]
		if !ok {
			t.Fatalf("unexpected visit entry: %#v", entry)
		}
		parts := stringSliceFromAny(t, entry["updated_parts"])
		assertStringSlicesEqual(t, parts, []string{"visit"})
		changes := mapSliceFromAny(t, entry["changes"])
		if len(changes) != 1 {
			t.Fatalf("expected one visit change, got %#v", changes)
		}
		assertChangeRow(t, changes[0], "visit", expectedUser)
	}
}

func setVisitVisibility(tb testing.TB, app *tests.TestApp, userID, visibility string) {
	tb.Helper()
	rec, err := app.FindRecordById("user_info", userID)
	if err != nil {
		tb.Fatalf("failed to load user_info: %v", err)
	}
	rec.Set("visit_visibility", visibility)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to set visit visibility: %v", err)
	}
}

func seedArcadeVisit(tb testing.TB, app *tests.TestApp, userID, arcadeID string, ts time.Time) {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("arcade_visit")
	if err != nil {
		tb.Fatalf("failed to load arcade_visit collection: %v", err)
	}
	rec := core.NewRecord(coll)
	rec.Set("user", userID)
	rec.Set("arcade", arcadeID)
	rec.Set("visit_day", ts.Format("2006-01-02"))
	rec.Set("visited_at", ts.Format(time.RFC3339))
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade visit: %v", err)
	}
}

func seedArcadeChangelog(tb testing.TB, app *tests.TestApp, arcadeID, changed, by string, ts time.Time) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_changelog")
	if err != nil {
		tb.Fatalf("failed to load arcade_changelog collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("changed", changed)
	rec.Set("by", by)
	rec.Set("from", "")
	rec.Set("to", "")

	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_changelog: %v", err)
	}

	when := ts.UTC().Format(types.DefaultDateLayout)
	if _, err := app.NonconcurrentDB().
		NewQuery("UPDATE arcade_changelog SET created={:created} WHERE id={:id}").
		Bind(dbx.Params{"created": when, "id": rec.Id}).
		Execute(); err != nil {
		tb.Fatalf("failed to update arcade_changelog.created for %s: %v", rec.Id, err)
	}
	return rec.Id
}

func setArcadeVisibilityAndUpdated(tb testing.TB, app *tests.TestApp, arcadeID string, public, closed bool, ts time.Time) {
	tb.Helper()

	rec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade %s: %v", arcadeID, err)
	}
	rec.Set("public", public)
	rec.Set("closed", closed)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade visibility: %v", err)
	}

	when := ts.UTC().Format(types.DefaultDateLayout)
	if _, err := app.NonconcurrentDB().
		NewQuery("UPDATE arcade SET updated={:updated} WHERE id={:id}").
		Bind(dbx.Params{"updated": when, "id": arcadeID}).
		Execute(); err != nil {
		tb.Fatalf("failed to update arcade.updated for %s: %v", arcadeID, err)
	}
}

func stringSliceFromAny(tb testing.TB, raw any) []string {
	tb.Helper()

	items, ok := raw.([]any)
	if !ok {
		tb.Fatalf("expected []any, got %T", raw)
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := item.(string)
		if !ok {
			tb.Fatalf("expected string item, got %T", item)
		}
		out = append(out, s)
	}
	return out
}

func mapSliceFromAny(tb testing.TB, raw any) []map[string]any {
	tb.Helper()

	items, ok := raw.([]any)
	if !ok {
		tb.Fatalf("expected []any, got %T", raw)
	}

	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			tb.Fatalf("expected map[string]any item, got %T", item)
		}
		out = append(out, m)
	}
	return out
}

func assertStringSlicesEqual(tb testing.TB, got, want []string) {
	tb.Helper()

	if len(got) != len(want) {
		tb.Fatalf("expected %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			tb.Fatalf("expected %v, got %v", want, got)
		}
	}
}

func assertChangeRow(tb testing.TB, row map[string]any, part, by string) {
	tb.Helper()

	if got := row["part"]; got != part {
		tb.Fatalf("expected change part %q, got %v", part, got)
	}
	if got := row["by"]; got != by {
		tb.Fatalf("expected change by %q, got %v", by, got)
	}
	if _, ok := row["created"].(string); !ok {
		tb.Fatalf("expected change created string, got %T", row["created"])
	}
}
