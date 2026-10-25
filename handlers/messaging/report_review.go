package messaging

import (
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/pocketbase/core"
	"strconv"
)

func ReviewReports(re *core.RequestEvent) error {
	limit := 20
	var err error
	if v := re.Request.URL.Query().Get("limit"); v != "" {
		limit, err = strconv.Atoi(v)
		if err != nil {
			return re.BadRequestError("Invalid limit.", nil)
		}
	}
	out, err := messaging.ReviewReports(re.App, re.Auth.Id, re.Request.URL.Query().Get("cursor"), limit)
	return reply(re, 200, out, err)
}
func ReviewReport(re *core.RequestEvent) error {
	out, err := messaging.ReviewReport(re.App, re.Auth.Id, re.Request.PathValue("id"))
	return reply(re, 200, out, err)
}
func ReviewPhoto(re *core.RequestEvent) error {
	storage, file, err := messaging.ReportAttachmentPath(re.App, re.Auth.Id, re.Request.PathValue("id"), re.Request.PathValue("file"))
	if err != nil {
		return err
	}
	fs, err := re.App.NewFilesystem()
	if err != nil {
		return err
	}
	defer fs.Close()
	re.Response.Header().Set("Cache-Control", "private, no-store")
	re.Response.Header().Set("X-Content-Type-Options", "nosniff")
	return fs.Serve(re.Response, re.Request, storage, file)
}
