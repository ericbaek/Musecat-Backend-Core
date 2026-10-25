package community

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
)

const CollectionComment = "community_comment"

type commentInput struct {
	Post       string             `json:"post"`
	Parent     string             `json:"parent"`
	ReplyTo    string             `json:"reply_to"`
	Body       string             `json:"body"`
	Photos     []*filesystem.File `json:"-"`
	KeepPhotos *[]string          `json:"keep_photos"`
}
type socialCount struct {
	Target string `db:"target"`
	Count  int    `db:"count"`
	Liked  int    `db:"liked"`
}
type authorSummary struct{ Name, Avatar string }
type socialCursor struct {
	Created string `json:"created"`
	ID      string `json:"id"`
}

func activePost(app core.App, id string) error {
	record, err := app.FindRecordById(CollectionPost, id)
	if err != nil || record.GetString("status") != "active" {
		return apis.NewNotFoundError("Community post not found", nil)
	}
	return nil
}
func activeComment(app core.App, id string) (*core.Record, error) {
	record, err := app.FindRecordById(CollectionComment, id)
	if err != nil || record.GetString("status") != "active" {
		return nil, apis.NewNotFoundError("Comment not found", nil)
	}
	return record, activePost(app, record.GetString("post"))
}
func viewerID(re *core.RequestEvent) string {
	if re.Auth != nil && re.Auth.Collection().Name == "user" {
		return re.Auth.Id
	}
	return ""
}
func privateSocialResponse(re *core.RequestEvent) {
	re.Response.Header().Set("Cache-Control", "private, no-store")
	re.Response.Header().Set("Vary", "Authorization")
}

func decodeCommentInput(re *core.RequestEvent) (commentInput, error) {
	var input commentInput
	if strings.HasPrefix(re.Request.Header.Get("Content-Type"), "multipart/form-data") {
		if err := re.Request.ParseMultipartForm(MaxCommentBodyBytes); err != nil {
			return input, err
		}
		input.Post = re.Request.FormValue("post")
		input.Parent = re.Request.FormValue("parent")
		input.ReplyTo = re.Request.FormValue("reply_to")
		input.Body = re.Request.FormValue("body")
		files, err := re.FindUploadedFiles("photos")
		if err != nil && !errors.Is(err, http.ErrMissingFile) {
			return input, err
		}
		input.Photos = files
		if values, ok := re.Request.MultipartForm.Value["keep_photos"]; ok {
			var keep []string
			if len(values) != 1 || json.Unmarshal([]byte(values[0]), &keep) != nil {
				return input, fmt.Errorf("invalid kept photos")
			}
			input.KeepPhotos = &keep
		}
	} else if err := json.NewDecoder(io.LimitReader(re.Request.Body, 16<<10)).Decode(&input); err != nil {
		return input, err
	}
	input.Body = strings.TrimSpace(input.Body)
	if len([]rune(input.Body)) > 2000 {
		return input, fmt.Errorf("comment must contain at most 2000 characters")
	}
	return input, validatePhotos(input.Photos, MaxCommentPhotos)
}
func cleanupCommentUpload(re *core.RequestEvent) {
	if re.Request.MultipartForm != nil {
		_ = re.Request.MultipartForm.RemoveAll()
	}
}

