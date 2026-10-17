package gtk

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/ericbaek/musecat-backend-core/service/xp"
)

// Native GTK v1 is a fixed delta contract over the existing GTK molecule/atom
// pipeline. It never replaces the whole facility list: every add, update and
// removal is explicit, and untouched atoms (including types and columns this
// contract does not know) are copied verbatim into the new immutable revision.

const nativeGTKCapability = "gtk.delta.v1"

// Web parity: the parking entrance must stay within this distance of the
// arcade's basic location whenever the entrance actually changes.
const nativeParkingMaxDistanceMeters = 300.0

type nativeGTKPatch struct {
	Revision *string           `json:"revision"`
	Add      []nativeGTKAdd    `json:"add"`
	Update   []nativeGTKUpdate `json:"update"`
	Remove   []string          `json:"remove"`
}

// Raw members keep "absent", "null" and "wrong type" distinguishable so each
// case maps to a documented error instead of a silent default.
type nativeGTKAdd struct {
	Type string          `json:"type"`
	Bool json.RawMessage `json:"bool"`
	Note json.RawMessage `json:"note"`
	Meta json.RawMessage `json:"meta"`
}

type nativeGTKUpdate struct {
	ID      string                     `json:"id"`
	Changes map[string]json.RawMessage `json:"changes"`
}

// parkingMetaPatch is a validated partial update of Parking metadata. Keys holds
// only the supported members present in the request; a nil value is an explicit
// clear of that member. Clear is the whole-object `meta: null`.
type parkingMetaPatch struct {
	Clear bool
	Keys  map[string]any
}

// nativeGTKAtomPlan is one atom of the next revision: a copied source atom with
// optional explicit field changes, or a brand-new atom.
type nativeGTKAtomPlan struct {
	source  *core.Record
	typ     string
	boolVal bool
	note    string
	meta    any
	setBool bool
	setNote bool
	setMeta bool
}

func nativeGTKError(status int, code string) *arcadeinternal.NativeEditError {
	return &arcadeinternal.NativeEditError{Status: status, Code: code}
}

func nativeGTKBadRequest(code string) *arcadeinternal.NativeEditError {
	return nativeGTKError(http.StatusBadRequest, code)
}

// GetNativeGTKV1 binds the current GTK atoms to one immutable revision token.
func GetNativeGTKV1(e *core.RequestEvent) error {
	arcade, err := e.App.FindRecordById(arcadeinternal.CollectionArcade, e.Request.PathValue("id"))
	if err != nil {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "arcade_not_found"})
	}
	if !arcadeinternal.CanWriteArcade(e.Auth, arcade) {
		return e.JSON(http.StatusForbidden, map[string]string{"error": "arcade_write_forbidden"})
	}
	revision := strings.TrimSpace(arcade.GetString("gtk"))
	atoms, err := loadCurrentGTKAtoms(e.App, arcade.Id, revision)
	if err != nil {
		// A revision that cannot be loaded or belongs to another arcade is never exposed.
		return e.JSON(http.StatusInternalServerError, map[string]string{"error": "gtk_unavailable"})
	}
	items := make([]map[string]any, 0, len(atoms))
	for _, atom := range atoms {
		meta, err := decodeStoredMeta(atom.Meta)
		if err != nil {
			return e.JSON(http.StatusInternalServerError, map[string]string{"error": "gtk_unavailable"})
		}
		items = append(items, map[string]any{
			"id":   atom.AtomID,
			"type": atom.Type,
			"bool": atom.Bool,
			"note": atom.Note,
			"meta": meta,
		})
	}
	return e.JSON(http.StatusOK, map[string]any{
		"contract_version":      1,
		"required_capabilities": []string{nativeGTKCapability},
		"arcade":                arcade.Id,
		"revision":              revision,
		"items":                 items,
	})
}

