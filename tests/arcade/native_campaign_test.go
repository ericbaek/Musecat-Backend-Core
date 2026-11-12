package arcade_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/pocketbase/pocketbase/tools/types"
)

var nativeCampaignOps = []struct{ method, path string }{{"POST", "/campaign"}, {"PUT", "/campaign"}, {"POST", "/campaign/end"}, {"POST", "/campaign/check"}}

func exportNativeCampaign(t *testing.T, name string, value any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_CAMPAIGN_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "email", "access_token", "refresh_token"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+key+`"`) {
			t.Fatal("credential field in domain fixture")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func nativeCampaignState(t *testing.T, app core.App) map[string]string {
	t.Helper()
	state := nativeBulkState(t, app)
	for _, collection := range []string{"arcade_campaign", "arcade_campaign_check", "arcade_visit"} {
		rows, err := app.FindAllRecords(collection)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(rows)
		if err != nil {
			t.Fatal(err)
		}
		state[collection] = string(raw)
	}
	return state
}

func TestCanonicalNativeCampaignManagementSharesWebDTOAndStaffScope(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, creator := createAuthUserWithTags(t, app, []string{"developer"})
	_, session := nativeNearbySession(t, app, creator)
	web, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, otherSession := nativeNearbySession(t, app, staff)
	series := seedGameSeries(t, app, 1301, "Canonical campaign")
	from := seedGameSeriesVersionWithSeries(t, app, series, "2025-01-01", "Before")
	to := seedGameSeriesVersionWithSeries(t, app, series, "2026-01-01", "After")
	body := map[string]any{"from_version": from, "to_version": to, "cabinet_scope": "all", "cabinets": []string{}, "country_scope": "all", "countries": []string{}, "reward_exp": 10, "status": "active", "start_at": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339), "end_at": time.Now().UTC().Add(time.Hour).Format(time.RFC3339), "created_by": staff.Id, "future_field": true}
	created := nativeBulkRequest(t, app, "POST", "/campaign", session.AccessToken, body, 201).(map[string]any)
	exportNativeCampaign(t, "campaign-create", created)
	id := created["campaign"].(map[string]any)["id"].(string)
	stored, err := app.FindRecordById("arcade_campaign", id)
	if err != nil || stored.GetString("created_by") != creator.Id {
		t.Fatal("campaign creator was not server-derived", err)
	}
	body["id"] = id
	body["reward_exp"] = 12
	updated := nativeBulkRequest(t, app, "PUT", "/campaign", otherSession.AccessToken, body, 200)
	webUpdated := nativeBulkRequest(t, app, "PUT", "/campaign", web, body, 200)
	if !reflect.DeepEqual(updated, webUpdated) {
		t.Fatal("Native/Web campaign update DTO differs")
	}
	exportNativeCampaign(t, "campaign-update", updated)
	stored, err = app.FindRecordById("arcade_campaign", id)
	if err != nil || stored.GetString("created_by") != creator.Id {
		t.Fatal("another staff update replaced creator", err)
	}
	ended := nativeBulkRequest(t, app, "POST", "/campaign/end", otherSession.AccessToken, map[string]any{"id": id}, 200)
	webEnded := nativeBulkRequest(t, app, "POST", "/campaign/end", web, map[string]any{"id": id}, 200)
	if !reflect.DeepEqual(ended, webEnded) {
		t.Fatal("Native/Web campaign end DTO differs")
	}
	exportNativeCampaign(t, "campaign-end", ended)
}

