package basic

import (
	"strings"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

type basicDiffLogItem struct {
	ChangeType string                      `json:"change_type"`
	Bullets    []arcadeinternal.I18nBullet `json:"bullets"`
	Diff       []map[string]any            `json:"diff,omitempty"`
}

// buildBasicSnapshot keeps the diff input stable even when optional arrays are
// nil in PocketBase records.
func buildBasicLocationValue(fields BasicFields) any {
	return map[string]any{
		"lat": fields.Lat,
		"lon": fields.Lon,
	}
}

func buildBasicSnapshot(fields BasicFields) map[string]any {
	return map[string]any{
		"name":        fields.Name,
		"address":     fields.Address,
		"direction":   fields.Direction,
		"nickname":    arcadeinternal.TrimmedStringSlice(fields.Nickname),
		"location":    buildBasicLocationValue(fields),
		"subway_line": arcadeinternal.TrimmedStringSlice(fields.SubwayLine),
	}
}

// buildBasicDiffLogItem powers both initial basic linking and basic updates.
func buildBasicDiffLogItem(prev *BasicFields, next BasicFields) basicDiffLogItem {
	item := basicDiffLogItem{
		Bullets: []arcadeinternal.I18nBullet{},
		Diff:    []map[string]any{},
	}

	if prev == nil {
		item.ChangeType = "added"
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.basic.added", map[string]any{
			"name": next.Name,
		}))
		nextSnapshot := buildBasicSnapshot(next)
		for _, field := range []string{"name", "address", "direction", "nickname", "location", "subway_line"} {
			value := nextSnapshot[field]
			if value == nil {
				continue
			}
			if field == "direction" && strings.TrimSpace(next.Direction) == "" {
				continue
			}
			if (field == "nickname" || field == "subway_line") && len(value.([]string)) == 0 {
				continue
			}
			item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, field, nil, value)
		}
		return item
	}

	prevSnapshot := buildBasicSnapshot(*prev)
	nextSnapshot := buildBasicSnapshot(next)
	for _, field := range []string{"name", "address", "direction", "nickname", "location", "subway_line"} {
		from := prevSnapshot[field]
		to := nextSnapshot[field]
		if arcadeinternal.JSONValueEqual(from, to) {
			continue
		}
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, field, from, to)
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet(
			"arcade.changelog.basic."+field+".changed",
			map[string]any{"from": from, "to": to},
		))
	}

	if len(item.Diff) == 0 {
		item.ChangeType = "unchanged"
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.basic.no_changes", nil))
		item.Diff = nil
		return item
	}

	item.ChangeType = "updated"
	return item
}