// PatchNativeGTKV1 applies explicit add/update/remove operations against one
// base revision inside the shared GTK mutation transaction.
func PatchNativeGTKV1(e *core.RequestEvent) error {
	patch, decodeErr := decodeNativeGTKPatch(e)
	if decodeErr != nil {
		return writeNativeGTKError(e, decodeErr)
	}
	if err := validateNativeGTKPatch(patch); err != nil {
		return writeNativeGTKError(e, err)
	}

	arcadeID := e.Request.PathValue("id")
	arcade, err := e.App.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil {
		return e.JSON(http.StatusNotFound, map[string]string{"error": "arcade_not_found"})
	}
	if !arcadeinternal.CanWriteArcade(e.Auth, arcade) {
		return e.JSON(http.StatusForbidden, map[string]string{"error": "arcade_write_forbidden"})
	}

	var newGTKId string
	var xpFeedback xp.ExpFeedback

	if err := e.App.RunInTransaction(func(txApp core.App) error {
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		if !arcadeinternal.CanWriteArcade(e.Auth, arcadeRec) {
			return arcadeinternal.ErrArcadeWriteForbidden
		}
		prevID := strings.TrimSpace(arcadeRec.GetString("gtk"))
		if prevID != *patch.Revision {
			return nativeGTKError(http.StatusConflict, "revision_conflict")
		}
		prevAtoms, err := loadCurrentGTKAtoms(txApp, arcadeID, prevID)
		if err != nil {
			return nativeGTKError(http.StatusBadGateway, "gtk_unavailable")
		}
		var prevMolecule *core.Record
		if prevID != "" {
			// loadCurrentGTKAtoms already verified the molecule belongs to this arcade.
			prevMolecule, err = txApp.FindRecordById(arcadeinternal.CollectionArcadeGTK, prevID)
			if err != nil {
				return nativeGTKError(http.StatusBadGateway, "gtk_unavailable")
			}
		}

		plans, err := mergeNativeGTK(txApp, arcadeRec, prevAtoms, patch)
		if err != nil {
			return err
		}

		baseExp, err := xp.LoadCurrentExp(txApp, e.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp

		gtkColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeGTK)
		if err != nil {
			return fmt.Errorf("failed to find arcade_gtk: %w", err)
		}
		gtkRec := core.NewRecord(gtkColl)
		arcadeinternal.CopyRevisionContent(gtkRec, prevMolecule)
		gtkRec.Set("arcade", arcadeID)
		gtkRec.Set("createdBy", e.Auth.Id)
		if err := txApp.Save(gtkRec); err != nil {
			return fmt.Errorf("failed to create arcade_gtk: %w", err)
		}
		newGTKId = gtkRec.Id

		atomColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeGTKAtoms)
		if err != nil {
			return fmt.Errorf("failed to find arcade_gtk_atoms: %w", err)
		}
		prevByID := map[string]gtkAtomState{}
		for _, prev := range prevAtoms {
			prevByID[prev.AtomID] = prev
		}
		referenced := map[string]struct{}{}
		logItems := make([]gtkDiffLogItem, 0, len(plans)+len(prevAtoms))
		for i, plan := range plans {
			atom := core.NewRecord(atomColl)
			if plan.source != nil {
				arcadeinternal.CopyRevisionContent(atom, plan.source)
			} else {
				atom.Set("type", plan.typ)
			}
			atom.Set("molecule", newGTKId)
			if plan.setBool {
				atom.Set("bool", plan.boolVal)
			}
			if plan.setNote {
				atom.Set("note", plan.note)
			}
			if plan.setMeta {
				atom.Set("meta", plan.meta)
			}
			atom.Set("createdBy", e.Auth.Id)
			if err := txApp.Save(atom); err != nil {
				return fmt.Errorf("failed to create gtk atom %d: %w", i, err)
			}
			next := gtkAtomState{
				AtomID: atom.Id,
				Type:   strings.TrimSpace(atom.GetString("type")),
				Bool:   atom.GetBool("bool"),
				Note:   strings.TrimSpace(atom.GetString("note")),
				Meta:   atom.Get("meta"),
			}
			if plan.source == nil {
				logItems = append(logItems, buildGTKDiffLogItem(next, nil))
				continue
			}
			prev := prevByID[plan.source.Id]
			referenced[prev.AtomID] = struct{}{}
			logItems = append(logItems, buildGTKDiffLogItem(next, &prev))
		}
		for _, prev := range prevAtoms {
			if _, ok := referenced[prev.AtomID]; ok {
				continue
			}
			logItems = append(logItems, buildDeletedGTKDiffLogItem(prev))
		}

		if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(
			txApp,
			arcadeID,
			map[string]any{"gtk": newGTKId},
			map[string]any{"gtk": arcadeinternal.BuildChangelogEnvelope("gtk", logItems)},
			e.Auth.Id,
		); err != nil {
			return fmt.Errorf("failed to update arcade.gtk: %w", err)
		}
		if arcadeRec.GetBool("public") {
			nextExp, _, err := xp.AwardArcadeEditExpTx(txApp, e.Auth.Id, arcadeID, "gtk", 2, baseExp, time.Now().UTC())
			if err != nil {
				return err
			}
			currentExp = nextExp
		}
		xpFeedback = xp.BuildExpFeedback(baseExp, currentExp)
		return nil
	}); err != nil {
		return writeNativeGTKError(e, err)
	}

	return e.JSON(http.StatusOK, map[string]any{
		"arcade":      arcadeID,
		"revision":    newGTKId,
		"xp_feedback": xpFeedback,
	})
}

