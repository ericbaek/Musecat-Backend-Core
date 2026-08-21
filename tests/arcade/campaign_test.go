package arcade_test

import (
	"encoding/json"
	"fmt"
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
		)
		if err := app.Save(checks); err != nil {
			tb.Fatalf("failed to create campaign check collection: %v", err)
		}
	}
}
