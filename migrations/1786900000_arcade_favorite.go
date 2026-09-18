package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

const arcadeFavoriteCollection = "arcade_favorite"

var arcadeFavoriteIndexes = []struct {
	name    string
	unique  bool
	columns string
}{
	{name: "idx_arcade_favorite_user_arcade", unique: true, columns: "user, arcade"},
	{name: "idx_arcade_favorite_user_created", columns: "user, created"},
}

func init() {
	m.Register(ensureArcadeFavoriteSchema, func(app core.App) error { return nil })
}

func ensureArcadeFavoriteSchema(app core.App) error {
	userInfo, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		return fmt.Errorf("find user_info: %w", err)
	}
	if userInfo.Name != "user_info" || userInfo.System || !userInfo.IsBase() {
		return fmt.Errorf("user_info must be a base collection")
	}
	if err := ensureFavoriteVisibilityField(app, userInfo); err != nil {
		return err
	}
	if _, err := app.DB().NewQuery("UPDATE user_info SET favorite_visibility = 'private' WHERE COALESCE(favorite_visibility, '') = ''").Execute(); err != nil {
		return fmt.Errorf("backfill favorite visibility: %w", err)
	}

	users, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		return fmt.Errorf("find user: %w", err)
	}
	if users.Name != "user" || !users.IsAuth() {
		return fmt.Errorf("user must be an auth collection")
	}
	arcades, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		return fmt.Errorf("find arcade: %w", err)
	}
	if arcades.Name != "arcade" || arcades.System || !arcades.IsBase() {
		return fmt.Errorf("arcade must be a base collection")
	}

	if err := ensureArcadeFavoriteCollection(app, users, arcades); err != nil {
		return err
	}
	return nil
}

func ensureFavoriteVisibilityField(app core.App, collection *core.Collection) error {
	field := collection.Fields.GetByName("favorite_visibility")
	if field == nil {
		collection.Fields.Add(&core.SelectField{Name: "favorite_visibility", Values: []string{"private", "public"}, MaxSelect: 1})
		if err := app.Save(collection); err != nil {
			return fmt.Errorf("add user_info.favorite_visibility: %w", err)
		}
		return nil
	}
	selectField, ok := field.(*core.SelectField)
	if !ok || selectField.MaxSelect != 1 || !slices.Equal(selectField.Values, []string{"private", "public"}) {
		return fmt.Errorf("user_info.favorite_visibility has an incompatible schema")
	}
	return nil
}

func ensureArcadeFavoriteCollection(app core.App, users, arcades *core.Collection) error {
	collection, err := app.FindCollectionByNameOrId(arcadeFavoriteCollection)
	isNew := false
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("find %s: %w", arcadeFavoriteCollection, err)
		}
		collection = core.NewBaseCollection(arcadeFavoriteCollection)
		isNew = true
	} else if collection.Name != arcadeFavoriteCollection || collection.System || !collection.IsBase() {
		return fmt.Errorf("%s must be a base collection", arcadeFavoriteCollection)
	}

	changed := isNew
	for _, expected := range []struct {
		name          string
		collectionID  string
		cascadeDelete bool
	}{
		{name: "user", collectionID: users.Id, cascadeDelete: true},
		{name: "arcade", collectionID: arcades.Id},
	} {
		field := collection.Fields.GetByName(expected.name)
		if field == nil {
			collection.Fields.Add(&core.RelationField{
				Name:          expected.name,
				CollectionId:  expected.collectionID,
				Required:      true,
				MaxSelect:     1,
				CascadeDelete: expected.cascadeDelete,
			})
			changed = true
			continue
		}
		relation, ok := field.(*core.RelationField)
		if !ok || relation.CollectionId != expected.collectionID || !relation.Required || relation.MaxSelect != 1 || relation.MinSelect != 0 || relation.CascadeDelete != expected.cascadeDelete {
			return fmt.Errorf("%s.%s has an incompatible schema", arcadeFavoriteCollection, expected.name)
		}
	}

	createdField := collection.Fields.GetByName("created")
	if createdField == nil {
		collection.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
		changed = true
	} else {
		created, ok := createdField.(*core.AutodateField)
		if !ok {
			return fmt.Errorf("%s.created has an incompatible schema", arcadeFavoriteCollection)
		}
		if !created.OnCreate || created.OnUpdate {
			created.OnCreate = true
			created.OnUpdate = false
			changed = true
		}
	}

	for _, index := range arcadeFavoriteIndexes {
		want := favoriteIndexExpression(index.name, index.unique, index.columns)
		if collection.GetIndex(index.name) != want {
			collection.AddIndex(index.name, index.unique, index.columns, "")
			changed = true
		}
	}
	if changed || !app.HasTable(collection.Name) {
		if err := app.Save(collection); err != nil {
			return fmt.Errorf("save %s schema: %w", arcadeFavoriteCollection, err)
		}
	}

	missingIndexes := make([]struct {
		name    string
		unique  bool
		columns string
	}, 0, len(arcadeFavoriteIndexes))
	for _, index := range arcadeFavoriteIndexes {
		matches, err := favoriteIndexExists(app, index.name, favoriteIndexExpression(index.name, index.unique, index.columns))
		if err != nil {
			return fmt.Errorf("check %s: %w", index.name, err)
		}
		if !matches {
			missingIndexes = append(missingIndexes, index)
		}
	}
	if len(missingIndexes) == 0 {
		return nil
	}

	// Rebuild only indexes whose persisted SQL is missing or incompatible. This
	// also makes a retry repair a collection row left behind by a failed save.
	for _, index := range missingIndexes {
		collection.RemoveIndex(index.name)
	}
	if err := app.Save(collection); err != nil {
		return fmt.Errorf("remove incomplete favorite indexes: %w", err)
	}
	for _, index := range missingIndexes {
		collection.AddIndex(index.name, index.unique, index.columns, "")
	}
	if err := app.Save(collection); err != nil {
		return fmt.Errorf("recreate favorite indexes: %w", err)
	}
	return nil
}

func favoriteIndexExpression(name string, unique bool, columns string) string {
	uniqueSQL := ""
	if unique {
		uniqueSQL = "UNIQUE "
	}
	return fmt.Sprintf("CREATE %sINDEX `%s` ON `%s` (%s)", uniqueSQL, name, arcadeFavoriteCollection, columns)
}

func favoriteIndexExists(app core.App, name, expected string) (bool, error) {
	var actual string
	err := app.DB().NewQuery("SELECT sql FROM sqlite_master WHERE type = 'index' AND name = {:name}").Bind(dbx.Params{"name": name}).Row(&actual)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return normalizeFavoriteIndexSQL(actual) == normalizeFavoriteIndexSQL(expected), nil
}

func normalizeFavoriteIndexSQL(value string) string {
	return strings.ToLower(strings.NewReplacer("`", "", `"`, "", "[", "", "]", "", " ", "", "\n", "", "\r", "", "\t", "").Replace(strings.TrimSpace(value)))
}
