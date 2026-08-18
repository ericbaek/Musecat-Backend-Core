package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestArcadeMemo_AuthenticatedUserKeepsImmutableHistory(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{
		Name:     "Memo Arcade",
		Address:  "1 Memo St",
		Location: location{Lat: 37.5, Lon: 127.0},
	})

	authToken, _ := createAuthUserWithTags(t, app, nil)
	document := map[string]any{
		"type":    "doc",
		"content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Low"}}}},
	}
	body, _ := json.Marshal(map[string]any{"arcade": arcadeID, "document": document})
	firstResponse := executeJSONRequest(t, app, http.MethodPut, "/arcade/memo", string(body), map[string]string{"Authorization": "Bearer " + authToken})
	if firstResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected authenticated memo write to succeed, got %d", firstResponse.StatusCode)
	}
	firstMemoID := decodeJSONMap(t, firstResponse)["memo"].(map[string]any)["id"].(string)

	secondDocument := map[string]any{
		"type":    "doc",
		"content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Second"}}}},
	}
	secondBody, _ := json.Marshal(map[string]any{"arcade": arcadeID, "document": secondDocument})
	secondResponse := executeJSONRequest(t, app, http.MethodPut, "/arcade/memo", string(secondBody), map[string]string{"Authorization": "Bearer " + authToken})
	if secondResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected second memo write to succeed, got %d", secondResponse.StatusCode)
	}
	secondMemoID := decodeJSONMap(t, secondResponse)["memo"].(map[string]any)["id"].(string)

	emptyBody, _ := json.Marshal(map[string]any{
		"arcade":   arcadeID,
		"document": map[string]any{"type": "doc", "content": []any{}},
	})
	emptyResponse := executeJSONRequest(t, app, http.MethodPut, "/arcade/memo", string(emptyBody), map[string]string{"Authorization": "Bearer " + authToken})
	if emptyResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected empty memo write to succeed, got %d", emptyResponse.StatusCode)
	}
	emptyMemoID := decodeJSONMap(t, emptyResponse)["memo"].(map[string]any)["id"].(string)
	if emptyMemoID == secondMemoID {
		t.Fatalf("expected empty document to create a new immutable revision")
	}

	rollbackBody, _ := json.Marshal(map[string]any{"arcade": arcadeID, "part": "memo", "value": firstMemoID})
	rollbackResponse := executeJSONRequest(t, app, http.MethodPost, "/arcade/rollback", string(rollbackBody), map[string]string{"Authorization": "Bearer " + authToken})
	if rollbackResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected memo rollback to succeed, got %d", rollbackResponse.StatusCode)
	}
	rollbackResponse.Body.Close()

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatal(err)
	}
	if got := arcade.GetString("memo"); got != firstMemoID {
		t.Fatalf("expected rollback to point at first memo %q, got %q", firstMemoID, got)
	}
	changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:arcade} && changed='memo'", "created", 0, 0, map[string]any{"arcade": arcadeID})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 4 {
		t.Fatalf("expected four immutable memo changes, got %d", len(changes))
	}
	memos, err := app.FindRecordsByFilter("arcade_memo", "arcade={:arcade}", "created", 0, 0, map[string]any{"arcade": arcadeID})
	if err != nil {
		t.Fatal(err)
	}
	if len(memos) != 3 {
		t.Fatalf("expected three immutable memo revisions, got %d", len(memos))
	}

	readResponse := executeJSONRequest(t, app, http.MethodGet, "/arcade/memo?arcade="+arcadeID, "", nil)
	if readResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected anonymous memo read to succeed, got %d", readResponse.StatusCode)
	}
	readMemo := decodeJSONMap(t, readResponse)["memo"].(map[string]any)
	readMemoID := readMemo["id"].(string)
	if readMemoID != firstMemoID {
		t.Fatalf("expected public memo read to return rolled back revision %q, got %q", firstMemoID, readMemoID)
	}
	if readMemo["arcade"] != arcadeID {
		t.Fatalf("expected memo payload to include arcade id %q, got %v", arcadeID, readMemo["arcade"])
	}

	changelogResponse := executeJSONRequest(t, app, http.MethodGet, "/arcade/changelog?arcade="+arcadeID+"&changed=memo", "", nil)
	if changelogResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected memo changelog filter to succeed, got %d", changelogResponse.StatusCode)
	}
	changelog := decodeJSONMap(t, changelogResponse)
	items := changelog["items"].([]any)
	if len(items) != 4 {
		t.Fatalf("expected four filtered memo changelog rows, got %d", len(items))
	}
	lastItem := items[len(items)-1].(map[string]any)
	if lastItem["changed"] != "memo" {
		t.Fatalf("expected filtered row category memo, got %v", lastItem["changed"])
	}
	if _, ok := lastItem["memo"].(map[string]any); !ok {
		t.Fatalf("expected memo changelog row to include before/after snapshots")
	}
}
