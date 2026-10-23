package reporting

import (
	"time"

	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type nativeDevicePreference struct {
	Arcade string `json:"arcade"`
	Device string `json:"device"`
}

func NativeAPNSSubscriptionStatus(e *core.RequestEvent) error {
	var body nativeDevicePreference
	if err := e.BindBody(&body); err != nil || body.Arcade == "" || body.Device == "" || len(body.Arcade) > 15 || len(body.Device) > 15 {
		return e.BadRequestError("arcade and device are required", nil)
	}
	if _, err := target(e, body.Arcade); err != nil {
		return err
	}
	device, err := e.App.FindRecordById(usernotification.Devices, body.Device)
	if err != nil || device.GetString("user") != e.Auth.Id || !usernotification.IsAPNSDevice(device) {
		return e.NotFoundError("device not registered", nil)
	}
	enabled, err := usernotification.ReportDeviceEnabled(e.App, e.Auth, body.Arcade, device.Id)
	if err != nil {
		return err
	}
	return e.JSON(200, map[string]bool{"enabled": enabled})
}

func UpdateNativeAPNSSubscription(e *core.RequestEvent) error {
	var body nativeDevicePreference
	if err := e.BindBody(&body); err != nil || body.Arcade == "" || body.Device == "" || len(body.Arcade) > 15 || len(body.Device) > 15 {
		return e.BadRequestError("arcade and device are required", nil)
	}
	err := e.App.RunInTransaction(func(tx core.App) error {
		device, err := tx.FindRecordById(usernotification.Devices, body.Device)
		if err != nil || device.GetString("user") != e.Auth.Id || !usernotification.IsAPNSDevice(device) {
			return e.NotFoundError("device not registered", nil)
		}
		enabled := e.Request.Method == "PUT"
		if enabled {
			arcade, err := tx.FindRecordById("arcade", body.Arcade)
			if err != nil {
				return e.NotFoundError("arcade not found", nil)
			}
			user, err := tx.FindRecordById("user", e.Auth.Id)
			if err != nil || !arcadereport.CanManage(user, arcade) {
				return e.ForbiddenError("arcade management is not permitted", nil)
			}
		}
		return setDevicePreferenceTx(tx, body.Arcade, device.Id, enabled)
	})
	if err != nil {
		return err
	}
	return e.JSON(200, map[string]bool{"ok": true})
}

// Both browser subscription adapters and APNs device adapters persist the same
// preference, including cancellation of queued reports on explicit opt-out.
func setDevicePreferenceTx(app core.App, arcadeID, deviceID string, enabled bool) error {
	rows, err := app.FindRecordsByFilter(arcadereport.Subscriptions, "arcade={:arcade} && device={:device}", "", 1, 0, dbx.Params{"arcade": arcadeID, "device": deviceID})
	if err != nil {
		return err
	}
	var pref *core.Record
	if len(rows) > 0 {
		pref = rows[0]
	} else {
		col, err := app.FindCollectionByNameOrId(arcadereport.Subscriptions)
		if err != nil {
			return err
		}
		pref = core.NewRecord(col)
	}
	pref.Set("arcade", arcadeID)
	pref.Set("device", deviceID)
	pref.Set("enabled", enabled)
	if err := app.Save(pref); err != nil {
		return err
	}
	if enabled {
		return nil
	}
	pending, err := app.FindRecordsByFilter(usernotification.Deliveries, "device={:device} && notification.arcade={:arcade} && notification.kind='report_created' && done=false", "", 0, 0, dbx.Params{"device": deviceID, "arcade": arcadeID})
	if err != nil {
		return err
	}
	for _, row := range pending {
		row.Set("done", true)
		row.Set("completed_at", time.Now().UTC())
		if err := app.Save(row); err != nil {
			return err
		}
	}
	return nil
}
