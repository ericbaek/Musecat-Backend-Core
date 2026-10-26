package supportfeedback

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"strings"
)

const Messages = "support_feedback_message"

// Matches the existing strict support-feedback review policy.
func IsStaff(user *core.Record) bool {
	if user == nil || user.GetBool("withdrawn") {
		return false
	}
	for _, field := range []string{"tags", "tag"} {
		for _, tag := range user.GetStringSlice(field) {
			switch strings.ToLower(strings.TrimSpace(tag)) {
			case "developer", "moderator":
				return true
			}
		}
	}
	return false
}
func CanAccess(app core.App, user, feedback *core.Record) bool {
	return user != nil && !user.GetBool("withdrawn") && (feedback.GetString("createdBy") == user.Id || IsStaff(user) || IsOfficial(app, user, feedback))
}
func EnsureSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		feedback, err := tx.FindCollectionByNameOrId("support_feedback")
		if err != nil {
			return err
		}
		user, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		col, err := tx.FindCollectionByNameOrId(Messages)
		if errors.Is(err, sql.ErrNoRows) {
			col = core.NewBaseCollection(Messages)
		} else if err != nil {
			return err
		} else if !col.IsBase() || col.System {
			return fmt.Errorf("incompatible %s", Messages)
		}
		fields := []core.Field{
			&core.RelationField{Name: "feedback", CollectionId: feedback.Id, Required: true, MaxSelect: 1, CascadeDelete: true},
			&core.RelationField{Name: "author", CollectionId: user.Id, Required: true, MaxSelect: 1, CascadeDelete: false},
			&core.BoolField{Name: "staff"}, &core.BoolField{Name: "official"}, &core.TextField{Name: "message", Max: 4000},
			&core.FileField{Name: "photos", MaxSelect: 3, MaxSize: 15_000_000, Protected: true},
			&core.AutodateField{Name: "created", OnCreate: true},
		}
		for _, field := range fields {
			old := col.Fields.GetByName(field.GetName())
			if old == nil {
				col.Fields.Add(field)
				continue
			}
			if old.Type() != field.Type() {
				return fmt.Errorf("incompatible %s.%s", Messages, field.GetName())
			}
			if want, ok := field.(*core.RelationField); ok {
				got := old.(*core.RelationField)
				if got.CollectionId != want.CollectionId || (got.Required != want.Required && !(field.GetName() == "author" && !got.Required)) || got.CascadeDelete != want.CascadeDelete || got.MaxSelect != 1 {
					return fmt.Errorf("incompatible message relation")
				}
			}
		}
		col.ListRule = nil
		col.ViewRule = nil
		col.CreateRule = nil
		col.UpdateRule = nil
		col.DeleteRule = nil
		col.AddIndex("idx_feedback_messages", false, "feedback,created,id", "")
		return tx.Save(col)
	})
}

func IsOfficial(app core.App, user, feedback *core.Record) bool {
	if user == nil || user.GetBool("withdrawn") || feedback.GetString("arcade") == "" {
		return false
	}
	id := feedback.GetString("arcade")
	arcade, err := app.FindRecordById("arcade", id)
	if err != nil {
		return false
	}
	for _, owned := range user.GetStringSlice("owns") {
		if owned == id {
			return arcadereport.CanManage(user, arcade)
		}
	}
	return false
}
func EnsureArcadeSchema(app core.App) error {
	if err := EnsureSchema(app); err != nil {
		return err
	}
	return app.RunInTransaction(func(tx core.App) error {
		arcade, err := tx.FindCollectionByNameOrId("arcade")
		if err != nil {
			return err
		}
		feedback, err := tx.FindCollectionByNameOrId("support_feedback")
		if err != nil {
			return err
		}
		old := feedback.Fields.GetByName("arcade")
		if old == nil {
			feedback.Fields.Add(&core.RelationField{Name: "arcade", CollectionId: arcade.Id, MaxSelect: 1})
		} else {
			field, ok := old.(*core.RelationField)
			if !ok || field.CollectionId != arcade.Id || field.MaxSelect != 1 || field.Required || field.CascadeDelete {
				return fmt.Errorf("incompatible support feedback arcade relation")
			}
		}
		feedback.AddIndex("idx_feedback_arcade", false, "arcade,updated", "")
		return tx.Save(feedback)
	})
}

// Responders narrows candidates in the database, then applies exact current role/ownership policy.
func Responders(app core.App, feedback *core.Record) ([]*core.Record, error) {
	col, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		return nil, err
	}
	filters := []string{}
	params := dbx.Params{}
	for _, field := range []string{"tags", "tag"} {
		if col.Fields.GetByName(field) != nil {
			filters = append(filters, field+" ~ 'developer'", field+" ~ 'moderator'")
		}
	}
	if id := feedback.GetString("arcade"); id != "" && col.Fields.GetByName("owns") != nil {
		filters = append(filters, "owns ~ {:arcade}")
		params["arcade"] = id
	}
	if len(filters) == 0 {
		return nil, nil
	}
	users, err := app.FindRecordsByFilter("user", strings.Join(filters, " || "), "", 0, 0, params)
	if err != nil {
		return nil, err
	}
	result := []*core.Record{}
	for _, user := range users {
		if IsStaff(user) || IsOfficial(app, user, feedback) {
			result = append(result, user)
		}
	}
	return result, nil
}
