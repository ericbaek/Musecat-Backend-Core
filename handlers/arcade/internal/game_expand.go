package arcadeinternal

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func exportGameSeriesVersion(rec *core.Record) map[string]any {
	if rec == nil {
		return nil
	}
	return map[string]any{
		"id":            rec.Id,
		"series":        rec.Get("series"),
		"released_on":   rec.Get("released_on"),
		"en":            rec.GetString("en"),
		"kr":            rec.GetString("kr"),
		"jp":            rec.GetString("jp"),
		"price_default": rec.Get("price_default"),
	}
}

func exportGameSeries(rec *core.Record) map[string]any {
	if rec == nil {
		return nil
	}
	return map[string]any{
		"id":           rec.Id,
		"seriesNumber": rec.Get("seriesNumber"),
		"en":           rec.GetString("en"),
		"kr":           rec.GetString("kr"),
		"jp":           rec.GetString("jp"),
		"en_short":     rec.GetString("en_short"),
		"kr_short":     rec.GetString("kr_short"),
		"jp_short":     rec.GetString("jp_short"),
		"manufacturer": rec.Get("manufacturer"),
	}
}

// BuildGameSeriesBundle returns a map with { version, series } for a game_series_version id.
func BuildGameSeriesBundle(app core.App, versionID string) (map[string]any, error) {
	if strings.TrimSpace(versionID) == "" {
		return nil, fmt.Errorf("empty versionId")
	}
	versionRec, err := app.FindRecordById(CollectionGameSeriesVersion, versionID)
	if err != nil {
		return nil, err
	}
	seriesID, _ := AsString(versionRec.Get("series"))
	var seriesRec *core.Record
	if seriesID != "" {
		if rec, err := app.FindRecordById(CollectionGameSeries, seriesID); err == nil {
			seriesRec = rec
		}
	}
	return map[string]any{
		"version": exportGameSeriesVersion(versionRec),
		"series":  exportGameSeries(seriesRec),
	}, nil
}

func BuildExpandedGameValue(app core.App, moleculeID string) (map[string]any, bool) {
	if strings.TrimSpace(moleculeID) == "" {
		return nil, false
	}

	gameRec, err := app.FindRecordById(CollectionArcadeGame, moleculeID)
	if err != nil {
		return nil, false
	}

	atoms, _ := app.FindRecordsByFilter(CollectionArcadeGameAtoms, "molecule={:id}", "", 0, 0, dbx.Params{"id": gameRec.Id})
	items := make([]map[string]any, 0, len(atoms))
	linkedFlagIDs := map[string]struct{}{}

	for _, a := range atoms {
		price := a.Get("price")
		tags := DecodeGameTagPayload(a.Get("tag"))

		gameID := a.GetString("game")
		var versionObj any
		var seriesObj any
		if gameID != "" {
			if bundle, err := BuildGameSeriesBundle(app, gameID); err == nil {
				versionObj = bundle["version"]
				seriesObj = bundle["series"]
			}
		}

		uncertain := a.GetBool("uncertain")
		var prevGameObj any
		if uncertain {
			prevGameID := a.GetString("prev_game")
			if prevGameID != "" {
				if bundle, err := BuildGameSeriesBundle(app, prevGameID); err == nil {
					prevGameObj = bundle["version"]
				}
			}
		}

		item := map[string]any{
			"version":   versionObj,
			"series":    seriesObj,
			"uncertain": uncertain,
			"location":  a.GetString("location"),
			"quantity":  a.GetInt("quantity"),
			"price":     price,
			"tag":       tags,
			"id":        a.GetString("id"),
			"updated":   GameAtomUpdatedValue(a),
		}
		if uncertain {
			item["prev_game"] = prevGameObj
		}

		flagIDs := a.GetStringSlice("flags")
		flags := make([]map[string]any, 0, len(flagIDs))
		for _, flagID := range flagIDs {
			if flagID == "" {
				continue
			}
			flagObj, ok := expandFlag(app, flagID, nil)
			if !ok {
				continue
			}
			linkedFlagIDs[flagID] = struct{}{}
			flags = append(flags, flagObj)
		}
		item["flags"] = flags
		items = append(items, item)
	}

	sortExpandedGameItems(items)

	gameObj := map[string]any{
		"id":    gameRec.Id,
		"items": items,
	}

	arcadeFlags, _ := app.FindRecordsByFilter(
		CollectionArcadeFlag,
		"arcade={:id} && solved=false",
		"created",
		0,
		0,
		dbx.Params{"id": gameRec.GetString("arcade")},
	)
	orphanFlags := make([]map[string]any, 0)
	for _, flagRec := range arcadeFlags {
		if flagRec.GetBool("solved") {
			continue
		}
		if _, linked := linkedFlagIDs[flagRec.Id]; linked {
			continue
		}
		flagObj, ok := expandFlag(app, flagRec.Id, flagRec)
		if !ok {
			continue
		}
		orphanFlags = append(orphanFlags, flagObj)
	}
	if len(orphanFlags) > 0 {
		gameObj["orphanFlags"] = orphanFlags
	}

	return gameObj, true
}

func GameAtomUpdatedValue(atom *core.Record) string {
	if atom == nil {
		return ""
	}

	updated := strings.TrimSpace(atom.GetString("updated"))
	if updated != "" {
		return updated
	}

	return strings.TrimSpace(atom.GetString("created"))
}

