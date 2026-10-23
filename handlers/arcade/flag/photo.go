package flag

import (
	"slices"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/pocketbase/pocketbase/core"
)

// DownloadArcadeFlagPhoto serves only an attachment belonging to the requested
// report. Private drafts use the same contributor scope as report editing.
func DownloadArcadeFlagPhoto(e *core.RequestEvent) error {
	flag, err := e.App.FindRecordById(arcadeinternal.CollectionArcadeFlag, e.Request.URL.Query().Get("id"))
	if err != nil {
		return e.NotFoundError("photo not found", nil)
	}
	arcade, err := e.App.FindRecordById(arcadeinternal.CollectionArcade, flag.GetString("arcade"))
	if err != nil || !arcadeinternal.CanWriteArcade(e.Auth, arcade) {
		return e.NotFoundError("photo not found", nil)
	}
	filename := e.Request.URL.Query().Get("file")
	if filename == "" || !slices.Contains(flag.GetStringSlice("photos"), filename) {
		return e.NotFoundError("photo not found", nil)
	}
	fs, err := e.App.NewFilesystem()
	if err != nil {
		return e.InternalServerError("failed to load photo", err)
	}
	defer fs.Close()
	e.Response.Header().Set("Cache-Control", "private, no-store")
	e.Response.Header().Set("X-Content-Type-Options", "nosniff")
	if err := fs.Serve(e.Response, e.Request, flag.BaseFilesPath()+"/"+filename, filename); err != nil {
		return e.NotFoundError("photo not found", err)
	}
	return nil
}
