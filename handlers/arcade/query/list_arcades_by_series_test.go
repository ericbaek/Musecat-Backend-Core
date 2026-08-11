package query

import (
	"reflect"
	"testing"
)

func TestAddressMatchesFilter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		address string
		filter  string
		want    bool
	}{
		{
			name:    "full region address matches short filter",
			address: "대구광역시 중구 중앙대로 123",
			filter:  "대구",
			want:    true,
		},
		{
			name:    "short address matches full region filter",
			address: "대구 중구 중앙대로 123",
			filter:  "대구광역시",
			want:    true,
		},
		{
			name:    "province full name matches shorthand filter",
			address: "경기도 수원시 영통구",
			filter:  "경기",
			want:    true,
		},
		{
			name:    "multi-token filter matches by token prefix",
			address: "대구광역시 중구 중앙대로 123",
			filter:  "대구 중",
			want:    true,
		},
		{
			name:    "different region does not match",
			address: "부산광역시 해운대구",
			filter:  "대구",
			want:    false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			filter := normalizeAddressKeyword(tc.filter)
			got := addressMatchesFilter(tc.address, filter)
			if got != tc.want {
				t.Fatalf("addressMatchesFilter(%q, %q) = %v, want %v", tc.address, tc.filter, got, tc.want)
			}
		})
	}
}

func TestParseOrderedIDs_DeduplicatesInFirstSeenOrder(t *testing.T) {
	t.Parallel()

	got := parseOrderedIDs([]string{" series_b, series_a ", "series_b", "series_c,series_a"})
	want := []string{"series_b", "series_a", "series_c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseOrderedIDs() = %#v, want %#v", got, want)
	}
}

func TestBuildNearbyGameFilters(t *testing.T) {
	t.Parallel()

	filters, err := buildNearbyGameFilters([]string{"series_a", "series_b"}, []string{"cabinet_a", "cabinet_b"})
	if err != nil {
		t.Fatalf("expected paired filters, got %v", err)
	}
	want := []nearbyGameFilter{
		{SeriesID: "series_a", CabinetID: "cabinet_a"},
		{SeriesID: "series_b", CabinetID: "cabinet_b"},
	}
	if !reflect.DeepEqual(filters, want) {
		t.Fatalf("buildNearbyGameFilters() = %#v, want %#v", filters, want)
	}

	if _, err := buildNearbyGameFilters(nil, []string{"cabinet_a"}); err == nil {
		t.Fatal("expected cabinet-only filters to fail")
	}
	if _, err := buildNearbyGameFilters([]string{"series_a", "series_b"}, []string{"cabinet_a"}); err == nil {
		t.Fatal("expected mixed paired and series-only filters to fail")
	}
}

func TestMatchesAllGameFilters_RequiresSameRevisionPairs(t *testing.T) {
	t.Parallel()

	installations := []ArcadeGameInstallation{
		{SeriesID: "series_a", CabinetID: "silver"},
		{SeriesID: "series_b", CabinetID: "gold"},
	}
	if matchesAllGameFilters(installations, []nearbyGameFilter{{SeriesID: "series_a", CabinetID: "gold"}}) {
		t.Fatal("expected cross-series cabinet match to be rejected")
	}
	if !matchesAllGameFilters(installations, []nearbyGameFilter{
		{SeriesID: "series_a", CabinetID: "silver"},
		{SeriesID: "series_b", CabinetID: "gold"},
	}) {
		t.Fatal("expected every same-revision pair to match")
	}
	if !matchesAllGameFilters([]ArcadeGameInstallation{{SeriesID: "series_a"}}, []nearbyGameFilter{{SeriesID: "series_a"}}) {
		t.Fatal("expected unknown cabinet to match a series-only filter")
	}
}
