package notice

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
)

var (
	legacyHeadingPattern = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
	legacyOrderedPattern = regexp.MustCompile(`^\d+\.\s+(.+)$`)
	legacyAnchorPattern  = regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	legacyTagPattern     = regexp.MustCompile(`(?is)<[^>]+>`)
)

func normalizeNoticeDocument(raw json.RawMessage) (map[string]any, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, fmt.Errorf("notice document is required")
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("notice document must be a JSON object")
	}
	if document["type"] != "doc" {
		return nil, fmt.Errorf("notice document type must be doc")
	}
	if content, ok := document["content"]; ok {
		if _, ok := content.([]any); !ok {
			return nil, fmt.Errorf("notice document content must be an array")
		}
	}
	return document, nil
}

func LegacyMarkdownToDocument(markdown string) map[string]any {
	markdown = legacyHTMLToMarkdown(markdown)
	lines := strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n")
	content := make([]any, 0, len(lines))
	for index := 0; index < len(lines); {
		line := lines[index]
		if strings.TrimSpace(line) == "" {
			index++
			continue
		}
		if strings.TrimSpace(line) == "---" {
			content = append(content, map[string]any{"type": "horizontalRule"})
			index++
			continue
		}
		if matches := legacyHeadingPattern.FindStringSubmatch(line); matches != nil {
			content = append(content, map[string]any{
				"type":    "heading",
				"attrs":   map[string]any{"level": len(matches[1])},
				"content": legacyInlineContent(matches[2]),
			})
			index++
			continue
		}
		if strings.HasPrefix(line, "> ") {
			content = append(content, map[string]any{
				"type": "blockquote",
				"content": []any{map[string]any{
					"type":    "paragraph",
					"content": legacyInlineContent(strings.TrimPrefix(line, "> ")),
				}},
			})
			index++
			continue
		}
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			items := make([]any, 0)
			for index < len(lines) && (strings.HasPrefix(lines[index], "- ") || strings.HasPrefix(lines[index], "* ")) {
				items = append(items, map[string]any{
					"type": "listItem",
					"content": []any{map[string]any{
						"type":    "paragraph",
						"content": legacyInlineContent(strings.TrimSpace(lines[index][2:])),
					}},
				})
				index++
			}
			content = append(content, map[string]any{"type": "bulletList", "content": items})
			continue
		}
		if legacyOrderedPattern.MatchString(line) {
			items := make([]any, 0)
			for index < len(lines) {
				matches := legacyOrderedPattern.FindStringSubmatch(lines[index])
				if matches == nil {
					break
				}
				items = append(items, map[string]any{
					"type": "listItem",
					"content": []any{map[string]any{
						"type":    "paragraph",
						"content": legacyInlineContent(matches[1]),
					}},
				})
				index++
			}
			content = append(content, map[string]any{"type": "orderedList", "content": items})
			continue
		}
		content = append(content, map[string]any{
			"type":    "paragraph",
			"content": legacyInlineContent(line),
		})
		index++
	}
	return map[string]any{"type": "doc", "content": content}
}

func legacyHTMLToMarkdown(value string) string {
	if !strings.Contains(value, "<") {
		return value
	}

	value = html.UnescapeString(value)
	value = regexp.MustCompile(`(?is)<br\s*/?>`).ReplaceAllString(value, "\n")
	value = regexp.MustCompile(`(?is)<h([1-6])[^>]*>`).ReplaceAllString(value, "\n$1 ")
	value = regexp.MustCompile(`(?is)</h[1-6]>`).ReplaceAllString(value, "\n")
	value = regexp.MustCompile(`(?is)<(?:p|div)[^>]*>`).ReplaceAllString(value, "")
	value = regexp.MustCompile(`(?is)</(?:p|div)>`).ReplaceAllString(value, "\n")
	value = regexp.MustCompile(`(?is)<li[^>]*>`).ReplaceAllString(value, "- ")
	value = regexp.MustCompile(`(?is)</li>`).ReplaceAllString(value, "\n")
	value = regexp.MustCompile(`(?is)<hr\s*/?>`).ReplaceAllString(value, "\n---\n")
	value = regexp.MustCompile(`(?is)<(?:strong|b)[^>]*>`).ReplaceAllString(value, "**")
	value = regexp.MustCompile(`(?is)</(?:strong|b)>`).ReplaceAllString(value, "**")
	value = regexp.MustCompile(`(?is)<(?:em|i)[^>]*>`).ReplaceAllString(value, "*")
	value = regexp.MustCompile(`(?is)</(?:em|i)>`).ReplaceAllString(value, "*")
	value = regexp.MustCompile(`(?is)<(?:s|del)[^>]*>`).ReplaceAllString(value, "~~")
	value = regexp.MustCompile(`(?is)</(?:s|del)>`).ReplaceAllString(value, "~~")
	value = regexp.MustCompile(`(?is)<code[^>]*>`).ReplaceAllString(value, "`")
	value = regexp.MustCompile(`(?is)</code>`).ReplaceAllString(value, "`")
	value = legacyAnchorPattern.ReplaceAllString(value, "[$2]($1)")
	value = legacyTagPattern.ReplaceAllString(value, "")
	return strings.TrimSpace(value)
}

func legacyInlineContent(value string) []any {
	content := make([]any, 0, 1)
	for len(value) > 0 {
		markType, opener, closer := "", "", ""
		switch {
		case strings.HasPrefix(value, "**"):
			markType, opener, closer = "bold", "**", "**"
		case strings.HasPrefix(value, "~~"):
			markType, opener, closer = "strike", "~~", "~~"
		case strings.HasPrefix(value, "*"):
			markType, opener, closer = "italic", "*", "*"
		case strings.HasPrefix(value, "`"):
			markType, opener, closer = "code", "`", "`"
		}
		if markType != "" {
			end := strings.Index(value[len(opener):], closer)
			if end >= 0 {
				end += len(opener)
				content = append(content, map[string]any{
					"type":  "text",
					"text":  value[len(opener):end],
					"marks": []any{map[string]any{"type": markType}},
				})
				value = value[end+len(closer):]
				continue
			}
		}
		if strings.HasPrefix(value, "[") {
			labelEnd := strings.Index(value, "](")
			if labelEnd > 0 {
				urlEnd := strings.Index(value[labelEnd+2:], ")")
				if urlEnd >= 0 {
					urlEnd += labelEnd + 2
					content = append(content, map[string]any{
						"type": "text",
						"text": value[1:labelEnd],
						"marks": []any{map[string]any{
							"type":  "link",
							"attrs": map[string]any{"href": value[labelEnd+2 : urlEnd]},
						}},
					})
					value = value[urlEnd+1:]
					continue
				}
			}
		}
		next := len(value)
		for _, marker := range []string{"**", "~~", "*", "`", "["} {
			if index := strings.Index(value[1:], marker); index >= 0 && index+1 < next {
				next = index + 1
			}
		}
		content = append(content, map[string]any{"type": "text", "text": value[:next]})
		value = value[next:]
	}
	return content
}
