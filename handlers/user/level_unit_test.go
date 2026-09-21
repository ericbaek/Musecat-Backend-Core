package user

import (
	"testing"
	"time"
)

func TestLevelFromExpTable(t *testing.T) {
	tests := []struct {
		name string
		exp  int
		want int
	}{
		{name: "negative exp", exp: -10, want: 0},
		{name: "zero exp", exp: 0, want: 0},
		{name: "exp 1", exp: 1, want: 0},
		{name: "exp 3 boundary", exp: 3, want: 0},
		{name: "exp 4 first level", exp: 4, want: 1},
		{name: "exp 7 boundary level 1", exp: 7, want: 1},
		{name: "exp 8 level 2", exp: 8, want: 2},
		{name: "exp 12 boundary level 2", exp: 12, want: 2},
		{name: "exp 13 level 3", exp: 13, want: 3},
		{name: "exp 59 level 10", exp: 59, want: 10},
		{name: "exp 483 level 40", exp: 483, want: 40},
		{name: "exp 100000", exp: 100000, want: LevelFromExp(100000)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := LevelFromExp(tc.exp)
			if got != tc.want {
				t.Errorf("LevelFromExp(%d) = %d, want %d", tc.exp, got, tc.want)
			}
		})
	}
}

func TestLevelMonotonicityAndBaseExp(t *testing.T) {
	// Verify that LevelBaseExp is strictly monotonically increasing for levels >= 1
	// and LevelFromExp(LevelBaseExp(l)) == l for levels 1 to 50.
	prevBase := 0
	for l := 1; l <= 50; l++ {
		base := LevelBaseExp(l)
		if base <= prevBase {
			t.Fatalf("LevelBaseExp(%d) = %d not strictly greater than previous %d", l, base, prevBase)
		}
		gotLevel := LevelFromExp(base)
		if gotLevel != l {
			t.Fatalf("LevelFromExp(LevelBaseExp(%d) = %d) = %d, want %d", l, base, gotLevel, l)
		}
		prevBase = base
	}
}

func TestNextLevelExpTable(t *testing.T) {
	tests := []struct {
		level int
		want  int
	}{
		{level: -5, want: 0},
		{level: -1, want: 0},
		{level: 0, want: LevelBaseExp(1)},
		{level: 1, want: LevelBaseExp(2)},
		{level: 5, want: LevelBaseExp(6)},
		{level: 10, want: LevelBaseExp(11)},
	}

	for _, tc := range tests {
		got := NextLevelExp(tc.level)
		if got != tc.want {
			t.Errorf("NextLevelExp(%d) = %d, want %d", tc.level, got, tc.want)
		}
	}
}

func TestBuildExpFeedbackComprehensive(t *testing.T) {
	t.Run("zero base zero gain", func(t *testing.T) {
		fb := BuildExpFeedback(0, 0)
		if fb.DiffExp != 0 {
			t.Errorf("expected DiffExp 0, got %d", fb.DiffExp)
		}
		if fb.PreviousLevel != 0 || fb.NewLevel != 0 || fb.LevelDiff != 0 {
			t.Errorf("unexpected level info in feedback: %+v", fb)
		}
	})

	t.Run("zero base positive gain no level up", func(t *testing.T) {
		fb := BuildExpFeedback(0, 2)
		if fb.DiffExp != 2 {
			t.Errorf("expected DiffExp 2, got %d", fb.DiffExp)
		}
		if fb.PreviousLevel != 0 || fb.NewLevel != 0 {
			t.Errorf("expected level 0, got prev=%d new=%d", fb.PreviousLevel, fb.NewLevel)
		}
	})

	t.Run("level up transition", func(t *testing.T) {
		// exp 3 is level 0, exp 4 is level 1
		fb := BuildExpFeedback(3, 4)
		if fb.DiffExp != 1 {
			t.Errorf("expected DiffExp 1, got %d", fb.DiffExp)
		}
		if fb.PreviousLevel != 0 || fb.NewLevel != 1 || fb.LevelDiff != 1 {
			t.Errorf("expected level 0->1 diff 1, got prev=%d new=%d diff=%d",
				fb.PreviousLevel, fb.NewLevel, fb.LevelDiff)
		}
	})

	t.Run("multi-level jump", func(t *testing.T) {
		fb := BuildExpFeedback(0, 100)
		lvl := LevelFromExp(100)
		if fb.PreviousLevel != 0 || fb.NewLevel != lvl || fb.LevelDiff != lvl {
			t.Errorf("expected level 0->%d diff %d, got prev=%d new=%d diff=%d",
				lvl, lvl, fb.PreviousLevel, fb.NewLevel, fb.LevelDiff)
		}
		if fb.NewPercentToNextLevel < 0 || fb.NewPercentToNextLevel > 100 {
			t.Errorf("NewPercentToNextLevel out of range: %d", fb.NewPercentToNextLevel)
		}
		if fb.RemainingPercentToNextLevel < 0 || fb.RemainingPercentToNextLevel > 100 {
			t.Errorf("RemainingPercentToNextLevel out of range: %d", fb.RemainingPercentToNextLevel)
		}
	})

	t.Run("percentage consistency", func(t *testing.T) {
		for exp := 4; exp <= 200; exp += 7 {
			fb := BuildExpFeedback(exp, exp+5)
			sum := fb.NewPercentToNextLevel + fb.RemainingPercentToNextLevel
			if sum < 99 || sum > 101 {
				t.Errorf("exp=%d: NewPercent (%d) + RemainingPercent (%d) = %d, expected ~100",
					exp, fb.NewPercentToNextLevel, fb.RemainingPercentToNextLevel, sum)
			}
		}
	})
}

