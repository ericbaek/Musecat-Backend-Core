package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Core owns the fresh-bootstrap fields used to tie a publication location
// verification to the arcade's current basic-information revision.
func init() {
	m.Register(addArcadeLocationVerification, func(app core.App) error { return nil })
}

func addArcadeLocationVerification(app core.App) error {
	arcade, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		return fmt.Errorf("find arcade: %w", err)
	}
	basics, err := app.FindCollectionByNameOrId("arcade_basic")
	if err != nil {
		return fmt.Errorf("find arcade_basic: %w", err)
	}
	users, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		return fmt.Errorf("find user: %w", err)
	}
	if err := ensureArcadeLocationVerificationFields(arcade, basics.Id, users.Id); err != nil {
		return err
	}
	if err := app.Save(arcade); err != nil {
		return fmt.Errorf("save arcade location verification fields: %w", err)
	}
	return nil
}

func ensureArcadeLocationVerificationFields(collection *core.Collection, basicID, userID string) error {
	if field := collection.Fields.GetByName("location_verification_basic"); field == nil {
		collection.Fields.Add(&core.RelationField{
			Name:         "location_verification_basic",
			CollectionId: basicID,
			MaxSelect:    1,
		})
	} else if relation, ok := field.(*core.RelationField); !ok || relation.CollectionId != basicID || relation.MaxSelect != 1 {
		return fmt.Errorf("arcade.location_verification_basic has an incompatible schema")
	}

	if field := collection.Fields.GetByName("location_verification_by"); field == nil {
		collection.Fields.Add(&core.RelationField{
			Name:         "location_verification_by",
			CollectionId: userID,
			MaxSelect:    1,
		})
	} else if relation, ok := field.(*core.RelationField); !ok || relation.CollectionId != userID || relation.MaxSelect != 1 {
		return fmt.Errorf("arcade.location_verification_by has an incompatible schema")
	}

	for _, name := range []string{
		"location_verification_at",
		"location_verification_distance_meters",
		"location_verification_accuracy_meters",
	} {
		if field := collection.Fields.GetByName(name); field == nil {
			switch name {
			case "location_verification_at":
				collection.Fields.Add(&core.DateField{Name: name})
			default:
				min := 0.0
				collection.Fields.Add(&core.NumberField{Name: name, Min: &min})
			}
		} else {
			switch typed := field.(type) {
			case *core.DateField:
				if name != "location_verification_at" {
					return fmt.Errorf("arcade.%s has an incompatible schema", name)
				}
			case *core.NumberField:
				if name == "location_verification_at" || typed.Min == nil || *typed.Min != 0 {
					return fmt.Errorf("arcade.%s has an incompatible schema", name)
				}
			default:
				return fmt.Errorf("arcade.%s has an incompatible schema", name)
			}
		}
	}
	return nil
}