// decodeNativeGTKPatch is deliberately local: a decode failure is reported as a
// non-nil error so the handler can never continue into a mutation after a 400.
func decodeNativeGTKPatch(e *core.RequestEvent) (nativeGTKPatch, error) {
	var patch nativeGTKPatch
	data, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return patch, nativeGTKBadRequest("invalid_patch")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&patch); err != nil {
		return patch, nativeGTKBadRequest("invalid_patch")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return patch, nativeGTKBadRequest("invalid_patch")
	}
	return patch, nil
}

func writeNativeGTKError(e *core.RequestEvent, err error) error {
	var nativeErr *arcadeinternal.NativeEditError
	if errors.As(err, &nativeErr) {
		return e.JSON(nativeErr.Status, map[string]string{"error": nativeErr.Code})
	}
	if errors.Is(err, arcadeinternal.ErrArcadeWriteForbidden) {
		return e.JSON(http.StatusForbidden, map[string]string{"error": "arcade_write_forbidden"})
	}
	return e.JSON(http.StatusBadGateway, map[string]any{
		"error":   "transaction failed",
		"details": err.Error(),
	})
}

// validateNativeGTKPatch performs every check that needs no database state.
func validateNativeGTKPatch(patch nativeGTKPatch) error {
	if patch.Revision == nil || len(patch.Add)+len(patch.Update)+len(patch.Remove) == 0 {
		return nativeGTKBadRequest("revision_and_operations_required")
	}
	addTypes := map[string]struct{}{}
	for _, add := range patch.Add {
		if _, ok := allowedGTKTypes[add.Type]; !ok {
			return nativeGTKBadRequest("unsupported_gtk_type")
		}
		if _, dup := addTypes[add.Type]; dup {
			return nativeGTKBadRequest("duplicate_gtk_type")
		}
		addTypes[add.Type] = struct{}{}
		if _, ok := decodeJSONBool(add.Bool); !ok {
			return nativeGTKBadRequest("invalid_gtk_value")
		}
		if add.Note != nil {
			if _, ok := decodeJSONString(add.Note); !ok {
				return nativeGTKBadRequest("invalid_gtk_value")
			}
		}
		if add.Meta != nil {
			if add.Type != "Parking" {
				return nativeGTKBadRequest("meta_not_allowed")
			}
			if _, err := parseParkingMetaPatch(add.Meta); err != nil {
				return err
			}
		}
	}
	seenIDs := map[string]struct{}{}
	for _, update := range patch.Update {
		id := strings.TrimSpace(update.ID)
		if id == "" || len(update.Changes) == 0 {
			return nativeGTKBadRequest("invalid_gtk_id")
		}
		if _, dup := seenIDs[id]; dup {
			return nativeGTKBadRequest("invalid_gtk_id")
		}
		seenIDs[id] = struct{}{}
		for key, raw := range update.Changes {
			switch key {
			case "bool":
				if _, ok := decodeJSONBool(raw); !ok {
					return nativeGTKBadRequest("invalid_gtk_value")
				}
			case "note":
				if _, ok := decodeJSONString(raw); !ok {
					return nativeGTKBadRequest("invalid_gtk_value")
				}
			case "meta":
				if _, err := parseParkingMetaPatch(raw); err != nil {
					return err
				}
			default:
				return nativeGTKBadRequest("unsupported_field")
			}
		}
	}
	for _, id := range patch.Remove {
		id = strings.TrimSpace(id)
		if id == "" {
			return nativeGTKBadRequest("invalid_gtk_id")
		}
		if _, dup := seenIDs[id]; dup {
			return nativeGTKBadRequest("invalid_gtk_id")
		}
		seenIDs[id] = struct{}{}
	}
	return nil
}

