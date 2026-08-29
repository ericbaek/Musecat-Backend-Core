package gamecatalog

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeversion "github.com/ericbaek/musecat-backend-core/handlers/arcade/version"
)

const (
	maxReasonRunes = 500
	minReasonRunes = 10
	maxNameRunes   = 120
	maxShortRunes  = 60
	maxPriceBytes  = 64 << 10

	collectionArcade                   = "arcade"
	collectionArcadeCampaign           = "arcade_campaign"
	collectionArcadeGameRevision       = "arcade_game_history"
	collectionGameCabinet              = "game_cabinet"
	collectionGameCatalogChangelog     = "game_catalog_changelog"
	collectionGameManufacturer         = "game_manufacturer"
	collectionGameSeries               = "game_series"
	collectionGameSeriesVersion        = "game_series_version"
	collectionGameSeriesVersionCabinet = "game_series_version_cabinet"
)

type mutationBody struct {
	Entity           string         `json:"entity"`
	ID               string         `json:"id"`
	OperationID      string         `json:"operation_id"`
	Reason           string         `json:"reason"`
	ExpectedRevision int            `json:"expected_revision"`
	Values           map[string]any `json:"values"`
	ChangeID         string         `json:"change_id"`
}

type apiError struct {
	status  int
	message string
	details any
}

func (e *apiError) Error() string { return e.message }

var entityCollections = map[string]string{
	"series":        collectionGameSeries,
	"version":       collectionGameSeriesVersion,
	"cabinet":       collectionGameCabinet,
	"compatibility": collectionGameSeriesVersionCabinet,
	"manufacturer":  collectionGameManufacturer,
}

func GetCatalog(re *core.RequestEvent) error {
	locale := strings.TrimSpace(re.Request.URL.Query().Get("locale"))
	if locale != "en-US" && locale != "ko-KR" && locale != "ja-JP" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "locale must be one of en-US, ko-KR, ja-JP"})
	}

	response := map[string]any{}
	for entity, collection := range entityCollections {
		if entity == "manufacturer" {
			continue
		}
		sort := "created"
		if entity == "version" {
			sort = "-released_on"
		}
		records, err := re.App.FindRecordsByFilter(collection, "", sort, 0, 0, nil)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load game catalog", "details": err.Error()})
		}
		items := make([]map[string]any, 0, len(records))
		for _, record := range records {
			items = append(items, snapshot(entity, record))
		}
		response[plural(entity)] = items
	}

	manufacturers, err := re.App.FindRecordsByFilter(collectionGameManufacturer, "", "created", 0, 0, nil)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load manufacturers", "details": err.Error()})
	}
	manufacturerItems := make([]map[string]any, 0, len(manufacturers))
	for _, record := range manufacturers {
		item := snapshot("manufacturer", record)
		item["name"] = localizedName(record, locale)
		manufacturerItems = append(manufacturerItems, item)
	}
	response["manufacturers"] = manufacturerItems

	entity := normalizeEntity(re.Request.URL.Query().Get("entity"))
	id := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	if entity != "" && id != "" {
		blockers, blockerErr := blockingReferences(re.App, entity, id)
		if blockerErr != nil {
			return writeError(re, blockerErr)
		}
		response["blocking_references"] = blockers
	}
	return re.JSON(http.StatusOK, response)
}

func ListChanges(re *core.RequestEvent) error {
	page := queryInt(re, "page", 1)
	perPage := queryInt(re, "per_page", 50)
	if perPage < 1 || perPage > 100 {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "per_page must be between 1 and 100"})
	}
	filters := []string{}
	params := dbx.Params{}
	for query, field := range map[string]string{"entity": "entity_type", "entity_id": "entity_id", "actor": "actor", "action": "action"} {
		if value := strings.TrimSpace(re.Request.URL.Query().Get(query)); value != "" {
			filters = append(filters, field+"={:"+field+"}")
			params[field] = value
		}
	}
	result, err := re.App.FindRecordsByFilter(
		collectionGameCatalogChangelog,
		strings.Join(filters, " && "),
		"-created",
		perPage,
		(page-1)*perPage,
		params,
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to list catalog changes", "details": err.Error()})
	}
	items := make([]map[string]any, 0, len(result))
	for _, record := range result {
		items = append(items, changeSnapshot(record))
	}
	return re.JSON(http.StatusOK, map[string]any{"page": page, "per_page": perPage, "items": items})
}

