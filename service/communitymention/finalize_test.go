package communitymention

import (
	"github.com/pocketbase/pocketbase/core"
	_ "github.com/pocketbase/pocketbase/migrations"
	"testing"
)

func TestFinalizePreservesLegacyAndRejectsConflicts(t *testing.T) {
	app := core.NewBaseApp(core.BaseAppConfig{DataDir: t.TempDir()})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	defer app.ResetBootstrapState()
	col := core.NewBaseCollection("community_post")
	col.Fields.Add(&core.TextField{Name: "body"}, &core.AutodateField{Name: "created", OnCreate: true}, &core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true})
	if err := app.Save(col); err != nil {
		t.Fatal(err)
	}
	row := core.NewRecord(col)
	row.Set("body", "🎉 <literal>\nsecond line")
	if err := app.Save(row); err != nil {
		t.Fatal(err)
	}
	created, updated := row.GetString("created"), row.GetString("updated")
	if err := EnsureSchema(app); err != nil {
		t.Fatal(err)
	}
	row, _ = app.FindRecordById("community_post", row.Id)
	row.Set("body_document", DocumentFromText("conflict", nil))
	if err := app.Save(row); err != nil {
		t.Fatal(err)
	}
	updated = row.GetString("updated")
	if err := FinalizeSchema(app); err == nil {
		t.Fatal("conflicting original accepted")
	}
	col, _ = app.FindCollectionByNameOrId("community_post")
	if col.Fields.GetByName("body") == nil {
		t.Fatal("failed conversion dropped original")
	}
	row, _ = app.FindRecordById("community_post", row.Id)
	row.Set("body_document", nil)
	if err := app.Save(row); err != nil {
		t.Fatal(err)
	}
	updated = row.GetString("updated")
	for range 2 {
		if err := FinalizeSchema(app); err != nil {
			t.Fatal(err)
		}
	}
	row, _ = app.FindRecordById("community_post", row.Id)
	col, _ = app.FindCollectionByNameOrId("community_post")
	if col.Fields.GetByName("body") != nil || !col.Fields.GetByName("body_document").(*core.JSONField).Required || Body(row) != "🎉 <literal>\nsecond line" || row.GetString("created") != created || row.GetString("updated") != updated {
		t.Fatal("legacy state changed")
	}
}