// mergeNativeGTK resolves the patch against the base revision. Untouched atoms
// are copied, explicit removals are dropped, adds are appended. Any failure
// returns before a single record is written.
func mergeNativeGTK(app core.App, arcadeRec *core.Record, previous []gtkAtomState, patch nativeGTKPatch) ([]nativeGTKAtomPlan, error) {
	plans := make([]nativeGTKAtomPlan, 0, len(previous)+len(patch.Add))
	indexes := map[string]int{}
	for i, prev := range previous {
		record, err := app.FindRecordById(arcadeinternal.CollectionArcadeGTKAtoms, prev.AtomID)
		if err != nil {
			return nil, nativeGTKError(http.StatusBadGateway, "gtk_unavailable")
		}
		indexes[prev.AtomID] = i
		plans = append(plans, nativeGTKAtomPlan{source: record, typ: prev.Type})
	}

	removed := map[string]bool{}
	changed := false
	for _, id := range patch.Remove {
		id = strings.TrimSpace(id)
		if _, ok := indexes[id]; !ok {
			return nil, nativeGTKBadRequest("invalid_gtk_id")
		}
		removed[id] = true
		changed = true
	}

	var basicLat, basicLon float64
	basicResolved, basicKnown := false, false
	resolveBasic := func() bool {
		if basicResolved {
			return basicKnown
		}
		basicResolved = true
		basicID := strings.TrimSpace(arcadeRec.GetString("basic"))
		if basicID == "" {
			return false
		}
		basic, err := app.FindRecordById(arcadeinternal.CollectionArcadeBasic, basicID)
		if err != nil {
			return false
		}
		lat, lon, ok := arcadeinternal.ReadLocation(basic.Get("location"))
		basicKnown = ok && (lat != 0 || lon != 0)
		basicLat, basicLon = lat, lon
		return basicKnown
	}

	// Web parity: a changed parking entrance must be within 300 m of the arcade.
	// An unchanged legacy entrance is never revalidated by an unrelated edit.
	checkParkingDistance := func(previousMeta, nextMeta any) error {
		nextGeo, ok := metaGeo(nextMeta)
		if !ok {
			return nil
		}
		if prevGeo, ok := metaGeo(previousMeta); ok && prevGeo == nextGeo {
			return nil
		}
		if !resolveBasic() {
			return nil
		}
		if arcadeinternal.DistanceKm(basicLat, basicLon, nextGeo[0], nextGeo[1])*1000 > nativeParkingMaxDistanceMeters {
			return nativeGTKBadRequest("parking_geo_too_far")
		}
		return nil
	}
	korean := isKoreanCountry(arcadeRec.GetString("country"))

	for _, update := range patch.Update {
		id := strings.TrimSpace(update.ID)
		index, ok := indexes[id]
		if !ok || removed[id] {
			return nil, nativeGTKBadRequest("invalid_gtk_id")
		}
		plan := &plans[index]
		if _, known := allowedGTKTypes[plan.typ]; !known {
			// A type this contract cannot render is read-only; removal stays explicit.
			return nil, nativeGTKBadRequest("unsupported_gtk_type")
		}
		for key, raw := range update.Changes {
			switch key {
			case "bool":
				value, _ := decodeJSONBool(raw)
				if value != plan.source.GetBool("bool") {
					changed = true
				}
				plan.boolVal, plan.setBool = value, true
			case "note":
				value, _ := decodeJSONString(raw)
				value = strings.TrimSpace(value)
				if value != strings.TrimSpace(plan.source.GetString("note")) {
					changed = true
				}
				plan.note, plan.setNote = value, true
			case "meta":
				if plan.typ != "Parking" {
					return nil, nativeGTKBadRequest("meta_not_allowed")
				}
				metaPatch, err := parseParkingMetaPatch(raw)
				if err != nil {
					return nil, err
				}
				if gov, present := metaPatch.Keys["gov_parking"]; present && gov != nil && !korean {
					return nil, nativeGTKBadRequest("gov_parking_not_available")
				}
				existing, err := decodeStoredMeta(plan.source.Get("meta"))
				if err != nil {
					return nil, nativeGTKBadRequest("unsupported_meta_shape")
				}
				merged, err := applyParkingMetaPatch(existing, metaPatch)
				if err != nil {
					return nil, err
				}
				if err := checkParkingDistance(existing, merged); err != nil {
					return nil, err
				}
				if !arcadeinternal.JSONValueEqual(normalizeStoredMeta(existing), merged) {
					changed = true
				}
				plan.meta, plan.setMeta = merged, true
			}
		}
	}

	kept := make([]nativeGTKAtomPlan, 0, len(plans)+len(patch.Add))
	keptTypes := map[string]struct{}{}
	for _, plan := range plans {
		if removed[plan.source.Id] {
			continue
		}
		keptTypes[plan.typ] = struct{}{}
		kept = append(kept, plan)
	}
	for _, add := range patch.Add {
		if _, exists := keptTypes[add.Type]; exists {
			return nil, nativeGTKBadRequest("duplicate_gtk_type")
		}
		keptTypes[add.Type] = struct{}{}
		plan := nativeGTKAtomPlan{typ: add.Type, setBool: true, setNote: true}
		plan.boolVal, _ = decodeJSONBool(add.Bool)
		if add.Note != nil {
			note, _ := decodeJSONString(add.Note)
			plan.note = strings.TrimSpace(note)
		}
		if add.Meta != nil {
			metaPatch, err := parseParkingMetaPatch(add.Meta)
			if err != nil {
				return nil, err
			}
			if gov, present := metaPatch.Keys["gov_parking"]; present && gov != nil && !korean {
				return nil, nativeGTKBadRequest("gov_parking_not_available")
			}
			merged, err := applyParkingMetaPatch(nil, metaPatch)
			if err != nil {
				return nil, err
			}
			if err := checkParkingDistance(nil, merged); err != nil {
				return nil, err
			}
			if merged != nil {
				plan.meta, plan.setMeta = merged, true
			}
		}
		kept = append(kept, plan)
		changed = true
	}
	if !changed {
		return nil, nativeGTKBadRequest("no_changes")
	}
	return kept, nil
}

