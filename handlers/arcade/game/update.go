package game

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

type PriceItem struct {
	Title     *string  `json:"title,omitempty"`
	Value     *float32 `json:"value"`
	ModeKey   *string  `json:"mode_key,omitempty"`
	Represent *bool    `json:"represent,omitempty"`
}
type Price struct {
	Currency string      `json:"currency"`
	Type     string      `json:"type"`
	List     []PriceItem `json:"list"`
	Accept   []string    `json:"accept"`
}

// ID is an arcade_game_entry id. It is intentionally stable across revisions.
// Game is a game_series_version id and may change only within the same series.
type GameAtomInput struct {
	ID string `json:"id,omitempty"`
	// PrevID is legacy internal-only log input. API v2 does not decode it.
	PrevID    string    `json:"-"`
	Game      string    `json:"game"`
	Location  string    `json:"location"`
	Quantity  int       `json:"quantity"`
	Price     Price     `json:"price"`
	Tag       []TagItem `json:"tag"`
	Uncertain bool      `json:"uncertain,omitempty"`
	PrevGame  string    `json:"prev_game,omitempty"`
	Confirm   bool      `json:"-"`
	RawPrice  any       `json:"-"`
	RawTag    any       `json:"-"`
}

type UpdateArcadeGameBody struct {
	Arcade      string          `json:"arcade"`
	BaseStateID string          `json:"base_state_id"`
	Games       []GameAtomInput `json:"games"`
}

func parseUpdateGameBody(re *core.RequestEvent) (UpdateArcadeGameBody, error) {
	var body UpdateArcadeGameBody
	return body, json.NewDecoder(re.Request.Body).Decode(&body)
}

func NormalizePriceForRead(p Price) Price {
	p.Currency = strings.TrimSpace(p.Currency)
	if err := ValidatePriceType(strings.TrimSpace(p.Type)); err != nil {
		p.Type = string(PriceTypeCustom)
	}
	if p.List == nil {
		p.List = []PriceItem{}
	}
	if p.Accept == nil {
		p.Accept = []string{}
	}
	return p
}
func NormalizePriceForStorage(p Price) Price {
	p.Currency = strings.TrimSpace(p.Currency)
	p.Type = strings.TrimSpace(p.Type)
	for i := range p.List {
		if p.List[i].Title != nil {
			v := strings.TrimSpace(*p.List[i].Title)
			p.List[i].Title = &v
		}
		if p.List[i].ModeKey != nil {
			v := strings.TrimSpace(*p.List[i].ModeKey)
			p.List[i].ModeKey = &v
		}
	}
	if p.Accept == nil {
		p.Accept = []string{}
	} else {
		for i := range p.Accept {
			p.Accept[i] = strings.TrimSpace(p.Accept[i])
		}
	}
	return p
}
func NormalizeTagForStorage(tags any) any { return arcadeinternal.NormalizeGameTagPayload(tags) }

func validatePrice(p Price) error {
	if strings.TrimSpace(p.Currency) == "" {
		return fmt.Errorf("price.currency is required")
	}
	if strings.TrimSpace(p.Type) == "" {
		return fmt.Errorf("price.type is required")
	}
	if err := ValidatePriceType(p.Type); err != nil {
		return err
	}
	if len(p.List) == 0 {
		return fmt.Errorf("price.list must have at least 1 item")
	}
	for i, it := range p.List {
		if it.Value != nil && *it.Value <= 0 {
			return fmt.Errorf("price.list[%d].value must be > 0 or null", i)
		}
	}
	return ValidatePriceAccept(p.Accept)
}

func validateUpdateGameBody(body *UpdateArcadeGameBody) error {
	body.Arcade, body.BaseStateID = strings.TrimSpace(body.Arcade), strings.TrimSpace(body.BaseStateID)
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	seenEntries, seenVersions := map[string]struct{}{}, map[string]struct{}{}
	for i := range body.Games {
		g := &body.Games[i]
		g.ID, g.Game = strings.TrimSpace(g.ID), strings.TrimSpace(g.Game)
		if g.Game == "" {
			return fmt.Errorf("games[%d].game is required", i)
		}
		if g.Quantity <= 0 {
			return fmt.Errorf("games[%d].quantity must be > 0", i)
		}
		if _, ok := seenVersions[g.Game]; ok {
			return fmt.Errorf("games[%d].game duplicates an active version", i)
		}
		seenVersions[g.Game] = struct{}{}
		if g.ID != "" {
			if _, ok := seenEntries[g.ID]; ok {
				return fmt.Errorf("games[%d].id is duplicated", i)
			}
			seenEntries[g.ID] = struct{}{}
		}
		if err := validatePrice(g.Price); err != nil {
			return fmt.Errorf("games[%d].%v", i, err)
		}
		if err := ValidateTagItems(g.Tag); err != nil {
			return fmt.Errorf("games[%d].%v", i, err)
		}
	}
	return nil
}

