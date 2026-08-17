package memo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const maxMemoDocumentBytes = 200_000

type memoUpdateBody struct {
	Arcade   string          `json:"arcade"`
	Document json.RawMessage `json:"document"`
}

func GetArcadeMemo(re *core.RequestEvent) error {
	id := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	if id == "" {
		id = strings.TrimSpace(re.Request.URL.Query().Get("id"))
	}
	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, id)
	if err != nil || !canReadArcade(re, arcade) {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}

	memoID := strings.TrimSpace(arcade.GetString("memo"))
	if memoID == "" {
		return re.JSON(http.StatusOK, map[string]any{"arcade": arcade.Id, "memo": nil})
	}
	memo, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeMemo, memoID)
	if err != nil || strings.TrimSpace(memo.GetString("arcade")) != arcade.Id {
		return re.JSON(http.StatusOK, map[string]any{"arcade": arcade.Id, "memo": nil})
	}
	return re.JSON(http.StatusOK, memoPayload(arcade.Id, memo))
}

func UpdateArcadeMemo(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.UnauthorizedError("The request requires valid record authorization token.", nil)
	}
	level, err := userhandler.LoadUserLevelState(re.App, re.Auth.Id)
	if err != nil || level.Level < 10 {
		return re.JSON(http.StatusForbidden, map[string]any{"error": "level 10 is required to edit arcade memos"})
	}

	var body memoUpdateBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
	}
	document, err := normalizeDocument(body.Document)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	body.Arcade = strings.TrimSpace(body.Arcade)
	if body.Arcade == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}

	var result map[string]any
	err = re.App.RunInTransaction(func(tx core.App) error {
		arcade, findErr := tx.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if findErr != nil {
			return &memoValidationError{"arcade not found"}
		}
		if !canWriteArcade(re, arcade) {
			return &memoValidationError{"arcade memo editing is not permitted"}
		}

		currentID := strings.TrimSpace(arcade.GetString("memo"))
		current, currentErr := tx.FindRecordById(arcadeinternal.CollectionArcadeMemo, currentID)
		if currentErr == nil && arcadeinternal.JSONValueEqual(current.Get("document"), document) {
			result = memoPayload(arcade.Id, current)
			result["changed"] = false
			return nil
		}

		collection, collectionErr := tx.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeMemo)
		if collectionErr != nil {
			return collectionErr
		}
		memo := core.NewRecord(collection)
		memo.Set("arcade", arcade.Id)
		memo.Set("document", document)
		memo.Set("by", re.Auth.Id)
		if saveErr := tx.Save(memo); saveErr != nil {
			return fmt.Errorf("failed to save arcade memo: %w", saveErr)
		}
		log := arcadeinternal.BuildChangelogEnvelope("memo", []map[string]any{
			{"change_type": "updated", "diff": []map[string]any{{"field": "document", "from": currentID, "to": memo.Id}}},
		})
		if updateErr := arcadeinternal.UpdateArcadeFieldsTxWithLogs(tx, arcade.Id, map[string]any{"memo": memo.Id}, map[string]any{"memo": log}, re.Auth.Id); updateErr != nil {
			return updateErr
		}
		result = memoPayload(arcade.Id, memo)
		result["changed"] = true
		return nil
	})
	if err != nil {
		if validationErr, ok := err.(*memoValidationError); ok {
			status := http.StatusBadRequest
			if validationErr.message == "arcade memo editing is not permitted" {
				status = http.StatusForbidden
			}
			return re.JSON(status, map[string]any{"error": validationErr.message})
		}
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to save arcade memo", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, result)
}

type memoValidationError struct{ message string }

func (e *memoValidationError) Error() string { return e.message }

func normalizeDocument(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 || len(raw) > maxMemoDocumentBytes {
		return nil, fmt.Errorf("memo document is required and must be at most %d bytes", maxMemoDocumentBytes)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("memo document must be a JSON object")
	}
	if document["type"] != "doc" {
		return nil, fmt.Errorf("memo document type must be doc")
	}
	if content, ok := document["content"]; ok && !isArray(content) {
		return nil, fmt.Errorf("memo document content must be an array")
	}
	return document, nil
}

func isArray(value any) bool {
	_, ok := value.([]any)
	return ok
}

func memoPayload(arcadeID string, memo *core.Record) map[string]any {
	return map[string]any{
		"arcade": arcadeID,
		"memo": map[string]any{
			"id":       memo.Id,
			"arcade":   arcadeID,
			"document": memo.Get("document"),
			"by":       memo.GetString("by"),
			"created":  memo.Get("created"),
		},
	}
}

func canReadArcade(re *core.RequestEvent, arcade *core.Record) bool {
	if arcade == nil {
		return false
	}
	return arcade.GetBool("public") || (re.Auth != nil && (arcade.GetString("createdBy") == re.Auth.Id || arcadequery.HasStrictReviewerAccess(re.Auth)))
}

func canWriteArcade(re *core.RequestEvent, arcade *core.Record) bool {
	return arcade.GetBool("public") || (re.Auth != nil && (arcade.GetString("createdBy") == re.Auth.Id || arcadequery.HasStrictReviewerAccess(re.Auth)))
}
