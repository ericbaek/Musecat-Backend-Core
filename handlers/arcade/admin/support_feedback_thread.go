package admin

import (
	"errors"
	"net/http"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/security"
	"strings"
	"unicode/utf8"
)

func feedbackThreadAccess(re *core.RequestEvent, id string) (*core.Record, error) {
	record, err := re.App.FindRecordById("support_feedback", id)
	if err != nil || !canAccessFeedback(re, re.App, re.Auth, record) {
		return nil, re.NotFoundError("feedback not found", nil)
	}
	return record, nil
}
func feedbackMessageItem(record *core.Record) map[string]any {
	return map[string]any{"id": record.Id, "message": record.GetString("message"), "author": record.GetString("author"), "staff": record.GetBool("staff"), "official": record.GetBool("official"), "created": record.GetString("created"), "photos": append([]string{}, record.GetStringSlice("photos")...)}
}
func GetSupportFeedbackThread(re *core.RequestEvent) error {
	if cid := mappedFeedback(re, re.Request.URL.Query().Get("id")); cid != "" {
		out, err := messaging.LegacyThread(re.App, re.Auth.Id, cid, re.Request.URL.Query().Get("before"))
		if err != nil {
			return err
		}
		re.Response.Header().Set("Cache-Control", "private, no-store")
		return re.JSON(200, out)
	}
	record, err := feedbackThreadAccess(re, re.Request.URL.Query().Get("id"))
	if err != nil {
		return err
	}
	filter := "feedback={:id}"
	params := dbx.Params{"id": record.Id}
	if before := re.Request.URL.Query().Get("before"); before != "" {
		cursor, err := re.App.FindRecordById(supportfeedback.Messages, before)
		if err != nil || cursor.GetString("feedback") != record.Id {
			return re.BadRequestError("invalid cursor", nil)
		}
		filter += " && (created < {:created} || (created = {:created} && id < {:cursor}))"
		params["created"] = cursor.GetString("created")
		params["cursor"] = cursor.Id
	}
	rows, err := re.App.FindRecordsByFilter(supportfeedback.Messages, filter, "-created,-id", 51, 0, params)
	if err != nil {
		return err
	}
	next := ""
	if len(rows) > 50 {
		rows = rows[:50]
		next = rows[len(rows)-1].Id
	}
	items := []map[string]any{}
	for i := len(rows) - 1; i >= 0; i-- {
		items = append(items, feedbackMessageItem(rows[i]))
	}
	arcadeName := ""
	arcadeID := record.GetString("arcade")
	if arcadeID != "" {
		if arcade, err := re.App.FindRecordById("arcade", arcadeID); err == nil {
			if basic, err := re.App.FindRecordById("arcade_basic", arcade.GetString("basic")); err == nil {
				arcadeName = basic.GetString("name")
			}
		}
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(200, map[string]any{"conversationId": nil, "feedback": map[string]any{"id": record.Id, "message": record.GetString("message"), "status": record.GetString("status"), "arcadeId": arcadeID, "arcadeName": arcadeName, "arcadeHasOfficial": supportfeedback.HasOfficialAccount(re.App, arcadeID), "createdBy": record.GetString("createdBy"), "guestChat": record.GetString("guest_key_hash") != "", "photos": record.GetStringSlice("photos"), "created": record.GetString("created")}, "items": items, "next": next})
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
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return apis.ErrRequestEntityTooLarge
		}
		return re.BadRequestError("invalid reply", nil)
	}
	if cid := mappedFeedback(re, body.ID); cid != "" {
		m, _, err := messaging.Send(re.App, re.Auth.Id, cid, messaging.SendInput{Text: body.Message, ClientMessageID: "legacy:" + security.RandomString(24), Photos: body.Photos, ActingAs: messaging.LegacySender(re.App, re.Auth.Id, cid)})
		if err != nil {
			return err
		}
		return re.JSON(201, messaging.LegacyMessage(m))
	}
	body.Message = strings.TrimSpace(body.Message)
	if (body.Message == "" && len(body.Photos) == 0) || utf8.RuneCountInString(body.Message) > 4000 {
		return re.BadRequestError("reply must contain a message of up to 4000 characters or photos", nil)
	}
	if _, err := feedbackThreadAccess(re, body.ID); err != nil {
		return err
	}
	if err := arcadeinternal.ValidateImageUploads(body.Photos, maxSupportFeedbackPhotosPerRequest, maxSupportFeedbackPhotoBytes); err != nil {
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return apis.ErrRequestEntityTooLarge
		}
		return re.BadRequestError("invalid photos", err)
	}
	var result map[string]any
	err = re.App.RunInTransaction(func(tx core.App) error {
		feedback, err := tx.FindRecordById("support_feedback", body.ID)
		if err != nil {
			return re.NotFoundError("feedback not found", nil)
		}
		// Recheck policy under the same transaction as the message and its alerts.
		var user *core.Record
		if re.Auth != nil {
			user, err = tx.FindRecordById("user", re.Auth.Id)
			if err != nil {
				return err
			}
		}
		if !canAccessFeedback(re, tx, user, feedback) {
			return re.NotFoundError("feedback not found", nil)
		}
		col, err := tx.FindCollectionByNameOrId(supportfeedback.Messages)
		if err != nil {
			return err
		}
		message := core.NewRecord(col)
		message.Set("feedback", body.ID)
		if user != nil {
			message.Set("author", user.Id)
		}
		message.Set("staff", supportfeedback.IsStaff(user))
		message.Set("official", !supportfeedback.IsStaff(user) && supportfeedback.IsOfficial(tx, user, feedback))
		message.Set("message", body.Message)
		if len(body.Photos) > 0 {
			message.Set("photos", body.Photos)
		}
		if err := tx.Save(message); err != nil {
			return err
		}
		// A new user message returns the inquiry to the waiting queue.
		status := "waiting"
		if (message.GetBool("staff") || message.GetBool("official")) && message.GetString("author") != feedback.GetString("createdBy") {
			status = "recognised"
		}
		feedback.Set("status", status)
		if err := tx.Save(feedback); err != nil {
			return err
		}
		if err := usernotification.FeedbackReplyTx(tx, feedback, message); err != nil {
			return err
		}
		result = feedbackMessageItem(message)
		return nil
	})
	if err != nil {
		return err
	}
	return re.JSON(201, result)
}

