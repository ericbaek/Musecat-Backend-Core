package geo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type lookupRoundTripFunc func(*http.Request) (*http.Response, error)

func (f lookupRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stubLookupHTTPClient(tb testing.TB, fn lookupRoundTripFunc) {
	tb.Helper()

	restore := SetHTTPClient(&http.Client{
		Transport: fn,
		Timeout:   time.Second,
	})
	tb.Cleanup(restore)
}

func TestLookupCountryAndTimezone_FallsBackToOpenMeteo(t *testing.T) {
	stubLookupHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "api.bigdatacloud.net":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"countryCode":"US"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "timeapi.io":
			return nil, fmt.Errorf("connection reset by peer")
		case "api.open-meteo.com":
			if got := req.URL.Query().Get("timezone"); got != "auto" {
				return nil, fmt.Errorf("expected timezone=auto, got %q", got)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"timezone":"America/Los_Angeles"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
	})

	res, err := LookupCountryAndTimezone(context.Background(), 37.422, -122.084)
	if err != nil {
		t.Fatalf("LookupCountryAndTimezone returned error: %v", err)
	}
	if res.Country != "US" {
		t.Fatalf("expected country US, got %q", res.Country)
	}
	if res.Timezone != "America/Los_Angeles" {
		t.Fatalf("expected timezone America/Los_Angeles, got %q", res.Timezone)
	}
}

func TestLookupCountryAndTimezone_FallsBackToNominatimForCountry(t *testing.T) {
	stubLookupHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "api.bigdatacloud.net":
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader(`{"statusCode":400}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "nominatim.openstreetmap.org":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"address":{"country_code":"au"}}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "timeapi.io":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"timeZone":"Australia/Sydney"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
	})

	res, err := LookupCountryAndTimezone(context.Background(), -33.861945, 151.209202)
	if err != nil {
		t.Fatalf("LookupCountryAndTimezone returned error: %v", err)
	}
	if res.Country != "AU" {
		t.Fatalf("expected country AU, got %q", res.Country)
	}
	if res.Timezone != "Australia/Sydney" {
		t.Fatalf("expected timezone Australia/Sydney, got %q", res.Timezone)
	}
}

func TestLookupCountryAndTimezone_OverridesCountryByTimezone(t *testing.T) {
	cases := []struct {
		name     string
		timezone string
		want     string
	}{
		{
			name:     "hong kong",
			timezone: "Asia/Hong_Kong",
			want:     "HK",
		},
		{
			name:     "macau",
			timezone: "Asia/Macau",
			want:     "MO",
		},
		{
			name:     "macao alias",
			timezone: "Asia/Macao",
			want:     "MO",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			stubLookupHTTPClient(t, func(req *http.Request) (*http.Response, error) {
				switch req.URL.Host {
				case "api.bigdatacloud.net":
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"countryCode":"CN"}`)),
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Request:    req,
					}, nil
				case "timeapi.io":
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"timeZone":"` + tc.timezone + `"}`)),
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Request:    req,
					}, nil
				default:
					return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
				}
			})

			res, err := LookupCountryAndTimezone(context.Background(), 22.3, 114.2)
			if err != nil {
				t.Fatalf("LookupCountryAndTimezone returned error: %v", err)
			}
			if res.Country != tc.want {
				t.Fatalf("expected country %s, got %q", tc.want, res.Country)
			}
			if res.Timezone != tc.timezone {
				t.Fatalf("expected timezone %s, got %q", tc.timezone, res.Timezone)
			}
		})
	}
}

func TestLookupCountryAndTimezone_TimezoneProvidersAllFail(t *testing.T) {
	stubLookupHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "api.bigdatacloud.net":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"countryCode":"US"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "timeapi.io":
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader(`{"error":"upstream fail"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "api.open-meteo.com":
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader(`{"reason":"bad gateway"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
	})

	_, err := LookupCountryAndTimezone(context.Background(), 37.422, -122.084)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "timeapi.io failed") {
		t.Fatalf("expected timeapi failure in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "open-meteo failed") {
		t.Fatalf("expected open-meteo failure in error, got %v", err)
	}
}

func TestLookupCountryAndTimezone_CountryProvidersAllFail(t *testing.T) {
	stubLookupHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "api.bigdatacloud.net":
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader(`{"statusCode":400}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "nominatim.openstreetmap.org":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"error":"Unable to geocode"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
	})

	_, err := LookupCountryAndTimezone(context.Background(), 1, 2)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "bigdatacloud failed") {
		t.Fatalf("expected bigdatacloud failure in error, got %v", err)
	}
	if !strings.Contains(err.Error(), "nominatim failed") {
		t.Fatalf("expected nominatim failure in error, got %v", err)
	}
}

func TestLookupCountryAndTimezone_CachesOnlySuccessfulValidatedResults(t *testing.T) {
	requests := 0
	stubLookupHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		switch req.URL.Host {
		case "api.bigdatacloud.net":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"countryCode":"KR"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "timeapi.io":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"timeZone":"Asia/Seoul"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
	})

	first, err := LookupCountryAndTimezone(context.Background(), 37.56651, 126.97801)
	if err != nil {
		t.Fatalf("first lookup failed: %v", err)
	}
	second, err := LookupCountryAndTimezone(context.Background(), 37.56654, 126.97804)
	if err != nil {
		t.Fatalf("cached lookup failed: %v", err)
	}
	if first != second || first.Country != "KR" || first.Timezone != "Asia/Seoul" {
		t.Fatalf("unexpected cached result: first=%#v second=%#v", first, second)
	}
	if requests != 2 {
		t.Fatalf("expected one successful provider pair, got %d requests", requests)
	}
}

func TestLookupCountryAndTimezone_DoesNotCacheInvalidResult(t *testing.T) {
	requests := 0
	stubLookupHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		requests++
		switch req.URL.Host {
		case "api.bigdatacloud.net":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"countryCode":"KR"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "timeapi.io":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"timeZone":"Asia/Busan"}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
	})

	for i := 0; i < 2; i++ {
		if _, err := LookupCountryAndTimezone(context.Background(), 35.1796, 129.0756); err == nil {
			t.Fatalf("lookup %d unexpectedly accepted invalid timezone", i+1)
		}
	}
	if requests != 4 {
		t.Fatalf("expected failed result to make a fresh provider pair each time, got %d requests", requests)
	}
}
