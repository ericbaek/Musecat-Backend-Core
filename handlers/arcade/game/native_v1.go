package game

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/ericbaek/musecat-backend-core/service/xp"
)

// Native games v1 is a fixed delta contract over the existing immutable game
// state pipeline (arcade_game_id / arcade_game_history_batch /
// arcade_game_history). It never replaces the whole inventory: every add,
// update and removal is explicit, `update.changes` carries only the fields the
// client changed, and untouched entries (including raw price/tag JSON and
// columns this contract does not know) are copied verbatim into the new batch.
//
// Unlike the contacts/GTK contracts, item ids are the durable arcade_game_id
// and stay stable across saves; only `revision` (arcade.game_v2) changes.
//
// The mutation itself is the shared web path: materializeUpdateGameDelta +
// UpdateArcadeGameTx + the game XP grant, inside one transaction.

const nativeGamesCapability = "games.delta.v1"

// nativeGameLocationMax mirrors the arcade_game_history.location schema max.
const nativeGameLocationMax = 500

type nativeGamePatch struct {
	Revision *string                      `json:"revision"`
	Add      []map[string]json.RawMessage `json:"add"`
	Update   []nativeGameUpdate           `json:"update"`
	Remove   []string                     `json:"remove"`
}

type nativeGameUpdate struct {
	ID      string                     `json:"id"`
	Changes map[string]json.RawMessage `json:"changes"`
}

// nativeGameFields is one validated add object or `changes` map. A nil member
// means "not provided"; provided members already passed the web policy checks.
type nativeGameFields struct {
	game     *string
	cabinet  *string
	location *string
	quantity *int
	price    *Price
	tag      *[]TagItem
}

func nativeGameError(status int, code string) *arcadeinternal.NativeEditError {
	return &arcadeinternal.NativeEditError{Status: status, Code: code}
}

func nativeGameBadRequest(code string) *arcadeinternal.NativeEditError {
	return nativeGameError(http.StatusBadRequest, code)
}

// GetNativeGamesV1 binds the current game inventory to one immutable revision.
func GetNativeGamesV1(e *core.RequestEvent) error {
	arcade, err := e.App.FindRecordById(arcadeinternal.CollectionArcade, e.Request.PathValue("id"))
	if err != nil {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "arcade_not_found"})
	}
	if !arcadeinternal.CanWriteArcade(e.Auth, arcade) {
		return e.JSON(http.StatusForbidden, map[string]string{"error": "arcade_write_forbidden"})
	}
	revision := strings.TrimSpace(arcade.GetString("game_v2"))
	revisions, err := loadNativeGameRevisions(e.App, arcade.Id, revision)
	if err != nil {
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "games_unavailable"})
	}
	items := make([]map[string]any, 0, len(revisions))
	for _, row := range revisions {
		items = append(items, nativeGameItem(e.App, row))
	}
	return e.JSON(http.StatusOK, map[string]any{
		"contract_version":      1,
		"required_capabilities": []string{nativeGamesCapability},
		"arcade":                arcade.Id,
		"revision":              revision,
		"items":                 items,
	})
}

