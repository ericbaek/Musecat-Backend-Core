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

func TestCatalogGroupsOnlyUnambiguousSections(t *testing.T) {
	row := func(id, name, code, country, admin string) string {
		return strings.Join([]string{id, name, name, "", "-33", "151", "P", code, country, "", admin, "", "", "", "1000", "", "", "UTC", "2026-01-01"}, "\t") + "\n"
	}
	places := row("1", "Sydney", "PPLA", "AU", "02") + row("2", "Haymarket", "PPLX", "AU", "02") + row("3", "OtherCity", "PPL", "AU", "02") + row("4", "Ambiguous", "PPLX", "AU", "02") + row("5", "Orphan", "PPLX", "AU", "02") + row("6", "MissingAdmin", "PPL", "AU", "") + row("7", "Petaling Jaya", "PPLA2", "MY", "12") + row("8", "Kuala Lumpur", "PPLC", "MY", "14")
	var cities []City
	err := ReadCatalog(strings.NewReader(places), strings.NewReader("1\t2\t\n1\t4\t\n3\t4\t\n"), func(c City) error { cities = append(cities, c); return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(cities) != 4 {
		t.Fatalf("cities: %+v", cities)
	}
	if got := Match(cities, "AU", "02", "Haymarket"); len(got) != 1 || got[0].Name != "Sydney" {
		t.Fatalf("suburb: %+v", got)
	}
	for _, name := range []string{"Ambiguous", "Orphan"} {
		if got := Match(cities, "AU", "02", name); len(got) != 0 {
			t.Fatalf("unresolved section matched: %+v", got)
		}
	}
	if got := Match(cities, "MY", "12", "Petaling Jaya"); len(got) != 1 || got[0].Name != "Petaling Jaya" {
		t.Fatalf("separate city: %+v", got)
	}
}

func TestAddressCandidatesRequireNamesAndRemainAmbiguous(t *testing.T) {
	cities := []City{{SourceID: "1", Name: "Sydney", Country: "AU", Admin1: "02", Aliases: []string{"Haymarket"}, Lat: -33.87, Lon: 151.21}, {SourceID: "2", Name: "Sydney", Country: "CA", Admin1: "07", Lat: 46.13, Lon: -60.18}, {SourceID: "3", Name: "Burwood", Country: "AU", Admin1: "07", Lat: -37.85, Lon: 145.1}}
	got := AddressCandidates(cities, "AU", "Shop 9/13 Hay St, Haymarket NSW 2000", -33.879, 151.204)
	if len(got) != 1 || got[0].SourceID != "1" {
		t.Fatalf("section alias: %+v", got)
	}
	for _, address := range []string{"Unknown street NSW", "Sydneyville NSW", "Burwood NSW"} {
		if got := AddressCandidates(cities, "AU", address, -33.879, 151.204); len(got) != 0 {
			t.Fatalf("false candidate for %q: %+v", address, got)
		}
	}
	cities = append(cities, City{SourceID: "4", Name: "Sydney", Country: "AU", Admin1: "02", Lat: -33.88, Lon: 151.2})
	if got := AddressCandidates(cities, "AU", "Sydney NSW", -33.879, 151.204); len(got) != 2 {
		t.Fatalf("ambiguity discarded: %+v", got)
	}
}

func TestNearestPrefersClosestCityAndKeepsCountryBoundary(t *testing.T) {
	cities := []City{
		{SourceID: "2", Name: "Far", Country: "AU", Lat: -34, Lon: 151},
		{SourceID: "1", Name: "Near", Country: "AU", Lat: -33.88, Lon: 151.2},
		{SourceID: "3", Name: "Other country", Country: "NZ", Lat: -33.88, Lon: 151.2},
	}
	got, ok := Nearest(cities, "AU", -33.879, 151.201)
	if !ok || got.SourceID != "1" {
		t.Fatalf("unexpected nearest city: %+v %v", got, ok)
	}
	if _, ok := Nearest(cities, "US", -33.879, 151.201); ok {
		t.Fatal("nearest city crossed country boundary")
	}
	if _, ok := Nearest(cities, "AU", 0, 0); ok {
		t.Fatal("invalid zero coordinate selected a city")
	}
}
