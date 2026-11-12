package arcade_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

var nativeCatalogReadPaths = []string{"/moderation/game/catalog?locale=en-US", "/moderation/game/catalog/changes"}

func exportNativeCatalogRead(t *testing.T, name string, value any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_CATALOG_READ_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "email", "access_token", "refresh_token"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+key+`"`) {
			t.Fatal("credential in catalog fixture")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalNativeCatalogReadsShareActualWebSnapshotsAndLocale(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, user := createAuthUserWithTags(t, app, []string{"developer"})
	_, session := nativeNearbySession(t, app, user)
	create := func(entity string, values map[string]any) string {
		t.Helper()
		result := nativeBulkRequest(t, app, "POST", "/moderation/game/catalog", web, map[string]any{"entity": entity, "operation_id": uuid.NewString(), "reason": "Synthetic catalog read fixture", "values": values}, 200).(map[string]any)
		return result["item"].(map[string]any)["id"].(string)
	}
	maker := create("manufacturer", map[string]any{"en": "Maker English", "kr": "합성 제조사", "jp": "合成メーカー"})
	series := create("series", map[string]any{"series_number": 73001, "en": "Synthetic series", "kr": "합성 시리즈", "jp": "合成シリーズ", "manufacturer": maker})
	newer := create("version", catalogVersionValues(series, "Newer version", "2026-01-01"))
	older := create("version", catalogVersionValues(series, "Older version", "2025-01-01"))
	newCab := create("cabinet", map[string]any{"series": series, "en": "New cabinet", "kr": "새 캐비넷", "jp": "新筐体"})
	oldCab := create("cabinet", map[string]any{"series": series, "en": "Old cabinet", "kr": "이전 캐비넷", "jp": "旧筐体"})
	create("compatibility", map[string]any{"version": newer, "cabinet": newCab, "price_default": nil})
	create("compatibility", map[string]any{"version": older, "cabinet": oldCab, "price_default": nil})
	archived := create("manufacturer", map[string]any{"en": "Archived maker", "kr": "보관 제조사", "jp": "保管メーカー"})
	nativeBulkRequest(t, app, "DELETE", "/moderation/game/catalog", web, map[string]any{"entity": "manufacturer", "id": archived, "expected_revision": 1, "operation_id": uuid.NewString(), "reason": "Archive unused synthetic manufacturer"}, 200)
	alias := seedGameSeriesVersionWithSeries(t, app, series, "2024-01-01", "Historical alias")
	row, err := app.FindRecordById("game_series_version", alias)
	if err != nil {
		t.Fatal(err)
	}
	row.Set("alias_of", newer)
	if err := app.Save(row); err != nil {
		t.Fatal(err)
	}
	before := nativeBulkState(t, app)
	catalogBefore := map[string]string{}
	for _, name := range []string{"game_manufacturer", "game_series", "game_series_version", "game_cabinet", "game_series_version_cabinet", "game_catalog_changelog"} {
		rows, err := app.FindAllRecords(name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		catalogBefore[name] = string(raw)
	}
	for _, locale := range []struct{ code, name string }{{"en-US", "Maker English"}, {"ko-KR", "합성 제조사"}, {"ja-JP", "合成メーカー"}} {
		path := "/moderation/game/catalog?locale=" + locale.code
		value := nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 200).(map[string]any)
		if !reflect.DeepEqual(value, nativeBulkRequest(t, app, "GET", path, web, nil, 200)) {
			t.Fatal("Native/Web catalog snapshots differ")
		}
		foundMaker, foundArchived := false, false
		for _, raw := range value["manufacturers"].([]any) {
			item := raw.(map[string]any)
			if item["id"] == maker {
				foundMaker = item["name"] == locale.name
			}
			if item["id"] == archived {
				foundArchived = item["archived"] == true && item["revision"] == float64(2)
			}
		}
		if !foundMaker || !foundArchived {
			t.Fatal("localized or archived manufacturer lost")
		}
		for _, raw := range value["versions"].([]any) {
			if raw.(map[string]any)["id"] == alias {
				t.Fatal("historical version alias became selectable")
			}
		}
		cabinets := value["cabinets"].([]any)
		if len(cabinets) != 2 || cabinets[0].(map[string]any)["id"] != newCab || cabinets[1].(map[string]any)["id"] != oldCab {
			t.Fatal("canonical cabinet release ordering changed")
		}
		exportNativeCatalogRead(t, "catalog-management-"+locale.code, value)
	}
	for _, path := range []string{"/moderation/game/catalog/changes", "/moderation/game/catalog/changes?page=2", "/moderation/game/catalog/changes?entity=manufacturer&entity_id=" + maker + "&actor=" + user.Id + "&action=create"} {
		value := nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 200).(map[string]any)
		if !reflect.DeepEqual(value, nativeBulkRequest(t, app, "GET", path, web, nil, 200)) {
			t.Fatal("Native/Web catalog audit differs")
		}
		name := "catalog-changes-page1"
		if strings.Contains(path, "page=2") {
			name = "catalog-changes-page2"
			if len(value["items"].([]any)) != 0 {
				t.Fatal("unexpected extra audit page")
			}
		} else if strings.Contains(path, "entity=") {
			name = "catalog-changes-filter"
			if len(value["items"].([]any)) != 1 {
				t.Fatal("audit filter ignored")
			}
		}
		if value["per_page"] != float64(50) {
			t.Fatal("default audit page size changed")
		}
		exportNativeCatalogRead(t, name, value)
	}
	first := nativeBulkRequest(t, app, "GET", "/moderation/game/catalog/changes?per_page=1", session.AccessToken, nil, 200).(map[string]any)
	second := nativeBulkRequest(t, app, "GET", "/moderation/game/catalog/changes?per_page=1&page=2", session.AccessToken, nil, 200).(map[string]any)
	if first["items"].([]any)[0].(map[string]any)["id"] == second["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("catalog audit pagination repeated first row")
	}
	for _, path := range []string{"/moderation/game/catalog", "/moderation/game/catalog?locale=invalid", "/moderation/game/catalog/changes?per_page=101"} {
		nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 400)
	}
	if !reflect.DeepEqual(before, nativeBulkState(t, app)) {
		t.Fatal("catalog reads changed arcade or XP state")
	}
	for name, old := range catalogBefore {
		rows, err := app.FindAllRecords(name)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(rows)
		if err != nil || string(raw) != old {
			t.Fatal("catalog reads changed records or audit", err)
		}
	}
}

