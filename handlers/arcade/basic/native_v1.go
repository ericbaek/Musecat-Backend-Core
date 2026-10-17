package basic

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/pocketbase/pocketbase/core"
)

// Native v1 is a stable boundary; web request changes must not change its meaning.
func GetNativeBasicV1(e *core.RequestEvent) error {
	arcade, err := e.App.FindRecordById(arcadeinternal.CollectionArcade, e.Request.PathValue("id"))
	if err != nil {
		return e.JSON(404, map[string]string{"error": "arcade not found"})
	}
	if !arcadeinternal.CanWriteArcade(e.Auth, arcade) {
		return e.JSON(403, map[string]string{"error": "arcade_write_forbidden"})
	}
	// Read the exact immutable revision identified by this response, not a second
	// aggregate lookup that could return newer values with an older revision token.
	rec, err := e.App.FindRecordById(arcadeinternal.CollectionArcadeBasic, arcade.GetString("basic"))
	if err != nil {
		return e.JSON(502, map[string]string{"error": "basic information unavailable"})
	}
	return e.JSON(200, map[string]any{
		"contract_version": 1, "required_capabilities": []string{"basic.patch.v1"},
		"arcade": arcade.Id, "revision": rec.Id, "country": arcade.GetString("country"),
		"values": map[string]any{"name": rec.GetString("name"), "address": rec.GetString("address"), "direction": rec.GetString("direction"), "nickname": rec.GetStringSlice("nickname"), "subway_line": rec.GetStringSlice("subway_line"), "location": rec.Get("location")},
	})
}

func PatchNativeBasicV1(e *core.RequestEvent) error {
	var in struct {
		Revision string                     `json:"revision"`
		Changes  map[string]json.RawMessage `json:"changes"`
	}
	// PocketBase rewinds its request reader after EOF. Decode a bounded snapshot
	// so checking for a second JSON value does not reread the first document.
	data, err := io.ReadAll(e.Request.Body)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&in); err != nil {
		return e.JSON(400, map[string]string{"error": "invalid_patch"})
	}
	if decoder.Decode(new(any)) != io.EOF || strings.TrimSpace(in.Revision) == "" || len(in.Changes) == 0 {
		return e.JSON(400, map[string]string{"error": "revision_and_changes_required"})
	}
	for key, value := range in.Changes {
		switch key {
		case "name", "address", "direction", "nickname", "subway_line", "location":
		default:
			return e.JSON(400, map[string]string{"error": "unsupported_field"})
		}
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return e.JSON(400, map[string]string{"error": "null_not_supported"})
		}
	}
	encoded, _ := json.Marshal(in.Changes)
	var body UpdateArcadeBasicBody
	// Strict decoding also rejects unknown members of location.
	fields := json.NewDecoder(bytes.NewReader(encoded))
	fields.DisallowUnknownFields()
	if err := fields.Decode(&body); err != nil {
		return e.JSON(400, map[string]string{"error": "invalid_field_type"})
	}
	if body.Location != nil {
		var coords map[string]json.RawMessage
		_ = json.Unmarshal(in.Changes["location"], &coords)
		if len(coords) != 2 || coords["lat"] == nil || coords["lon"] == nil || string(coords["lat"]) == "null" || string(coords["lon"]) == "null" {
			return e.JSON(400, map[string]string{"error": "latitude_and_longitude_required"})
		}
	}
	for _, required := range []*string{body.Name, body.Address} {
		if required != nil && strings.TrimSpace(*required) == "" {
			return e.JSON(400, map[string]string{"error": "required_field_empty"})
		}
	}
	body.Arcade = e.Request.PathValue("id")
	body.BaseBasicID = &in.Revision
	return updateArcadeBasic(e, body)
}
