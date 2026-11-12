package notifications

import (
	"errors"
	"net/http"

	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/pocketbase/core"
)

// AimePublicStatus never consults authentication or caller-supplied user IDs.
func AimePublicStatus(re *core.RequestEvent) error {
	result, err := usernotification.GetAimeStatus(re.App, "")
	if err != nil {
		return err
	}
	re.Response.Header().Set("Cache-Control", "no-store")
	return re.JSON(http.StatusOK, result)
}

func AimeStatus(re *core.RequestEvent) error {
	result, err := usernotification.GetAimeStatus(re.App, re.Auth.Id)
	if err != nil {
		return err
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(http.StatusOK, result)
}

func AimeSubscribe(re *core.RequestEvent) error {
	var input struct {
		Enabled *bool `json:"enabled"`
	}
	if err := re.BindBody(&input); err != nil || input.Enabled == nil {
		return re.BadRequestError("enabled is required", nil)
	}
	err := usernotification.SetAimeSubscription(re.App, re.Auth.Id, *input.Enabled)
	if errors.Is(err, usernotification.ErrAimeNotBlocked) {
		return re.BadRequestError("Aime is not awaiting recovery", nil)
	}
	if err != nil {
		return err
	}
	return AimeStatus(re)
}
