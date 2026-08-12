package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestGetArcadeValues_ExpandGameIncludesLocalizedCabinet(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:            "GET /arcade expand game includes localized cabinet",
		Method:          http.MethodGet,
		ExpectedContent: []string{"CHUNITHM GOLD", "츄니즘 골드"},
		ExpectedStatus:  http.StatusOK,
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Localized Cabinet Arcade",
			Address:  "Cabinet Street",
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		versionID := seedGameSeriesVersion(tb, app)
		moleculeID := seedArcadeGameMolecule(tb, app, arcadeID)
		atomID := seedArcadeGameAtom(tb, app, moleculeID, versionID, "1F")
		cabinetID := seedGameCabinet(tb, app, "Cabinet")

		cabinet, err := app.FindRecordById("game_cabinet", cabinetID)
		if err != nil {
			tb.Fatalf("failed to load cabinet: %v", err)
		}
		cabinet.Set("en", "CHUNITHM GOLD")
		cabinet.Set("kr", "츄니즘 골드")
		cabinet.Set("jp", "チュウニズム ゴールド")
		if err := app.Save(cabinet); err != nil {
			tb.Fatalf("failed to save localized cabinet: %v", err)
		}

		revision, err := app.FindFirstRecordByFilter(
			"arcade_game_history",
			"batch={:batch} && entry={:entry}",
			map[string]any{"batch": moleculeID, "entry": atomID},
		)
		if err != nil {
			tb.Fatalf("failed to load revision: %v", err)
		}
		revision.Set("cabinet", cabinetID)
		if err := app.Save(revision); err != nil {
			tb.Fatalf("failed to save revision cabinet: %v", err)
		}

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		game, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object, got %T", payload["game"])
		}
		items, ok := game["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %#v", game["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected game item object, got %T", items[0])
		}
		cabinet, ok := item["cabinet"].(map[string]any)
		if !ok {
			tb.Fatalf("expected cabinet object, got %T", item["cabinet"])
		}
		if id, ok := cabinet["id"].(string); !ok || id == "" {
			tb.Fatalf("expected cabinet id, got %#v", cabinet["id"])
		}
		for key, want := range map[string]string{
			"en": "CHUNITHM GOLD",
			"kr": "츄니즘 골드",
			"jp": "チュウニズム ゴールド",
		} {
			if got := cabinet[key]; got != want {
				tb.Fatalf("expected cabinet.%s=%q, got %v", key, want, got)
			}
		}
	}

	scenario.Test(t)
}