func TestCanonicalNativeCampaignChecksKeepVisitXPAndRollbackPolicy(t *testing.T) {
	for _, verified := range []bool{false, true} {
		t.Run(map[bool]string{false: "bypass", true: "verified"}[verified], func(t *testing.T) {
			app := nativeNearbyApp(t, true)
			web, arcade, campaign, entry, initial := seedCampaignCheckFixture(t, app, nil)
			venue, err := app.FindRecordById("arcade", arcade)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := app.FindRecordById("user", venue.GetString("createdBy"))
			if err != nil {
				t.Fatal(err)
			}
			_, session := nativeNearbySession(t, app, owner)
			body := map[string]any{"campaign": campaign, "arcade": arcade, "game_id": entry, "result": "updated", "bypass_location": false, "user": "ignored-actor", "future_field": true}
			before := nativeCampaignState(t, app)
			nativeBulkRequest(t, app, "POST", "/campaign/check", session.AccessToken, body, 403)
			if !reflect.DeepEqual(before, nativeCampaignState(t, app)) {
				t.Fatal("missing visit changed campaign domain")
			}
			if verified {
				at := time.Now().UTC()
				seedArcadeVisit(t, app, owner.Id, arcade, at)
				zone, err := time.LoadLocation(venue.GetString("timezone"))
				if err != nil {
					t.Fatal(err)
				}
				visit, err := app.FindFirstRecordByFilter("arcade_visit", "user={:user} && arcade={:arcade}", dbx.Params{"user": owner.Id, "arcade": arcade})
				if err != nil {
					t.Fatal(err)
				}
				visit.Set("visit_day", at.In(zone).Format("2006-01-02"))
				if err := app.Save(visit); err != nil {
					t.Fatal(err)
				}
			} else {
				body["bypass_location"] = true
			}
			result := nativeBulkRequest(t, app, "POST", "/campaign/check", session.AccessToken, body, 200).(map[string]any)
			wantXP := float64(1)
			suffix := "bypass"
			if verified {
				wantXP = 10
				suffix = "verified"
			}
			if result["gained_exp"] != wantXP || result["updated"] != true || result["state_id"] == initial {
				t.Fatal("campaign check changed reward/state policy")
			}
			exportNativeCampaign(t, "campaign-check-"+suffix, result)
			checks, err := app.FindAllRecords("arcade_campaign_check")
			if err != nil || len(checks) != 1 || checks[0].GetString("user") != owner.Id || checks[0].GetBool("location_verified") != verified {
				t.Fatal("campaign report owner/verification changed", err)
			}
			before = nativeCampaignState(t, app)
			duplicateNative := nativeBulkRequest(t, app, "POST", "/campaign/check", session.AccessToken, body, 200).(map[string]any)
			duplicateWeb := nativeBulkRequest(t, app, "POST", "/campaign/check", web, body, 200)
			if duplicateNative["gained_exp"] != float64(0) || !reflect.DeepEqual(duplicateNative, duplicateWeb) || !reflect.DeepEqual(before, nativeCampaignState(t, app)) {
				t.Fatal("duplicate changed XP/reports or Web DTO")
			}
			if !verified {
				exportNativeCampaign(t, "campaign-check-duplicate", duplicateNative)
			}
			body["result"] = "still_old"
			rollback := nativeBulkRequest(t, app, "POST", "/campaign/check", session.AccessToken, body, 200).(map[string]any)
			if rollback["rolled_back"] != true || rollback["updated"] != false {
				t.Fatal("campaign rollback changed")
			}
			rollbackXP := float64(0)
			if verified {
				rollbackXP = 10
			}
			if rollback["gained_exp"] != rollbackXP {
				t.Fatal("rollback still-old XP changed")
			}
			if !verified {
				exportNativeCampaign(t, "campaign-check-rollback", rollback)
			}
		})
	}
}

func TestCanonicalNativeCampaignRetainsRolesAndRejectsInvalidInactiveDisabled(t *testing.T) {
	app := nativeNearbyApp(t, true)
	for _, role := range []string{"developer", "moderator", "supporter", "founding_supporter", "arcade_owner", ""} {
		web, user := createAuthUserWithTags(t, app, []string{role})
		setUserLevel(t, app, user.Id, 30)
		_, session := nativeNearbySession(t, app, user)
		for i, op := range nativeCampaignOps {
			status := 403
			if i == 3 || role == "developer" || role == "moderator" {
				status = 400
			}
			for _, token := range []string{web, session.AccessToken} {
				nativeBulkRequest(t, app, op.method, op.path, token, map[string]any{}, status)
			}
		}
	}
	web, user := createAuthUserWithTags(t, app, []string{"developer"})
	service, session := nativeNearbySession(t, app, user)
	before := nativeCampaignState(t, app)
	for _, op := range nativeCampaignOps {
		for _, token := range []string{"", strings.Repeat("z", 43)} {
			nativeBulkRequest(t, app, op.method, op.path, token, map[string]any{}, 401)
		}
		r := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal("invalid bearer fell back to cookie")
		}
	}
	for _, change := range []struct{ deny, restore string }{{"access_expires=0", "access_expires=unixepoch()+600"}, {"expires=0", "expires=unixepoch()+3600"}, {"client='removed-client'", "client='app.musecat.native.dev'"}} {
		for i, sql := range []string{change.deny, change.restore} {
			if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + sql + " WHERE user_id={:user}").Bind(dbx.Params{"user": user.Id}).Execute(); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				for _, op := range nativeCampaignOps {
					nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 401)
				}
			}
		}
	}
	user.Set("withdrawn", true)
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	for _, op := range nativeCampaignOps {
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 401)
	}
	user.Set("withdrawn", false)
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	seedUserBan(t, app, user.Id, "", "native_campaign_fixture", time.Now().Add(time.Hour))
	for _, op := range nativeCampaignOps {
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 403)
	}
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	for _, op := range nativeCampaignOps {
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 401)
	}
	if !reflect.DeepEqual(before, nativeCampaignState(t, app)) {
		t.Fatal("denied campaign request changed domain")
	}
	disabled := nativeNearbyApp(t, false)
	web, actor := createAuthUserWithTags(t, disabled, []string{"developer"})
	disabledService, issued := nativeNearbySession(t, disabled, actor)
	if got, err := disabledService.Authenticate(disabled, issued.AccessToken); err != nil || got.Id != actor.Id {
		t.Fatal("invalid disabled same-database fixture")
	}
	for _, op := range nativeCampaignOps {
		r := executeJSONRequest(t, disabled, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + issued.AccessToken})
		r.Body.Close()
		if r.StatusCode != 401 {
			t.Fatal("disabled adapter accepted Native credentials")
		}
		nativeBulkRequest(t, disabled, op.method, op.path, web, map[string]any{}, 400)
	}
}

