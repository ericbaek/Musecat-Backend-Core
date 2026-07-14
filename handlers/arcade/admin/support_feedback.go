package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const supportFeedbackWaitingStatus = "waiting"
const maxSupportFeedbackPhotosPerRequest = 3

type CreateSupportFeedbackBody struct {
	Message string `json:"message"`
	Photos  []*filesystem.File
}

func parseCreateSupportFeedbackBody(re *core.RequestEvent) (CreateSupportFeedbackBody, error) {
	contentType := strings.ToLower(strings.TrimSpace(re.Request.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := re.Request.ParseMultipartForm(32 << 20); err != nil {
			return CreateSupportFeedbackBody{}, err
		}

		body := CreateSupportFeedbackBody{
			Message: re.Request.FormValue("message"),
		}

		files, err := re.FindUploadedFiles("photos")
		if err != nil {
			if errors.Is(err, http.ErrMissingFile) {
				return body, nil
			}
			return CreateSupportFeedbackBody{}, err
		}

		body.Photos = files
		return body, nil
	}

	var body CreateSupportFeedbackBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return body, err
	}
	return body, nil
}

func validateCreateSupportFeedbackBody(body *CreateSupportFeedbackBody) error {
	body.Message = strings.TrimSpace(body.Message)
	if body.Message == "" {
		return fmt.Errorf("message is required")
	}
	if len(body.Photos) > maxSupportFeedbackPhotosPerRequest {
		return fmt.Errorf("photos must have at most %d items", maxSupportFeedbackPhotosPerRequest)
	}
	return nil
}

func CreateSupportFeedback(re *core.RequestEvent) error {
	body, err := parseCreateSupportFeedbackBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateCreateSupportFeedbackBody(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	coll, err := re.App.FindCollectionByNameOrId(arcadeinternal.CollectionSupportFeedback)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create support_feedback",
			"details": fmt.Sprintf("failed to find support_feedback collection: %v", err),
		})
	}

	rec := core.NewRecord(coll)
	rec.Set("message", body.Message)
	rec.Set("status", supportFeedbackWaitingStatus)
	if len(body.Photos) > 0 {
		rec.Set("photos", body.Photos)
	}

	if re.Auth != nil {
		authID := strings.TrimSpace(re.Auth.Id)
		if authID != "" {
			rec.Set("createdBy", authID)
		}
	}

	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create support_feedback",
			"details": fmt.Sprintf("failed to save support_feedback record: %v", err),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"id":        rec.Id,
		"message":   rec.GetString("message"),
		"status":    rec.GetString("status"),
		"createdBy": rec.GetString("createdBy"),
		"photos":    append([]string{}, rec.GetStringSlice("photos")...),
	})
}

func ListSupportFeedback(re *core.RequestEvent) error {
	createdByID := strings.TrimSpace(re.Request.URL.Query().Get("createdBy"))
	status := strings.TrimSpace(re.Request.URL.Query().Get("status"))

	filters := make([]string, 0, 2)
	params := dbx.Params{}
	if createdByID != "" {
		filters = append(filters, "createdBy = {:createdBy}")
		params["createdBy"] = createdByID
	}
	if status != "" {
		filters = append(filters, "status = {:status}")
		params["status"] = status
	}
	filter := strings.Join(filters, " && ")

	recs, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionSupportFeedback,
		filter,
		"-created",
		0,
		0,
		params,
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to list support_feedback",
			"details": err.Error(),
		})
	}

	items := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		items = append(items, map[string]any{
			"id":        rec.Id,
			"message":   rec.GetString("message"),
			"status":    rec.GetString("status"),
			"createdBy": rec.GetString("createdBy"),
			"photos":    append([]string{}, rec.GetStringSlice("photos")...),
			"created":   rec.GetString("created"),
			"updated":   rec.GetString("updated"),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}
