package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"net/http"
	"strings"
	"unicode/utf8"
)

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

	return createMessagingFeedback(re, body)
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
	items, err := messaging.LegacyList(re.App, re.Auth.Id, createdByID, re.Request.URL.Query().Get("arcade"), re.Request.URL.Query().Get("status"))
	if err != nil {
		return err
	}
	return re.JSON(200, map[string]any{"items": items, "total": len(items)})
}

func ReviewSupportFeedback(re *core.RequestEvent) error {
	var body struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := re.BindBody(&body); err != nil {
		return re.BadRequestError("Invalid inquiry status.", nil)
	}
	cid := mappedFeedback(re, body.ID)
	summary, err := messaging.SetStatus(re.App, re.Auth.Id, cid, body.Status)
	if err != nil {
		return err
	}
	item, err := messaging.LegacyItem(re.App, summary)
	if err != nil {
		return err
	}
	return re.JSON(200, item)
}
