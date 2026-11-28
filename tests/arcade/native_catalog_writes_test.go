package arcade_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

var nativeCatalogWriteOps = []struct{ method, path string }{{"POST", "/moderation/game/catalog"}, {"PUT", "/moderation/game/catalog"}, {"DELETE", "/moderation/game/catalog"}, {"POST", "/moderation/game/catalog/restore"}, {"POST", "/moderation/game/catalog/compatibilities"}, {"POST", "/moderation/game/catalog/changes/revert"}}

func nativeCatalogWriteState(t *testing.T, app core.App) map[string]string {
	t.Helper()
	state := nativeBulkState(t, app)
	for _, name := range []string{"game_manufacturer", "game_series", "game_series_version", "game_cabinet", "game_series_version_cabinet", "game_catalog_changelog"} {
		rows, err := app.FindAllRecords(name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		state[name] = string(raw)
	}
	return state
}
func exportNativeCatalogWrite(t *testing.T, name string, cases map[string]any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_CATALOG_WRITE_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(cases, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "email", "access_token", "refresh_token"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+key+`"`) {
			t.Fatal("credential in catalog wire fixture")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func catalogOperation() string { return strings.ToUpper(uuid.NewString()) }

func TestCanonicalNativeCatalogWritesKeepWebAuditReplayRevisionsAndRollback(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, user := createAuthUserWithTags(t, app, []string{"moderator"})
	_, session := nativeNearbySession(t, app, user)
	_, other := createAuthUserWithTags(t, app, []string{"moderator"})
	_, foreign := nativeNearbySession(t, app, other)
	cases := map[string]any{}
	beforeXP := nativeBulkState(t, app)
	perform := func(name, method, path string, body map[string]any) map[string]any {
		t.Helper()
		value := nativeBulkRequest(t, app, method, path, session.AccessToken, body, 200).(map[string]any)
		change := value["change"].(map[string]any)
		if change["actor"] != user.Id || change["operation_id"] != body["operation_id"] {
			t.Fatal("catalog actor/operation was not server bound")
		}
		cases[name] = map[string]any{"method": method, "path": path, "request": body, "response": value, "status": 200, "actor": user.Id}
		count, err := app.CountRecords("game_catalog_changelog")
		if err != nil {
			t.Fatal(err)
		}
		replay := nativeBulkRequest(t, app, method, path, web, body, 200).(map[string]any)
		if replay["replayed"] != true || !reflect.DeepEqual(value["item"], replay["item"]) || !reflect.DeepEqual(change, replay["change"]) {
			t.Fatal("Web/Native catalog replay changed immutable receipt")
		}
		after, err := app.CountRecords("game_catalog_changelog")
		if err != nil || after != count {
			t.Fatal("replay created another audit")
		}
		nativeBulkRequest(t, app, method, path, foreign.AccessToken, body, 409)
		if !reflect.DeepEqual(beforeXP, nativeBulkState(t, app)) {
			t.Fatal("catalog edit awarded XP or changed arcade history")
		}
		return value
	}
	createBody := map[string]any{"entity": "manufacturer", "operation_id": catalogOperation(), "reason": "Synthetic canonical manufacturer", "values": map[string]any{"en": "Original maker", "kr": "원래 제조사", "jp": "元メーカー"}}
	created := perform("create", "POST", "/moderation/game/catalog", createBody)
	id := created["item"].(map[string]any)["id"].(string)
	updateBody := map[string]any{"entity": "manufacturer", "id": id, "operation_id": catalogOperation(), "expected_revision": 1, "reason": "Synthetic canonical name correction", "values": map[string]any{"en": "Updated maker", "kr": "수정 제조사", "jp": "更新メーカー"}}
	updated := perform("update", "PUT", "/moderation/game/catalog", updateBody)
	stale := map[string]any{"entity": "manufacturer", "id": id, "operation_id": catalogOperation(), "expected_revision": 1, "reason": "Synthetic stale revision attempt", "values": updateBody["values"]}
	state := nativeCatalogWriteState(t, app)
	conflict := nativeBulkRequest(t, app, "PUT", "/moderation/game/catalog", session.AccessToken, stale, 409)
	cases["stale-conflict"] = map[string]any{"method": "PUT", "path": "/moderation/game/catalog", "request": stale, "response": conflict, "status": 409, "actor": user.Id}
	changedBody := map[string]any{}
	for k, v := range updateBody {
		changedBody[k] = v
	}
	changedBody["reason"] = "Different payload same operation"
	nativeBulkRequest(t, app, "PUT", "/moderation/game/catalog", session.AccessToken, changedBody, 409)
	if !reflect.DeepEqual(state, nativeCatalogWriteState(t, app)) {
		t.Fatal("stale/operation conflict changed catalog")
	}
	archive := perform("archive", "DELETE", "/moderation/game/catalog", map[string]any{"entity": "manufacturer", "id": id, "operation_id": catalogOperation(), "expected_revision": 2, "reason": "Archive unused synthetic manufacturer"})
	if archive["item"].(map[string]any)["archived"] != true {
		t.Fatal("archive hard-deleted or did not mark row")
	}
	perform("restore", "POST", "/moderation/game/catalog/restore", map[string]any{"entity": "manufacturer", "id": id, "operation_id": catalogOperation(), "expected_revision": 3, "reason": "Restore synthetic manufacturer"})
	target := updated["change"].(map[string]any)["id"].(string)
	reverted := perform("revert", "POST", "/moderation/game/catalog/changes/revert", map[string]any{"entity": "manufacturer", "id": id, "operation_id": catalogOperation(), "expected_revision": 4, "change_id": target, "reason": "Revert synthetic name correction"})
	if reverted["item"].(map[string]any)["en"] != "Original maker" || reverted["item"].(map[string]any)["revision"] != float64(5) || reverted["change"].(map[string]any)["reverts_change"] != target {
		t.Fatalf("revert contract: original_name_restored=%v revision=%v target_reference_matches=%v archived=%v", reverted["item"].(map[string]any)["en"] == "Original maker", reverted["item"].(map[string]any)["revision"], reverted["change"].(map[string]any)["reverts_change"] == target, reverted["item"].(map[string]any)["archived"])
	}
	if _, err := app.FindRecordById("game_catalog_changelog", target); err != nil {
		t.Fatal("revert erased original audit")
	}
	// The same create operation still returns its original snapshot after later edits.
	replay := nativeBulkRequest(t, app, "POST", "/moderation/game/catalog", session.AccessToken, createBody, 200).(map[string]any)
	if !reflect.DeepEqual(replay["item"], created["item"]) {
		t.Fatal("late replay returned mutable current row")
	}
	if !reflect.DeepEqual(beforeXP, nativeBulkState(t, app)) {
		t.Fatal("catalog lifecycle changed XP or arcade history")
	}
	exportNativeCatalogWrite(t, "catalog-mutation-cases", cases)
}

func TestCanonicalNativeCatalogCompatibilityKeepsDerivedIntentIDsAtomicityAndNoOp(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, user := createAuthUserWithTags(t, app, []string{"moderator"})
	_, session := nativeNearbySession(t, app, user)
	series := seedGameSeries(t, app, 74001, "Synthetic compatibility series")
	version := seedGameSeriesVersionWithSeries(t, app, series, "2026-01-01", "Synthetic compatibility version")
	oldA := seedCatalogCabinet(t, app, series, "Old A")
	oldB := seedCatalogCabinet(t, app, series, "Old B")
	newA := seedCatalogCabinet(t, app, series, "New A")
	newB := seedCatalogCabinet(t, app, series, "New B")
	oldIDs := []string{seedCatalogCompatibility(t, app, version, oldA), seedCatalogCompatibility(t, app, version, oldB)}
	existing := []map[string]any{}
	for _, id := range oldIDs {
		row, err := app.FindRecordById("game_series_version_cabinet", id)
		if err != nil {
			t.Fatal(err)
		}
		row.Set("price_default", map[string]any{"future_snapshot": map[string]any{"retained": true}})
		if err := app.Save(row); err != nil {
			t.Fatal(err)
		}
		existing = append(existing, map[string]any{"id": id, "cabinet": row.GetString("cabinet"), "revision": 1})
	}
	sort.Slice(existing, func(i, j int) bool { return existing[i]["id"].(string) < existing[j]["id"].(string) })
	wanted := []string{newA, newB}
	sort.Strings(wanted)
	body := map[string]any{"operation_id": catalogOperation(), "reason": "Replace synthetic compatible cabinets", "version_id": version, "cabinet_ids": wanted, "expected_compatibilities": existing}
	beforeXP := nativeBulkState(t, app)
	value := nativeBulkRequest(t, app, "POST", "/moderation/game/catalog/compatibilities", session.AccessToken, body, 200).(map[string]any)
	changes := value["changes"].([]any)
	if len(changes) != 4 || value["replayed"] != false {
		t.Fatal("multi-intent compatibility reconciliation changed")
	}
	for _, raw := range changes {
		change := raw.(map[string]any)
		after := change["after"].(map[string]any)
		intent := "activate"
		if change["action"] == "archive" {
			intent = "archive"
		}
		derived := uuid.NewSHA1(uuid.NameSpaceOID, []byte(body["operation_id"].(string)+":"+intent+":"+after["cabinet"].(string))).String()
		if change["operation_id"] != derived || change["actor"] != user.Id || change["entity_type"] != "compatibility" {
			t.Fatal("derived operation/actor/entity contract changed")
		}
	}
	cases := map[string]any{"compatibility-replace": map[string]any{"method": "POST", "path": "/moderation/game/catalog/compatibilities", "request": body, "response": value, "status": 200, "actor": user.Id}}
	byID := func(value any) map[string]any {
		result := map[string]any{}
		for _, raw := range value.([]any) {
			row := raw.(map[string]any)
			id := row["id"].(string)
			if _, exists := result[id]; exists {
				t.Fatal("duplicate compatibility identity")
			}
			result[id] = row
		}
		return result
	}
	// Reconciliation returns an unordered cabinet set; compare every snapshot by persistent ID.
	state := nativeCatalogWriteState(t, app)
	replay := nativeBulkRequest(t, app, "POST", "/moderation/game/catalog/compatibilities", web, body, 200).(map[string]any)
	if replay["replayed"] != true || len(replay["changes"].([]any)) != 0 || !reflect.DeepEqual(byID(replay["compatibilities"]), byID(value["compatibilities"])) || !reflect.DeepEqual(state, nativeCatalogWriteState(t, app)) {
		t.Fatal("compatibility replay changed item snapshots or persisted state")
	}
	cases["compatibility-replay"] = map[string]any{"method": "POST", "path": "/moderation/game/catalog/compatibilities", "request": body, "response": replay, "status": 200, "actor": user.Id}
	active := []map[string]any{}
	for _, raw := range value["compatibilities"].([]any) {
		row := raw.(map[string]any)
		if row["archived"] == true {
			if row["price_default"].(map[string]any)["future_snapshot"] == nil {
				t.Fatal("archive discarded unknown price snapshot")
			}
			continue
		}
		active = append(active, map[string]any{"id": row["id"], "cabinet": row["cabinet"], "revision": row["revision"]})
	}
	sort.Slice(active, func(i, j int) bool { return active[i]["id"].(string) < active[j]["id"].(string) })
	noop := map[string]any{}
	for k, v := range body {
		noop[k] = v
	}
	noop["operation_id"] = catalogOperation()
	noop["expected_compatibilities"] = active
	same := nativeBulkRequest(t, app, "POST", "/moderation/game/catalog/compatibilities", session.AccessToken, noop, 200).(map[string]any)
	if same["replayed"] != false || len(same["changes"].([]any)) != 0 || !reflect.DeepEqual(state, nativeCatalogWriteState(t, app)) {
		t.Fatal("zero-change replacement wrote audit/state")
	}
	cases["compatibility-noop"] = map[string]any{"method": "POST", "path": "/moderation/game/catalog/compatibilities", "request": noop, "response": same, "status": 200, "actor": user.Id}
	stale := map[string]any{}
	for k, v := range body {
		stale[k] = v
	}
	stale["operation_id"] = catalogOperation()
	nativeBulkRequest(t, app, "POST", "/moderation/game/catalog/compatibilities", session.AccessToken, stale, 409)
	if !reflect.DeepEqual(state, nativeCatalogWriteState(t, app)) || !reflect.DeepEqual(beforeXP, nativeBulkState(t, app)) {
		t.Fatal("stale replacement changed catalog/XP")
	}
	// Force an audit failure after a cabinet set mutation; transaction must restore every row.
	failing := map[string]any{}
	for k, v := range noop {
		failing[k] = v
	}
	failing["operation_id"] = catalogOperation()
	failing["cabinet_ids"] = []string{oldA}
	failureHook := app.OnRecordCreate("game_catalog_changelog").BindFunc(func(*core.RecordEvent) error { return errors.New("synthetic audit failure") })
	nativeBulkRequest(t, app, "POST", "/moderation/game/catalog/compatibilities", session.AccessToken, failing, 502)
	if !reflect.DeepEqual(state, nativeCatalogWriteState(t, app)) {
		t.Fatal("audit failure left partial compatibility changes")
	}
	app.OnRecordCreate("game_catalog_changelog").Unbind(failureHook)
	originalCabinets := []string{oldA, oldB}
	sort.Strings(originalCabinets)
	restoreBody := map[string]any{}
	for k, v := range noop {
		restoreBody[k] = v
	}
	restoreBody["operation_id"] = catalogOperation()
	restoreBody["cabinet_ids"] = originalCabinets
	restored := nativeBulkRequest(t, app, "POST", "/moderation/game/catalog/compatibilities", session.AccessToken, restoreBody, 200).(map[string]any)
	if len(restored["changes"].([]any)) != 4 {
		t.Fatal("restored cabinet intents missing")
	}
	restoredCount := 0
	for _, raw := range restored["changes"].([]any) {
		if raw.(map[string]any)["action"] == "restore" {
			restoredCount++
		}
	}
	if restoredCount != 2 || !reflect.DeepEqual(beforeXP, nativeBulkState(t, app)) {
		t.Fatal("compatibility restore changed XP or created new identities")
	}
	cases["compatibility-restore"] = map[string]any{"method": "POST", "path": "/moderation/game/catalog/compatibilities", "request": restoreBody, "response": restored, "status": 200, "actor": user.Id}
	exportNativeCatalogWrite(t, "catalog-compatibility-cases", cases)
}

func TestCanonicalNativeCatalogWritesKeepRoleAuthenticationAndBodyBoundaries(t *testing.T) {
	for _, test := range []struct {
		name        string
		tags        []string
		exp, status int
	}{{"ordinary30", nil, 300, 403}, {"designer", []string{"designer"}, 300, 403}, {"supporter29", []string{"supporter"}, 299, 403}, {"supporter30", []string{"supporter"}, 300, 400}, {"founding30", []string{"founding_supporter"}, 300, 400}, {"moderator", []string{"moderator"}, 0, 400}, {"developer", []string{"developer"}, 0, 400}} {
		t.Run(test.name, func(t *testing.T) {
			app := nativeNearbyApp(t, true)
			web, user := createAuthUserWithTags(t, app, test.tags)
			seedUserLevelExp(t, app, user.Id, test.exp)
			_, session := nativeNearbySession(t, app, user)
			before := nativeCatalogWriteState(t, app)
			for _, op := range nativeCatalogWriteOps {
				for _, token := range []string{web, session.AccessToken} {
					nativeBulkRequest(t, app, op.method, op.path, token, map[string]any{}, test.status)
				}
			}
			if !reflect.DeepEqual(before, nativeCatalogWriteState(t, app)) {
				t.Fatal("permission/validation denial mutated catalog")
			}
		})
	}
	app := nativeNearbyApp(t, true)
	web, user := createAuthUserWithTags(t, app, []string{"developer"})
	service, session := nativeNearbySession(t, app, user)
	for _, op := range nativeCatalogWriteOps {
		for _, token := range []string{"", strings.Repeat("z", 43)} {
			nativeBulkRequest(t, app, op.method, op.path, token, map[string]any{}, 401)
		}
		r := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal("invalid bearer fell back to cookie")
		}
		r = executeJSONRequest(t, app, op.method, op.path, strings.Repeat("x", (128<<10)+1), map[string]string{"Authorization": "Bearer " + session.AccessToken})
		r.Body.Close()
		if r.StatusCode != 413 {
			t.Fatal("catalog body limit changed")
		}
	}
	seedUserBan(t, app, user.Id, "", "catalog_write_canary", time.Now().Add(time.Hour))
	for _, op := range nativeCatalogWriteOps {
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 403)
	}
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	for _, op := range nativeCatalogWriteOps {
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 401)
	}
	disabled := nativeNearbyApp(t, false)
	_, actor := createAuthUserWithTags(t, disabled, []string{"developer"})
	valid, issued := nativeNearbySession(t, disabled, actor)
	if u, err := valid.Authenticate(disabled, issued.AccessToken); err != nil || u.Id != actor.Id {
		t.Fatal("disabled fixture is not valid sameDB session")
	}
	for _, op := range nativeCatalogWriteOps {
		r := executeJSONRequest(t, disabled, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + issued.AccessToken})
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal("disabled runtime accepted Native catalog writer")
		}
	}
}

