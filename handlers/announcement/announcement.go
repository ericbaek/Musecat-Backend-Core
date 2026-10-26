package announcement

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/service/announcement"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func RegisterRoutes(se *core.ServeEvent) {
	se.Router.GET("/announcements", List)
	se.Router.GET("/announcements/unseen", Unseen).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser())
	se.Router.GET("/announcements/{id}", Get)
	se.Router.GET("/announcements/{id}/images/{filename}", Image)
	se.Router.POST("/announcements/{id}/view", View).Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser(), apis.BodyLimit(4096))
	staff := se.Router.Group("/moderation/announcements").Bind(apis.RequireAuth("user"), userhandler.RequireActiveUser(), arcadequery.RequireStrictReviewerAccess())
	staff.GET("", ListAdmin)
	staff.POST("", Create).Bind(apis.BodyLimit(announcement.MaxBodyBytes))
	staff.PUT("/{id}", Update).Bind(apis.BodyLimit(announcement.MaxBodyBytes))
	staff.POST("/{id}/publish", Publish).Bind(apis.BodyLimit(4096))
	staff.POST("/{id}/archive", Archive).Bind(apis.BodyLimit(4096))
}

func List(re *core.RequestEvent) error      { return list(re, false) }
func ListAdmin(re *core.RequestEvent) error { return list(re, true) }
func list(re *core.RequestEvent, admin bool) error {
	filter, order, field := "status='published'", "-published_at,-id", "published_at"
	if admin {
		filter, order, field = "", "-created,-id", "created"
	}
	params := dbx.Params{}
	if !admin {
		locale, err := requestedLocale(re)
		if err != nil {
			return err
		}
		filter += " && locales ~ {:locale}"
		params["locale"] = locale
	}
	if before := re.Request.URL.Query().Get("before"); before != "" {
		cursor, err := re.App.FindRecordById(announcement.Collection, before)
		if err != nil || (!admin && cursor.GetString("published_at") == "") {
			return re.BadRequestError("invalid announcement cursor", nil)
		}
		if filter != "" {
			filter += " && "
		}
		filter += "(" + field + "<{:before} || (" + field + "={:before} && id<{:id}))"
		params["before"], params["id"] = cursor.GetString(field), cursor.Id
	}
	rows, err := re.App.FindRecordsByFilter(announcement.Collection, filter, order, 21, 0, params)
	if err != nil {
		return err
	}
	next := ""
	if len(rows) > 20 {
		rows = rows[:20]
		next = rows[19].Id
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		seen, err := viewed(re.App, re.Auth, row.Id)
		if err != nil {
			return err
		}
		if admin {
			items = append(items, announcement.Item(row, seen))
		} else {
			items = append(items, announcement.PublicItem(row, seen))
		}
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(http.StatusOK, map[string]any{"items": items, "next": next})
}

func Get(re *core.RequestEvent) error {
	locale, err := requestedLocale(re)
	if err != nil {
		return err
	}
	row, err := readable(re)
	if err != nil {
		return err
	}
	seen, err := viewed(re.App, re.Auth, row.Id)
	if err != nil {
		return err
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	if row.GetString("status") == "published" {
		if !announcement.HasLocale(row, locale) {
			return re.NotFoundError("announcement not available in this locale", nil)
		}
		return re.JSON(http.StatusOK, announcement.PublicItem(row, seen))
	}
	return re.JSON(http.StatusOK, announcement.Item(row, seen))
}

func Unseen(re *core.RequestEvent) error {
	locale, err := requestedLocale(re)
	if err != nil {
		return err
	}
	rows, err := re.App.FindRecordsByFilter(announcement.Collection, "status='published' && locales ~ {:locale}", "-published_at,-id", 1, 0, dbx.Params{"locale": locale})
	if err != nil {
		return err
	}
	var item any
	if len(rows) > 0 {
		seen, err := viewed(re.App, re.Auth, rows[0].Id)
		if err != nil {
			return err
		}
		if !seen {
			item = announcement.PublicItem(rows[0], false)
		}
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(http.StatusOK, map[string]any{"announcement": item})
}

func View(re *core.RequestEvent) error {
	err := re.App.RunInTransaction(func(tx core.App) error {
		row, err := tx.FindRecordById(announcement.Collection, re.Request.PathValue("id"))
		if err != nil || row.GetString("status") != "published" {
			return re.NotFoundError("announcement not found", nil)
		}
		seen, err := viewed(tx, re.Auth, row.Id)
		if err != nil || seen {
			return err
		}
		col, err := tx.FindCollectionByNameOrId(announcement.Views)
		if err != nil {
			return err
		}
		view := core.NewRecord(col)
		view.Set("user", re.Auth.Id)
		view.Set("announcement", row.Id)
		return tx.Save(view)
	})
	if err != nil {
		return err
	}
	return re.JSON(http.StatusOK, map[string]bool{"ok": true})
}

func Create(re *core.RequestEvent) error { return save(re, false) }
func Update(re *core.RequestEvent) error { return save(re, true) }
func save(re *core.RequestEvent, update bool) error {
	defer func() {
		if re.Request.MultipartForm != nil {
			_ = re.Request.MultipartForm.RemoveAll()
		}
	}()
	input, err := decode(re)
	if err != nil {
		return err
	}
	var result *core.Record
	err = re.App.RunInTransaction(func(tx core.App) error {
		var row *core.Record
		if update {
			row, err = tx.FindRecordById(announcement.Collection, re.Request.PathValue("id"))
			if err != nil {
				return re.NotFoundError("announcement not found", nil)
			}
			if row.GetString("status") == "archived" {
				return re.Error(http.StatusConflict, "archived announcements cannot be edited", nil)
			}
		} else {
			col, err := tx.FindCollectionByNameOrId(announcement.Collection)
			if err != nil {
				return err
			}
			row = core.NewRecord(col)
			row.Set("status", "draft")
			row.Set("created_by", re.Auth.Id)
		}
		if err := applyInput(row, input); err != nil {
			return re.BadRequestError(err.Error(), nil)
		}
		if row.GetString("status") == "published" {
			if err := announcement.ValidatePublished(row, row.GetStringSlice("locales")); err != nil {
				return re.BadRequestError(err.Error(), nil)
			}
		}
		if err := tx.Save(row); err != nil {
			return err
		}
		result = row
		return nil
	})
	if err != nil {
		return err
	}
	status := http.StatusCreated
	if update {
		status = http.StatusOK
	}
	return re.JSON(status, announcement.Item(result, false))
}

func Publish(re *core.RequestEvent) error { return changeStatus(re, "published") }
func Archive(re *core.RequestEvent) error { return changeStatus(re, "archived") }
func changeStatus(re *core.RequestEvent, status string) error {
	var locales []string
	var sendPush *bool
	if status == "published" {
		var body struct {
			Locales  []string `json:"locales"`
			SendPush *bool    `json:"sendPush"`
		}
		if err := re.BindBody(&body); err != nil {
			return re.BadRequestError("publication locales are required", err)
		}
		sendPush = body.SendPush
		if len(body.Locales) == 0 || len(body.Locales) > len(announcement.Locales) {
			return re.BadRequestError("select at least one supported locale", nil)
		}
		unique := map[string]bool{}
		for _, locale := range body.Locales {
			if !validLocale(locale) || unique[locale] {
				return re.BadRequestError("publication locales must be supported and unique", nil)
			}
			unique[locale] = true
		}
		for _, locale := range announcement.Locales {
			if unique[locale] {
				locales = append(locales, locale)
			}
		}
	}
	var result *core.Record
	err := re.App.RunInTransaction(func(tx core.App) error {
		row, err := tx.FindRecordById(announcement.Collection, re.Request.PathValue("id"))
		if err != nil {
			return re.NotFoundError("announcement not found", nil)
		}
		if row.GetString("status") == status {
			if status == "published" && !slices.Equal(row.GetStringSlice("locales"), locales) {
				return re.Error(http.StatusConflict, "published locales cannot change", nil)
			}
			result = row
			return nil
		}
		if status == "published" {
			if row.GetString("status") != "draft" {
				return re.Error(http.StatusConflict, "archived announcements cannot be published", nil)
			}
			if err := announcement.ValidatePublished(row, locales); err != nil {
				return re.BadRequestError(err.Error(), nil)
			}
			row.Set("suppress_device_push", sendPush != nil && !*sendPush)
			row.Set("published_at", time.Now().UTC())
			row.Set("locales", locales)
		}
		row.Set("status", status)
		if err := tx.Save(row); err != nil {
			return err
		}
		result = row
		return nil
	})
	if err != nil {
		return err
	}
	return re.JSON(http.StatusOK, announcement.Item(result, false))
}

func Image(re *core.RequestEvent) error {
	row, err := readable(re)
	if err != nil {
		return err
	}
	filename := re.Request.PathValue("filename")
	if !slices.Contains(row.GetStringSlice("images"), filename) {
		return re.NotFoundError("image not found", nil)
	}
	if !announcement.PublicImage(row, filename) {
		staff, err := userhandler.HasActiveStaffAccess(re.App, re.Auth)
		if err != nil {
			return err
		}
		if !staff {
			return re.NotFoundError("image not found", nil)
		}
	}
	fs, err := re.App.NewFilesystem()
	if err != nil {
		return err
	}
	defer fs.Close()
	re.Response.Header().Set("Cache-Control", "private, no-store")
	re.Response.Header().Set("X-Content-Type-Options", "nosniff")
	if err := fs.Serve(re.Response, re.Request, row.BaseFilesPath()+"/"+filename, filename); err != nil {
		return re.NotFoundError("image not found", err)
	}
	return nil
}

func readable(re *core.RequestEvent) (*core.Record, error) {
	row, err := re.App.FindRecordById(announcement.Collection, re.Request.PathValue("id"))
	if err != nil {
		return nil, re.NotFoundError("announcement not found", nil)
	}
	staff := false
	if row.GetString("status") != "published" {
		staff, err = userhandler.HasActiveStaffAccess(re.App, re.Auth)
		if err != nil {
			return nil, err
		}
	}
	if row.GetString("status") != "published" && !staff {
		return nil, re.NotFoundError("announcement not found", nil)
	}
	return row, nil
}

func viewed(app core.App, user *core.Record, id string) (bool, error) {
	if user == nil {
		return false, nil
	}
	_, err := app.FindFirstRecordByFilter(announcement.Views, "user={:user} && announcement={:announcement}", dbx.Params{"user": user.Id, "announcement": id})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func validLocale(locale string) bool {
	return slices.Contains(announcement.Locales, strings.TrimSpace(locale)) && strings.TrimSpace(locale) == locale
}

func requestedLocale(re *core.RequestEvent) (string, error) {
	locale := re.Request.URL.Query().Get("locale")
	if locale == "" {
		return "ko", nil
	}
	if !validLocale(locale) {
		return "", re.BadRequestError("unsupported locale", nil)
	}
	return locale, nil
}
