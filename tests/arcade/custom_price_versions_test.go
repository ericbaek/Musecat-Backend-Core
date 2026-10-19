package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestCustomPriceVersions_SaveReadAndRollback(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "Custom price modes", Address: "Test", Location: location{Lat: 37.5665, Lon: 126.9780}})
	series := seedGameSeries(t, app, 31, "Price series")
	version := seedGameSeriesVersionWithSeries(t, app, series, "2025-01-01", "Price version")
	headers := map[string]string{"Authorization": token}
	v1 := `{"currency":"JPY","type":"custom_v1","list":[{"title":"Normal","value":100,"represent":true}],"accept":[]}`
	v2 := `{"currency":"JPY","type":"custom_v2","list":["7 Chips / 1 Credit","Wednesday 3.5 Chips"],"accept":[]}`
	body := fmt.Sprintf(`{"arcade":%q,"base_state_id":"","add":[{"game":%q,"quantity":1,"location":"","price":%s,"tag":[]}],"modify":[],"remove":[]}`, arcadeID, version, v1)
	res := executeJSONRequest(t, app, http.MethodPut, "/arcade/game", body, headers)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("v1 save failed: %s", res.Body)
	}
	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatal(err)
	}
	oldState := arcade.GetString("game_v2")
	revisions, err := app.FindRecordsByFilter("arcade_game_history", "batch={:batch}", "", 0, 0, map[string]any{"batch": oldState})
	if err != nil || len(revisions) != 1 {
		t.Fatalf("missing revision: %v", err)
	}
	entry := revisions[0].GetString("entry")
	historical, _ := json.Marshal(revisions[0].Get("price"))
	body = fmt.Sprintf(`{"arcade":%q,"base_state_id":%q,"add":[],"modify":[{"id":%q,"game":%q,"quantity":1,"location":"","price":%s,"tag":[]}],"remove":[]}`, arcadeID, oldState, entry, version, v2)
	res = executeJSONRequest(t, app, http.MethodPut, "/arcade/game", body, headers)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("v2 save failed: %s", res.Body)
	}
	read := executeJSONRequest(t, app, http.MethodGet, "/arcade?id="+arcadeID+"&expand=game", "", headers)
	var detail map[string]any
	if err := json.NewDecoder(read.Body).Decode(&detail); err != nil || read.StatusCode != http.StatusOK {
		t.Fatalf("readback failed: status=%d detail=%#v err=%v", read.StatusCode, detail, err)
	}
	game := detail["game"].(map[string]any)
	items := game["items"].([]any)
	price := items[0].(map[string]any)["price"].(map[string]any)
	if price["type"] != "custom_v2" || len(price["list"].([]any)) != 2 {
		t.Fatalf("incorrect readback: %#v", price)
	}
	res = executeJSONRequest(t, app, http.MethodPost, "/arcade/rollback", fmt.Sprintf(`{"arcade":%q,"part":"game","value":%q}`, arcadeID, oldState), headers)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("rollback failed: %s", res.Body)
	}
	saved, err := app.FindRecordById("arcade_game_history", revisions[0].Id)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(saved.Get("price"))
	if string(after) != string(historical) {
		t.Fatalf("historical v1 changed: %s", after)
	}
	read = executeJSONRequest(t, app, http.MethodGet, "/arcade?id="+arcadeID+"&expand=game", "", headers)
	json.NewDecoder(read.Body).Decode(&detail)
	price = detail["game"].(map[string]any)["items"].([]any)[0].(map[string]any)["price"].(map[string]any)
	if price["type"] != "custom_v1" {
		t.Fatalf("v1 not restored: %#v", price)
	}
}
