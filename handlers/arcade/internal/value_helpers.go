package arcadeinternal

import (
	"encoding/json"
	"fmt"
)

// AsString converts common PB value types to string.
func AsString(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case []byte:
		var s string
		if json.Unmarshal(t, &s) == nil {
			return s, true
		}
		return string(t), true
	case json.RawMessage:
		var s string
		if json.Unmarshal(t, &s) == nil {
			return s, true
		}
		return string(t), true
	case fmt.Stringer:
		return t.String(), true
	default:
		if t == nil {
			return "", false
		}
		return fmt.Sprintf("%v", t), true
	}
}
