package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	noticehandler "github.com/ericbaek/musecat-backend-core/handlers/arcade/notice"
)

func init() {
	m.Register(migrateArcadeNoticeDocument, func(app core.App) error { return nil })
}

func migrateArcadeNoticeDocument(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		notices, err := tx.FindCollectionByNameOrId("arcade_notice")
		if err != nil {
			return fmt.Errorf("find arcade_notice: %w", err)
		}
		document, hasDocument := notices.Fields.GetByName("document").(*core.JSONField)
		message := notices.Fields.GetByName("message")
		if !hasDocument && message == nil {
			return fmt.Errorf("arcade_notice requires either message or document during migration")
		}
		if !hasDocument {
			document = &core.JSONField{Name: "document", MaxSize: 200000}
			notices.Fields.Add(document)
		}

		if message != nil {
			document.Required = false
			if err := tx.Save(notices); err != nil {
				return fmt.Errorf("save arcade_notice schema before document migration: %w", err)
			}
			records, err := tx.FindAllRecords("arcade_notice")
			if err != nil {
				return fmt.Errorf("list arcade notices for document migration: %w", err)
			}
			for _, record := range records {
				record.Set("document", noticehandler.LegacyMarkdownToDocument(record.GetString("message")))
				if err := tx.Save(record); err != nil {
					return fmt.Errorf("migrate arcade_notice %s: %w", record.Id, err)
				}
			}
			notices.Fields.RemoveByName("message")
			document.Required = true
		}

		if err := tx.Save(notices); err != nil {
			return fmt.Errorf("save arcade_notice document schema: %w", err)
		}
		return nil
	})
}
