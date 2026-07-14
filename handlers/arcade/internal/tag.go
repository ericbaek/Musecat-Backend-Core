package arcadeinternal

import "encoding/json"

// DecodeGameTagPayload converts stored tag JSON into a response shape.
// tag.quantity is removed on read so legacy values do not leak back to clients.
func DecodeGameTagPayload(raw any) []map[string]any {
	if raw == nil {
		return nil
	}

	buf, err := json.Marshal(raw)
	if err != nil {
		return nil
	}

	var decoded []map[string]any
	if err := json.Unmarshal(buf, &decoded); err != nil {
		return nil
	}

	normalized, ok := stripGameTagQuantity(decoded).([]map[string]any)
	if !ok {
		return nil
	}

	return normalized
}

// NormalizeGameTagPayload removes the tag.quantity field from any game tag payload
// before it is stored.
func NormalizeGameTagPayload(raw any) any {
	if raw == nil {
		return nil
	}

	buf, err := json.Marshal(raw)
	if err != nil {
		return raw
	}

	var decoded any
	if err := json.Unmarshal(buf, &decoded); err != nil {
		return raw
	}

	return stripGameTagQuantity(decoded)
}

func stripGameTagQuantity(raw any) any {
	switch value := raw.(type) {
	case map[string]any:
		out := make(map[string]any, len(value))
		for key, item := range value {
			if key == "quantity" {
				continue
			}
			out[key] = stripGameTagQuantity(item)
		}
		return out
	case []map[string]any:
		out := make([]map[string]any, 0, len(value))
		for _, item := range value {
			normalized := stripGameTagQuantity(item)
			tag, ok := normalized.(map[string]any)
			if !ok {
				continue
			}
			out = append(out, tag)
		}
		return out
	case []any:
		out := make([]any, 0, len(value))
		for _, item := range value {
			out = append(out, stripGameTagQuantity(item))
		}
		return out
	default:
		return raw
	}
}