func Create(re *core.RequestEvent) error  { return mutate(re, "create") }
func Update(re *core.RequestEvent) error  { return mutate(re, "update") }
func Archive(re *core.RequestEvent) error { return mutate(re, "archive") }
func Restore(re *core.RequestEvent) error { return mutate(re, "restore") }
func Revert(re *core.RequestEvent) error  { return mutate(re, "revert") }

func mutate(re *core.RequestEvent, action string) error {
	body, payloadHash, err := parseMutationBody(re)
	if err != nil {
		return writeError(re, err)
	}
	if action == "revert" {
		return revert(re, body, payloadHash)
	}
	entity := normalizeEntity(body.Entity)
	collection := entityCollections[entity]
	if collection == "" {
		return writeError(re, &apiError{status: http.StatusBadRequest, message: "entity must be series, version, cabinet, compatibility, or manufacturer"})
	}

	if replay, replayErr := findReplay(re.App, body.OperationID, payloadHash, re.Auth.Id); replay != nil || replayErr != nil {
		if replayErr != nil {
			return writeError(re, replayErr)
		}
		return re.JSON(http.StatusOK, map[string]any{"item": replay.Get("after"), "change": changeSnapshot(replay), "replayed": true})
	}

	var item map[string]any
	var change *core.Record
	err = re.App.RunInTransaction(func(tx core.App) error {
		var record *core.Record
		if action == "create" {
			catalogCollection, findErr := tx.FindCollectionByNameOrId(collection)
			if findErr != nil {
				return findErr
			}
			record = core.NewRecord(catalogCollection)
			record.Set("revision", 1)
		} else {
			if len(strings.TrimSpace(body.ID)) != 15 {
				return &apiError{status: http.StatusBadRequest, message: "id must be a valid record id"}
			}
			var findErr error
			record, findErr = tx.FindRecordById(collection, body.ID)
			if findErr != nil {
				return &apiError{status: http.StatusNotFound, message: "catalog item not found"}
			}
			if body.ExpectedRevision < 1 || revision(record) != body.ExpectedRevision {
				return &apiError{status: http.StatusConflict, message: "catalog revision conflict", details: map[string]any{"current_revision": revision(record)}}
			}
		}

		before := snapshot(entity, record)
		switch action {
		case "create", "update":
			if err := applyValues(tx, entity, record, body.Values, action == "create"); err != nil {
				return err
			}
			if action == "update" {
				record.Set("revision", revision(record)+1)
			}
		case "archive":
			if record.GetBool("archived") {
				return &apiError{status: http.StatusConflict, message: "catalog item is already archived"}
			}
			blockers, blockerErr := blockingReferences(tx, entity, record.Id)
			if blockerErr != nil {
				return blockerErr
			}
			if len(blockers) > 0 {
				return &apiError{status: http.StatusConflict, message: "catalog item is still in use", details: map[string]any{"blocking_references": blockers}}
			}
			record.Set("archived", true)
			record.Set("archived_at", time.Now().UTC())
			record.Set("archived_by", re.Auth.Id)
			record.Set("revision", revision(record)+1)
		case "restore":
			if !record.GetBool("archived") {
				return &apiError{status: http.StatusConflict, message: "catalog item is not archived"}
			}
			if err := ensureRestorable(tx, entity, record); err != nil {
				return err
			}
			record.Set("archived", false)
			record.Set("archived_at", nil)
			record.Set("archived_by", "")
			record.Set("revision", revision(record)+1)
		}

		if err := tx.Save(record); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return &apiError{status: http.StatusConflict, message: "catalog item conflicts with an existing record"}
			}
			return fmt.Errorf("save catalog item: %w", err)
		}
		after := snapshot(entity, record)
		var auditErr error
		change, auditErr = saveChange(tx, re.Auth, body, payloadHash, action, entity, record.Id, beforeForAction(action, before), after, "")
		if auditErr != nil {
			return auditErr
		}
		item = after
		return nil
	})
	if err != nil {
		return writeError(re, err)
	}
	return re.JSON(http.StatusOK, map[string]any{"item": item, "change": changeSnapshot(change)})
}

