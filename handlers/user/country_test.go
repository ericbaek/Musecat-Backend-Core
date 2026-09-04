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
