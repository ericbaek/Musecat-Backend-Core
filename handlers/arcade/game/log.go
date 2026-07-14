package game

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

type gameDiffLogItem struct {
	AtomID     string                      `json:"atom_id"`
	PrevID     string                      `json:"prev_id,omitempty"`
	Game       string                      `json:"game"`
	ChangeType string                      `json:"change_type"`
	Bullets    []arcadeinternal.I18nBullet `json:"bullets"`
	Diff       []map[string]any            `json:"diff,omitempty"`
}

// buildGameDiffLogItem keeps the existing game changelog contract but isolates
// the field-level diffing from the write path so it is easier to debug.
func buildGameDiffLogItem(newAtomID string, g GameAtomInput, prevAtom *core.Record, action string) gameDiffLogItem {
	item := gameDiffLogItem{
		AtomID:     newAtomID,
		Game:       strings.TrimSpace(g.Game),
		ChangeType: "added",
		Bullets:    []arcadeinternal.I18nBullet{},
		Diff:       []map[string]any{},
	}

	if prevID := strings.TrimSpace(g.PrevID); prevID != "" {
		item.PrevID = prevID
	}

	if prevAtom == nil {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.added", map[string]any{
			"game": item.Game,
		}))
		if loc := strings.TrimSpace(g.Location); loc != "" {
			item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.location.set", map[string]any{
				"to": loc,
			}))
			item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "location", "", loc)
		}
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.quantity.set", map[string]any{
			"to": g.Quantity,
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "quantity", 0, g.Quantity)
		if g.Uncertain {
			item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.uncertain.changed", map[string]any{
				"from": false,
				"to":   true,
			}))
			item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "uncertain", false, true)
		}
		return item
	}

	item.ChangeType = "updated"

	prevGame := strings.TrimSpace(prevAtom.GetString("game"))
	nextGame := strings.TrimSpace(g.Game)
	if prevGame != nextGame {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.name.changed", map[string]any{
			"from": arcadeinternal.DisplayDiffText(prevGame),
			"to":   arcadeinternal.DisplayDiffText(nextGame),
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "game", prevGame, nextGame)
	}

	prevLocation := strings.TrimSpace(prevAtom.GetString("location"))
	nextLocation := strings.TrimSpace(g.Location)
	if prevLocation != nextLocation {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.location.changed", map[string]any{
			"from": arcadeinternal.DisplayDiffText(prevLocation),
			"to":   arcadeinternal.DisplayDiffText(nextLocation),
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "location", prevLocation, nextLocation)
	}

	prevQuantity := prevAtom.GetInt("quantity")
	if prevQuantity != g.Quantity {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.quantity.changed", map[string]any{
			"from": prevQuantity,
			"to":   g.Quantity,
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "quantity", prevQuantity, g.Quantity)
	}

	nextPrice := any(g.RawPrice)
	if nextPrice == nil {
		nextPrice = NormalizePriceForStorage(g.Price)
	}
	prevPrice := prevAtom.Get("price")
	if !arcadeinternal.JSONValueEqual(prevPrice, nextPrice) {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.price.changed", nil))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "price", prevPrice, nextPrice)
	}

	nextTag := any(g.RawTag)
	if nextTag == nil {
		nextTag = g.Tag
	}
	nextTag = arcadeinternal.NormalizeGameTagPayload(nextTag)
	prevTag := prevAtom.Get("tag")
	prevTag = arcadeinternal.NormalizeGameTagPayload(prevTag)
	if !arcadeinternal.JSONValueEqual(prevTag, nextTag) {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.tag.changed", nil))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "tag", prevTag, nextTag)
	}

	prevUncertain := prevAtom.GetBool("uncertain")
	if prevUncertain != g.Uncertain {
		if prevUncertain && !g.Uncertain && action != "" {
			item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.uncertain."+action, map[string]any{
				"from": prevUncertain,
				"to":   g.Uncertain,
			}))
		} else {
			item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.uncertain.changed", map[string]any{
				"from": prevUncertain,
				"to":   g.Uncertain,
			}))
		}
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "uncertain", prevUncertain, g.Uncertain)
	}

	if len(item.Diff) == 0 {
		item.ChangeType = "unchanged"
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.no_changes", nil))
		item.Diff = nil
	}

	return item
}

// buildDeletedGameDiffLogItem stores the full removed atom snapshot so the UI
// can render deletes without reloading the old molecule.
func buildDeletedGameDiffLogItem(prevAtom *core.Record) gameDiffLogItem {
	gameID := strings.TrimSpace(prevAtom.GetString("game"))
	location := strings.TrimSpace(prevAtom.GetString("location"))
	quantity := prevAtom.GetInt("quantity")
	item := gameDiffLogItem{
		PrevID:     prevAtom.Id,
		Game:       gameID,
		ChangeType: "deleted",
		Bullets: []arcadeinternal.I18nBullet{
			arcadeinternal.BuildI18nBullet("arcade.changelog.game.deleted", map[string]any{
				"game": gameID,
			}),
		},
		Diff: []map[string]any{
			{
				"field": "deleted",
				"from": map[string]any{
					"game":     gameID,
					"location": location,
					"quantity": quantity,
					"price":    prevAtom.Get("price"),
					"tag":      prevAtom.Get("tag"),
				},
				"to": nil,
			},
		},
	}
	if location != "" {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.location.was", map[string]any{
			"from": location,
		}))
	}
	item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.game.quantity.was", map[string]any{
		"from": quantity,
	}))
	return item
}
