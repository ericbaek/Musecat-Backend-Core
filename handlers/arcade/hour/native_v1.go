package hour

import (
	"encoding/json"
	"reflect"

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
		if err != nil {
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

func PatchNativeHourV1(e *core.RequestEvent) error {
	var patch nativeHourPatch
	if err := arcadeinternal.DecodeNativeEdit(e, &patch); err != nil {
		return err
	}
	if patch.Revision == nil || len(patch.Changes) == 0 {
		return e.BadRequestError("revision_and_changes_required", nil)
	}
	for key, raw := range patch.Changes {
		if key == "note" {
			var note *string
			if json.Unmarshal(raw, &note) != nil || note == nil {
				return e.BadRequestError("invalid_note", nil)
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
			return e.BadRequestError("unsupported_field", nil)
		}
		var day DayHours
		if err := json.Unmarshal(raw, &day); err != nil || validateDay(key, day) != nil {
			return e.BadRequestError("invalid_day_hours", nil)
		}
		if !day.Unknown && !day.Closed {
			var fields map[string]json.RawMessage
			if json.Unmarshal(raw, &fields) != nil || len(fields) != 2 || fields["start"] == nil || fields["end"] == nil {
				return e.BadRequestError("invalid_day_hours", nil)
			}
		}
	}
	return updateArcadeHour(e, UpdateArcadeHourBody{Arcade: e.Request.PathValue("id")}, &patch)
}

func applyNativeHours(next, previous *core.Record, patch nativeHourPatch) error {
	arcadeinternal.CopyRevisionContent(next, previous)
	changed := false
	for key, raw := range patch.Changes {
		if key == "note" {
			var note string
			_ = json.Unmarshal(raw, &note)
			current := ""
			if previous != nil {
				current = previous.GetString("Note")
				if current == "" {
					current = previous.GetString("note")
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
		// Updating open hours keeps additional server-owned members of that day.
		if incoming, ok := value.(map[string]any); ok {
			if existing, ok := old.(map[string]any); ok {
				merged := map[string]any{}
				for k, v := range existing {
					merged[k] = v
				}
				for k, v := range incoming {
					merged[k] = v
				}
				value = merged
			}
		}
		if !reflect.DeepEqual(old, value) {
			changed = true
		}
		next.Set(key, value)
	}
	if !changed {
		return &arcadeinternal.NativeEditError{Status: 400, Code: "no_changes"}
	}
	return nil
}
