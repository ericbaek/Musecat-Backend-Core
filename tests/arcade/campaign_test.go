package arcade_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestCampaignLocationBypass(t *testing.T) {
	for _, tag := range []string{"supporter", "founding_supporter", "developer", "moderator"} {
		t.Run(tag+" can bypass location verification", func(t *testing.T) {
			app := newArcadeTestApp(t)
			token, arcadeID, campaignID, gameID, initialStateID := seedCampaignCheckFixture(t, app, []string{tag})

			response := executeJSONRequest(t, app, http.MethodPost, "/campaign/check", fmt.Sprintf(`{"campaign":%q,"arcade":%q,"game_id":%q,"result":"updated","bypass_location":true}`, campaignID, arcadeID, gameID), map[string]string{
				"Authorization": "Bearer " + token,
			})
			defer response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("expected bypass to succeed, got %d", response.StatusCode)
			}
			var responseBody struct {
				GainedExp int `json:"gained_exp"`
			}
			if err := json.NewDecoder(response.Body).Decode(&responseBody); err != nil {
				t.Fatalf("failed to decode campaign check response: %v", err)
			}
			if responseBody.GainedExp != 1 {
				t.Fatalf("expected bypass to award 1 XP, got %d", responseBody.GainedExp)
			}

			arcade, err := app.FindRecordById("arcade", arcadeID)
			if err != nil {
				t.Fatal(err)
			}
			if arcade.GetString("game_v2") == initialStateID {
				t.Fatal("expected campaign update to move the game state")
			}
			checks, err := app.FindRecordsByFilter("arcade_campaign_check", "campaign={:campaign} && game_id={:game_id}", "", 0, 0, map[string]any{
				"campaign": campaignID,
				"game_id":  gameID,
			})
			if err != nil || len(checks) != 1 || checks[0].GetString("result") != "updated" {
				t.Fatalf("expected updated campaign check, err=%v checks=%d", err, len(checks))
			}
		})
	}
}

func TestCampaignLocationBypassRequiresSupporterAccess(t *testing.T) {
	app := newArcadeTestApp(t)
	token, arcadeID, campaignID, gameID, initialStateID := seedCampaignCheckFixture(t, app, nil)

	response := executeJSONRequest(t, app, http.MethodPost, "/campaign/check", fmt.Sprintf(`{"campaign":%q,"arcade":%q,"game_id":%q,"result":"updated","bypass_location":true}`, campaignID, arcadeID, gameID), map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected non-supporter bypass to be rejected, got %d", response.StatusCode)
	}

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatal(err)
	}
	if arcade.GetString("game_v2") != initialStateID {
		t.Fatal("rejected bypass moved the game state")
	}
}

func TestCampaignStillOldAwardsOneXPOnlyOnce(t *testing.T) {
	app := newArcadeTestApp(t)
	token, arcadeID, campaignID, gameID, _ := seedCampaignCheckFixture(t, app, []string{"supporter"})
	body := fmt.Sprintf(`{"campaign":%q,"arcade":%q,"game_id":%q,"result":"still_old","bypass_location":true}`, campaignID, arcadeID, gameID)

	for index, wantExp := range []int{1, 0} {
		response := executeJSONRequest(t, app, http.MethodPost, "/campaign/check", body, map[string]string{
			"Authorization": "Bearer " + token,
		})
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200, got %d", index+1, response.StatusCode)
		}
		var responseBody struct {
			GainedExp int `json:"gained_exp"`
		}
		if err := json.NewDecoder(response.Body).Decode(&responseBody); err != nil {
			t.Fatalf("request %d: failed to decode response: %v", index+1, err)
		}
		if responseBody.GainedExp != wantExp {
			t.Fatalf("request %d: expected %d XP, got %d", index+1, wantExp, responseBody.GainedExp)
		}
	}
}

