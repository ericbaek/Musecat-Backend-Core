package arcadeinternal

import (
	"testing"
	"time"
)

func TestParsePhotoCaptureTime(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name, raw, timezone, want string
		invalid                   bool
	}{
		{"EXIF offset", "2025-02-03T17:47:27+09:00", "Australia/Sydney", "2025-02-03T08:47:27Z", false},
		{"venue timezone", "2025-02-03T17:47:27", "Asia/Seoul", "2025-02-03T08:47:27Z", false},
		{"UTC fallback", "2025-02-03T17:47:27", "", "2025-02-03T17:47:27Z", false},
		{"invalid date", "2025-02-30T17:47:27", "", "", true},
		{"future", "2027-01-01T00:00:00Z", "", "", true},
		{"invalid timezone", "2025-02-03T17:47:27", "invalid", "", true},
		{"absent", "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, err := ParsePhotoCaptureTime(tc.raw, tc.timezone, now)
			if (err != nil) != tc.invalid {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.want != "" && at.Format(time.RFC3339) != tc.want {
				t.Fatalf("got %s, want %s", at, tc.want)
			}
			if tc.raw == "" && !at.IsZero() {
				t.Fatal("absent capture date should stay empty")
			}
		})
	}
}
