package notifications

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/google/uuid"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

type NativeAPNSConfig struct {
	Enabled     bool
	Topic       string
	Environment string
}

// RegisterNativeAPNSRoutes requires the existing opaque-session guarded group.
// Full supplies provider availability; Core does not load keys or send pushes.
func RegisterNativeAPNSRoutes(secured *router.RouterGroup[*core.RequestEvent], config NativeAPNSConfig) {
	available := config.Enabled && usernotification.ValidAPNSNamespace(config.Topic, config.Environment)
	group := secured.Group("/user/notifications/apns").Bind(apis.BodyLimit(4096))
	group.GET("", func(e *core.RequestEvent) error {
		if !available {
			return e.JSON(200, map[string]bool{"available": false})
		}
		return e.JSON(200, map[string]any{"available": true, "environment": config.Environment, "topic": config.Topic})
	})
	group.PUT("", func(e *core.RequestEvent) error {
		if !available {
			return e.JSON(http.StatusServiceUnavailable, map[string]string{"error": "APNs is not configured", "code": "APNS_UNAVAILABLE"})
		}
		var body struct {
			Token  string `json:"token"`
			Locale string `json:"locale"`
		}
		if err := e.BindBody(&body); err != nil {
			return e.BadRequestError("invalid APNs registration", nil)
		}
		token, valid := usernotification.NormalizeAPNSToken(body.Token)
		if !valid || (body.Locale != "ko" && body.Locale != "en" && body.Locale != "ja") {
			return e.BadRequestError("invalid APNs registration", nil)
		}
		session, _ := e.Get(nativeauth.SessionContextKey).(string)
		if session == "" {
			return e.UnauthorizedError("native session required", nil)
		}
		subscription := usernotification.APNSDevice{Provider: "apns", Token: token, Topic: config.Topic, Environment: config.Environment, Session: session, Registration: uuid.NewString(), RegisteredAt: time.Now().UTC().UnixMilli()}
		var deviceID string
		err := e.App.RunInTransaction(func(tx core.App) error {
			current, err := (nativeauth.Service{}).SessionUser(tx, session)
			if errors.Is(err, nativeauth.ErrInvalid) || (err == nil && current.Id != e.Auth.Id) {
				return e.UnauthorizedError("native session required", nil)
			}
			if err != nil {
				return err
			}
			device, err := usernotification.UpsertAPNSDeviceTx(tx, e.Auth.Id, subscription, body.Locale)
			if err == nil {
				deviceID = device.Id
			}
			return err
		})
		if err != nil {
			return err
		}
		return e.JSON(200, map[string]any{"enabled": true, "device": deviceID})
	})
	group.DELETE("", DeleteNativeAPNSDevice)
}

func DeleteNativeAPNSDevice(e *core.RequestEvent) error {
	var body struct {
		Device string `json:"device"`
	}
	if err := e.BindBody(&body); err != nil || body.Device == "" || len(body.Device) > 15 {
		return e.BadRequestError("device is required", nil)
	}
	err := e.App.RunInTransaction(func(tx core.App) error {
		device, err := tx.FindRecordById(usernotification.Devices, body.Device)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if device.GetString("user") != e.Auth.Id || !usernotification.IsAPNSDevice(device) {
			return nil
		}
		return tx.Delete(device)
	})
	if err != nil {
		return err
	}
	return e.JSON(200, map[string]bool{"enabled": false})
}
