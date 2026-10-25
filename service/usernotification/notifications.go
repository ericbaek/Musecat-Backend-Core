package usernotification

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"slices"
)

func CreateTx(app core.App, userID, actor, kind, event string, flag *core.Record, reactionID string) error {
	if userID == "" || (userID == actor && kind != "report_created") {
		return nil
	}
	user, err := app.FindRecordById("user", userID)
	if err != nil || user.GetBool("withdrawn") {
		return nil
	}
	existing, err := app.FindRecordsByFilter(Inbox, "user={:user} && event_key={:event}", "", 1, 0, dbx.Params{"user": userID, "event": event})
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil
	}
	col, err := app.FindCollectionByNameOrId(Inbox)
	if err != nil {
		return err
	}
	row := core.NewRecord(col)
	row.Set("user", userID)
	row.Set("actor", actor)
	row.Set("kind", kind)
	row.Set("event_key", event)
	row.Set("arcade", flag.GetString("arcade"))
	row.Set("flag", flag.Id)
	row.Set("reaction_record", reactionID)
	if err := app.Save(row); err != nil {
		return err
	}
	return enqueueDevicesTx(app, row, userID)
}
func enqueueDevicesTx(app core.App, row *core.Record, userID string) error {
	devices, err := app.FindRecordsByFilter(Devices, "user={:user}", "", 0, 0, dbx.Params{"user": userID})
	if err != nil {
		return err
	}
	deliveryCol, err := app.FindCollectionByNameOrId(Deliveries)
	if err != nil {
		return err
	}
	for _, device := range devices {
		if row.GetString("kind") == "report_created" {
			preferences, err := app.FindRecordsByFilter(arcadereport.Subscriptions, "arcade={:arcade} && device={:device}", "", 1, 0, dbx.Params{"arcade": row.GetString("arcade"), "device": device.Id})
			if err != nil {
				return err
			}
			user, err := app.FindRecordById("user", userID)
			if err != nil {
				return err
			}
			if len(preferences) > 0 && !preferences[0].GetBool("enabled") {
				continue
			}
			if !slices.Contains(user.GetStringSlice("owns"), row.GetString("arcade")) && len(preferences) == 0 {
				continue
			}
		}
		delivery := core.NewRecord(deliveryCol)
		delivery.Set("notification", row.Id)
		delivery.Set("device", device.Id)
		if err := app.Save(delivery); err != nil {
			return err
		}
	}
	return nil
}
func ReportCreatedTx(app core.App, flag, arcade *core.Record) error {
	recipients := map[string]bool{}
	owners, err := app.FindRecordsByFilter("user", "owns ~ {:arcade}", "", 0, 0, dbx.Params{"arcade": arcade.Id})
	if err != nil {
		return err
	}
	for _, owner := range owners {
		recipients[owner.Id] = true
	}
	subs, err := app.FindRecordsByFilter(arcadereport.Subscriptions, "arcade={:arcade}", "", 0, 0, dbx.Params{"arcade": arcade.Id})
	if err != nil {
		return err
	}
	explicit := map[string]bool{}
	for _, sub := range subs {
		if !sub.GetBool("enabled") {
			continue
		}
		device, err := app.FindRecordById(Devices, sub.GetString("device"))
		if err != nil {
			return err
		}
		userID := device.GetString("user")
		recipients[userID] = true
		explicit[userID] = true
	}
	for userID := range recipients {
		if userID == flag.GetString("createdBy") && !explicit[userID] {
			continue
		}
		user, err := app.FindRecordById("user", userID)
		if err == nil && arcadereport.CanManage(user, arcade) {
			if err := CreateTx(app, userID, flag.GetString("createdBy"), "report_created", "report:"+flag.Id, flag, ""); err != nil {
				return err
			}
		}
	}
	return nil
}
func ReactionCreatedTx(app core.App, flag *core.Record, actor, reactionID string) error {
	return CreateTx(app, flag.GetString("createdBy"), actor, "report_reaction", "reaction:"+reactionID, flag, reactionID)
}
func Visible(app core.App, user, row *core.Record) bool {
	if row.GetString("kind") == "message" {
		return messaging.NotificationVisible(app, user, row)
	}
	if row.GetString("kind") == ArcadeSetupKind {
		if user == nil || row.GetString("user") != user.Id {
			return false
		}
		current, err := app.FindRecordById("user", user.Id)
		if err != nil {
			return false
		}
		active, err := activeSetupOwner(app, current)
		return err == nil && active
	}
	if row.GetString("kind") == "feedback_reply" || row.GetString("kind") == "feedback_created" {
		return feedbackVisible(app, user, row)
	}
	if user == nil || user.GetBool("withdrawn") || row.GetString("user") != user.Id {
		return false
	}
	arcade, err := app.FindRecordById("arcade", row.GetString("arcade"))
	if err != nil || arcadereport.Read(arcade).Mode != "internal" {
		return false
	}
	if row.GetString("kind") == "report_created" {
		return arcadereport.CanManage(user, arcade)
	}
	return arcade.GetBool("public") || arcadereport.CanManage(user, arcade)
}
func Item(app core.App, row *core.Record) map[string]any {
	if row.GetString("kind") == "message" {
		return messaging.NotificationItem(row)
	}
	if row.GetString("kind") == ArcadeSetupKind {
		return arcadeSetupItem(row)
	}
	if row.GetString("kind") == "feedback_reply" || row.GetString("kind") == "feedback_created" {
		return feedbackItem(app, row)
	}
	name, game, message, reaction := "", "", "", ""
	if arcade, err := app.FindRecordById("arcade", row.GetString("arcade")); err == nil {
		if basic, err := app.FindRecordById("arcade_basic", arcade.GetString("basic")); err == nil {
			name = basic.GetString("name")
		}
	}
	if flag, err := app.FindRecordById("arcade_flag", row.GetString("flag")); err == nil {
		runes := []rune(flag.GetString("message"))
		if len(runes) > 160 {
			runes = runes[:160]
		}
		message = string(runes)
		if entry, err := app.FindRecordById("arcade_game_id", flag.GetString("game_id")); err == nil {
			if series, err := app.FindRecordById("game_series", entry.GetString("series")); err == nil {
				game = series.GetString("kr")
				if game == "" {
					game = series.GetString("en")
				}
			}
		}
	}
	if record, err := app.FindRecordById("arcade_flag_reaction", row.GetString("reaction_record")); err == nil {
		reaction = record.GetString("reaction")
	}
	return map[string]any{"id": row.Id, "kind": row.GetString("kind"), "read": row.GetBool("read"), "created": row.GetString("created"), "arcadeName": name, "gameName": game, "message": message, "reaction": reaction, "url": fmt.Sprintf("/arcade/%s?report=%s#report-%s", row.GetString("arcade"), row.GetString("flag"), row.GetString("flag"))}
}

