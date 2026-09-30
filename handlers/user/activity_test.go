package user

import "testing"

func TestComputeActivityLevelFixedBuckets(t *testing.T) {
	tests := []struct {
		count int
		want  int
	}{
		{count: 0, want: 0},
		{count: 1, want: 1},
		{count: 2, want: 2},
		{count: 3, want: 2},
		{count: 4, want: 3},
		{count: 7, want: 3},
		{count: 8, want: 4},
		{count: 100, want: 4},
	}

	for _, tc := range tests {
		if got := computeActivityLevel(tc.count); got != tc.want {
			t.Errorf("computeActivityLevel(%d) = %d, want %d", tc.count, got, tc.want)
		}
	}
}
