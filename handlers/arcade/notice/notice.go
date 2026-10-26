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
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/ericbaek/musecat-backend-core/service/xp"
)

const noticeSupporterMinimumLevel = 30
const maxNoticePhotos = 3
const maxNoticePhotoBytes = 5 << 20

// MaxNoticeBodyBytes includes one MiB for the document and multipart metadata.
const MaxNoticeBodyBytes = maxNoticePhotos*maxNoticePhotoBytes + (1 << 20)

var noticeAccessTags = map[string]struct{}{
	"arcade_owner":       {},
	"developer":          {},
	"moderator":          {},
	"supporter":          {},
	"founding_supporter": {},
}

type NoticeBody struct {
	Title       *string            `json:"title,omitempty"`
	DisplayMode *string            `json:"display_mode,omitempty"`
	ID          string             `json:"id,omitempty"`
	Arcade      string             `json:"arcade,omitempty"`
	Type        *string            `json:"type,omitempty"`
	Document    json.RawMessage    `json:"document,omitempty"`
	Link        *string            `json:"link,omitempty"`
	Until       *time.Time         `json:"until,omitempty"`
	Priority    *float64           `json:"priority,omitempty"`
	Photos      []*filesystem.File `json:"-"`
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
		if values, ok := re.Request.MultipartForm.Value["title"]; ok && len(values) > 0 {
			body.Title = &values[0]
		}
		if values, ok := re.Request.MultipartForm.Value["display_mode"]; ok && len(values) > 0 {
			body.DisplayMode = &values[0]
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

		return body, arcadeinternal.ValidateImageUploads(body.Photos, maxNoticePhotos, maxNoticePhotoBytes)
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

	level, err := xp.LoadUserLevelState(app, auth.Id)
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
	collection, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		return true
	}
	roles := make([]string, 0, 2)
	for _, field := range []string{"tag", "tags"} {
		if collection.Fields.GetByName(field) != nil {
			roles = append(roles, "EXISTS (SELECT 1 FROM json_each(u."+field+") WHERE lower(trim(value)) = 'arcade_owner')")
		}
	}
	if len(roles) == 0 {
		return true
	}
	userID := ""
	if auth != nil {
		userID = auth.Id
	}
	var result struct {
		Managed bool `db:"managed"`
	}
	err = app.DB().NewQuery("SELECT EXISTS (SELECT 1 FROM user u WHERE (" + strings.Join(roles, " OR ") + ") AND EXISTS (SELECT 1 FROM json_each(u.owns) WHERE value = {:arcade}) AND u.id != {:user}) AS managed").
		Bind(dbx.Params{"arcade": arcadeID, "user": userID}).One(&result)
	// Failed lookups fail closed without loading the entire user collection.
	return err != nil || result.Managed
}

type noticeRequestError struct {
	status  int
	message string
	mode    string
	limit   int
}

func (err *noticeRequestError) Error() string { return err.message }

func noticeError(status int, message string) error {
	return &noticeRequestError{status: status, message: message}
}

func writeNoticeError(re *core.RequestEvent, err error) error {
	var requestError *noticeRequestError
	if errors.As(err, &requestError) {
		payload := map[string]any{"error": requestError.message}
		if requestError.limit > 0 {
			payload["code"] = "notice_display_limit_exceeded"
			payload["display_mode"] = requestError.mode
			payload["limit"] = requestError.limit
		}
		return re.JSON(requestError.status, payload)
	}
	return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to save arcade_notice", "details": err.Error()})
}

func noticeCreateAccess(app core.App, auth *core.Record, arcadeID string) error {
	if auth == nil {
		return noticeError(http.StatusUnauthorized, "authentication required")
	}
	if !hasAnyNoticeAccessTag(auth) {
		return noticeError(http.StatusForbidden, "notice access required")
	}
	if hasElevatedNoticeAccess(auth) {
		return nil
	}
	if hasNoticeTag(auth, "arcade_owner") && ownsArcade(auth, arcadeID) {
		return nil
	}
	if isSupporter(auth) {
		if !hasSupporterNoticeAccess(app, auth) {
			return noticeError(http.StatusForbidden, "supporter level 30 or staff access required")
		}
		if !hasOtherOfficialArcadeManager(app, auth, arcadeID) {
			return nil
		}
	}
	return noticeError(http.StatusForbidden, "notice creation is not allowed for this arcade")
}