func revisionChanged(previous *core.Record, g GameAtomInput) bool {
	if previous == nil || previous.GetString("version") != g.Game || previous.GetString("location") != g.Location || previous.GetInt("quantity") != g.Quantity || previous.GetBool("uncertain") != g.Uncertain || previous.GetString("previous_version") != strings.TrimSpace(g.PrevGame) {
		return true
	}
	price, tag := any(g.RawPrice), any(g.RawTag)
	if price == nil {
		price = NormalizePriceForStorage(g.Price)
	}
	if tag == nil {
		tag = NormalizeTagForStorage(g.Tag)
	}
	return !arcadeinternal.JSONValueEqual(previous.Get("price"), price) || !arcadeinternal.JSONValueEqual(arcadeinternal.NormalizeGameTagPayload(previous.Get("tag")), NormalizeTagForStorage(tag))
}

// gameRevisionSnapshot is intentionally self-contained: the timeline can show
// a meaningful before/after diff even after catalog titles or later revisions
// change. The changelog row's `by` and `created` identify the editor and time.
func gameRevisionSnapshot(revision *core.Record) map[string]any {
	if revision == nil {
		return nil
	}
	return map[string]any{
		"version":          revision.GetString("version"),
		"location":         revision.GetString("location"),
		"quantity":         revision.GetInt("quantity"),
		"price":            revision.Get("price"),
		"tag":              arcadeinternal.DecodeGameTagPayload(revision.Get("tag")),
		"uncertain":        revision.GetBool("uncertain"),
		"previous_version": revision.GetString("previous_version"),
	}
}

func versionSeries(app core.App, versionID string) (string, error) {
	rec, err := app.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, versionID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(rec.GetString("series")), nil
}

func updateArcadeGameTx(txApp core.App, body UpdateArcadeGameBody, createdBy string, validate bool, action string) (string, error) {
	if validate {
		if err := validateUpdateGameBody(&body); err != nil {
			return "", err
		}
	}
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" {
		return "", fmt.Errorf("createdBy is required")
	}
	arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
	if err != nil {
		return "", fmt.Errorf("arcade not found: %w", err)
	}
	currentState := strings.TrimSpace(arcadeRec.GetString("game_state"))
	if strings.TrimSpace(body.BaseStateID) != currentState {
		return "", fmt.Errorf("game state conflict")
	}
	previousByEntry := map[string]*core.Record{}
	if currentState != "" {
		rows, findErr := txApp.FindRecordsByFilter(arcadeinternal.CollectionArcadeGameRevision, "batch={:batch}", "", 0, 0, dbx.Params{"batch": currentState})
		if findErr != nil {
			return "", findErr
		}
		for _, row := range rows {
			previousByEntry[row.GetString("entry")] = row
		}
	}
	entryColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeGameEntry)
	if err != nil {
		return "", err
	}
	batchColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeGameRevisionBatch)
	if err != nil {
		return "", err
	}
	revisionColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeGameRevision)
	if err != nil {
		return "", err
	}
	batch := core.NewRecord(batchColl)
	batch.Set("arcade", body.Arcade)
	batch.Set("created_by", createdBy)
	batch.Set("reason", action)
	if err := txApp.Save(batch); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	logItems := make([]map[string]any, 0, len(body.Games))
	for i, g := range body.Games {
		entryID := strings.TrimSpace(g.ID)
		versionSeriesID, seriesErr := versionSeries(txApp, g.Game)
		if seriesErr != nil || versionSeriesID == "" {
			return "", fmt.Errorf("games[%d].game not found", i)
		}
		var entry *core.Record
		if entryID == "" {
			entry = core.NewRecord(entryColl)
			entry.Set("arcade", body.Arcade)
			entry.Set("series", versionSeriesID)
			entry.Set("created_by", createdBy)
			if err := txApp.Save(entry); err != nil {
				return "", err
			}
			entryID = entry.Id
		} else {
			entry, err = txApp.FindRecordById(arcadeinternal.CollectionArcadeGameEntry, entryID)
			if err != nil {
				return "", fmt.Errorf("games[%d].id not found", i)
			}
			if entry.GetString("arcade") != body.Arcade {
				return "", fmt.Errorf("games[%d].id does not belong to arcade", i)
			}
			if entry.GetString("series") != versionSeriesID {
				return "", fmt.Errorf("games[%d].game must remain in the entry series", i)
			}
		}
		if prevVersion := strings.TrimSpace(g.PrevGame); prevVersion != "" {
			previousSeries, seriesErr := versionSeries(txApp, prevVersion)
			if seriesErr != nil || previousSeries != entry.GetString("series") {
				return "", fmt.Errorf("games[%d].prev_game must be in the entry series", i)
			}
		}
		previous := previousByEntry[entryID]
		revision := core.NewRecord(revisionColl)
		revision.Set("batch", batch.Id)
		revision.Set("entry", entryID)
		revision.Set("version", g.Game)
		revision.Set("location", g.Location)
		revision.Set("quantity", g.Quantity)
		if g.RawPrice != nil {
			revision.Set("price", g.RawPrice)
		} else {
			revision.Set("price", NormalizePriceForStorage(g.Price))
		}
		if g.RawTag != nil {
			revision.Set("tag", NormalizeTagForStorage(g.RawTag))
		} else {
			revision.Set("tag", NormalizeTagForStorage(g.Tag))
		}
		revision.Set("uncertain", g.Uncertain)
		revision.Set("previous_version", strings.TrimSpace(g.PrevGame))
		if previous != nil && !revisionChanged(previous, g) {
			revision.Set("last_modified_at", previous.Get("last_modified_at"))
			revision.Set("last_modified_by", previous.GetString("last_modified_by"))
		} else {
			revision.Set("last_modified_at", now)
			revision.Set("last_modified_by", createdBy)
		}
		if g.Confirm {
			revision.Set("last_confirmed_at", now)
			revision.Set("last_confirmed_by", createdBy)
		} else if previous != nil {
			revision.Set("last_confirmed_at", previous.Get("last_confirmed_at"))
			revision.Set("last_confirmed_by", previous.GetString("last_confirmed_by"))
		}
		if err := txApp.Save(revision); err != nil {
			return "", fmt.Errorf("create game revision %d: %w", i, err)
		}
		kind := "updated"
		if previous == nil {
			kind = "added"
		} else if g.Confirm {
			kind = "confirmed"
		} else if !revisionChanged(previous, g) {
			kind = "unchanged"
		}
		logItems = append(logItems, map[string]any{
			"entry_id":    entryID,
			"change_type": kind,
			"before":      gameRevisionSnapshot(previous),
			"after":       gameRevisionSnapshot(revision),
		})
	}
	for entryID, previous := range previousByEntry {
		found := false
		for _, g := range body.Games {
			if strings.TrimSpace(g.ID) == entryID {
				found = true
				break
			}
		}
		if !found {
			logItems = append(logItems, map[string]any{
				"entry_id":    entryID,
				"change_type": "deleted",
				"before":      gameRevisionSnapshot(previous),
				"after":       nil,
			})
		}
	}
	log := map[string]any{
		"type":       "game_diff",
		"version":    2,
		"state_from": currentState,
		"state_to":   batch.Id,
		"items":      logItems,
	}
	if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(txApp, arcadeRec.Id, map[string]any{"game_state": batch.Id}, map[string]any{"game": log}, createdBy); err != nil {
		return "", err
	}
	return batch.Id, nil
}