// parseParkingMetaPatch validates one meta value using the same catalog as the
// web editor and PUT /arcade/gtk: exact enum values, unique options, numeric
// in-range coordinates with both members, and no unknown members anywhere.
func parseParkingMetaPatch(raw json.RawMessage) (parkingMetaPatch, error) {
	patch := parkingMetaPatch{Keys: map[string]any{}}
	if isJSONNull(raw) {
		patch.Clear = true
		return patch, nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || members == nil {
		return patch, nativeGTKBadRequest("invalid_gtk_value")
	}
	for key, value := range members {
		switch key {
		case "geo":
			if isJSONNull(value) {
				patch.Keys[key] = nil
				continue
			}
			var coords map[string]json.RawMessage
			if err := json.Unmarshal(value, &coords); err != nil || coords == nil {
				return patch, nativeGTKBadRequest("invalid_parking_geo")
			}
			for member := range coords {
				if member != "lat" && member != "lon" {
					return patch, nativeGTKBadRequest("unsupported_field")
				}
			}
			lat, latOK := decodeJSONNumber(coords["lat"])
			lon, lonOK := decodeJSONNumber(coords["lon"])
			if !latOK || !lonOK || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
				return patch, nativeGTKBadRequest("invalid_parking_geo")
			}
			patch.Keys[key] = map[string]any{"lat": lat, "lon": lon}
		case "availability":
			if isJSONNull(value) {
				patch.Keys[key] = nil
				continue
			}
			text, ok := decodeJSONString(value)
			if !ok {
				return patch, nativeGTKBadRequest("invalid_parking_availability")
			}
			if _, allowed := allowedParkingAvailability[text]; !allowed {
				return patch, nativeGTKBadRequest("invalid_parking_availability")
			}
			patch.Keys[key] = text
		case "options":
			if isJSONNull(value) {
				patch.Keys[key] = nil
				continue
			}
			var options []string
			if err := json.Unmarshal(value, &options); err != nil || options == nil {
				return patch, nativeGTKBadRequest("invalid_parking_options")
			}
			seen := map[string]struct{}{}
			for _, option := range options {
				if _, allowed := allowedParkingOptions[option]; !allowed {
					return patch, nativeGTKBadRequest("invalid_parking_options")
				}
				if _, dup := seen[option]; dup {
					return patch, nativeGTKBadRequest("invalid_parking_options")
				}
				seen[option] = struct{}{}
			}
			patch.Keys[key] = options
		case "ev_charging", "gov_parking":
			if isJSONNull(value) {
				patch.Keys[key] = nil
				continue
			}
			flag, ok := decodeJSONBool(value)
			if !ok {
				return patch, nativeGTKBadRequest("invalid_parking_flag")
			}
			patch.Keys[key] = flag
		default:
			return patch, nativeGTKBadRequest("unsupported_field")
		}
	}
	return patch, nil
}

