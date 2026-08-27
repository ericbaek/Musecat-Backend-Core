package query

import (
	"time"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

func decorateNearbyCampaigns(app core.App, page []arcadeDistance, campaigns []arcadeinternal.NearbyCampaign) ([]map[string]any, error) {
	summaries := make([]map[string]any, 0, len(campaigns))
	for _, campaign := range campaigns {
		nearbyTargetCount := 0
		for _, result := range page {
			arcadeID, _ := result.payload["id"].(string)
			targetCount := campaign.ArcadeTargetCount[arcadeID]
			if targetCount == 0 {
				continue
			}
			nearbyTargetCount += targetCount
			campaignsForArcade, _ := result.payload["campaigns"].([]map[string]any)
			result.payload["campaigns"] = append(campaignsForArcade, map[string]any{
				"id":           campaign.ID,
				"target_count": targetCount,
			})
		}
		if nearbyTargetCount == 0 {
			continue
		}
		summary, err := buildNearbyCampaignSummary(app, campaign)
		if err != nil {
			return nil, err
		}
		summary["nearby_target_count"] = nearbyTargetCount
		summaries = append(summaries, summary)
	}
	return summaries, nil
}

func buildNearbyCampaignSummary(app core.App, campaign arcadeinternal.NearbyCampaign) (map[string]any, error) {
	from, err := arcadeinternal.BuildGameSeriesBundle(app, campaign.FromVersion)
	if err != nil {
		return nil, err
	}
	to, err := arcadeinternal.BuildGameSeriesBundle(app, campaign.ToVersion)
	if err != nil {
		return nil, err
	}
	cabinets := make([]map[string]any, 0, len(campaign.Cabinets))
	for _, cabinetID := range campaign.Cabinets {
		record, err := app.FindRecordById(arcadeinternal.CollectionGameCabinet, cabinetID)
		if err != nil {
			return nil, err
		}
		cabinets = append(cabinets, map[string]any{
			"id": record.Id,
			"en": record.GetString("en"),
			"kr": record.GetString("kr"),
			"jp": record.GetString("jp"),
		})
	}
	return map[string]any{
		"id":            campaign.ID,
		"from_version":  from,
		"to_version":    to,
		"cabinet_scope": campaign.CabinetScope,
		"cabinets":      cabinets,
		"country_scope": campaign.CountryScope,
		"countries":     campaign.Countries,
		"reward_exp":    campaign.RewardExp,
		"status":        campaign.Status,
		"start_at":      campaign.StartAt.Format(time.RFC3339),
		"end_at":        campaign.EndAt.Format(time.RFC3339),
		"target_count":  campaign.TargetCount,
	}, nil
}