// PatchNativeGamesV1 applies explicit add/update/remove operations against one
// base revision through the shared game mutation transaction.
func PatchNativeGamesV1(e *core.RequestEvent) error {
	patch, err := decodeNativeGamePatch(e)
	if err != nil {
		return writeNativeGameError(e, err)
	}
	adds, updates, err := validateNativeGamePatch(patch)
	if err != nil {
		return writeNativeGameError(e, err)
	}

	arcadeID := e.Request.PathValue("id")
	arcade, err := e.App.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "arcade_not_found"})
	}
	if !arcadeinternal.CanWriteArcade(e.Auth, arcade) {
		return e.JSON(http.StatusForbidden, map[string]string{"error": "arcade_write_forbidden"})
	}

	var stateID string
	var count int
	var xpFeedback xp.ExpFeedback
	if err := e.App.RunInTransaction(func(txApp core.App) error {
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		if !arcadeinternal.CanWriteArcade(e.Auth, arcadeRec) {
			return arcadeinternal.ErrArcadeWriteForbidden
		}
		currentState := strings.TrimSpace(arcadeRec.GetString("game_v2"))
		if currentState != *patch.Revision {
			return nativeGameError(http.StatusConflict, "revision_conflict")
		}
		delta, err := mergeNativeGames(txApp, arcadeID, currentState, patch, adds, updates)
		if err != nil {
			return err
		}
		if err := validateUpdateGameDeltaBody(&delta); err != nil {
			return nativeGameCodeForMessage(err.Error())
		}
		base, err := xp.LoadCurrentExp(txApp, e.Auth.Id)
		if err != nil {
			return err
		}
		materialized, changedEntries, err := materializeUpdateGameDelta(txApp, delta)
		if err != nil {
			return err
		}
		if changedEntries == 0 {
			return nativeGameBadRequest("no_changes")
		}
		stateID, err = UpdateArcadeGameTx(txApp, materialized, e.Auth.Id)
		if err != nil {
			return err
		}
		count = len(materialized.Games)
		changedEntryIDs, err := loadChangedGameEntryIDs(txApp, arcadeID, stateID)
		if err != nil {
			return err
		}
		current := base
		if arcadeRec.GetBool("public") {
			current, _, err = xp.AwardArcadeGameEditExpTx(txApp, e.Auth.Id, arcadeID, changedEntryIDs, base, time.Now().UTC())
			if err != nil {
				return err
			}
		}
		xpFeedback = xp.BuildExpFeedback(base, current)
		return nil
	}); err != nil {
		return writeNativeGameError(e, err)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"arcade":      arcadeID,
		"revision":    stateID,
		"count":       count,
		"xp_feedback": xpFeedback,
	})
}

// loadNativeGameRevisions returns the revisions of exactly the given batch in
// stored order, refusing a batch that belongs to another arcade.
func loadNativeGameRevisions(app core.App, arcadeID, stateID string) ([]*core.Record, error) {
	if stateID == "" {
		return nil, nil
	}
	batch, err := app.FindRecordById(arcadeinternal.CollectionArcadeGameRevisionBatch, stateID)
	if err != nil {
		return nil, err
	}
	if batch.GetString("arcade") != arcadeID {
		return nil, fmt.Errorf("game state %s belongs to another arcade", stateID)
	}
	return app.FindRecordsByFilter(arcadeinternal.CollectionArcadeGameRevision, "batch={:batch}", "created", 0, 0, dbx.Params{"batch": stateID})
}

// nativeGameItem renders one revision with raw price/tag JSON plus a read-only
// catalog bundle. Catalog members are null when the catalog record is missing.
func nativeGameItem(app core.App, revision *core.Record) map[string]any {
	entryID := strings.TrimSpace(revision.GetString("entry"))
	series := ""
	if entry, err := app.FindRecordById(arcadeinternal.CollectionArcadeGameEntry, entryID); err == nil {
		series = strings.TrimSpace(entry.GetString("series"))
	}
	versionID := strings.TrimSpace(revision.GetString("version"))
	cabinetID := strings.TrimSpace(revision.GetString("cabinet"))
	catalog := map[string]any{"version": nil, "series": nil, "cabinet": nil}
	if versionID != "" {
		if bundle, err := arcadeinternal.BuildGameSeriesBundle(app, versionID); err == nil {
			catalog["version"], catalog["series"] = bundle["version"], bundle["series"]
		}
	}
	if cabinetID != "" {
		if rec, err := app.FindRecordById(arcadeinternal.CollectionGameCabinet, cabinetID); err == nil {
			catalog["cabinet"] = map[string]any{"id": rec.Id, "en": rec.GetString("en"), "kr": rec.GetString("kr"), "jp": rec.GetString("jp")}
		}
	}
	var price any
	if raw := revision.Get("price"); raw != nil {
		if encoded, err := json.Marshal(raw); err == nil && len(bytes.TrimSpace(encoded)) > 0 && string(bytes.TrimSpace(encoded)) != `""` {
			_ = json.Unmarshal(encoded, &price)
		}
	}
	tag := arcadeinternal.NormalizeGameTagPayload(revision.Get("tag"))
	if tag == nil {
		tag = []any{}
	}
	return map[string]any{
		"id":       entryID,
		"series":   series,
		"game":     versionID,
		"cabinet":  cabinetID,
		"location": revision.GetString("location"),
		"quantity": revision.GetInt("quantity"),
		"price":    price,
		"tag":      tag,
		"updated":  revision.GetString("last_modified_at"),
		"catalog":  catalog,
	}
}

