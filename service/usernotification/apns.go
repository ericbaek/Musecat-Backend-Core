package usernotification

import (
	"encoding/hex"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// APNSDevice uses the existing private device row and delivery relation. The
// endpoint is an internal identity, never a user-selected network destination.
type APNSDevice struct {
	Provider     string `json:"provider"`
	Token        string `json:"token"`
	Environment  string `json:"environment"`
	Topic        string `json:"topic"`
	Session      string `json:"session"`
	Registration string `json:"registration"`
	RegisteredAt int64  `json:"registered_at"`
}

var apnsTopicPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,254}$`)

func ValidAPNSNamespace(topic, environment string) bool {
	return apnsTopicPattern.MatchString(topic) && (environment == "sandbox" || environment == "production")
}

// Apple device token sizes are variable. This is an input ceiling, not an
// assumption that every token is the common 32-byte value.
func NormalizeAPNSToken(token string) (string, bool) {
	if token == "" || len(token) > 1024 || len(token)%2 != 0 {
		return "", false
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", false
	}
	return strings.ToLower(token), true
}

func (d APNSDevice) Endpoint() string {
	return "apns://" + d.Environment + "/" + d.Topic + "/" + d.Token
}

func IsAPNSDevice(device *core.Record) bool {
	return device != nil && strings.HasPrefix(device.GetString("endpoint"), "apns://")
}

func DecodeAPNSDevice(device *core.Record) (APNSDevice, bool) {
	var value APNSDevice
	if !IsAPNSDevice(device) || json.Unmarshal([]byte(device.GetString("subscription")), &value) != nil {
		return value, false
	}
	token, ok := NormalizeAPNSToken(value.Token)
	return value, ok && token == value.Token && value.Provider == "apns" &&
		ValidAPNSNamespace(value.Topic, value.Environment) && value.Session != "" && value.Registration != "" && value.RegisteredAt > 0 &&
		value.Endpoint() == device.GetString("endpoint")
}

// UpsertAPNSDeviceTx preserves device-linked preferences during token rotation
// within one native session. A different session gets a new row, so stale client
// cleanup cannot remove a registration adopted by a newer session.
func UpsertAPNSDeviceTx(app core.App, userID string, subscription APNSDevice, locale string) (*core.Record, error) {
	rows, err := app.FindRecordsByFilter(Devices, "user={:user}", "", 0, 0, dbx.Params{"user": userID})
	if err != nil {
		return nil, err
	}
	var device *core.Record
	for _, row := range rows {
		stored, valid := DecodeAPNSDevice(row)
		if valid && stored.Session == subscription.Session && stored.Environment == subscription.Environment && stored.Topic == subscription.Topic {
			device = row
			break
		}
	}
	collisions, err := app.FindRecordsByFilter(Devices, "endpoint={:endpoint}", "", 0, 0, dbx.Params{"endpoint": subscription.Endpoint()})
	if err != nil {
		return nil, err
	}
	for _, row := range collisions {
		if device == nil || row.Id != device.Id {
			if err := app.Delete(row); err != nil {
				return nil, err
			}
		}
	}
	if device == nil {
		collection, err := app.FindCollectionByNameOrId(Devices)
		if err != nil {
			return nil, err
		}
		device = core.NewRecord(collection)
	}
	device.Set("user", userID)
	device.Set("endpoint", subscription.Endpoint())
	device.Set("subscription", subscription)
	device.Set("locale", locale)
	if err := app.Save(device); err != nil {
		return nil, err
	}
	return device, nil
}
