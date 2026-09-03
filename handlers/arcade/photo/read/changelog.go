package read

import (
	"encoding/json"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// ExpandChangelogAssets adds read-time file references without rewriting audit
// evidence. Membership removal does not unpublish a photo. File access still
// follows the same current authorization as DownloadArcadePhotoAtom.
func ExpandChangelogAssets(re *core.RequestEvent, items []map[string]any) error {
	rowIDs := make([][]string, len(items))
	ids := map[string]bool{}
	arcadeIDs := map[string]bool{}
	for i, item := range items {
		if item["changed"] != "photo" {
			continue
		}
		item["photo_assets"] = []map[string]any{}
		var log struct {
			Items []struct {
				AtomID string `json:"atom_id"`
				PrevID string `json:"prev_id"`
				Photo  string `json:"photo"`
			} `json:"items"`
		}
		data, err := json.Marshal(item["log"])
		if value, ok := item["log"].(string); ok {
			data = []byte(value)
		}
		if err != nil || json.Unmarshal(data, &log) != nil {
			continue
		}
		seen := map[string]bool{}
		for _, entry := range log.Items {
			for _, id := range []string{entry.AtomID, entry.PrevID, entry.Photo} {
				if id == "" || seen[id] {
					continue
				}
				seen[id], ids[id] = true, true
				rowIDs[i] = append(rowIDs[i], id)
			}
		}
		if id, ok := item["arcade"].(string); ok {
			arcadeIDs[id] = true
		}
	}
	if len(ids) == 0 {
		return nil
	}
	values := func(set map[string]bool) []any {
		out := make([]any, 0, len(set))
		for id := range set {
			out = append(out, id)
		}
		return out
	}
	atoms, err := re.App.FindAllRecords("arcade_photo_atoms", dbx.In("id", values(ids)...))
	if err != nil {
		return err
	}
	arcades, err := re.App.FindAllRecords("arcade", dbx.In("id", values(arcadeIDs)...))
	if err != nil {
		return err
	}
	arcadeByID := map[string]*core.Record{}
	for _, arcade := range arcades {
		arcadeByID[arcade.Id] = arcade
	}
	atomByID := map[string]*core.Record{}
	for _, atom := range atoms {
		atomByID[atom.Id] = atom
	}
	for i, item := range items {
		assets := []map[string]any{}
		for _, id := range rowIDs[i] {
			atom := atomByID[id]
			if atom == nil || atom.GetString("arcade") != item["arcade"] || atom.GetString("photo") == "" {
				continue
			}
			arcade := arcadeByID[atom.GetString("arcade")]
			if !CanReadAtom(re.Auth, arcade, atom) {
				continue
			}
			assets = append(assets, map[string]any{"id": id, "file_url": FileURL(id)})
		}
		if item["changed"] == "photo" {
			item["photo_assets"] = assets
		}
	}
	return nil
}