// decodeNativeGamePatch is deliberately local: a decode failure is reported as
// a non-nil error so the handler can never continue into a mutation after a 400.
func decodeNativeGamePatch(e *core.RequestEvent) (nativeGamePatch, error) {
	var patch nativeGamePatch
	data, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return patch, nativeGameBadRequest("invalid_patch")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		return patch, nativeGameBadRequest("invalid_patch")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return patch, nativeGameBadRequest("invalid_patch")
	}
	for _, add := range patch.Add {
		if add == nil {
			return patch, nativeGameBadRequest("invalid_patch")
		}
	}
	return patch, nil
}

func writeNativeGameError(e *core.RequestEvent, err error) error {
	var nativeErr *arcadeinternal.NativeEditError
	if errors.As(err, &nativeErr) {
		return e.JSON(nativeErr.Status, map[string]string{"error": nativeErr.Code})
	}
	if errors.Is(err, arcadeinternal.ErrArcadeWriteForbidden) {
		return e.JSON(http.StatusForbidden, map[string]string{"error": "arcade_write_forbidden"})
	}
	if errors.Is(err, ErrGameStateConflict) {
		return e.JSON(http.StatusConflict, map[string]string{"error": "revision_conflict"})
	}
	var validationErr *gameRequestValidationError
	if errors.As(err, &validationErr) {
		return e.JSON(http.StatusBadRequest, map[string]string{"error": nativeGameCodeForMessage(validationErr.message).Code})
	}
	return e.JSON(http.StatusBadGateway, map[string]any{
		"error":   "transaction failed",
		"details": err.Error(),
	})
}

// nativeGameCodeForMessage maps the shared pipeline's validation messages to
// the stable native codes. The messages are the web `PUT /arcade/game`
// `details` strings produced by validateUpdateGameDeltaBody,
// materializeUpdateGameDelta and validateUpdateGameBody.
func nativeGameCodeForMessage(message string) *arcadeinternal.NativeEditError {
	switch {
	case strings.Contains(message, "must remain in the entry series"):
		return nativeGameBadRequest("series_mismatch")
	case strings.Contains(message, "cabinet is not supported"):
		return nativeGameBadRequest("cabinet_not_supported")
	case strings.Contains(message, "cabinet not found"):
		return nativeGameBadRequest("invalid_cabinet")
	case strings.Contains(message, "game not found"):
		return nativeGameBadRequest("invalid_game_version")
	case strings.Contains(message, "duplicate an active revision"):
		return nativeGameBadRequest("duplicate_game")
	case strings.Contains(message, ".price"):
		return nativeGameBadRequest("invalid_price")
	case strings.Contains(message, ".tag"):
		return nativeGameBadRequest("invalid_tag")
	case strings.Contains(message, "quantity"):
		return nativeGameBadRequest("invalid_quantity")
	case strings.Contains(message, ".game is required"):
		return nativeGameBadRequest("invalid_game_version")
	case strings.Contains(message, "is not active"), strings.Contains(message, ".id"), strings.Contains(message, "remove["):
		return nativeGameBadRequest("invalid_game_id")
	default:
		return nativeGameBadRequest("invalid_patch")
	}
}

// validateNativeGamePatch performs every check that needs no database state and
// returns the decoded add objects and update changes in request order.
func validateNativeGamePatch(patch nativeGamePatch) ([]nativeGameFields, []nativeGameFields, error) {
	if patch.Revision == nil || len(patch.Add)+len(patch.Update)+len(patch.Remove) == 0 {
		return nil, nil, nativeGameBadRequest("revision_and_operations_required")
	}
	adds := make([]nativeGameFields, 0, len(patch.Add))
	for _, add := range patch.Add {
		fields, err := parseNativeGameFields(add)
		if err != nil {
			return nil, nil, err
		}
		if fields.game == nil {
			return nil, nil, nativeGameBadRequest("invalid_game_version")
		}
		if fields.quantity == nil {
			return nil, nil, nativeGameBadRequest("invalid_quantity")
		}
		if fields.price == nil {
			return nil, nil, nativeGameBadRequest("invalid_price")
		}
		adds = append(adds, fields)
	}
	seen := map[string]struct{}{}
	updates := make([]nativeGameFields, 0, len(patch.Update))
	for _, update := range patch.Update {
		id := strings.TrimSpace(update.ID)
		if id == "" || len(update.Changes) == 0 {
			return nil, nil, nativeGameBadRequest("invalid_game_id")
		}
		if _, dup := seen[id]; dup {
			return nil, nil, nativeGameBadRequest("invalid_game_id")
		}
		seen[id] = struct{}{}
		fields, err := parseNativeGameFields(update.Changes)
		if err != nil {
			return nil, nil, err
		}
		updates = append(updates, fields)
	}
	for _, id := range patch.Remove {
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, nil, nativeGameBadRequest("invalid_game_id")
		}
		if _, dup := seen[id]; dup {
			return nil, nil, nativeGameBadRequest("invalid_game_id")
		}
		seen[id] = struct{}{}
	}
	return adds, updates, nil
}

