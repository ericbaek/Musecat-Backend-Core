package community

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const (
	CollectionPost      = "community_post"
	OriginalLocale      = "ko-KR"
	translationDelay    = 5 * time.Minute
	defaultPostPageSize = 30
	maxPostPageSize     = 100
	MaxPostPhotos       = 8
	MaxPostPhotoBytes   = 10 << 20
	MaxPostBodyBytes    = MaxPostPhotos*MaxPostPhotoBytes + (1 << 20)
)

var allowedFlairs = map[string]struct{}{
	"achievement": {},
	"question":    {},
	"tip":         {},
	"news":        {},
	"event":       {},
	"art":         {},
	"chitchat":    {},
}

type postInput struct {
	Title      string             `json:"title"`
	Body       string             `json:"body"`
	Flair      string             `json:"flair"`
	GameSeries string             `json:"game_series"`
	Photos     []*filesystem.File `json:"-"`
}

func RegisterRoutes(se *core.ServeEvent) {
	se.Router.GET("/community/posts", ListPosts)
	se.Router.GET("/community/post", GetPost)
	auth := se.Router.Group("/community").Bind(
		apis.RequireAuth("user"),
		userhandler.RequireActiveUser(),
	)
	auth.POST("/post", CreatePost).Bind(apis.BodyLimit(MaxPostBodyBytes))
	auth.PUT("/post", UpdatePost).Bind(apis.BodyLimit(MaxPostBodyBytes))
}

func CreatePost(re *core.RequestEvent) error {
	defer func() {
		if re.Request.MultipartForm != nil {
			re.Request.MultipartForm.RemoveAll()
		}
	}()
	input, err := decodePostInput(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	if err := validatePostInput(re.App, input); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": err.Error()})
	}

	collection, err := re.App.FindCollectionByNameOrId(CollectionPost)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load community post schema", "details": err.Error()})
	}

	now := time.Now().UTC()
	editableUntil := now.Add(translationDelay)
	record := core.NewRecord(collection)
	record.Set("author", re.Auth.Id)
	record.Set("game_series", input.GameSeries)
	record.Set("title", input.Title)
	record.Set("body", input.Body)
	record.Set("original_locale", OriginalLocale)
	record.Set("flair", input.Flair)
	record.Set("status", "active")
	record.Set("editable_until", editableUntil)
	record.Set("translate_after", editableUntil)
	record.Set("translation_status", "pending")
	record.Set("translation_attempts", 0)
	record.Set("original_hash", contentHash(input.Title, input.Body))
	record.Set("translations", map[string]any{})
	record.Set("photos", input.Photos)

	if err := re.App.Save(record); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to create community post", "details": err.Error()})
	}
	return re.JSON(http.StatusCreated, buildPostResponse(re.App, record, OriginalLocale))
}

func UpdatePost(re *core.RequestEvent) error {
	defer func() {
		if re.Request.MultipartForm != nil {
			re.Request.MultipartForm.RemoveAll()
		}
	}()
	id := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	if id == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "id is required"})
	}
	input, err := decodePostInput(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	if err := validatePostInput(re.App, input); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": err.Error()})
	}

	now := time.Now().UTC()
	var updated *core.Record
	err = re.App.RunInTransaction(func(tx core.App) error {
		record, err := tx.FindRecordById(CollectionPost, id)
		if err != nil || record.GetString("status") != "active" {
			return fmt.Errorf("not_found")
		}
		if record.GetString("author") != re.Auth.Id {
			return fmt.Errorf("forbidden")
		}
		if !now.Before(record.GetDateTime("editable_until").Time().UTC()) {
			return fmt.Errorf("edit_window_closed")
		}

		record.Set("game_series", input.GameSeries)
		record.Set("title", input.Title)
		record.Set("body", input.Body)
		record.Set("flair", input.Flair)
		if len(input.Photos) > 0 {
			record.Set("photos+", input.Photos)
		}
		if len(record.Original().GetStringSlice("photos"))+len(input.Photos) > MaxPostPhotos {
			return fmt.Errorf("too_many_photos")
		}
		record.Set("translation_status", "pending")
		record.Set("translation_attempts", 0)
		record.Set("translation_started_at", "")
		record.Set("translated_at", "")
		record.Set("translation_error", "")
		record.Set("translations", map[string]any{})
		record.Set("original_hash", contentHash(input.Title, input.Body))
		if err := tx.Save(record); err != nil {
			return err
		}
		updated = record
		return nil
	})
	if err != nil {
		switch err.Error() {
		case "not_found":
			return re.JSON(http.StatusNotFound, map[string]any{"error": "community post not found"})
		case "forbidden":
			return re.JSON(http.StatusForbidden, map[string]any{"error": "only the author can edit this post"})
		case "too_many_photos":
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "photos must have at most 8 items"})
		case "edit_window_closed":
			return re.JSON(http.StatusConflict, map[string]any{"error": "the five-minute edit window has closed"})
		default:
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update community post", "details": err.Error()})
		}
	}
	return re.JSON(http.StatusOK, buildPostResponse(re.App, updated, OriginalLocale))
}

