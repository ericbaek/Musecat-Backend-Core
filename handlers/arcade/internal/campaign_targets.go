package arcadeinternal

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type NearbyCampaignFilter struct {
	SeriesID  string
	CabinetID string
}

type NearbyCampaign struct {
	ID                string
	FromVersion       string
	ToVersion         string
	CabinetScope      string
	Cabinets          []string
	CountryScope      string
	Countries         []string
	RewardExp         int
	Status            string
	StartAt           time.Time
	EndAt             time.Time
	TargetCount       int
	ArcadeTargetCount map[string]int
}

// BuildNearbyCampaigns returns active campaigns and their pending target
// counts by arcade. The caller decides which already-filtered nearby page is
// visible; this function deliberately does not apply a distance policy.
func BuildNearbyCampaigns(app core.App, filters []NearbyCampaignFilter) ([]NearbyCampaign, error) {
	if _, err := app.FindCollectionByNameOrId(CollectionArcadeCampaign); err != nil {
		return []NearbyCampaign{}, nil
	}

	records, err := app.FindRecordsByFilter(
		CollectionArcadeCampaign,
		"status={:status} && start_at <= {:now} && end_at >= {:now}",
		"-start_at",
		20,
		0,
		dbx.Params{"status": "active", "now": time.Now().UTC()},
	)
	if err != nil {
		return nil, err
	}

	campaigns := make([]NearbyCampaign, 0, len(records))
	for _, record := range records {
		fromVersionID := strings.TrimSpace(record.GetString("from_version"))
		toVersionID := strings.TrimSpace(record.GetString("to_version"))
		fromVersion, err := app.FindRecordById(CollectionGameSeriesVersion, fromVersionID)
		if err != nil {
			return nil, err
		}
		seriesID, _ := AsString(fromVersion.Get("series"))
		seriesID = strings.TrimSpace(seriesID)

		selectedCabinet, matchesFilter := nearbyCampaignCabinetFilter(filters, seriesID)
		if len(filters) > 0 && !matchesFilter {
			continue
		}

		campaign := NearbyCampaign{
			ID:           record.Id,
			FromVersion:  fromVersionID,
			ToVersion:    toVersionID,
			CabinetScope: strings.TrimSpace(record.GetString("cabinet_scope")),
			Cabinets:     normalizeNearbyCampaignIDs(readNearbyCampaignStringArray(record.Get("cabinets"))),
			CountryScope: strings.TrimSpace(record.GetString("country_scope")),
			Countries:    normalizeNearbyCampaignCountries(readNearbyCampaignStringArray(record.Get("countries"))),
			RewardExp:    record.GetInt("reward_exp"),
			Status:       strings.TrimSpace(record.GetString("status")),
			StartAt:      record.GetDateTime("start_at").Time().UTC(),
			EndAt:        record.GetDateTime("end_at").Time().UTC(),
		}

		pendingCounts, err := loadNearbyCampaignArcadeCounts(app, campaign, campaign.FromVersion, true, selectedCabinet)
		if err != nil {
			return nil, err
		}
		if len(pendingCounts) == 0 {
			continue
		}
		updatedCounts, err := loadNearbyCampaignArcadeCounts(app, campaign, campaign.ToVersion, false, "")
		if err != nil {
			return nil, err
		}
		campaign.ArcadeTargetCount = pendingCounts
		for _, count := range pendingCounts {
			campaign.TargetCount += count
		}
		for _, count := range updatedCounts {
			campaign.TargetCount += count
		}
		campaigns = append(campaigns, campaign)
	}

	return campaigns, nil
}

func nearbyCampaignCabinetFilter(filters []NearbyCampaignFilter, seriesID string) (string, bool) {
	for _, filter := range filters {
		if strings.TrimSpace(filter.SeriesID) == seriesID {
			return strings.TrimSpace(filter.CabinetID), true
		}
	}
	return "", false
}

