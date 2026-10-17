package arcadeinternal

import "fmt"

// ValidateDocument checks the recursive wire shape without restricting extension
// node names. Bound traversal before the document reaches a recursive renderer.
func ValidateDocument(document map[string]any) error {
	remaining := 5000
	var visit func(any, int) bool
	visit = func(value any, depth int) bool {
		remaining--
		if depth > 64 || remaining < 0 {
			return false
		}
		node, ok := value.(map[string]any)
		if !ok {
			return false
		}
		kind, ok := node["type"].(string)
		if !ok || kind == "" {
			return false
		}
		if text, exists := node["text"]; exists {
			if _, ok := text.(string); !ok {
				return false
			}
		} else if kind == "text" {
			return false
		}
		if attrs, exists := node["attrs"]; exists {
			if _, ok := attrs.(map[string]any); !ok {
				return false
			}
		}
		if kind == "heading" {
			if attrs, ok := node["attrs"].(map[string]any); ok {
				if raw, exists := attrs["level"]; exists {
					level, ok := raw.(float64)
					if !ok || level < 1 || level > 6 || level != float64(int(level)) {
						return false
					}
				}
			}
		}
		if raw, exists := node["marks"]; exists {
			marks, ok := raw.([]any)
			if !ok || len(marks) > 32 {
				return false
			}
			for _, value := range marks {
				mark, ok := value.(map[string]any)
				if !ok {
					return false
				}
				kind, ok := mark["type"].(string)
				if !ok || kind == "" {
					return false
				}
				if attrs, exists := mark["attrs"]; exists {
					if _, ok := attrs.(map[string]any); !ok {
						return false
					}
				}
			}
		}
		if raw, exists := node["content"]; exists {
			children, ok := raw.([]any)
			if !ok {
				return false
			}
			for _, child := range children {
				if !visit(child, depth+1) {
					return false
				}
			}
		}
		return true
	}
	if document["type"] != "doc" || !documentNestingSafe(document, 0) || !visit(document, 0) {
		return fmt.Errorf("document must contain valid rich-text nodes (maximum depth 64 and 5000 nodes)")
	}
	return nil
}

// Attributes and extension metadata also reach JSON serialization in editors.
func documentNestingSafe(value any, depth int) bool {
	if depth > 128 {
		return false
	}
	switch value := value.(type) {
	case map[string]any:
		for _, child := range value {
			if !documentNestingSafe(child, depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range value {
			if !documentNestingSafe(child, depth+1) {
				return false
			}
		}
	}
	return true
}
