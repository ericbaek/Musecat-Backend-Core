package geo

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
)

func formatPoint(lon, lat float64) string { return fmt.Sprintf("[%g,%g]", lon, lat) }

type offlineRoundTripFunc func(*http.Request) (*http.Response, error)

func (f offlineRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestOfflineResolverUsesCountryTimezoneAndUniqueCityPolygons(t *testing.T) {
	polygon := func(properties map[string]interface{}, west, south, east, north float64) boundaryFeature {
		return boundaryFeature{
			Country:  propertyString(properties, "country"),
			Timezone: propertyString(properties, "timezone"),
			CityID:   propertyString(properties, "city_source_id"),
			Geometry: geoJSONGeometry{Type: "Polygon", Coordinates: []byte(`[[` +
				formatPoint(west, south) + `,` + formatPoint(east, south) + `,` + formatPoint(east, north) + `,` + formatPoint(west, north) + `,` + formatPoint(west, south) + `]]`)},
		}
	}
	resolver := &OfflineResolver{
		countries: []boundaryFeature{polygon(map[string]interface{}{"country": "AU"}, 150, -35, 152, -33)},
		timezones: []boundaryFeature{polygon(map[string]interface{}{"timezone": "Australia/Sydney"}, 150, -35, 152, -33)},
		cities:    []boundaryFeature{polygon(map[string]interface{}{"country": "AU", "city_source_id": "2147714"}, 151, -34, 152, -33)},
	}
	got, err := resolver.Resolve(-33.88, 151.2)
	if err != nil {
		t.Fatal(err)
	}
	if got.Country != "AU" || got.Timezone != "Australia/Sydney" || got.CitySourceID != "2147714" || got.CityStatus != "resolved" {
		t.Fatalf("unexpected result: %+v", got)
	}
	got, err = resolver.Resolve(-34.5, 151.2)
	if err != nil {
		t.Fatal(err)
	}
	if got.CityStatus != "unmapped" || got.CitySourceID != "" {
		t.Fatalf("outside city polygon was resolved: %+v", got)
	}
}

func TestDecodeBoundaryFileRejectsUnsupportedIdentity(t *testing.T) {
	var dst []boundaryFeature
	err := decodeBoundaryFile(strings.NewReader(`{"type":"FeatureCollection","features":[{"type":"Feature","properties":{},"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}]}`), &dst)
	if err == nil {
		t.Fatal("expected identity validation error")
	}
}

func TestLookupUsesOfflineResolverForCountryTimezoneAndCity(t *testing.T) {
	polygon := func(properties map[string]interface{}, west, south, east, north float64) boundaryFeature {
		return boundaryFeature{
			Country:  propertyString(properties, "country"),
			Timezone: propertyString(properties, "timezone"),
			CityID:   propertyString(properties, "city_source_id"),
			Geometry: geoJSONGeometry{Type: "Polygon", Coordinates: []byte(`[[` +
				formatPoint(west, south) + `,` + formatPoint(east, south) + `,` + formatPoint(east, north) + `,` + formatPoint(west, north) + `,` + formatPoint(west, south) + `]]`)},
		}
	}
	resolver := &OfflineResolver{
		countries: []boundaryFeature{polygon(map[string]interface{}{"country": "AU"}, 150, -35, 152, -33)},
		timezones: []boundaryFeature{polygon(map[string]interface{}{"timezone": "Australia/Sydney"}, 150, -35, 152, -33)},
		cities:    []boundaryFeature{polygon(map[string]interface{}{"country": "AU", "city_source_id": "2147714"}, 151, -34, 152, -33)},
	}
	SetOfflineResolver(resolver, true)
	t.Cleanup(func() { SetOfflineResolver(nil, false) })
	restore := SetHTTPClient(&http.Client{Transport: offlineRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("network must not be used")
	})})
	defer restore()
	result, err := LookupCountryAndTimezone(context.Background(), -33.88, 151.2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Country != "AU" || result.Timezone != "Australia/Sydney" || result.CitySourceID != "2147714" {
		t.Fatalf("unexpected offline lookup: %+v", result)
	}
	tz, err := LookupTimezone(context.Background(), -33.88, 151.2)
	if err != nil || tz != "Australia/Sydney" {
		t.Fatalf("unexpected offline timezone: %q %v", tz, err)
	}
}

func TestLookupFailsClosedWhenOfflineResolverIsRequired(t *testing.T) {
	SetOfflineResolver(nil, true)
	t.Cleanup(func() { SetOfflineResolver(nil, false) })
	result, err := LookupCountryAndTimezone(context.Background(), 37.5665, 126.978)
	if err == nil || result != (Result{}) {
		t.Fatalf("expected required offline lookup failure, got %+v %v", result, err)
	}
}

func TestLoadEmbeddedResolver(t *testing.T) {
	resolver, err := LoadEmbeddedResolver()
	if err != nil {
		t.Fatal(err)
	}
	result, err := resolver.Resolve(3.139, 101.6869)
	if err != nil {
		t.Fatal(err)
	}
	if result.Country != "MY" || result.Timezone != "Asia/Kuala_Lumpur" {
		t.Fatalf("unexpected embedded lookup: %+v", result)
	}
}

