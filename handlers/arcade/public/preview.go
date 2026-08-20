package public

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

// PreviewPublicArcade returns the XP that the creator would receive if the
// current private arcade were converted to public. It never writes the XP
// ledger; the PUT /arcade/public transaction remains authoritative.
func PreviewPublicArcade(re *core.RequestEvent) error {
	arcadeID := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	if arcadeID == "" {
		arcadeID = strings.TrimSpace(re.Request.URL.Query().Get("id"))
	}
	if arcadeID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}

	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}
	if arcade.GetString("createdBy") != re.Auth.Id {
		return re.JSON(http.StatusForbidden, map[string]any{
			"error": "only the creator can preview public conversion",
		})
	}
	if arcade.GetBool("closed") {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "cannot preview public conversion for closed arcade",
		})
	}
	if arcade.GetBool("public") {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "arcade is already public",
		})
	}

	preview, err := userhandler.PreviewArcadePublicExp(re.App, re.Auth.Id, arcadeID)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to preview public conversion XP",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade":     arcadeID,
		"public":     false,
		"xp_preview": preview,
	})
}
