package arcadeinternal

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

type I18nBullet struct {
	Key    string         `json:"key"`
	Params map[string]any `json:"params,omitempty"`
}

// BuildI18nBullet standardizes the bullet payload shape used by all changelogs.
func BuildI18nBullet(key string, params map[string]any) I18nBullet {
	b := I18nBullet{Key: key}
	if len(params) > 0 {
		b.Params = params
	}
	return b
}

func AppendDiffEntry(entries []map[string]any, field string, from, to any) []map[string]any {
	return append(entries, map[string]any{
		"field": field,
		"from":  from,
		"to":    to,
	})
}

// JSONValueEqual compares normalized JSON payloads so nil-vs-empty serialization
// differences do not create noisy changelog entries.
func JSONValueEqual(a, b any) bool {
	ab, aErr := json.Marshal(a)
	bb, bErr := json.Marshal(b)
	if aErr != nil || bErr != nil {
		return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
	}

	var av any
	var bv any
	if err := json.Unmarshal(ab, &av); err != nil {
		return string(ab) == string(bb)
	}
	if err := json.Unmarshal(bb, &bv); err != nil {
		return string(ab) == string(bb)
	}

	return reflect.DeepEqual(av, bv)
}

func DisplayDiffText(v string) string {
	trimmed := strings.TrimSpace(v)
	if trimmed == "" {
		return "(없음)"
	}
	return trimmed
}

// BuildChangelogEnvelope keeps every field-specific log under the same versioned wrapper.
func BuildChangelogEnvelope(field string, items any) map[string]any {
	return map[string]any{
		"type":    fmt.Sprintf("%s_diff", field),
		"version": 1,
		"items":   items,
	}
}

func TrimmedStringSlice(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}

	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