func noticeMutationAccess(app core.App, auth *core.Record, rec *core.Record) error {
	if auth == nil {
		return noticeError(http.StatusUnauthorized, "authentication required")
	}
	arcade, err := app.FindRecordById(arcadeinternal.CollectionArcade, rec.GetString("arcade"))
	if err != nil {
		return noticeError(http.StatusNotFound, "arcade not found")
	}
	if !arcadeinternal.CanWriteArcade(auth, arcade) {
		return noticeError(http.StatusForbidden, arcadeinternal.ErrArcadeWriteForbidden.Error())
	}
	if hasElevatedNoticeAccess(auth) {
		return nil
	}
	author := rec.GetString("createdBy")
	if author == "" && hasNoticeTag(auth, "arcade_owner") && ownsArcade(auth, arcade.Id) {
		return nil
	}
	if author != auth.Id {
		return noticeError(http.StatusForbidden, "only the notice author can modify this notice")
	}
	if hasNoticeTag(auth, "arcade_owner") && ownsArcade(auth, arcade.Id) {
		return nil
	}
	if isSupporter(auth) && !hasSupporterNoticeAccess(app, auth) {
		return noticeError(http.StatusForbidden, "supporter level 30 or staff access required")
	}
	return nil
}

func noticeDisplayMode(rec *core.Record) string {
	mode := rec.GetString("display_mode")
	if mode == "" {
		return "normal"
	}
	return mode
}

func noticeDisplayRank(rec *core.Record) int {
	switch noticeDisplayMode(rec) {
	case "required":
		return 0
	case "pinned":
		return 1
	default:
		return 2
	}
}

// Called inside the same write transaction as Save. PocketBase serializes write
// transactions, so simultaneous promotions cannot reserve the same last slot.
func checkNoticeDisplayLimit(app core.App, rec *core.Record) error {
	mode := noticeDisplayMode(rec)
	limit := 0
	switch mode {
	case "required":
		limit = 1
	case "pinned":
		limit = 3
	}
	now := time.Now().UTC()
	if limit == 0 || rec.GetBool("delete") {
		return nil
	}
	if until := noticeUntil(rec); !until.IsZero() && !until.After(now) {
		return nil
	}
	records, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcadeNotice,
		"arcade = {:arcade} && display_mode = {:mode} && delete = false && id != {:id} && (until = '' || until > {:now})", "", limit, 0,
		dbx.Params{"arcade": rec.GetString("arcade"), "mode": mode, "id": rec.Id, "now": now.Format(types.DefaultDateLayout)})
	if err != nil {
		return err
	}
	count := len(records)
	if count >= limit {
		return &noticeRequestError{status: http.StatusConflict, message: "notice display limit exceeded", mode: mode, limit: limit}
	}
	return nil
}