func GetPost(re *core.RequestEvent) error {
	id := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	if id == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "id is required"})
	}
	record, err := re.App.FindRecordById(CollectionPost, id)
	if err != nil || record.GetString("status") != "active" {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "community post not found"})
	}
	locale, err := requestedLocale(re.Request.URL.Query().Get("locale"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	return re.JSON(http.StatusOK, buildPostResponse(re.App, record, locale))
}

func ListPosts(re *core.RequestEvent) error {
	page, perPage, err := parsePagination(re.Request.URL.Query().Get("page"), re.Request.URL.Query().Get("per_page"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	locale, err := requestedLocale(re.Request.URL.Query().Get("locale"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}

	filter := "status = 'active'"
	params := dbx.Params{}
	countFilter := dbx.And(dbx.HashExp{"status": "active"})
	series := re.Request.URL.Query()["game_series"]
	if len(series) > 50 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "at most 50 game series are allowed"})
	}
	if len(series) > 0 {
		clauses := make([]string, 0, len(series))
		seriesFilters := []dbx.Expression{}
		for i, id := range series {
			id = strings.TrimSpace(id)
			if id == "" {
				return re.JSON(http.StatusBadRequest, map[string]any{"error": "game_series must not be empty"})
			}
			key := fmt.Sprintf("series%d", i)
			params[key] = id
			clauses = append(clauses, "game_series = {:"+key+"}")
			seriesFilters = append(seriesFilters, dbx.HashExp{"game_series": id})
		}
		filter += " && (" + strings.Join(clauses, " || ") + ")"
		countFilter = dbx.And(countFilter, dbx.Or(seriesFilters...))
	}

	for _, name := range []string{"author", "flair"} {
		value := strings.TrimSpace(re.Request.URL.Query().Get(name))
		if value == "" {
			continue
		}
		if name == "flair" {
			if _, ok := allowedFlairs[value]; !ok {
				return re.JSON(http.StatusBadRequest, map[string]any{"error": "flair is invalid"})
			}
		}
		filter += " && " + name + " = {:" + name + "}"
		params[name] = value
		countFilter = dbx.And(countFilter, dbx.HashExp{name: value})
	}
	if raw := re.Request.URL.Query().Get("has_images"); raw != "" {
		if raw != "true" {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "has_images must be true"})
		}
		filter += " && photos:length > 0"
		countFilter = dbx.And(countFilter, dbx.NewExp("json_array_length(photos) > 0"))
	}

	total, err := re.App.CountRecords(CollectionPost, countFilter)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to count community posts", "details": err.Error()})
	}
	records, err := re.App.FindRecordsByFilter(
		CollectionPost,
		filter,
		"-created,-id",
		perPage,
		(page-1)*perPage,
		params,
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to list community posts", "details": err.Error()})
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, buildPostResponse(re.App, record, locale))
	}
	lastPage := 0
	if total > 0 {
		lastPage = int((total + int64(perPage) - 1) / int64(perPage))
	}
	return re.JSON(http.StatusOK, map[string]any{
		"page": page, "per_page": perPage, "last_page": lastPage, "total": total, "items": items,
	})
}

