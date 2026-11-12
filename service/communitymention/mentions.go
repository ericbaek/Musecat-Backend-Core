// Package communitymention owns plain-text caption mention metadata.
package communitymention

import (
	"encoding/json"
	"fmt"
	"github.com/pocketbase/pocketbase/core"
	"strings"
	"unicode/utf16"
)

type Mention struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Label string `json:"label"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

func Read(post *core.Record) []Mention {
	result := []Mention{}
	raw, _ := json.Marshal(post.GetRaw("mentions"))
	_ = json.Unmarshal(raw, &result)
	if result == nil {
		return []Mention{}
	}
	return result
}

// Validate uses browser UTF-16 offsets and verifies both identity and visible label.
func Validate(app core.App, body string, mentions []Mention) error {
	if len(mentions) > 10 {
		return fmt.Errorf("at most ten mentions are allowed")
	}
	if err := ValidateRanges(body, mentions); err != nil {
		return err
	}
	for _, mention := range mentions {
		switch mention.Kind {
		case "user":
			user, err := app.FindRecordById("user", mention.ID)
			if err != nil || user.GetBool("withdrawn") || user.GetString("username") != mention.Label {
				return fmt.Errorf("invalid user mention")
			}
		case "arcade":
			arcade, err := app.FindRecordById("arcade", mention.ID)
			if err != nil || !arcade.GetBool("public") {
				return fmt.Errorf("invalid arcade mention")
			}
			basic, err := app.FindRecordById("arcade_basic", arcade.GetString("basic"))
			if err != nil || basic.GetString("name") != mention.Label {
				return fmt.Errorf("invalid arcade mention")
			}
		default:
			return fmt.Errorf("invalid mention kind")
		}
	}
	return nil
}

func EnsureSchema(app core.App) error {
	col, err := app.FindCollectionByNameOrId("community_post")
	if err != nil {
		return err
	}
	if !col.IsBase() || col.System || col.ListRule != nil || col.ViewRule != nil || col.DeleteRule != nil || col.CreateRule != nil || col.UpdateRule != nil {
		return fmt.Errorf("incompatible community post schema")
	}
	for _, spec := range []struct {
		name string
		size int64
	}{{"mentions", 8192}, {"body_document", 65536}} {
		if old := col.Fields.GetByName(spec.name); old != nil {
			field, ok := old.(*core.JSONField)
			if !ok || (field.Required && spec.name != "body_document") || field.MaxSize != spec.size {
				return fmt.Errorf("incompatible %s field", spec.name)
			}
		} else {
			col.Fields.Add(&core.JSONField{Name: spec.name, MaxSize: spec.size})
		}
	}
	return app.Save(col)
}

func ValidateRanges(body string, mentions []Mention) error {
	units := utf16.Encode([]rune(body))
	previous := 0
	for _, mention := range mentions {
		if mention.Start < previous || mention.End <= mention.Start || mention.End > len(units) || mention.ID == "" || mention.Label == "" || strings.ContainsAny(mention.Label, "\r\n") || string(utf16.Decode(units[mention.Start:mention.End])) != "@"+mention.Label {
			return fmt.Errorf("invalid mention range")
		}
		previous = mention.End
	}
	return nil
}
