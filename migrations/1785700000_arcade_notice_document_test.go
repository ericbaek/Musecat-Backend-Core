package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestMigrateArcadeNoticeDocumentPersistsLegacyRows(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "musecat_notice_document_migration_test",
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })

	notices := core.NewBaseCollection("arcade_notice")
	notices.Fields.Add(&core.TextField{Name: "message"})
	if err := app.Save(notices); err != nil {
		t.Fatalf("create arcade_notice collection: %v", err)
	}
	record := core.NewRecord(notices)
	record.Set("message", "## **Migrated notice**")
	if err := app.Save(record); err != nil {
		t.Fatalf("create legacy notice: %v", err)
	}

	if err := migrateArcadeNoticeDocument(app); err != nil {
		t.Fatalf("migrate arcade notice document: %v", err)
	}

	notices, err := app.FindCollectionByNameOrId("arcade_notice")
	if err != nil {
		t.Fatalf("reload arcade_notice collection: %v", err)
	}
	if notices.Fields.GetByName("message") != nil {
		t.Fatal("expected legacy message field to be removed")
	}
	record, err = app.FindRecordById("arcade_notice", record.Id)
	if err != nil {
		t.Fatalf("reload migrated notice: %v", err)
	}
	if record.Get("document") == nil {
		t.Fatal("expected migrated notice document to be persisted")
	}
}
