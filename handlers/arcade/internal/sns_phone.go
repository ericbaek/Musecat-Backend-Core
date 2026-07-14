package arcadeinternal

import (
	"strings"
	"unicode"
)

func IsPhoneSNSType(snsType string) bool {
	return strings.EqualFold(strings.TrimSpace(snsType), "phone")
}

func NormalizePhoneValue(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	var b strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsDigit(r):
			b.WriteRune(r)
		case r == '+' && b.Len() == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ResolveSNSLinkForOutput keeps API compatibility by always returning `link`.
// For phone type, prefer the new `phone` field and fallback to legacy `link`.
func ResolveSNSLinkForOutput(snsType, link, phone string) string {
	if !IsPhoneSNSType(snsType) {
		return strings.TrimSpace(link)
	}

	if normalized := NormalizePhoneValue(phone); normalized != "" {
		return normalized
	}
	if normalized := NormalizePhoneValue(link); normalized != "" {
		return normalized
	}
	return ""
}
