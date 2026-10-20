package migrations

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// This repository owns its migration history; Full never imports Core bootstrap migrations.
func init() { m.Register(applyCommunitySocialSchema, func(core.App) error { return nil }) }

func applyCommunitySocialSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		users, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return err
		}
		posts, err := tx.FindCollectionByNameOrId("community_post")
		if err != nil {
			return err
		}
		comments, err := tx.FindCollectionByNameOrId("community_comment")
		fresh := errors.Is(err, sql.ErrNoRows)
		if err != nil && !fresh {
			return err
		}
		if fresh {
			comments = core.NewBaseCollection("community_comment")
			if err = tx.Save(comments); err != nil {
				return err
			}
		}
		relation := func(name, id string, required bool) core.Field {
			return &core.RelationField{Name: name, CollectionId: id, Required: required, MaxSelect: 1}
		}
		zero, five := float64(0), float64(5)
		fields := []core.Field{
			relation("post", posts.Id, true), relation("author", users.Id, true), relation("parent", comments.Id, false), relation("reply_to", comments.Id, false),
			&core.TextField{Name: "body", Max: 2000},
			&core.SelectField{Name: "status", Values: []string{"active", "deleted"}, Required: true, MaxSelect: 1},
			&core.DateField{Name: "editable_until", Required: true}, &core.DateField{Name: "edited_at"},
			&core.DateField{Name: "translate_after", Required: true},
			&core.SelectField{Name: "translation_status", Values: []string{"pending", "processing", "ready", "failed"}, Required: true, MaxSelect: 1},
			&core.NumberField{Name: "translation_attempts", OnlyInt: true, Min: &zero, Max: &five},
			&core.DateField{Name: "translation_started_at"}, &core.DateField{Name: "translated_at"},
			&core.TextField{Name: "original_hash", Max: 64}, &core.JSONField{Name: "translations", MaxSize: 100000}, &core.TextField{Name: "translation_error", Max: 2000},
			&core.AutodateField{Name: "created", OnCreate: true}, &core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		}
		if err = socialFields(comments, fields, fresh); err != nil {
			return err
		}
		comments.AddIndex("idx_community_comment_thread", false, "post, parent, created, id", "")
		comments.AddIndex("idx_community_comment_due", false, "status, translation_status, translate_after", "")
		if err = tx.Save(comments); err != nil {
			return err
		}
		for _, spec := range []struct{ name, target, collection string }{{"community_post_like", "post", posts.Id}, {"community_comment_like", "comment", comments.Id}} {
			col, err := tx.FindCollectionByNameOrId(spec.name)
			fresh = errors.Is(err, sql.ErrNoRows)
			if err != nil && !fresh {
				return err
			}
			if fresh {
				col = core.NewBaseCollection(spec.name)
			}
			if err = socialFields(col, []core.Field{relation(spec.target, spec.collection, true), relation("author", users.Id, true)}, fresh); err != nil {
				return err
			}
			col.AddIndex("idx_"+spec.name+"_unique", true, spec.target+", author", "")
			if err = tx.Save(col); err != nil {
				return err
			}
		}
		return nil
	})
}

func socialFields(col *core.Collection, fields []core.Field, fresh bool) error {
	if col.Type != core.CollectionTypeBase {
		return fmt.Errorf("%s must be a base collection", col.Name)
	}
	if col.ListRule != nil || col.ViewRule != nil || col.CreateRule != nil || col.UpdateRule != nil || col.DeleteRule != nil {
		return fmt.Errorf("%s must have locked rules", col.Name)
	}
	for _, field := range fields {
		if fresh {
			col.Fields.Add(field)
			continue
		}
		actual := col.Fields.GetByName(field.GetName())
		if actual == nil {
			return fmt.Errorf("%s.%s is missing", col.Name, field.GetName())
		}
		normalize := func(value core.Field) map[string]any {
			data, _ := json.Marshal(value)
			var out map[string]any
			_ = json.Unmarshal(data, &out)
			delete(out, "id")
			return out
		}
		if !reflect.DeepEqual(normalize(actual), normalize(field)) {
			return fmt.Errorf("%s.%s is incompatible", col.Name, field.GetName())
		}
	}
	// Never replace an incompatible unique index on an existing database.
	if !fresh && strings.HasSuffix(col.Name, "_like") {
		key := "post"
		if col.Name == "community_comment_like" {
			key = "comment"
		}
		expected := core.NewBaseCollection(col.Name)
		expected.AddIndex("idx_"+col.Name+"_unique", true, key+", author", "")
		found := false
		for _, index := range col.Indexes {
			if index == expected.Indexes[0] {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%s unique index is missing or incompatible", col.Name)
		}
	}
	return nil
}
