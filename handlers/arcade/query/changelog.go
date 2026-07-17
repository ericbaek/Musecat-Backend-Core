package query

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const maxChangelogPageSize = 100

// ListArcadeChangelog is the only supported wire API for arcade history.
// Changelog rows are immutable and are never edited or deleted by clients.
func ListArcadeChangelog(re *core.RequestEvent) error {
	arcadeID := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	if arcadeID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}
	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil || !canReadArcade(re, arcade) {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}

	page, perPage, err := parseChangelogPagination(re.Request.URL.Query().Get("page"), re.Request.URL.Query().Get("per_page"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	total, err := re.App.CountRecords(arcadeinternal.CollectionArcadeChangelog, dbx.HashExp{"arcade": arcadeID})
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to count arcade changelog", "details": err.Error()})
	}
	records, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeChangelog,
		"arcade = {:arcade}",
		"-created",
		perPage,
		(page-1)*perPage,
		dbx.Params{"arcade": arcadeID},
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to list arcade changelog", "details": err.Error()})
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, map[string]any{
			"id":      record.Id,
			"arcade":  record.GetString("arcade"),
			"changed": record.GetString("changed"),
			"from":    record.Get("from"),
			"to":      record.Get("to"),
			"by":      record.GetString("by"),
			"log":     record.Get("log"),
			"created": record.Get("created"),
			"updated": record.Get("updated"),
		})
	}
	lastPage := 0
	if total > 0 {
		lastPage = int((total + int64(perPage) - 1) / int64(perPage))
	}
	return re.JSON(http.StatusOK, map[string]any{
		"page":      page,
		"per_page":  perPage,
		"last_page": lastPage,
		"total":     total,
		"items":     items,
	})
}

func canReadArcade(re *core.RequestEvent, arcade *core.Record) bool {
	if arcade == nil {
		return false
	}
	if arcade.GetBool("public") {
		return true
	}
	return re.Auth != nil && (arcade.GetString("createdBy") == re.Auth.Id || HasStrictReviewerAccess(re.Auth))
}

func parseChangelogPagination(rawPage, rawPerPage string) (int, int, error) {
	page := 1
	perPage := 50
	if rawPage = strings.TrimSpace(rawPage); rawPage != "" {
		parsed, err := strconv.Atoi(rawPage)
		if err != nil || parsed < 1 {
			return 0, 0, fmt.Errorf("page must be a positive integer")
		}
		page = parsed
	}
	if rawPerPage = strings.TrimSpace(rawPerPage); rawPerPage != "" {
		parsed, err := strconv.Atoi(rawPerPage)
		if err != nil || parsed < 1 || parsed > maxChangelogPageSize {
			return 0, 0, fmt.Errorf("per_page must be between 1 and %d", maxChangelogPageSize)
		}
		perPage = parsed
	}
	return page, perPage, nil
}
