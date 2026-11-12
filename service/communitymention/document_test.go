package communitymention

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLaunchDocumentRoundTrip(t *testing.T) {
	for _, body := range []string{"", "<b>literal</b> & text", "one\ntwo\n", "🎉 @player"} {
		mentions := []Mention{}
		if body == "🎉 @player" {
			mentions = []Mention{{Kind: "user", ID: "abcdefghijklmno", Label: "player", Start: 3, End: 10}}
		}
		doc := DocumentFromText(body, mentions)
		raw, _ := json.Marshal(doc)
		_, text, got, err := ParseDocument(raw)
		if err != nil || text != body || len(got) != len(mentions) {
			t.Fatal(string(raw), text, got, err)
		}
	}
}
func TestRestrictedRichTextSchema(t *testing.T) {
	raw := []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Bold"}]}]}`)
	_, text, _, err := ParseDocument(raw)
	if err != nil || text != "Bold" {
		t.Fatal(text, err)
	}
	for _, bad := range []string{`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"bold"}]}]}]}`, `{"type":"doc","content":[{"type":"iframe"}]}`, `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}]}`, string(raw) + ` {}`} {
		if _, _, _, err := ParseDocument([]byte(bad)); err == nil {
			t.Fatal("invalid document accepted", bad)
		}
	}
}

func TestDocumentBoundsApplyWithoutRequestHandler(t *testing.T) {
	for _, doc := range []Node{
		DocumentFromText(strings.Repeat("x", 10001), nil),
		{Type: "doc", Content: []Node{{Type: "paragraph", Content: []Node{{Type: "mention", Attrs: map[string]string{"kind": "html", "id": "abcdefghijklmno", "label": "bad"}}}}}},
		{Type: "doc", Content: []Node{{Type: "paragraph", Content: []Node{{Type: "mention", Attrs: map[string]string{"kind": "user", "id": "invalid", "label": "bad"}}}}}},
	} {
		raw, _ := json.Marshal(doc)
		if _, _, _, err := ParseDocument(raw); err == nil {
			t.Fatal("invalid canonical document accepted")
		}
	}
}