func noticePayload(rec *core.Record) map[string]any {
	return map[string]any{
		"id":           rec.Id,
		"title":        rec.GetString("title"),
		"display_mode": noticeDisplayMode(rec),
		"arcade":       strings.TrimSpace(rec.GetString("arcade")),
		"createdBy":    rec.GetString("createdBy"),
		"type":         strings.TrimSpace(rec.GetString("type")),
		"document":     rec.Get("document"),
		"link":         strings.TrimSpace(rec.GetString("link")),
		"until":        rec.Get("until"),
		"priority":     rec.Get("priority"),
		"delete":       rec.GetBool("delete"),
		"photos":       append([]string{}, rec.GetStringSlice("photos")...),
		"created":      rec.Get("created"),
		"updated":      rec.Get("updated"),
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
		ri, rj := noticeDisplayRank(filtered[i]), noticeDisplayRank(filtered[j])
		if ri != rj {
			return ri < rj
		}
		if ri == 1 {
			pi, pj := noticePriorityValue(filtered[i]), noticePriorityValue(filtered[j])
			if pi != pj {
				return pi < pj
			}
		}
		ti, tj := noticeCreatedAt(filtered[i]), noticeCreatedAt(filtered[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return filtered[i].Id < filtered[j].Id
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
	if body.Title != nil {
		title := strings.TrimSpace(*body.Title)
		if title == "" {
			return fmt.Errorf("title is required")
		}
		if utf8.RuneCountInString(title) > 5000 {
			return fmt.Errorf("title exceeds storage limit of 5000 characters")
		}
		rec.Set("title", title)
	}
	if body.DisplayMode != nil {
		mode := strings.TrimSpace(*body.DisplayMode)
		if mode != "normal" && mode != "pinned" && mode != "required" {
			return fmt.Errorf("invalid display_mode")
		}
		rec.Set("display_mode", mode)
	}
	if body.Type != nil {
		topic := strings.TrimSpace(*body.Type)
		switch topic {
		case "alert", "close", "issue", "event":
		default:
			if topic == "" || topic != rec.GetString("type") {
				return fmt.Errorf("invalid notice type")
			}
		}
		rec.Set("type", topic)
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
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return apis.ErrRequestEntityTooLarge
		}
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	if len(body.Document) == 0 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "document is required"})
	}
	if body.Title == nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "title is required"})
	}
	if body.Arcade == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}
	var rec *core.Record
	err = re.App.RunInTransaction(func(tx core.App) error {
		if err := noticeCreateAccess(tx, re.Auth, body.Arcade); err != nil {
			return err
		}
		arcade, err := tx.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return noticeError(http.StatusNotFound, "arcade not found")
		}
		if !arcadeinternal.CanWriteArcade(re.Auth, arcade) {
			return noticeError(http.StatusForbidden, arcadeinternal.ErrArcadeWriteForbidden.Error())
		}
		coll, err := tx.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeNotice)
		if err != nil {
			return err
		}
		rec = core.NewRecord(coll)
		rec.Set("arcade", arcade.Id)
		rec.Set("createdBy", re.Auth.Id)
		rec.Set("display_mode", "normal")
		rec.Set("delete", false)
		if err := applyNoticeFields(rec, body); err != nil {
			return noticeError(http.StatusBadRequest, err.Error())
		}
		if err := checkNoticeDisplayLimit(tx, rec); err != nil {
			return err
		}
		return tx.Save(rec)
	})
	if err != nil {
		return writeNoticeError(re, err)
	}
	return re.JSON(http.StatusOK, noticePayload(rec))
}

func UpdateArcadeNotice(re *core.RequestEvent) error {
	body, err := parseNoticeBody(re)
	if err != nil {
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return apis.ErrRequestEntityTooLarge
		}
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	if body.ID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "id is required"})
	}
	var rec *core.Record
	err = re.App.RunInTransaction(func(tx core.App) error {
		var err error
		rec, err = tx.FindRecordById(arcadeinternal.CollectionArcadeNotice, body.ID)
		if err != nil {
			return noticeError(http.StatusNotFound, "notice not found")
		}
		if err := noticeMutationAccess(tx, re.Auth, rec); err != nil {
			return err
		}
		if err := applyNoticeFields(rec, body); err != nil {
			return noticeError(http.StatusBadRequest, err.Error())
		}
		if err := checkNoticeDisplayLimit(tx, rec); err != nil {
			return err
		}
		return tx.Save(rec)
	})
	if err != nil {
		return writeNoticeError(re, err)
	}
	return re.JSON(http.StatusOK, noticePayload(rec))
}

func DeleteArcadeNotice(re *core.RequestEvent) error {
	body, err := parseNoticeBody(re)
	if err != nil {
		if errors.Is(err, apis.ErrRequestEntityTooLarge) {
			return apis.ErrRequestEntityTooLarge
		}
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	if body.ID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "id is required"})
	}
	err = re.App.RunInTransaction(func(tx core.App) error {
		rec, err := tx.FindRecordById(arcadeinternal.CollectionArcadeNotice, body.ID)
		if err != nil {
			return noticeError(http.StatusNotFound, "notice not found")
		}
		if err := noticeMutationAccess(tx, re.Auth, rec); err != nil {
			return err
		}
		rec.Set("delete", true)
		return tx.Save(rec)
	})
	if err != nil {
		return writeNoticeError(re, err)
	}
	return re.JSON(http.StatusOK, map[string]any{"id": body.ID, "delete": true})
}