func loadNearbyCampaignArcadeCounts(app core.App, campaign NearbyCampaign, versionID string, requireTargetCompatibility bool, selectedCabinet string) (map[string]int, error) {
	clauses := []string{
		"a.public = 1",
		"a.closed = 0",
		"a.game_v2 <> ''",
		"r.batch = a.game_v2",
		"r.version = {:version}",
	}
	params := dbx.Params{"version": versionID}
	if requireTargetCompatibility {
		clauses = append(clauses, "(TRIM(COALESCE(r.cabinet, '')) = '' OR EXISTS (SELECT 1 FROM game_series_version_cabinet target_support WHERE target_support.version = {:to_version} AND target_support.cabinet = r.cabinet))")
		params["to_version"] = campaign.ToVersion
	}
	if selectedCabinet = strings.TrimSpace(selectedCabinet); selectedCabinet != "" {
		clauses = append(clauses, "TRIM(COALESCE(r.cabinet, '')) = {:selected_cabinet}")
		params["selected_cabinet"] = selectedCabinet
	}
	if campaign.CabinetScope == "include" {
		placeholders := make([]string, 0, len(campaign.Cabinets))
		for index, cabinetID := range campaign.Cabinets {
			key := "campaign_cabinet_" + strconv.Itoa(index)
			placeholders = append(placeholders, "{:"+key+"}")
			params[key] = cabinetID
		}
		if len(placeholders) == 0 {
			clauses = append(clauses, "1 = 0")
		} else {
			clauses = append(clauses, "r.cabinet IN ("+strings.Join(placeholders, ",")+")")
		}
	}
	if campaign.CountryScope != "all" {
		placeholders := make([]string, 0, len(campaign.Countries))
		for index, country := range campaign.Countries {
			key := "campaign_country_" + strconv.Itoa(index)
			placeholders = append(placeholders, "{:"+key+"}")
			params[key] = country
		}
		if len(placeholders) == 0 {
			clauses = append(clauses, "1 = 0")
		} else {
			operator := "IN"
			if campaign.CountryScope == "exclude" {
				operator = "NOT IN"
			}
			clauses = append(clauses, "UPPER(TRIM(a.country)) "+operator+" ("+strings.Join(placeholders, ",")+")")
		}
	}

	rows, err := app.DB().NewQuery(`
SELECT a.id AS arcade_id, COUNT(*) AS target_count
FROM arcade a
INNER JOIN arcade_game_history r ON r.batch = a.game_v2
WHERE ` + strings.Join(clauses, " AND ") + `
GROUP BY a.id`).Bind(params).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		raw := dbx.NullStringMap{}
		if err := rows.ScanMap(raw); err != nil {
			return nil, err
		}
		arcadeID := nearbyCampaignNullString(raw, "arcade_id")
		if arcadeID == "" {
			continue
		}
		if count := nearbyCampaignNullInt(raw, "target_count"); count > 0 {
			counts[arcadeID] = count
		}
	}
	return counts, rows.Err()
}

func readNearbyCampaignStringArray(raw any) []string {
	if raw == nil {
		return []string{}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return []string{}
	}
	var result []string
	if json.Unmarshal(encoded, &result) == nil {
		return result
	}
	if text, ok := raw.(string); ok {
		_ = json.Unmarshal([]byte(text), &result)
	}
	return result
}

func normalizeNearbyCampaignIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func normalizeNearbyCampaignCountries(values []string) []string {
	result := normalizeNearbyCampaignIDs(values)
	for index := range result {
		result[index] = strings.ToUpper(result[index])
	}
	return result
}

func nearbyCampaignNullString(raw dbx.NullStringMap, key string) string {
	value := raw[key]
	if !value.Valid {
		return ""
	}
	return strings.TrimSpace(value.String)
}

func nearbyCampaignNullInt(raw dbx.NullStringMap, key string) int {
	value := nearbyCampaignNullString(raw, key)
	parsed, _ := strconv.Atoi(value)
	return parsed
}