func decodePostInput(re *core.RequestEvent) (postInput, error) {
	var input postInput
	if strings.HasPrefix(re.Request.Header.Get("Content-Type"), "multipart/form-data") {
		if err := re.Request.ParseMultipartForm(MaxPostBodyBytes); err != nil {
			return input, err
		}
		input.Title = re.Request.FormValue("title")
		input.Body = re.Request.FormValue("body")
		input.Flair = re.Request.FormValue("flair")
		input.GameSeries = re.Request.FormValue("game_series")
		files, err := re.FindUploadedFiles("photos")
		if err != nil && !errors.Is(err, http.ErrMissingFile) {
			return input, err
		}
		input.Photos = files
	} else if err := json.NewDecoder(io.LimitReader(re.Request.Body, 1<<20)).Decode(&input); err != nil {
		return input, err
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Body = strings.TrimSpace(input.Body)
	input.Flair = strings.ToLower(strings.TrimSpace(input.Flair))
	input.GameSeries = strings.TrimSpace(input.GameSeries)
	if input.Flair == "" {
		input.Flair = "chitchat"
	}
	return input, nil
}

func validatePostInput(app core.App, input postInput) error {
	if len(input.Photos) > MaxPostPhotos {
		return fmt.Errorf("photos must have at most 8 items")
	}
	for _, file := range input.Photos {
		if file.Size > MaxPostPhotoBytes {
			return fmt.Errorf("each photo must be at most 10 MB")
		}
		reader, err := file.Reader.Open()
		if err != nil {
			return fmt.Errorf("failed to read photo")
		}
		mime, err := mimetype.DetectReader(reader)
		reader.Close()
		if err != nil || (mime.String() != "image/jpeg" && mime.String() != "image/png" && mime.String() != "image/webp" && mime.String() != "image/gif") {
			return fmt.Errorf("photos must be JPEG, PNG, WebP or GIF images")
		}
	}
	if input.Body == "" {
		return fmt.Errorf("body is required")
	}
	if len([]rune(input.Body)) > 10000 {
		return fmt.Errorf("body must be at most 10000 characters")
	}
	if len([]rune(input.Title)) > 200 {
		return fmt.Errorf("title must be at most 200 characters")
	}
	if _, ok := allowedFlairs[input.Flair]; !ok {
		return fmt.Errorf("flair is invalid")
	}
	if input.GameSeries != "" {
		if _, err := app.FindRecordById("game_series", input.GameSeries); err != nil {
			return fmt.Errorf("game_series not found")
		}
	}
	return nil
}

func buildPostResponse(app core.App, record *core.Record, requested string) map[string]any {
	authorName := record.GetString("author")
	if author, err := app.FindRecordById("user", authorName); err == nil {
		if nickname := author.GetString("nickname"); nickname != "" {
			authorName = nickname
		} else if username := author.GetString("username"); username != "" {
			authorName = username
		}
	}
	title := record.GetString("title")
	body := record.GetString("body")
	servedLocale := OriginalLocale
	translated := false
	if translationsPublic() && requested != OriginalLocale && record.GetString("translation_status") == "ready" {
		if value, ok := decodeTranslations(record)[requested]; ok {
			title = value.Title
			body = value.Body
			servedLocale = requested
			translated = true
		}
	}
	return map[string]any{
		"id":                 record.Id,
		"author":             record.GetString("author"),
		"author_name":        authorName,
		"game_series":        record.GetString("game_series"),
		"title":              title,
		"body":               body,
		"flair":              record.GetString("flair"),
		"photos":             append([]string{}, record.GetStringSlice("photos")...),
		"locale":             servedLocale,
		"requested_locale":   requested,
		"source_locale":      OriginalLocale,
		"translated":         translated,
		"translation_status": record.GetString("translation_status"),
		"editable_until":     record.Get("editable_until"),
		"translated_at":      record.Get("translated_at"),
		"created":            record.Get("created"),
		"updated":            record.Get("updated"),
	}
}

func decodeTranslations(record *core.Record) TranslationResult {
	result := TranslationResult{}
	raw := record.GetRaw("translations")
	switch value := raw.(type) {
	case types.JSONRaw:
		_ = json.Unmarshal(value, &result)
	case json.RawMessage:
		_ = json.Unmarshal(value, &result)
	case []byte:
		_ = json.Unmarshal(value, &result)
	case string:
		_ = json.Unmarshal([]byte(value), &result)
	default:
		encoded, err := json.Marshal(value)
		if err == nil {
			_ = json.Unmarshal(encoded, &result)
		}
	}
	return result
}

func requestedLocale(raw string) (string, error) {
	locale := strings.TrimSpace(raw)
	if locale == "" {
		return OriginalLocale, nil
	}
	switch locale {
	case "ko-KR", "en-US", "ja-JP":
		return locale, nil
	default:
		return "", fmt.Errorf("locale must be one of ko-KR, en-US, ja-JP")
	}
}

func translationsPublic() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("MUSECAT_COMMUNITY_TRANSLATIONS_PUBLIC")))
	return value == "1" || value == "true" || value == "yes"
}

func parsePagination(rawPage, rawPerPage string) (int, int, error) {
	page := 1
	perPage := defaultPostPageSize
	if raw := strings.TrimSpace(rawPage); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			return 0, 0, fmt.Errorf("page must be a positive integer")
		}
		page = value
	}
	if raw := strings.TrimSpace(rawPerPage); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > maxPostPageSize {
			return 0, 0, fmt.Errorf("per_page must be between 1 and %d", maxPostPageSize)
		}
		perPage = value
	}
	return page, perPage, nil
}

func contentHash(title, body string) string {
	sum := sha256.Sum256([]byte(title + "\x00" + body))
	return hex.EncodeToString(sum[:])
}
