package messaging

import (
	"database/sql"
	"errors"
	"fmt"
	"github.com/pocketbase/pocketbase/core"
)

// RetireEmptyFeedbackSchema is for Core's fresh bootstrap only. It refuses all
// existing source data; Full owns verification and migration of populated stores.
func RetireEmptyFeedbackSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		targets := map[string]bool{}
		for _, name := range []string{"support_feedback_message", "support_feedback"} {
			col, err := tx.FindCollectionByNameOrId(name)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			count, err := tx.CountRecords(col)
			if err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("Core cannot retire populated %s; use Full migration", name)
			}
			targets[col.Id] = true
		}
		cols, err := tx.FindAllCollections()
		if err != nil {
			return err
		}
		for _, col := range cols {
			if targets[col.Id] {
				continue
			}
			names := []string{}
			for _, f := range col.Fields {
				if rel, ok := f.(*core.RelationField); ok && targets[rel.CollectionId] {
					if col.Name != "user_notification" || (f.GetName() != "feedback" && f.GetName() != "feedback_message") {
						return fmt.Errorf("unexpected legacy relation %s.%s", col.Name, f.GetName())
					}
					names = append(names, f.GetName())
				}
			}
			if len(names) > 0 {
				for _, name := range names {
					col.Fields.RemoveByName(name)
				}
				if err := tx.Save(col); err != nil {
					return err
				}
			}
		}
		for _, name := range []string{"support_feedback_message", "support_feedback"} {
			col, err := tx.FindCollectionByNameOrId(name)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if err := tx.Delete(col); err != nil {
				return err
			}
		}
		return nil
	})
}