func TestArcadeGameEditExpTable(t *testing.T) {
	tests := []struct {
		changed int
		want    int
	}{
		{changed: -1, want: 0},
		{changed: 0, want: 0},
		{changed: 1, want: 2},
		{changed: 2, want: 4},
		{changed: 3, want: 6},
		{changed: 4, want: 8},
		{changed: 5, want: 10},
		{changed: 6, want: 10},
		{changed: 100, want: 10},
	}

	for _, tc := range tests {
		got := ArcadeGameEditExp(tc.changed)
		if got != tc.want {
			t.Errorf("ArcadeGameEditExp(%d) = %d, want %d", tc.changed, got, tc.want)
		}
	}
}

func TestKSTDay(t *testing.T) {
	// 2026-09-21 14:59:59 UTC is 23:59:59 KST on 2026-09-21
	utcBeforeMidnightKST := time.Date(2026, 9, 21, 14, 59, 59, 0, time.UTC)
	if day := KSTDay(utcBeforeMidnightKST); day != "2026-09-21" {
		t.Errorf("expected 2026-09-21, got %s", day)
	}

	// 2026-09-21 15:00:00 UTC is 00:00:00 KST on 2026-09-22
	utcAfterMidnightKST := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)
	if day := KSTDay(utcAfterMidnightKST); day != "2026-09-22" {
		t.Errorf("expected 2026-09-22, got %s", day)
	}
}

func TestResolveStoredProfileCountriesExtended(t *testing.T) {
	tests := []struct {
		name        string
		countries   []string
		mode        string
		autoPrimary string
		wantList    []string
		wantPrimary string
	}{
		{
			name:        "off mode returns empty",
			countries:   []string{"KR", "JP"},
			mode:        "off",
			autoPrimary: "KR",
			wantList:    []string{},
			wantPrimary: "",
		},
		{
			name:        "manual mode with countries returns list and first item",
			countries:   []string{"US", "GB"},
			mode:        "manual",
			autoPrimary: "KR",
			wantList:    []string{"US", "GB"},
			wantPrimary: "US",
		},
		{
			name:        "manual mode with empty countries returns empty",
			countries:   []string{},
			mode:        "manual",
			autoPrimary: "KR",
			wantList:    []string{},
			wantPrimary: "",
		},
		{
			name:        "auto mode with valid autoPrimary",
			countries:   []string{"US", "GB"},
			mode:        "auto",
			autoPrimary: "JP",
			wantList:    []string{"JP"},
			wantPrimary: "JP",
		},
		{
			name:        "auto mode with invalid autoPrimary returns empty",
			countries:   []string{"US"},
			mode:        "auto",
			autoPrimary: "INVALID",
			wantList:    []string{},
			wantPrimary: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotList, gotPrimary := ResolveStoredProfileCountries(tc.countries, tc.mode, tc.autoPrimary)
			if len(gotList) != len(tc.wantList) {
				t.Fatalf("len(gotList) = %d, want %d", len(gotList), len(tc.wantList))
			}
			for i := range gotList {
				if gotList[i] != tc.wantList[i] {
					t.Errorf("gotList[%d] = %s, want %s", i, gotList[i], tc.wantList[i])
				}
			}
			if gotPrimary != tc.wantPrimary {
				t.Errorf("gotPrimary = %s, want %s", gotPrimary, tc.wantPrimary)
			}
		})
	}
}

func TestProfileCountriesFromJSON(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{name: "empty string", raw: "", want: []string{}},
		{name: "invalid json", raw: "{invalid}", want: []string{}},
		{name: "valid array", raw: `["KR", "JP"]`, want: []string{"KR", "JP"}},
		{name: "lowercase normalized to uppercase", raw: `["kr", "jp"]`, want: []string{"KR", "JP"}},
		{name: "duplicates filtered by NormalizeProfileCountries", raw: `["KR", "KR"]`, want: []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ProfileCountriesFromJSON(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("len(got) = %d, want %d", len(got), len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("got[%d] = %s, want %s", i, got[i], tc.want[i])
				}
			}
		})
	}
}