// UpdateSupportFeedbackThreadStatus lets current conversation participants close
// an inquiry or return it to the waiting queue without posting a reply.
func UpdateSupportFeedbackThreadStatus(re *core.RequestEvent) error {
	var body struct {
		ID     string `json:"feedbackId"`
		Status string `json:"status"`
	}
	if err := re.BindBody(&body); err != nil || (body.Status != "solved" && body.Status != "waiting") {
		return re.BadRequestError("status must be solved or waiting", nil)
	}
	if cid := mappedFeedback(re, body.ID); cid != "" {
		s, err := messaging.SetStatus(re.App, re.Auth.Id, cid, body.Status)
		if err != nil {
			return err
		}
		return re.JSON(200, map[string]any{"id": body.ID, "status": *s.Status})
	}
	if _, err := feedbackThreadAccess(re, body.ID); err != nil {
		return err
	}
	err := re.App.RunInTransaction(func(tx core.App) error {
		feedback, err := tx.FindRecordById("support_feedback", body.ID)
		if err != nil {
			return re.NotFoundError("feedback not found", nil)
		}
		var user *core.Record
		if re.Auth != nil {
			user, err = tx.FindRecordById("user", re.Auth.Id)
			if err != nil {
				return err
			}
		}
		if !canAccessFeedback(re, tx, user, feedback) {
			return re.NotFoundError("feedback not found", nil)
		}
		feedback.Set("status", body.Status)
		return tx.Save(feedback)
	})
	if err != nil {
		return err
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(200, map[string]any{"id": body.ID, "status": body.Status})
}

// GetSupportFeedbackReplyPhoto serves original or reply attachments only to
// current inquiry participants. Protected fields deny raw PocketBase file URLs.
func GetSupportFeedbackReplyPhoto(re *core.RequestEvent) error {
	query := re.Request.URL.Query()
	if query.Has("id") == query.Has("feedbackId") || (query.Get("id") == "" && query.Get("feedbackId") == "") {
		return re.BadRequestError("exactly one reply id or feedbackId is required", nil)
	}
	if handled, err := messagingFeedbackPhoto(re); handled {
		return err
	}
	var record *core.Record
	var err error
	if feedbackID := query.Get("feedbackId"); feedbackID != "" {
		record, err = feedbackThreadAccess(re, feedbackID)
		if err != nil {
			return err
		}
	} else {
		record, err = re.App.FindRecordById(supportfeedback.Messages, query.Get("id"))
		if err != nil {
			return re.NotFoundError("photo not found", nil)
		}
		if _, err := feedbackThreadAccess(re, record.GetString("feedback")); err != nil {
			return err
		}
	}
	filename := re.Request.URL.Query().Get("file")
	found := false
	for _, photo := range record.GetStringSlice("photos") {
		if photo == filename {
			found = true
			break
		}
	}
	if !found {
		return re.NotFoundError("photo not found", nil)
	}
	fs, err := re.App.NewFilesystem()
	if err != nil {
		return re.InternalServerError("failed to load photo", err)
	}
	defer fs.Close()
	re.Response.Header().Set("Cache-Control", "private, no-store")
	re.Response.Header().Set("X-Content-Type-Options", "nosniff")
	if err := fs.Serve(re.Response, re.Request, record.BaseFilesPath()+"/"+filename, filename); err != nil {
		return re.NotFoundError("photo not found", err)
	}
	return nil
}
