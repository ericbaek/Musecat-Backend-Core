package hour

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

// BuildArcadeHourExpandedValue 는 GET /arcade?expand=hour 와 같은 형태의 hour 값을 만든다.
func BuildArcadeHourExpandedValue(rec *core.Record) map[string]any {
	if rec == nil {
		return nil
	}
	note := strings.TrimSpace(rec.GetString("Note"))
	if note == "" {
		note = strings.TrimSpace(rec.GetString("note"))
	}
	return map[string]any{
		"id":        rec.Id,
		"Monday":    normalizeHourFieldValue(rec.GetRaw("Monday")),
		"Tuesday":   normalizeHourFieldValue(rec.GetRaw("Tuesday")),
		"Wednesday": normalizeHourFieldValue(rec.GetRaw("Wednesday")),
		"Thursday":  normalizeHourFieldValue(rec.GetRaw("Thursday")),
		"Friday":    normalizeHourFieldValue(rec.GetRaw("Friday")),
		"Saturday":  normalizeHourFieldValue(rec.GetRaw("Saturday")),
		"Sunday":    normalizeHourFieldValue(rec.GetRaw("Sunday")),
		"Note":      note,
	}
}

type hourDiffLogItem struct {
	ChangeType string                      `json:"change_type"`
	Bullets    []arcadeinternal.I18nBullet `json:"bullets"`
	Diff       []map[string]any            `json:"diff,omitempty"`
}

func normalizeHourFieldValue(raw any) any {
	if raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) == "" || strings.TrimSpace(v) == "null" {
			return nil
		}
	case []byte:
		trimmed := bytes.TrimSpace(v)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			return nil
		}
	}

	switch v := raw.(type) {
	case int:
		if v == 499 {
			return 499
		}
	case int64:
		if v == 499 {
			return 499
		}
	case float64:
		if int(v) == 499 {
			return 499
		}
	}

	buf, err := json.Marshal(raw)
	if err != nil {
		return raw
	}
	// PocketBase JSON null values can arrive as zero-value JSONRaw bytes. Treat
	// them as nil before decoding into the day-hours shape to avoid 0/0 false positives.
	if trimmed := bytes.TrimSpace(buf); bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	var day struct {
		Start int `json:"start"`
		End   int `json:"end"`
	}
	if err := json.Unmarshal(buf, &day); err != nil {
		return raw
	}
	return map[string]int{"start": day.Start, "end": day.End}
}

func hourBodyDayValue(day *DayHours) any {
	if day == nil || day.Unknown {
		return nil
	}
	if day.Closed {
		return 499
	}
	return map[string]int{"start": *day.Start, "end": *day.End}
}

func buildHourSnapshotFromRecord(rec *core.Record) map[string]any {
	if rec == nil {
		return map[string]any{}
	}
	note := strings.TrimSpace(rec.GetString("Note"))
	if note == "" {
		note = strings.TrimSpace(rec.GetString("note"))
	}
	return map[string]any{
		"Monday":    normalizeHourFieldValue(rec.GetRaw("Monday")),
		"Tuesday":   normalizeHourFieldValue(rec.GetRaw("Tuesday")),
		"Wednesday": normalizeHourFieldValue(rec.GetRaw("Wednesday")),
		"Thursday":  normalizeHourFieldValue(rec.GetRaw("Thursday")),
		"Friday":    normalizeHourFieldValue(rec.GetRaw("Friday")),
		"Saturday":  normalizeHourFieldValue(rec.GetRaw("Saturday")),
		"Sunday":    normalizeHourFieldValue(rec.GetRaw("Sunday")),
		"Note":      note,
	}
}

func buildHourSnapshotFromBody(body UpdateArcadeHourBody) map[string]any {
	return map[string]any{
		"Monday":    hourBodyDayValue(body.Monday),
		"Tuesday":   hourBodyDayValue(body.Tuesday),
		"Wednesday": hourBodyDayValue(body.Wednesday),
		"Thursday":  hourBodyDayValue(body.Thursday),
		"Friday":    hourBodyDayValue(body.Friday),
		"Saturday":  hourBodyDayValue(body.Saturday),
		"Sunday":    hourBodyDayValue(body.Sunday),
		"Note":      strings.TrimSpace(body.Note),
	}
}

func buildHourProvidedFieldSet(body UpdateArcadeHourBody) map[string]bool {
	return map[string]bool{
		"Monday":    body.Monday != nil,
		"Tuesday":   body.Tuesday != nil,
		"Wednesday": body.Wednesday != nil,
		"Thursday":  body.Thursday != nil,
		"Friday":    body.Friday != nil,
		"Saturday":  body.Saturday != nil,
		"Sunday":    body.Sunday != nil,
		"Note":      strings.TrimSpace(body.Note) != "",
	}
}

// buildHourDiffLogItem compares raw stored values against the requested shape so
// nil, 499, and 24-hour representations remain distinguishable in the changelog.
func buildHourDiffLogItem(prev, next map[string]any, provided map[string]bool, hadPrev bool) hourDiffLogItem {
	item := hourDiffLogItem{
		Bullets: []arcadeinternal.I18nBullet{},
		Diff:    []map[string]any{},
	}

	fields := []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday", "Note"}
	for _, field := range fields {
		from := prev[field]
		to := next[field]
		if !hadPrev && !provided[field] {
			continue
		}
		if hadPrev && arcadeinternal.JSONValueEqual(from, to) {
			continue
		}
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, field, from, to)
		if hadPrev {
			item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet(
				fmt.Sprintf("arcade.changelog.hour.%s.changed", strings.ToLower(field)),
				map[string]any{"from": from, "to": to},
			))
		}
	}

	if !hadPrev {
		item.ChangeType = "added"
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.hour.added", nil))
		return item
	}

	if len(item.Diff) == 0 {
		item.ChangeType = "unchanged"
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.hour.no_changes", nil))
		item.Diff = nil
		return item
	}

	item.ChangeType = "updated"
	return item
}