func TestCanonicalNativeCampaignKeepsRateLimitsAndCredentialScope(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, actor := createAuthUserWithTags(t, app, []string{"developer"})
	_, session := nativeNearbySession(t, app, actor)
	for _, op := range nativeCampaignOps {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: op.method + " " + op.path, MaxRequests: 1, Duration: 60}}}
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 400)
		nativeBulkRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 429)
		r := executeJSONRequest(t, app, op.method, "/native"+op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		r.Body.Close()
		if r.StatusCode != 404 {
			t.Fatal("Native campaign alias added")
		}
	}
	for _, op := range []struct {
		method, path string
		status       int
	}{{"GET", "/arcade/campaigns", 401}, {"PUT", "/arcade/report-settings", 401}, {"GET", "/api/collections/arcade_campaign/records", 403}} {
		r := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + session.AccessToken})
		r.Body.Close()
		if r.StatusCode != op.status {
			t.Fatal("campaign adapter expanded unrelated credential scope")
		}
	}
}

func TestCanonicalNativeCampaignRejectsInvisibleInactiveAndExpiredRollbackAtomically(t *testing.T) {
	for _, scenario := range []string{"private", "closed", "inactive", "country", "rollback-expired"} {
		t.Run(scenario, func(t *testing.T) {
			app := nativeNearbyApp(t, true)
			web, arcade, campaign, entry, _ := seedCampaignCheckFixture(t, app, nil)
			venue, err := app.FindRecordById("arcade", arcade)
			if err != nil {
				t.Fatal(err)
			}
			owner, err := app.FindRecordById("user", venue.GetString("createdBy"))
			if err != nil {
				t.Fatal(err)
			}
			_, session := nativeNearbySession(t, app, owner)
			body := map[string]any{"campaign": campaign, "arcade": arcade, "game_id": entry, "result": "updated", "bypass_location": true}
			status := 409
			switch scenario {
			case "private":
				venue.Set("public", false)
				status = 404
				if err := app.Save(venue); err != nil {
					t.Fatal(err)
				}
			case "closed":
				venue.Set("closed", true)
				status = 404
				if err := app.Save(venue); err != nil {
					t.Fatal(err)
				}
			case "inactive", "country":
				record, err := app.FindRecordById("arcade_campaign", campaign)
				if err != nil {
					t.Fatal(err)
				}
				if scenario == "inactive" {
					record.Set("status", "ended")
				} else {
					record.Set("country_scope", "include")
					record.Set("countries", []string{"JP"})
				}
				if err := app.Save(record); err != nil {
					t.Fatal(err)
				}
			case "rollback-expired":
				nativeBulkRequest(t, app, "POST", "/campaign/check", session.AccessToken, body, 200)
				record, err := app.FindFirstRecordByFilter("arcade_campaign_check", "campaign={:campaign}", dbx.Params{"campaign": campaign})
				if err != nil {
					t.Fatal(err)
				}
				// Only this newly created test fixture is aged to test the existing seven-day boundary.
				created, _ := pbtypes.ParseDateTime(time.Now().UTC().Add(-8 * 24 * time.Hour))
				if _, err := app.NonconcurrentDB().NewQuery("UPDATE arcade_campaign_check SET created={:created} WHERE id={:id}").Bind(dbx.Params{"created": created, "id": record.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
				body["result"] = "still_old"
			}
			before := nativeCampaignState(t, app)
			native := nativeBulkRequest(t, app, "POST", "/campaign/check", session.AccessToken, body, status)
			webResult := nativeBulkRequest(t, app, "POST", "/campaign/check", web, body, status)
			if !reflect.DeepEqual(native, webResult) || !reflect.DeepEqual(before, nativeCampaignState(t, app)) {
				t.Fatal("campaign failure changed domain or Web error DTO")
			}
		})
	}
}
