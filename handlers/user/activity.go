package user

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

const (
	defaultActivityDays = 365
	maxActivityDays     = 365
	activityDateLayout  = "2006-01-02"
)

var activityNow = func() time.Time {
	return time.Now().UTC()
}

type ActivityRange struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
	TZ        string `json:"tz"`
	Days      int    `json:"days"`
}

type ActivityTotals struct {
	TotalCount        int `json:"total_count"`
	ChangelogCount    int `json:"changelog_count"`
	FlagCount         int `json:"flag_count"`
	LegacyTicketCount int `json:"legacy_ticket_count"`
	AttendanceCount   int `json:"attendance_count"`
	MaxDailyCount     int `json:"max_daily_count"`
}

type ActivityDay struct {
	Date              string `json:"date"`
	TotalCount        int    `json:"total_count"`
	Level             int    `json:"level"`
	ChangelogCount    int    `json:"changelog_count"`
	FlagCount         int    `json:"flag_count"`
	LegacyTicketCount int    `json:"legacy_ticket_count"`
	AttendanceCount   int    `json:"attendance_count"`
}

type ActivityResponse struct {
	User   *Profile       `json:"user"`
	Range  ActivityRange  `json:"range"`
	Totals ActivityTotals `json:"totals"`
	Days   []*ActivityDay `json:"days"`
}

// GetUserActivity handles GET /user/activity?id=<userId>|username=<username>&tz=<timezone>&days=<1..365>.
func GetUserActivity(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	id := strings.TrimSpace(q.Get("id"))
	username := strings.TrimSpace(q.Get("username"))

	if (id == "" && username == "") || (id != "" && username != "") {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "exactly one of query param 'id' or 'username' is required",
		})
	}

	loc, tzName, err := parseActivityTimezone(q.Get("tz"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid 'tz' value; expected IANA timezone",
			"details": err.Error(),
		})
	}

	days, err := parseActivityDays(q.Get("days"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "invalid 'days' value; expected integer between 1 and 365",
		})
	}

	profile, err := resolveActivityProfile(re.App, id, username)
	if err != nil {
		if isNotFoundError(err) {
			return re.JSON(http.StatusNotFound, map[string]any{
				"error": "user not found",
			})
		}

		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load user profile",
			"details": err.Error(),
		})
	}

	resp, err := BuildUserActivity(re.App, profile, loc, tzName, days, activityNow())
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load user activity",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, resp)
}

func resolveActivityProfile(app core.App, id, username string) (*Profile, error) {
	if id != "" {
		return FetchMergedProfile(app, id)
	}
	return FetchMergedProfileByUsername(app, username)
}

func parseActivityTimezone(raw string) (*time.Location, string, error) {
	tzName := strings.TrimSpace(raw)
	if tzName == "" {
		return time.UTC, "UTC", nil
	}

	loc, err := time.LoadLocation(tzName)
	if err != nil {
		return nil, "", err
	}

	return loc, tzName, nil
}

func parseActivityDays(raw string) (int, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return defaultActivityDays, nil
	}

	days, err := strconv.Atoi(value)
	if err != nil || days < 1 || days > maxActivityDays {
		return 0, fmt.Errorf("invalid days")
	}

	return days, nil
}

func computeActivityLevel(count, maxDaily int) int {
	if count <= 0 || maxDaily <= 0 {
		return 0
	}

	level := int(math.Ceil(float64(count*4) / float64(maxDaily)))
	if level < 1 {
		return 1
	}
	if level > 4 {
		return 4
	}
	return level
}
