package announcement

import (
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

var Locales = []string{"ko", "en", "ja"}

type Slide struct {
	FileName string `json:"fileName"`
	Alt      string `json:"alt"`
}
type Localization struct {
	Title   string  `json:"title"`
	Summary string  `json:"summary"`
	Slides  []Slide `json:"slides"`
}
type Copy struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

func Read(record *core.Record) map[string]Localization {
	result := map[string]Localization{}
	_ = json.Unmarshal([]byte(record.GetString("localizations")), &result)
	return result
}

func ValidatePublished(record *core.Record, locales []string) error {
	localized := Read(record)
	for _, locale := range locales {
		content, exists := localized[locale]
		if !exists || strings.TrimSpace(content.Title) == "" || len(content.Slides) == 0 || len(content.Slides) > MaxSlides {
			return fmt.Errorf("%s requires a title and 1..%d slides", locale, MaxSlides)
		}
		for _, slide := range content.Slides {
			if strings.TrimSpace(slide.Alt) == "" {
				return fmt.Errorf("%s slide requires alt text", locale)
			}
		}
	}
	return nil
}

func Snapshot(record *core.Record) map[string]Copy {
	copy := map[string]Copy{}
	for locale, localized := range Read(record) {
		if !HasLocale(record, locale) {
			continue
		}
		copy[locale] = Copy{Title: localized.Title, Summary: localized.Summary}
	}
	return copy
}

func URL(id string) string { return "/community/updates?announcement=" + url.QueryEscape(id) }

func Item(record *core.Record, viewed bool) map[string]any {
	localizations := map[string]any{}
	for locale, content := range Read(record) {
		slides := make([]map[string]string, 0, len(content.Slides))
		for _, slide := range content.Slides {
			slides = append(slides, map[string]string{"id": slide.FileName, "fileName": slide.FileName, "alt": slide.Alt, "imageUrl": "/announcements/" + record.Id + "/images/" + url.PathEscape(slide.FileName)})
		}
		localizations[locale] = map[string]any{"title": content.Title, "summary": content.Summary, "slides": slides}
	}
	return map[string]any{"id": record.Id, "status": record.GetString("status"), "createdAt": record.GetString("created"), "updatedAt": record.GetString("updated"), "publishedAt": record.GetString("published_at"), "viewed": viewed, "localizations": localizations, "locales": record.GetStringSlice("locales")}
}

func HasLocale(record *core.Record, locale string) bool {
	return slices.Contains(record.GetStringSlice("locales"), locale)
}

// PublicItem never exposes unfinished variants that were not selected for release.
func PublicItem(record *core.Record, viewed bool) map[string]any {
	item := Item(record, viewed)
	localized := item["localizations"].(map[string]any)
	for locale := range localized {
		if !HasLocale(record, locale) {
			delete(localized, locale)
		}
	}
	return item
}

func PublicImage(record *core.Record, filename string) bool {
	if record.GetString("status") != "published" {
		return false
	}
	for locale, content := range Read(record) {
		if !HasLocale(record, locale) {
			continue
		}
		for _, slide := range content.Slides {
			if slide.FileName == filename {
				return true
			}
		}
	}
	return false
}
