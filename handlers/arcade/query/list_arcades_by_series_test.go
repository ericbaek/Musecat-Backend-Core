package query

import "testing"

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
