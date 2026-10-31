package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(applyArcadeCampaignSchema, func(core.App) error { return nil })
}

// Core bootstraps the current empty schema; Full owns existing-data migrations.
func applyArcadeCampaignSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		collections := make(map[string]*core.Collection)
		for _, name := range []string{"user", "arcade", "game_series_version", "game_cabinet", "arcade_game_id"} {
			collection, err := tx.FindCollectionByNameOrId(name)
			if err != nil {
				return fmt.Errorf("find campaign dependency %s: %w", name, err)
			}
			collections[name] = collection
		}

		minReward := float64(1)
		campaign := core.NewBaseCollection("arcade_campaign")
		campaign.Fields.Add(
			&core.RelationField{Name: "from_version", CollectionId: collections["game_series_version"].Id, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "to_version", CollectionId: collections["game_series_version"].Id, Required: true, MaxSelect: 1},
			&core.SelectField{Name: "cabinet_scope", Values: []string{"all", "include"}, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "cabinets", CollectionId: collections["game_cabinet"].Id, MaxSelect: 50},
			&core.SelectField{Name: "country_scope", Values: []string{"all", "include", "exclude"}, Required: true, MaxSelect: 1},
			&core.JSONField{Name: "countries"},
			&core.NumberField{Name: "reward_exp", Required: true, OnlyInt: true, Min: &minReward},
			&core.SelectField{Name: "status", Values: []string{"draft", "active", "ended"}, Required: true, MaxSelect: 1},
			&core.DateField{Name: "start_at", Required: true},
			&core.DateField{Name: "end_at", Required: true},
			&core.RelationField{Name: "created_by", CollectionId: collections["user"].Id, Required: true, MaxSelect: 1},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		campaign.AddIndex("idx_arcade_campaign_status_dates", false, "status, start_at, end_at", "")
		campaign.AddIndex("idx_arcade_campaign_from_to", false, "from_version, to_version", "")
		if err := tx.Save(campaign); err != nil {
			return fmt.Errorf("create arcade_campaign: %w", err)
		}

		check := core.NewBaseCollection("arcade_campaign_check")
		check.Fields.Add(
			&core.RelationField{Name: "campaign", CollectionId: campaign.Id, CascadeDelete: true, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "arcade", CollectionId: collections["arcade"].Id, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "game_id", CollectionId: collections["arcade_game_id"].Id, Required: true, MaxSelect: 1},
			&core.RelationField{Name: "user", CollectionId: collections["user"].Id, Required: true, MaxSelect: 1},
			&core.SelectField{Name: "result", Values: []string{"still_old", "updated"}, Required: true, MaxSelect: 1},
			&core.TextField{Name: "state_id", Max: 15},
			&core.BoolField{Name: "location_verified"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		check.AddIndex("idx_arcade_campaign_check_unique", false, "campaign, user, game_id", "")
		check.AddIndex("idx_arcade_campaign_check_campaign", false, "campaign, result", "")
		check.AddIndex("idx_arcade_campaign_check_target_created", false, "campaign, arcade, game_id, created", "")
		if err := tx.Save(check); err != nil {
			return fmt.Errorf("create arcade_campaign_check: %w", err)
		}
		return nil
	})
}
