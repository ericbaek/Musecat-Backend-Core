package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestGameCatalog_RevertReloadedUpdateRestoresNamesAndArchiveState(t *testing.T) {
	app := newArcadeTestApp(t)
	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	headers := map[string]string{"Authorization": "Bearer " + token}
	mutate := func(method, path, id string, revision int, values map[string]any, changeID string) (map[string]any, string) {
		t.Helper()
		body := map[string]any{"entity": "manufacturer", "operation_id": uuid.NewString(), "reason": "Verify persisted catalog snapshot revert"}
		if id != "" {
			body["id"] = id
			body["expected_revision"] = revision
		}
		if values != nil {
			body["values"] = values
		}
		if changeID != "" {
			body["change_id"] = changeID
		}
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response := executeJSONRequest(t, app, method, "/moderation/game/catalog"+path, string(data), headers)
		defer response.Body.Close()
		var receipt struct {
			Item   map[string]any `json:"item"`
			Change struct {
				ID      string `json:"id"`
				Reverts string `json:"reverts_change"`
			} `json:"change"`
		}
		if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("catalog %s %s: status %d", method, path, response.StatusCode)
		}
		if changeID != "" && receipt.Change.Reverts != changeID {
			t.Fatalf("revert must cite selected change: %+v", receipt.Change)
		}
		return receipt.Item, receipt.Change.ID
	}
	names := map[string]any{"en": "Original manufacturer", "kr": "원래 제조사", "jp": "元のメーカー"}
	item, creationID := mutate(http.MethodPost, "", "", 0, names, "")
	id := item["id"].(string)
	_, updateID := mutate(http.MethodPut, "", id, 1, map[string]any{"en": "Edited", "kr": "편집", "jp": "編集"}, "")
	mutate(http.MethodDelete, "", id, 2, nil, "")
	mutate(http.MethodPost, "/restore", id, 3, nil, "")
	// The revert handler loads the audit row from storage in a new transaction.
	// Its JSON field is not the map originally passed to Record.Set.
	item, _ = mutate(http.MethodPost, "/changes/revert", id, 4, nil, updateID)
	for field, expected := range names {
		if item[field] != expected {
			t.Errorf("reverted %s = %v, want %v", field, item[field], expected)
		}
	}
	if item["archived"] != false || item["revision"] != float64(5) || item["archived_at"] != "" || item["archived_by"] != "" {
		t.Fatalf("update revert must restore unarchived snapshot at revision 5: %+v", item)
	}
	original, err := app.FindRecordById("game_catalog_changelog", updateID)
	if err != nil || original.GetString("action") != "update" {
		t.Fatalf("selected audit must remain immutable: %v", err)
	}
	item, _ = mutate(http.MethodPost, "/changes/revert", id, 5, nil, creationID)
	if item["archived"] != true || item["revision"] != float64(6) {
		t.Fatalf("creation revert must still archive at revision 6: %+v", item)
	}
}
