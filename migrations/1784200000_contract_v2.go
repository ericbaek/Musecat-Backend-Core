package migrations

import (
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

const publicPhotoAtomViewRule = "public = true && arcade.public = true"

var arcadeDomainCollections = []string{
	"arcade",
	"arcade_basic",
	"arcade_changelog",
	"arcade_flag",
	"arcade_flag_reaction",
	"arcade_game",
	"arcade_game_atoms",
	"arcade_gtk",
	"arcade_gtk_atoms",
	"arcade_hour",
	"arcade_notice",
	"arcade_photo",
	"arcade_photo_atoms",
	"arcade_request_admin",
	"arcade_sns",
	"arcade_sns_atoms",
	"arcade_visit",
}

// This migration deliberately contains only forward, guarded schema changes.
// Backend-Full has a matching local migration because importing Core's bootstrap
// migration into an existing Full installation would attempt to recreate its schema.
func init() {
	m.Register(func(app core.App) error {
		if err := lockArcadeRawREST(app); err != nil {
			return err
		}
		return ensureArcadeReviewFields(app)
	}, func(app core.App) error {
		// Contract v2 is a deliberate breaking cutover. Restoring unsafe raw REST
		// rules during a rollback would violate the security boundary.
		return nil
	})
}

func lockArcadeRawREST(app core.App) error {
	for _, name := range arcadeDomainCollections {
		collection, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			// Full installations can be behind Core on optional collections. The
			// migration remains forward-compatible and locks every collection present.
			continue
		}

		collection.ListRule = nil
		collection.CreateRule = nil
		collection.UpdateRule = nil
		collection.DeleteRule = nil
		collection.ViewRule = nil
		if name == "arcade_photo_atoms" {
			// PocketBase uses the record view rule to authorize public file delivery.
			// This is the only raw-record exception; list and mutation rules remain nil.
			collection.ViewRule = types.Pointer(publicPhotoAtomViewRule)
			photoField, ok := collection.Fields.GetByName("photo").(*core.FileField)
			if !ok {
				return fmt.Errorf("arcade_photo_atoms.photo must be a file field")
			}
			// Without Protected, PocketBase serves files solely by an unguessable
			// filename and never evaluates the view rule. Protected makes the
			// narrow public view rule authoritative for direct file delivery.
			photoField.Protected = true
		}

		if err := app.Save(collection); err != nil {
			return fmt.Errorf("lock raw REST for %s: %w", name, err)
		}
	}

	return nil
}

func ensureArcadeReviewFields(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("arcade_request_admin")
	if err != nil {
		return fmt.Errorf("find arcade_request_admin: %w", err)
	}
	users, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		return fmt.Errorf("find user collection: %w", err)
	}
	changelog, err := app.FindCollectionByNameOrId("arcade_changelog")
	if err != nil {
		return fmt.Errorf("find arcade_changelog: %w", err)
	}

	if collection.Fields.GetByName("kind") == nil {
		collection.Fields.Add(&core.SelectField{
			Name:      "kind",
			Values:    []string{"general", "edit_report", "rollback_report"},
			MaxSelect: 1,
		})
	}
	if collection.Fields.GetByName("changelog") == nil {
		collection.Fields.Add(&core.RelationField{
			Name:         "changelog",
			CollectionId: changelog.Id,
			MaxSelect:    1,
		})
	}
	if collection.Fields.GetByName("reported_editor") == nil {
		collection.Fields.Add(&core.RelationField{
			Name:         "reported_editor",
			CollectionId: users.Id,
			MaxSelect:    1,
		})
	}
	if collection.Fields.GetByName("reviewed_by") == nil {
		collection.Fields.Add(&core.RelationField{
			Name:         "reviewed_by",
			CollectionId: users.Id,
			MaxSelect:    1,
		})
	}
	if collection.Fields.GetByName("reviewed_at") == nil {
		collection.Fields.Add(&core.DateField{Name: "reviewed_at"})
	}
	if collection.Fields.GetByName("review_outcome") == nil {
		collection.Fields.Add(&core.SelectField{
			Name:      "review_outcome",
			Values:    []string{"upheld", "dismissed", "actioned"},
			MaxSelect: 1,
		})
	}
	if collection.Fields.GetByName("review_note") == nil {
		collection.Fields.Add(&core.TextField{Name: "review_note", Max: 1200})
	}

	const unresolvedReportIndex = "idx_arcade_request_admin_open_changelog_report"
	if !strings.Contains(collection.GetIndex(unresolvedReportIndex), unresolvedReportIndex) {
		collection.AddIndex(
			unresolvedReportIndex,
			true,
			"arcade, changelog",
			"changelog != '' AND kind != 'general' AND status != 'done'",
		)
	}

	if err := app.Save(collection); err != nil {
		return fmt.Errorf("update arcade_request_admin contract fields: %w", err)
	}

	return nil
}
