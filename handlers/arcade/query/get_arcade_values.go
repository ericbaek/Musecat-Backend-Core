package query

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadgtk "github.com/ericbaek/musecat-backend-core/handlers/arcade/gtk"
	arcadehour "github.com/ericbaek/musecat-backend-core/handlers/arcade/hour"
	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	arcadesns "github.com/ericbaek/musecat-backend-core/handlers/arcade/sns"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

// GetArcadeValues handles GET /arcade?id=... (or ?arcade=...)
// - Default: returns relation ids basic/hour/sns/gtk/game/photo
// - With expand=all: returns full objects for each relation in a frontend-friendly shape.
func GetArcadeValues(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	id := q.Get("id")

	if id == "" {
		id = q.Get("arcade")
	}
	if id == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "missing required query param 'id' or 'arcade'",
		})
	}

	rec, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, id)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{
			"error":   "arcade not found",
			"details": err.Error(),
		})
	}

	// Always include relation ids and selected arcade admin metadata.
	basicId, _ := arcadeinternal.AsString(rec.Get("basic"))
	hourId, _ := arcadeinternal.AsString(rec.Get("hour"))
	snsId, _ := arcadeinternal.AsString(rec.Get("sns"))
	gtkId, _ := arcadeinternal.AsString(rec.Get("gtk"))
	gameId, _ := arcadeinternal.AsString(rec.Get("game"))
	photoId, _ := arcadeinternal.AsString(rec.Get("photo"))
	admin := map[string]any{
		"id":        rec.Id,
		"public":    rec.GetBool("public"),
		"closed":    rec.GetBool("closed"),
		"country":   rec.GetString("country"),
		"timezone":  rec.GetString("timezone"),
		"createdBy": rec.GetString("createdBy"),
		"created":   rec.Get("created"),
		"updated":   rec.Get("updated"),
	}

	// expand can be: "none" (default), "all", or a comma list like "basic,hour"
	expandParam := strings.TrimSpace(strings.ToLower(q.Get("expand")))

	// id-only base response
	out := map[string]any{
		"admin": admin,
		"basic": basicId,
		"hour":  hourId,
		"sns":   snsId,
		"gtk":   gtkId,
		"game":  gameId,
		"photo": photoId,
	}

	if expandParam == "" || expandParam == "none" {
		return re.JSON(http.StatusOK, out)
	}

	// determine which sections to expand
	want := map[string]bool{}
	if expandParam == "all" {
		want["basic"], want["hour"], want["sns"], want["gtk"], want["game"], want["photo"] = true, true, true, true, true, true
	} else {
		for _, part := range strings.Split(expandParam, ",") {
			p := strings.TrimSpace(part)
			if p != "" {
				want[p] = true
			}
		}
	}

	if want["basic"] && basicId != "" {
		if basicRec, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeBasic, basicId); err == nil {
			// normalize location
			lat, lon, _ := arcadeinternal.ReadLocation(basicRec.Get("location"))
			out["basic"] = map[string]any{
				"id":          basicRec.Id,
				"name":        basicRec.GetString("name"),
				"address":     basicRec.GetString("address"),
				"direction":   basicRec.GetString("direction"),
				"nickname":    basicRec.GetStringSlice("nickname"),
				"subway_line": basicRec.GetStringSlice("subway_line"),
				"location":    map[string]any{"lat": lat, "lon": lon},
			}
		}
	}

	if want["hour"] && hourId != "" {
		if hourRec, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeHour, hourId); err == nil {
			out["hour"] = arcadehour.BuildArcadeHourExpandedValue(hourRec)
		}
	}

	if want["sns"] && snsId != "" {
		if snsRec, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeSNS, snsId); err == nil {
			atoms, _ := re.App.FindRecordsByFilter(arcadeinternal.CollectionArcadeSNSAtoms, "molecule={:id}", "", 0, 0, dbx.Params{"id": snsRec.Id})
			items := make([]arcadesns.ExpandedSNSItem, 0, len(atoms))
			for _, a := range atoms {
				snsType := a.GetString("type")
				item := arcadesns.ExpandedSNSItem{
					Type: snsType,
					Link: arcadeinternal.ResolveSNSLinkForOutput(snsType, a.GetString("link"), a.GetString("phone")),
				}
				if name := a.GetString("name"); name != "" {
					item.Name = name
				}
				items = append(items, item)
			}
			out["sns"] = arcadesns.BuildExpandedSNSValue(snsRec.Id, items)
		}
	}

	if want["gtk"] && gtkId != "" {
		if gtkRec, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeGTK, gtkId); err == nil {
			atoms, _ := re.App.FindRecordsByFilter(arcadeinternal.CollectionArcadeGTKAtoms, "molecule={:id}", "", 0, 0, dbx.Params{"id": gtkRec.Id})
			items := make([]arcadgtk.ExpandedGTKItem, 0, len(atoms))
			for _, a := range atoms {
				item := arcadgtk.ExpandedGTKItem{
					Type: a.GetString("type"),
					Bool: a.GetBool("bool"),
					Meta: a.Get("meta"),
				}
				if note := a.GetString("note"); note != "" {
					item.Note = note
				}
				items = append(items, item)
			}
			out["gtk"] = arcadgtk.BuildExpandedGTKValue(gtkRec.Id, items)
		}
	}

	if want["game"] {
		gameExpanded := false
		items := []map[string]any{}
		linkedFlagIDs := map[string]struct{}{}

		if gameId != "" {
			if gameRec, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeGame, gameId); err == nil {
				atoms, _ := re.App.FindRecordsByFilter(arcadeinternal.CollectionArcadeGameAtoms, "molecule={:id}", "", 0, 0, dbx.Params{"id": gameRec.Id})
				items = make([]map[string]any, 0, len(atoms))

				for _, a := range atoms {
					// Return stored price JSON as-is to preserve original scalar types.
					price := a.Get("price")
					tags := arcadeinternal.DecodeGameTagPayload(a.Get("tag"))

					gameId := a.GetString("game")
					var versionObj any
					var seriesObj any
					if gameId != "" {
						if bundle, err := BuildGameSeriesBundle(re.App, gameId); err == nil {
							versionObj = bundle["version"]
							seriesObj = bundle["series"]
						}
					}
					uncertain := a.GetBool("uncertain")
					var prevGameObj any
					if uncertain {
						prevGameID := a.GetString("prev_game")
						if prevGameID != "" {
							if bundle, err := BuildGameSeriesBundle(re.App, prevGameID); err == nil {
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
						"updated":   arcadeinternal.GameAtomUpdatedValue(a),
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
						flagObj, ok := expandFlag(re.App, flagID, nil)
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

				gameExpanded = true
			}
		}

		arcadeFlags, _ := re.App.FindRecordsByFilter(
			arcadeinternal.CollectionArcadeFlag,
			"arcade={:id} && solved=false",
			"created",
			0,
			0,
			dbx.Params{"id": rec.Id},
		)
		orphanFlags := make([]map[string]any, 0)
		for _, flagRec := range arcadeFlags {
			if flagRec.GetBool("solved") {
				continue
			}
			if _, linked := linkedFlagIDs[flagRec.Id]; linked {
				continue
			}
			flagObj, ok := expandFlag(re.App, flagRec.Id, flagRec)
			if !ok {
				continue
			}
			orphanFlags = append(orphanFlags, flagObj)
		}

		if gameExpanded || len(orphanFlags) > 0 {
			gameObj := map[string]any{
				"id":    gameId,
				"items": items,
			}
			if len(orphanFlags) > 0 {
				gameObj["orphanFlags"] = orphanFlags
			}
			out["game"] = gameObj
		}
	}

	if want["photo"] && photoId != "" {
		if photoRec, err := re.App.FindRecordById(arcadeinternal.CollectionArcadePhoto, photoId); err == nil {
			atomIDs := photoRec.GetStringSlice("photos")
			items := make([]map[string]any, 0, len(atomIDs))
			for _, atomID := range atomIDs {
				if atomID == "" {
					continue
				}
				atom, err := re.App.FindRecordById(arcadeinternal.CollectionArcadePhotoAtoms, atomID)
				if err != nil {
					items = append(items, map[string]any{
						"id":        atomID,
						"photo":     "",
						"public":    false,
						"created":   nil,
						"createdBy": map[string]any{"nickname": userhandler.WithdrawnDisplayName(), "username": userhandler.WithdrawnDisplayName()},
					})
					continue
				}
				createdByID := atom.GetString("createdBy")
				items = append(items, map[string]any{
					"id":        atom.Id,
					"photo":     atom.GetString("photo"),
					"public":    atom.GetBool("public"),
					"created":   atom.Get("created"),
					"createdBy": buildPhotoCreatedBy(re.App, createdByID),
				})
			}
			out["photo"] = map[string]any{
				"id":    photoRec.Id,
				"items": items,
			}
		}
	}

	return re.JSON(http.StatusOK, out)
}

func buildPhotoCreatedBy(app core.App, userID string) map[string]any {
	out := map[string]any{
		"nickname": userhandler.WithdrawnDisplayName(),
		"username": userhandler.WithdrawnDisplayName(),
	}
	if userID == "" {
		return out
	}

	profile, err := userhandler.FetchMergedProfile(app, userID)
	if err != nil {
		return out
	}
	out["nickname"] = profile.Nickname
	out["username"] = profile.Username
	return out
}

func expandFlag(app core.App, flagID string, flagRec *core.Record) (map[string]any, bool) {
	if flagID == "" {
		return nil, false
	}

	if flagRec == nil {
		var err error
		flagRec, err = app.FindRecordById(arcadeinternal.CollectionArcadeFlag, flagID)
		if err != nil {
			return map[string]any{"id": flagID}, true
		}
	}
	if flagRec.GetBool("solved") {
		return nil, false
	}

	reactionRecs, _ := app.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeFlagReaction,
		"flag={:id}",
		"created",
		0,
		0,
		dbx.Params{"id": flagRec.Id},
	)
	reactions := make([]map[string]any, 0, len(reactionRecs))
	for _, rr := range reactionRecs {
		createdByID := rr.GetString("createdBy")
		reactions = append(reactions, map[string]any{
			"id":        rr.Id,
			"reaction":  rr.GetString("reaction"),
			"createdBy": createdByID,
			"created":   rr.Get("created"),
			"updated":   rr.Get("updated"),
		})
	}

	createdByID := flagRec.GetString("createdBy")
	return map[string]any{
		"id":         flagRec.Id,
		"arcade":     flagRec.GetString("arcade"),
		"disruption": flagRec.GetString("disruption"),
		"solved":     flagRec.GetBool("solved"),
		"message":    flagRec.GetString("message"),
		"photos":     flagRec.GetStringSlice("photos"),
		"createdBy":  createdByID,
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
