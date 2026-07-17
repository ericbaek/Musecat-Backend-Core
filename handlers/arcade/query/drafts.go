package query

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

// ListMyArcadeDrafts returns only the authenticated creator's non-public
// arcades. Reviewers intentionally use GET /arcade/draft by id so this endpoint
// cannot become a private-directory API.
func ListMyArcadeDrafts(re *core.RequestEvent) error {
	records, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionArcade,
		"public = false && createdBy = {:createdBy}",
		"-updated",
		0,
		0,
		dbx.Params{"createdBy": re.Auth.Id},
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to list arcade drafts",
			"details": err.Error(),
		})
	}

	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		item := buildArcadeSummary(re.App, record)
		if item == nil {
			item = map[string]any{"id": record.Id}
		}
		item["public"] = false
		item["created"] = record.Get("created")
		item["updated"] = record.Get("updated")
		items = append(items, item)
	}

	return re.JSON(http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

// DeleteMyArcadeDraft deletes an unpublished arcade owned by the requester.
// Published records are historical public data and must never be removed by
// this endpoint.
func DeleteMyArcadeDraft(re *core.RequestEvent) error {
	id := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	if id == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "id is required"})
	}

	err := re.App.RunInTransaction(func(txApp core.App) error {
		record, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, id)
		if err != nil {
			return err
		}
		if record.GetBool("public") {
			return errPublishedDraftDelete
		}
		if record.GetString("createdBy") != re.Auth.Id {
			return errDraftDeleteForbidden
		}
		return txApp.Delete(record)
	})
	if err != nil {
		switch err {
		case errPublishedDraftDelete:
			return re.JSON(http.StatusConflict, map[string]any{"error": "published arcades cannot be deleted"})
		case errDraftDeleteForbidden:
			return re.JSON(http.StatusForbidden, map[string]any{"error": "only the draft creator can delete it"})
		default:
			return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade draft not found"})
		}
	}

	return re.JSON(http.StatusOK, map[string]any{"id": id, "deleted": true})
}

var (
	errPublishedDraftDelete = &draftDeleteError{"published"}
	errDraftDeleteForbidden = &draftDeleteError{"forbidden"}
)

type draftDeleteError struct{ reason string }

func (e *draftDeleteError) Error() string { return e.reason }
