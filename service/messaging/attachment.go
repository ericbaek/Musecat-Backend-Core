package messaging

import (
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"slices"
	"strings"
)

func AttachmentPath(app core.App, user, cid, mid, file string) (string, string, error) {
	deny := func() (string, string, error) { return "", "", apis.NewNotFoundError("Photo not found.", nil) }
	if _, _, err := Access(app, user, cid); err != nil {
		return "", "", err
	}
	m, err := app.FindRecordById(Messages, mid)
	if err != nil || m.GetString("conversation") != cid || file == "" || strings.ContainsAny(file, "/\\") {
		return deny()
	}
	if slices.Contains(m.GetStringSlice("photos"), file) {
		return m.BaseFilesPath() + "/" + file, file, nil
	}
	if !slices.Contains(m.GetStringSlice("legacy_photos"), file) {
		return deny()
	}
	col := m.GetString("legacy_collection")
	if col != "support_feedback" && col != "support_feedback_message" {
		return deny()
	}
	source, err := app.FindRecordById(col, m.GetString("legacy_record"))
	if err != nil || !slices.Contains(source.GetStringSlice("photos"), file) {
		return deny()
	}
	q, err := inquiry(app, cid)
	if err != nil {
		return deny()
	}
	legacy := source.Id
	if col == "support_feedback_message" {
		legacy = source.GetString("feedback")
	}
	if q.GetString("legacy_feedback_id") != legacy {
		return deny()
	}
	return source.BaseFilesPath() + "/" + file, file, nil
}
