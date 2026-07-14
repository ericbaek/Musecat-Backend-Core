package user

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

const checkInExp = 2

func CheckIn(re *core.RequestEvent) error {
	if re.Auth == nil || strings.TrimSpace(re.Auth.Id) == "" {
		return re.JSON(http.StatusUnauthorized, map[string]any{
			"error": "authentication required",
		})
	}

	now := attendanceNow()
	day := KSTDay(now)
	baseExp, err := LoadCurrentExp(re.App, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load current exp",
			"details": err.Error(),
		})
	}

	var currentExp int
	var granted bool
	if err := re.App.RunInTransaction(func(txApp core.App) error {
		var err error
		currentExp, granted, err = AwardExpTx(txApp, re.Auth.Id, CheckInKind(day), checkInExp, baseExp)
		return err
	}); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "check-in failed",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"checked_in":         granted,
		"already_checked_in": !granted,
		"gained_exp": func() int {
			if granted {
				return checkInExp
			}
			return 0
		}(),
		"exp":         currentExp,
		"level":       LevelFromExp(currentExp),
		"day":         day,
		"xp_feedback": BuildExpFeedback(baseExp, currentExp),
	})
}
