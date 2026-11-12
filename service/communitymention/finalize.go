package communitymention

import (
	"encoding/json"
	"fmt"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// BodyDocument adapts old records only while migrating; canonical records have no body column.
func BodyDocument(record *core.Record) Node {
	raw, _ := json.Marshal(record.GetRaw("body_document"))
	if doc, _, _, err := ParseDocument(raw); err == nil {
		return doc
	}
	return DocumentFromText(record.GetString("body"), Read(record))
}
func Body(record *core.Record) string {
	raw, _ := json.Marshal(record.GetRaw("body_document"))
	if _, text, _, err := ParseDocument(raw); err == nil {
		return text
	}
	return record.GetString("body")
}

// FinalizeSchema converts every row before dropping the legacy caption column.
// Raw bounded writes preserve created/updated, photos, edit deadlines and event identities.
func FinalizeSchema(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		if err := EnsureSchema(tx); err != nil {
			return err
		}
		col, err := tx.FindCollectionByNameOrId("community_post")
		if err != nil {
			return err
		}
		legacy := col.Fields.GetByName("body")
		if legacy != nil {
			if _, ok := legacy.(*core.TextField); !ok {
				return fmt.Errorf("incompatible legacy body field")
			}
		}
		after := ""
		for {
			rows, err := tx.FindRecordsByFilter("community_post", "id>{:after}", "id", 200, 0, dbx.Params{"after": after})
			if err != nil {
				return err
			}
			for _, row := range rows {
				raw, err := json.Marshal(row.GetRaw("body_document"))
				if err != nil {
					return err
				}
				var doc Node
				var text string
				var mentions []Mention
				if string(raw) == "null" {
					if legacy == nil {
						return fmt.Errorf("missing document for post %s", row.Id)
					}
					text = row.GetString("body")
					mentions = Read(row)
					if err := ValidateRanges(text, mentions); err != nil {
						return fmt.Errorf("invalid legacy mentions for %s: %w", row.Id, err)
					}
					doc = DocumentFromText(text, mentions)
				} else {
					doc, text, mentions, err = ParseDocument(raw)
					if err != nil {
						return fmt.Errorf("invalid document for post %s: %w", row.Id, err)
					}
					if legacy != nil && text != row.GetString("body") {
						return fmt.Errorf("caption/document mismatch for post %s", row.Id)
					}
				}
				encoded, err := json.Marshal(doc)
				if err != nil {
					return err
				}
				if _, _, _, err := ParseDocument(encoded); err != nil {
					return fmt.Errorf("unrepresentable legacy post %s: %w", row.Id, err)
				}
				refs, _ := json.Marshal(mentions)
				if _, err := tx.DB().NewQuery("UPDATE community_post SET body_document={:doc},mentions={:mentions} WHERE id={:id}").Bind(dbx.Params{"doc": string(encoded), "mentions": string(refs), "id": row.Id}).Execute(); err != nil {
					return err
				}
				after = row.Id
			}
			if len(rows) < 200 {
				break
			}
		}
		field := col.Fields.GetByName("body_document").(*core.JSONField)
		field.Required = true
		if legacy != nil {
			col.Fields.RemoveByName("body")
		}
		return tx.Save(col)
	})
}
