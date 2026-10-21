package reporting

import (
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
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
