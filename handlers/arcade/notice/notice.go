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
)

var noticeAccessTags = map[string]struct{}{
	"arcade_owner": {},
	"developer":    {},
	"moderator":    {},
}

type NoticeBody struct {
	ID       string     `json:"id,omitempty"`
	Arcade   string     `json:"arcade,omitempty"`
	Type     *string    `json:"type,omitempty"`
	Message  *string    `json:"message,omitempty"`
	Link     *string    `json:"link,omitempty"`
	Until    *time.Time `json:"until,omitempty"`
	Priority *float64   `json:"priority,omitempty"`
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
		if v := re.Request.FormValue("message"); v != "" {
			body.Message = &v
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

func rejectNoticeAccess(re *core.RequestEvent, arcadeID string) error {
	if re.Auth == nil {
		return re.UnauthorizedError("The request requires valid record authorization token.", nil)
	}

	if !hasAnyNoticeAccessTag(re.Auth) {
		return re.JSON(http.StatusForbidden, map[string]any{
			"error": "notice access required",
		})
	}

	if !hasElevatedNoticeAccess(re.Auth) && !ownsArcade(re.Auth, arcadeID) {
		return re.JSON(http.StatusForbidden, map[string]any{
			"error": "arcade owner can only manage notices for owned arcades",
		})
	}

	return nil
}

func noticePayload(rec *core.Record) map[string]any {
	return map[string]any{
		"id":       rec.Id,
		"arcade":   strings.TrimSpace(rec.GetString("arcade")),
		"type":     strings.TrimSpace(rec.GetString("type")),
		"message":  rec.GetString("message"),
		"link":     strings.TrimSpace(rec.GetString("link")),
		"until":    rec.Get("until"),
		"priority": rec.Get("priority"),
		"delete":   rec.GetBool("delete"),
		"photos":   append([]string{}, rec.GetStringSlice("photos")...),
		"created":  rec.Get("created"),
		"updated":  rec.Get("updated"),
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

func applyNoticeFields(rec *core.Record, body NoticeBody) {
	if body.Type != nil {
		rec.Set("type", strings.TrimSpace(*body.Type))
	}
	if body.Message != nil {
		rec.Set("message", *body.Message)
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
}

func CreateArcadeNotice(re *core.RequestEvent) error {
	body, err := parseNoticeBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}

	if body.Arcade == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "arcade is required",
		})
	}

	if err := rejectNoticeAccess(re, body.Arcade); err != nil {
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
	rec.Set("delete", false)
	applyNoticeFields(rec, body)

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

	arcadeID := strings.TrimSpace(rec.GetString("arcade"))
	if err := rejectNoticeAccess(re, arcadeID); err != nil {
		return err
	}

	applyNoticeFields(rec, body)

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

	arcadeID := strings.TrimSpace(rec.GetString("arcade"))
	if err := rejectNoticeAccess(re, arcadeID); err != nil {
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