func UpdateArcadeGameTx(txApp core.App, body UpdateArcadeGameBody, createdBy string) (string, error) {
	return updateArcadeGameTx(txApp, body, createdBy, true, "edit")
}
func UpdateArcadeGameTxFromExistingAtoms(txApp core.App, body UpdateArcadeGameBody, createdBy string, action string) (string, error) {
	return updateArcadeGameTx(txApp, body, createdBy, false, action)
}

func UpdateArcadeGame(re *core.RequestEvent) error {
	body, err := parseUpdateGameBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	if err := validateUpdateGameBody(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": err.Error()})
	}
	var stateID string
	var xp userhandler.ExpFeedback
	if err := re.App.RunInTransaction(func(txApp core.App) error {
		arcadeRec, findErr := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if findErr != nil {
			return fmt.Errorf("arcade not found: %w", findErr)
		}
		base, expErr := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if expErr != nil {
			return expErr
		}
		stateID, err = UpdateArcadeGameTx(txApp, body, re.Auth.Id)
		if err != nil {
			return err
		}
		current := base
		if arcadeRec.GetBool("public") {
			current, _, err = userhandler.AwardArcadeEditExpTx(txApp, re.Auth.Id, body.Arcade, "game", 3, base, time.Now().UTC())
			if err != nil {
				return err
			}
		}
		xp = userhandler.BuildExpFeedback(base, current)
		return nil
	}); err != nil {
		status := http.StatusBadGateway
		if strings.Contains(err.Error(), "game state conflict") {
			status = http.StatusConflict
		}
		return re.JSON(status, map[string]any{"error": "game update failed", "details": err.Error()})
	}
	gameValue, ok := arcadeinternal.BuildExpandedGameValue(re.App, stateID)
	if !ok {
		gameValue = map[string]any{"id": stateID, "items": []map[string]any{}}
	}
	return re.JSON(http.StatusOK, map[string]any{"arcade": body.Arcade, "game": gameValue, "count": len(body.Games), "xp_feedback": xp})
}
