package communitymention

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
)

type Mark struct {
	Type  string            `json:"type"`
	Attrs map[string]string `json:"attrs,omitempty"`
}
type Node struct {
	Type    string            `json:"type"`
	Attrs   map[string]string `json:"attrs,omitempty"`
	Content []Node            `json:"content,omitempty"`
	Text    string            `json:"text,omitempty"`
	Marks   []Mark            `json:"marks,omitempty"`
}

// DocumentFromText is the legacy/input adapter. Mention identities become inline nodes.
func DocumentFromText(body string, mentions []Mention) Node {
	doc := Node{Type: "doc"}
	offset := 0
	for _, line := range strings.Split(body, "\n") {
		paragraph := Node{Type: "paragraph"}
		units := utf16.Encode([]rune(line))
		cursor := 0
		for _, m := range mentions {
			if m.Start < offset || m.End > offset+len(units) {
				continue
			}
			start, end := m.Start-offset, m.End-offset
			if start > cursor {
				paragraph.Content = append(paragraph.Content, Node{Type: "text", Text: string(utf16.Decode(units[cursor:start]))})
			}
			paragraph.Content = append(paragraph.Content, Node{Type: "mention", Attrs: map[string]string{"kind": m.Kind, "id": m.ID, "label": m.Label}})
			cursor = end
		}
		if cursor < len(units) {
			paragraph.Content = append(paragraph.Content, Node{Type: "text", Text: string(utf16.Decode(units[cursor:]))})
		}
		doc.Content = append(doc.Content, paragraph)
		offset += len(units) + 1
	}
	return doc
}

// ParseDocument is the server-owned launch schema: paragraphs, breaks, mentions,
// and plain text. Formatting marks are rejected. Text/ranges are derived on read.
func ParseDocument(raw []byte) (Node, string, []Mention, error) {
	var doc Node
	fail := func() (Node, string, []Mention, error) { return Node{}, "", nil, fmt.Errorf("invalid body_document") }
	if len(raw) > 65536 {
		return fail()
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&doc); err != nil {
		return fail()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fail()
	}
	if doc.Type != "doc" || len(doc.Content) == 0 || len(doc.Content) > 500 || len(doc.Attrs) > 0 || doc.Text != "" || len(doc.Marks) > 0 {
		return fail()
	}
	var body strings.Builder
	mentions := []Mention{}
	position, nodes := 0, 0
	write := func(text string) { body.WriteString(text); position += len(utf16.Encode([]rune(text))) }
	for i, p := range doc.Content {
		if p.Type != "paragraph" || p.Text != "" || len(p.Attrs) > 0 || len(p.Marks) > 0 {
			return fail()
		}
		if i > 0 {
			write("\n")
		}
		for _, n := range p.Content {
			nodes++
			if nodes > 2000 || len(n.Content) > 0 {
				return fail()
			}
			switch n.Type {
			case "text":
				if n.Text == "" || len(n.Attrs) > 0 || len(n.Marks) > 0 {
					return fail()
				}
				write(n.Text)
			case "hardBreak":
				if n.Text != "" || len(n.Attrs) > 0 || len(n.Marks) > 0 {
					return fail()
				}
				write("\n")
			case "mention":
				if n.Text != "" || len(n.Marks) > 0 || len(n.Attrs) != 3 || strings.ContainsAny(n.Attrs["label"], "\r\n") {
					return fail()
				}
				start := position
				write("@" + n.Attrs["label"])
				mentions = append(mentions, Mention{Kind: n.Attrs["kind"], ID: n.Attrs["id"], Label: n.Attrs["label"], Start: start, End: position})
			default:
				return fail()
			}
		}
	}
	return doc, body.String(), mentions, nil
}