func CreateComment(re *core.RequestEvent) error {
	defer cleanupCommentUpload(re)
	input, err := decodeCommentInput(re)
	if err != nil {
		return apis.NewBadRequestError("Invalid comment or images", err)
	}
	if input.Body == "" && len(input.Photos) == 0 {
		return apis.NewBadRequestError("A comment requires text or a photo", nil)
	}
	var saved *core.Record
	err = re.App.RunInTransaction(func(tx core.App) error {
		if err := activePost(tx, input.Post); err != nil {
			return err
		}
		target := input.ReplyTo
		if target == "" {
			target = input.Parent
		}
		parent := ""
		if target != "" {
			record, err := activeComment(tx, target)
			if err != nil {
				return err
			}
			if record.GetString("post") != input.Post {
				return apis.NewBadRequestError("Reply target belongs to another post", nil)
			}
			parent = record.GetString("parent")
			if parent == "" {
				parent = record.Id
			}
			if input.Parent != "" && input.Parent != parent {
				return apis.NewBadRequestError("Invalid reply parent", nil)
			}
		}
		collection, err := tx.FindCollectionByNameOrId(CollectionComment)
		if err != nil {
			return err
		}
		saved = core.NewRecord(collection)
		now := time.Now().UTC()
		deadline := now.Add(translationDelay)
		saved.Set("post", input.Post)
		saved.Set("author", re.Auth.Id)
		saved.Set("parent", parent)
		saved.Set("reply_to", target)
		saved.Set("body", input.Body)
		saved.Set("photos", input.Photos)
		saved.Set("status", "active")
		saved.Set("editable_until", deadline)
		saved.Set("translate_after", deadline)
		saved.Set("translation_status", "pending")
		if input.Body == "" {
			saved.Set("translation_status", "ready")
		}
		saved.Set("original_hash", contentHash("", input.Body))
		return tx.Save(saved)
	})
	if err != nil {
		return err
	}
	return serveComment(re, saved, OriginalLocale, http.StatusCreated)
}
func UpdateComment(re *core.RequestEvent) error {
	defer cleanupCommentUpload(re)
	input, err := decodeCommentInput(re)
	if err != nil {
		return apis.NewBadRequestError("Invalid comment or images", err)
	}
	var saved *core.Record
	err = re.App.RunInTransaction(func(tx core.App) error {
		record, err := activeComment(tx, re.Request.URL.Query().Get("id"))
		if err != nil {
			return err
		}
		if record.GetString("author") != re.Auth.Id {
			return apis.NewForbiddenError("Only the author can edit this comment", nil)
		}
		now := time.Now().UTC()
		if !now.Before(record.GetDateTime("editable_until").Time()) {
			return apis.NewApiError(http.StatusConflict, "The five-minute edit window has closed", nil)
		}
		existing := record.GetStringSlice("photos")
		keep := existing
		if input.KeepPhotos != nil {
			keep = *input.KeepPhotos
			allowed := map[string]bool{}
			for _, name := range existing {
				allowed[name] = true
			}
			seen := map[string]bool{}
			for _, name := range keep {
				if !allowed[name] || seen[name] {
					return apis.NewBadRequestError("Invalid kept photos", nil)
				}
				seen[name] = true
			}
		}
		if len(keep)+len(input.Photos) > MaxCommentPhotos {
			return apis.NewBadRequestError("Comments support at most 3 photos", nil)
		}
		if input.Body == "" && len(keep)+len(input.Photos) == 0 {
			return apis.NewBadRequestError("A comment requires text or a photo", nil)
		}
		if record.GetString("body") != input.Body || !reflect.DeepEqual(existing, keep) || len(input.Photos) > 0 {
			record.Set("edited_at", now)
			record.Set("photos", keep)
			if len(input.Photos) > 0 {
				record.Set("photos+", input.Photos)
			}
			if record.GetString("body") != input.Body {
				record.Set("body", input.Body)
				record.Set("translation_status", "pending")
				if input.Body == "" {
					record.Set("translation_status", "ready")
				}
				record.Set("translation_attempts", 0)
				record.Set("translation_started_at", "")
				record.Set("translations", map[string]any{})
				record.Set("translated_at", "")
				record.Set("translation_error", "")
				record.Set("original_hash", contentHash("", input.Body))
			}
			if err = tx.Save(record); err != nil {
				return err
			}
		}
		saved = record
		return nil
	})
	if err != nil {
		return err
	}
	return serveComment(re, saved, OriginalLocale, http.StatusOK)
}
func DeleteComment(re *core.RequestEvent) error {
	err := re.App.RunInTransaction(func(tx core.App) error {
		record, err := tx.FindRecordById(CollectionComment, re.Request.URL.Query().Get("id"))
		if err != nil {
			return apis.NewNotFoundError("Comment not found", nil)
		}
		if err = activePost(tx, record.GetString("post")); err != nil {
			return err
		}
		if record.GetString("author") != re.Auth.Id {
			return apis.NewForbiddenError("Only the author can delete this comment", nil)
		}
		return eraseComment(tx, record)
	})
	if err != nil {
		return err
	}
	return re.NoContent(http.StatusNoContent)
}
func eraseComment(app core.App, record *core.Record) error {
	record.Set("status", "deleted")
	record.Set("body", "")
	record.Set("photos", []string{})
	record.Set("translations", map[string]any{})
	record.Set("translation_started_at", "")
	record.Set("translation_error", "")
	return app.Save(record)
}
func GetComment(re *core.RequestEvent) error {
	record, err := activeComment(re.App, re.Request.URL.Query().Get("id"))
	if err != nil {
		return err
	}
	locale, err := requestedLocale(re.Request.URL.Query().Get("locale"))
	if err != nil {
		return apis.NewBadRequestError("Invalid locale", nil)
	}
	return serveComment(re, record, locale, http.StatusOK)
}
func serveComment(re *core.RequestEvent, record *core.Record, locale string, status int) error {
	items, err := commentResponses(re.App, []*core.Record{record}, locale, viewerID(re))
	if err != nil {
		return err
	}
	privateSocialResponse(re)
	return re.JSON(status, items[0])
}
func ListComments(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	post, parent := q.Get("post"), q.Get("parent")
	if err := activePost(re.App, post); err != nil {
		return err
	}
	locale, err := requestedLocale(q.Get("locale"))
	if err != nil {
		return apis.NewBadRequestError("Invalid locale", nil)
	}
	if parent != "" {
		record, err := re.App.FindRecordById(CollectionComment, parent)
		if err != nil || record.GetString("post") != post || record.GetString("parent") != "" {
			return apis.NewBadRequestError("Invalid reply parent", nil)
		}
	}
	query := re.App.RecordQuery(CollectionComment).AndWhere(dbx.HashExp{"post": post, "parent": parent})
	if parent == "" {
		query.AndWhere(dbx.NewExp("(status = 'active' OR (status = 'deleted' AND EXISTS (SELECT 1 FROM community_comment AS reply WHERE reply.parent = community_comment.id AND reply.status = 'active')))"))
	} else {
		query.AndWhere(dbx.HashExp{"status": "active"})
	}
	if raw := q.Get("cursor"); raw != "" {
		var cursor socialCursor
		if len(raw) > 512 {
			return apis.NewBadRequestError("Invalid cursor", nil)
		}
		data, err := base64.RawURLEncoding.DecodeString(raw)
		if err != nil || len(data) > 256 || json.Unmarshal(data, &cursor) != nil || cursor.Created == "" || cursor.ID == "" {
			return apis.NewBadRequestError("Invalid cursor", nil)
		}
		query.AndWhere(dbx.NewExp("(created < {:created} OR (created = {:created} AND id < {:id}))", dbx.Params{"created": cursor.Created, "id": cursor.ID}))
	}
	var records []*core.Record
	if err = query.OrderBy("created DESC", "id DESC").Limit(21).All(&records); err != nil {
		return err
	}
	next := ""
	if len(records) > 20 {
		records = records[:20]
		last := records[19]
		data, _ := json.Marshal(socialCursor{last.GetString("created"), last.Id})
		next = base64.RawURLEncoding.EncodeToString(data)
	}
	items, err := commentResponses(re.App, records, locale, viewerID(re))
	if err != nil {
		return err
	}
	privateSocialResponse(re)
	return re.JSON(http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}

func LikePost(re *core.RequestEvent) error {
	return setLike(re, CollectionPost, "community_post_like", "post")
}
func LikeComment(re *core.RequestEvent) error {
	return setLike(re, CollectionComment, "community_comment_like", "comment")
}
func setLike(re *core.RequestEvent, targetCollection, likeCollection, key string) error {
	id := re.Request.URL.Query().Get("id")
	var result socialCount
	err := re.App.RunInTransaction(func(tx core.App) error {
		if targetCollection == CollectionPost {
			if err := activePost(tx, id); err != nil {
				return err
			}
		} else {
			if _, err := activeComment(tx, id); err != nil {
				return err
			}
		}
		existing, err := tx.FindRecordsByFilter(likeCollection, key+" = {:target} && author = {:author}", "", 1, 0, dbx.Params{"target": id, "author": re.Auth.Id})
		if err != nil {
			return err
		}
		if re.Request.Method == http.MethodDelete {
			if len(existing) > 0 {
				if err = tx.Delete(existing[0]); err != nil {
					return err
				}
			}
		} else if len(existing) == 0 {
			col, err := tx.FindCollectionByNameOrId(likeCollection)
			if err != nil {
				return err
			}
			record := core.NewRecord(col)
			record.Set(key, id)
			record.Set("author", re.Auth.Id)
			if err = tx.Save(record); err != nil {
				return err
			}
		}
		counts, err := socialCounts(tx, likeCollection, key, []string{id}, re.Auth.Id, "")
		if err != nil {
			return err
		}
		result = counts[id]
		return nil
	})
	if err != nil {
		return err
	}
	privateSocialResponse(re)
	return re.JSON(http.StatusOK, map[string]any{"like_count": result.Count, "liked": result.Liked > 0})
}

// All identifiers in this query are internal constants, never request input.
func socialCounts(app core.App, collection, key string, ids []string, viewer, condition string) (map[string]socialCount, error) {
	result := map[string]socialCount{}
	if len(ids) == 0 {
		return result, nil
	}
	params := dbx.Params{"viewer": viewer}
	parts := make([]string, len(ids))
	for i, id := range ids {
		name := fmt.Sprintf("target%d", i)
		parts[i] = "{:" + name + "}"
		params[name] = id
	}
	var records []socialCount
	err := app.DB().NewQuery("SELECT " + key + " AS target, COUNT(*) AS count, SUM(CASE WHEN author = {:viewer} THEN 1 ELSE 0 END) AS liked FROM " + collection + " WHERE " + key + " IN (" + strings.Join(parts, ",") + ") " + condition + " GROUP BY " + key).Bind(params).All(&records)
	for _, record := range records {
		result[record.Target] = record
	}
	return result, err
}
func authorSummaries(app core.App, records []*core.Record) (map[string]authorSummary, error) {
	ids := []any{}
	seen := map[string]bool{}
	for _, record := range records {
		for _, key := range []string{"author", "reply_to_author"} {
			id := record.GetString(key)
			if id != "" && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	result := map[string]authorSummary{}
	if len(ids) == 0 {
		return result, nil
	}
	users, err := app.FindAllRecords("user", dbx.In("id", ids...))
	if err != nil {
		return nil, err
	}
	infos, err := app.FindAllRecords("user_info", dbx.In("id", ids...))
	if err != nil {
		return nil, err
	}
	infoByID := map[string]*core.Record{}
	for _, info := range infos {
		infoByID[info.Id] = info
	}
	for _, user := range users {
		summary := authorSummary{Name: user.GetString("username")}
		if summary.Name == "" {
			summary.Name = user.Id
		}
		if user.GetBool("withdrawn") {
			summary.Name = userhandler.WithdrawnDisplayName()
		} else if info := infoByID[user.Id]; info != nil {
			if name := strings.TrimSpace(info.GetString("nickname")); name != "" {
				summary.Name = name
			}
			if avatar := info.GetString("avatar"); avatar != "" {
				summary.Avatar = "/api/files/" + info.Collection().Id + "/" + info.Id + "/" + url.PathEscape(avatar)
			}
		}
		result[user.Id] = summary
	}
	return result, nil
}
func commentResponses(app core.App, records []*core.Record, locale, viewer string) ([]map[string]any, error) {
	ids := make([]string, len(records))
	targetIDs := []any{}
	for i, record := range records {
		ids[i] = record.Id
		if id := record.GetString("reply_to"); id != "" {
			targetIDs = append(targetIDs, id)
		}
	}
	if len(targetIDs) > 0 {
		targets, err := app.FindAllRecords(CollectionComment, dbx.In("id", targetIDs...))
		if err != nil {
			return nil, err
		}
		authors := map[string]string{}
		for _, target := range targets {
			if target.GetString("status") == "active" {
				authors[target.Id] = target.GetString("author")
			}
		}
		for _, record := range records {
			record.Set("reply_to_author", authors[record.GetString("reply_to")])
		}
	}
	authors, err := authorSummaries(app, records)
	if err != nil {
		return nil, err
	}
	likes, err := socialCounts(app, "community_comment_like", "comment", ids, viewer, "")
	if err != nil {
		return nil, err
	}
	replies, err := socialCounts(app, CollectionComment, "parent", ids, "", "AND status = 'active'")
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(records))
	for _, record := range records {
		author := authors[record.GetString("author")]
		body := record.GetString("body")
		served := OriginalLocale
		translated := false
		deleted := record.GetString("status") == "deleted"
		if translationsPublic() && locale != OriginalLocale && record.GetString("translation_status") == "ready" {
			if value, ok := decodeTranslations(record)[locale]; ok {
				body = value.Body
				served = locale
				translated = true
			}
		}
		name, avatar := author.Name, author.Avatar
		authorID := record.GetString("author")
		if deleted {
			body = ""
			name = ""
			avatar = ""
			authorID = ""
			translated = false
			served = OriginalLocale
		}
		result = append(result, map[string]any{"id": record.Id, "post": record.GetString("post"), "parent": record.GetString("parent"), "reply_to": record.GetString("reply_to"), "reply_to_name": authors[record.GetString("reply_to_author")].Name, "author": authorID, "author_name": name, "author_avatar_url": avatar, "body": body, "photos": append([]string{}, record.GetStringSlice("photos")...), "deleted": deleted, "created": record.GetString("created"), "editable_until": record.GetString("editable_until"), "edited_at": record.GetString("edited_at"), "locale": served, "translated": translated, "translation_status": record.GetString("translation_status"), "like_count": likes[record.Id].Count, "liked": likes[record.Id].Liked > 0, "reply_count": replies[record.Id].Count})
	}
	return result, nil
}
func postResponses(app core.App, records []*core.Record, locale, viewer string) ([]map[string]any, error) {
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.Id
	}
	authors, err := authorSummaries(app, records)
	if err != nil {
		return nil, err
	}
	likes, err := socialCounts(app, "community_post_like", "post", ids, viewer, "")
	if err != nil {
		return nil, err
	}
	comments, err := socialCounts(app, CollectionComment, "post", ids, "", "AND status = 'active'")
	if err != nil {
		return nil, err
	}
	result := make([]map[string]any, 0, len(records))
	for _, record := range records {
		item := buildPostResponse(app, record, locale)
		author := authors[record.GetString("author")]
		item["author_name"] = author.Name
		item["author_avatar_url"] = author.Avatar
		item["like_count"] = likes[record.Id].Count
		item["liked"] = likes[record.Id].Liked > 0
		item["comment_count"] = comments[record.Id].Count
		result = append(result, item)
	}
	return result, nil
}
func servePost(re *core.RequestEvent, record *core.Record, locale string, status int) error {
	items, err := postResponses(re.App, []*core.Record{record}, locale, viewerID(re))
	if err != nil {
		return err
	}
	privateSocialResponse(re)
	return re.JSON(status, items[0])
}
