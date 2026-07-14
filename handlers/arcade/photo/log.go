package photo

import arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"

type photoDiffLogItem struct {
	AtomID     string                      `json:"atom_id,omitempty"`
	PrevID     string                      `json:"prev_id,omitempty"`
	Photo      string                      `json:"photo"`
	ChangeType string                      `json:"change_type"`
	Bullets    []arcadeinternal.I18nBullet `json:"bullets"`
	Diff       []map[string]any            `json:"diff,omitempty"`
}

// Photo logs only track molecule membership changes; the photo atom itself is
// immutable, so there is no "updated" case to represent.
func buildPhotoDiffLogItem(atomID, prevID string, existed bool) photoDiffLogItem {
	item := photoDiffLogItem{
		AtomID:  atomID,
		Photo:   atomID,
		Bullets: []arcadeinternal.I18nBullet{},
	}
	if prevID != "" {
		item.PrevID = prevID
	}

	if existed {
		item.ChangeType = "unchanged"
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.photo.kept", map[string]any{
			"photo": atomID,
		}))
		return item
	}

	item.ChangeType = "added"
	item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.photo.added", map[string]any{
		"photo": atomID,
	}))
	item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "photo", nil, atomID)
	return item
}

func buildDeletedPhotoDiffLogItem(atomID string) photoDiffLogItem {
	return photoDiffLogItem{
		PrevID:     atomID,
		Photo:      atomID,
		ChangeType: "deleted",
		Bullets: []arcadeinternal.I18nBullet{
			arcadeinternal.BuildI18nBullet("arcade.changelog.photo.deleted", map[string]any{
				"photo": atomID,
			}),
		},
		Diff: []map[string]any{
			{
				"field": "deleted",
				"from": map[string]any{
					"photo": atomID,
				},
				"to": nil,
			},
		},
	}
}
