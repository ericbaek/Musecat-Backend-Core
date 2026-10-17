package sns

import (
	"encoding/json"
	"net/url"
	"strings"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/pocketbase/pocketbase/core"
)

type nativeSNSPatch struct {
	Revision *string        `json:"revision"`
	Add      []SNSAtomInput `json:"add,omitempty"`
	Update   []struct {
		ID      string                     `json:"id"`
		Changes map[string]json.RawMessage `json:"changes"`
	} `json:"update,omitempty"`
	Remove []string `json:"remove,omitempty"`
}

func GetNativeSNSV1(e *core.RequestEvent) error {
	arcade, err := e.App.FindRecordById(arcadeinternal.CollectionArcade, e.Request.PathValue("id"))
	if err != nil {
		return e.NotFoundError("arcade_not_found", nil)
	}
	if !arcadeinternal.CanWriteArcade(e.Auth, arcade) {
		return e.ForbiddenError("arcade_write_forbidden", nil)
	}
	revision := arcade.GetString("sns")
	atoms, err := loadCurrentSNSAtoms(e.App, arcade.Id, revision)
	if err != nil {
		return e.InternalServerError("contacts_unavailable", nil)
	}
	items := make([]map[string]string, 0, len(atoms))
	for _, a := range atoms {
		items = append(items, map[string]string{"id": a.AtomID, "type": a.Type, "link": a.Link, "name": a.Name})
	}
	return e.JSON(200, map[string]any{"contract_version": 1, "required_capabilities": []string{"contacts.delta.v1"}, "arcade": arcade.Id, "revision": revision, "items": items})
}

func PatchNativeSNSV1(e *core.RequestEvent) error {
	var patch nativeSNSPatch
	// Decode failures are written exactly once here and never reach the update.
	if err := arcadeinternal.DecodeNativeEdit(e, &patch); err != nil {
		return arcadeinternal.RespondNativeEditError(e, err)
	}
	if patch.Revision == nil || len(patch.Add)+len(patch.Update)+len(patch.Remove) == 0 {
		return arcadeinternal.RespondNativeEditError(e, arcadeinternal.NativeBadRequest("revision_and_operations_required"))
	}
	return updateArcadeSNS(e, UpdateArcadeSNSBody{Arcade: e.Request.PathValue("id")}, &patch)
}

func mergeNativeSNS(app core.App, previous []snsAtomState, patch nativeSNSPatch) ([]SNSAtomInput, error) {
	invalid := func(code string) error { return arcadeinternal.NativeBadRequest(code) }
	indexes := map[string]int{}
	result := make([]SNSAtomInput, 0, len(previous)+len(patch.Add))
	for i, a := range previous {
		record, err := app.FindRecordById(arcadeinternal.CollectionArcadeSNSAtoms, a.AtomID)
		if err != nil {
			return nil, err
		}
		indexes[a.AtomID] = i
		result = append(result, SNSAtomInput{Type: a.Type, Link: a.Link, Name: a.Name, source: record, nativeFields: map[string]bool{}})
	}
	touched := map[string]bool{}
	removed := map[string]bool{}
	changed := false
	for _, id := range patch.Remove {
		if _, ok := indexes[id]; !ok || touched[id] {
			return nil, invalid("invalid_contact_id")
		}
		touched[id] = true
		removed[id] = true
		changed = true
	}
	for _, update := range patch.Update {
		index, ok := indexes[update.ID]
		if !ok || touched[update.ID] || len(update.Changes) == 0 {
			return nil, invalid("invalid_contact_id")
		}
		touched[update.ID] = true
		item := &result[index]
		for key, raw := range update.Changes {
			if key != "link" && key != "name" {
				return nil, invalid("unsupported_field")
			}
			var value *string
			if err := json.Unmarshal(raw, &value); err != nil || value == nil {
				return nil, invalid("invalid_contact_value")
			}
			valueText := strings.TrimSpace(*value)
			if key == "link" {
				normalized, err := normalizeNativeContact(item.Type, valueText)
				if err != nil {
					return nil, err
				}
				if normalized != item.Link {
					changed = true
				}
				item.Link = normalized
			} else {
				if valueText != item.Name {
					changed = true
				}
				item.Name = valueText
			}
			item.nativeFields[key] = true
		}
	}
	kept := make([]SNSAtomInput, 0, len(result)+len(patch.Add))
	for _, item := range result {
		if !removed[item.source.Id] {
			kept = append(kept, item)
		}
	}
	for _, item := range patch.Add {
		item = normalizeSNSInput(item)
		link, err := normalizeNativeContact(item.Type, item.Link)
		if err != nil {
			return nil, err
		}
		item.Link = link
		kept = append(kept, item)
		changed = true
	}
	if !changed {
		return nil, invalid("no_changes")
	}
	return kept, nil
}

// Canonical type/host rules match the web editor; untouched legacy/future types
// are never revalidated or removed by another contact's edit.
func normalizeNativeContact(kind, value string) (string, error) {
	invalid := arcadeinternal.NativeBadRequest("invalid_contact_link")
	if kind == "phone" {
		normalized := arcadeinternal.NormalizePhoneValue(value)
		digits := strings.TrimPrefix(normalized, "+")
		if len(digits) < 7 {
			return "", invalid
		}
		return normalized, nil
	}
	hosts := map[string][]string{
		"website": {}, "reservation": {}, "facebook": {"facebook.com", "fb.com"}, "instagram": {"instagram.com"},
		"twitter": {"x.com", "twitter.com"}, "youtube": {"youtube.com", "youtu.be"}, "discord": {"discord.gg", "discordapp.com", "discord.com"},
		"kakaoopenchat": {"open.kakao.com"}, "kakaochannel": {"pf.kakao.com", "kakao.com"}, "threads": {"threads.net"},
	}
	allowed, ok := hosts[kind]
	if !ok {
		return "", arcadeinternal.NativeBadRequest("unsupported_contact_type")
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || !strings.Contains(u.Hostname(), ".") || u.User != nil {
		return "", invalid
	}
	if len(allowed) > 0 {
		matched := false
		host := strings.ToLower(u.Hostname())
		for _, allowedHost := range allowed {
			if host == allowedHost || strings.HasSuffix(host, "."+allowedHost) {
				matched = true
			}
		}
		if !matched {
			return "", invalid
		}
	}
	return u.String(), nil
}
