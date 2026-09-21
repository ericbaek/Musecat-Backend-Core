package xp

import (
	"testing"
	"time"
)

func TestServiceLevelFromExp(t *testing.T) {
	cases := []struct {
		exp  int
		want int
	}{
		{-100, 0},
		{0, 0},
		{1, 0},
		{3, 0},
		{4, 1},
		{7, 1},
		{8, 2},
		{12, 2},
		{13, 3},
		{59, 10},
		{483, 40},
	}

	for _, tc := range cases {
		got := LevelFromExp(tc.exp)
		if got != tc.want {
			t.Errorf("LevelFromExp(%d) = %d, want %d", tc.exp, got, tc.want)
		}
	}
}

func TestServiceLevelBaseExpMonotonicity(t *testing.T) {
	for l := 1; l <= 50; l++ {
		base := LevelBaseExp(l)
		if base <= LevelBaseExp(l-1) {
			t.Fatalf("LevelBaseExp(%d)=%d <= LevelBaseExp(%d)=%d", l, base, l-1, LevelBaseExp(l-1))
		}
		computedLevel := LevelFromExp(base)
		if computedLevel != l {
			t.Fatalf("LevelFromExp(LevelBaseExp(%d)) = %d, want %d", l, computedLevel, l)
		}
	}
}

func TestServiceBuildExpFeedback(t *testing.T) {
	fb := BuildExpFeedback(3, 4)
	if fb.DiffExp != 1 {
		t.Errorf("expected DiffExp=1, got %d", fb.DiffExp)
	}
	if fb.PreviousLevel != 0 || fb.NewLevel != 1 || fb.LevelDiff != 1 {
		t.Errorf("unexpected level info: %+v", fb)
	}
	if fb.NewPercentToNextLevel < 0 || fb.NewPercentToNextLevel > 100 {
		t.Errorf("unexpected NewPercentToNextLevel: %d", fb.NewPercentToNextLevel)
	}
	if fb.RemainingPercentToNextLevel < 0 || fb.RemainingPercentToNextLevel > 100 {
		t.Errorf("unexpected RemainingPercentToNextLevel: %d", fb.RemainingPercentToNextLevel)
	}
}

func TestServiceArcadeGameEditExp(t *testing.T) {
	if got := ArcadeGameEditExp(0); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
	if got := ArcadeGameEditExp(2); got != 4 {
		t.Errorf("expected 4, got %d", got)
	}
	if got := ArcadeGameEditExp(5); got != 10 {
		t.Errorf("expected 10, got %d", got)
	}
	if got := ArcadeGameEditExp(25); got != 10 {
		t.Errorf("expected cap 10, got %d", got)
	}
}

func TestServiceKinds(t *testing.T) {
	if got := FlagKind("flag123"); got != "xp:flag:flag123" {
		t.Errorf("FlagKind = %s", got)
	}
	if got := FlagReactionKind("rxn456"); got != "xp:flag-reaction:rxn456" {
		t.Errorf("FlagReactionKind = %s", got)
	}
	if got := ArcadePublicKind("arc789"); got != "xp:arcade-public:arc789" {
		t.Errorf("ArcadePublicKind = %s", got)
	}
	if got := ArcadeEditKind("arc1", "basic"); got != "xp:arcade-edit:basic:arc1" {
		t.Errorf("ArcadeEditKind = %s", got)
	}
	if got := ArcadePublicBackfillKind("arc1", "basic"); got != "xp:arcade-edit:basic:arc1" {
		t.Errorf("ArcadePublicBackfillKind = %s", got)
	}
	if got := ArcadePhotoSubmissionKind("arc1"); got != "xp:arcade-photo-submission:arc1" {
		t.Errorf("ArcadePhotoSubmissionKind = %s", got)
	}
	if got := ArcadePhotoGrantKind("arc1", "atom1"); got != "xp:arcade-photo:arc1:atom1" {
		t.Errorf("ArcadePhotoGrantKind = %s", got)
	}
	if got := ArcadeVisitKind("visit1"); got != "xp:arcade-visit:visit1" {
		t.Errorf("ArcadeVisitKind = %s", got)
	}
	if got := CheckInKind("2026-09-21"); got != "xp:attendance:service:2026-09-21" {
		t.Errorf("CheckInKind = %s", got)
	}
}

func TestServiceKSTDay(t *testing.T) {
	// 2026-09-21 14:59 UTC -> 2026-09-21 23:59 KST
	t1 := time.Date(2026, 9, 21, 14, 59, 0, 0, time.UTC)
	if got := KSTDay(t1); got != "2026-09-21" {
		t.Errorf("KSTDay(%v) = %s, want 2026-09-21", t1, got)
	}

	// 2026-09-21 15:00 UTC -> 2026-09-22 00:00 KST
	t2 := time.Date(2026, 9, 21, 15, 0, 0, 0, time.UTC)
	if got := KSTDay(t2); got != "2026-09-22" {
		t.Errorf("KSTDay(%v) = %s, want 2026-09-22", t2, got)
	}
}
