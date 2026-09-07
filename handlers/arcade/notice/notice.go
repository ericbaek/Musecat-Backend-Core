package notice

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const noticeSupporterMinimumLevel = 30

var noticeAccessTags = map[string]struct{}{
	"arcade_owner":       {},
	"developer":          {},
	"moderator":          {},
	"supporter":          {},
	"founding_supporter": {},
}

type NoticeBody struct {
	ID       string          `json:"id,omitempty"`
	Arcade   string          `json:"arcade,omitempty"`
	Type     *string         `json:"type,omitempty"`
	Document json.RawMessage `json:"document,omitempty"`
	Link     *string         `json:"link,omitempty"`
	Until    *time.Time      `json:"until,omitempty"`
	Priority *float64        `json:"priority,omitempty"`
	Photos   []*filesystem.File
}

func parseNoticeBody(re *core.RequestEvent) (NoticeBody, error) {
	contentType := strings.ToLower(strings.TrimSpace(re.Request.Header.Get("Content-Type")))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := re.Request.ParseMultipartForm(32 << 20); err != nil {
			return NoticeBody{}, err
		}

		body := NoticeBody{
			ID:     strings.TrimSpace(re.Request.FormValue("id")),
			Arcade: strings.TrimSpace(re.Request.FormValue("arcade")),
		}
		if v := strings.TrimSpace(re.Request.FormValue("type")); v != "" {
			body.Type = &v
		}
		if v := re.Request.FormValue("document"); v != "" {
			body.Document = json.RawMessage(v)
		}
		if v := strings.TrimSpace(re.Request.FormValue("link")); v != "" {
			body.Link = &v
		}
		if v := strings.TrimSpace(re.Request.FormValue("until")); v != "" {
			if parsed, err := parseNoticeTime(v); err == nil {
				body.Until = &parsed
			} else {
				return NoticeBody{}, err
			}
		}
		if v := strings.TrimSpace(re.Request.FormValue("priority")); v != "" {
			if parsed, err := parseNoticePriority(v); err == nil {
				body.Priority = &parsed
			} else {
				return NoticeBody{}, err
			}
		}

		files, err := re.FindUploadedFiles("photos")
		if err != nil {
			if !errors.Is(err, http.ErrMissingFile) {
				return NoticeBody{}, err
			}
		} else {
			body.Photos = files
		}

		return body, nil
	}

	var body NoticeBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return body, err
	}

	body.ID = strings.TrimSpace(body.ID)
	body.Arcade = strings.TrimSpace(body.Arcade)
	return body, nil
}

func parseNoticeTime(raw string) (time.Time, error) {
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, nil
	}

	return time.Parse("2006-01-02", raw)
}

func parseNoticePriority(raw string) (float64, error) {
	return strconv.ParseFloat(raw, 64)
}

func hasAnyNoticeAccessTag(auth *core.Record) bool {
	if auth == nil {
		return false
	}

	for _, tag := range auth.GetStringSlice("tag") {
		if _, ok := noticeAccessTags[strings.ToLower(strings.TrimSpace(tag))]; ok {
			return true
		}
	}

	for _, tag := range auth.GetStringSlice("tags") {
		if _, ok := noticeAccessTags[strings.ToLower(strings.TrimSpace(tag))]; ok {
			return true
		}
	}

	return false
}

func hasElevatedNoticeAccess(auth *core.Record) bool {
	if auth == nil {
		return false
	}

	for _, tag := range auth.GetStringSlice("tag") {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "developer", "moderator":
			return true
		}
	}

	for _, tag := range auth.GetStringSlice("tags") {
		switch strings.ToLower(strings.TrimSpace(tag)) {
		case "developer", "moderator":
			return true
		}
	}

	return false
}

func hasNoticeTag(auth *core.Record, want string) bool {
	if auth == nil {
		return false
	}
	for _, tags := range [][]string{auth.GetStringSlice("tag"), auth.GetStringSlice("tags")} {
		for _, tag := range tags {
			if strings.EqualFold(strings.TrimSpace(tag), want) {
				return true
			}
		}
	}
	return false
}

func isSupporter(auth *core.Record) bool {
	return hasNoticeTag(auth, "supporter") || hasNoticeTag(auth, "founding_supporter")
}

func hasSupporterNoticeAccess(app core.App, auth *core.Record) bool {
	if !isSupporter(auth) {
		return false
	}

	level, err := userhandler.LoadUserLevelState(app, auth.Id)
	return err == nil && level.Level >= noticeSupporterMinimumLevel
}