// parseNativeGameFields validates one add object or `changes` map member by
// member. Unknown keys anywhere (object, price, price list rows, tag items) are
// rejected instead of being dropped; values must have the documented JSON type.
func parseNativeGameFields(raw map[string]json.RawMessage) (nativeGameFields, error) {
	var fields nativeGameFields
	for key, value := range raw {
		switch key {
		case "game":
			text, ok := decodeNativeJSONString(value)
			text = strings.TrimSpace(text)
			if !ok || text == "" {
				return fields, nativeGameBadRequest("invalid_game_version")
			}
			fields.game = &text
		case "cabinet":
			text, ok := decodeNativeJSONString(value)
			if !ok {
				return fields, nativeGameBadRequest("invalid_cabinet")
			}
			text = strings.TrimSpace(text)
			fields.cabinet = &text
		case "location":
			text, ok := decodeNativeJSONString(value)
			if !ok || utf8.RuneCountInString(text) > nativeGameLocationMax {
				return fields, nativeGameBadRequest("invalid_location")
			}
			fields.location = &text
		case "quantity":
			var number *float64
			if json.Unmarshal(value, &number) != nil || number == nil || *number < 1 || *number != math.Trunc(*number) || *number > math.MaxInt32 {
				return fields, nativeGameBadRequest("invalid_quantity")
			}
			quantity := int(*number)
			fields.quantity = &quantity
		case "price":
			price, err := parseNativeGamePrice(value)
			if err != nil {
				return fields, err
			}
			fields.price = &price
		case "tag":
			tags, err := parseNativeGameTags(value)
			if err != nil {
				return fields, err
			}
			fields.tag = &tags
		default:
			return fields, nativeGameBadRequest("unsupported_field")
		}
	}
	return fields, nil
}

// parseNativeGamePrice accepts exactly the web price object (currency, type,
// list, accept) and validates it with the shared validatePrice policy.
func parseNativeGamePrice(raw json.RawMessage) (Price, error) {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return Price{}, nativeGameBadRequest("invalid_price")
	}
	for key := range members {
		switch key {
		case "currency", "type", "list", "accept":
		default:
			return Price{}, nativeGameBadRequest("unsupported_field")
		}
	}
	for _, key := range []string{"currency", "type", "list", "accept"} {
		if _, present := members[key]; !present {
			return Price{}, nativeGameBadRequest("invalid_price")
		}
	}
	if _, ok := decodeNativeJSONString(members["currency"]); !ok {
		return Price{}, nativeGameBadRequest("invalid_price")
	}
	if _, ok := decodeNativeJSONString(members["type"]); !ok {
		return Price{}, nativeGameBadRequest("invalid_price")
	}
	var accept []string
	if json.Unmarshal(members["accept"], &accept) != nil || accept == nil {
		return Price{}, nativeGameBadRequest("invalid_price")
	}
	var list []json.RawMessage
	if json.Unmarshal(members["list"], &list) != nil || list == nil {
		return Price{}, nativeGameBadRequest("invalid_price")
	}
	for _, row := range list {
		var rowMembers map[string]json.RawMessage
		if json.Unmarshal(row, &rowMembers) != nil || rowMembers == nil {
			// Strings (custom lines) and other scalars are judged by the shared decoder.
			continue
		}
		for key := range rowMembers {
			switch key {
			case "title", "value", "mode_key", "represent":
			default:
				return Price{}, nativeGameBadRequest("unsupported_field")
			}
		}
	}
	var price Price
	if err := json.Unmarshal(raw, &price); err != nil {
		return Price{}, nativeGameBadRequest("invalid_price")
	}
	if err := validatePrice(price); err != nil {
		return Price{}, nativeGameBadRequest("invalid_price")
	}
	return price, nil
}

