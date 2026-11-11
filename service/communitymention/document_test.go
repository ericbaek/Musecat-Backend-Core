package communitymention

import (
	"encoding/json"
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
	raw := []byte(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Bold","marks":[{"type":"bold"},{"type":"textStyle","attrs":{"fontSize":"20px"}}]}]}]}`)
	_, text, _, err := ParseDocument(raw)
	if err != nil || text != "Bold" {
		t.Fatal(text, err)
	}
	for _, bad := range []string{`{"type":"doc","content":[{"type":"iframe"}]}`, `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}]}`, string(raw) + ` {}`} {
		if _, _, _, err := ParseDocument([]byte(bad)); err == nil {
			t.Fatal("invalid document accepted", bad)
		}
	}
}
