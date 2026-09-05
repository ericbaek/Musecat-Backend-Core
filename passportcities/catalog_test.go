package passportcities

import (
	"strings"
	"testing"
)

func TestCityMatchRejectsAmbiguityAndDistricts(t *testing.T) {
	cities := []City{{SourceID: "1", Name: "Seoul", Country: "KR", Admin1: "11", Aliases: []string{"서울"}}, {SourceID: "2", Name: "Seoul", Country: "US", Admin1: "11"}}
	if got := Match(cities, "KR", "11", "서울"); len(got) != 1 || got[0].SourceID != "1" {
		t.Fatalf("alias: %+v", got)
	}
	if got := Match(cities, "KR", "", "서울"); len(got) != 0 {
		t.Fatal("missing admin matched")
	}
	cities = append(cities, City{SourceID: "3", Name: "서울", Country: "KR", Admin1: "11"})
	if len(Match(cities, "KR", "11", "서울")) != 2 {
		t.Fatal("ambiguous matches hidden")
	}
	count := 0
	row := "1\tGangnam\tGangnam\t강남\t37\t127\tP\tPPLX\tKR\t\t11\t\t\t\t1\t\t\tAsia/Seoul\t2026-01-01\n"
	if err := ReadGeoNames(strings.NewReader(row), func(City) error { count++; return nil }); err != nil || count != 0 {
		t.Fatalf("district imported: %d %v", count, err)
	}
}