func TestCanonicalNativeCatalogWritesKeepRateAndUnselectedCapabilities(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, user := createAuthUserWithTags(t, app, []string{"developer"})
	_, session := nativeNearbySession(t, app, user)
	for _, op := range nativeCatalogWriteOps {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: op.method + " " + op.path, MaxRequests: 1, Duration: 60}}}
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 400)
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 429)
	}
	for _, op := range nativeCatalogWriteOps {
		r := executeJSONRequest(t, app, op.method, "/native"+op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatal("duplicate Native domain route introduced")
		}
	}
}

func TestCanonicalNativeCatalogRevertRestoresEveryStoredActionWithoutRewritingHistory(t *testing.T) {
	for _, transport := range []string{"Web", "Native"} {
		for _, action := range []string{"create-missing", "create-null", "update", "archive", "restore"} {
			t.Run(transport+"/"+action, func(t *testing.T) {
				app := nativeNearbyApp(t, true)
				web, user := createAuthUserWithTags(t, app, []string{"moderator"})
				_, session := nativeNearbySession(t, app, user)
				body := map[string]any{"entity": "manufacturer", "operation_id": catalogOperation(), "reason": "Synthetic stored revert fixture", "values": map[string]any{"en": "Before maker", "kr": "이전 제조사", "jp": "以前メーカー"}}
				target := nativeBulkRequest(t, app, "POST", "/moderation/game/catalog", web, body, 200).(map[string]any)
				item := target["item"].(map[string]any)
				id := item["id"].(string)
				revision := 1
				if action == "create-null" {
					if _, err := app.DB().NewQuery(`UPDATE game_catalog_changelog SET "before"='null' WHERE id={:id}`).Bind(dbx.Params{"id": target["change"].(map[string]any)["id"]}).Execute(); err != nil {
						t.Fatal(err)
					}
				}
				steps := []string{}
				if action == "update" {
					steps = []string{"update"}
				} else if action == "archive" {
					steps = []string{"archive"}
				} else if action == "restore" {
					steps = []string{"archive", "restore"}
				}
				for _, step := range steps {
					request := map[string]any{"entity": "manufacturer", "id": id, "expected_revision": revision, "operation_id": catalogOperation(), "reason": "Synthetic stored revert fixture"}
					method, path := "DELETE", "/moderation/game/catalog"
					if step == "update" {
						method = "PUT"
						request["values"] = map[string]any{"en": "After maker", "kr": "이후 제조사", "jp": "以後メーカー"}
					} else if step == "restore" {
						method = "POST"
						path += "/restore"
					}
					target = nativeBulkRequest(t, app, method, path, web, request, 200).(map[string]any)
					revision++
				}
				rows, err := app.FindAllRecords("game_catalog_changelog")
				if err != nil {
					t.Fatal(err)
				}
				history := map[string]string{}
				for _, row := range rows {
					raw, err := json.Marshal(row)
					if err != nil {
						t.Fatal(err)
					}
					history[row.Id] = string(raw)
				}
				targetID := target["change"].(map[string]any)["id"].(string)
				beforeXP := nativeBulkState(t, app)
				token := web
				if transport == "Native" {
					token = session.AccessToken
				}
				reverted := nativeBulkRequest(t, app, "POST", "/moderation/game/catalog/changes/revert", token, map[string]any{"entity": "manufacturer", "id": id, "expected_revision": revision, "change_id": targetID, "operation_id": catalogOperation(), "reason": "Revert original synthetic snapshot"}, 200).(map[string]any)
				after := reverted["item"].(map[string]any)
				change := reverted["change"].(map[string]any)
				if after["id"] != id || after["revision"] != float64(revision+1) || change["reverts_change"] != targetID || change["actor"] != user.Id {
					t.Fatal("revert identity/revision/reference changed")
				}
				expected := target["change"].(map[string]any)["before"]
				if strings.HasPrefix(action, "create-") {
					expected = item
					if after["archived"] != true || after["archived_by"] != user.Id {
						t.Fatal("create revert failed to archive retained row")
					}
				}
				for key, value := range expected.(map[string]any) {
					if key == "revision" || key == "updated" || (strings.HasPrefix(action, "create-") && (key == "archived" || key == "archived_at" || key == "archived_by")) {
						continue
					}
					if !reflect.DeepEqual(value, after[key]) {
						t.Fatal("revert did not restore original snapshot field: " + key)
					}
				}
				for oldID, raw := range history {
					row, err := app.FindRecordById("game_catalog_changelog", oldID)
					if err != nil {
						t.Fatal(err)
					}
					encoded, err := json.Marshal(row)
					if err != nil || string(encoded) != raw {
						t.Fatal("revert rewrote immutable original audit", err)
					}
				}
				if !reflect.DeepEqual(beforeXP, nativeBulkState(t, app)) {
					t.Fatal("revert changed arcade XP/history")
				}
			})
		}
	}
}