// UpsertDeviceTx accepts a validated subscription from either registration API.
// Canonical credentials are stored once, while preferences reference this row.
func UpsertDeviceTx(app core.App, userID, endpoint string, subscription any, locale string) (*core.Record, error) {
	if err := ReleaseOtherEndpointOwnersTx(app, userID, endpoint); err != nil {
		return nil, err
	}
	rows, err := app.FindRecordsByFilter(Devices, "user={:user} && endpoint={:endpoint}", "", 1, 0, dbx.Params{"user": userID, "endpoint": endpoint})
	if err != nil {
		return nil, err
	}
	var row *core.Record
	if len(rows) > 0 {
		row = rows[0]
	} else {
		col, err := app.FindCollectionByNameOrId(Devices)
		if err != nil {
			return nil, err
		}
		row = core.NewRecord(col)
	}
	row.Set("user", userID)
	row.Set("endpoint", endpoint)
	row.Set("subscription", subscription)
	if locale != "" {
		row.Set("locale", locale)
	} else if row.GetString("locale") == "" {
		row.Set("locale", "ko")
	}
	if err := app.Save(row); err != nil {
		return nil, err
	}
	return row, nil
}
func RemoveDeviceTx(app core.App, userID, endpoint string) error {
	rows, err := app.FindRecordsByFilter(Devices, "user={:user} && endpoint={:endpoint}", "", 0, 0, dbx.Params{"user": userID, "endpoint": endpoint})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := app.Delete(row); err != nil {
			return err
		}
	}
	return nil
}

// ReleaseOtherEndpointOwnersTx transfers the endpoint and cascading preferences
// together, so a browser never retains credentials belonging to another account.
func ReleaseOtherEndpointOwnersTx(app core.App, userID, endpoint string) error {
	rows, err := app.FindRecordsByFilter(Devices, "endpoint={:endpoint} && user!={:user}", "", 0, 0, dbx.Params{"user": userID, "endpoint": endpoint})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := app.Delete(row); err != nil {
			return err
		}
	}
	return nil
}