func TestCanonicalNativeCatalogReadsKeepAuthoritativeRoleAndXPGuard(t *testing.T) {
	for _, test := range []struct {
		name      string
		tags      []string
		exp, want int
	}{{"ordinary30", nil, 300, 403}, {"designer", []string{"designer"}, 300, 403}, {"supporter29", []string{"supporter"}, 299, 403}, {"supporter30", []string{"supporter"}, 300, 200}, {"founding30", []string{"founding_supporter"}, 300, 200}, {"developer", []string{"developer"}, 0, 200}, {"moderator", []string{"moderator"}, 0, 200}} {
		t.Run(test.name, func(t *testing.T) {
			app := nativeNearbyApp(t, true)
			web, user := createAuthUserWithTags(t, app, test.tags)
			seedUserLevelExp(t, app, user.Id, test.exp)
			_, session := nativeNearbySession(t, app, user)
			for _, path := range nativeCatalogReadPaths {
				for _, token := range []string{web, session.AccessToken} {
					nativeBulkRequest(t, app, "GET", path, token, nil, test.want)
				}
			}
		})
	}
}

func TestCanonicalNativeCatalogReadsRejectInvalidInactiveAndDisabledSessions(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, user := createAuthUserWithTags(t, app, []string{"developer"})
	service, session := nativeNearbySession(t, app, user)
	for _, path := range nativeCatalogReadPaths {
		for _, token := range []string{"", strings.Repeat("z", 43)} {
			nativeBulkRequest(t, app, "GET", path, token, nil, 401)
		}
		response := executeJSONRequest(t, app, "GET", path, "", map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("invalid Native bearer fell back to cookie")
		}
	}
	seedUserBan(t, app, user.Id, "", "catalog_read_canary", time.Now().Add(time.Hour))
	for _, path := range nativeCatalogReadPaths {
		nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 403)
	}
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	for _, path := range nativeCatalogReadPaths {
		nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 401)
	}
	disabled := nativeNearbyApp(t, false)
	disabledWeb, actor := createAuthUserWithTags(t, disabled, []string{"developer"})
	valid, issued := nativeNearbySession(t, disabled, actor)
	if u, err := valid.Authenticate(disabled, issued.AccessToken); err != nil || u.Id != actor.Id {
		t.Fatal("disabled fixture is not valid sameDB session")
	}
	for _, path := range nativeCatalogReadPaths {
		res := executeJSONRequest(t, disabled, "GET", path, "", map[string]string{"Authorization": "Bearer " + issued.AccessToken})
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatal("disabled runtime accepted Native catalog session")
		}
		nativeBulkRequest(t, disabled, "GET", path, disabledWeb, nil, 200)
	}
	for _, mode := range []string{"expired", "withdrawn"} {
		t.Run(mode, func(t *testing.T) {
			a := nativeNearbyApp(t, true)
			_, u := createAuthUserWithTags(t, a, []string{"developer"})
			_, s := nativeNearbySession(t, a, u)
			if mode == "expired" {
				if _, err := a.DB().NewQuery("UPDATE native_auth_session SET access_expires=0 WHERE user_id={:user}").Bind(dbx.Params{"user": u.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
			} else {
				u.Set("withdrawn", true)
				if err := a.Save(u); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range nativeCatalogReadPaths {
				nativeBulkRequest(t, a, "GET", path, s.AccessToken, nil, 401)
			}
		})
	}
}

func TestCanonicalNativeCatalogReadsKeepOtherModerationUnselectedAndRateLimits(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, user := createAuthUserWithTags(t, app, []string{"developer"})
	_, session := nativeNearbySession(t, app, user)
	for _, op := range []struct {
		method, path string
		status       int
	}{{"POST", "/moderation/user/ban", 401}, {"PUT", "/moderation/arcade/status", 401}, {"PUT", "/moderation/supporter/request", 401}, {"GET", "/native/moderation/game/catalog", 404}, {"GET", "/native/moderation/game/catalog/changes", 404}} {
		res := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		res.Body.Close()
		if res.StatusCode != op.status {
			t.Fatal("read2 binding widened unrelated capability")
		}
	}
	for _, path := range nativeCatalogReadPaths {
		label := strings.Split(path, "?")[0]
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "GET " + label, MaxRequests: 1, Duration: 60}}}
		nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 200)
		nativeBulkRequest(t, app, "GET", path, session.AccessToken, nil, 429)
	}
}
