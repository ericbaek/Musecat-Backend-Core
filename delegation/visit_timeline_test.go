package delegation_test

import (
	"github.com/pocketbase/pocketbase/core"
	"testing"
)

func TestAccountConnectionNeverReceivesOwnerVisitTimeline(t *testing.T) {
	f := newFixture(t)
	infoCollection, _ := f.app.FindCollectionByNameOrId("user_info")
	info := core.NewRecord(infoCollection)
	info.Id = f.user.Id
	info.Set("visit_visibility", "full")
	if err := f.app.Save(info); err != nil {
		t.Fatal(err)
	}
	arcades, _ := f.app.FindCollectionByNameOrId("arcade")
	arcade := core.NewRecord(arcades)
	arcade.Set("public", true)
	if err := f.app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	visits, _ := f.app.FindCollectionByNameOrId("arcade_visit")
	visit := core.NewRecord(visits)
	visit.Set("user", f.user.Id)
	visit.Set("arcade", arcade.Id)
	visit.Set("visit_day", "2026-07-01")
	visit.Set("visited_at", "2026-07-01 10:00:00.000Z")
	if err := f.app.Save(visit); err != nil {
		t.Fatal(err)
	}
	owner := f.access(f.token, "/user/visits", 200)
	if len(owner["timeline"].([]any)) != 1 {
		t.Fatal("owner visit missing")
	}
	at := f.accountToken()
	delegated := f.access(at, "/developer/v1/account/user/visits", 200)
	if _, exists := delegated["timeline"]; exists {
		t.Fatal("account connection received private owner timeline")
	}
	if delegated["stats"].(map[string]any)["total_visits"] != float64(1) {
		t.Fatal("existing delegated visit summary changed")
	}
	f.access(at, "/user/visits", 401)
}
