package gtk

import (
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

type gtkAtomState struct {
	AtomID string
	Type   string
	Bool   bool
	Note   string
	Meta   any
}

type gtkDiffLogItem struct {
	AtomID     string                      `json:"atom_id,omitempty"`
	PrevID     string                      `json:"prev_id,omitempty"`
	GTKType    string                      `json:"gtk_type"`
	ChangeType string                      `json:"change_type"`
	Bullets    []arcadeinternal.I18nBullet `json:"bullets"`
	Diff       []map[string]any            `json:"diff,omitempty"`
}

// GTK atoms are keyed by type, so each log item explains how that one facility
// changed between molecules.
func loadCurrentGTKAtoms(app core.App, arcadeID, moleculeID string) ([]gtkAtomState, error) {
	if strings.TrimSpace(moleculeID) == "" {
		return []gtkAtomState{}, nil
	}

	rec, err := app.FindRecordById(arcadeinternal.CollectionArcadeGTK, moleculeID)
	if err != nil {
		return nil, fmt.Errorf("failed to load previous arcade_gtk: %w", err)
	}
	if rec.GetString("arcade") != arcadeID {
		return nil, fmt.Errorf("previous arcade_gtk does not belong to arcade")
	}

	atoms, err := app.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeGTKAtoms,
		"molecule={:id}",
		"+created",
		0,
		0,
		map[string]any{"id": moleculeID},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load previous gtk atoms: %w", err)
	}

	out := make([]gtkAtomState, 0, len(atoms))
	for _, atom := range atoms {
		out = append(out, gtkAtomState{
			AtomID: atom.Id,
			Type:   strings.TrimSpace(atom.GetString("type")),
			Bool:   atom.GetBool("bool"),
			Note:   strings.TrimSpace(atom.GetString("note")),
			Meta:   atom.Get("meta"),
		})
	}
	return out, nil
}

func buildGTKDiffLogItem(next gtkAtomState, prev *gtkAtomState) gtkDiffLogItem {
	item := gtkDiffLogItem{
		AtomID:  next.AtomID,
		GTKType: next.Type,
		Bullets: []arcadeinternal.I18nBullet{},
	}

	if prev == nil {
		item.ChangeType = "added"
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.gtk.added", map[string]any{
			"type": next.Type,
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "bool", nil, next.Bool)
		if next.Note != "" {
			item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "note", nil, next.Note)
		}
		if next.Meta != nil {
			item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "meta", nil, next.Meta)
		}
		return item
	}

	item.PrevID = prev.AtomID
	item.ChangeType = "updated"
	if prev.Bool != next.Bool {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.gtk.bool.changed", map[string]any{
			"from": prev.Bool,
			"to":   next.Bool,
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "bool", prev.Bool, next.Bool)
	}
	if prev.Note != next.Note {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.gtk.note.changed", map[string]any{
			"from": arcadeinternal.DisplayDiffText(prev.Note),
			"to":   arcadeinternal.DisplayDiffText(next.Note),
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "note", prev.Note, next.Note)
	}
	if !arcadeinternal.JSONValueEqual(prev.Meta, next.Meta) {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.gtk.meta.changed", map[string]any{
			"from": prev.Meta,
			"to":   next.Meta,
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "meta", prev.Meta, next.Meta)
	}
	if len(item.Diff) == 0 {
		item.ChangeType = "unchanged"
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.gtk.no_changes", nil))
		item.Diff = nil
	}
	return item
}

func buildDeletedGTKDiffLogItem(prev gtkAtomState) gtkDiffLogItem {
	return gtkDiffLogItem{
		PrevID:     prev.AtomID,
		GTKType:    prev.Type,
		ChangeType: "deleted",
		Bullets: []arcadeinternal.I18nBullet{
			arcadeinternal.BuildI18nBullet("arcade.changelog.gtk.deleted", map[string]any{
				"type": prev.Type,
			}),
		},
		Diff: []map[string]any{
			{
				"field": "deleted",
				"from": map[string]any{
					"type": prev.Type,
					"bool": prev.Bool,
					"note": prev.Note,
					"meta": prev.Meta,
				},
				"to": nil,
			},
		},
	}
}
