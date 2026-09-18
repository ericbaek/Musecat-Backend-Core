package user

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type arcadeFavoriteRequest struct {
	Arcade    string `json:"arcade"`
	Favorited *bool  `json:"favorited"`
}

// UpdateArcadeFavorite sets the authenticated user's saved state for one
// public arcade. The requested state makes retries idempotent.
func UpdateArcadeFavorite(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	}
	var input arcadeFavoriteRequest
	if err := json.NewDecoder(re.Request.Body).Decode(&input); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
	}
	input.Arcade = strings.TrimSpace(input.Arcade)
	if input.Arcade == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}
	if input.Favorited == nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "favorited is required"})
	}
	arcade, err := re.App.FindRecordById("arcade", input.Arcade)
	if err != nil || arcade == nil || !arcade.GetBool("public") {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}

	err = re.App.RunInTransaction(func(tx core.App) error {
		existing, err := tx.FindRecordsByFilter(
			CollectionArcadeFavorite,
			"user={:user} && arcade={:arcade}",
			"",
			1,
			0,
			dbx.Params{"user": re.Auth.Id, "arcade": input.Arcade},
		)
		if err != nil {
			return err
		}
		if *input.Favorited {
			if len(existing) > 0 {
				return nil
			}
			collection, err := tx.FindCollectionByNameOrId(CollectionArcadeFavorite)
			if err != nil {
				return err
			}
			record := core.NewRecord(collection)
			record.Set("user", re.Auth.Id)
			record.Set("arcade", input.Arcade)
			return tx.Save(record)
		}
		if len(existing) == 0 {
			return nil
		}
		return tx.Delete(existing[0])
	})
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update arcade favorite"})
	}
	return re.JSON(http.StatusOK, map[string]any{"arcade": input.Arcade, "favorited": *input.Favorited})
}
