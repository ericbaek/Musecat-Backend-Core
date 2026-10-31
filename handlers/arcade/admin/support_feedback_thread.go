package admin

import (
	"errors"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/security"
	"net/http"
	"strings"
)

func GetSupportFeedbackThread(re *core.RequestEvent) error {
	out, err := messaging.LegacyThreadFor(re.App, feedbackPrincipal(re), mappedFeedback(re, re.Request.URL.Query().Get("id")), re.Request.URL.Query().Get("before"))
	if err != nil {
		return err
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(200, out)
}

type replySupportFeedbackBody struct {
	ID      string             `json:"feedbackId"`
	Message string             `json:"message"`
	Photos  []*filesystem.File `json:"-"`
}

func parseReplySupportFeedbackBody(re *core.RequestEvent) (replySupportFeedbackBody, error) {
	var body replySupportFeedbackBody
	if !strings.HasPrefix(strings.ToLower(re.Request.Header.Get("Content-Type")), "multipart/form-data") {
		err := re.BindBody(&body)
		return body, err
	}
	if err := re.Request.ParseMultipartForm(1 << 20); err != nil {
		return body, err
	}
	body.ID = re.Request.FormValue("feedbackId")
	body.Message = re.Request.FormValue("message")
	files, err := re.FindUploadedFiles("photos")
	if errors.Is(err, http.ErrMissingFile) {
		return body, nil
	}
	body.Photos = files
	return body, err
}

func ReplySupportFeedback(re *core.RequestEvent) error {
	body, err := parseReplySupportFeedbackBody(re)
	if re.Request.MultipartForm != nil {
		defer re.Request.MultipartForm.RemoveAll()
	}
	if err != nil {
		return re.BadRequestError("Invalid reply.", err)
	}
	cid := mappedFeedback(re, body.ID)
	var acting *messaging.Identity
	if re.Auth != nil {
		acting = messaging.LegacySender(re.App, re.Auth.Id, cid)
	}
	m, _, err := messaging.SendFor(re.App, feedbackPrincipal(re), cid, messaging.SendInput{Text: body.Message, ClientMessageID: "legacy:" + security.RandomString(24), Photos: body.Photos, ActingAs: acting})
	if err != nil {
		return err
	}
	return re.JSON(201, messaging.LegacyMessage(m))
}
func UpdateSupportFeedbackThreadStatus(re *core.RequestEvent) error {
	var body struct {
		ID     string `json:"feedbackId"`
		Status string `json:"status"`
	}
	if err := re.BindBody(&body); err != nil || (body.Status != "waiting" && body.Status != "recognised" && body.Status != "solved") {
		return re.BadRequestError("Status must be waiting, recognised or solved.", nil)
	}
	s, err := messaging.SetStatusFor(re.App, feedbackPrincipal(re), mappedFeedback(re, body.ID), body.Status)
	if err != nil {
		return err
	}
	return re.JSON(200, map[string]any{"id": body.ID, "status": s.Status})
}
func GetSupportFeedbackReplyPhoto(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	if q.Has("id") == q.Has("feedbackId") || (q.Get("id") == "" && q.Get("feedbackId") == "") {
		return re.BadRequestError("Exactly one reply id or feedbackId is required.", nil)
	}
	handled, err := messagingFeedbackPhoto(re)
	if handled {
		return err
	}
	return re.NotFoundError("Photo not found.", nil)
}
