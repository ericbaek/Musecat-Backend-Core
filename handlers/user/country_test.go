package user

import "testing"

func TestNormalizeProfileCountries(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
		valid bool
	}{
		{name: "empty", input: []string{}, want: []string{}, valid: true},
		{name: "normalizes and preserves order", input: []string{" kr ", "JP", "us"}, want: []string{"KR", "JP", "US"}, valid: true},
		{name: "duplicate", input: []string{"KR", "kr"}},
		{name: "invalid", input: []string{"ZZ"}},
		{name: "non alpha2", input: []string{"KOR"}},
		{name: "too many", input: []string{"KR", "JP", "US", "AU"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeProfileCountries(tc.input)
			if tc.valid && err != nil {
				t.Fatalf("NormalizeProfileCountries(%v): %v", tc.input, err)
			}
			if !tc.valid && err == nil {
				t.Fatalf("NormalizeProfileCountries(%v) unexpectedly succeeded", tc.input)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestResolveProfileCountries(t *testing.T) {
	stats := VisitStats{Arcades: []ArcadeVisitCount{
		{Arcade: "kr-1", Country: "KR", VisitCount: 4, lastVisitedAt: "2026-09-10T00:00:00Z"},
		{Arcade: "jp-1", Country: "JP", VisitCount: 4, lastVisitedAt: "2026-09-11T00:00:00Z"},
		{Arcade: "jp-2", Country: "JP", VisitCount: 1, lastVisitedAt: "2026-09-01T00:00:00Z"},
	}}

	countries, primary := ResolveProfileCountries([]string{"AU"}, countryModeAuto, stats)
	if primary != "JP" || len(countries) != 1 || countries[0] != "JP" {
		t.Fatalf("auto country=%v primary=%q, want JP", countries, primary)
	}
	countries, primary = ResolveProfileCountries([]string{"AU", "KR"}, countryModeManual, stats)
	if primary != "AU" || len(countries) != 2 {
		t.Fatalf("manual country=%v primary=%q", countries, primary)
	}
	countries, primary = ResolveProfileCountries([]string{"AU"}, countryModeOff, stats)
	if len(countries) != 0 || primary != "" {
		t.Fatalf("off country=%v primary=%q", countries, primary)
	}
}

func TestResolveStoredProfileCountries(t *testing.T) {
	countries, primary := ResolveStoredProfileCountries([]string{"KR"}, countryModeAuto, "JP")
	if primary != "JP" || len(countries) != 1 || countries[0] != "JP" {
		t.Fatalf("auto country=%v primary=%q, want JP", countries, primary)
	}
	countries, primary = ResolveStoredProfileCountries([]string{"KR", "JP"}, countryModeManual, "AU")
	if primary != "KR" || len(countries) != 2 {
		t.Fatalf("manual country=%v primary=%q", countries, primary)
	}
	countries, primary = ResolveStoredProfileCountries([]string{"KR"}, countryModeOff, "JP")
	if len(countries) != 0 || primary != "" {
		t.Fatalf("off country=%v primary=%q", countries, primary)
	}
}
