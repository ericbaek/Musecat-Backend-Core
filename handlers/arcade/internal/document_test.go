package arcadeinternal

import (
	"encoding/json"
	"testing"
)

func TestValidateDocument(t *testing.T) {
	for _, raw := range []string{
		`{"type":"doc"}`,
		`{"type":"doc","content":[]}`,
		`{"type":"doc","content":[{"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"Title"}]}]}`,
		`{"type":"doc","content":[{"type":"customExtension","attrs":{"custom":true},"content":[{"type":"text","text":"hello","marks":[{"type":"link","attrs":{"href":"https://example.com"}}]}]}]}`,
	} {
		var doc map[string]any
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		if err := ValidateDocument(doc); err != nil {
			t.Fatalf("rejected valid document: %v", err)
		}
	}
	for _, raw := range []string{
		`{"type":"doc","content":[null]}`,
		`{"type":"doc","content":[{"type":"heading","attrs":{"level":-1}}]}`,
		`{"type":"doc","content":[{"type":"heading","attrs":{"level":{}}}]}`,
		`{"type":"doc","content":[{"type":"paragraph","content":{}}]}`,
		`{"type":"doc","content":[{"type":"text"}]}`,
		`{"type":"doc","content":[{"type":"text","text":[]}]}`,
		`{"type":"doc","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":[]}]}]}`,
		`{"type":"doc","content":[{"type":"paragraph","attrs":null}]}`,
	} {
		var doc map[string]any
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatal(err)
		}
		if ValidateDocument(doc) == nil {
			t.Fatalf("accepted malformed document: %s", raw)
		}
	}
}

func TestValidateDocumentTraversalLimits(t *testing.T) {
	doc := map[string]any{"type": "doc"}
	node := doc
	for range 65 {
		child := map[string]any{"type": "blockquote"}
		node["content"] = []any{child}
		node = child
	}
	if ValidateDocument(doc) == nil {
		t.Fatal("accepted excessive depth")
	}
	children := make([]any, 5000)
	for i := range children {
		children[i] = map[string]any{"type": "paragraph"}
	}
	if ValidateDocument(map[string]any{"type": "doc", "content": children}) == nil {
		t.Fatal("accepted excessive node count")
	}
	if err := ValidateDocument(map[string]any{"type": "doc", "content": children[:4999]}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateDocumentRejectsDeepAttributes(t *testing.T) {
	attrs := map[string]any{}
	value := attrs
	for range 200 {
		next := map[string]any{}
		value["nested"] = next
		value = next
	}
	if ValidateDocument(map[string]any{"type": "doc", "attrs": attrs}) == nil {
		t.Fatal("accepted deeply nested attributes")
	}
}

func TestValidateDocumentRejectsExcessiveMarks(t *testing.T) {
	marks := make([]any, 10000)
	for i := range marks {
		marks[i] = map[string]any{"type": "bold"}
	}
	doc := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "text", "text": "x", "marks": marks}}}
	if ValidateDocument(doc) == nil {
		t.Fatal("accepted excessive mark nesting")
	}
}
