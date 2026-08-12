package user

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const (
	maxUserChangelogPageSize = 100
	userChangelogCategories  = "basic,game,hour,sns,gtk,photo"
)

var userChangelogCategorySet = map[string]struct{}{
	"basic": {},
	"game":  {},
	"hour":  {},
	"sns":   {},
	"gtk":   {},
	"photo": {},
}

// GetUserChangelog handles GET /user/changelog?user=<userId>.
//
// This endpoint is intentionally separate from /arcade/changelog. It exposes
// only the rows authored by the requested user and applies the same arcade
// visibility boundary as the arcade-scoped history endpoint. Public arcades
// are readable anonymously; a caller may additionally read their own private
// arcade history, while strict reviewers may read all rows.
func GetUserChangelog(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	userID := strings.TrimSpace(q.Get("user"))
	if userID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "user is required"})
	}

	changed, err := parseUserChangelogCategory(q.Get("changed"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	userRecord, err := re.App.FindRecordById(CollectionUser, userID)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "user not found"})
	}

	page, perPage, err := parseUserChangelogPagination(q.Get("page"), q.Get("per_page"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	if userRecord.GetBool("withdrawn") {
		return re.JSON(http.StatusOK, userChangelogPage(page, perPage, 0, 0, []map[string]any{}))
	}
	where, params := userChangelogVisibility(re, userID, changed)
	var total int64
	if err := re.App.DB().NewQuery(fmt.Sprintf(`
SELECT COUNT(*)
FROM arcade_changelog c
INNER JOIN arcade a ON a.id = c.arcade
WHERE %s
`, where)).Bind(params).Row(&total); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to count user changelog",
			"details": err.Error(),
		})
	}

	rows, err := re.App.DB().NewQuery(fmt.Sprintf(`
SELECT
	c.id,
	c.arcade,
	a.basic,
	COALESCE(ab.name, '') AS arcade_name,
	c.changed,
	c."from",
	c."to",
	c."by",
	c.created,
	c.log
FROM arcade_changelog c
INNER JOIN arcade a ON a.id = c.arcade
LEFT JOIN arcade_basic ab ON ab.id = a.basic
WHERE %s
ORDER BY c.created DESC, c.id DESC
LIMIT {:limit} OFFSET {:offset}
`, where)).Bind(mergeUserChangelogParams(params, perPage, (page-1)*perPage)).Rows()
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to list user changelog",
			"details": err.Error(),
		})
	}
	defer rows.Close()

	items := make([]map[string]any, 0, perPage)
	for rows.Next() {
		raw := dbx.NullStringMap{}
		if err := rows.ScanMap(raw); err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{
				"error":   "failed to decode user changelog",
				"details": err.Error(),
			})
		}
		items = append(items, map[string]any{
			"id":          userChangelogString(raw, "id"),
			"arcade":      userChangelogString(raw, "arcade"),
			"arcade_name": userChangelogString(raw, "arcade_name"),
			"changed":     userChangelogString(raw, "changed"),
			"from":        userChangelogString(raw, "from"),
			"to":          userChangelogString(raw, "to"),
			"by":          userChangelogString(raw, "by"),
			"created":     userChangelogString(raw, "created"),
			"updated":     nil,
			"log":         decodeUserChangelogJSON(userChangelogString(raw, "log")),
		})
	}
	if err := rows.Err(); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to iterate user changelog",
			"details": err.Error(),
		})
	}

	lastPage := int64(0)
	if total > 0 {
		lastPage = (total + int64(perPage) - 1) / int64(perPage)
	}
	return re.JSON(http.StatusOK, userChangelogPage(page, perPage, lastPage, total, items))
}

func userChangelogPage(page, perPage int, lastPage, total int64, items []map[string]any) map[string]any {
	return map[string]any{
		"page":      page,
		"per_page":  perPage,
		"last_page": lastPage,
		"total":     total,
		"items":     items,
	}
}

func parseUserChangelogPagination(rawPage, rawPerPage string) (int, int, error) {
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
		if err != nil || parsed < 1 || parsed > maxUserChangelogPageSize {
			return 0, 0, fmt.Errorf("per_page must be between 1 and %d", maxUserChangelogPageSize)
		}
		perPage = parsed
	}
	return page, perPage, nil
}

func parseUserChangelogCategory(raw string) (string, error) {
	changed := strings.TrimSpace(strings.ToLower(raw))
	if changed == "" {
		return "", nil
	}
	if _, ok := userChangelogCategorySet[changed]; !ok {
		return "", fmt.Errorf("changed must be one of %s", userChangelogCategories)
	}
	return changed, nil
}

func userChangelogVisibility(re *core.RequestEvent, userID, changed string) (string, dbx.Params) {
	parts := []string{"c.\"by\" = {:user}"}
	params := dbx.Params{"user": userID}
	if changed != "" {
		parts = append(parts, "c.changed = {:changed}")
		params["changed"] = changed
	} else {
		parts = append(parts, "c.changed IN ('basic', 'game', 'hour', 'sns', 'gtk', 'photo')")
	}

	switch {
	case hasStrictReviewerAccess(re.Auth):
		// Reviewers can audit private arcade history as well as public history.
	case re.Auth != nil:
		parts = append(parts, "(a.public = 1 OR a.\"createdBy\" = {:viewer})")
		params["viewer"] = re.Auth.Id
	default:
		parts = append(parts, "a.public = 1")
	}

	return strings.Join(parts, " AND "), params
}

func mergeUserChangelogParams(params dbx.Params, perPage, offset int) dbx.Params {
	merged := dbx.Params{}
	for key, value := range params {
		merged[key] = value
	}
	merged["limit"] = perPage
	merged["offset"] = offset
	return merged
}

func userChangelogString(raw dbx.NullStringMap, key string) string {
	value, ok := raw[key]
	if !ok || !value.Valid {
		return ""
	}
	return value.String
}

func decodeUserChangelogJSON(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	return value
}

func hasStrictReviewerAccess(auth *core.Record) bool {
	if auth == nil {
		return false
	}
	for _, values := range [][]string{auth.GetStringSlice("tag"), auth.GetStringSlice("tags")} {
		for _, value := range values {
			switch strings.ToLower(strings.TrimSpace(value)) {
			case "developer", "moderator":
				return true
			}
		}
	}
	return false
}