// parseNativeGameTags accepts exactly the web tag array ({category, note} rows)
// and validates it with the shared ValidateTagItems policy.
func parseNativeGameTags(raw json.RawMessage) ([]TagItem, error) {
	var rows []map[string]json.RawMessage
	if json.Unmarshal(raw, &rows) != nil || rows == nil {
		return nil, nativeGameBadRequest("invalid_tag")
	}
	for _, row := range rows {
		if row == nil {
			return nil, nativeGameBadRequest("invalid_tag")
		}
		for key := range row {
			switch key {
			case "category", "note":
			default:
				return nil, nativeGameBadRequest("unsupported_field")
			}
		}
		for _, key := range []string{"category", "note"} {
			if _, ok := decodeNativeJSONString(row[key]); !ok {
				return nil, nativeGameBadRequest("invalid_tag")
			}
		}
	}
	var tags []TagItem
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil, nativeGameBadRequest("invalid_tag")
	}
	if tags == nil {
		tags = []TagItem{}
	}
	if err := ValidateTagItems(tags); err != nil {
		return nil, nativeGameBadRequest("invalid_tag")
	}
	return tags, nil
}

// mergeNativeGames resolves the patch against the base revision into the web
// delta body. Updates start from the stored revision (raw price/tag included)
// and only the provided members are replaced; adds are complete objects.
func mergeNativeGames(app core.App, arcadeID, currentState string, patch nativeGamePatch, adds, updates []nativeGameFields) (UpdateArcadeGameDeltaBody, error) {
	delta := UpdateArcadeGameDeltaBody{
		Arcade:      arcadeID,
		BaseStateID: currentState,
		Add:         make([]GameAtomInput, 0, len(adds)),
		Modify:      make([]GameAtomInput, 0, len(updates)),
		Remove:      make([]string, 0, len(patch.Remove)),
	}
	currentByEntry := map[string]GameAtomInput{}
	if currentState != "" {
		if _, err := loadNativeGameRevisions(app, arcadeID, currentState); err != nil {
			return delta, nativeGameError(http.StatusBadGateway, "games_unavailable")
		}
		current, err := BuildUpdateBodyFromCurrentState(app, arcadeID)
		if err != nil {
			return delta, nativeGameError(http.StatusBadGateway, "games_unavailable")
		}
		for _, g := range current.Games {
			currentByEntry[strings.TrimSpace(g.ID)] = g
		}
	}
	for _, id := range patch.Remove {
		id = strings.TrimSpace(id)
		if _, ok := currentByEntry[id]; !ok {
			return delta, nativeGameBadRequest("invalid_game_id")
		}
		delta.Remove = append(delta.Remove, id)
	}
	for i, update := range patch.Update {
		id := strings.TrimSpace(update.ID)
		g, ok := currentByEntry[id]
		if !ok {
			return delta, nativeGameBadRequest("invalid_game_id")
		}
		fields := updates[i]
		if fields.game != nil {
			g.Game = *fields.game
		}
		if fields.cabinet != nil {
			g.Cabinet = *fields.cabinet
		}
		if fields.location != nil {
			g.Location = *fields.location
		}
		if fields.quantity != nil {
			g.Quantity = *fields.quantity
		}
		if fields.price != nil {
			g.Price, g.RawPrice = *fields.price, nil
		}
		if fields.tag != nil {
			g.Tag, g.RawTag = *fields.tag, nil
		}
		delta.Modify = append(delta.Modify, g)
	}
	for _, fields := range adds {
		g := GameAtomInput{Game: *fields.game, Quantity: *fields.quantity, Price: *fields.price, Tag: []TagItem{}}
		if fields.cabinet != nil {
			g.Cabinet = *fields.cabinet
		}
		if fields.location != nil {
			g.Location = *fields.location
		}
		if fields.tag != nil {
			g.Tag = *fields.tag
		}
		delta.Add = append(delta.Add, g)
	}
	return delta, nil
}

func decodeNativeJSONString(raw json.RawMessage) (string, bool) {
	var value *string
	if raw == nil || json.Unmarshal(raw, &value) != nil || value == nil {
		return "", false
	}
	return *value, true
}
