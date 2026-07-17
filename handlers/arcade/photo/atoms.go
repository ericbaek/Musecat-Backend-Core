package photo

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

var (
	errPhotoAtomForbidden = errors.New("photo atom access forbidden")
	errPhotoAtomPublished = errors.New("published photo atoms are immutable")
)

// ListArcadePhotoAtoms replaces the raw PocketBase atom list. A public arcade
// is editable by authenticated contributors; a private draft is visible only
// to its creator or a developer/moderator.
func ListArcadePhotoAtoms(re *core.RequestEvent) error {
	arcadeID := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	if arcadeID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}
	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil || !canAccessPhotoAtoms(re.Auth, arcade) {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}

	atoms, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionArcadePhotoAtoms,
		"arcade = {:arcade}",
		"-created",
		0,
		0,
		dbx.Params{"arcade": arcadeID},
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to list photo atoms", "details": err.Error()})
	}
	items := make([]map[string]any, 0, len(atoms))
	for _, atom := range atoms {
		items = append(items, photoAtomPayload(atom))
	}
	return re.JSON(http.StatusOK, map[string]any{"arcade": arcadeID, "items": items, "total": len(items)})
}

// DeleteArcadePhotoAtom removes only the uploader's pending atom. Published
// atoms remain immutable so history and public file references cannot be
// silently rewritten.
func DeleteArcadePhotoAtom(re *core.RequestEvent) error {
	id := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	if id == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "id is required"})
	}

	err := re.App.RunInTransaction(func(txApp core.App) error {
		atom, err := txApp.FindRecordById(arcadeinternal.CollectionArcadePhotoAtoms, id)
		if err != nil {
			return err
		}
		arcade, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, atom.GetString("arcade"))
		if err != nil {
			return err
		}
		if !canAccessPhotoAtoms(re.Auth, arcade) {
			return errPhotoAtomForbidden
		}
		if atom.GetBool("public") {
			return errPhotoAtomPublished
		}
		if atom.GetString("createdBy") != re.Auth.Id && !hasStrictReviewerTag(re.Auth) {
			return errPhotoAtomForbidden
		}
		return txApp.Delete(atom)
	})
	if err != nil {
		switch {
		case errors.Is(err, errPhotoAtomPublished):
			return re.JSON(http.StatusConflict, map[string]any{"error": errPhotoAtomPublished.Error()})
		case errors.Is(err, errPhotoAtomForbidden):
			return re.JSON(http.StatusForbidden, map[string]any{"error": errPhotoAtomForbidden.Error()})
		default:
			return re.JSON(http.StatusNotFound, map[string]any{"error": "photo atom not found"})
		}
	}

	return re.JSON(http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// DownloadArcadePhotoAtom is the custom file endpoint for photo atoms. Public
// published photos are readable anonymously; pending and draft photos follow
// the same authenticated authorization as the atom-list API.
func DownloadArcadePhotoAtom(re *core.RequestEvent) error {
	id := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	if id == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "id is required"})
	}
	atom, err := re.App.FindRecordById(arcadeinternal.CollectionArcadePhotoAtoms, id)
	if err != nil {
		return re.NotFoundError("photo atom not found", nil)
	}
	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, atom.GetString("arcade"))
	if err != nil {
		return re.NotFoundError("photo atom not found", nil)
	}
	if !(arcade.GetBool("public") && atom.GetBool("public")) && !canAccessPhotoAtoms(re.Auth, arcade) {
		return re.NotFoundError("photo atom not found", nil)
	}
	filename := atom.GetString("photo")
	if filename == "" {
		return re.NotFoundError("photo file not found", nil)
	}

	fsys, err := re.App.NewFilesystem()
	if err != nil {
		return re.InternalServerError("failed to load photo file", err)
	}
	defer fsys.Close()
	if err := fsys.Serve(re.Response, re.Request, atom.BaseFilesPath()+"/"+filename, filename); err != nil {
		return re.NotFoundError("photo file not found", err)
	}
	return nil
}

func photoAtomPayload(atom *core.Record) map[string]any {
	return map[string]any{
		"id":        atom.Id,
		"arcade":    atom.GetString("arcade"),
		"photo":     atom.GetString("photo"),
		"file_url":  photoAtomFileURL(atom.Id),
		"public":    atom.GetBool("public"),
		"createdBy": atom.GetString("createdBy"),
		"created":   atom.Get("created"),
		"updated":   atom.Get("updated"),
	}
}

func photoAtomFileURL(id string) string {
	return "/arcade/photo/file?id=" + url.QueryEscape(id)
}

func canAccessPhotoAtoms(auth, arcade *core.Record) bool {
	if auth == nil || arcade == nil {
		return false
	}
	if arcade.GetBool("public") {
		return true
	}
	return arcade.GetString("createdBy") == auth.Id || hasStrictReviewerTag(auth)
}

func hasStrictReviewerTag(auth *core.Record) bool {
	for _, tags := range [][]string{auth.GetStringSlice("tag"), auth.GetStringSlice("tags")} {
		for _, tag := range tags {
			switch strings.ToLower(strings.TrimSpace(tag)) {
			case "developer", "moderator":
				return true
			}
		}
	}
	return false
}
