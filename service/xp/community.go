package xp

import (
	"fmt"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"time"
)

// AwardCommunityTx uses the existing level ledger in the source mutation's transaction.
// Posts and reactions have separate KST-day caps. Stable event identities survive unlike/delete.
func AwardCommunityTx(app core.App, userID, category, event string, now time.Time) error {
	_, err := AwardCommunityWithFeedbackTx(app, userID, category, event, now)
	return err
}

// AwardCommunityWithFeedbackTx returns feedback only for XP actually granted in
// the caller's transaction. Nil means a cap or duplicate event.
func AwardCommunityWithFeedbackTx(app core.App, userID, category, event string, now time.Time) (*ExpFeedback, error) {
	grant, cap := 1, 5
	if category == "post" {
		grant, cap = 2, 10
	} else if category != "reaction" {
		return nil, fmt.Errorf("invalid community XP category")
	}
	prefix := "xp:community:" + category + ":"
	loc := time.FixedZone("KST", 9*60*60)
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).UTC()
	end := start.Add(24 * time.Hour)
	var used struct {
		Total int `db:"total"`
	}
	if err := app.DB().NewQuery("SELECT COALESCE(SUM(diff_exp),0) total FROM user_level_log WHERE user={:user} AND kind LIKE {:prefix} AND created>={:start} AND created<{:end}").Bind(dbx.Params{"user": userID, "prefix": prefix + "%", "start": start.Format("2006-01-02 15:04:05.000Z"), "end": end.Format("2006-01-02 15:04:05.000Z")}).One(&used); err != nil {
		return nil, err
	}
	if used.Total+grant > cap {
		return nil, nil
	}
	base, err := LoadCurrentExp(app, userID)
	if err != nil {
		return nil, err
	}
	current, awarded, err := AwardExpTx(app, userID, prefix+event, grant, base)
	if err != nil || !awarded || current <= base {
		return nil, err
	}
	feedback := BuildExpFeedback(base, current)
	return &feedback, nil
}