func TestEmbeddedResolverBoardwalkCoastline(t *testing.T) {
	resolver, err := LoadEmbeddedResolver()
	if err != nil {
		t.Fatal(err)
	}
	const lat, lon = 33.692758510849, -78.878906894204
	SetOfflineResolver(resolver, true)
	t.Cleanup(func() { SetOfflineResolver(nil, false) })
	restore := SetHTTPClient(&http.Client{Transport: offlineRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("coastline lookup must not use the network")
		return nil, fmt.Errorf("unexpected network request")
	})})
	defer restore()
	result, err := LookupCountryAndTimezone(context.Background(), lat, lon)
	if err != nil {
		t.Fatal(err)
	}
	if result.Country != "US" || result.Timezone != "America/New_York" {
		t.Fatalf("unexpected Boardwalk lookup: %+v", result)
	}
	tz, err := LookupTimezone(context.Background(), lat, lon)
	if err != nil || tz != "America/New_York" {
		t.Fatalf("unexpected Boardwalk timezone: %q %v", tz, err)
	}
}

func TestOfflineCountryCoastlineTolerance(t *testing.T) {
	box := func(country, timezone string, west, south, east, north float64) boundaryFeature {
		return boundaryFeature{Country: country, Timezone: timezone, Polygon: [][][]float64{{
			{west, south}, {east, south}, {east, north}, {west, north}, {west, south},
		}}}
	}
	land := box("US", "", 10, 20, 11, 21)
	zone := box("", "America/New_York", 9, 19, 12, 22)
	neighbor := box("CA", "", 11.006, 20, 12, 21)
	hole := land
	hole.Polygon = append([][][]float64{land.Polygon[0]}, [][]float64{
		{10.995, 20.4}, {10.999, 20.4}, {10.999, 20.6}, {10.995, 20.6}, {10.995, 20.4},
	})
	multi := land
	multi.MultiPolygon, multi.Polygon = [][][][]float64{land.Polygon}, nil
	for _, tc := range []struct {
		name      string
		countries []boundaryFeature
		timezones []boundaryFeature
		lat, lon  float64
		wantError bool
	}{
		{"near coast", []boundaryFeature{land}, []boundaryFeature{zone}, 20.5, 11.003, false},
		{"multipolygon coast", []boundaryFeature{multi}, []boundaryFeature{zone}, 20.5, 11.003, false},
		{"distant water", []boundaryFeature{land}, []boundaryFeature{zone}, 20.5, 11.02, true},
		{"ambiguous countries", []boundaryFeature{land, neighbor}, []boundaryFeature{zone}, 20.5, 11.003, true},
		{"exact country wins", []boundaryFeature{land, neighbor}, []boundaryFeature{zone}, 20.5, 10.999, false},
		{"overlapping countries rejected", []boundaryFeature{land, land}, []boundaryFeature{zone}, 20.5, 10.9, true},
		{"missing timezone", []boundaryFeature{land}, nil, 20.5, 11.003, true},
		{"timezone not snapped", []boundaryFeature{land}, []boundaryFeature{box("", "America/New_York", 10, 20, 11, 21)}, 20.5, 11.003, true},
		{"ambiguous timezone", []boundaryFeature{land}, []boundaryFeature{zone, zone}, 20.5, 11.003, true},
		{"polygon hole", []boundaryFeature{hole}, []boundaryFeature{zone}, 20.5, 10.997, true},
		{"nan", []boundaryFeature{land}, []boundaryFeature{zone}, math.NaN(), 11.003, true},
		{"infinity", []boundaryFeature{land}, []boundaryFeature{zone}, 20.5, math.Inf(1), true},
		{"out of range", []boundaryFeature{land}, []boundaryFeature{zone}, 20.5, 370.003, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &OfflineResolver{countries: tc.countries, timezones: tc.timezones}
			result, err := resolver.Resolve(tc.lat, tc.lon)
			if tc.wantError {
				if err == nil || result != (OfflineResult{}) {
					t.Fatalf("expected lookup failure, got %+v %v", result, err)
				}
				return
			}
			if err != nil || result.Country != "US" || result.Timezone != "America/New_York" {
				t.Fatalf("unexpected coastline result: %+v %v", result, err)
			}
		})
	}
	for _, tc := range []struct {
		name     string
		feature  boundaryFeature
		lat, lon float64
		near     bool
	}{
		{"across antimeridian", box("US", "", 179.99, 20, 180, 21), 20.5, -179.999, true},
		{"antimeridian does not cross Greenwich", box("US", "", 179.99, 20, -179.99, 21), 20.5, 0, false},
		{"inside tolerance", land, 20.5, 11.009, true},
		{"outside tolerance", land, 20.5, 11.010, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if distance := tc.feature.exteriorDistanceMeters(tc.lat, tc.lon); (distance <= 1000) != tc.near {
				t.Fatalf("unexpected boundary distance: %f", distance)
			}
		})
	}
}
