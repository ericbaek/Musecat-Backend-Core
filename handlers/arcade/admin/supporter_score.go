package admin

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

func GetSupporterScore(re *core.RequestEvent) error {
	if re.Auth == nil || strings.TrimSpace(re.Auth.Id) == "" {
		return re.JSON(http.StatusUnauthorized, map[string]any{
			"error": "authentication required",
		})
	}

	score, err := arcadeinternal.BuildSupporterScore(re.App, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to build supporter exp",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, score)
}