func ownsArcade(auth *core.Record, arcadeID string) bool {
	if auth == nil {
		return false
	}

	arcadeID = strings.TrimSpace(arcadeID)
	if arcadeID == "" {
		return false
	}

	for _, owned := range auth.GetStringSlice("owns") {
		if strings.TrimSpace(owned) == arcadeID {
			return true
		}
	}

	return false
}

func hasOtherOfficialArcadeManager(app core.App, auth *core.Record, arcadeID string) bool {
	users, err := app.FindAllRecords("user")
	if err != nil {
		// A failed management lookup must not let a supporter post to a
		// potentially official-managed arcade.
		return true
	}
	for _, user := range users {
		if auth != nil && user.Id == auth.Id {
			continue
		}
		if hasNoticeTag(user, "arcade_owner") && ownsArcade(user, arcadeID) {
			return true
		}
	}
	return false
}

func rejectNoticeCreateAccess(re *core.RequestEvent, arcadeID string) error {
	if re.Auth == nil {
		return re.UnauthorizedError("The request requires valid record authorization token.", nil)
	}

	if !hasAnyNoticeAccessTag(re.Auth) {
		return re.JSON(http.StatusForbidden, map[string]any{
			"error": "notice access required",
		})
	}

	if hasElevatedNoticeAccess(re.Auth) {
		return nil
	}
	if hasNoticeTag(re.Auth, "arcade_owner") && ownsArcade(re.Auth, arcadeID) {
		return nil
	}
	if isSupporter(re.Auth) {
		if !hasSupporterNoticeAccess(re.App, re.Auth) {
			return re.JSON(http.StatusForbidden, map[string]any{
				"error": "supporter level 30 or staff access required",
			})
		}
		if !hasOtherOfficialArcadeManager(re.App, re.Auth, arcadeID) {
			return nil
		}
	}
	return re.JSON(http.StatusForbidden, map[string]any{"error": "notice creation is not allowed for this arcade"})
}

func rejectNoticeMutationAccess(re *core.RequestEvent, rec *core.Record) error {
	if re.Auth == nil {
		return re.UnauthorizedError("The request requires valid record authorization token.", nil)
	}
	if hasElevatedNoticeAccess(re.Auth) {
		return nil
	}
	if rec.GetString("createdBy") != re.Auth.Id {
		return re.JSON(http.StatusForbidden, map[string]any{"error": "only the notice author can modify this notice"})
	}
	if hasNoticeTag(re.Auth, "arcade_owner") && ownsArcade(re.Auth, rec.GetString("arcade")) {
		return nil
	}
	if isSupporter(re.Auth) && !hasSupporterNoticeAccess(re.App, re.Auth) {
		return re.JSON(http.StatusForbidden, map[string]any{
			"error": "supporter level 30 or staff access required",
		})
	}

	return nil
}

func noticePayload(rec *core.Record) map[string]any {
	return map[string]any{
		"id":        rec.Id,
		"arcade":    strings.TrimSpace(rec.GetString("arcade")),
		"createdBy": rec.GetString("createdBy"),
		"type":      strings.TrimSpace(rec.GetString("type")),
		"document":  rec.Get("document"),
		"link":      strings.TrimSpace(rec.GetString("link")),
		"until":     rec.Get("until"),
		"priority":  rec.Get("priority"),
		"delete":    rec.GetBool("delete"),
		"photos":    append([]string{}, rec.GetStringSlice("photos")...),
		"created":   rec.Get("created"),
		"updated":   rec.Get("updated"),
	}
}

func noticePriorityValue(rec *core.Record) float64 {
	switch v := rec.Get("priority").(type) {
	case nil:
		return 0
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	case json.Number:
		parsed, err := v.Float64()
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return parsed
		}
	}

	return 0
}

func noticeCreatedAt(rec *core.Record) time.Time {
	switch v := rec.Get("created").(type) {
	case time.Time:
		return v
	case *time.Time:
		if v != nil {
			return *v
		}
	case types.DateTime:
		return v.Time()
	case *types.DateTime:
		if v != nil {
			return v.Time()
		}
	case string:
		if parsed, err := time.Parse(types.DefaultDateLayout, v); err == nil {
			return parsed
		}
		if parsed, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return parsed
		}
		if parsed, err := time.Parse(time.RFC3339, v); err == nil {
			return parsed
		}
	}

	return time.Time{}
}

func noticeUntil(rec *core.Record) time.Time {
	switch v := rec.Get("until").(type) {
	case time.Time:
		return v
	case *time.Time:
		if v != nil {
			return *v
		}
	case types.DateTime:
		return v.Time()
	case *types.DateTime:
		if v != nil {
			return v.Time()
		}
	case string:
		if parsed, err := time.Parse(types.DefaultDateLayout, v); err == nil {
			return parsed
		}
		if parsed, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return parsed
		}
		if parsed, err := time.Parse(time.RFC3339, v); err == nil {
			return parsed
		}
	}

	return time.Time{}
}

