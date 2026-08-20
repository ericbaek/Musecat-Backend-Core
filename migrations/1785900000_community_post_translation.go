package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

const communityPostCollection = "community_post"

// Core owns only the fresh-bootstrap community post contract. Backend Full
// must add the equivalent schema through its own guarded forward migration.
func init() {
	m.Register(applyCommunityPostSchema, func(app core.App) error { return nil })
}

func applyCommunityPostSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		users, err := tx.FindCollectionByNameOrId("user")
		if err != nil {
			return fmt.Errorf("find user: %w", err)
		}
		series, err := tx.FindCollectionByNameOrId("game_series")
		if err != nil {
			return fmt.Errorf("find game_series: %w", err)
		}

		posts, err := tx.FindCollectionByNameOrId(communityPostCollection)
		if err != nil {
			if !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("find %s: %w", communityPostCollection, err)
			}

			zero := float64(0)
			five := float64(5)
			posts = core.NewBaseCollection(communityPostCollection)
			posts.Fields.Add(
				&core.RelationField{Name: "author", CollectionId: users.Id, Required: true, MaxSelect: 1},
				&core.RelationField{Name: "game_series", CollectionId: series.Id, MaxSelect: 1},
				&core.TextField{Name: "title", Max: 200},
				&core.TextField{Name: "body", Required: true, Max: 10000},
				&core.SelectField{Name: "original_locale", Values: []string{"ko-KR"}, Required: true, MaxSelect: 1},
				&core.SelectField{Name: "flair", Values: []string{"achievement", "question", "tip", "news", "event", "art", "chitchat"}, Required: true, MaxSelect: 1},
				&core.SelectField{Name: "status", Values: []string{"active", "deleted"}, Required: true, MaxSelect: 1},
				&core.DateField{Name: "editable_until", Required: true},
				&core.DateField{Name: "translate_after", Required: true},
				&core.SelectField{Name: "translation_status", Values: []string{"pending", "processing", "ready", "failed"}, Required: true, MaxSelect: 1},
				&core.NumberField{Name: "translation_attempts", OnlyInt: true, Min: &zero, Max: &five},
				&core.DateField{Name: "translation_started_at"},
				&core.DateField{Name: "translated_at"},
				&core.TextField{Name: "original_hash", Max: 64},
				&core.JSONField{Name: "translations", MaxSize: 100000},
				&core.TextField{Name: "translation_error", Max: 2000},
				&core.AutodateField{Name: "created", OnCreate: true},
				&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
			)
			posts.AddIndex("idx_community_post_status_created", false, "status, created", "")
			posts.AddIndex("idx_community_post_translation_due", false, "translation_status, translate_after", "")
			posts.AddIndex("idx_community_post_author_created", false, "author, created", "")
			posts.AddIndex("idx_community_post_game_created", false, "game_series, created", "")
		} else {
			if err := validateCommunityPostCollection(posts, users, series); err != nil {
				return err
			}
		}

		posts.ListRule = nil
		posts.ViewRule = nil
		posts.CreateRule = nil
		posts.UpdateRule = nil
		posts.DeleteRule = nil
		if err := tx.Save(posts); err != nil {
			return fmt.Errorf("save %s: %w", communityPostCollection, err)
		}
		return nil
	})
}

func validateCommunityPostCollection(collection, users, series *core.Collection) error {
	if collection.Name != communityPostCollection || collection.System || !collection.IsBase() {
		return fmt.Errorf("%s must be a base collection", communityPostCollection)
	}
	if err := requireExactRelationField(collection, "author", users.Id, true, false); err != nil {
		return err
	}
	if err := requireExactRelationField(collection, "game_series", series.Id, false, false); err != nil {
		return err
	}
	for _, spec := range []struct {
		name     string
		required bool
		max      int
	}{
		{name: "title", max: 200},
		{name: "body", required: true, max: 10000},
		{name: "original_hash", max: 64},
		{name: "translation_error", max: 2000},
	} {
		if err := requireCommunityText(collection, spec.name, spec.required, spec.max); err != nil {
			return err
		}
	}
	for _, spec := range []struct {
		name   string
		values []string
	}{
		{name: "original_locale", values: []string{"ko-KR"}},
		{name: "flair", values: []string{"achievement", "question", "tip", "news", "event", "art", "chitchat"}},
		{name: "status", values: []string{"active", "deleted"}},
		{name: "translation_status", values: []string{"pending", "processing", "ready", "failed"}},
	} {
		if err := requireCommunitySelect(collection, spec.name, spec.values); err != nil {
			return err
		}
	}
	for _, name := range []string{"editable_until", "translate_after"} {
		if err := requireCommunityDate(collection, name, true); err != nil {
			return err
		}
	}
	for _, name := range []string{"translation_started_at", "translated_at"} {
		if err := requireCommunityDate(collection, name, false); err != nil {
			return err
		}
	}
	attempts, ok := collection.Fields.GetByName("translation_attempts").(*core.NumberField)
	if !ok || attempts.Required || !attempts.OnlyInt || attempts.Min == nil || *attempts.Min != 0 || attempts.Max == nil || *attempts.Max != 5 || attempts.System || attempts.Hidden || attempts.Presentable {
		return fmt.Errorf("%s.translation_attempts has incompatible number settings", communityPostCollection)
	}
	translations, ok := collection.Fields.GetByName("translations").(*core.JSONField)
	if !ok || translations.Required || translations.MaxSize != 100000 || translations.System || translations.Hidden || translations.Presentable {
		return fmt.Errorf("%s.translations has incompatible JSON settings", communityPostCollection)
	}
	if err := requireExactAutodateField(collection, "created", true, false); err != nil {
		return err
	}
	if err := requireExactAutodateField(collection, "updated", true, true); err != nil {
		return err
	}
	for _, spec := range []struct {
		name    string
		columns []string
	}{
		{name: "idx_community_post_status_created", columns: []string{"status", "created"}},
		{name: "idx_community_post_translation_due", columns: []string{"translation_status", "translate_after"}},
		{name: "idx_community_post_author_created", columns: []string{"author", "created"}},
		{name: "idx_community_post_game_created", columns: []string{"game_series", "created"}},
	} {
		if err := requireExactIndex(collection, spec.name, false, spec.columns, ""); err != nil {
			return err
		}
	}
	return requireLockedCollectionRules(collection)
}

func requireCommunityText(collection *core.Collection, name string, required bool, max int) error {
	field, ok := collection.Fields.GetByName(name).(*core.TextField)
	if !ok || field.Required != required || field.Max != max || field.Min != 0 || field.Pattern != "" || field.AutogeneratePattern != "" || field.PrimaryKey || field.System || field.Hidden || field.Presentable {
		return fmt.Errorf("%s.%s has incompatible text settings", communityPostCollection, name)
	}
	return nil
}

func requireCommunitySelect(collection *core.Collection, name string, values []string) error {
	field, ok := collection.Fields.GetByName(name).(*core.SelectField)
	if !ok || !reflect.DeepEqual(field.Values, values) || !field.Required || field.MaxSelect != 1 || field.System || field.Hidden || field.Presentable {
		return fmt.Errorf("%s.%s has incompatible select settings", communityPostCollection, name)
	}
	return nil
}

func requireCommunityDate(collection *core.Collection, name string, required bool) error {
	field, ok := collection.Fields.GetByName(name).(*core.DateField)
	if !ok || field.Required != required || !field.Min.IsZero() || !field.Max.IsZero() || field.System || field.Hidden || field.Presentable {
		return fmt.Errorf("%s.%s has incompatible date settings", communityPostCollection, name)
	}
	return nil
}
