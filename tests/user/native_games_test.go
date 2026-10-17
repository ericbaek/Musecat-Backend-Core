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
	"github.com/pocketbase/pocketbase/tests"
)

func seedNativeGameVersion(tb testing.TB, app *tests.TestApp, seriesID, name string) string {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		tb.Fatal(err)
	}
	rec := core.NewRecord(coll)
	rec.Set("series", seriesID)
	rec.Set("en", name)
	rec.Set("kr", name)
	rec.Set("jp", name)
	if err := app.Save(rec); err != nil {
		tb.Fatal(err)
	}
	return rec.Id
}

func seedNativeGameCabinet(tb testing.TB, app *tests.TestApp, seriesID, name string, versions ...string) string {
	tb.Helper()
	coll, err := app.FindCollectionByNameOrId("game_cabinet")
	if err != nil {
		tb.Fatal(err)
	}
	rec := core.NewRecord(coll)
	rec.Set("en", name)
	rec.Set("kr", name)
	rec.Set("jp", name)
	rec.Set("series", seriesID)
	if err := app.Save(rec); err != nil {
		tb.Fatal(err)
	}
	linkColl, err := app.FindCollectionByNameOrId("game_series_version_cabinet")
	if err != nil {
		tb.Fatal(err)
	}
	for _, version := range versions {
		link := core.NewRecord(linkColl)
		link.Set("version", version)
		link.Set("cabinet", rec.Id)
		if err := app.Save(link); err != nil {
			tb.Fatal(err)
		}
	}
	return rec.Id
}