func ResolveGameMoleculeIDForFlag(app core.App, arcadeID, flagID string) string {
	if arcadeID != "" {
		if arcadeRec, err := app.FindRecordById(CollectionArcade, arcadeID); err == nil {
			if gameID := strings.TrimSpace(arcadeRec.GetString("game")); gameID != "" {
				return gameID
			}
		}
	}

	if flagID == "" {
		return ""
	}

	atomRecs, err := app.FindRecordsByFilter(CollectionArcadeGameAtoms, "", "", 0, 0)
	if err != nil {
		return ""
	}

	for _, atom := range atomRecs {
		if strings.TrimSpace(atom.GetString("molecule")) == "" {
			continue
		}
		for _, linkedFlagID := range atom.GetStringSlice("flags") {
			if linkedFlagID == flagID {
				return atom.GetString("molecule")
			}
		}
	}

	return ""
}

func BuildExpandedGameValueForArcadeFlag(app core.App, arcadeID, flagID string) (map[string]any, bool) {
	gameID := ResolveGameMoleculeIDForFlag(app, arcadeID, flagID)
	if gameID == "" {
		return map[string]any{
			"id":    "",
			"items": []map[string]any{},
		}, false
	}

	if gameValue, ok := BuildExpandedGameValue(app, gameID); ok {
		return gameValue, true
	}

	return map[string]any{
		"id":    gameID,
		"items": []map[string]any{},
	}, false
}

func expandFlag(app core.App, flagID string, flagRec *core.Record) (map[string]any, bool) {
	if flagID == "" {
		return nil, false
	}
	if flagRec == nil {
		var err error
		flagRec, err = app.FindRecordById(CollectionArcadeFlag, flagID)
		if err != nil {
			return map[string]any{"id": flagID}, true
		}
	}
	if flagRec.GetBool("solved") {
		return nil, false
	}
	reactionRecs, _ := app.FindRecordsByFilter(
		CollectionArcadeFlagReaction,
		"flag={:id}",
		"created",
		0,
		0,
		dbx.Params{"id": flagRec.Id},
	)
	reactions := make([]map[string]any, 0, len(reactionRecs))
	for _, rr := range reactionRecs {
		reactions = append(reactions, map[string]any{
			"id":        rr.Id,
			"reaction":  rr.GetString("reaction"),
			"createdBy": rr.GetString("createdBy"),
			"created":   rr.Get("created"),
			"updated":   rr.Get("updated"),
		})
	}
	return map[string]any{
		"id":         flagRec.Id,
		"arcade":     flagRec.GetString("arcade"),
		"disruption": flagRec.GetString("disruption"),
		"solved":     flagRec.GetBool("solved"),
		"message":    flagRec.GetString("message"),
		"photos":     flagRec.GetStringSlice("photos"),
		"createdBy":  flagRec.GetString("createdBy"),
		"created":    flagRec.Get("created"),
		"updated":    flagRec.Get("updated"),
		"reactions":  reactions,
	}, true
}

func sortExpandedGameItems(items []map[string]any) {
	sort.SliceStable(items, func(i, j int) bool {
		leftSeriesNumber, leftHasSeriesNumber := gameItemSeriesNumber(items[i])
		rightSeriesNumber, rightHasSeriesNumber := gameItemSeriesNumber(items[j])

		if leftHasSeriesNumber != rightHasSeriesNumber {
			return leftHasSeriesNumber
		}
		if leftHasSeriesNumber && leftSeriesNumber != rightSeriesNumber {
			return leftSeriesNumber < rightSeriesNumber
		}

		leftReleasedOn, leftHasReleasedOn := gameItemReleasedOn(items[i])
		rightReleasedOn, rightHasReleasedOn := gameItemReleasedOn(items[j])

		if leftHasReleasedOn != rightHasReleasedOn {
			return leftHasReleasedOn
		}
		if leftHasReleasedOn && leftReleasedOn != rightReleasedOn {
			return leftReleasedOn > rightReleasedOn
		}

		return strings.TrimSpace(gameItemID(items[i])) < strings.TrimSpace(gameItemID(items[j]))
	})
}

func gameItemSeriesNumber(item map[string]any) (int64, bool) {
	series, ok := item["series"].(map[string]any)
	if !ok || series == nil {
		return 0, false
	}

	switch value := series["seriesNumber"].(type) {
	case int:
		return int64(value), true
	case int8:
		return int64(value), true
	case int16:
		return int64(value), true
	case int32:
		return int64(value), true
	case int64:
		return value, true
	case float32:
		return int64(value), true
	case float64:
		return int64(value), true
	default:
		return 0, false
	}
}

func gameItemReleasedOn(item map[string]any) (string, bool) {
	version, ok := item["version"].(map[string]any)
	if !ok || version == nil {
		return "", false
	}

	rawReleasedOn, ok := version["released_on"]
	if !ok || rawReleasedOn == nil {
		return "", false
	}

	releasedOn := strings.TrimSpace(fmt.Sprint(rawReleasedOn))
	if releasedOn == "" || releasedOn == "<nil>" {
		return "", false
	}

	return releasedOn, true
}

func gameItemID(item map[string]any) string {
	id, _ := item["id"].(string)
	return id
}
