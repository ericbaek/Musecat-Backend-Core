package geo

import (
	"context"
	"fmt"
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