func TestGetCampaignIncludesUpdatedItemsAndReportLog(t *testing.T) {
	app := newArcadeTestApp(t)
	firstToken, arcadeID, campaignID, gameID, _ := seedCampaignCheckFixture(t, app, []string{"supporter"})
	secondToken, _ := createAuthUserWithTags(t, app, []string{"supporter"})
	campaign, err := app.FindRecordById("arcade_campaign", campaignID)
	if err != nil {
		t.Fatalf("failed to load campaign: %v", err)
	}
	_, alreadyUpdatedOwner := createAuthUserWithTags(t, app, nil)
	alreadyUpdatedArcadeID, _ := seedPublicArcade(t, app, alreadyUpdatedOwner.Id, arcadeSeed{
		Name:     "Already Updated Arcade",
		Address:  "Updated Street",
		Country:  "KR",
		Timezone: "Asia/Seoul",
		Location: location{Lat: 37.51, Lon: 127.01},
	})
	seedBulkHistoryState(t, app, alreadyUpdatedArcadeID, alreadyUpdatedOwner.Id, campaign.GetString("to_version"))

	stillOld := fmt.Sprintf(`{"campaign":%q,"arcade":%q,"game_id":%q,"result":"still_old","bypass_location":true}`, campaignID, arcadeID, gameID)
	response := executeJSONRequest(t, app, http.MethodPost, "/campaign/check", stillOld, map[string]string{
		"Authorization": "Bearer " + firstToken,
	})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected still_old report to succeed, got %d", response.StatusCode)
	}

	updated := fmt.Sprintf(`{"campaign":%q,"arcade":%q,"game_id":%q,"result":"updated","bypass_location":true}`, campaignID, arcadeID, gameID)
	response = executeJSONRequest(t, app, http.MethodPost, "/campaign/check", updated, map[string]string{
		"Authorization": "Bearer " + secondToken,
	})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected updated report to succeed, got %d", response.StatusCode)
	}

	response = executeJSONRequest(t, app, http.MethodGet, "/campaign?id="+campaignID, "", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("expected campaign detail to succeed, got %d: %s", response.StatusCode, body)
	}
	var payload struct {
		Campaign map[string]any   `json:"campaign"`
		Items    []map[string]any `json:"items"`
		Updated  []map[string]any `json:"updated_items"`
		Logs     []map[string]any `json:"logs"`
		Latest   map[string]any   `json:"latest_still_old_report"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode campaign detail: %v", err)
	}
	if len(payload.Items) != 0 {
		t.Fatalf("expected old target to leave the pending list after update, got %d", len(payload.Items))
	}
	if len(payload.Updated) != 2 {
		t.Fatalf("expected both reported and already-updated targets, got %d", len(payload.Updated))
	}
	foundAlreadyUpdated := false
	for _, item := range payload.Updated {
		if item["status"] != "updated" {
			t.Fatalf("expected updated target status, got %#v", item["status"])
		}
		arcade, ok := item["arcade"].(map[string]any)
		if ok && arcade["id"] == alreadyUpdatedArcadeID {
			foundAlreadyUpdated = true
		}
	}
	if !foundAlreadyUpdated {
		t.Fatalf("expected already-updated arcade %q in updated targets", alreadyUpdatedArcadeID)
	}
	var reportedTarget map[string]any
	for _, item := range payload.Updated {
		arcade, _ := item["arcade"].(map[string]any)
		if arcade["id"] == arcadeID {
			reportedTarget = item
			break
		}
	}
	if reportedTarget["last_still_old_report"] == nil {
		t.Fatal("expected updated target to retain the latest still_old reporter")
	}
	if len(payload.Logs) != 2 {
		t.Fatalf("expected two campaign reports, got %d", len(payload.Logs))
	}
	if payload.Logs[0]["reaction"] != "updated" || payload.Logs[1]["reaction"] != "still_old" {
		t.Fatalf("expected newest-first report reactions, got %#v and %#v", payload.Logs[0]["reaction"], payload.Logs[1]["reaction"])
	}
	if payload.Latest == nil || payload.Latest["reaction"] != "still_old" {
		t.Fatalf("expected latest still_old report, got %#v", payload.Latest)
	}
	if got, ok := payload.Campaign["updated_count"].(float64); !ok || got != 2 {
		t.Fatalf("expected campaign updated_count=2, got %#v", payload.Campaign["updated_count"])
	}
}

func TestNearbyIncludesCampaignsForVisibleArcades(t *testing.T) {
	app := newArcadeTestApp(t)
	token, arcadeID, campaignID, gameID, _ := seedCampaignCheckFixture(t, app, []string{"supporter"})
	campaign, err := app.FindRecordById("arcade_campaign", campaignID)
	if err != nil {
		t.Fatalf("failed to load campaign: %v", err)
	}
	fromVersion, err := app.FindRecordById("game_series_version", campaign.GetString("from_version"))
	if err != nil {
		t.Fatalf("failed to load campaign version: %v", err)
	}
	seriesID := fromVersion.GetString("series")

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatalf("failed to load arcade: %v", err)
	}
	basic, err := app.FindRecordById("arcade_basic", arcade.GetString("basic"))
	if err != nil {
		t.Fatalf("failed to load arcade basic: %v", err)
	}
	basic.Set("location", map[string]any{"lat": 38.0, "lon": 127.0})
	if err := app.Save(basic); err != nil {
		t.Fatalf("failed to move arcade: %v", err)
	}

	response := executeJSONRequest(t, app, http.MethodGet, "/arcades/nearby?lat=37.5&lon=127.0&game_series="+seriesID, "", nil)
	var payload struct {
		Items     []map[string]any `json:"items"`
		Campaigns []map[string]any `json:"campaigns"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		response.Body.Close()
		t.Fatalf("failed to decode nearby response: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected nearby request to succeed, got %d", response.StatusCode)
	}
	var visibleItem map[string]any
	for _, item := range payload.Items {
		if item["id"] == arcadeID {
			visibleItem = item
			break
		}
	}
	if visibleItem == nil {
		t.Fatalf("expected moved arcade %q in nearby results", arcadeID)
	}
	itemCampaigns, ok := visibleItem["campaigns"].([]any)
	if !ok || len(itemCampaigns) != 1 || itemCampaigns[0].(map[string]any)["id"] != campaignID {
		t.Fatalf("expected campaign marker on nearby arcade, got %#v", visibleItem["campaigns"])
	}
	if len(payload.Campaigns) != 1 || payload.Campaigns[0]["id"] != campaignID || payload.Campaigns[0]["nearby_target_count"] != float64(1) {
		t.Fatalf("expected current-page campaign summary, got %#v", payload.Campaigns)
	}

	check := fmt.Sprintf(`{"campaign":%q,"arcade":%q,"game_id":%q,"result":"updated","bypass_location":true}`, campaignID, arcadeID, gameID)
	response = executeJSONRequest(t, app, http.MethodPost, "/campaign/check", check, map[string]string{
		"Authorization": "Bearer " + token,
	})
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected campaign update to succeed, got %d", response.StatusCode)
	}

	response = executeJSONRequest(t, app, http.MethodGet, "/arcades/nearby?lat=37.5&lon=127.0&game_series="+seriesID, "", nil)
	defer response.Body.Close()
	payload = struct {
		Items     []map[string]any `json:"items"`
		Campaigns []map[string]any `json:"campaigns"`
	}{}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode updated nearby response: %v", err)
	}
	if len(payload.Campaigns) != 0 {
		t.Fatalf("expected no campaign after the visible target was updated, got %#v", payload.Campaigns)
	}
}