func revert(re *core.RequestEvent, body mutationBody, payloadHash string) error {
	if len(strings.TrimSpace(body.ChangeID)) != 15 {
		return writeError(re, &apiError{status: http.StatusBadRequest, message: "change_id must be a valid record id"})
	}
	if replay, replayErr := findReplay(re.App, body.OperationID, payloadHash, re.Auth.Id); replay != nil || replayErr != nil {
		if replayErr != nil {
			return writeError(re, replayErr)
		}
		return re.JSON(http.StatusOK, map[string]any{"item": replay.Get("after"), "change": changeSnapshot(replay), "replayed": true})
	}

	var item map[string]any
	var savedChange *core.Record
	err := re.App.RunInTransaction(func(tx core.App) error {
		change, err := tx.FindRecordById(collectionGameCatalogChangelog, body.ChangeID)
		if err != nil {
			return &apiError{status: http.StatusNotFound, message: "catalog change not found"}
		}
		entity := normalizeEntity(change.GetString("entity_type"))
		collection := entityCollections[entity]
		record, err := tx.FindRecordById(collection, change.GetString("entity_id"))
		if err != nil {
			return &apiError{status: http.StatusNotFound, message: "catalog item not found"}
		}
		if body.ExpectedRevision < 1 || revision(record) != body.ExpectedRevision {
			return &apiError{status: http.StatusConflict, message: "catalog revision conflict", details: map[string]any{"current_revision": revision(record)}}
		}
		before := snapshot(entity, record)
		target, _ := change.Get("before").(map[string]any)
		if target == nil || len(target) == 0 {
			blockers, blockerErr := blockingReferences(tx, entity, record.Id)
			if blockerErr != nil {
				return blockerErr
			}
			if len(blockers) > 0 {
				return &apiError{status: http.StatusConflict, message: "created item cannot be reverted while in use", details: map[string]any{"blocking_references": blockers}}
			}
			record.Set("archived", true)
			record.Set("archived_at", time.Now().UTC())
			record.Set("archived_by", re.Auth.Id)
		} else {
			if archived, _ := target["archived"].(bool); archived && !record.GetBool("archived") {
				blockers, blockerErr := blockingReferences(tx, entity, record.Id)
				if blockerErr != nil {
					return blockerErr
				}
				if len(blockers) > 0 {
					return &apiError{status: http.StatusConflict, message: "catalog change cannot be reverted while item is in use", details: map[string]any{"blocking_references": blockers}}
				}
			}
			if err := applySnapshot(tx, entity, record, target); err != nil {
				return err
			}
		}
		record.Set("revision", revision(record)+1)
		if err := tx.Save(record); err != nil {
			return fmt.Errorf("save reverted catalog item: %w", err)
		}
		after := snapshot(entity, record)
		savedChange, err = saveChange(tx, re.Auth, body, payloadHash, "revert", entity, record.Id, before, after, change.Id)
		if err != nil {
			return err
		}
		item = after
		return nil
	})
	if err != nil {
		return writeError(re, err)
	}
	return re.JSON(http.StatusOK, map[string]any{"item": item, "change": changeSnapshot(savedChange)})
}

