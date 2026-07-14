package user

import (
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

type activitySource struct {
	table       string
	authorField string
	target      string
}

var activitySources = []activitySource{
	{table: "arcade_changelog", authorField: `"by"`, target: "changelog"},
	{table: "arcade_flag", authorField: "createdBy", target: "flag"},
	{table: "arcade_flag_reaction", authorField: "createdBy", target: "flag"},
	{table: "z_legacy_tickets", authorField: "createdBy", target: "legacy"},
}

func BuildUserActivity(app core.App, profile *Profile, loc *time.Location, tzName string, days int, now time.Time) (*ActivityResponse, error) {
	if profile == nil {
		return nil, fmt.Errorf("profile is required")
	}
	if loc == nil {
		loc = time.UTC
	}

	endLocal := now.In(loc)
	startDay := time.Date(endLocal.Year(), endLocal.Month(), endLocal.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(days - 1))
	endExclusive := startDay.AddDate(0, 0, days)

	outDays := make([]*ActivityDay, 0, days)
	byDate := make(map[string]*ActivityDay, days)
	for i := 0; i < days; i++ {
		date := startDay.AddDate(0, 0, i).Format(activityDateLayout)
		day := &ActivityDay{Date: date}
		outDays = append(outDays, day)
		byDate[date] = day
	}

	startUTC := startDay.UTC().Format(types.DefaultDateLayout)
	endUTC := endExclusive.UTC().Format(types.DefaultDateLayout)

	for _, source := range activitySources {
		timestamps, err := loadActivityCreatedAt(app, source.table, source.authorField, profile.ID, startUTC, endUTC)
		if err != nil {
			return nil, err
		}

		for _, ts := range timestamps {
			dayKey := ts.In(loc).Format(activityDateLayout)
			day := byDate[dayKey]
			if day == nil {
				continue
			}

			switch source.target {
			case "changelog":
				day.ChangelogCount++
			case "flag":
				day.FlagCount++
			case "legacy":
				day.LegacyTicketCount++
			}
		}
	}

	attendanceTimestamps, err := loadAttendanceCreatedAt(app, profile.ID, startUTC, endUTC)
	if err != nil {
		return nil, err
	}
	for _, ts := range attendanceTimestamps {
		dayKey := ts.In(loc).Format(activityDateLayout)
		day := byDate[dayKey]
		if day == nil {
			continue
		}
		day.AttendanceCount++
	}

	resp := &ActivityResponse{
		User: profile,
		Range: ActivityRange{
			StartDate: startDay.Format(activityDateLayout),
			EndDate:   endExclusive.AddDate(0, 0, -1).Format(activityDateLayout),
			TZ:        tzName,
			Days:      days,
		},
		Days: outDays,
	}

	for _, day := range outDays {
		day.TotalCount = day.ChangelogCount + day.FlagCount + day.LegacyTicketCount + day.AttendanceCount
		resp.Totals.TotalCount += day.TotalCount
		resp.Totals.ChangelogCount += day.ChangelogCount
		resp.Totals.FlagCount += day.FlagCount
		resp.Totals.LegacyTicketCount += day.LegacyTicketCount
		resp.Totals.AttendanceCount += day.AttendanceCount
		if day.TotalCount > resp.Totals.MaxDailyCount {
			resp.Totals.MaxDailyCount = day.TotalCount
		}
	}

	for _, day := range outDays {
		day.Level = computeActivityLevel(day.TotalCount, resp.Totals.MaxDailyCount)
	}

	return resp, nil
}

func loadActivityCreatedAt(app core.App, table, authorField, userID, startUTC, endUTC string) ([]time.Time, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, nil
	}

	sql := fmt.Sprintf(`
SELECT created
FROM %s
WHERE %s = {:userId}
  AND %s != ''
  AND created != ''
  AND created >= {:start}
  AND created < {:end}
ORDER BY created ASC
`, table, authorField, authorField)

	rows, err := app.DB().NewQuery(sql).
		Bind(dbx.Params{
			"userId": userID,
			"start":  startUTC,
			"end":    endUTC,
		}).
		Rows()
	if err != nil {
		return nil, fmt.Errorf("query %s failed: %w", table, err)
	}
	defer rows.Close()

	out := make([]time.Time, 0)
	for rows.Next() {
		var created string
		if err := rows.Scan(&created); err != nil {
			return nil, fmt.Errorf("scan %s failed: %w", table, err)
		}
		dt, err := types.ParseDateTime(created)
		if err != nil {
			return nil, fmt.Errorf("parse %s.created failed: %w", table, err)
		}
		out = append(out, dt.Time().UTC())
	}

	return out, rows.Err()
}

func loadAttendanceCreatedAt(app core.App, userID, startUTC, endUTC string) ([]time.Time, error) {
	rows, err := app.DB().NewQuery(`
SELECT created
FROM user_level_log
WHERE "user" = {:userId}
  AND kind LIKE 'xp:attendance:service:%'
  AND created != ''
  AND created >= {:start}
  AND created < {:end}
ORDER BY created ASC
`).Bind(dbx.Params{
		"userId": userID,
		"start":  startUTC,
		"end":    endUTC,
	}).Rows()
	if err != nil {
		return nil, fmt.Errorf("query attendance log failed: %w", err)
	}
	defer rows.Close()

	out := make([]time.Time, 0)
	for rows.Next() {
		var created string
		if err := rows.Scan(&created); err != nil {
			return nil, fmt.Errorf("scan attendance log failed: %w", err)
		}
		dt, err := types.ParseDateTime(created)
		if err != nil {
			return nil, fmt.Errorf("parse attendance.created failed: %w", err)
		}
		out = append(out, dt.Time().UTC())
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate attendance log failed: %w", err)
	}
	return out, nil
}
