package messaging

import (
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"slices"
	"strings"
)

func AttachmentPath(app core.App, user, cid, mid, file string) (string, string, error) {
	return AttachmentPathFor(app, Principal{UserID: user}, cid, mid, file)
}
func AttachmentPathFor(app core.App, viewer Principal, cid, mid, file string) (string, string, error) {
	deny := func() (string, string, error) { return "", "", apis.NewNotFoundError("Photo not found.", nil) }
	if _, _, err := access(app, viewer, cid); err != nil {
		return "", "", err
	}
	m, err := app.FindRecordById(Messages, mid)
	if err != nil || m.GetString("conversation") != cid || file == "" || strings.ContainsAny(file, "/\\") {
		return deny()
	}
	if slices.Contains(m.GetStringSlice("photos"), file) {
		return m.BaseFilesPath() + "/" + file, file, nil
	}
	return deny()
}
