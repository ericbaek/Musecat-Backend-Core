package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	noticehandler "github.com/ericbaek/musecat-backend-core/handlers/arcade/notice"
)

func init() {
	m.Register(repairArcadeNoticeDocuments, func(app core.App) error { return nil })
}

// The first document migration added the field before persisting the updated
// collection schema. PocketBase then ignored document values while saving the
// legacy records. This migration repeats the data step after the schema exists.
func repairArcadeNoticeDocuments(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		notices, err := tx.FindCollectionByNameOrId("arcade_notice")
		if err != nil {
			return fmt.Errorf("find arcade_notice for document repair: %w", err)
		}
		document, hasDocument := notices.Fields.GetByName("document").(*core.JSONField)
		message := notices.Fields.GetByName("message")
		if !hasDocument {
			document = &core.JSONField{Name: "document", MaxSize: 200000}
			notices.Fields.Add(document)
		}

		if message != nil {
			document.Required = false
			if err := tx.Save(notices); err != nil {
				return fmt.Errorf("save arcade_notice schema before document repair: %w", err)
			}
			records, err := tx.FindAllRecords("arcade_notice")
			if err != nil {
				return fmt.Errorf("list arcade notices for document repair: %w", err)
			}
			for _, record := range records {
				record.Set("document", noticehandler.LegacyMarkdownToDocument(record.GetString("message")))
				if err := tx.Save(record); err != nil {
					return fmt.Errorf("repair arcade_notice %s: %w", record.Id, err)
				}
			}
			notices.Fields.RemoveByName("message")
			document.Required = true
			if err := tx.Save(notices); err != nil {
				return fmt.Errorf("save repaired arcade_notice schema: %w", err)
			}
			return nil
		}

		records, err := tx.FindAllRecords("arcade_notice")
		if err != nil {
			return fmt.Errorf("list arcade notices for document validation: %w", err)
		}
		for _, record := range records {
			if record.Get("document") == nil {
				return fmt.Errorf("arcade_notice %s has no document and no legacy message to repair", record.Id)
			}
		}
		document.Required = true
		if err := tx.Save(notices); err != nil {
			return fmt.Errorf("require repaired arcade_notice document: %w", err)
		}
		return nil
	})
}
