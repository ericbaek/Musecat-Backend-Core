package user_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// Native GTK v1 regression: real HTTP through the native bearer guard, explicit
// delta operations, revision CAS, no-op/history/XP invariants, future metadata
// and unknown types, parking validation parity, explicit clears, authorization.
func TestNativeGTKV1(t *testing.T) {
	app := newUserFetchTestApp(t)
	web, user := createAuthUser(t, app, true)
	clients := []nativeauth.Client{{ID: "ios", Callback: "musecat-test://auth/callback"}}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error { nativeauth.RegisterRoutes(e, clients); return e.Next() })
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	event := &core.ServeEvent{App: app, Router: router}
	if err = app.OnServe().Trigger(event, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	verifier := strings.Repeat("g", 43)
	hash := sha256.Sum256([]byte(verifier))
	service := nativeauth.Service{Clients: clients}
	code, err := service.IssueCode(app, user, "ios", clients[0].Callback, base64.RawURLEncoding.EncodeToString(hash[:]))
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.Exchange(app, code, verifier, "ios", clients[0].Callback)
	if err != nil {
		t.Fatal(err)
	}
	token := credentials.AccessToken
	arcade := seedVisitArcade(t, app, "Asia/Seoul")

	requestArcade := func(arcadeID, method string, body any, status int) map[string]any {
		t.Helper()
		var encoded []byte
		if raw, ok := body.([]byte); ok {
			encoded = raw
		} else if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req, err := http.NewRequest(method, server.URL+"/native/v1/arcades/"+arcadeID+"/gtk", strings.NewReader(string(encoded)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != status {
			t.Fatalf("%s gtk got %d want %d: %s", method, response.StatusCode, status, data)
		}
		var out map[string]any
		_ = json.Unmarshal(data, &out)
		return out
	}
	request := func(method string, body any, status int) map[string]any {
		t.Helper()
		return requestArcade(arcade.Id, method, body, status)
	}
	count := func(collection string) int64 {
		n, err := app.CountRecords(collection)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	type snapshot struct{ molecules, atoms, logs, xp int64 }
	take := func() snapshot {
		return snapshot{count("arcade_gtk"), count("arcade_gtk_atoms"), count("arcade_changelog"), count("user_level_log")}
	}
	unchanged := func(before snapshot, what string) {
		t.Helper()
		if after := take(); after != before {
			t.Fatalf("%s wrote state: before %+v after %+v", what, before, after)
		}
	}
	// Every native 400 uses the documented {error} envelope and writes nothing.
	rejected := func(body any, code string) {
		t.Helper()
		before := take()
		if got := request("PATCH", body, 400)["error"]; got != code {
			t.Fatalf("PATCH %v error %v want %s", body, got, code)
		}
		unchanged(before, code)
	}
	items := func() (string, map[string]map[string]any) {
		t.Helper()
		got := request("GET", nil, 200)
		out := map[string]map[string]any{}
		for _, raw := range got["items"].([]any) {
			item := raw.(map[string]any)
			out[item["type"].(string)] = item
		}
		return got["revision"].(string), out
	}
	patch := func(revision any, ops map[string]any) map[string]any {
		body := map[string]any{"revision": revision}
		for key, value := range ops {
			body[key] = value
		}
		return body
	}
	add := func(revision any, atoms ...map[string]any) map[string]any {
		return patch(revision, map[string]any{"add": atoms})
	}
	update := func(revision any, id string, changes map[string]any) map[string]any {
		return patch(revision, map[string]any{"update": []any{map[string]any{"id": id, "changes": changes}}})
	}
	parking := func(meta map[string]any) map[string]any {
		return map[string]any{"type": "Parking", "bool": true, "meta": meta}
	}
	changelogItems := func(expected int) []map[string]any {
		t.Helper()
		rows, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:arcade} && changed='gtk'", "-created", 1, 0, map[string]any{"arcade": arcade.Id})
		if err != nil || len(rows) != 1 {
			t.Fatalf("gtk changelog rows: %v %v", len(rows), err)
		}
		encoded, _ := json.Marshal(rows[0].Get("log"))
		var log struct {
			Type  string           `json:"type"`
			Items []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(encoded, &log)
		if log.Type != "gtk_diff" || len(log.Items) != expected {
			t.Fatalf("gtk changelog %s with %d items, want %d", log.Type, len(log.Items), expected)
		}
		return log.Items
	}

	// Absent section: explicit empty revision, empty item list, fixed capability.
	empty := request("GET", nil, 200)
	if empty["contract_version"] != float64(1) || !reflect.DeepEqual(empty["required_capabilities"], []any{"gtk.delta.v1"}) || empty["revision"] != "" || empty["arcade"] != arcade.Id || !reflect.DeepEqual(empty["items"], []any{}) {
		t.Fatal(empty)
	}

	// Request shape: revision and at least one operation, strict JSON, documented codes.
	near := map[string]any{"lat": 37.5670, "lon": 126.9785}
	rejected(map[string]any{}, "revision_and_operations_required")
	rejected(map[string]any{"revision": ""}, "revision_and_operations_required")
	rejected(map[string]any{"revision": nil, "add": []any{map[string]any{"type": "FreeWifi", "bool": true}}}, "revision_and_operations_required")
	rejected(map[string]any{"add": []any{map[string]any{"type": "FreeWifi", "bool": true}}}, "revision_and_operations_required")
	rejected([]byte(`{"revision":"","add":[{"type":"FreeWifi","bool":true}]} {}`), "invalid_patch")
	rejected([]byte(`{"revision":"","add":[{"type":"FreeWifi","bool":true}],"extra":1}`), "invalid_patch")
	rejected([]byte(`{"revision":"","add":[{"type":"FreeWifi","bool":true,"extra":1}]}`), "invalid_patch")
	rejected([]byte(`{"revision":"","update":[{"id":"x","changes":{"bool":true},"extra":1}]}`), "invalid_patch")
	rejected([]byte(`not json`), "invalid_patch")
	rejected([]byte(``), "invalid_patch")
	rejected(add("", map[string]any{"type": "Nope", "bool": true}), "unsupported_gtk_type")
	rejected(add("", map[string]any{"type": "", "bool": true}), "unsupported_gtk_type")
	rejected(add("", map[string]any{"type": "freewifi", "bool": true}), "unsupported_gtk_type")
	rejected(add("", map[string]any{"type": "FreeWifi"}), "invalid_gtk_value")
	rejected(add("", map[string]any{"type": "FreeWifi", "bool": nil}), "invalid_gtk_value")
	rejected(add("", map[string]any{"type": "FreeWifi", "bool": "yes"}), "invalid_gtk_value")
	rejected(add("", map[string]any{"type": "FreeWifi", "bool": 1}), "invalid_gtk_value")
	rejected(add("", map[string]any{"type": "FreeWifi", "bool": true, "note": nil}), "invalid_gtk_value")
	rejected(add("", map[string]any{"type": "FreeWifi", "bool": true, "note": 5}), "invalid_gtk_value")
	rejected(add("", map[string]any{"type": "FreeWifi", "bool": true}, map[string]any{"type": "FreeWifi", "bool": false}), "duplicate_gtk_type")
	rejected(add("", map[string]any{"type": "FreeWifi", "bool": true, "meta": map[string]any{}}), "meta_not_allowed")
	rejected(add("", map[string]any{"type": "FreeWifi", "bool": true, "meta": nil}), "meta_not_allowed")
	// Parking metadata: exact web/Core catalog, both coordinates in range, no unknown members.
	rejected(add("", map[string]any{"type": "Parking", "bool": true, "meta": []any{}}), "invalid_gtk_value")
	rejected(add("", map[string]any{"type": "Parking", "bool": true, "meta": "x"}), "invalid_gtk_value")
	for _, geo := range []any{map[string]any{"lat": 37.5}, map[string]any{"lon": 126.9}, map[string]any{"lat": 91, "lon": 126.9}, map[string]any{"lat": -91, "lon": 126.9}, map[string]any{"lat": 37.5, "lon": 180.5}, map[string]any{"lat": 37.5, "lon": -181}, map[string]any{"lat": "37.5", "lon": 126.9}, map[string]any{"lat": nil, "lon": 126.9}, map[string]any{"lat": true, "lon": 126.9}, []any{37.5, 126.9}, "37.5,126.9", 1} {
		rejected(add("", parking(map[string]any{"geo": geo})), "invalid_parking_geo")
	}
	rejected(add("", parking(map[string]any{"geo": map[string]any{"lat": 37.5, "lon": 126.9, "alt": 1}})), "unsupported_field")
	rejected(add("", parking(map[string]any{"geo": map[string]any{"lat": 37.5, "lon": 126.9, "source": "map"}})), "unsupported_field")
	for _, availability := range []any{"plenty", " always_plenty", "ALWAYS_PLENTY", "", 1, true, []any{"difficult"}} {
		rejected(add("", parking(map[string]any{"availability": availability})), "invalid_parking_availability")
	}
	for _, options := range []any{"free_parking_lot", []any{"valet"}, []any{"free_parking_lot", "free_parking_lot"}, []any{""}, []any{1}, []any{nil}, []any{" free_parking_lot"}, map[string]any{"free_parking_lot": true}} {
		rejected(add("", parking(map[string]any{"options": options})), "invalid_parking_options")
	}
	for _, flag := range []any{"yes", 1, 0, []any{true}, map[string]any{}} {
		rejected(add("", parking(map[string]any{"ev_charging": flag})), "invalid_parking_flag")
		rejected(add("", parking(map[string]any{"gov_parking": flag})), "invalid_parking_flag")
	}
	rejected(add("", parking(map[string]any{"future_hint": "x"})), "unsupported_field")
	rejected(add("", parking(map[string]any{"Geo": near})), "unsupported_field")
	// Web parity: a new or changed entrance must be within 300 m of the arcade.
	rejected(add("", parking(map[string]any{"geo": map[string]any{"lat": 37.5700, "lon": 126.9780}})), "parking_geo_too_far")
	rejected(add("", parking(map[string]any{"geo": map[string]any{"lat": 37.5665, "lon": 126.9820}})), "parking_geo_too_far")
	// Identity rules apply even before the first revision exists.
	rejected(update("", "missing", map[string]any{"bool": true}), "invalid_gtk_id")
	rejected(update("", "", map[string]any{"bool": true}), "invalid_gtk_id")
	rejected(update("", "missing", map[string]any{}), "invalid_gtk_id")
	rejected(update("", "missing", map[string]any{"type": "ATM"}), "unsupported_field")
	rejected(update("", "missing", map[string]any{"id": "other"}), "unsupported_field")
	rejected(patch("", map[string]any{"remove": []string{"missing"}}), "invalid_gtk_id")
	rejected(patch("", map[string]any{"remove": []string{""}}), "invalid_gtk_id")
	rejected(patch("", map[string]any{"remove": []string{"a", "a"}}), "invalid_gtk_id")
	rejected(patch("", map[string]any{"update": []any{map[string]any{"id": "a", "changes": map[string]any{"bool": true}}, map[string]any{"id": "a", "changes": map[string]any{"bool": false}}}}), "invalid_gtk_id")
	rejected(patch("", map[string]any{"update": []any{map[string]any{"id": "a", "changes": map[string]any{"bool": true}}}, "remove": []string{"a"}}), "invalid_gtk_id")
	if request("GET", nil, 200)["revision"] != "" {
		t.Fatal("rejected requests created a revision")
	}

	// First save: explicit adds only. Notes are trimmed; parking keeps full metadata.
	before := take()
	saved := request("PATCH", add("",
		map[string]any{"type": "FreeWifi", "bool": true, "note": " Fast wifi \n"},
		map[string]any{"type": "Locker", "bool": false},
		parking(map[string]any{"geo": near, "availability": "somewhat_plenty", "options": []string{"free_parking_lot", "paid_street_parking"}, "ev_charging": true, "gov_parking": false}),
	), 200)
	if saved["arcade"] != arcade.Id || saved["revision"] == "" || saved["xp_feedback"] == nil {
		t.Fatal(saved)
	}
	if after := take(); after.molecules != before.molecules+1 || after.atoms != before.atoms+3 || after.logs != before.logs+1 || after.xp != before.xp+1 {
		t.Fatalf("first save counts: before %+v after %+v", before, after)
	}
	if saved["xp_feedback"].(map[string]any)["diff_exp"] != float64(2) {
		t.Fatal(saved["xp_feedback"])
	}
	revision, current := items()
	if revision != saved["revision"] || len(current) != 3 {
		t.Fatal(current)
	}
	if current["FreeWifi"]["note"] != "Fast wifi" || current["FreeWifi"]["bool"] != true || current["FreeWifi"]["meta"] != nil || current["FreeWifi"]["id"] == "" {
		t.Fatal(current["FreeWifi"])
	}
	if current["Locker"]["bool"] != false || current["Locker"]["note"] != "" || current["Locker"]["meta"] != nil {
		t.Fatal(current["Locker"])
	}
	parkingMeta := current["Parking"]["meta"].(map[string]any)
	if parkingMeta["availability"] != "somewhat_plenty" || parkingMeta["ev_charging"] != true || parkingMeta["gov_parking"] != false || !reflect.DeepEqual(parkingMeta["geo"], map[string]any{"lat": 37.5670, "lon": 126.9785}) || !reflect.DeepEqual(parkingMeta["options"], []any{"free_parking_lot", "paid_street_parking"}) {
		t.Fatal(parkingMeta)
	}
	for _, item := range changelogItems(3) {
		if item["change_type"] != "added" {
			t.Fatal(item)
		}
	}
	wifiID, lockerID, parkingID := current["FreeWifi"]["id"].(string), current["Locker"]["id"].(string), current["Parking"]["id"].(string)

	// Stale base revision (including the pre-save empty token) is a 409 with no writes.
	before = take()
	if got := request("PATCH", add("", map[string]any{"type": "ATM", "bool": true}), 409)["error"]; got != "revision_conflict" {
		t.Fatal(got)
	}
	if got := request("PATCH", patch("stale", map[string]any{"remove": []string{wifiID}}), 409)["error"]; got != "revision_conflict" {
		t.Fatal(got)
	}
	unchanged(before, "stale revision")

	// No-ops: equal values after trimming/normalization create no revision, history or XP.
	rejected(update(revision, wifiID, map[string]any{"note": "\tFast wifi "}), "no_changes")
	rejected(update(revision, wifiID, map[string]any{"bool": true, "note": "Fast wifi"}), "no_changes")
	rejected(update(revision, lockerID, map[string]any{"bool": false}), "no_changes")
	rejected(update(revision, parkingID, map[string]any{"meta": map[string]any{}}), "no_changes")
	rejected(update(revision, parkingID, map[string]any{"meta": map[string]any{"availability": "somewhat_plenty", "geo": near, "ev_charging": true}}), "no_changes")
	rejected(patch(revision, map[string]any{"update": []any{map[string]any{"id": wifiID, "changes": map[string]any{"bool": true}}, map[string]any{"id": lockerID, "changes": map[string]any{"note": ""}}}}), "no_changes")
	// Existing types are immutable and unique; meta belongs to Parking only.
	rejected(add(revision, map[string]any{"type": "FreeWifi", "bool": false}), "duplicate_gtk_type")
	rejected(add(revision, parking(nil)), "duplicate_gtk_type")
	rejected(update(revision, wifiID, map[string]any{"meta": nil}), "meta_not_allowed")
	rejected(update(revision, wifiID, map[string]any{"meta": map[string]any{"ev_charging": true}}), "meta_not_allowed")
	rejected(update(revision, lockerID, map[string]any{"bool": nil}), "invalid_gtk_value")
	rejected(update(revision, lockerID, map[string]any{"note": nil}), "invalid_gtk_value")
	rejected(update(revision, parkingID, map[string]any{"meta": map[string]any{"geo": map[string]any{"lat": 37.5700, "lon": 126.9780}}}), "parking_geo_too_far")
	rejected(update(revision, parkingID, map[string]any{"meta": map[string]any{"geo": map[string]any{"lat": 37.5670, "lon": 126.9785, "source": "map"}}}), "unsupported_field")
	rejected(update(revision, parkingID, map[string]any{"meta": map[string]any{"options": []string{"paid_parking_lot", "paid_parking_lot"}}}), "invalid_parking_options")
	rejected(update(revision, parkingID, map[string]any{"meta": "difficult"}), "invalid_gtk_value")
	if got, _ := items(); got != revision {
		t.Fatal("rejected requests changed the revision")
	}

	// Simulate server evolution: extra columns, unknown meta members, and a GTK
	// type this contract does not know. All must survive an old client's save.
	atomCollection, _ := app.FindCollectionByNameOrId("arcade_gtk_atoms")
	atomCollection.Fields.Add(&core.TextField{Name: "future_policy"})
	selectType := atomCollection.Fields.GetByName("type").(*core.SelectField)
	selectType.Values = append(selectType.Values, "FutureFacility")
	if err = app.Save(atomCollection); err != nil {
		t.Fatal(err)
	}
	moleculeCollection, _ := app.FindCollectionByNameOrId("arcade_gtk")
	moleculeCollection.Fields.Add(&core.TextField{Name: "future_policy"})
	if err = app.Save(moleculeCollection); err != nil {
		t.Fatal(err)
	}
	molecule, _ := app.FindRecordById("arcade_gtk", revision)
	molecule.Set("future_policy", "molecule policy")
	if err = app.Save(molecule); err != nil {
		t.Fatal(err)
	}
	futureMeta := map[string]any{
		"geo":          map[string]any{"lat": 37.5670, "lon": 126.9785, "source": "map"},
		"availability": "somewhat_plenty",
		"options":      []any{"free_parking_lot", "paid_street_parking"},
		"ev_charging":  true,
		"gov_parking":  false,
		"future_hint":  "keep",
	}
	parkingAtom, _ := app.FindRecordById("arcade_gtk_atoms", parkingID)
	parkingAtom.Set("future_policy", "parking policy")
	parkingAtom.Set("meta", futureMeta)
	if err = app.Save(parkingAtom); err != nil {
		t.Fatal(err)
	}
	future := core.NewRecord(atomCollection)
	future.Set("molecule", revision)
	future.Set("type", "FutureFacility")
	future.Set("bool", true)
	future.Set("note", "vr booth")
	future.Set("meta", map[string]any{"kind": "vr"})
	future.Set("future_policy", "future policy")
	if err = app.Save(future); err != nil {
		t.Fatal(err)
	}
	_, current = items()
	if len(current) != 4 || !reflect.DeepEqual(current["FutureFacility"]["meta"], map[string]any{"kind": "vr"}) || current["FutureFacility"]["note"] != "vr booth" || !reflect.DeepEqual(current["Parking"]["meta"], futureMeta) {
		t.Fatalf("GET hides future data: %v", current)
	}
	futureID := current["FutureFacility"]["id"].(string)
	// Unknown types cannot be created or edited by v1, only kept or explicitly removed.
	rejected(add(revision, map[string]any{"type": "FutureFacility", "bool": true}), "unsupported_gtk_type")
	rejected(update(revision, futureID, map[string]any{"bool": false}), "unsupported_gtk_type")
	rejected(update(revision, futureID, map[string]any{"note": ""}), "unsupported_gtk_type")
	// Unchanged legacy metadata is never revalidated by an unrelated edit.
	before = take()
	request("PATCH", update(revision, lockerID, map[string]any{"bool": true}), 200)
	if after := take(); after.molecules != before.molecules+1 || after.atoms != before.atoms+4 || after.logs != before.logs+1 {
		t.Fatalf("second save counts: before %+v after %+v", before, after)
	}
	revision, current = items()
	if len(current) != 4 || current["Locker"]["bool"] != true || current["Locker"]["id"] == lockerID {
		t.Fatalf("locker update failed or atom IDs were reused: %v", current)
	}
	if !reflect.DeepEqual(current["Parking"]["meta"], futureMeta) || !reflect.DeepEqual(current["FutureFacility"]["meta"], map[string]any{"kind": "vr"}) || current["FutureFacility"]["bool"] != true || current["FutureFacility"]["note"] != "vr booth" || current["FreeWifi"]["note"] != "Fast wifi" {
		t.Fatalf("old client save altered untouched atoms: %v", current)
	}
	for typ, policy := range map[string]string{"Parking": "parking policy", "FutureFacility": "future policy"} {
		rec, _ := app.FindRecordById("arcade_gtk_atoms", current[typ]["id"].(string))
		if rec.GetString("future_policy") != policy || rec.GetString("molecule") != revision {
			t.Fatalf("%s lost future column: %q", typ, rec.GetString("future_policy"))
		}
	}
	if rec, _ := app.FindRecordById("arcade_gtk", revision); rec.GetString("future_policy") != "molecule policy" {
		t.Fatal("lost future molecule column")
	}
	changeTypes := map[string]string{}
	for _, item := range changelogItems(4) {
		changeTypes[item["gtk_type"].(string)] = item["change_type"].(string)
	}
	if changeTypes["Locker"] != "updated" || changeTypes["Parking"] != "unchanged" || changeTypes["FutureFacility"] != "unchanged" || changeTypes["FreeWifi"] != "unchanged" {
		t.Fatal(changeTypes)
	}
	wifiID, lockerID, parkingID, futureID = current["FreeWifi"]["id"].(string), current["Locker"]["id"].(string), current["Parking"]["id"].(string), current["FutureFacility"]["id"].(string)

	// Partial meta patch: only provided known keys change; omitted and future
	// members survive, including inside geo.
	request("PATCH", update(revision, parkingID, map[string]any{"meta": map[string]any{"availability": "difficult", "geo": map[string]any{"lat": 37.5668, "lon": 126.9782}}}), 200)
	revision, current = items()
	parkingID = current["Parking"]["id"].(string)
	parkingMeta = current["Parking"]["meta"].(map[string]any)
	if !reflect.DeepEqual(parkingMeta, map[string]any{
		"geo":          map[string]any{"lat": 37.5668, "lon": 126.9782, "source": "map"},
		"availability": "difficult",
		"options":      []any{"free_parking_lot", "paid_street_parking"},
		"ev_charging":  true,
		"gov_parking":  false,
		"future_hint":  "keep",
	}) {
		t.Fatalf("partial meta patch: %v", parkingMeta)
	}
	for _, item := range changelogItems(4) {
		if item["gtk_type"] == "Parking" && item["change_type"] != "updated" {
			t.Fatal(item)
		}
	}
	// Explicit member clears remove only those members.
	request("PATCH", update(revision, parkingID, map[string]any{"meta": map[string]any{"options": nil, "ev_charging": nil}}), 200)
	revision, current = items()
	parkingID = current["Parking"]["id"].(string)
	parkingMeta = current["Parking"]["meta"].(map[string]any)
	if !reflect.DeepEqual(parkingMeta, map[string]any{
		"geo":          map[string]any{"lat": 37.5668, "lon": 126.9782, "source": "map"},
		"availability": "difficult",
		"gov_parking":  false,
		"future_hint":  "keep",
	}) {
		t.Fatalf("member clear: %v", parkingMeta)
	}
	rejected(update(revision, parkingID, map[string]any{"meta": map[string]any{"options": nil}}), "no_changes")
	request("PATCH", update(revision, parkingID, map[string]any{"meta": map[string]any{"geo": nil, "options": []string{}}}), 200)
	revision, current = items()
	parkingID = current["Parking"]["id"].(string)
	parkingMeta = current["Parking"]["meta"].(map[string]any)
	if !reflect.DeepEqual(parkingMeta, map[string]any{"availability": "difficult", "gov_parking": false, "future_hint": "keep", "options": []any{}}) {
		t.Fatalf("geo clear: %v", parkingMeta)
	}
	// Whole-object clear removes even members this contract does not know; note survives.
	request("PATCH", update(revision, parkingID, map[string]any{"meta": nil, "note": " B1 entrance "}), 200)
	revision, current = items()
	parkingID = current["Parking"]["id"].(string)
	if current["Parking"]["meta"] != nil || current["Parking"]["note"] != "B1 entrance" || current["Parking"]["bool"] != true {
		t.Fatalf("meta clear: %v", current["Parking"])
	}
	rejected(update(revision, parkingID, map[string]any{"meta": nil}), "no_changes")
	rejected(update(revision, parkingID, map[string]any{"meta": map[string]any{"geo": nil}}), "no_changes")
	// Setting bool false does not implicitly drop note or metadata.
	request("PATCH", update(revision, parkingID, map[string]any{"bool": false, "meta": map[string]any{"availability": "always_plenty"}}), 200)
	revision, current = items()
	parkingID = current["Parking"]["id"].(string)
	if current["Parking"]["bool"] != false || current["Parking"]["note"] != "B1 entrance" || !reflect.DeepEqual(current["Parking"]["meta"], map[string]any{"availability": "always_plenty"}) {
		t.Fatalf("bool false kept nothing: %v", current["Parking"])
	}

	// Atom IDs belong to one revision: an ID from an earlier revision is unknown now.
	rejected(patch(revision, map[string]any{"remove": []string{futureID}}), "invalid_gtk_id")
	futureID = current["FutureFacility"]["id"].(string)
	// Explicit removal, including an unknown type and a remove+add of one type in one request.
	before = take()
	request("PATCH", patch(revision, map[string]any{"remove": []string{futureID, parkingID}, "add": []any{map[string]any{"type": "Parking", "bool": false, "note": "closed lot"}}}), 200)
	if after := take(); after.logs != before.logs+1 || after.atoms != before.atoms+3 {
		t.Fatalf("remove/add counts: before %+v after %+v", before, after)
	}
	revision, current = items()
	if len(current) != 3 || current["FutureFacility"] != nil || current["Parking"]["id"] == parkingID || current["Parking"]["note"] != "closed lot" || current["Parking"]["meta"] != nil {
		t.Fatalf("explicit removal: %v", current)
	}
	changeTypes = map[string]string{}
	deleted := 0
	for _, item := range changelogItems(5) {
		if item["change_type"] == "deleted" {
			deleted++
			continue
		}
		changeTypes[item["gtk_type"].(string)] = item["change_type"].(string)
	}
	if deleted != 2 || changeTypes["Parking"] != "added" || changeTypes["FreeWifi"] != "unchanged" {
		t.Fatalf("deleted=%d %v", deleted, changeTypes)
	}
	rejected(patch(revision, map[string]any{"remove": []string{futureID}}), "invalid_gtk_id")
	// Removing everything is an explicit, valid state.
	request("PATCH", patch(revision, map[string]any{"remove": []string{current["FreeWifi"]["id"].(string), current["Locker"]["id"].(string), current["Parking"]["id"].(string)}}), 200)
	revision, current = items()
	if revision == "" || len(current) != 0 {
		t.Fatal(current)
	}
	request("PATCH", add(revision, map[string]any{"type": "ATM", "bool": true}), 200)

	// gov_parking follows the web rule: only Korean arcades may set it; null clears anywhere.
	foreign := seedVisitArcade(t, app, "Asia/Tokyo")
	foreign.Set("country", "JP")
	if err = app.Save(foreign); err != nil {
		t.Fatal(err)
	}
	before = take()
	if got := requestArcade(foreign.Id, "PATCH", add("", parking(map[string]any{"gov_parking": true})), 400)["error"]; got != "gov_parking_not_available" {
		t.Fatal(got)
	}
	if got := requestArcade(foreign.Id, "PATCH", add("", parking(map[string]any{"gov_parking": false, "availability": "difficult"})), 400)["error"]; got != "gov_parking_not_available" {
		t.Fatal(got)
	}
	unchanged(before, "gov_parking on non-Korean arcade")
	foreignSaved := requestArcade(foreign.Id, "PATCH", add("", parking(map[string]any{"gov_parking": nil, "availability": "difficult"})), 200)
	foreignItems := requestArcade(foreign.Id, "GET", nil, 200)["items"].([]any)
	foreignParking := foreignItems[0].(map[string]any)
	if len(foreignItems) != 1 || !reflect.DeepEqual(foreignParking["meta"], map[string]any{"availability": "difficult"}) {
		t.Fatal(foreignItems)
	}
	if got := requestArcade(foreign.Id, "PATCH", update(foreignSaved["revision"], foreignParking["id"].(string), map[string]any{"meta": map[string]any{"gov_parking": true}}), 400)["error"]; got != "gov_parking_not_available" {
		t.Fatal(got)
	}
	// A legacy far entrance stays untouched by an unrelated edit, but cannot be moved further.
	legacy, _ := app.FindRecordById("arcade_gtk_atoms", foreignParking["id"].(string))
	legacy.Set("meta", map[string]any{"availability": "difficult", "geo": map[string]any{"lat": 35.6895, "lon": 139.6917}})
	if err = app.Save(legacy); err != nil {
		t.Fatal(err)
	}
	foreignSaved = requestArcade(foreign.Id, "PATCH", update(foreignSaved["revision"], foreignParking["id"].(string), map[string]any{"note": "far lot"}), 200)
	foreignItems = requestArcade(foreign.Id, "GET", nil, 200)["items"].([]any)
	foreignParking = foreignItems[0].(map[string]any)
	if !reflect.DeepEqual(foreignParking["meta"].(map[string]any)["geo"], map[string]any{"lat": 35.6895, "lon": 139.6917}) || foreignParking["note"] != "far lot" {
		t.Fatal(foreignParking)
	}
	if got := requestArcade(foreign.Id, "PATCH", update(foreignSaved["revision"], foreignParking["id"].(string), map[string]any{"meta": map[string]any{"geo": map[string]any{"lat": 35.6900, "lon": 139.6917}}}), 400)["error"]; got != "parking_geo_too_far" {
		t.Fatal(got)
	}
	// A stored meta shape v1 cannot read is neither merged nor silently replaced.
	legacy, _ = app.FindRecordById("arcade_gtk_atoms", foreignParking["id"].(string))
	legacy.Set("meta", []any{"opaque"})
	if err = app.Save(legacy); err != nil {
		t.Fatal(err)
	}
	if got := requestArcade(foreign.Id, "PATCH", update(foreignSaved["revision"], foreignParking["id"].(string), map[string]any{"meta": map[string]any{"availability": "difficult"}}), 400)["error"]; got != "unsupported_meta_shape" {
		t.Fatal(got)
	}
	requestArcade(foreign.Id, "PATCH", update(foreignSaved["revision"], foreignParking["id"].(string), map[string]any{"meta": nil}), 200)
	if got := requestArcade(foreign.Id, "GET", nil, 200)["items"].([]any)[0].(map[string]any)["meta"]; got != nil {
		t.Fatal(got)
	}

	// A GTK revision pointing at another arcade is neither exposed nor copied.
	other := seedVisitArcade(t, app, "Asia/Seoul")
	otherMolecule := core.NewRecord(moleculeCollection)
	otherMolecule.Set("arcade", other.Id)
	if err = app.Save(otherMolecule); err != nil {
		t.Fatal(err)
	}
	arcade, _ = app.FindRecordById("arcade", arcade.Id)
	arcade.Set("gtk", otherMolecule.Id)
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	before = take()
	if got := request("GET", nil, 500)["error"]; got != "gtk_unavailable" {
		t.Fatal(got)
	}
	if got := request("PATCH", add(otherMolecule.Id, map[string]any{"type": "Fan", "bool": true}), 502)["error"]; got != "gtk_unavailable" {
		t.Fatal(got)
	}
	unchanged(before, "mispointed revision")
	arcade.Set("gtk", revision)
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}

	// Authorization: native bearer only, same arcade write rules as the web.
	requestArcade("missing", "GET", nil, 404)
	requestArcade("missing", "PATCH", add("", map[string]any{"type": "Fan", "bool": true}), 404)
	token = web
	request("GET", nil, 401)
	request("PATCH", add(revision, map[string]any{"type": "Fan", "bool": true}), 401)
	token = ""
	request("GET", nil, 401)
	token = credentials.AccessToken
	arcade.Set("public", false)
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	before = take()
	if got := request("GET", nil, 403)["error"]; got != "arcade_write_forbidden" {
		t.Fatal(got)
	}
	if got := request("PATCH", add(revision, map[string]any{"type": "Fan", "bool": true}), 403)["error"]; got != "arcade_write_forbidden" {
		t.Fatal(got)
	}
	unchanged(before, "forbidden write")
	// The creator may edit a private draft, and private edits earn no XP.
	arcade.Set("createdBy", user.Id)
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	draft := request("PATCH", add(revision, map[string]any{"type": "Fan", "bool": true}), 200)
	if after := take(); after.xp != before.xp || after.logs != before.logs+1 || draft["xp_feedback"].(map[string]any)["diff_exp"] != float64(0) {
		t.Fatalf("private draft edit: before %+v after %+v %v", before, after, draft["xp_feedback"])
	}
}
