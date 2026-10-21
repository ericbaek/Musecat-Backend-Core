package supportfeedback

import (
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"slices"
)

type InquirySettingsData struct {
	arcadereport.Settings
	LoginRequired bool `json:"loginRequired"`
	Configured    bool `json:"configured"`
}

// Inquiry routing is independent from fault-report routing. Existing conversations remain accessible.
func InquirySettings(arcade *core.Record) InquirySettingsData {
	s := InquirySettingsData{}
	if arcade != nil {
		if err := json.Unmarshal([]byte(arcade.GetString("inquiry_settings")), &s); err != nil {
			s = InquirySettingsData{}
		}
	}
	s.Configured = s.Mode == "internal" || s.Mode == "external" || s.Mode == "disabled"
	if !s.Configured {
		s.Mode = "disabled"
	}
	return s
}

// Validate shares contact limits with reports while allowing an explicit OFF choice.
func (s *InquirySettingsData) Validate() error {
	settings := s.Settings
	if settings.Mode == "disabled" {
		settings.Mode = "internal"
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	s.ContactValue, s.Guide = settings.ContactValue, settings.Guide
	return nil
}

// Stored excludes derived response metadata, including any client-supplied flag.
func (s InquirySettingsData) Stored() any {
	return struct {
		arcadereport.Settings
		LoginRequired bool `json:"loginRequired"`
	}{s.Settings, s.LoginRequired}
}

func EnsureInquirySettingsSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		arcade, err := tx.FindCollectionByNameOrId("arcade")
		if err != nil {
			return err
		}
		old := arcade.Fields.GetByName("inquiry_settings")
		if old == nil {
			arcade.Fields.Add(&core.JSONField{Name: "inquiry_settings", MaxSize: arcadereport.SettingsMaxSize})
		} else if field, ok := old.(*core.JSONField); !ok || (field.MaxSize != arcadereport.LegacySettingsMaxSize && field.MaxSize != arcadereport.SettingsMaxSize) {
			return fmt.Errorf("incompatible arcade.inquiry_settings")
		}
		return tx.Save(arcade)
	})
}

// HasOfficialAccount checks exact roles after narrowing by ownership.
func HasOfficialAccount(app core.App, arcadeID string) bool {
	users, err := app.FindRecordsByFilter("user", "owns ~ {:arcade} && tags ~ 'arcade_owner' && withdrawn = false", "", 0, 0, dbx.Params{"arcade": arcadeID})
	if err != nil {
		return false
	}
	for _, user := range users {
		if slices.Contains(user.GetStringSlice("owns"), arcadeID) && slices.Contains(user.GetStringSlice("tags"), "arcade_owner") {
			return true
		}
	}
	return false
}
