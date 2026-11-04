package checknote

import (
	"encoding/json"
	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/ericbaek/musecat-backend-core/service/arcadechecknote"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

type body struct {
	Arcade     string   `json:"arcade"`
	ID         string   `json:"id"`
	Message    string   `json:"message"`
	Answer     string   `json:"answer"`
	Lat        *float64 `json:"lat"`
	Lon        *float64 `json:"lon"`
	Accuracy   *float64 `json:"accuracy"`
	CapturedAt int64    `json:"captured_at"`
}

func readable(re *core.RequestEvent, arcade *core.Record) bool {
	return arcade.GetBool("public") || (re.Auth != nil && !re.Auth.GetBool("withdrawn") && arcadeinternal.CanWriteArcade(re.Auth, arcade))
}
func person(app core.App, id string) map[string]string {
	user, err := app.FindRecordById("user", id)
	if err != nil || user.GetBool("withdrawn") {
		return map[string]string{"id": "", "name": ""}
	}
	name := user.GetString("nickname")
	if name == "" {
		name = user.GetString("username")
	}
	return map[string]string{"id": user.Id, "name": name}
}
func payload(app core.App, note *core.Record) map[string]any {
	return map[string]any{"id": note.Id, "arcade": note.GetString("arcade"), "message": note.GetString("message"), "author": person(app, note.GetString("author")), "answer": note.GetString("answer"), "answeredBy": person(app, note.GetString("answered_by")), "completed": note.GetBool("completed"), "created": note.GetString("created"), "answeredAt": note.GetString("answered_at")}
}
func List(re *core.RequestEvent) error {
	re.Response.Header().Set("Cache-Control", "private, no-store")
	id := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	arcade, err := re.App.FindRecordById("arcade", id)
	if err != nil || !readable(re, arcade) {
		return re.NotFoundError("arcade not found", nil)
	}
	notes, err := re.App.FindRecordsByFilter(arcadechecknote.Collection, "arcade={:arcade}", "completed,-created", 100, 0, dbx.Params{"arcade": id})
	if err != nil {
		return re.InternalServerError("failed to load check notes", nil)
	}
	items := make([]map[string]any, 0, len(notes))
	for _, note := range notes {
		items = append(items, payload(re.App, note))
	}
	return re.JSON(http.StatusOK, map[string]any{"items": items})
}
func Mutate(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.UnauthorizedError("authentication required", nil)
	}
	var input body
	if err := json.NewDecoder(re.Request.Body).Decode(&input); err != nil {
		return re.BadRequestError("invalid JSON body", nil)
	}
	input.Arcade = strings.TrimSpace(input.Arcade)
	input.Message = strings.TrimSpace(input.Message)
	input.Answer = strings.TrimSpace(input.Answer)
	var result map[string]any
	err := re.App.RunInTransaction(func(tx core.App) error {
		arcade, err := tx.FindRecordById("arcade", input.Arcade)
		if err != nil || !readable(re, arcade) {
			return re.NotFoundError("arcade not found", nil)
		}
		if !arcadeinternal.CanWriteArcade(re.Auth, arcade) {
			return re.ForbiddenError("arcade writing is not permitted", nil)
		}
		if re.Request.Method == http.MethodPost {
			if utf8.RuneCountInString(input.Message) < 1 || utf8.RuneCountInString(input.Message) > 1000 {
				return re.BadRequestError("message must contain 1 to 1000 characters", nil)
			}
			existing, err := tx.FindRecordsByFilter(arcadechecknote.Collection, "arcade={:arcade} && completed=false", "", 50, 0, dbx.Params{"arcade": input.Arcade})
			if err != nil {
				return err
			}
			for _, note := range existing {
				if note.GetString("author") == re.Auth.Id && note.GetString("message") == input.Message {
					result = payload(tx, note)
					return nil
				}
			}
			if len(existing) >= 50 {
				return re.TooManyRequestsError("too many open check notes", nil)
			}
			col, err := tx.FindCollectionByNameOrId(arcadechecknote.Collection)
			if err != nil {
				return err
			}
			note := core.NewRecord(col)
			note.Set("arcade", arcade.Id)
			note.Set("author", re.Auth.Id)
			note.Set("message", input.Message)
			if err := tx.Save(note); err != nil {
				return err
			}
			result = payload(tx, note)
			return nil
		}
		note, err := tx.FindRecordById(arcadechecknote.Collection, input.ID)
		if err != nil || note.GetString("arcade") != arcade.Id {
			return re.NotFoundError("check note not found", nil)
		}
		if re.Request.Method == http.MethodDelete {
			if note.GetString("author") != re.Auth.Id && !isReviewer(re.Auth) {
				return re.ForbiddenError("only the author or a moderator can remove this note", nil)
			}
			return tx.Delete(note)
		}
		if note.GetBool("completed") {
			return apis.NewApiError(http.StatusConflict, "check note already completed", nil)
		}
		if utf8.RuneCountInString(input.Answer) < 1 || utf8.RuneCountInString(input.Answer) > 1000 {
			return re.BadRequestError("answer must contain 1 to 1000 characters", nil)
		}
		if input.Lat == nil || input.Lon == nil || input.Accuracy == nil || arcadeinternal.ValidateLocationCoords(*input.Lat, *input.Lon) != nil || math.IsNaN(*input.Accuracy) || math.IsInf(*input.Accuracy, 0) || *input.Accuracy < 0 || *input.Accuracy > 150 {
			return re.BadRequestError("a GPS fix with accuracy within 150 meters is required", nil)
		}
		age := time.Now().UnixMilli() - input.CapturedAt
		if age < -5000 || age > 60000 {
			return re.BadRequestError("a fresh GPS fix is required", nil)
		}
		basic, err := tx.FindRecordById("arcade_basic", arcade.GetString("basic"))
		if err != nil {
			return apis.NewApiError(http.StatusConflict, "arcade location is unavailable", nil)
		}
		lat, lon, ok := arcadeinternal.ReadLocation(basic.Get("location"))
		if !ok || arcadeinternal.ValidateLocationCoords(lat, lon) != nil {
			return apis.NewApiError(http.StatusConflict, "arcade location is unavailable", nil)
		}
		if arcadeinternal.DistanceKm(*input.Lat, *input.Lon, lat, lon)*1000 > 300 {
			return re.ForbiddenError("check notes can only be completed within 300 meters of the arcade", nil)
		}
		note.Set("answer", input.Answer)
		note.Set("answered_by", re.Auth.Id)
		note.Set("completed", true)
		note.Set("answered_at", time.Now().UTC())
		if err := tx.Save(note); err != nil {
			return err
		}
		result = payload(tx, note)
		return nil
	})
	if err != nil {
		return err
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	if re.Request.Method == http.MethodDelete {
		return re.NoContent(http.StatusNoContent)
	}
	return re.JSON(http.StatusOK, result)
}
func isReviewer(user *core.Record) bool {
	for _, field := range []string{"tag", "tags"} {
		for _, tag := range user.GetStringSlice(field) {
			if tag == "developer" || tag == "moderator" {
				return true
			}
		}
	}
	return false
}