func seedCampaignCheckFixture(tb testing.TB, app *tests.TestApp, tags []string) (token, arcadeID, campaignID, gameID, stateID string) {
	tb.Helper()
	ensureCampaignCollectionsForTest(tb, app)

	token, user := createAuthUserWithTags(tb, app, tags)
	arcadeID, _ = seedPublicArcade(tb, app, user.Id, arcadeSeed{
		Name:     "Campaign Arcade",
		Address:  "Campaign Street",
		Country:  "KR",
		Timezone: "Asia/Seoul",
		Location: location{Lat: 37.5, Lon: 127.0},
	})
	seriesID := seedGameSeries(tb, app, 901, "Campaign Series")
	fromVersion := seedGameSeriesVersionWithSeries(tb, app, seriesID, "2025-01-01", "Old Version")
	toVersion := seedGameSeriesVersionWithSeries(tb, app, seriesID, "2026-01-01", "New Version")
	entries, stateID := seedBulkHistoryState(tb, app, arcadeID, user.Id, fromVersion)
	gameID = entries[0]

	collection, err := app.FindCollectionByNameOrId("arcade_campaign")
	if err != nil {
		tb.Fatalf("failed to load campaign collection: %v", err)
	}
	campaign := core.NewRecord(collection)
	campaign.Set("from_version", fromVersion)
	campaign.Set("to_version", toVersion)
	campaign.Set("cabinet_scope", "all")
	campaign.Set("cabinets", []string{})
	campaign.Set("country_scope", "all")
	campaign.Set("countries", []string{})
	campaign.Set("reward_exp", 10)
	campaign.Set("status", "active")
	campaign.Set("start_at", time.Now().UTC().Add(-time.Hour))
	campaign.Set("end_at", time.Now().UTC().Add(time.Hour))
	campaign.Set("created_by", user.Id)
	if err := app.Save(campaign); err != nil {
		tb.Fatalf("failed to seed campaign: %v", err)
	}
	campaignID = campaign.Id

	return token, arcadeID, campaignID, gameID, stateID
}