func ListArcadeNotice(re *core.RequestEvent) error {
	arcadeID := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	if arcadeID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "arcade is required",
		})
	}
	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil || !arcade.GetBool("public") {
		return re.JSON(http.StatusNotFound, map[string]any{
			"error": "arcade not found",
		})
	}

	recs, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeNotice,
		"arcade = {:arcade}",
		"",
		0,
		0,
		dbx.Params{"arcade": arcadeID},
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to list arcade_notice",
			"details": err.Error(),
		})
	}

	filtered := make([]*core.Record, 0, len(recs))
	now := time.Now().UTC()
	for _, rec := range recs {
		if rec.GetBool("delete") {
			continue
		}
		if until := noticeUntil(rec); !until.IsZero() && !until.After(now) {
			rec.Set("delete", true)
			if err := re.App.Save(rec); err != nil {
				return re.JSON(http.StatusBadGateway, map[string]any{
					"error":   "failed to expire arcade_notice",
					"details": err.Error(),
				})
			}
			continue
		}
		filtered = append(filtered, rec)
	}

	sort.SliceStable(filtered, func(i, j int) bool {
		pi := noticePriorityValue(filtered[i])
		pj := noticePriorityValue(filtered[j])
		if pi <= 0 && pj > 0 {
			return false
		}
		if pj <= 0 && pi > 0 {
			return true
		}
		if pi != pj {
			return pi < pj
		}

		return noticeCreatedAt(filtered[i]).After(noticeCreatedAt(filtered[j]))
	})

	items := make([]map[string]any, 0, len(filtered))
	for _, rec := range filtered {
		items = append(items, noticePayload(rec))
	}

	return re.JSON(http.StatusOK, map[string]any{
		"items": items,
		"total": len(items),
	})
}

func applyNoticeFields(rec *core.Record, body NoticeBody) error {
	if body.Type != nil {
		rec.Set("type", strings.TrimSpace(*body.Type))
	}
	if len(body.Document) > 0 {
		document, err := normalizeNoticeDocument(body.Document)
		if err != nil {
			return err
		}
		rec.Set("document", document)
	}
	if body.Link != nil {
		rec.Set("link", strings.TrimSpace(*body.Link))
	}
	if body.Until != nil {
		rec.Set("until", body.Until.UTC())
	}
	if body.Priority != nil {
		rec.Set("priority", *body.Priority)
	}
	if body.Photos != nil {
		rec.Set("photos", body.Photos)
	}
	return nil
}

func CreateArcadeNotice(re *core.RequestEvent) error {
	body, err := parseNoticeBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if len(body.Document) == 0 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "document is required"})
	}

	if body.Arcade == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "arcade is required",
		})
	}

	if err := rejectNoticeCreateAccess(re, body.Arcade); err != nil {
		return err
	}

	arcadeRec, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{
			"error":   "arcade not found",
			"details": err.Error(),
		})
	}

	coll, err := re.App.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeNotice)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create arcade_notice",
			"details": fmt.Sprintf("failed to find arcade_notice collection: %v", err),
		})
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeRec.Id)
	rec.Set("createdBy", re.Auth.Id)
	rec.Set("delete", false)
	if err := applyNoticeFields(rec, body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create arcade_notice",
			"details": fmt.Sprintf("failed to save arcade_notice record: %v", err),
		})
	}

	return re.JSON(http.StatusOK, noticePayload(rec))
}

func UpdateArcadeNotice(re *core.RequestEvent) error {
	body, err := parseNoticeBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}

	if body.ID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "id is required",
		})
	}

	rec, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeNotice, body.ID)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{
			"error":   "notice not found",
			"details": err.Error(),
		})
	}

	if err := rejectNoticeMutationAccess(re, rec); err != nil {
		return err
	}

	if err := applyNoticeFields(rec, body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to update arcade_notice",
			"details": fmt.Sprintf("failed to save arcade_notice record: %v", err),
		})
	}

	return re.JSON(http.StatusOK, noticePayload(rec))
}

func DeleteArcadeNotice(re *core.RequestEvent) error {
	body, err := parseNoticeBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}

	if body.ID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "id is required",
		})
	}

	rec, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeNotice, body.ID)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{
			"error":   "notice not found",
			"details": err.Error(),
		})
	}

	if err := rejectNoticeMutationAccess(re, rec); err != nil {
		return err
	}

	rec.Set("delete", true)

	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to delete arcade_notice",
			"details": fmt.Sprintf("failed to update arcade_notice record: %v", err),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"id":     body.ID,
		"delete": true,
	})
}