// Native games v1 regression: real HTTP through the native bearer guard,
// explicit delta operations with changes-only updates, revision CAS, stable
// entry ids, no-op/history/XP invariants, raw price/tag and future column
// preservation, catalog/version/cabinet/price/tag policy parity, authorization.
func TestNativeGamesV1(t *testing.T) {
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

	// Catalog: series 1 with two versions and two cabinets, series 2 with one version.
	series1 := seedGameSeries(t, app, 7001, "Maimai", "마이마이", "マイマイ").Id
	series2 := seedGameSeries(t, app, 7002, "Chunithm", "츄니즘", "チュウニズム").Id
	v1a := seedNativeGameVersion(t, app, series1, "Maimai 2026")
	v1b := seedNativeGameVersion(t, app, series1, "Maimai 2027")
	v2 := seedNativeGameVersion(t, app, series2, "Chunithm 5")
	cabDX := seedNativeGameCabinet(t, app, series1, "DX", v1a, v1b)
	cabSD := seedNativeGameCabinet(t, app, series1, "SD", v1a)
	cabNone := seedNativeGameCabinet(t, app, series1, "Unlinked")

	requestArcade := func(arcadeID, method string, body any, status int) map[string]any {
		t.Helper()
		var encoded []byte
		if raw, ok := body.([]byte); ok {
			encoded = raw
		} else if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req, err := http.NewRequest(method, server.URL+"/native/v1/arcades/"+arcadeID+"/games", strings.NewReader(string(encoded)))
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
			t.Fatalf("%s games got %d want %d: %s", method, response.StatusCode, status, data)
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
	type snapshot struct{ batches, revisions, entries, logs, xp int64 }
	take := func() snapshot {
		return snapshot{count("arcade_game_history_batch"), count("arcade_game_history"), count("arcade_game_id"), count("arcade_changelog"), count("user_level_log")}
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
			out[item["id"].(string)] = item
		}
		return got["revision"].(string), out
	}
	byVersion := func(current map[string]map[string]any, version string) map[string]any {
		t.Helper()
		for _, item := range current {
			if item["game"] == version {
				return item
			}
		}
		t.Fatalf("no item for version %s in %v", version, current)
		return nil
	}
	patch := func(revision any, ops map[string]any) map[string]any {
		body := map[string]any{"revision": revision}
		for key, value := range ops {
			body[key] = value
		}
		return body
	}
	add := func(revision any, games ...map[string]any) map[string]any {
		return patch(revision, map[string]any{"add": games})
	}
	update := func(revision any, id string, changes map[string]any) map[string]any {
		return patch(revision, map[string]any{"update": []any{map[string]any{"id": id, "changes": changes}}})
	}
	creditPrice := func(value float64) map[string]any {
		return map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"title": "1 credit", "value": value}}, "accept": []string{"Cash"}}
	}
	game := func(version, cabinet string, extra map[string]any) map[string]any {
		body := map[string]any{"game": version, "location": "1F", "quantity": 1, "price": creditPrice(500), "tag": []any{}}
		if cabinet != "" {
			body["cabinet"] = cabinet
		}
		for key, value := range extra {
			body[key] = value
		}
		return body
	}
	changelogItems := func(expected int) map[string]map[string]any {
		t.Helper()
		rows, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:arcade} && changed='game'", "-created", 1, 0, map[string]any{"arcade": arcade.Id})
		if err != nil || len(rows) != 1 {
			t.Fatalf("game changelog rows: %v %v", len(rows), err)
		}
		encoded, _ := json.Marshal(rows[0].Get("log"))
		var log struct {
			Type    string           `json:"type"`
			Version int              `json:"version"`
			Items   []map[string]any `json:"items"`
		}
		_ = json.Unmarshal(encoded, &log)
		if log.Type != "game_diff" || log.Version != 2 || len(log.Items) != expected {
			t.Fatalf("game changelog %s v%d with %d items, want %d", log.Type, log.Version, len(log.Items), expected)
		}
		out := map[string]map[string]any{}
		for _, item := range log.Items {
			out[item["entry_id"].(string)] = item
		}
		return out
	}
	storedRevision := func(batch, entry string) *core.Record {
		t.Helper()
		rec, err := app.FindFirstRecordByFilter("arcade_game_history", "batch={:batch} && entry={:entry}", map[string]any{"batch": batch, "entry": entry})
		if err != nil {
			t.Fatalf("revision %s/%s: %v", batch, entry, err)
		}
		return rec
	}
	// Absent inventory: explicit empty revision, empty item list, fixed capability.
	empty := request("GET", nil, 200)
	if empty["contract_version"] != float64(1) || !reflect.DeepEqual(empty["required_capabilities"], []any{"games.delta.v1"}) || empty["revision"] != "" || empty["arcade"] != arcade.Id || !reflect.DeepEqual(empty["items"], []any{}) {
		t.Fatal(empty)
	}

	// Request shape: revision and at least one operation, strict JSON, documented codes.
	rejected(map[string]any{}, "revision_and_operations_required")
	rejected(map[string]any{"revision": ""}, "revision_and_operations_required")
	rejected(map[string]any{"revision": nil, "add": []any{game(v1a, "", nil)}}, "revision_and_operations_required")
	rejected(map[string]any{"add": []any{game(v1a, "", nil)}}, "revision_and_operations_required")
	rejected(patch("", map[string]any{"add": []any{}, "update": []any{}, "remove": []any{}}), "revision_and_operations_required")
	rejected([]byte(`{"revision":"","add":[{"game":"x","quantity":1,"price":{}}]} {}`), "invalid_patch")
	rejected([]byte(`{"revision":"","add":[],"extra":1}`), "invalid_patch")
	rejected([]byte(`{"revision":"","add":[null]}`), "invalid_patch")
	rejected([]byte(`{"revision":"","add":"x"}`), "invalid_patch")
	rejected([]byte(`{"revision":"","update":[{"id":"x","changes":{"quantity":1},"extra":1}]}`), "invalid_patch")
	rejected([]byte(`{"revision":"","update":[{"id":"x","changes":[]}]}`), "invalid_patch")
	rejected([]byte(`{"revision":1,"add":[]}`), "invalid_patch")
	rejected([]byte(`not json`), "invalid_patch")
	rejected([]byte(``), "invalid_patch")
	// Add object: required members, exact types, no unknown keys anywhere.
	rejected(add("", game(v1a, "", map[string]any{"future": 1})), "unsupported_field")
	rejected(add("", game(v1a, "", map[string]any{"id": "x"})), "unsupported_field")
	rejected(add("", map[string]any{"quantity": 1, "price": creditPrice(500)}), "invalid_game_version")
	for _, version := range []any{"", " ", nil, 1, []any{}} {
		rejected(add("", game(v1a, "", map[string]any{"game": version})), "invalid_game_version")
	}
	rejected(add("", map[string]any{"game": v1a, "price": creditPrice(500)}), "invalid_quantity")
	for _, quantity := range []any{0, -1, 1.5, "1", nil, true, 1e10} {
		rejected(add("", game(v1a, "", map[string]any{"quantity": quantity})), "invalid_quantity")
	}
	rejected(add("", map[string]any{"game": v1a, "quantity": 1}), "invalid_price")
	for _, cabinet := range []any{nil, 1, []any{}} {
		rejected(add("", game(v1a, "", map[string]any{"cabinet": cabinet})), "invalid_cabinet")
	}
	for _, location := range []any{nil, 1, strings.Repeat("가", 501)} {
		rejected(add("", game(v1a, "", map[string]any{"location": location})), "invalid_location")
	}
	// Price: exact web object, shared validatePrice policy, no unknown members.
	for _, price := range []any{
		nil, "500", []any{}, map[string]any{},
		map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"value": 500}}},
		map[string]any{"currency": "", "type": "credit", "list": []any{map[string]any{"value": 500}}, "accept": []any{}},
		map[string]any{"currency": 1, "type": "credit", "list": []any{map[string]any{"value": 500}}, "accept": []any{}},
		map[string]any{"currency": "KRW", "type": "legacy", "list": []any{map[string]any{"value": 500}}, "accept": []any{}},
		map[string]any{"currency": "KRW", "type": "credit", "list": []any{}, "accept": []any{}},
		map[string]any{"currency": "KRW", "type": "credit", "list": nil, "accept": []any{}},
		map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"value": 0}}, "accept": []any{}},
		map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"value": -5}}, "accept": []any{}},
		map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"title": true, "value": 500}}, "accept": []any{}},
		map[string]any{"currency": "KRW", "type": "credit", "list": []any{"1 credit 500"}, "accept": []any{}},
		map[string]any{"currency": "KRW", "type": "custom_v2", "list": []any{map[string]any{"value": 500}}, "accept": []any{}},
		map[string]any{"currency": "KRW", "type": "custom_v2", "list": []any{" "}, "accept": []any{}},
		map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"value": 500}}, "accept": []any{"Bitcoin"}},
		map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"value": 500}}, "accept": nil},
	} {
		rejected(add("", game(v1a, "", map[string]any{"price": price})), "invalid_price")
	}
	rejected(add("", game(v1a, "", map[string]any{"price": map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"value": 500}}, "accept": []any{}, "future": 1}})), "unsupported_field")
	rejected(add("", game(v1a, "", map[string]any{"price": map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"value": 500, "note": "x"}}, "accept": []any{}}})), "unsupported_field")
	// Tags: exact {category, note} rows with the shared enum/note policy.
	for _, tag := range []any{nil, "x", map[string]any{}, []any{nil}, []any{"x"}, []any{map[string]any{"category": "기타"}}, []any{map[string]any{"category": "기타", "note": ""}}, []any{map[string]any{"category": "기타", "note": " "}}, []any{map[string]any{"category": "Other", "note": "x"}}, []any{map[string]any{"category": "기타", "note": 1}}} {
		rejected(add("", game(v1a, "", map[string]any{"tag": tag})), "invalid_tag")
	}
	rejected(add("", game(v1a, "", map[string]any{"tag": []any{map[string]any{"category": "기타", "note": "x", "future": 1}}})), "unsupported_field")
	// Update/remove identity rules apply before the first revision exists.
	rejected(update("", "", map[string]any{"quantity": 2}), "invalid_game_id")
	rejected(update("", "missing", map[string]any{}), "invalid_game_id")
	rejected(update("", "missing", map[string]any{"quantity": 2}), "invalid_game_id")
	rejected(update("", "missing", map[string]any{"series": series2}), "unsupported_field")
	rejected(update("", "missing", map[string]any{"id": "other"}), "unsupported_field")
	rejected(update("", "missing", map[string]any{"quantity": 0}), "invalid_quantity")
	rejected(patch("", map[string]any{"remove": []string{"missing"}}), "invalid_game_id")
	rejected(patch("", map[string]any{"remove": []string{""}}), "invalid_game_id")
	rejected(patch("", map[string]any{"remove": []string{"a", "a"}}), "invalid_game_id")
	rejected(patch("", map[string]any{"update": []any{map[string]any{"id": "a", "changes": map[string]any{"quantity": 1}}, map[string]any{"id": "a", "changes": map[string]any{"quantity": 2}}}}), "invalid_game_id")
	rejected(patch("", map[string]any{"update": []any{map[string]any{"id": "a", "changes": map[string]any{"quantity": 1}}}, "remove": []string{"a"}}), "invalid_game_id")
	// Catalog references: version, cabinet existence, version/cabinet compatibility, uniqueness.
	rejected(add("", game("missing_version", "", nil)), "invalid_game_version")
	rejected(add("", game(v1a, "missing_cabinet", nil)), "invalid_cabinet")
	rejected(add("", game(v1a, cabNone, nil)), "cabinet_not_supported")
	rejected(add("", game(v2, cabDX, nil)), "cabinet_not_supported")
	rejected(add("", game(v1a, cabDX, nil), game(v1a, cabDX, map[string]any{"location": "2F"})), "duplicate_game")
	rejected(add("", game(v1a, "", nil), game(v1a, "", map[string]any{"location": "2F"})), "duplicate_game")
	if request("GET", nil, 200)["revision"] != "" {
		t.Fatal("rejected requests created a revision")
	}

	// First save: explicit adds only; location optional, tags optional, custom_v2 lines trimmed.
	before := take()
	saved := request("PATCH", add("",
		game(v1a, cabDX, map[string]any{"location": " 1F ", "quantity": 2, "tag": []any{map[string]any{"category": "모니터", "note": "120Hz"}}}),
		map[string]any{"game": v2, "quantity": 1, "price": map[string]any{"currency": "JPY", "type": "custom_v2", "list": []any{" 1 play 100 yen ", "3 plays 200 yen"}, "accept": []any{"Cash", "Prepaid Card"}}},
	), 200)
	if saved["arcade"] != arcade.Id || saved["revision"] == "" || saved["count"] != float64(2) || saved["xp_feedback"] == nil {
		t.Fatal(saved)
	}
	if after := take(); after.batches != before.batches+1 || after.revisions != before.revisions+2 || after.entries != before.entries+2 || after.logs != before.logs+1 || after.xp != before.xp+1 {
		t.Fatalf("first save counts: before %+v after %+v", before, after)
	}
	if saved["xp_feedback"].(map[string]any)["diff_exp"] != float64(4) {
		t.Fatal(saved["xp_feedback"])
	}
	revision, current := items()
	if revision != saved["revision"] || len(current) != 2 {
		t.Fatal(current)
	}
	maimai, chunithm := byVersion(current, v1a), byVersion(current, v2)
	if maimai["series"] != series1 || maimai["cabinet"] != cabDX || maimai["location"] != " 1F " || maimai["quantity"] != float64(2) || maimai["updated"] == "" {
		t.Fatal(maimai)
	}
	if !reflect.DeepEqual(maimai["price"], map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"title": "1 credit", "value": float64(500)}}, "accept": []any{"Cash"}}) {
		t.Fatalf("stored price: %v", maimai["price"])
	}
	if !reflect.DeepEqual(maimai["tag"], []any{map[string]any{"category": "모니터", "note": "120Hz"}}) {
		t.Fatalf("stored tag: %v", maimai["tag"])
	}
	catalog := maimai["catalog"].(map[string]any)
	if catalog["version"].(map[string]any)["en"] != "Maimai 2026" || catalog["series"].(map[string]any)["kr"] != "마이마이" || catalog["cabinet"].(map[string]any)["id"] != cabDX {
		t.Fatalf("catalog bundle: %v", catalog)
	}
	if chunithm["series"] != series2 || chunithm["cabinet"] != "" || chunithm["location"] != "" || !reflect.DeepEqual(chunithm["tag"], []any{}) || chunithm["catalog"].(map[string]any)["cabinet"] != nil {
		t.Fatal(chunithm)
	}
	if !reflect.DeepEqual(chunithm["price"], map[string]any{"currency": "JPY", "type": "custom_v2", "list": []any{"1 play 100 yen", "3 plays 200 yen"}, "accept": []any{"Cash", "Prepaid Card"}}) {
		t.Fatalf("custom_v2 price: %v", chunithm["price"])
	}
	for _, item := range changelogItems(2) {
		if item["change_type"] != "added" || item["before"] != nil {
			t.Fatal(item)
		}
	}
	maimaiID, chunithmID := maimai["id"].(string), chunithm["id"].(string)
	firstRevision := revision

	// Stale base revision (including the pre-save empty token) is a 409 with no writes.
	before = take()
	if got := request("PATCH", add("", game(v1b, "", nil)), 409)["error"]; got != "revision_conflict" {
		t.Fatal(got)
	}
	if got := request("PATCH", patch("stale", map[string]any{"remove": []string{maimaiID}}), 409)["error"]; got != "revision_conflict" {
		t.Fatal(got)
	}
	unchanged(before, "stale revision")

	// No-ops: equal values after normalization create no revision, history or XP.
	rejected(update(revision, maimaiID, map[string]any{"quantity": 2}), "no_changes")
	rejected(update(revision, maimaiID, map[string]any{"location": " 1F ", "cabinet": cabDX, "game": v1a}), "no_changes")
	rejected(update(revision, maimaiID, map[string]any{"price": map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"title": "1 credit", "value": 500, "represent": false}}, "accept": []any{"Cash"}}}), "no_changes")
	rejected(update(revision, maimaiID, map[string]any{"tag": []any{map[string]any{"category": "모니터", "note": "120Hz"}}}), "no_changes")
	rejected(update(revision, chunithmID, map[string]any{"tag": []any{}, "cabinet": ""}), "no_changes")
	rejected(patch(revision, map[string]any{"update": []any{map[string]any{"id": maimaiID, "changes": map[string]any{"quantity": 2}}, map[string]any{"id": chunithmID, "changes": map[string]any{"location": ""}}}}), "no_changes")
	// Policy parity on updates: same series only, compatible cabinets, uniqueness.
	rejected(update(revision, maimaiID, map[string]any{"game": v2}), "series_mismatch")
	rejected(update(revision, maimaiID, map[string]any{"game": "missing"}), "invalid_game_version")
	rejected(update(revision, maimaiID, map[string]any{"cabinet": "missing"}), "invalid_cabinet")
	rejected(update(revision, maimaiID, map[string]any{"cabinet": cabNone}), "cabinet_not_supported")
	rejected(update(revision, maimaiID, map[string]any{"game": v1b, "cabinet": cabSD}), "cabinet_not_supported")
	rejected(update(revision, chunithmID, map[string]any{"cabinet": cabDX}), "cabinet_not_supported")
	rejected(add(revision, game(v1a, cabDX, nil)), "duplicate_game")
	rejected(add(revision, game(v2, "", nil)), "duplicate_game")
	rejected(update(revision, maimaiID, map[string]any{"quantity": nil}), "invalid_quantity")
	rejected(update(revision, maimaiID, map[string]any{"price": nil}), "invalid_price")
	rejected(update(revision, maimaiID, map[string]any{"tag": nil}), "invalid_tag")
	rejected(update(revision, maimaiID, map[string]any{"location": nil}), "invalid_location")
	if got, _ := items(); got != revision {
		t.Fatal("rejected requests changed the revision")
	}

	// Simulate server evolution: a future history column and unknown members in
	// stored price/tag JSON. A changes-only update must keep all of it, and
	// untouched entries are copied verbatim with stable ids.
	historyCollection, _ := app.FindCollectionByNameOrId("arcade_game_history")
	historyCollection.Fields.Add(&core.TextField{Name: "future_policy"})
	if err = app.Save(historyCollection); err != nil {
		t.Fatal(err)
	}
	futurePrice := map[string]any{"currency": "JPY", "type": "custom_v2", "list": []any{"1 play 100 yen", "3 plays 200 yen"}, "accept": []any{"Cash", "Prepaid Card"}, "future_member": map[string]any{"k": float64(1)}}
	futureTag := []any{map[string]any{"category": "기타", "note": "keep", "future": "yes"}}
	for entry, policy := range map[string]string{maimaiID: "maimai policy", chunithmID: "chunithm policy"} {
		rec := storedRevision(revision, entry)
		rec.Set("future_policy", policy)
		if entry == chunithmID {
			rec.Set("price", futurePrice)
			rec.Set("tag", futureTag)
		}
		if err = app.Save(rec); err != nil {
			t.Fatal(err)
		}
	}
	_, current = items()
	if !reflect.DeepEqual(current[chunithmID]["price"], futurePrice) || !reflect.DeepEqual(current[chunithmID]["tag"], futureTag) {
		t.Fatalf("GET hides future JSON: %v", current[chunithmID])
	}
	before = take()
	saved = request("PATCH", update(revision, maimaiID, map[string]any{"quantity": 3}), 200)
	if after := take(); after.batches != before.batches+1 || after.revisions != before.revisions+2 || after.entries != before.entries || after.logs != before.logs+1 || after.xp != before.xp {
		t.Fatalf("quantity update counts: before %+v after %+v", before, after)
	}
	if saved["xp_feedback"].(map[string]any)["diff_exp"] != float64(0) {
		t.Fatalf("already-counted entry must award 0 XP: %v", saved["xp_feedback"])
	}
	revision, current = items()
	if revision != saved["revision"] || revision == firstRevision || len(current) != 2 || current[maimaiID] == nil || current[chunithmID] == nil {
		t.Fatalf("entry ids must be stable across saves: %v", current)
	}
	if current[maimaiID]["quantity"] != float64(3) || current[maimaiID]["location"] != " 1F " || current[maimaiID]["cabinet"] != cabDX || !reflect.DeepEqual(current[maimaiID]["price"], maimai["price"]) || !reflect.DeepEqual(current[maimaiID]["tag"], maimai["tag"]) {
		t.Fatalf("changes-only update altered omitted fields: %v", current[maimaiID])
	}
	if !reflect.DeepEqual(current[chunithmID]["price"], futurePrice) || !reflect.DeepEqual(current[chunithmID]["tag"], futureTag) || current[chunithmID]["quantity"] != float64(1) {
		t.Fatalf("untouched entry was flattened: %v", current[chunithmID])
	}
	for entry, policy := range map[string]string{maimaiID: "maimai policy", chunithmID: "chunithm policy"} {
		if got := storedRevision(revision, entry).GetString("future_policy"); got != policy {
			t.Fatalf("%s lost future column: %q", entry, got)
		}
	}
	log := changelogItems(2)
	if log[maimaiID]["change_type"] != "updated" || log[chunithmID]["change_type"] != "unchanged" {
		t.Fatal(log)
	}
	if after := log[maimaiID]["after"].(map[string]any); after["quantity"] != float64(3) || log[maimaiID]["before"].(map[string]any)["quantity"] != float64(2) {
		t.Fatal(log[maimaiID])
	}
	// Immutable history: the first batch and its revisions are untouched.
	if rec := storedRevision(firstRevision, maimaiID); rec.GetInt("quantity") != 2 {
		t.Fatal("previous revision was mutated")
	}
	if rows, _ := app.FindRecordsByFilter("arcade_game_history", "batch={:batch}", "", 0, 0, map[string]any{"batch": firstRevision}); len(rows) != 2 {
		t.Fatalf("first batch rows: %d", len(rows))
	}

	// Updating an entry whose stored price carries unknown members keeps them
	// when price is omitted from `changes` (merged object is revalidated).
	request("PATCH", update(revision, chunithmID, map[string]any{"location": "B1"}), 200)
	revision, current = items()
	if current[chunithmID]["location"] != "B1" || !reflect.DeepEqual(current[chunithmID]["price"], futurePrice) || !reflect.DeepEqual(current[chunithmID]["tag"], futureTag) {
		t.Fatalf("location update dropped unknown price/tag members: %v", current[chunithmID])
	}
	// A provided price replaces the stored object wholesale (web parity).
	request("PATCH", update(revision, chunithmID, map[string]any{"price": creditPrice(600), "tag": []any{}}), 200)
	revision, current = items()
	if !reflect.DeepEqual(current[chunithmID]["price"], map[string]any{"currency": "KRW", "type": "credit", "list": []any{map[string]any{"title": "1 credit", "value": float64(600)}}, "accept": []any{"Cash"}}) || !reflect.DeepEqual(current[chunithmID]["tag"], []any{}) {
		t.Fatalf("price/tag replace: %v", current[chunithmID])
	}
	// Legacy stored price that fails the current policy: an unrelated update is
	// rejected like a web modify; sending a valid full price in the same update passes.
	legacy := storedRevision(revision, chunithmID)
	legacy.Set("price", map[string]any{"currency": "KRW", "type": "legacy", "list": []any{}, "accept": []any{}})
	if err = app.Save(legacy); err != nil {
		t.Fatal(err)
	}
	rejected(update(revision, chunithmID, map[string]any{"quantity": 2}), "invalid_price")
	// ...while an untouched legacy entry never blocks edits of other entries.
	request("PATCH", update(revision, maimaiID, map[string]any{"location": "2F"}), 200)
	revision, current = items()
	if current[chunithmID]["price"].(map[string]any)["type"] != "legacy" {
		t.Fatalf("untouched legacy price was altered: %v", current[chunithmID])
	}
	request("PATCH", update(revision, chunithmID, map[string]any{"quantity": 2, "price": creditPrice(700)}), 200)
	revision, current = items()
	if current[chunithmID]["quantity"] != float64(2) || current[chunithmID]["price"].(map[string]any)["type"] != "credit" {
		t.Fatal(current[chunithmID])
	}

	// Version within the same series, cabinet clear, tag replace, price title as number.
	request("PATCH", update(revision, maimaiID, map[string]any{"game": v1b, "cabinet": "", "tag": []any{map[string]any{"category": "기타", "note": " 확인 "}}, "price": map[string]any{"currency": "KRW", "type": "gamemode", "list": []any{map[string]any{"title": 2, "value": 1000, "mode_key": "standard", "represent": true}, map[string]any{"title": "Premium", "value": 1500}}, "accept": []any{}}}), 200)
	revision, current = items()
	if current[maimaiID] == nil || current[maimaiID]["game"] != v1b || current[maimaiID]["series"] != series1 || current[maimaiID]["cabinet"] != "" || current[maimaiID]["quantity"] != float64(3) {
		t.Fatalf("same-series version update: %v", current[maimaiID])
	}
	if !reflect.DeepEqual(current[maimaiID]["tag"], []any{map[string]any{"category": "기타", "note": " 확인 "}}) || !reflect.DeepEqual(current[maimaiID]["price"], map[string]any{"currency": "KRW", "type": "gamemode", "list": []any{map[string]any{"title": float64(2), "value": float64(1000), "mode_key": "standard", "represent": true}, map[string]any{"title": "Premium", "value": float64(1500)}}, "accept": []any{}}) {
		t.Fatalf("tag/price update: %v", current[maimaiID])
	}
	if got := changelogItems(2)[maimaiID]; got["change_type"] != "updated" || got["before"].(map[string]any)["version"] != v1a || got["after"].(map[string]any)["version"] != v1b {
		t.Fatal(got)
	}
	// cabSD is compatible with v1a only; the entry now runs v1b.
	rejected(update(revision, maimaiID, map[string]any{"cabinet": cabSD}), "cabinet_not_supported")
	request("PATCH", update(revision, maimaiID, map[string]any{"cabinet": cabSD, "game": v1a}), 200)
	revision, current = items()
	if current[maimaiID]["cabinet"] != cabSD || current[maimaiID]["game"] != v1a {
		t.Fatal(current[maimaiID])
	}

	// Explicit removal with a replace in one request; removed ids leave the active set.
	before = take()
	saved = request("PATCH", patch(revision, map[string]any{"remove": []string{chunithmID}, "add": []any{game(v2, "", map[string]any{"location": "3F", "quantity": 4})}}), 200)
	if after := take(); after.logs != before.logs+1 || after.revisions != before.revisions+2 || after.batches != before.batches+1 {
		t.Fatalf("remove/add counts: before %+v after %+v", before, after)
	}
	revision, current = items()
	readded := byVersion(current, v2)
	if len(current) != 2 || saved["count"] != float64(2) || readded["location"] != "3F" || readded["quantity"] != float64(4) {
		t.Fatalf("remove + add: %v", current)
	}
	if readded["id"] == chunithmID {
		t.Fatal("unverified cabinet must not reuse the removed identity")
	}
	log = changelogItems(3)
	if log[chunithmID]["change_type"] != "deleted" || log[chunithmID]["after"] != nil || log[chunithmID]["before"].(map[string]any)["location"] != "B1" || log[readded["id"].(string)]["change_type"] != "added" || log[maimaiID]["change_type"] != "unchanged" {
		t.Fatal(log)
	}
	rejected(patch(revision, map[string]any{"remove": []string{chunithmID}}), "invalid_game_id")
	rejected(update(revision, chunithmID, map[string]any{"quantity": 1}), "invalid_game_id")
	// Web identity rule: removing and re-adding the same series + verified cabinet reuses the entry id.
	request("PATCH", patch(revision, map[string]any{"remove": []string{maimaiID}}), 200)
	revision, current = items()
	if len(current) != 1 || current[maimaiID] != nil {
		t.Fatal(current)
	}
	request("PATCH", add(revision, game(v1b, cabDX, nil)), 200)
	revision, current = items()
	if byVersion(current, v1b)["id"] != maimaiID {
		t.Fatalf("same series + verified cabinet must reuse %s: %v", maimaiID, current)
	}
	// Removing everything is an explicit, valid state.
	request("PATCH", patch(revision, map[string]any{"remove": []string{maimaiID, readded["id"].(string)}}), 200)
	revision, current = items()
	if revision == "" || len(current) != 0 {
		t.Fatal(current)
	}
	request("PATCH", add(revision, game(v1a, cabDX, nil)), 200)
	revision, current = items()
	if len(current) != 1 || byVersion(current, v1a)["id"] != maimaiID {
		t.Fatalf("reused identity after full removal: %v", current)
	}

	// Body limit evidence: a realistic add object stays well under the shared
	// 4096-byte native ceiling; an oversized request is rejected by the group.
	realistic := game(v1a, cabDX, map[string]any{"location": "2F 안쪽 벽면", "tag": []any{map[string]any{"category": "모니터", "note": "120Hz"}, map[string]any{"category": "기타", "note": "확인 완료"}}, "price": map[string]any{"currency": "KRW", "type": "gamemode", "list": []any{map[string]any{"title": "Standard", "value": 1000, "mode_key": "standard", "represent": true}, map[string]any{"title": "Premium", "value": 1500, "mode_key": "premium"}}, "accept": []any{"Cash", "Credit Card"}}})
	encodedAdd, _ := json.Marshal(add(revision, realistic))
	t.Logf("realistic single-add PATCH body: %d bytes", len(encodedAdd))
	if len(encodedAdd) > 1024 {
		t.Fatalf("realistic add unexpectedly large: %d bytes", len(encodedAdd))
	}
	before = take()
	request("PATCH", add(revision, game(v1b, "", map[string]any{"location": strings.Repeat("x", 4200)})), 413)
	unchanged(before, "oversized body")

	// A game state pointing at another arcade is neither exposed nor copied.
	other := seedVisitArcade(t, app, "Asia/Seoul")
	batchCollection, _ := app.FindCollectionByNameOrId("arcade_game_history_batch")
	otherBatch := core.NewRecord(batchCollection)
	otherBatch.Set("arcade", other.Id)
	otherBatch.Set("reason", "edit")
	if err = app.Save(otherBatch); err != nil {
		t.Fatal(err)
	}
	arcade, _ = app.FindRecordById("arcade", arcade.Id)
	arcade.Set("game_v2", otherBatch.Id)
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	before = take()
	if got := request("GET", nil, 500)["error"]; got != "games_unavailable" {
		t.Fatal(got)
	}
	if got := request("PATCH", add(otherBatch.Id, game(v1b, "", nil)), 502)["error"]; got != "games_unavailable" {
		t.Fatal(got)
	}
	unchanged(before, "mispointed revision")
	arcade.Set("game_v2", revision)
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}

	// Authorization: native bearer only, same arcade write rules as the web.
	requestArcade("missing", "GET", nil, 404)
	requestArcade("missing", "PATCH", add("", game(v1a, "", nil)), 404)
	token = web
	request("GET", nil, 401)
	request("PATCH", add(revision, game(v1b, "", nil)), 401)
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
	if got := request("PATCH", add(revision, game(v1b, "", nil)), 403)["error"]; got != "arcade_write_forbidden" {
		t.Fatal(got)
	}
	unchanged(before, "forbidden write")
	// The creator may edit a private draft, and private edits earn no XP.
	arcade.Set("createdBy", user.Id)
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	draft := request("PATCH", add(revision, game(v1b, "", nil)), 200)
	if after := take(); after.xp != before.xp || after.logs != before.logs+1 || draft["xp_feedback"].(map[string]any)["diff_exp"] != float64(0) {
		t.Fatalf("private draft edit: before %+v after %+v %v", before, after, draft["xp_feedback"])
	}
	if _, current = items(); len(current) != 2 {
		t.Fatal(current)
	}
}