func ensureCampaignCollectionsForTest(tb testing.TB, app core.App) {
	tb.Helper()
	// Campaign collections are deployed by Backend Full, so Core tests create
	// only the reusable API contract needed for this handler coverage.
	if _, err := app.FindCollectionByNameOrId("arcade_campaign"); err != nil {
		users, err := app.FindCollectionByNameOrId("user")
		if err != nil {
			tb.Fatalf("failed to load user collection: %v", err)
		}
		versions, err := app.FindCollectionByNameOrId("game_series_version")
		if err != nil {
			tb.Fatalf("failed to load game version collection: %v", err)
		}
		cabinets, err := app.FindCollectionByNameOrId("game_cabinet")
		if err != nil {
			tb.Fatalf("failed to load cabinet collection: %v", err)
		}
		campaign := core.NewBaseCollection("arcade_campaign")
		campaign.Fields.Add(
			&core.RelationField{Name: "from_version", CollectionId: versions.Id, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "to_version", CollectionId: versions.Id, Required: true, MaxSelect: 1},
			&core.SelectField{Name: "cabinet_scope", Values: []string{"all", "include"}, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "cabinets", CollectionId: cabinets.Id, MaxSelect: 50},
			&core.SelectField{Name: "country_scope", Values: []string{"all", "include", "exclude"}, Required: true, MaxSelect: 1},
			&core.JSONField{Name: "countries"},
			&core.NumberField{Name: "reward_exp", Required: true, OnlyInt: true, Min: func() *float64 { value := float64(1); return &value }()},
			&core.SelectField{Name: "status", Values: []string{"draft", "active", "ended"}, Required: true, MaxSelect: 1},
			&core.DateField{Name: "start_at", Required: true},
			&core.DateField{Name: "end_at", Required: true},
			&core.RelationField{Name: "created_by", CollectionId: users.Id, Required: true, MaxSelect: 1},
		)
		if err := app.Save(campaign); err != nil {
			tb.Fatalf("failed to create campaign collection: %v", err)
		}
	}

	if _, err := app.FindCollectionByNameOrId("arcade_campaign_check"); err != nil {
		campaign, err := app.FindCollectionByNameOrId("arcade_campaign")
		if err != nil {
			tb.Fatalf("failed to load campaign collection: %v", err)
		}
		arcades, err := app.FindCollectionByNameOrId("arcade")
		if err != nil {
			tb.Fatalf("failed to load arcade collection: %v", err)
		}
		users, err := app.FindCollectionByNameOrId("user")
		if err != nil {
			tb.Fatalf("failed to load user collection: %v", err)
		}
		entries, err := app.FindCollectionByNameOrId("arcade_game_id")
		if err != nil {
			tb.Fatalf("failed to load game entry collection: %v", err)
		}
		checks := core.NewBaseCollection("arcade_campaign_check")
		checks.Fields.Add(
			&core.RelationField{Name: "campaign", CollectionId: campaign.Id, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "arcade", CollectionId: arcades.Id, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "game_id", CollectionId: entries.Id, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "user", CollectionId: users.Id, Required: true, MaxSelect: 1},
			&core.SelectField{Name: "result", Values: []string{"still_old", "updated"}, Required: true, MaxSelect: 1},
			&core.TextField{Name: "state_id", Max: 15},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		if err := app.Save(checks); err != nil {
			tb.Fatalf("failed to create campaign check collection: %v", err)
		}
	}
}
