package messaging

import (
	"encoding/json"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"net/url"
	"slices"
	"strings"
)

type MessageReport struct {
	ID         string  `json:"id"`
	ReporterID string  `json:"reporterId"`
	MessageID  string  `json:"messageId"`
	Reason     string  `json:"reason"`
	CreatedAt  string  `json:"createdAt"`
	Message    Message `json:"message"`
}
type ReportPage struct {
	Items      []MessageReport `json:"items"`
	NextCursor *string         `json:"nextCursor"`
}
type reportEvidence struct {
	Message          Message  `json:"message"`
	LegacyCollection string   `json:"legacyCollection"`
	LegacyRecord     string   `json:"legacyRecord"`
	LegacyPhotos     []string `json:"legacyPhotos"`
}

func RequireReviewer(app core.App, user string) error {
	u, err := ActiveUser(app, user)
	if err != nil {
		return err
	}
	if !supportfeedback.IsStaff(u) {
		return apis.NewForbiddenError("Staff access required.", nil)
	}
	return nil
}
func reviewDTO(r *core.Record) (MessageReport, error) {
	var evidence reportEvidence
	err := json.Unmarshal([]byte(r.GetString("evidence")), &evidence)
	if err != nil {
		return MessageReport{}, err
	}
	evidence.Message.Mentions = append([]string{}, evidence.Message.Mentions...)
	for i := range evidence.Message.Photos {
		evidence.Message.Photos[i].URL = "/moderation/messages/reports/" + r.Id + "/attachments/" + url.PathEscape(evidence.Message.Photos[i].Name)
	}
	return MessageReport{ID: r.Id, ReporterID: r.GetString("reporter"), MessageID: r.GetString("message"), Reason: r.GetString("reason"), CreatedAt: r.GetString("created"), Message: evidence.Message}, nil
}
func ReviewReport(app core.App, user, id string) (MessageReport, error) {
	if err := RequireReviewer(app, user); err != nil {
		return MessageReport{}, err
	}
	r, err := app.FindRecordById(Reports, id)
	if err != nil {
		return MessageReport{}, apis.NewNotFoundError("Report not found.", nil)
	}
	return reviewDTO(r)
}
func ReviewReports(app core.App, user, cursor string, limit int) (ReportPage, error) {
	out := ReportPage{Items: []MessageReport{}}
	if err := RequireReviewer(app, user); err != nil {
		return out, err
	}
	if limit < 1 || limit > 50 {
		return out, apis.NewBadRequestError("Invalid limit.", nil)
	}
	filter := "id!=''"
	params := dbx.Params{}
	if cursor != "" {
		r, err := app.FindRecordById(Reports, cursor)
		if err != nil {
			return out, apis.NewBadRequestError("Invalid cursor.", nil)
		}
		filter += " && (created < {:at} || (created = {:at} && id < {:id}))"
		params["at"] = r.GetString("created")
		params["id"] = r.Id
	}
	rows, err := app.FindRecordsByFilter(Reports, filter, "-created,-id", limit+1, 0, params)
	if err != nil {
		return out, err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		out.NextCursor = ptr(rows[len(rows)-1].Id)
	}
	for _, r := range rows {
		item, err := reviewDTO(r)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
func ReportAttachmentPath(app core.App, user, id, file string) (string, string, error) {
	if err := RequireReviewer(app, user); err != nil {
		return "", "", err
	}
	deny := func() (string, string, error) { return "", "", apis.NewNotFoundError("Report photo not found.", nil) }
	if file == "" || strings.ContainsAny(file, "/\\") {
		return deny()
	}
	r, err := app.FindRecordById(Reports, id)
	if err != nil {
		return deny()
	}
	var evidence reportEvidence
	if json.Unmarshal([]byte(r.GetString("evidence")), &evidence) != nil {
		return deny()
	}
	found := false
	for _, p := range evidence.Message.Photos {
		if p.Name == file {
			found = true
		}
	}
	if !found {
		return deny()
	}
	m, err := app.FindRecordById(Messages, r.GetString("message"))
	if err != nil || m.Id != evidence.Message.ID || m.GetString("conversation") != evidence.Message.ConversationID {
		return deny()
	}
	if slices.Contains(m.GetStringSlice("photos"), file) {
		return m.BaseFilesPath() + "/" + file, file, nil
	}
	if !slices.Contains(evidence.LegacyPhotos, file) || (evidence.LegacyCollection != "support_feedback" && evidence.LegacyCollection != "support_feedback_message") {
		return deny()
	}
	if m.GetString("legacy_collection") != evidence.LegacyCollection || m.GetString("legacy_record") != evidence.LegacyRecord {
		return deny()
	}
	source, err := app.FindRecordById(evidence.LegacyCollection, evidence.LegacyRecord)
	if err != nil || !slices.Contains(source.GetStringSlice("photos"), file) {
		return deny()
	}
	return source.BaseFilesPath() + "/" + file, file, nil
}
