package user

import (
	"database/sql"
	"encoding/json"
	"errors"
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

			var maxOrder sql.NullInt64
			if err := tx.DB().NewQuery("SELECT MAX(sort_order) FROM " + CollectionArcadeFavorite + " WHERE user = {:user}").Bind(dbx.Params{"user": re.Auth.Id}).Row(&maxOrder); err != nil {
				return err
			}
			nextOrder := int64(0)
			if maxOrder.Valid {
				nextOrder = maxOrder.Int64 + 1
			}
			record.Set("sort_order", nextOrder)

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

type arcadeFavoriteOrderRequest struct {
	Arcades []string `json:"arcades"`
}

// UpdateArcadeFavoriteOrder sets the explicit ordering of the authenticated user's favorite arcades.
func UpdateArcadeFavoriteOrder(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	}
	var input arcadeFavoriteOrderRequest
	if err := json.NewDecoder(re.Request.Body).Decode(&input); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
	}
	if input.Arcades == nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcades array is required"})
	}

	seen := make(map[string]struct{}, len(input.Arcades))
	orderedIDs := make([]string, 0, len(input.Arcades))
	for _, raw := range input.Arcades {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "duplicate arcade in order list"})
		}
		seen[id] = struct{}{}
		orderedIDs = append(orderedIDs, id)
	}

	invalidOrder := errors.New("order contains an arcade that is not in favorites")
	err := re.App.RunInTransaction(func(tx core.App) error {
		existing, err := tx.FindRecordsByFilter(
			CollectionArcadeFavorite,
			"user={:user}",
			"sort_order,-created",
			0,
			0,
			dbx.Params{"user": re.Auth.Id},
		)
		if err != nil {
			return err
		}

		byArcadeID := make(map[string]*core.Record, len(existing))
		for _, rec := range existing {
			byArcadeID[rec.GetString("arcade")] = rec
		}

		for _, id := range orderedIDs {
			if _, ok := byArcadeID[id]; !ok {
				return invalidOrder
			}
		}

		order := 0
		for _, id := range orderedIDs {
			rec := byArcadeID[id]
			if rec.GetInt("sort_order") != order {
				rec.Set("sort_order", order)
				if err := tx.Save(rec); err != nil {
					return err
				}
			}
			order++
		}

		for _, rec := range existing {
			id := rec.GetString("arcade")
			if _, specified := seen[id]; !specified {
				if rec.GetInt("sort_order") != order {
					rec.Set("sort_order", order)
					if err := tx.Save(rec); err != nil {
						return err
					}
				}
				order++
			}
		}

		return nil
	})
	if errors.Is(err, invalidOrder) {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": invalidOrder.Error()})
	}
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update favorite order"})
	}

	return re.JSON(http.StatusOK, map[string]any{"arcades": orderedIDs})
}