func TestCanonicalNativeCatalogRevertCorruptSnapshotsFailClosedWithoutMutation(t *testing.T) {
	for _, damaged := range []struct{ name, raw string }{{"invalidJSON", "{"}, {"array", "[]"}, {"string", `"string"`}, {"emptyObject", "{}"}, {"missingUpdateBefore", "null"}, {"foreignIdentity", `{"id":"wrong","revision":1,"archived":false}`}, {"fractionalRevision", ""}, {"wrongArchiveType", ""}} {
		t.Run(damaged.name, func(t *testing.T) {
			bad := damaged.raw
			app := nativeNearbyApp(t, true)
			web, user := createAuthUserWithTags(t, app, []string{"moderator"})
			_, session := nativeNearbySession(t, app, user)
			create := map[string]any{"entity": "manufacturer", "operation_id": catalogOperation(), "reason": "Synthetic corrupt snapshot fixture", "values": map[string]any{"en": "Before", "kr": "이전", "jp": "以前"}}
			first := nativeBulkRequest(t, app, "POST", "/moderation/game/catalog", web, create, 200).(map[string]any)
			id := first["item"].(map[string]any)["id"].(string)
			updated := nativeBulkRequest(t, app, "PUT", "/moderation/game/catalog", web, map[string]any{"entity": "manufacturer", "id": id, "expected_revision": 1, "operation_id": catalogOperation(), "reason": "Synthetic corrupt snapshot fixture", "values": map[string]any{"en": "After", "kr": "이후", "jp": "以後"}}, 200).(map[string]any)
			target := updated["change"].(map[string]any)["id"].(string)
			if bad == "" {
				snapshot := updated["change"].(map[string]any)["before"].(map[string]any)
				if damaged.name == "fractionalRevision" {
					snapshot["revision"] = 1.5
				} else {
					snapshot["archived"] = "false"
				}
				encoded, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				bad = string(encoded)
			}
			if _, err := app.DB().NewQuery(`UPDATE game_catalog_changelog SET "before"={:bad} WHERE id={:id}`).Bind(dbx.Params{"bad": bad, "id": target}).Execute(); err != nil {
				t.Fatal(err)
			}
			beforeXP := nativeBulkState(t, app)
			record, err := app.FindRecordById("game_manufacturer", id)
			if err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			count, err := app.CountRecords("game_catalog_changelog")
			if err != nil {
				t.Fatal(err)
			}
			for _, token := range []string{web, session.AccessToken} {
				nativeBulkRequest(t, app, "POST", "/moderation/game/catalog/changes/revert", token, map[string]any{"entity": "manufacturer", "id": id, "expected_revision": 2, "change_id": target, "operation_id": catalogOperation(), "reason": "Reject corrupted stored snapshot"}, 502)
			}
			record, err = app.FindRecordById("game_manufacturer", id)
			if err != nil {
				t.Fatal(err)
			}
			after, err := json.Marshal(record)
			if err != nil || string(before) != string(after) {
				t.Fatal("corrupt snapshot mutated catalog", err)
			}
			afterCount, err := app.CountRecords("game_catalog_changelog")
			if err != nil || afterCount != count || !reflect.DeepEqual(beforeXP, nativeBulkState(t, app)) {
				t.Fatal("corrupt snapshot changed audit/XP", err)
			}
			stored := struct {
				Raw sql.NullString `db:"raw"`
			}{}
			if err := app.DB().NewQuery(`SELECT "before" AS raw FROM game_catalog_changelog WHERE id={:id}`).Bind(dbx.Params{"id": target}).One(&stored); err != nil || !stored.Raw.Valid || stored.Raw.String != bad {
				t.Fatal("corrupt snapshot was silently repaired", err)
			}
		})
	}
}
