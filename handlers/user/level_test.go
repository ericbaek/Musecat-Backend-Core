package user

import (
	"testing"
	"time"

	pbtypes "github.com/pocketbase/pocketbase/tools/types"
)

func TestParseLevelLogCreatedAcceptsDefaultDateLayout(t *testing.T) {
	ts := time.Date(2026, 7, 4, 12, 34, 56, 123000000, time.UTC)

	got, err := parseLevelLogCreated(ts.UTC().Format(pbtypes.DefaultDateLayout))
	if err != nil {
		t.Fatalf("expected DefaultDateLayout to parse, got error: %v", err)
	}
	if !got.Equal(ts.UTC()) {
		t.Fatalf("expected %v, got %v", ts.UTC(), got)
	}
}

func TestParseLevelLogCreatedAcceptsRFC3339Nano(t *testing.T) {
	ts := time.Date(2026, 7, 4, 12, 34, 56, 123456789, time.UTC)

	got, err := parseLevelLogCreated(ts.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("expected RFC3339Nano to parse, got error: %v", err)
	}
	if !got.Equal(ts) {
		t.Fatalf("expected %v, got %v", ts, got)
	}
}

func TestBuildExpFeedbackIncludesPreviousAndNewPercent(t *testing.T) {
	feedback := BuildExpFeedback(4, 5)

	if feedback.DiffExp != 1 {
		t.Fatalf("expected diff_exp 1, got %d", feedback.DiffExp)
	}
	if feedback.PreviousPercentToNextLevel != 0 {
		t.Fatalf("expected previous_percent_to_next_level 0, got %d", feedback.PreviousPercentToNextLevel)
	}
	if feedback.NewPercentToNextLevel != 25 {
		t.Fatalf("expected new_percent_to_next_level 25, got %d", feedback.NewPercentToNextLevel)
	}
}

func TestBuildExpFeedbackPreviousAndNewPercentZeroBase(t *testing.T) {
	feedback := BuildExpFeedback(0, 5)

	if feedback.PreviousPercentToNextLevel != 0 {
		t.Fatalf("expected previous_percent_to_next_level 0 for zero base, got %d", feedback.PreviousPercentToNextLevel)
	}
	if feedback.NewPercentToNextLevel != 25 {
		t.Fatalf("expected new_percent_to_next_level 25, got %d", feedback.NewPercentToNextLevel)
	}
}

func TestArcadeGameEditExpScalesByChangedEntryCount(t *testing.T) {
	cases := []struct {
		changed int
		want    int
	}{
		{changed: 0, want: 0},
		{changed: 1, want: 2},
		{changed: 2, want: 4},
		{changed: 3, want: 6},
		{changed: 4, want: 8},
		{changed: 5, want: 10},
		{changed: 20, want: 10},
	}

	for _, tc := range cases {
		if got := ArcadeGameEditExp(tc.changed); got != tc.want {
			t.Fatalf("ArcadeGameEditExp(%d) = %d, want %d", tc.changed, got, tc.want)
		}
	}
}