// applyParkingMetaPatch merges supported members into the stored object while
// keeping every member (and every geo member) the request did not mention. A
// stored shape that is not an object cannot be patched member-wise; only an
// explicit `meta: null` replaces it.
func applyParkingMetaPatch(existing any, patch parkingMetaPatch) (any, error) {
	if patch.Clear {
		return nil, nil
	}
	base := map[string]any{}
	switch current := existing.(type) {
	case nil:
	case map[string]any:
		for key, value := range current {
			base[key] = value
		}
	default:
		if len(patch.Keys) == 0 {
			return existing, nil
		}
		return nil, nativeGTKBadRequest("unsupported_meta_shape")
	}
	for key, value := range patch.Keys {
		if value == nil {
			delete(base, key)
			continue
		}
		if key == "geo" {
			merged := map[string]any{}
			switch currentGeo := base["geo"].(type) {
			case nil:
			case map[string]any:
				for member, memberValue := range currentGeo {
					merged[member] = memberValue
				}
			default:
				return nil, nativeGTKBadRequest("unsupported_meta_shape")
			}
			for member, memberValue := range value.(map[string]any) {
				merged[member] = memberValue
			}
			base["geo"] = merged
			continue
		}
		base[key] = value
	}
	return normalizeStoredMeta(base), nil
}

// decodeStoredMeta renders a persisted JSON column as plain Go values so
// unknown members survive comparison and copying.
func decodeStoredMeta(value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	if isJSONNull(encoded) || len(bytes.TrimSpace(encoded)) == 0 || string(bytes.TrimSpace(encoded)) == `""` {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

// An empty object and an absent object mean the same thing to every reader.
func normalizeStoredMeta(value any) any {
	if object, ok := value.(map[string]any); ok && len(object) == 0 {
		return nil
	}
	return value
}

func metaGeo(meta any) ([2]float64, bool) {
	object, ok := meta.(map[string]any)
	if !ok {
		return [2]float64{}, false
	}
	geo, ok := object["geo"].(map[string]any)
	if !ok {
		return [2]float64{}, false
	}
	lat, latOK := geo["lat"].(float64)
	lon, lonOK := geo["lon"].(float64)
	if !latOK || !lonOK {
		return [2]float64{}, false
	}
	return [2]float64{lat, lon}, true
}

// isKoreanCountry mirrors the web editor's gov_parking availability rule.
func isKoreanCountry(country string) bool {
	value := strings.ToUpper(strings.TrimSpace(country))
	return value == "KR" || value == "KOREA" || value == "대한민국"
}

func isJSONNull(raw []byte) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

func decodeJSONBool(raw json.RawMessage) (bool, bool) {
	var value *bool
	if raw == nil || json.Unmarshal(raw, &value) != nil || value == nil {
		return false, false
	}
	return *value, true
}

func decodeJSONString(raw json.RawMessage) (string, bool) {
	var value *string
	if raw == nil || json.Unmarshal(raw, &value) != nil || value == nil {
		return "", false
	}
	return *value, true
}

func decodeJSONNumber(raw json.RawMessage) (float64, bool) {
	var value *float64
	if raw == nil || json.Unmarshal(raw, &value) != nil || value == nil {
		return 0, false
	}
	return *value, true
}