func parseMutationBody(re *core.RequestEvent) (mutationBody, string, error) {
	var body mutationBody
	decoder := json.NewDecoder(io.LimitReader(re.Request.Body, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return body, "", &apiError{status: http.StatusBadRequest, message: "invalid JSON body", details: err.Error()}
	}
	body.Entity = normalizeEntity(body.Entity)
	body.ID = strings.TrimSpace(body.ID)
	body.OperationID = strings.TrimSpace(body.OperationID)
	body.Reason = strings.TrimSpace(body.Reason)
	body.ChangeID = strings.TrimSpace(body.ChangeID)
	if _, err := uuid.Parse(body.OperationID); err != nil {
		return body, "", &apiError{status: http.StatusBadRequest, message: "operation_id must be a UUID"}
	}
	reasonLength := utf8.RuneCountInString(body.Reason)
	if reasonLength < minReasonRunes || reasonLength > maxReasonRunes {
		return body, "", &apiError{status: http.StatusBadRequest, message: "reason must be between 10 and 500 characters"}
	}
	canonical, _ := json.Marshal(body)
	hash := sha256.Sum256(canonical)
	return body, hex.EncodeToString(hash[:]), nil
}

func applyValues(app core.App, entity string, record *core.Record, values map[string]any, creating bool) error {
	if values == nil {
		return &apiError{status: http.StatusBadRequest, message: "values are required"}
	}
	allowed := map[string][]string{
		"series":        {"series_number", "en", "kr", "jp", "en_short", "kr_short", "jp_short", "manufacturer", "hide_at"},
		"version":       {"series", "released_on", "en", "kr", "jp", "price_default", "hide_at"},
		"cabinet":       {"series", "en", "kr", "jp"},
		"compatibility": {"version", "cabinet", "price_default"},
		"manufacturer":  {"en", "kr", "jp"},
	}[entity]
	if err := ensureKeys(values, allowed); err != nil {
		return err
	}

	switch entity {
	case "manufacturer":
		return applyLocalizedNames(record, values, true)
	case "series":
		if err := applyLocalizedNames(record, values, true); err != nil {
			return err
		}
		for _, field := range []string{"en_short", "kr_short", "jp_short"} {
			value := optionalText(values[field])
			if utf8.RuneCountInString(value) > maxShortRunes {
				return &apiError{status: http.StatusBadRequest, message: field + " must be at most 60 characters"}
			}
			record.Set(field, value)
		}
		number, ok := numberValue(values["series_number"])
		if !ok || number < 1 || number != float64(int(number)) {
			return &apiError{status: http.StatusBadRequest, message: "series_number must be a positive integer"}
		}
		record.Set("seriesNumber", int(number))
		manufacturer := optionalText(values["manufacturer"])
		if manufacturer != "" {
			parent, err := app.FindRecordById(collectionGameManufacturer, manufacturer)
			if err != nil || parent.GetBool("archived") {
				return &apiError{status: http.StatusConflict, message: "manufacturer must be active"}
			}
		}
		record.Set("manufacturer", manufacturer)
		record.Set("hide_at", stringSlice(values["hide_at"]))
	case "version":
		if err := applyLocalizedNames(record, values, true); err != nil {
			return err
		}
		series := optionalText(values["series"])
		if creating {
			if len(series) != 15 {
				return &apiError{status: http.StatusBadRequest, message: "series must be a valid record id"}
			}
			parent, err := app.FindRecordById(collectionGameSeries, series)
			if err != nil || parent.GetBool("archived") {
				return &apiError{status: http.StatusConflict, message: "series must be active"}
			}
			record.Set("series", series)
		} else if series != "" && series != record.GetString("series") {
			return &apiError{status: http.StatusBadRequest, message: "version series is immutable"}
		}
		releasedOn := optionalText(values["released_on"])
		if len(releasedOn) != 10 {
			return &apiError{status: http.StatusBadRequest, message: "released_on must use YYYY-MM-DD"}
		}
		record.Set("released_on", releasedOn)
		if err := validatePrice(values["price_default"], false); err != nil {
			return err
		}
		record.Set("price_default", values["price_default"])
		record.Set("hide_at", stringSlice(values["hide_at"]))
	case "cabinet":
		if err := applyLocalizedNames(record, values, true); err != nil {
			return err
		}
		series := optionalText(values["series"])
		if creating {
			parent, err := app.FindRecordById(collectionGameSeries, series)
			if err != nil || parent.GetBool("archived") {
				return &apiError{status: http.StatusConflict, message: "cabinet series must be active"}
			}
			record.Set("series", series)
		} else if series != "" && series != record.GetString("series") {
			return &apiError{status: http.StatusBadRequest, message: "cabinet series is immutable"}
		}
		return nil
	case "compatibility":
		versionID := optionalText(values["version"])
		cabinetID := optionalText(values["cabinet"])
		if creating {
			version, err := app.FindRecordById(collectionGameSeriesVersion, versionID)
			if err != nil || version.GetBool("archived") {
				return &apiError{status: http.StatusConflict, message: "version must be active"}
			}
			cabinet, err := app.FindRecordById(collectionGameCabinet, cabinetID)
			if err != nil || cabinet.GetBool("archived") {
				return &apiError{status: http.StatusConflict, message: "cabinet must be active"}
			}
			if cabinet.GetString("series") != version.GetString("series") {
				return &apiError{status: http.StatusConflict, message: "cabinet must belong to the version series"}
			}
			record.Set("version", versionID)
			record.Set("cabinet", cabinetID)
		} else if (versionID != "" && versionID != record.GetString("version")) || (cabinetID != "" && cabinetID != record.GetString("cabinet")) {
			return &apiError{status: http.StatusBadRequest, message: "compatibility version and cabinet are immutable"}
		}
		if err := validatePrice(values["price_default"], true); err != nil {
			return err
		}
		record.Set("price_default", values["price_default"])
	}
	return nil
}

func applySnapshot(app core.App, entity string, record *core.Record, value map[string]any) error {
	values := map[string]any{}
	for _, field := range map[string][]string{
		"series":        {"series_number", "en", "kr", "jp", "en_short", "kr_short", "jp_short", "manufacturer", "hide_at"},
		"version":       {"series", "released_on", "en", "kr", "jp", "price_default", "hide_at"},
		"cabinet":       {"series", "en", "kr", "jp"},
		"compatibility": {"version", "cabinet", "price_default"},
		"manufacturer":  {"en", "kr", "jp"},
	}[entity] {
		values[field] = value[field]
	}
	if err := applyValues(app, entity, record, values, false); err != nil {
		return err
	}
	archived, _ := value["archived"].(bool)
	if !archived {
		if err := ensureRestorable(app, entity, record); err != nil {
			return err
		}
	}
	record.Set("archived", archived)
	if archived {
		record.Set("archived_at", value["archived_at"])
		record.Set("archived_by", optionalText(value["archived_by"]))
	} else {
		record.Set("archived_at", nil)
		record.Set("archived_by", "")
	}
	return nil
}

func applyLocalizedNames(record *core.Record, values map[string]any, required bool) error {
	for _, field := range []string{"en", "kr", "jp"} {
		value := optionalText(values[field])
		length := utf8.RuneCountInString(value)
		if required && length == 0 {
			return &apiError{status: http.StatusBadRequest, message: field + " is required"}
		}
		if length > maxNameRunes {
			return &apiError{status: http.StatusBadRequest, message: field + " must be at most 120 characters"}
		}
		record.Set(field, value)
	}
	return nil
}

func validatePrice(value any, nullable bool) error {
	if value == nil && nullable {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maxPriceBytes {
		return &apiError{status: http.StatusBadRequest, message: "price_default must be valid JSON up to 64 KiB"}
	}
	if !nullable {
		if _, ok := value.(map[string]any); !ok {
			return &apiError{status: http.StatusBadRequest, message: "price_default must be a JSON object"}
		}
	}
	if err := arcadeversion.ValidatePriceDefaultValue(value); err != nil {
		return &apiError{status: http.StatusBadRequest, message: "price_default is invalid", details: err.Error()}
	}
	return nil
}

func blockingReferences(app core.App, entity, id string) ([]map[string]any, error) {
	blockers := []map[string]any{}
	add := func(kind string, count int) {
		if count > 0 {
			blockers = append(blockers, map[string]any{"kind": kind, "count": count})
		}
	}

	switch entity {
	case "manufacturer":
		children, err := app.FindRecordsByFilter(collectionGameSeries, "manufacturer={:id} && archived=false", "", 0, 0, dbx.Params{"id": id})
		if err != nil {
			return nil, err
		}
		add("active_game_series", len(children))
	case "series":
		children, err := app.FindRecordsByFilter(collectionGameSeriesVersion, "series={:id} && archived=false", "", 0, 0, dbx.Params{"id": id})
		if err != nil {
			return nil, err
		}
		add("active_versions", len(children))
		cabinets, err := app.FindRecordsByFilter(collectionGameCabinet, "series={:id} && archived=false", "", 0, 0, dbx.Params{"id": id})
		if err != nil {
			return nil, err
		}
		add("active_cabinets", len(cabinets))
	case "version":
		children, err := app.FindRecordsByFilter(collectionGameSeriesVersionCabinet, "version={:id} && archived=false", "", 0, 0, dbx.Params{"id": id})
		if err != nil {
			return nil, err
		}
		add("active_compatibilities", len(children))
	case "cabinet":
		children, err := app.FindRecordsByFilter(collectionGameSeriesVersionCabinet, "cabinet={:id} && archived=false", "", 0, 0, dbx.Params{"id": id})
		if err != nil {
			return nil, err
		}
		add("active_compatibilities", len(children))
	}

	current, err := currentGameRevisions(app)
	if err != nil {
		return nil, err
	}
	currentCount := 0
	for _, revision := range current {
		versionID := revision.GetString("version")
		cabinetID := revision.GetString("cabinet")
		matches := entity == "version" && versionID == id || entity == "cabinet" && cabinetID == id || entity == "compatibility" && revisionMatchesCompatibility(app, versionID, cabinetID, id)
		if entity == "series" {
			if version, findErr := app.FindRecordById(collectionGameSeriesVersion, versionID); findErr == nil && version.GetString("series") == id {
				matches = true
			}
		}
		if matches {
			currentCount++
		}
	}
	add("current_arcade_installations", currentCount)

	campaignCount, err := activeCampaignReferences(app, entity, id)
	if err != nil {
		return nil, err
	}
	add("active_campaigns", campaignCount)
	return blockers, nil
}

func currentGameRevisions(app core.App) ([]*core.Record, error) {
	arcades, err := app.FindRecordsByFilter(collectionArcade, "game_v2 != ''", "", 0, 0, nil)
	if err != nil {
		return nil, err
	}
	batches := map[string]struct{}{}
	for _, arcade := range arcades {
		if id := arcade.GetString("game_v2"); id != "" {
			batches[id] = struct{}{}
		}
	}
	revisions, err := app.FindRecordsByFilter(collectionArcadeGameRevision, "", "", 0, 0, nil)
	if err != nil {
		return nil, err
	}
	current := make([]*core.Record, 0)
	for _, revision := range revisions {
		if _, ok := batches[revision.GetString("batch")]; ok {
			current = append(current, revision)
		}
	}
	return current, nil
}

func revisionMatchesCompatibility(app core.App, versionID, cabinetID, compatibilityID string) bool {
	compatibility, err := app.FindRecordById(collectionGameSeriesVersionCabinet, compatibilityID)
	return err == nil && compatibility.GetString("version") == versionID && compatibility.GetString("cabinet") == cabinetID
}

func activeCampaignReferences(app core.App, entity, id string) (int, error) {
	campaigns, err := app.FindRecordsByFilter(collectionArcadeCampaign, "status != 'ended'", "", 0, 0, nil)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, campaign := range campaigns {
		versions := []string{campaign.GetString("from_version"), campaign.GetString("to_version")}
		cabinets := campaign.GetStringSlice("cabinets")
		matches := false
		switch entity {
		case "version":
			matches = contains(versions, id)
		case "cabinet":
			matches = campaign.GetString("cabinet_scope") == "all" || contains(cabinets, id)
		case "series":
			for _, versionID := range versions {
				if version, findErr := app.FindRecordById(collectionGameSeriesVersion, versionID); findErr == nil && version.GetString("series") == id {
					matches = true
				}
			}
		case "compatibility":
			compatibility, findErr := app.FindRecordById(collectionGameSeriesVersionCabinet, id)
			if findErr == nil && contains(versions, compatibility.GetString("version")) {
				matches = campaign.GetString("cabinet_scope") == "all" || contains(cabinets, compatibility.GetString("cabinet"))
			}
		}
		if matches {
			count++
		}
	}
	return count, nil
}

func ensureRestorable(app core.App, entity string, record *core.Record) error {
	switch entity {
	case "series":
		manufacturer := record.GetString("manufacturer")
		if manufacturer != "" {
			parent, err := app.FindRecordById(collectionGameManufacturer, manufacturer)
			if err != nil || parent.GetBool("archived") {
				return &apiError{status: http.StatusConflict, message: "manufacturer must be active"}
			}
		}
	case "cabinet":
		parent, err := app.FindRecordById(collectionGameSeries, record.GetString("series"))
		if err != nil || parent.GetBool("archived") {
			return &apiError{status: http.StatusConflict, message: "parent series must be active"}
		}
	case "version":
		parent, err := app.FindRecordById(collectionGameSeries, record.GetString("series"))
		if err != nil || parent.GetBool("archived") {
			return &apiError{status: http.StatusConflict, message: "parent series must be active"}
		}
	case "compatibility":
		version, err := app.FindRecordById(collectionGameSeriesVersion, record.GetString("version"))
		if err != nil || version.GetBool("archived") {
			return &apiError{status: http.StatusConflict, message: "parent version must be active"}
		}
		cabinet, err := app.FindRecordById(collectionGameCabinet, record.GetString("cabinet"))
		if err != nil || cabinet.GetBool("archived") {
			return &apiError{status: http.StatusConflict, message: "cabinet must be active"}
		}
		if cabinet.GetString("series") != version.GetString("series") {
			return &apiError{status: http.StatusConflict, message: "cabinet must belong to the version series"}
		}
	}
	return nil
}

func saveChange(app core.App, actor *core.Record, body mutationBody, payloadHash, action, entity, entityID string, before, after map[string]any, reverts string) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId(collectionGameCatalogChangelog)
	if err != nil {
		return nil, err
	}
	change := core.NewRecord(collection)
	change.Set("operation_id", body.OperationID)
	change.Set("payload_hash", payloadHash)
	change.Set("actor", actor.Id)
	change.Set("actor_tags", effectiveTags(actor))
	change.Set("action", action)
	change.Set("entity_type", entity)
	change.Set("entity_id", entityID)
	if before != nil {
		change.Set("before", before)
	}
	change.Set("after", after)
	change.Set("reason", body.Reason)
	change.Set("revision_before", numberFromMap(before, "revision"))
	change.Set("revision_after", numberFromMap(after, "revision"))
	change.Set("reverts_change", reverts)
	if err := app.Save(change); err != nil {
		return nil, fmt.Errorf("save catalog change: %w", err)
	}
	return change, nil
}

func findReplay(app core.App, operationID, payloadHash, actorID string) (*core.Record, error) {
	record, err := app.FindFirstRecordByFilter(collectionGameCatalogChangelog, "operation_id={:operation}", dbx.Params{"operation": operationID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if record.GetString("payload_hash") != payloadHash || record.GetString("actor") != actorID {
		return nil, &apiError{status: http.StatusConflict, message: "operation_id was already used with a different request"}
	}
	return record, nil
}

func snapshot(entity string, record *core.Record) map[string]any {
	if record == nil {
		return nil
	}
	item := map[string]any{
		"id":          record.Id,
		"revision":    revision(record),
		"archived":    record.GetBool("archived"),
		"archived_at": record.Get("archived_at"),
		"archived_by": record.GetString("archived_by"),
		"created":     record.Get("created"),
		"updated":     record.Get("updated"),
	}
	switch entity {
	case "manufacturer":
		for _, field := range []string{"en", "kr", "jp"} {
			item[field] = record.GetString(field)
		}
	case "series":
		item["series_number"] = record.GetInt("seriesNumber")
		for _, field := range []string{"en", "kr", "jp", "en_short", "kr_short", "jp_short", "manufacturer", "alias_of"} {
			item[field] = record.GetString(field)
		}
		item["hide_at"] = record.GetStringSlice("hide_at")
	case "version":
		for _, field := range []string{"series", "released_on", "en", "kr", "jp", "alias_of"} {
			item[field] = record.GetString(field)
		}
		item["price_default"] = record.Get("price_default")
		item["hide_at"] = record.GetStringSlice("hide_at")
	case "cabinet":
		item["series"] = record.GetString("series")
		for _, field := range []string{"en", "kr", "jp"} {
			item[field] = record.GetString(field)
		}
	case "compatibility":
		item["version"] = record.GetString("version")
		item["cabinet"] = record.GetString("cabinet")
		item["price_default"] = record.Get("price_default")
	}
	return item
}

func changeSnapshot(record *core.Record) map[string]any {
	if record == nil {
		return nil
	}
	return map[string]any{
		"id":              record.Id,
		"operation_id":    record.GetString("operation_id"),
		"actor":           record.GetString("actor"),
		"actor_tags":      record.Get("actor_tags"),
		"action":          record.GetString("action"),
		"entity_type":     record.GetString("entity_type"),
		"entity_id":       record.GetString("entity_id"),
		"before":          record.Get("before"),
		"after":           record.Get("after"),
		"reason":          record.GetString("reason"),
		"revision_before": record.GetInt("revision_before"),
		"revision_after":  record.GetInt("revision_after"),
		"reverts_change":  record.GetString("reverts_change"),
		"created":         record.Get("created"),
	}
}

func writeError(re *core.RequestEvent, err error) error {
	var target *apiError
	if errors.As(err, &target) {
		payload := map[string]any{"error": target.message}
		if target.details != nil {
			payload["details"] = target.details
		}
		return re.JSON(target.status, payload)
	}
	return re.JSON(http.StatusBadGateway, map[string]any{"error": "game catalog operation failed", "details": err.Error()})
}

func normalizeEntity(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "series", "version", "cabinet", "compatibility", "manufacturer":
		return value
	default:
		return ""
	}
}

func plural(entity string) string {
	switch entity {
	case "series":
		return "series"
	case "compatibility":
		return "compatibilities"
	default:
		return entity + "s"
	}
}

func revision(record *core.Record) int {
	if value := record.GetInt("revision"); value > 0 {
		return value
	}
	return 1
}

func beforeForAction(action string, before map[string]any) map[string]any {
	if action == "create" {
		return nil
	}
	return before
}

func localizedName(record *core.Record, locale string) string {
	fields := []string{"en", "kr", "jp"}
	if locale == "ko-KR" {
		fields = []string{"kr", "en", "jp"}
	} else if locale == "ja-JP" {
		fields = []string{"jp", "en", "kr"}
	}
	for _, field := range fields {
		if value := strings.TrimSpace(record.GetString(field)); value != "" {
			return value
		}
	}
	return "Untitled"
}

func effectiveTags(record *core.Record) []string {
	seen := map[string]struct{}{}
	tags := []string{}
	for _, values := range [][]string{record.GetStringSlice("tag"), record.GetStringSlice("tags")} {
		for _, value := range values {
			value = strings.ToLower(strings.TrimSpace(value))
			if value == "" {
				continue
			}
			if _, exists := seen[value]; !exists {
				seen[value] = struct{}{}
				tags = append(tags, value)
			}
		}
	}
	return tags
}

func ensureKeys(values map[string]any, allowed []string) error {
	set := map[string]struct{}{}
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key := range values {
		if _, ok := set[key]; !ok {
			return &apiError{status: http.StatusBadRequest, message: "unsupported field: " + key}
		}
	}
	return nil
}

func optionalText(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func numberValue(value any) (float64, bool) {
	number, ok := value.(float64)
	return number, ok
}

func stringSlice(value any) []string {
	if strings, ok := value.([]string); ok {
		return strings
	}
	items, _ := value.([]any)
	result := []string{}
	seen := map[string]struct{}{}
	for _, item := range items {
		text := strings.TrimSpace(optionalText(item))
		if text == "" {
			continue
		}
		if _, exists := seen[text]; !exists {
			seen[text] = struct{}{}
			result = append(result, text)
		}
	}
	return result
}

func numberFromMap(value map[string]any, key string) int {
	if value == nil {
		return 0
	}
	switch number := value[key].(type) {
	case int:
		return number
	case float64:
		return int(number)
	default:
		return 0
	}
}

func queryInt(re *core.RequestEvent, name string, fallback int) int {
	value := strings.TrimSpace(re.Request.URL.Query().Get(name))
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
