package query

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	memoDiff "github.com/ericbaek/musecat-backend-core/handlers/arcade/memo/diff"
	photo "github.com/ericbaek/musecat-backend-core/handlers/arcade/photo/read"
)

const (
	maxChangelogPageSize = 100
	changelogCategories  = "basic,game,hour,sns,gtk,photo,memo"
)

var changelogCategorySet = map[string]struct{}{
	"basic": {},
	"game":  {},
	"hour":  {},
	"sns":   {},
	"gtk":   {},
	"photo": {},
	"memo":  {},
}

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
	changed, err := parseChangelogCategory(re.Request.URL.Query().Get("changed"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	page, perPage, err := parseChangelogPagination(re.Request.URL.Query().Get("page"), re.Request.URL.Query().Get("per_page"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	filter := "arcade = {:arcade}"
	params := dbx.Params{"arcade": arcadeID}
	if changed != "" {
		filter += " && changed = {:changed}"
		params["changed"] = changed
	}
	total, err := re.App.CountRecords(arcadeinternal.CollectionArcadeChangelog, dbx.HashExp(params))
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to count arcade changelog", "details": err.Error()})
	}
	records, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeChangelog,
		filter,
		"-created",
		perPage,
		(page-1)*perPage,
		params,
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to list arcade changelog", "details": err.Error()})
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		item := map[string]any{
			"id":      record.Id,
			"arcade":  record.GetString("arcade"),
			"changed": record.GetString("changed"),
			"from":    record.Get("from"),
			"to":      record.Get("to"),
			"by":      record.GetString("by"),
			"log":     record.Get("log"),
			"created": record.Get("created"),
			"updated": record.Get("updated"),
		}
		if record.GetString("changed") == "memo" {
			item["memo"] = memoDiff.Build(re.App, arcadeID, record.GetString("from"), record.GetString("to"))
		}
		items = append(items, item)
	}
	if err := photo.ExpandChangelogAssets(re, items); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load changelog photos"})
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

func parseChangelogCategory(raw string) (string, error) {
	changed := strings.TrimSpace(raw)
	if changed == "" {
		return "", nil
	}
	if _, ok := changelogCategorySet[changed]; !ok {
		return "", fmt.Errorf("changed must be one of %s", changelogCategories)
	}
	return changed, nil
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
