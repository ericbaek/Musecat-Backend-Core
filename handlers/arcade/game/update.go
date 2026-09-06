package game

import (
	"encoding/json"
	"errors"
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

// ID is an arcade_game_id id. It is intentionally stable across revisions.
// Game is a game_series_version id and may change only within the same series.
type GameAtomInput struct {
	ID string `json:"id,omitempty"`
	// idProvided distinguishes an omitted add id from an explicit null/empty id.
	idProvided bool
	// PrevID is legacy internal-only log input. API v2 does not decode it.
	PrevID   string    `json:"-"`
	Game     string    `json:"game"`
	Cabinet  string    `json:"cabinet,omitempty"`
	Location string    `json:"location"`
	Quantity int       `json:"quantity"`
	Price    Price     `json:"price"`
	Tag      []TagItem `json:"tag"`
	RawPrice any       `json:"-"`
	RawTag   any       `json:"-"`
}

func (g *GameAtomInput) UnmarshalJSON(data []byte) error {
	type gameAtomInputAlias GameAtomInput
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	var decoded gameAtomInputAlias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*g = GameAtomInput(decoded)
	_, g.idProvided = fields["id"]
	return nil
}

type UpdateArcadeGameBody struct {
	Arcade      string          `json:"arcade"`
	BaseStateID string          `json:"base_state_id"`
	Games       []GameAtomInput `json:"games"`
}

// UpdateArcadeGameDeltaBody is the public request contract. The internal
// UpdateArcadeGameBody remains a complete-state representation for immutable
// batch creation and for non-HTTP callers such as campaign and bulk updates.
type UpdateArcadeGameDeltaBody struct {
	Arcade      string          `json:"arcade"`
	BaseStateID string          `json:"base_state_id"`
	Add         []GameAtomInput `json:"add"`
	Modify      []GameAtomInput `json:"modify"`
	Remove      []string        `json:"remove"`
}

func parseUpdateGameBody(re *core.RequestEvent) (UpdateArcadeGameDeltaBody, error) {
	var wire struct {
		Arcade      string           `json:"arcade"`
		BaseStateID string           `json:"base_state_id"`
		Add         *[]GameAtomInput `json:"add"`
		Modify      *[]GameAtomInput `json:"modify"`
		Remove      *[]string        `json:"remove"`
	}
	if err := json.NewDecoder(re.Request.Body).Decode(&wire); err != nil {
		return UpdateArcadeGameDeltaBody{}, err
	}
	if wire.Add == nil || wire.Modify == nil || wire.Remove == nil {
		return UpdateArcadeGameDeltaBody{}, fmt.Errorf("add, modify, and remove are required arrays")
	}
	return UpdateArcadeGameDeltaBody{
		Arcade:      wire.Arcade,
		BaseStateID: wire.BaseStateID,
		Add:         *wire.Add,
		Modify:      *wire.Modify,
		Remove:      *wire.Remove,
	}, nil
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

// normalizePriceForComparison collapses equivalent wire/storage shapes before
// comparing prices or writing them into a changelog snapshot. In particular,
// the frontend sends represent:false while older revisions may omit it.
func normalizePriceForComparison(raw any) any {
	if raw == nil {
		return nil
	}

	encoded, err := json.Marshal(raw)
	if err != nil {
		return raw
	}
	var price Price
	if err := json.Unmarshal(encoded, &price); err != nil {
		return raw
	}

	price = NormalizePriceForStorage(price)
	if price.List == nil {
		price.List = []PriceItem{}
	}
	for i := range price.List {
		if price.List[i].Title != nil && strings.TrimSpace(*price.List[i].Title) == "" {
			price.List[i].Title = nil
		}
		if price.List[i].ModeKey != nil && strings.TrimSpace(*price.List[i].ModeKey) == "" {
			price.List[i].ModeKey = nil
		}
		if price.List[i].Represent == nil {
			represent := false
			price.List[i].Represent = &represent
		}
	}
	return price
}

func gamePriceForComparison(g GameAtomInput) any {
	price := any(g.RawPrice)
	if price == nil {
		price = g.Price
	}
	return normalizePriceForComparison(price)
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

func validateGameAtomFields(label string, g *GameAtomInput) error {
	g.ID, g.Game, g.Cabinet = strings.TrimSpace(g.ID), strings.TrimSpace(g.Game), strings.TrimSpace(g.Cabinet)
	if g.Game == "" {
		return fmt.Errorf("%s.game is required", label)
	}
	if g.Quantity <= 0 {
		return fmt.Errorf("%s.quantity must be > 0", label)
	}
	price := g.Price
	if g.RawPrice != nil {
		encoded, err := json.Marshal(g.RawPrice)
		if err != nil {
			return fmt.Errorf("%s.price is invalid", label)
		}
		if err := json.Unmarshal(encoded, &price); err != nil {
			return fmt.Errorf("%s.price is invalid", label)
		}
	}
	if err := validatePrice(price); err != nil {
		return fmt.Errorf("%s.%v", label, err)
	}
	tags := g.Tag
	if g.RawTag != nil {
		encoded, err := json.Marshal(g.RawTag)
		if err != nil {
			return fmt.Errorf("%s.tag is invalid", label)
		}
		if err := json.Unmarshal(encoded, &tags); err != nil {
			return fmt.Errorf("%s.tag is invalid", label)
		}
	}
	if err := ValidateTagItems(tags); err != nil {
		return fmt.Errorf("%s.%v", label, err)
	}
	return nil
}

func validateUpdateGameBody(body *UpdateArcadeGameBody) error {
	body.Arcade, body.BaseStateID = strings.TrimSpace(body.Arcade), strings.TrimSpace(body.BaseStateID)
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	seenEntries, seenVersions := map[string]struct{}{}, map[string]struct{}{}
	for i := range body.Games {
		g := &body.Games[i]
		if err := validateGameAtomFields(fmt.Sprintf("games[%d]", i), g); err != nil {
			return err
		}
		versionKey := g.Game + "\x00" + g.Cabinet
		if _, ok := seenVersions[versionKey]; ok {
			return fmt.Errorf("games[%d].game and cabinet duplicate an active revision", i)
		}
		seenVersions[versionKey] = struct{}{}
		if g.ID != "" {
			if _, ok := seenEntries[g.ID]; ok {
				return fmt.Errorf("games[%d].id is duplicated", i)
			}
			seenEntries[g.ID] = struct{}{}
		}
	}
	return nil
}

func validateUpdateGameDeltaBody(body *UpdateArcadeGameDeltaBody) error {
	body.Arcade, body.BaseStateID = strings.TrimSpace(body.Arcade), strings.TrimSpace(body.BaseStateID)
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	if len(body.Add) == 0 && len(body.Modify) == 0 && len(body.Remove) == 0 {
		return fmt.Errorf("at least one add, modify, or remove item is required")
	}

	seenModify, seenRemove := map[string]struct{}{}, map[string]struct{}{}
	for i := range body.Add {
		g := &body.Add[i]
		if g.idProvided || strings.TrimSpace(g.ID) != "" {
			return fmt.Errorf("add[%d].id must be omitted", i)
		}
		if err := validateGameAtomFields(fmt.Sprintf("add[%d]", i), g); err != nil {
			return err
		}
	}
	for i := range body.Modify {
		g := &body.Modify[i]
		g.ID = strings.TrimSpace(g.ID)
		if g.ID == "" {
			return fmt.Errorf("modify[%d].id is required", i)
		}
		if _, exists := seenModify[g.ID]; exists {
			return fmt.Errorf("modify[%d].id is duplicated", i)
		}
		seenModify[g.ID] = struct{}{}
		if err := validateGameAtomFields(fmt.Sprintf("modify[%d]", i), g); err != nil {
			return err
		}
	}
	for i := range body.Remove {
		body.Remove[i] = strings.TrimSpace(body.Remove[i])
		if body.Remove[i] == "" {
			return fmt.Errorf("remove[%d] is required", i)
		}
		if _, exists := seenRemove[body.Remove[i]]; exists {
			return fmt.Errorf("remove[%d] is duplicated", i)
		}
		if _, modifies := seenModify[body.Remove[i]]; modifies {
			return fmt.Errorf("remove[%d] conflicts with modify", i)
		}
		seenRemove[body.Remove[i]] = struct{}{}
	}
	return nil
}

type gameRequestValidationError struct {
	message string
}

func (e *gameRequestValidationError) Error() string { return e.message }

func materializeUpdateGameDelta(txApp core.App, delta UpdateArcadeGameDeltaBody) (UpdateArcadeGameBody, int, error) {
	arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, delta.Arcade)
	if err != nil {
		return UpdateArcadeGameBody{}, 0, fmt.Errorf("arcade not found: %w", err)
	}
	currentState := strings.TrimSpace(arcadeRec.GetString("game_v2"))
	if strings.TrimSpace(delta.BaseStateID) != currentState {
		return UpdateArcadeGameBody{}, 0, fmt.Errorf("game state conflict")
	}

	full := UpdateArcadeGameBody{
		Arcade:      delta.Arcade,
		BaseStateID: currentState,
		Games:       []GameAtomInput{},
	}
	if currentState != "" {
		full, err = BuildUpdateBodyFromCurrentState(txApp, delta.Arcade)
		if err != nil {
			return UpdateArcadeGameBody{}, 0, err
		}
		full.BaseStateID = currentState
	}

	previousByEntry := map[string]*core.Record{}
	if currentState != "" {
		rows, findErr := txApp.FindRecordsByFilter(
			arcadeinternal.CollectionArcadeGameRevision,
			"batch={:batch}",
			"",
			0,
			0,
			dbx.Params{"batch": currentState},
		)
		if findErr != nil {
			return UpdateArcadeGameBody{}, 0, findErr
		}
		for _, row := range rows {
			previousByEntry[row.GetString("entry")] = row
		}
	}

	indexByEntry := make(map[string]int, len(full.Games))
	reserved := make(map[string]struct{}, len(full.Games))
	for i := range full.Games {
		id := strings.TrimSpace(full.Games[i].ID)
		indexByEntry[id] = i
		reserved[id] = struct{}{}
	}

	changedEntries := 0
	for i := range delta.Modify {
		g := delta.Modify[i]
		entryIndex, ok := indexByEntry[g.ID]
		if !ok {
			return UpdateArcadeGameBody{}, 0, &gameRequestValidationError{message: fmt.Sprintf("modify[%d].id is not active", i)}
		}

		seriesID, seriesErr := versionSeries(txApp, g.Game)
		if seriesErr != nil || seriesID == "" {
			return UpdateArcadeGameBody{}, 0, &gameRequestValidationError{message: fmt.Sprintf("modify[%d].game not found", i)}
		}
		entry, entryErr := txApp.FindRecordById(arcadeinternal.CollectionArcadeGameEntry, g.ID)
		if entryErr != nil {
			return UpdateArcadeGameBody{}, 0, &gameRequestValidationError{message: fmt.Sprintf("modify[%d].id not found", i)}
		}
		if entry.GetString("arcade") != delta.Arcade {
			return UpdateArcadeGameBody{}, 0, &gameRequestValidationError{message: fmt.Sprintf("modify[%d].id does not belong to arcade", i)}
		}
		if entry.GetString("series") != seriesID {
			return UpdateArcadeGameBody{}, 0, &gameRequestValidationError{message: fmt.Sprintf("modify[%d].game must remain in the entry series", i)}
		}
		if revisionChanged(previousByEntry[g.ID], g) {
			changedEntries++
		}
		full.Games[entryIndex] = g
	}

	removeSet := make(map[string]struct{}, len(delta.Remove))
	for i, id := range delta.Remove {
		if _, ok := indexByEntry[id]; !ok {
			return UpdateArcadeGameBody{}, 0, &gameRequestValidationError{message: fmt.Sprintf("remove[%d] is not active", i)}
		}
		removeSet[id] = struct{}{}
	}
	if len(removeSet) > 0 {
		remaining := make([]GameAtomInput, 0, len(full.Games)-len(removeSet))
		for _, g := range full.Games {
			if _, removed := removeSet[strings.TrimSpace(g.ID)]; removed {
				continue
			}
			remaining = append(remaining, g)
		}
		full.Games = remaining
		changedEntries += len(removeSet)
	}

	for i := range delta.Add {
		g := delta.Add[i]
		seriesID, seriesErr := versionSeries(txApp, g.Game)
		if seriesErr != nil || seriesID == "" {
			return UpdateArcadeGameBody{}, 0, &gameRequestValidationError{message: fmt.Sprintf("add[%d].game not found", i)}
		}
		entryID, reuseErr := findReusableGameEntryID(txApp, delta.Arcade, seriesID, g.Cabinet, currentState, reserved)
		if reuseErr != nil {
			return UpdateArcadeGameBody{}, 0, reuseErr
		}
		g.ID = entryID
		full.Games = append(full.Games, g)
		if entryID != "" {
			reserved[entryID] = struct{}{}
		}
		changedEntries++
	}

	if err := validateUpdateGameBody(&full); err != nil {
		return UpdateArcadeGameBody{}, 0, &gameRequestValidationError{message: err.Error()}
	}
	if err := validateGameAtomReferences(txApp, full); err != nil {
		return UpdateArcadeGameBody{}, 0, &gameRequestValidationError{message: err.Error()}
	}
	return full, changedEntries, nil
}

func validateGameAtomReferences(app core.App, body UpdateArcadeGameBody) error {
	for i := range body.Games {
		g := body.Games[i]
		seriesID, err := versionSeries(app, g.Game)
		if err != nil || seriesID == "" {
			return fmt.Errorf("games[%d].game not found", i)
		}
		if g.Cabinet == "" {
			continue
		}
		if _, err := app.FindRecordById(arcadeinternal.CollectionGameCabinet, g.Cabinet); err != nil {
			return fmt.Errorf("games[%d].cabinet not found", i)
		}
		if _, err := app.FindFirstRecordByFilter(
			arcadeinternal.CollectionGameSeriesVersionCabinet,
			"version={:version} && cabinet={:cabinet}",
			dbx.Params{"version": g.Game, "cabinet": g.Cabinet},
		); err != nil {
			return fmt.Errorf("games[%d].cabinet is not supported by game version", i)
		}
	}
	return nil
}

func revisionChanged(previous *core.Record, g GameAtomInput) bool {
	if previous == nil || previous.GetString("version") != g.Game || previous.GetString("cabinet") != g.Cabinet || previous.GetString("location") != g.Location || previous.GetInt("quantity") != g.Quantity {
		return true
	}
	price, tag := gamePriceForComparison(g), any(g.RawTag)
	if tag == nil {
		tag = NormalizeTagForStorage(g.Tag)
	}
	return !arcadeinternal.JSONValueEqual(normalizePriceForComparison(previous.Get("price")), price) || !arcadeinternal.JSONValueEqual(arcadeinternal.NormalizeGameTagPayload(previous.Get("tag")), NormalizeTagForStorage(tag))
}

// gameRevisionSnapshot is intentionally self-contained: the timeline can show
// a meaningful before/after diff even after catalog titles or later revisions
// change. The changelog row's `by` and `created` identify the editor and time.
func gameRevisionSnapshot(revision *core.Record) map[string]any {
	if revision == nil {
		return nil
	}
	return map[string]any{
		"version":  revision.GetString("version"),
		"cabinet":  revision.GetString("cabinet"),
		"location": revision.GetString("location"),
		"quantity": revision.GetInt("quantity"),
		"price":    normalizePriceForComparison(revision.Get("price")),
		"tag":      arcadeinternal.DecodeGameTagPayload(revision.Get("tag")),
	}
}

func versionSeries(app core.App, versionID string) (string, error) {
	rec, err := app.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, versionID)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(rec.GetString("series")), nil
}

func updateArcadeGameTx(txApp core.App, body UpdateArcadeGameBody, createdBy string, validate bool, action, bulkID string) (string, error) {
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
	currentState := strings.TrimSpace(arcadeRec.GetString("game_v2"))
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
	reservedEntryIDs := make(map[string]struct{}, len(previousByEntry))
	for entryID := range previousByEntry {
		reservedEntryIDs[entryID] = struct{}{}
	}
	logItems := make([]map[string]any, 0, len(body.Games))
	for i, g := range body.Games {
		entryID := strings.TrimSpace(g.ID)
		versionSeriesID, seriesErr := versionSeries(txApp, g.Game)
		if seriesErr != nil || versionSeriesID == "" {
			return "", fmt.Errorf("games[%d].game not found", i)
		}
		if g.Cabinet != "" {
			if _, cabinetErr := txApp.FindRecordById(arcadeinternal.CollectionGameCabinet, g.Cabinet); cabinetErr != nil {
				return "", fmt.Errorf("games[%d].cabinet not found", i)
			}
			if _, compatibilityErr := txApp.FindFirstRecordByFilter(
				arcadeinternal.CollectionGameSeriesVersionCabinet,
				"version={:version} && cabinet={:cabinet}",
				dbx.Params{"version": g.Game, "cabinet": g.Cabinet},
			); compatibilityErr != nil {
				return "", fmt.Errorf("games[%d].cabinet is not supported by game version", i)
			}
		}
		var entry *core.Record
		if entryID == "" {
			entryID, err = findReusableGameEntryID(txApp, body.Arcade, versionSeriesID, g.Cabinet, currentState, reservedEntryIDs)
			if err != nil {
				return "", err
			}
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
					return "", fmt.Errorf("games[%d].reusable id not found", i)
				}
			}
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
		reservedEntryIDs[entryID] = struct{}{}
		previous := previousByEntry[entryID]
		revision := core.NewRecord(revisionColl)
		revision.Set("batch", batch.Id)
		revision.Set("entry", entryID)
		revision.Set("version", g.Game)
		revision.Set("cabinet", g.Cabinet)
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
		if previous != nil && !revisionChanged(previous, g) {
			revision.Set("last_modified_at", previous.Get("last_modified_at"))
			revision.Set("last_modified_by", previous.GetString("last_modified_by"))
		} else {
			revision.Set("last_modified_at", now)
			revision.Set("last_modified_by", createdBy)
		}
		if err := txApp.Save(revision); err != nil {
			return "", fmt.Errorf("create game revision %d: %w", i, err)
		}
		kind := "updated"
		if previous == nil {
			kind = "added"
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
	if action == "bulk_version" {
		log["source"] = "bulk_version"
		if bulkID != "" {
			log["bulk_id"] = bulkID
		}
	}
	if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(txApp, arcadeRec.Id, map[string]any{"game_v2": batch.Id}, map[string]any{"game": log}, createdBy); err != nil {
		return "", err
	}
	return batch.Id, nil
}

func UpdateArcadeGameTx(txApp core.App, body UpdateArcadeGameBody, createdBy string) (string, error) {
	return updateArcadeGameTx(txApp, body, createdBy, true, "edit", "")
}
func UpdateArcadeGameTxFromExistingAtoms(txApp core.App, body UpdateArcadeGameBody, createdBy string, action string) (string, error) {
	return updateArcadeGameTx(txApp, body, createdBy, false, action, "")
}

// BulkUpdateArcadeGameTx writes a per-arcade immutable game revision while
// associating every row produced by one administrative request with bulkID.
func BulkUpdateArcadeGameTx(txApp core.App, body UpdateArcadeGameBody, createdBy, bulkID string) (string, error) {
	return updateArcadeGameTx(txApp, body, createdBy, false, "bulk_version", bulkID)
}

func loadChangedGameEntryIDs(app core.App, arcadeID, stateID string) ([]string, error) {
	change, err := app.FindFirstRecordByFilter(
		arcadeinternal.CollectionArcadeChangelog,
		"arcade={:arcade} && changed='game' && to={:state}",
		dbx.Params{"arcade": arcadeID, "state": stateID},
	)
	if err != nil {
		return nil, fmt.Errorf("game changelog not found for state: %w", err)
	}

	encoded, err := json.Marshal(change.Get("log"))
	if err != nil {
		return nil, fmt.Errorf("encode game changelog: %w", err)
	}
	var payload struct {
		Items []struct {
			EntryID    string `json:"entry_id"`
			ChangeType string `json:"change_type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		return nil, fmt.Errorf("decode game changelog: %w", err)
	}

	ids := make([]string, 0, len(payload.Items))
	for _, item := range payload.Items {
		if item.ChangeType == "unchanged" || strings.TrimSpace(item.EntryID) == "" {
			continue
		}
		ids = append(ids, item.EntryID)
	}
	return ids, nil
}

func UpdateArcadeGame(re *core.RequestEvent) error {
	delta, err := parseUpdateGameBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	if err := validateUpdateGameDeltaBody(&delta); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": err.Error()})
	}
	var stateID string
	var materialized UpdateArcadeGameBody
	var changedEntryIDs []string
	var xp userhandler.ExpFeedback
	if err := re.App.RunInTransaction(func(txApp core.App) error {
		arcadeRec, findErr := txApp.FindRecordById(arcadeinternal.CollectionArcade, delta.Arcade)
		if findErr != nil {
			return fmt.Errorf("arcade not found: %w", findErr)
		}
		base, expErr := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if expErr != nil {
			return expErr
		}
		materialized, _, err = materializeUpdateGameDelta(txApp, delta)
		if err != nil {
			return err
		}
		stateID, err = UpdateArcadeGameTx(txApp, materialized, re.Auth.Id)
		if err != nil {
			return err
		}
		changedEntryIDs, err = loadChangedGameEntryIDs(txApp, delta.Arcade, stateID)
		if err != nil {
			return err
		}
		current := base
		if arcadeRec.GetBool("public") {
			current, _, err = userhandler.AwardArcadeGameEditExpTx(txApp, re.Auth.Id, delta.Arcade, changedEntryIDs, base, time.Now().UTC())
			if err != nil {
				return err
			}
		}
		xp = userhandler.BuildExpFeedback(base, current)
		return nil
	}); err != nil {
		status := http.StatusBadGateway
		var validationErr *gameRequestValidationError
		if errors.As(err, &validationErr) {
			status = http.StatusBadRequest
		} else if strings.Contains(err.Error(), "game state conflict") {
			status = http.StatusConflict
		}
		return re.JSON(status, map[string]any{"error": "game update failed", "details": err.Error()})
	}
	gameValue, ok := arcadeinternal.BuildExpandedGameValue(re.App, stateID)
	if !ok {
		gameValue = map[string]any{"id": stateID, "items": []map[string]any{}}
	}
	return re.JSON(http.StatusOK, map[string]any{"arcade": delta.Arcade, "game": gameValue, "count": len(materialized.Games), "xp_feedback": xp})
}
