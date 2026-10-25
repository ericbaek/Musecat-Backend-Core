package arcade_test

import (
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestCampaignsFreshBootstrap(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:            "fresh Core bootstrap has no campaigns",
		Method:          http.MethodGet,
		URL:             "/campaigns",
		ExpectedStatus:  http.StatusOK,
		ExpectedContent: []string{`{"items":[]}`},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}
	scenario.Test(t)
}

func TestCampaignCheckFreshBootstrapSchema(t *testing.T) {
	app := newArcadeTestApp(t)
	campaigns := requireSchemaCollection(t, app, "arcade_campaign")
	checks := requireSchemaCollection(t, app, "arcade_campaign_check")
	assertLockedRules(t, campaigns)
	assertLockedRules(t, checks)
	assertSchemaRelation(t, checks, "campaign", campaigns.Id, true, true)
	for _, relation := range []struct {
		field      string
		collection string
	}{
		{"arcade", "arcade"},
		{"game_id", "arcade_game_id"},
		{"user", "user"},
	} {
		target := requireSchemaCollection(t, app, relation.collection)
		assertSchemaRelation(t, checks, relation.field, target.Id, true, false)
	}
	if _, ok := checks.Fields.GetByName("location_verified").(*core.BoolField); !ok {
		t.Fatal("campaign checks must retain a location_verified bool for each report")
	}
	assertAutodate(t, checks, "created", true, false)
	assertAutodate(t, checks, "updated", true, true)
	assertSchemaIndex(t, checks, "idx_arcade_campaign_check_unique", false, []string{"campaign", "user", "game_id"}, "")
	assertSchemaIndex(t, checks, "idx_arcade_campaign_check_target_created", false, []string{"campaign", "arcade", "game_id", "created"}, "")
}
