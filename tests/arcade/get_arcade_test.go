package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestGetArcadeValues_ExpandGameOmitsMissingTagQuantity(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:   "GET /arcade expand game omits missing tag quantity",
		Method: http.MethodGet,
		ExpectedContent: []string{
			`"game":{"id":"`,
		},
		ExpectedStatus: http.StatusOK,
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Tagless Quantity Arcade",
			Address:  "No Qty Street",
			Nickname: []string{"Tagless"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		versionID := seedGameSeriesVersion(tb, app)
		moleculeID := seedArcadeGameMolecule(tb, app, arcadeID)
		atomID := seedArcadeGameAtom(tb, app, moleculeID, versionID, "1F")

		atom, err := app.FindRecordById("arcade_game_atoms", atomID)
		if err != nil {
			tb.Fatalf("failed to load atom: %v", err)
		}
		atom.Set("tag", []map[string]any{{"category": "기타", "note": "ok"}})
		if err := app.Save(atom); err != nil {
			tb.Fatalf("failed to update atom tag: %v", err)
		}

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object, got %T", payload["game"])
		}
		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %T %#v", gameObj["items"], gameObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected game item object, got %T", items[0])
		}
		tags, ok := item["tag"].([]any)
		if !ok || len(tags) != 1 {
			tb.Fatalf("expected one tag entry, got %T %#v", item["tag"], item["tag"])
		}
		tag, ok := tags[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected tag object, got %T", tags[0])
		}
		if _, exists := tag["quantity"]; exists {
			tb.Fatalf("expected response tag to omit quantity, got %#v", tag)
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_Default(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade returns relations",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	var arcadeID string
	var basicID string
	var createdBy string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		createdBy = user.Id
		id, basic := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Default Arcade",
			Address:  "123 Seoul Road",
			Nickname: []string{"Default"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		arcadeID, basicID = id, basic
		scenario.URL = fmt.Sprintf("/arcade?id=%s", arcadeID)
		scenario.ExpectedContent = []string{
			fmt.Sprintf(`"id":"%s"`, arcadeID),
			fmt.Sprintf(`"basic":"%s"`, basicID),
		}
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		if _, exists := payload["arcade"]; exists {
			tb.Fatalf("expected top-level arcade key to be removed")
		}
		if got := payload["basic"]; got != basicID {
			tb.Fatalf("expected basic id %q, got %v", basicID, got)
		}

		admin, ok := payload["admin"].(map[string]any)
		if !ok {
			tb.Fatalf("expected admin object, got %T", payload["admin"])
		}
		if got := admin["id"]; got != arcadeID {
			tb.Fatalf("expected admin.id %q, got %v", arcadeID, got)
		}
		if got := admin["createdBy"]; got != createdBy {
			tb.Fatalf("expected admin.createdBy %q, got %v", createdBy, got)
		}
		if got := admin["public"]; got != true {
			tb.Fatalf("expected admin.public true, got %v", got)
		}
		if got := admin["closed"]; got != false {
			tb.Fatalf("expected admin.closed false, got %v", got)
		}
		if got := admin["country"]; got != "KR" {
			tb.Fatalf("expected admin.country KR, got %v", got)
		}
		if got := admin["timezone"]; got != "Asia/Seoul" {
			tb.Fatalf("expected admin.timezone Asia/Seoul, got %v", got)
		}
		if _, ok := admin["created"]; !ok {
			tb.Fatalf("expected admin.created to exist")
		}
		if _, ok := admin["updated"]; !ok {
			tb.Fatalf("expected admin.updated to exist")
		}
		if len(admin) != 8 {
			tb.Fatalf("expected admin to have only 8 keys, got %d (%v)", len(admin), admin)
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandBasic(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand basic",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:      "Expanded Arcade",
			Address:   "456 Busan St",
			Direction: "B1",
			Nickname:  []string{"Expand"},
			Location:  location{Lat: 35.1796, Lon: 129.0756},
		})

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=basic", arcadeID)
		scenario.ExpectedContent = []string{
			`"name":"Expanded Arcade"`,
			`"address":"456 Busan St"`,
		}
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		basic, ok := payload["basic"].(map[string]any)
		if !ok {
			tb.Fatalf("expected basic expansion object, got %T", payload["basic"])
		}

		if basic["name"] != "Expanded Arcade" {
			tb.Fatalf("expected name Expanded Arcade, got %v", basic["name"])
		}
		if basic["address"] != "456 Busan St" {
			tb.Fatalf("expected address 456 Busan St, got %v", basic["address"])
		}

		loc, ok := basic["location"].(map[string]any)
		if !ok {
			tb.Fatalf("expected location map, got %T", basic["location"])
		}
		if !floatAlmostEq(loc["lat"].(float64), 35.1796) || !floatAlmostEq(loc["lon"].(float64), 129.0756) {
			tb.Fatalf("unexpected location: %v", loc)
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandGTKParkingMeta(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand gtk returns parking meta",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Parking Expanded Arcade",
			Address:  "Parking Road",
			Nickname: []string{"Park"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		moleculeID := seedGTKMolecule(tb, app, arcadeID, user.Id)
		seedGTKAtom(tb, app, moleculeID, user.Id, "Parking", true, "parking available", map[string]any{
			"geo": map[string]any{
				"lat": 37.55,
				"lon": 126.97,
			},
			"availability": "always_plenty",
			"options":      []string{"free_parking_lot", "paid_street_parking"},
			"ev_charging":  true,
			"gov_parking":  false,
		})

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=gtk", arcadeID)
		scenario.ExpectedContent = []string{
			`"gtk":{`,
			`"type":"Parking"`,
			`"meta":{`,
			`"availability":"always_plenty"`,
		}
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gtkObj, ok := payload["gtk"].(map[string]any)
		if !ok {
			tb.Fatalf("expected gtk expansion object, got %T", payload["gtk"])
		}
		items, ok := gtkObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected 1 gtk item, got %T %#v", gtkObj["items"], gtkObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected gtk item object, got %T", items[0])
		}
		meta, ok := item["meta"].(map[string]any)
		if !ok {
			tb.Fatalf("expected meta object, got %T", item["meta"])
		}
		if got := meta["availability"]; got != "always_plenty" {
			tb.Fatalf("expected availability always_plenty, got %v", got)
		}
		if got := meta["ev_charging"]; got != true {
			tb.Fatalf("expected ev_charging true, got %v", got)
		}
		if got := meta["gov_parking"]; got != false {
			tb.Fatalf("expected gov_parking false, got %v", got)
		}
		geo, ok := meta["geo"].(map[string]any)
		if !ok {
			tb.Fatalf("expected geo object, got %T", meta["geo"])
		}
		if !floatAlmostEq(geo["lat"].(float64), 37.55) || !floatAlmostEq(geo["lon"].(float64), 126.97) {
			tb.Fatalf("unexpected geo: %v", geo)
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandGame_IncludesUncertainPrevGame(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand game includes uncertain prev_game",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"uncertain":true`,
			`"prev_game":{`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Uncertain Arcade",
			Address:  "789 Seoul Ave",
			Nickname: []string{"Uncertain"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		seriesID := seedGameSeries(tb, app, 3, "Series Three")
		prevVersionID := seedGameSeriesVersionWithSeries(tb, app, seriesID, "2024-01-01", "Previous Version")
		nextVersionID := seedGameSeriesVersionWithSeries(tb, app, seriesID, "2025-01-01", "Next Version")

		moleculeID := seedArcadeGameMolecule(tb, app, arcadeID)
		atomID := seedArcadeGameAtom(tb, app, moleculeID, nextVersionID, "1F")
		atom, err := app.FindRecordById("arcade_game_atoms", atomID)
		if err != nil {
			tb.Fatalf("failed to load game atom: %v", err)
		}
		atom.Set("uncertain", true)
		atom.Set("prev_game", prevVersionID)
		if err := app.Save(atom); err != nil {
			tb.Fatalf("failed to update atom uncertainty metadata: %v", err)
		}

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=game", arcadeID)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object, got %T", payload["game"])
		}
		items, ok := gameObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected one game item, got %T %#v", gameObj["items"], gameObj["items"])
		}

		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected item object, got %T", items[0])
		}
		if got, _ := item["uncertain"].(bool); !got {
			tb.Fatalf("expected item.uncertain=true, got %v", item["uncertain"])
		}
		if got, ok := item["updated"].(string); !ok || got == "" {
			tb.Fatalf("expected item.updated to exist, got %v", item["updated"])
		}
		prevGame, ok := item["prev_game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected prev_game object, got %T", item["prev_game"])
		}
		if prevGame["id"] == "" {
			tb.Fatalf("expected prev_game.id to exist, got %v", prevGame["id"])
		}
		if prevGame["series"] == "" {
			tb.Fatalf("expected prev_game.series to exist, got %v", prevGame["series"])
		}
	}

	scenario.Test(t)
}

func TestGetArcadeValues_MissingID(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade missing id",
		Method:         http.MethodGet,
		URL:            "/arcade",
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"missing required query param 'id' or 'arcade'"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.Test(t)
}

func TestGetArcadeValues_NotFound(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade not found",
		Method:         http.MethodGet,
		URL:            "/arcade?id=nonexistent",
		ExpectedStatus: http.StatusNotFound,
		ExpectedContent: []string{
			`"error":"arcade not found"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.Test(t)
}

func TestGetArcadeValues_ExpandPhoto(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /arcade expand photo",
		Method:         http.MethodGet,
		ExpectedStatus: http.StatusOK,
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	var atomID string

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		_, user := createAuthUser(tb, app)
		user.Set("username", "photo_user")
		if err := app.Save(user); err != nil {
			tb.Fatalf("failed to update user username: %v", err)
		}
		userInfo := ensureUserInfo(tb, app, user.Id)
		userInfo.Set("nickname", "photo_nick")
		if err := app.Save(userInfo); err != nil {
			tb.Fatalf("failed to update user_info nickname: %v", err)
		}

		arcadeID, _ := seedPublicArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Photo Expanded Arcade",
			Address:  "Photo Expanded Street",
			Nickname: []string{"PhotoExpanded"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		atomID = seedPhotoAtom(tb, app, arcadeID, user.Id, true)

		photoColl, err := app.FindCollectionByNameOrId("arcade_photo")
		if err != nil {
			tb.Fatalf("failed to load arcade_photo collection: %v", err)
		}
		photoRec := core.NewRecord(photoColl)
		photoRec.Set("arcade", arcadeID)
		photoRec.Set("photos", []string{atomID})
		photoRec.Set("createdBy", user.Id)
		if err := app.Save(photoRec); err != nil {
			tb.Fatalf("failed to save arcade_photo: %v", err)
		}

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		arcadeRec.Set("photo", photoRec.Id)
		if err := app.Save(arcadeRec); err != nil {
			tb.Fatalf("failed to link arcade.photo: %v", err)
		}

		scenario.URL = fmt.Sprintf("/arcade?id=%s&expand=photo", arcadeID)
		scenario.ExpectedContent = []string{
			`"photo":{`,
			`"items":[{`,
			`"photo":"`,
			`"username":"photo_user"`,
			`"nickname":"photo_nick"`,
		}
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		photoObj, ok := payload["photo"].(map[string]any)
		if !ok {
			tb.Fatalf("expected photo expansion object, got %T", payload["photo"])
		}

		itemsAny, ok := photoObj["items"].([]any)
		if !ok || len(itemsAny) == 0 {
			tb.Fatalf("expected photo.items array, got %T (%v)", photoObj["items"], photoObj["items"])
		}

		item, ok := itemsAny[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected photo item object, got %T", itemsAny[0])
		}
		if got := item["id"]; got != atomID {
			tb.Fatalf("expected photo item id %q, got %v", atomID, got)
		}
		if _, ok := item["photo"]; !ok {
			tb.Fatalf("expected photo item to include photo key")
		}
		if got := item["public"]; got != true {
			tb.Fatalf("expected photo item public=true, got %v", got)
		}
		if _, ok := item["created"]; !ok {
			tb.Fatalf("expected photo item to include created key")
		}

		createdBy, ok := item["createdBy"].(map[string]any)
		if !ok {
			tb.Fatalf("expected createdBy object, got %T", item["createdBy"])
		}
		if got := createdBy["nickname"]; got != "photo_nick" {
			tb.Fatalf("expected createdBy.nickname photo_nick, got %v", got)
		}
		if got := createdBy["username"]; got != "photo_user" {
			tb.Fatalf("expected createdBy.username photo_user, got %v", got)
		}
	}

	scenario.Test(t)
}
