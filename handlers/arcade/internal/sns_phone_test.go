package arcadeinternal

import "testing"

func TestNormalizePhoneValue(t *testing.T) {
	t.Parallel()

	if got := NormalizePhoneValue("010-1234-5678"); got != "01012345678" {
		t.Fatalf("expected normalized local number, got %q", got)
	}
	if got := NormalizePhoneValue("+82 (10) 1234-5678"); got != "+821012345678" {
		t.Fatalf("expected normalized intl number, got %q", got)
	}
}

func TestResolveSNSLinkForOutput(t *testing.T) {
	t.Parallel()

	if got := ResolveSNSLinkForOutput("phone", "", "010-1234-5678"); got != "01012345678" {
		t.Fatalf("expected phone field to be preferred, got %q", got)
	}
	if got := ResolveSNSLinkForOutput("phone", "010.5678.1234", ""); got != "01056781234" {
		t.Fatalf("expected legacy link fallback, got %q", got)
	}
	if got := ResolveSNSLinkForOutput("website", "https://example.com", "0101234"); got != "https://example.com" {
		t.Fatalf("expected non-phone passthrough, got %q", got)
	}
}
