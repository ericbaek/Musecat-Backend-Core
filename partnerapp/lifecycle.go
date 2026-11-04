package partnerapp

import (
	"database/sql"
	"errors"
	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// SuspendOwnedApplications runs inside account withdrawal. Disable registrations
// in the same transaction, so existing delegated access stops before commit.
func SuspendOwnedApplications(app core.App, userID string) error {
	rows, err := app.FindRecordsByFilter(Applications, "owner={:u}", "", 0, 0, dbx.Params{"u": userID})
	if err != nil {
		return err
	}
	for _, r := range rows {
		r.Set("status", StatusSuspended)
		r.Set("review_note", "owner_withdrawn")
		r.Set("direction_b_credentials", "")
		r.Set("joint_credentials", "")
		if err := app.Save(r); err != nil {
			return err
		}
		registration, err := app.FindFirstRecordByFilter(delegation.Registrations, "client={:c}", dbx.Params{"c": r.GetString("partner_id")})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err == nil {
			registration.Set("enabled", false)
			if err := app.Save(registration); err != nil {
				return err
			}
		}
		if err := logEvent(app, r.Id, userID, "suspended", "owner_withdrawn"); err != nil {
			return err
		}
	}
	runs, err := app.FindRecordsByFilter(CheckRuns, "(actor={:u} || application.owner={:u}) && (status='queued' || status='running')", "", 0, 0, dbx.Params{"u": userID})
	if err != nil {
		return err
	}
	for _, run := range runs {
		run.Set("status", "cancelled")
		run.Set("fixtures", "")
		if err := app.Save(run); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) activeOwner(app core.App, application *core.Record) bool {
	owner, err := app.FindRecordById("user", application.GetString("owner"))
	return err == nil && !owner.GetBool("withdrawn") && (s.manager == nil || s.manager.CheckUser(app, owner) == nil)
}
