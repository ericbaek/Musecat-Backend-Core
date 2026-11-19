package geo

import (
	"strings"
	"testing"
)

func TestEmbeddedAreasClassify(t *testing.T) {
	a, err := EmbeddedAreas()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, country string
		lat, lon      float64
		admin1, city  string
		status        string
	}{
		{"Seoul outer gu joins Seoul", "KR", 37.551, 126.849, "KR-11", "KR:11", AreaResolved},
		{"Seoul north gu joins Seoul", "KR", 37.654, 127.056, "KR-11", "KR:11", AreaResolved},
		{"Suwon general gu joins Suwon", "KR", 37.304, 127.010, "KR-41", "KR:41:수원시", AreaResolved},
		{"Yongin exists without GeoNames", "KR", 37.280, 127.115, "KR-41", "KR:41:용인시", AreaResolved},
		{"Gwangju gu inside the merged province", "KR", 35.146, 126.923, "KR-12", "KR:12:광주", AreaResolved},
		{"Yokohama ward joins Yokohama", "JP", 35.444, 139.642, "JP-14", "JP:14:横浜市", AreaResolved},
		{"Tokyo special wards are one city", "JP", 35.690, 139.703, "JP-13", "JP:13:tokyo-23", AreaResolved},
		{"Hachioji stands alone", "JP", 35.666, 139.316, "JP-13", "JP:13201", AreaResolved},
		{"Macau follows the stored country", "MO", 22.198, 113.543, "MO", "MO:all", AreaResolved},
		{"Hong Kong is one city", "HK", 22.319, 114.169, "HK", "HK:all", AreaResolved},
		{"Shanghai municipality is one city", "CN", 31.23, 121.47, "CN-SH", "CN:CN-SH", AreaResolved},
		{"Taipei", "TW", 25.042, 121.507, "TW-TPE", "TW:TW-TPE", AreaResolved},
		{"Bangkok districts are one city", "TH", 13.75, 100.50, "TH-10", "TH:TH-10", AreaResolved},
		{"Greater London is one city", "GB", 51.51, -0.12, "", "GB:region:London", AreaResolved},
	}
	for _, tc := range cases {
		got := a.Classify(tc.country, tc.lat, tc.lon)
		if got.Status != tc.status || got.City != tc.city || (tc.admin1 != "" && got.Admin1 != tc.admin1) {
			t.Errorf("%s: got %+v, want admin1=%s city=%s status=%s", tc.name, got, tc.admin1, tc.city, tc.status)
		}
		if _, ok := a.City(got.City); !ok {
			t.Errorf("%s: city %q missing from catalog", tc.name, got.City)
		}
	}
}

func TestEmbeddedAreasEdges(t *testing.T) {
	a, err := EmbeddedAreas()
	if err != nil {
		t.Fatal(err)
	}
	// Reclaimed island off the Jakarta coastline (Batavia PIK) is ~1.2km out.
	if got := a.Classify("ID", -6.0888, 106.7399); got.Status != AreaNear || got.Admin1 != "ID-JK" {
		t.Errorf("reclaimed coast: %+v", got)
	}
	// Open sea far from any unit stays unmapped for review.
	if got := a.Classify("US", 42.2692, -70.4498); got.Status != AreaUnmapped || got.City != "" {
		t.Errorf("sea: %+v", got)
	}
	// Countries without city boundaries take the nearest seat, estimated.
	got := a.Classify("FR", 48.86, 2.35)
	city, ok := a.City(got.City)
	if got.Status != AreaEstimated || got.Admin1 != "FR-75" || !ok || !city.Estimated || city.Name("en") != "Paris" {
		t.Errorf("Paris: %+v %+v", got, city)
	}
	// A coordinate in the wrong country never borrows a neighbour's unit.
	if got := a.Classify("KR", 35.69, 139.70); got.Status != AreaUnmapped {
		t.Errorf("foreign point: %+v", got)
	}
	if got := a.Classify("", 37.5, 127); got.Status != AreaUnmapped {
		t.Errorf("no country: %+v", got)
	}
}

func TestAreaCityNames(t *testing.T) {
	a, err := EmbeddedAreas()
	if err != nil {
		t.Fatal(err)
	}
	seoul, _ := a.City("KR:11")
	if seoul.Name("ko") != "서울" || seoul.Name("en") != "Seoul" || seoul.Name("ja") != "ソウル" {
		t.Errorf("Seoul names: %v", seoul.Names)
	}
	yongin, _ := a.City("KR:41:용인시")
	if yongin.Name("en") != "용인시" || yongin.Name("ko") != "용인시" {
		t.Errorf("unlinked city falls back to its local name: %v", yongin.Names)
	}
	for key, city := range a.cities {
		if strings.TrimSpace(city.Name("en")) == "" || city.Country == "" {
			t.Fatalf("city %s has no name or country: %+v", key, city)
		}
	}
}
