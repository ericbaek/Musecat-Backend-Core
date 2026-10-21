package reporting

import (
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
	"slices"
)

func UpdateInquirySettings(re *core.RequestEvent) error {
	var body struct {
		Arcade string `json:"arcade"`
		supportfeedback.InquirySettingsData
	}
	if err := re.BindBody(&body); err != nil {
		return re.BadRequestError("invalid inquiry settings", err)
	}
	if err := body.InquirySettingsData.Validate(); err != nil {
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
		arcade.Set("inquiry_settings", body.InquirySettingsData.Stored())
		return tx.Save(arcade)
	})
	if err != nil {
		return err
	}
	body.Configured = true
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(http.StatusOK, body.InquirySettingsData)
}

// InquiryOnboarding returns only this current official owner's arcades.
func InquiryOnboarding(re *core.RequestEvent) error {
	user, err := re.App.FindRecordById("user", re.Auth.Id)
	if err != nil || user.GetBool("withdrawn") {
		return re.ForbiddenError("account is inactive", nil)
	}
	type item struct {
		ArcadeID       string                              `json:"arcadeId"`
		ArcadeName     string                              `json:"arcadeName"`
		Settings       supportfeedback.InquirySettingsData `json:"settings"`
		ReportSettings arcadereport.Settings               `json:"reportSettings"`
	}
	items := []item{}
	if slices.Contains(user.GetStringSlice("tags"), "arcade_owner") {
		for _, id := range user.GetStringSlice("owns") {
			arcade, err := re.App.FindRecordById("arcade", id)
			if err != nil {
				continue
			}
			settings := supportfeedback.InquirySettings(arcade)
			name := ""
			if basic, err := re.App.FindRecordById("arcade_basic", arcade.GetString("basic")); err == nil {
				name = basic.GetString("name")
			}
			items = append(items, item{arcade.Id, name, settings, arcadereport.Read(arcade)})
		}
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(http.StatusOK, map[string]any{"items": items})
}
