package hour

import (
	"encoding/json"
	"reflect"
	"strings"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/pocketbase/pocketbase/core"
)

var nativeDays = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

type nativeHourPatch struct {
	Revision *string                    `json:"revision"`
	Changes  map[string]json.RawMessage `json:"changes"`
}

func GetNativeHourV1(e *core.RequestEvent) error {
	arcade, err := e.App.FindRecordById(arcadeinternal.CollectionArcade, e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("arcade_not_found", nil)
	}
	if !arcadeinternal.CanWriteArcade(e.Auth, arcade) {
		return e.ForbiddenError("arcade_write_forbidden", nil)
	}
	revision := arcade.GetString("hour")
	var record *core.Record
	if revision != "" {
		record, err = e.App.FindRecordById(arcadeinternal.CollectionArcadeHour, revision)
		// A revision pointing at another arcade must not be exposed as this arcade's hours.
		if err != nil || record.GetString("arcade") != arcade.Id {
			return e.InternalServerError("hours_unavailable", nil)
		}
	}
	values := map[string]any{"note": ""}
	for _, day := range nativeDays {
		values[day] = nil
		if record != nil {
			values[day] = record.Get(day)
		}
	}
	if record != nil {
		values["note"] = record.GetString("Note")
		if values["note"] == "" {
			values["note"] = record.GetString("note")
		}
	}
	return e.JSON(200, map[string]any{"contract_version": 1, "required_capabilities": []string{"hours.patch.v1"}, "arcade": arcade.Id, "revision": revision, "timezone": arcade.GetString("timezone"), "values": values})
}

// PatchNativeHourV1 decodes and validates the patch before any lookup or write.
// Decode and validation errors are written exactly once here; the update itself
// writes its own transaction-mapped responses.
func PatchNativeHourV1(e *core.RequestEvent) error {
	var patch nativeHourPatch
	if err := arcadeinternal.DecodeNativeEdit(e, &patch); err != nil {
		return arcadeinternal.RespondNativeEditError(e, err)
	}
	if err := validateNativeHourPatch(patch); err != nil {
		return arcadeinternal.RespondNativeEditError(e, err)
	}
	return updateArcadeHour(e, UpdateArcadeHourBody{Arcade: e.Request.PathValue("id")}, &patch)
}

// validateNativeHourPatch checks the v1 shape of a decoded patch without
// touching the request, response or database.
func validateNativeHourPatch(patch nativeHourPatch) error {
	if patch.Revision == nil || len(patch.Changes) == 0 {
		return arcadeinternal.NativeBadRequest("revision_and_changes_required")
	}
	for key, raw := range patch.Changes {
		if key == "note" {
			var note *string
			if json.Unmarshal(raw, &note) != nil || note == nil {
				return arcadeinternal.NativeBadRequest("invalid_note")
			}
			continue
		}
		validKey := false
		for _, day := range nativeDays {
			if key == day {
				validKey = true
			}
		}
		if !validKey {
			return arcadeinternal.NativeBadRequest("unsupported_field")
		}
		var day DayHours
		if err := json.Unmarshal(raw, &day); err != nil || validateDay(key, day) != nil {
			return arcadeinternal.NativeBadRequest("invalid_day_hours")
		}
		if !day.Unknown && !day.Closed {
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || fields["start"] == nil || fields["end"] == nil {
				return arcadeinternal.NativeBadRequest("invalid_day_hours")
			}
		}
	}
	return nil
}

// hasOpenHours reports whether a stored day object carries the v1 {start,end}
// members an open-time patch is allowed to update in place.
func hasOpenHours(day map[string]any) bool {
	_, start := day["start"].(float64)
	_, end := day["end"].(float64)
	return start && end
}

func applyNativeHours(next, previous *core.Record, patch nativeHourPatch) error {
	arcadeinternal.CopyRevisionContent(next, previous)
	changed := false
	for key, raw := range patch.Changes {
		if key == "note" {
			var note string
			_ = json.Unmarshal(raw, &note)
			// The changelog snapshot trims the note, so compare and store the same value;
			// otherwise whitespace-only edits create an "unchanged" revision and XP.
			note = strings.TrimSpace(note)
			current := ""
			if previous != nil {
				current = strings.TrimSpace(previous.GetString("Note"))
				if current == "" {
					current = strings.TrimSpace(previous.GetString("note"))
				}
			}
			if current != note {
				changed = true
			}
			next.Set("Note", note)
			next.Set("note", note)
			continue
		}
		var value any
		_ = json.Unmarshal(raw, &value)
		var old any
		if previous != nil {
			data, _ := json.Marshal(previous.Get(key))
			_ = json.Unmarshal(data, &old)
		}
		if incoming, ok := value.(map[string]any); ok {
			switch existing := old.(type) {
			case nil:
				// Unknown day: the open-hours object replaces it.
			case float64:
				if existing != 499 {
					// Not a v1 closed marker: a future shape this contract cannot patch.
					return arcadeinternal.NativeBadRequest("unsupported_day_shape")
				}
			case map[string]any:
				// Updating open hours keeps additional server-owned members of that day.
				// A stored object without v1 start/end is a future shape; refuse to
				// produce a hybrid or silently replace it. Null/499 remain deliberate.
				if !hasOpenHours(existing) {
					return arcadeinternal.NativeBadRequest("unsupported_day_shape")
				}
				merged := map[string]any{}
				for k, v := range existing {
					merged[k] = v
				}
				for k, v := range incoming {
					merged[k] = v
				}
				value = merged
			default:
				return arcadeinternal.NativeBadRequest("unsupported_day_shape")
			}
		}
		if !reflect.DeepEqual(old, value) {
			changed = true
		}
		next.Set(key, value)
	}
	if !changed {
		return arcadeinternal.NativeBadRequest("no_changes")
	}
	return nil
}
