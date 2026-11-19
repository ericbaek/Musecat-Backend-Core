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
		{"Seoul outer gu joins Seoul", "KR", 37.551, 126.849, "KR-11", "KR-11/서울특별시", AreaResolved},
		{"Seoul north gu joins Seoul", "KR", 37.654, 127.056, "KR-11", "KR-11/서울특별시", AreaResolved},
		{"Suwon general gu joins Suwon", "KR", 37.304, 127.010, "KR-41", "KR-41/수원시", AreaResolved},
		{"Yongin exists without GeoNames", "KR", 37.280, 127.115, "KR-41", "KR-41/용인시", AreaResolved},
		{"Gwangju gu inside the merged province", "KR", 35.146, 126.923, "KR-12", "KR-12/광주", AreaResolved},
		{"Yokohama ward joins Yokohama", "JP", 35.444, 139.642, "JP-14", "JP-14/横浜市", AreaResolved},
		{"Tokyo special wards are one city", "JP", 35.690, 139.703, "JP-13", "JP-13/東京都区部", AreaResolved},
		{"Hachioji stands alone", "JP", 35.666, 139.316, "JP-13", "JP-13/八王子市", AreaResolved},
		{"Macau follows the stored country", "MO", 22.198, 113.543, "MO", "MO/澳門", AreaResolved},
		{"Hong Kong is one city", "HK", 22.319, 114.169, "HK", "HK/香港", AreaResolved},
		{"Shanghai municipality is one city", "CN", 31.23, 121.47, "CN-SH", "CN-SH/Shanghai", AreaResolved},
		{"Taipei", "TW", 25.042, 121.507, "TW-TPE", "TW-TPE/Taipei City", AreaResolved},
		{"Wangcheng, an old county unit, rolls up to Changsha", "CN", 28.34334, 112.818431, "CN-HN", "CN-HN/Changsha", AreaResolved},
		{"Bangkok districts are one city", "TH", 13.75, 100.50, "TH-10", "TH-10/Bangkok Metropolis", AreaResolved},
		{"Greater London is one city", "GB", 51.51, -0.12, "", "GB/London", AreaResolved},
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
	seoul, _ := a.City("KR-11/서울특별시")
	if seoul.Name("ko") != "서울" || seoul.Name("en") != "Seoul" || seoul.Name("ja") != "ソウル" {
		t.Errorf("Seoul names: %v", seoul.Names)
	}
	yongin, _ := a.City("KR-41/용인시")
	if yongin.Name("en") != "용인시" || yongin.Name("ko") != "용인시" {
		t.Errorf("unlinked city falls back to its local name: %v", yongin.Names)
	}
	for key, city := range a.cities {
		if strings.TrimSpace(city.Name("en")) == "" || city.Country == "" {
			t.Fatalf("city %s has no name or country: %+v", key, city)
		}
	}
}

func TestAreasVersionAndAdmin1(t *testing.T) {
	a, err := EmbeddedAreas()
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Version()) != 16 {
		t.Errorf("version %q", a.Version())
	}
	if tokyo, ok := a.Admin1("JP-13"); !ok || tokyo.Name("ja") != "東京都" || tokyo.Name("en") != "Tokyo" {
		t.Errorf("JP-13: %+v", tokyo)
	}
	if hk, ok := a.Admin1("HK"); !ok || hk.Name("en") != "Hong Kong" {
		t.Errorf("HK admin-1 follows the city: %+v", hk)
	}
	if key, ok := a.CurrentCityKey("KR-11/서울특별시"); !ok || key != "KR-11/서울특별시" {
		t.Errorf("current key: %q %v", key, ok)
	}
	if _, ok := a.CurrentCityKey("KR:11"); ok {
		t.Error("retired key resolved")
	}
}

func TestAddressMismatch(t *testing.T) {
	a, err := EmbeddedAreas()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		country, admin1, address string
		mismatch                 bool
	}{
		{"JP", "JP-13", "東京都新宿区歌舞伎町1-1", false},
		{"JP", "JP-26", "東京都新宿区歌舞伎町1-1", true}, // 京都 inside 東京都 is not Kyoto
		{"JP", "JP-26", "京都府京都市中京区", false},
		{"KR", "KR-41", "경기 수원시 팔달구", false},
		{"KR", "KR-11", "경기도 성남시 분당구", true},
		{"KR", "KR-12", "광주광역시 동구 충장로", false},
		{"US", "US-CA", "123 Main St, Los Angeles, California 90012", false},
		{"US", "US-NV", "123 Main St, Los Angeles, California 90012", true},
		{"US", "US-CA", "1 Californiaway", false}, // no whole-word alias
		{"KR", "KR-11", "", false},                // no address
		{"KR", "KR-11", "강남구 테헤란로 1", false},      // names no admin-1
	}
	for _, tc := range cases {
		if got := a.AddressMismatch(tc.country, tc.admin1, tc.address); got != tc.mismatch {
			t.Errorf("%s %s %q: mismatch=%v, named %v", tc.country, tc.admin1, tc.address, got, a.AddressAdmin1(tc.country, tc.address))
		}
	}
}

func BenchmarkClassify(b *testing.B) {
	a, err := EmbeddedAreas()
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Classify("JP", 35.0+float64(i%100)/100, 139.0+float64(i%97)/100)
	}
}
