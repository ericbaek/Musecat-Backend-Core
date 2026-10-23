package reporting

import (
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
	"net/url"
	"strings"
)

func target(re *core.RequestEvent, id string) (*core.Record, error) {
	arcade, err := re.App.FindRecordById("arcade", strings.TrimSpace(id))
	if err != nil {
		return nil, re.NotFoundError("arcade not found", nil)
	}
	if !arcadereport.CanManage(re.Auth, arcade) {
		return nil, re.ForbiddenError("arcade management is not permitted", nil)
	}
	return arcade, nil
}

func GetSettings(re *core.RequestEvent) error {
	arcade, err := target(re, re.Request.URL.Query().Get("arcade"))
	if err != nil {
		return err
	}
	return re.JSON(http.StatusOK, arcadereport.Read(arcade))
}

func UpdateSettings(re *core.RequestEvent) error {
	var body struct {
		Arcade string `json:"arcade"`
		arcadereport.Settings
	}
	if err := re.BindBody(&body); err != nil {
		return re.BadRequestError("invalid settings", err)
	}
	if err := body.Settings.Validate(); err != nil {
		return re.BadRequestError(err.Error(), nil)
	}
	err := re.App.RunInTransaction(func(tx core.App) error {
		arcade, err := tx.FindRecordById("arcade", body.Arcade)
		if err != nil {
			return re.NotFoundError("arcade not found", nil)
		}
		user, err := tx.FindRecordById("user", re.Auth.Id)
		if err != nil || !arcadereport.CanManage(user, arcade) {
			return re.ForbiddenError("arcade management is not permitted", nil)
		}
		arcade.Set("report_settings", body.Settings)
		return tx.Save(arcade)
	})
	if err != nil {
		return err
	}
	return re.JSON(http.StatusOK, body.Settings)
}

type PushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// PushEndpointAllowed limits delivery to browser push services, never arbitrary URLs.
func PushEndpointAllowed(endpoint string) bool {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Fragment != "" {
		return false
	}
	host := u.Hostname()
	return host == "fcm.googleapis.com" || host == "jmt17.google.com" || host == "updates.push.services.mozilla.com" || host == "web.push.apple.com" || strings.HasSuffix(host, ".push.apple.com") || strings.HasSuffix(host, ".notify.windows.com")
}

func SaveSubscription(re *core.RequestEvent) error {
	var body struct {
		Arcade       string           `json:"arcade"`
		Subscription PushSubscription `json:"subscription"`
	}
	if err := re.BindBody(&body); err != nil {
		return re.BadRequestError("invalid subscription", err)
	}
	s := body.Subscription
	if !ValidPushSubscription(s) {
		return re.BadRequestError("invalid push subscription", nil)
	}
	err := re.App.RunInTransaction(func(tx core.App) error {
		arcade, err := tx.FindRecordById("arcade", body.Arcade)
		if err != nil {
			return re.NotFoundError("arcade not found", nil)
		}
		user, err := tx.FindRecordById("user", re.Auth.Id)
		if err != nil || !arcadereport.CanManage(user, arcade) {
			return re.ForbiddenError("arcade management is not permitted", nil)
		}
		device, err := usernotification.UpsertDeviceTx(tx, user.Id, s.Endpoint, s, "")
		if err != nil {
			return err
		}
		return setDevicePreferenceTx(tx, arcade.Id, device.Id, true)
	})
	if err != nil {
		return err
	}
	return re.JSON(http.StatusOK, map[string]bool{"ok": true})
}

func DeleteSubscription(re *core.RequestEvent) error {
	var body struct {
		Arcade   string `json:"arcade"`
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.BadRequestError("invalid subscription", err)
	}
	// Explicit per-arcade opt-out leaves this device enabled for other activity.
	err := re.App.RunInTransaction(func(tx core.App) error {
		devices, err := tx.FindRecordsByFilter(usernotification.Devices, "user={:user} && endpoint={:endpoint}", "", 1, 0, dbx.Params{"user": re.Auth.Id, "endpoint": body.Endpoint})
		if err != nil {
			return err
		}
		if len(devices) == 0 {
			return nil
		}
		return setDevicePreferenceTx(tx, body.Arcade, devices[0].Id, false)
	})
	if err != nil {
		return err
	}
	return re.JSON(http.StatusOK, map[string]bool{"ok": true})
}

// SubscriptionStatus only exposes whether this user's device is registered for this arcade.
func SubscriptionStatus(re *core.RequestEvent) error {
	var body struct {
		Arcade   string `json:"arcade"`
		Endpoint string `json:"endpoint"`
	}
	if err := re.BindBody(&body); err != nil || len(body.Endpoint) > 2048 || !PushEndpointAllowed(body.Endpoint) {
		return re.BadRequestError("invalid subscription", nil)
	}
	if _, err := target(re, body.Arcade); err != nil {
		return err
	}
	rows, err := re.App.FindRecordsByFilter(arcadereport.Subscriptions, "arcade={:arcade} && device.user={:user} && device.endpoint={:endpoint} && enabled=true", "", 1, 0, dbx.Params{"arcade": body.Arcade, "user": re.Auth.Id, "endpoint": body.Endpoint})
	if err != nil {
		return err
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(http.StatusOK, map[string]bool{"enabled": len(rows) > 0})
}

func ValidPushSubscription(s PushSubscription) bool {
	key, keyErr := base64.RawURLEncoding.DecodeString(s.Keys.P256dh)
	auth, authErr := base64.RawURLEncoding.DecodeString(s.Keys.Auth)
	x, _ := elliptic.Unmarshal(elliptic.P256(), key)
	return !(len(s.Endpoint) > 2048 || !PushEndpointAllowed(s.Endpoint) || keyErr != nil || len(key) != 65 || key[0] != 4 || x == nil || authErr != nil || len(auth) != 16)
}
