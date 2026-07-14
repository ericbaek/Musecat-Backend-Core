package search

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
)

func TestNormalizeAddressQuery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "metropolitan to short",
			in:   "대구광역시 중구",
			want: "대구중구",
		},
		{
			name: "province to short",
			in:   "충청북도 청주시",
			want: "충북청주시",
		},
		{
			name: "already short remains same",
			in:   "경기 수원시",
			want: "경기수원시",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := normalizeAddressQuery(tc.in)
			if got != tc.want {
				t.Fatalf("normalizeAddressQuery(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestBuildNormalizedAddressSQLExpr(t *testing.T) {
	t.Parallel()

	expr := buildNormalizedAddressSQLExpr("lower(trim(COALESCE(b.address, '')))")
	if !strings.Contains(expr, "replace(") {
		t.Fatalf("expected replace chain, got %q", expr)
	}
	if !strings.Contains(expr, "'대구광역시', '대구'") {
		t.Fatalf("expected 대구 alias replacement in %q", expr)
	}
	if !strings.Contains(expr, "'경기도', '경기'") {
		t.Fatalf("expected 경기 alias replacement in %q", expr)
	}
}

func TestParseSearchLocation(t *testing.T) {
	t.Parallel()

	t.Run("missing both", func(t *testing.T) {
		t.Parallel()

		lat, lon, ok, err := parseSearchLocation(url.Values{})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if ok {
			t.Fatalf("expected location to be optional")
		}
		if lat != 0 || lon != 0 {
			t.Fatalf("expected zero values for missing location, got %f %f", lat, lon)
		}
	})

	t.Run("requires pair", func(t *testing.T) {
		t.Parallel()

		_, _, _, err := parseSearchLocation(url.Values{"lat": {"37.5"}})
		if err == nil {
			t.Fatalf("expected error for partial location")
		}
	})

	t.Run("parses valid location", func(t *testing.T) {
		t.Parallel()

		lat, lon, ok, err := parseSearchLocation(url.Values{"lat": {"37.5"}, "lon": {"127.0"}})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if !ok {
			t.Fatalf("expected location to be enabled")
		}
		if lat != 37.5 || lon != 127.0 {
			t.Fatalf("unexpected location: %f %f", lat, lon)
		}
	})
}

func TestDebugArcadeSearchSQL(t *testing.T) {
	q := "timezone eastgarden"
	params := buildSearchParams(q)
	normalizedAddressQuery := normalizeAddressQuery(q)
	textTokens := splitSearchTokens(q)
	params["address_exact"] = normalizedAddressQuery
	params["address_prefix"] = escapeLike(normalizedAddressQuery) + "%"
	params["address_contains"] = "%" + escapeLike(normalizedAddressQuery) + "%"
	params["limit"] = 10

	addressExpr := buildNormalizedAddressSQLExpr("lower(trim(COALESCE(b.address, '')))")
	tokenClause := ""
	if len(textTokens) > 0 {
		tokenParts := make([]string, 0, len(textTokens))
		for i, token := range textTokens {
			key := fmt.Sprintf("term_%d", i)
			params[key] = "%" + escapeLike(token) + "%"
			tokenParts = append(tokenParts, `(
		lower(trim(COALESCE(b.name, ''))) LIKE {:`+key+`} ESCAPE '\'
		OR lower(trim(COALESCE(b.address, ''))) LIKE {:`+key+`} ESCAPE '\'
		OR lower(COALESCE(b.nickname, '')) LIKE {:`+key+`} ESCAPE '\'
	)`)
		}
		tokenClause = " OR (" + strings.Join(tokenParts, " AND ") + ")"
	}

	sql := fmt.Sprintf(`
SELECT
	a.id
FROM arcade a
INNER JOIN arcade_basic b ON b.id = a.basic
WHERE
	a.public = 1
	AND (
		lower(trim(COALESCE(b.name, ''))) LIKE {:contains} ESCAPE '\'
		OR lower(trim(COALESCE(b.address, ''))) LIKE {:contains} ESCAPE '\'
		OR %s LIKE {:address_contains} ESCAPE '\'
		OR lower(COALESCE(b.nickname, '')) LIKE {:contains} ESCAPE '\'
		%s
	)
ORDER BY
	CASE
		WHEN lower(trim(COALESCE(b.name, ''))) = {:exact}
			OR lower(trim(COALESCE(b.address, ''))) = {:exact}
			OR %s = {:address_exact}
		THEN 0
		WHEN lower(trim(COALESCE(b.name, ''))) LIKE {:prefix} ESCAPE '\'
			OR lower(trim(COALESCE(b.address, ''))) LIKE {:prefix} ESCAPE '\'
			OR %s LIKE {:address_prefix} ESCAPE '\'
		THEN 1
		ELSE 2
	END ASC,
	lower(trim(COALESCE(b.name, ''))) ASC,
	a.id ASC
LIMIT {:limit}
`, addressExpr, addressExpr, addressExpr, tokenClause)

	t.Log(sql)
}
