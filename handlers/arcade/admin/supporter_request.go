package admin

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

func CreateSupporterRequest(re *core.RequestEvent) error {
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

	if !score.Qualified {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "exp threshold not met",
			"details": fmt.Sprintf("need at least %d exp, got %d exp", score.Threshold, score.TotalExp),
			"exp":     score,
		})
	}

	if score.LatestRequest != nil {
		switch score.LatestRequest.Status {
		case "pending", "approved":
			return re.JSON(http.StatusConflict, map[string]any{
				"error":          "supporter request already exists",
				"latest_request": score.LatestRequest,
				"exp":            score,
			})
		}
	}

	coll, err := re.App.FindCollectionByNameOrId(arcadeinternal.CollectionSupporterRequest)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create supporter request",
			"details": fmt.Sprintf("failed to find supporter_request collection: %v", err),
		})
	}

	rec := core.NewRecord(coll)
	rec.Set("user", re.Auth.Id)
	rec.Set("createdBy", re.Auth.Id)
	rec.Set("status", "pending")
	rec.Set("qualified", score.Qualified)
	rec.Set("score_total", score.TotalExp)
	rec.Set("score_breakdown", score)

	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create supporter request",
			"details": fmt.Sprintf("failed to save supporter_request record: %v", err),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"id":        rec.Id,
		"status":    rec.GetString("status"),
		"qualified": rec.GetBool("qualified"),
		"exp":       score,
	})
}
