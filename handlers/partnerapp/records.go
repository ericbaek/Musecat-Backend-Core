package partnerapp

import (
	"net/http"
	"strings"

	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/ericbaek/musecat-backend-core/partnerapp"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// RegisterRecordsInvalidationRoutes accepts only Musecat-issued server credentials,
// including B-only apps. Partner-issued OAuth client secrets are never accepted.
func RegisterRecordsInvalidationRoutes(se *core.ServeEvent, service *partnerapp.Service, manager *connections.Manager) {
	se.Router.POST("/partner/v1/records/invalidate", func(re *core.RequestEvent) error {
		re.Response.Header().Set("Cache-Control", "no-store")
		if len(re.Request.Header.Values("Authorization")) != 1 {
			return re.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		}
		parts := strings.Fields(re.Request.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return re.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		}
		provider, err := service.AuthenticateRecordsKey(re.App, parts[1])
		if err != nil {
			return re.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		}
		var input struct {
			Sub     string `json:"sub"`
			Service string `json:"service"`
		}
		if err := decode(re, &input); err != nil {
			return err
		}
		if err := manager.InvalidateRecords(re.App, provider, input.Sub, input.Service); err != nil {
			if err == connections.ErrInvalid {
				return re.JSON(http.StatusBadRequest, map[string]string{"error": "invalid_request"})
			}
			return re.InternalServerError("Record invalidation failed.", nil)
		}
		return re.NoContent(http.StatusNoContent)
	}).Bind(apis.BodyLimit(4096))
}
