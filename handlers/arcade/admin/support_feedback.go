package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/router"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const supportFeedbackWaitingStatus = "waiting"
const maxSupportFeedbackPhotosPerRequest = 3
const maxSupportFeedbackPhotoBytes = 15_000_000
const maxSupportFeedbackMessageLength = 13_000

// MaxSupportFeedbackBodyBytes allows three maximum-size photos plus form overhead.
const MaxSupportFeedbackBodyBytes = maxSupportFeedbackPhotosPerRequest*maxSupportFeedbackPhotoBytes + (1 << 20)

type CreateSupportFeedbackBody struct {
	Arcade  string             `json:"arcade"`
	Message string             `json:"message"`
	Photos  []*filesystem.File `json:"-"`
}

func parseCreateSupportFeedbackBody(re *core.RequestEvent) (CreateSupportFeedbackBody, error) {
	contentType := strings.ToLower(strings.TrimSpace(re.Request.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := re.Request.ParseMultipartForm(32 << 20); err != nil {
			return CreateSupportFeedbackBody{}, err
		}

		body := CreateSupportFeedbackBody{
			Message: re.Request.FormValue("message"),
			Arcade:  re.Request.FormValue("arcade"),
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
	if utf8.RuneCountInString(body.Message) > maxSupportFeedbackMessageLength {
		return fmt.Errorf("message must have at most %d characters", maxSupportFeedbackMessageLength)
	}
	return arcadeinternal.ValidateImageUploads(body.Photos, maxSupportFeedbackPhotosPerRequest, maxSupportFeedbackPhotoBytes)
}

func CreateSupportFeedback(re *core.RequestEvent) error {
	body, err := parseCreateSupportFeedbackBody(re)
	if re.Request.MultipartForm != nil {
		defer re.Request.MultipartForm.RemoveAll()
	}
	if err != nil {
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return apis.ErrRequestEntityTooLarge
		}
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateCreateSupportFeedbackBody(&body); err != nil {
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return apis.ErrRequestEntityTooLarge
		}
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	body.Arcade = strings.TrimSpace(body.Arcade)
	if body.Arcade != "" {
		if re.Auth != nil && re.Auth.GetBool("withdrawn") {
			return re.UnauthorizedError("account is inactive", nil)
		}
		arcade, err := re.App.FindRecordById("arcade", body.Arcade)
		if err != nil || (!arcade.GetBool("public") && (re.Auth == nil || (arcade.GetString("createdBy") != re.Auth.Id && !arcadereport.CanManage(re.Auth, arcade)))) {
			return re.NotFoundError("arcade not found", nil)
		}
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
	if body.Arcade != "" {
		rec.Set("arcade", body.Arcade)
	}
	if len(body.Photos) > 0 {
		rec.Set("photos", body.Photos)
	}

	if re.Auth != nil {
		authID := strings.TrimSpace(re.Auth.Id)
		if authID != "" {
			rec.Set("createdBy", authID)
		}
	}

	if err := re.App.RunInTransaction(func(tx core.App) error {
		if body.Arcade != "" {
			var user *core.Record
			if re.Auth != nil {
				var err error
				user, err = tx.FindRecordById("user", re.Auth.Id)
				if err != nil || user.GetBool("withdrawn") {
					return re.UnauthorizedError("account is inactive", nil)
				}
			}
			arcade, err := tx.FindRecordById("arcade", body.Arcade)
			if err != nil {
				return re.NotFoundError("arcade not found", nil)
			}
			if !arcade.GetBool("public") && (user == nil || (arcade.GetString("createdBy") != user.Id && !arcadereport.CanManage(user, arcade))) {
				return re.NotFoundError("arcade not found", nil)
			}
			settings := supportfeedback.InquirySettings(arcade)
			if supportfeedback.HasOfficialAccount(tx, arcade.Id) {
				if settings.Mode == "disabled" {
					return router.NewApiError(http.StatusConflict, "INQUIRY_DISABLED", nil)
				}
				if settings.Mode == "external" {
					return router.NewApiError(http.StatusConflict, "INQUIRY_EXTERNAL", nil)
				}
				if settings.LoginRequired && user == nil {
					return router.NewApiError(http.StatusUnauthorized, "LOGIN_REQUIRED", nil)
				}
			}
		}
		if err := tx.Save(rec); err != nil {
			return err
		}
		return usernotification.FeedbackCreatedTx(tx, rec)
	}); err != nil {
		var apiErr *router.ApiError
		if errors.As(err, &apiErr) {
			return err
		}
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
		"arcade":    rec.GetString("arcade"),
		"photos":    append([]string{}, rec.GetStringSlice("photos")...),
		"created":   rec.GetString("created"),
		"updated":   rec.GetString("updated"),
	})
}

func ListSupportFeedback(re *core.RequestEvent) error {
	createdByID := strings.TrimSpace(re.Request.URL.Query().Get("createdBy"))
	return listSupportFeedback(re, createdByID)
}

// ListMySupportFeedback ignores client-supplied owner filters.
func ListMySupportFeedback(re *core.RequestEvent) error {
	if supportfeedback.IsStaff(re.Auth) && re.Request.URL.Query().Get("arcade") != "" {
		return listSupportFeedback(re, "")
	}
	return listSupportFeedback(re, re.Auth.Id)
}

func listSupportFeedback(re *core.RequestEvent, createdByID string) error {
	status := strings.TrimSpace(re.Request.URL.Query().Get("status"))

	filters := make([]string, 0, 3)
	params := dbx.Params{}
	if createdByID != "" {
		ownerFilter := "createdBy = {:createdBy}"
		if re.Auth != nil && createdByID == re.Auth.Id {
			for i, id := range re.Auth.GetStringSlice("owns") {
				key := fmt.Sprintf("owned%d", i)
				ownerFilter += " || arcade={:" + key + "}"
				params[key] = id
			}
		}
		filters = append(filters, "("+ownerFilter+")")
		params["createdBy"] = createdByID
	}
	if status != "" {
		filters = append(filters, "status = {:status}")
		params["status"] = status
	}
	if arcade := strings.TrimSpace(re.Request.URL.Query().Get("arcade")); arcade != "" {
		filters = append(filters, "arcade={:arcade}")
		params["arcade"] = arcade
	}
	filter := strings.Join(filters, " && ")

	recs, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionSupportFeedback,
		filter,
		"-updated,-created,-id",
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
		if !supportfeedback.CanAccess(re.App, re.Auth, rec) {
			continue
		}
		items = append(items, map[string]any{
			"id":        rec.Id,
			"message":   rec.GetString("message"),
			"status":    rec.GetString("status"),
			"createdBy": rec.GetString("createdBy"),
			"arcade":    rec.GetString("arcade"),
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

type ReviewSupportFeedbackBody struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func ReviewSupportFeedback(re *core.RequestEvent) error {
	var body ReviewSupportFeedbackBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	body.ID = strings.TrimSpace(body.ID)
	body.Status = strings.ToLower(strings.TrimSpace(body.Status))
	if len(body.ID) != 15 {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": "id must be a valid support feedback id",
		})
	}
	if body.Status == "done" || body.Status == "completed" {
		body.Status = "solved"
	} else if body.Status == "processing" {
		body.Status = "recognised"
	}
	if body.Status != "waiting" && body.Status != "recognised" && body.Status != "solved" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": "status must be waiting, recognised, or solved",
		})
	}

	record, err := re.App.FindRecordById(arcadeinternal.CollectionSupportFeedback, body.ID)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{
			"error": "support_feedback not found",
		})
	}
	record.Set("status", body.Status)
	if err := re.App.Save(record); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to update support_feedback",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"id":        record.Id,
		"message":   record.GetString("message"),
		"status":    record.GetString("status"),
		"createdBy": record.GetString("createdBy"),
		"photos":    append([]string{}, record.GetStringSlice("photos")...),
		"created":   record.GetString("created"),
		"updated":   record.GetString("updated"),
	})
}
