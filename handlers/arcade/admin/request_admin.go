package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const (
	defaultAdminRequestUrgency = "medium"
	adminRequestWaitingStatus  = "waiting"
)

var allowedAdminRequestUrgency = map[string]struct{}{
	"high":   {},
	"medium": {},
	"low":    {},
}

type CreateArcadeRequestAdminBody struct {
	Arcade  string `json:"arcade"`
	Urgency string `json:"urgency"`
	Message string `json:"message"`
}

func parseCreateArcadeRequestAdminBody(re *core.RequestEvent) (CreateArcadeRequestAdminBody, error) {
	var body CreateArcadeRequestAdminBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return body, err
	}
	body.Arcade = strings.TrimSpace(body.Arcade)
	body.Urgency = strings.ToLower(strings.TrimSpace(body.Urgency))
	body.Message = strings.TrimSpace(body.Message)
	if body.Urgency == "" {
		body.Urgency = defaultAdminRequestUrgency
	}
	return body, nil
}

func validateCreateArcadeRequestAdminBody(body CreateArcadeRequestAdminBody) error {
	if body.Arcade != "" {
		if len(body.Arcade) != 15 {
			return fmt.Errorf("arcade must be a valid arcade id")
		}
	}
	if body.Message == "" {
		return fmt.Errorf("message is required")
	}
	if _, ok := allowedAdminRequestUrgency[body.Urgency]; !ok {
		return fmt.Errorf("urgency must be one of high, medium, low")
	}
	return nil
}

func CreateArcadeRequestAdmin(re *core.RequestEvent) error {
	body, err := parseCreateArcadeRequestAdminBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateCreateArcadeRequestAdminBody(body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	coll, err := re.App.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeRequestAdmin)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create arcade_request_admin",
			"details": fmt.Sprintf("failed to find arcade_request_admin collection: %v", err),
		})
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", body.Arcade)
	rec.Set("urgency", body.Urgency)
	rec.Set("message", body.Message)
	rec.Set("kind", "general")
	rec.Set("status", adminRequestWaitingStatus)
	rec.Set("createdBy", re.Auth.Id)

	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create arcade_request_admin",
			"details": fmt.Sprintf("failed to save arcade_request_admin record: %v", err),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"id":        rec.Id,
		"kind":      rec.GetString("kind"),
		"arcade":    strings.TrimSpace(rec.GetString("arcade")),
		"urgency":   rec.GetString("urgency"),
		"message":   rec.GetString("message"),
		"status":    rec.GetString("status"),
		"createdBy": rec.GetString("createdBy"),
	})
}

func ListArcadeRequestAdmin(re *core.RequestEvent) error {
	arcadeID := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	filter := "createdBy = {:createdBy}"
	params := dbx.Params{"createdBy": re.Auth.Id}
	if arcadeID != "" {
		filter += " && arcade = {:arcade}"
		params["arcade"] = arcadeID
	}

	recs, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeRequestAdmin,
		filter,
		"-created",
		0,
		0,
		params,
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to list arcade_request_admin",
			"details": err.Error(),
		})
	}

	items := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		items = append(items, map[string]any{
			"id":        rec.Id,
			"kind":      rec.GetString("kind"),
			"arcade":    strings.TrimSpace(rec.GetString("arcade")),
			"urgency":   rec.GetString("urgency"),
			"message":   rec.GetString("message"),
			"status":    rec.GetString("status"),
			"createdBy": rec.GetString("createdBy"),
			"created":   rec.GetString("created"),
			"updated":   rec.GetString("updated"),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}
