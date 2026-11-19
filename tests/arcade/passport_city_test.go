package arcade_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	arcadebasic "github.com/ericbaek/musecat-backend-core/handlers/arcade/basic"
)

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
