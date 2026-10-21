package usernotification

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const ArcadeSetupKind = "arcade_setup"
const ArcadeSetupEvent = "arcade_dashboard_setup:v1"

// EnsureArcadeSetupNoticeTx inserts the one-time account notice and canonical
// device deliveries in the caller's transaction. Reruns never change read state.
func EnsureArcadeSetupNoticeTx(app core.App, user *core.Record) error {
	if user == nil {
		return nil
	}
	current, err := app.FindRecordById("user", user.Id)
	if err != nil {
		return err
	}
	active, err := activeSetupOwner(app, current)
	if err != nil || !active {
		return err
	}
	rows, err := app.FindRecordsByFilter(Inbox, "user={:user} && event_key={:event}", "", 1, 0, dbx.Params{"user": current.Id, "event": ArcadeSetupEvent})
	if err != nil {
		return err
	}
	if len(rows) > 0 {
		return nil
	}
	col, err := app.FindCollectionByNameOrId(Inbox)
	if err != nil {
		return err
	}
	row := core.NewRecord(col)
	row.Set("user", current.Id)
	row.Set("kind", ArcadeSetupKind)
	row.Set("event_key", ArcadeSetupEvent)
	if err := app.Save(row); err != nil {
		return err
	}
	return enqueueDevicesTx(app, row, current.Id)
}

func activeSetupOwner(app core.App, user *core.Record) (bool, error) {
	if user == nil || user.GetBool("withdrawn") || !slices.Contains(user.GetStringSlice("tags"), "arcade_owner") {
		return false, nil
	}
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(user.Email()))))
	bans, err := app.FindRecordsByFilter("user_ban", "id={:user} || hashed_email={:email}", "", 0, 0, dbx.Params{"user": user.Id, "email": hex.EncodeToString(sum[:])})
	if err != nil {
		return false, err
	}
	for _, ban := range bans {
		until := strings.TrimSpace(ban.GetString("until"))
		if until == "" {
			return false, nil
		}
		date, err := types.ParseDateTime(until)
		if err == nil && !date.IsZero() && date.Time().After(time.Now().UTC()) {
			return false, nil
		}
	}
	return true, nil
}

func arcadeSetupItem(row *core.Record) map[string]any {
	return map[string]any{"id": row.Id, "kind": ArcadeSetupKind, "read": row.GetBool("read"), "created": row.GetString("created"), "arcadeName": "", "gameName": "", "message": "새 관리자 대시보드를 설정해주세요", "reaction": "", "url": "/account/arcade-dashboard/setup"}
}
