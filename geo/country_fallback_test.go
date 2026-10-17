package geo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestEmbeddedMissingCountryCodesUseExternalCountryAndCache(t *testing.T) {
	resolver, err := LoadEmbeddedResolver()
	if err != nil {
		t.Fatal(err)
	}
	SetOfflineResolver(resolver, true)
	t.Cleanup(func() { SetOfflineResolver(nil, false) })
	for _, tc := range []struct {
		name          string
		lat, lon      float64
		country, zone string
	}{
		{"Geek Room France", 48.910027800911, 2.284050484981, "FR", "Europe/Paris"},
		{"Norway", 59.9139, 10.7522, "NO", "Europe/Oslo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			stubLookupHTTPClient(t, func(req *http.Request) (*http.Response, error) {
				calls++
				if req.URL.Host != "api.bigdatacloud.net" {
					t.Fatalf("unexpected provider %s", req.URL.Host)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"countryCode":%q}`, tc.country)))}, nil
			})
			for i := 0; i < 2; i++ {
				got, err := LookupCountryAndTimezone(context.Background(), tc.lat, tc.lon)
				if err != nil || got.Country != tc.country || got.Timezone != tc.zone {
					t.Fatalf("got %+v, %v", got, err)
				}
			}
			if calls != 1 {
				t.Fatalf("expected one cached country request, got %d", calls)
			}
		})
	}
}

func TestInvalidCountryProviderResultUsesSecondaryOrFailsWithoutCaching(t *testing.T) {
	resolver, err := LoadEmbeddedResolver()
	if err != nil {
		t.Fatal(err)
	}
	SetOfflineResolver(resolver, true)
	t.Cleanup(func() { SetOfflineResolver(nil, false) })
	for _, success := range []bool{true, false} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			calls := 0
			stubLookupHTTPClient(t, func(req *http.Request) (*http.Response, error) {
				calls++
				body := `{"countryCode":"-99"}`
				if req.URL.Host == "nominatim.openstreetmap.org" {
					body = `{"address":{"country_code":"-99"}}`
					if success {
						body = `{"address":{"country_code":"fr"}}`
					}
				} else if req.URL.Host != "api.bigdatacloud.net" {
					t.Fatalf("unexpected timezone request: %s", req.URL.Host)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})
			for i := 0; i < 2; i++ {
				got, err := LookupCountryAndTimezone(context.Background(), 48.910027800911, 2.284050484981)
				if success && (err != nil || got.Country != "FR" || got.Timezone != "Europe/Paris") {
					t.Fatalf("got %+v, %v", got, err)
				}
				if !success && (err == nil || got != (Result{})) {
					t.Fatalf("invalid country leaked: %+v, %v", got, err)
				}
			}
			wantCalls := 2
			if !success {
				wantCalls = 4
			}
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d", calls, wantCalls)
			}
		})
	}
}

func TestCountryMissFallsBackButAmbiguousBoundariesDoNot(t *testing.T) {
	polygon := [][][]float64{{{0, 40}, {10, 40}, {10, 55}, {0, 55}, {0, 40}}}
	for _, ambiguous := range []bool{false, true} {
		t.Run(fmt.Sprint(ambiguous), func(t *testing.T) {
			resolver := &OfflineResolver{timezones: []boundaryFeature{{Timezone: "Europe/Paris", Polygon: polygon}}}
			if ambiguous {
				resolver.countries = []boundaryFeature{{Country: "FR", Polygon: polygon}, {Country: "BE", Polygon: polygon}}
			}
			SetOfflineResolver(resolver, true)
			t.Cleanup(func() { SetOfflineResolver(nil, false) })
			calls := 0
			stubLookupHTTPClient(t, func(req *http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"countryCode":"FR"}`))}, nil
			})
			got, err := LookupCountryAndTimezone(context.Background(), 48.91, 2.28)
			if ambiguous {
				if err == nil || calls != 0 {
					t.Fatalf("ambiguous boundary must fail without HTTP: %+v %v calls=%d", got, err, calls)
				}
			} else if err != nil || got.Country != "FR" || got.Timezone != "Europe/Paris" || calls != 1 {
				t.Fatalf("unexpected country fallback: %+v %v calls=%d", got, err, calls)
			}
		})
	}
}
