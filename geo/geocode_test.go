package geo

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stubGeocodeHTTPClient(tb testing.TB, fn roundTripFunc) {
	tb.Helper()

	restore := SetHTTPClient(&http.Client{
		Transport: fn,
		Timeout:   time.Second,
	})
	tb.Cleanup(restore)
}

func setEnv(tb testing.TB, key, value string) {
	tb.Helper()

	prev, existed := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		tb.Fatalf("failed to set env %s: %v", key, err)
	}
	tb.Cleanup(func() {
		var err error
		if existed {
			err = os.Setenv(key, prev)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil {
			tb.Fatalf("failed to restore env %s: %v", key, err)
		}
	})
}

func TestForwardGeocode_GoogleMapping(t *testing.T) {
	setEnv(t, "GOOGLE_MAPS_API_KEY", "google-test-key")
	setEnv(t, "KAKAO_REST_API_KEY", "")

	stubGeocodeHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "maps.googleapis.com" {
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
		if got := req.URL.Query().Get("address"); got != "1600 Amphitheatre Parkway" {
			return nil, fmt.Errorf("unexpected address query: %s", got)
		}
		if got := req.URL.Query().Get("key"); got != "google-test-key" {
			return nil, fmt.Errorf("unexpected key: %s", got)
		}

		body := `{
			"status":"OK",
			"results":[
				{
					"formatted_address":"1600 Amphitheatre Pkwy, Mountain View, CA 94043, USA",
					"place_id":"google-place-1",
					"geometry":{"location":{"lat":37.422,"lng":-122.084}}
				},
				{
					"formatted_address":"1600 Amphitheatre Pkwy, Mountain View, CA, USA",
					"place_id":"google-place-2",
					"geometry":{"location":{"lat":37.4219,"lng":-122.0841}}
				}
			]
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    req,
		}, nil
	})

	res, err := ForwardGeocode(context.Background(), "1600 Amphitheatre Parkway", "", "")
	if err != nil {
		t.Fatalf("ForwardGeocode returned error: %v", err)
	}
	if res.Provider != ProviderGoogle {
		t.Fatalf("expected provider %q, got %q", ProviderGoogle, res.Provider)
	}
	if len(res.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res.Results))
	}
	if res.Results[0].Address != "1600 Amphitheatre Pkwy, Mountain View, CA 94043, USA" {
		t.Fatalf("unexpected address: %q", res.Results[0].Address)
	}
	if res.Results[0].Lat != 37.422 || res.Results[0].Lon != -122.084 {
		t.Fatalf("unexpected coordinates: %+v", res.Results[0])
	}
	if res.Results[0].PlaceID != "google-place-1" {
		t.Fatalf("unexpected place id: %q", res.Results[0].PlaceID)
	}
}

func TestForwardGeocode_KakaoMapping(t *testing.T) {
	setEnv(t, "GOOGLE_MAPS_API_KEY", "")
	setEnv(t, "KAKAO_REST_API_KEY", "kakao-test-key")

	stubGeocodeHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "dapi.kakao.com" {
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
		if got := req.Header.Get("Authorization"); got != "KakaoAK kakao-test-key" {
			return nil, fmt.Errorf("unexpected authorization: %s", got)
		}
		if got := req.URL.Query().Get("query"); got != "서울특별시 강남구 테헤란로 152" {
			return nil, fmt.Errorf("unexpected query: %s", got)
		}

		body := `{
			"documents":[
				{
					"address_name":"서울 강남구 역삼동 737",
					"x":"127.03637",
					"y":"37.50098",
					"address":{"address_name":"서울 강남구 역삼동 737"},
					"road_address":{"address_name":"서울 강남구 테헤란로 152"}
				}
			]
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    req,
		}, nil
	})

	res, err := ForwardGeocode(context.Background(), "서울특별시 강남구 테헤란로 152", "kr", "")
	if err != nil {
		t.Fatalf("ForwardGeocode returned error: %v", err)
	}
	if res.Provider != ProviderKakao {
		t.Fatalf("expected provider %q, got %q", ProviderKakao, res.Provider)
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(res.Results))
	}
	if res.Results[0].Address != "서울 강남구 역삼동 737" {
		t.Fatalf("unexpected address: %q", res.Results[0].Address)
	}
	if res.Results[0].RoadAddress != "서울 강남구 테헤란로 152" {
		t.Fatalf("unexpected road_address: %q", res.Results[0].RoadAddress)
	}
	if res.Results[0].Lat != 37.50098 || res.Results[0].Lon != 127.03637 {
		t.Fatalf("unexpected coordinates: %+v", res.Results[0])
	}
}

func TestForwardGeocode_MissingAPIKey(t *testing.T) {
	setEnv(t, "GOOGLE_MAPS_API_KEY", "")
	setEnv(t, "KAKAO_REST_API_KEY", "")

	if _, err := ForwardGeocode(context.Background(), "Sydney", "", ""); err == nil || !strings.Contains(err.Error(), "GOOGLE_MAPS_API_KEY") {
		t.Fatalf("expected google api key error, got %v", err)
	}
	if _, err := ForwardGeocode(context.Background(), "서울", "kr", ""); err == nil || !strings.Contains(err.Error(), "KAKAO_REST_API_KEY") {
		t.Fatalf("expected kakao api key error, got %v", err)
	}
}

func TestForwardGeocode_FallsBackWhenPreferredProviderHasNoResults(t *testing.T) {
	setEnv(t, "GOOGLE_MAPS_API_KEY", "google-test-key")
	setEnv(t, "KAKAO_REST_API_KEY", "kakao-test-key")

	stubGeocodeHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "dapi.kakao.com":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"documents":[]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "maps.googleapis.com":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":"OK","results":[{"formatted_address":"1600 Amphitheatre Pkwy, Mountain View, CA 94043, USA","place_id":"google-place-1","geometry":{"location":{"lat":37.422,"lng":-122.084}}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
	})

	res, err := ForwardGeocode(context.Background(), "서울특별시 강남구 테헤란로 152", "kr", "")
	if err != nil {
		t.Fatalf("ForwardGeocode returned error: %v", err)
	}
	if res.Provider != ProviderGoogle {
		t.Fatalf("expected fallback provider %q, got %q", ProviderGoogle, res.Provider)
	}
	if len(res.Results) != 1 {
		t.Fatalf("expected 1 fallback result, got %d", len(res.Results))
	}
}

func TestReverseGeocode_GoogleMapping(t *testing.T) {
	setEnv(t, "GOOGLE_MAPS_API_KEY", "google-test-key")
	setEnv(t, "KAKAO_REST_API_KEY", "")

	stubGeocodeHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "maps.googleapis.com" {
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
		if got := req.URL.Query().Get("latlng"); got != "37.422000,-122.084000" {
			return nil, fmt.Errorf("unexpected latlng query: %s", got)
		}

		body := `{
			"status":"OK",
			"results":[
				{
					"formatted_address":"1600 Amphitheatre Pkwy, Mountain View, CA 94043, USA",
					"place_id":"google-reverse-place-1"
				}
			]
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    req,
		}, nil
	})

	res, err := ReverseGeocode(context.Background(), 37.422, -122.084, "", "")
	if err != nil {
		t.Fatalf("ReverseGeocode returned error: %v", err)
	}
	if res.Provider != ProviderGoogle {
		t.Fatalf("expected provider %q, got %q", ProviderGoogle, res.Provider)
	}
	if res.DisplayName != "1600 Amphitheatre Pkwy, Mountain View, CA 94043, USA" {
		t.Fatalf("unexpected display name: %q", res.DisplayName)
	}
	if res.RoadAddress != res.DisplayName {
		t.Fatalf("expected road address to match display name, got %q", res.RoadAddress)
	}
	if res.PlaceID != "google-reverse-place-1" {
		t.Fatalf("unexpected place id: %q", res.PlaceID)
	}
}

func TestReverseGeocode_KakaoMapping(t *testing.T) {
	setEnv(t, "GOOGLE_MAPS_API_KEY", "")
	setEnv(t, "KAKAO_REST_API_KEY", "kakao-test-key")

	stubGeocodeHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "dapi.kakao.com" {
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
		if got := req.URL.Query().Get("x"); got != "127.036370" {
			return nil, fmt.Errorf("unexpected x: %s", got)
		}
		if got := req.URL.Query().Get("y"); got != "37.500980" {
			return nil, fmt.Errorf("unexpected y: %s", got)
		}

		body := `{
			"documents":[
				{
					"address":{"address_name":"서울 강남구 역삼동 737"},
					"road_address":{"address_name":"서울 강남구 테헤란로 152"}
				}
			]
		}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    req,
		}, nil
	})

	res, err := ReverseGeocode(context.Background(), 37.50098, 127.03637, "kr", "")
	if err != nil {
		t.Fatalf("ReverseGeocode returned error: %v", err)
	}
	if res.Provider != ProviderKakao {
		t.Fatalf("expected provider %q, got %q", ProviderKakao, res.Provider)
	}
	if res.Address != "서울 강남구 역삼동 737" {
		t.Fatalf("unexpected address: %q", res.Address)
	}
	if res.RoadAddress != "서울 강남구 테헤란로 152" {
		t.Fatalf("unexpected road_address: %q", res.RoadAddress)
	}
	if res.DisplayName != "서울 강남구 테헤란로 152" {
		t.Fatalf("unexpected display_name: %q", res.DisplayName)
	}
}

func TestReverseGeocode_MissingAPIKey(t *testing.T) {
	setEnv(t, "GOOGLE_MAPS_API_KEY", "")
	setEnv(t, "KAKAO_REST_API_KEY", "")

	if _, err := ReverseGeocode(context.Background(), 37.5, 127.0, "", ""); err == nil || !strings.Contains(err.Error(), "GOOGLE_MAPS_API_KEY") {
		t.Fatalf("expected google api key error, got %v", err)
	}
	if _, err := ReverseGeocode(context.Background(), 37.5, 127.0, "kr", ""); err == nil || !strings.Contains(err.Error(), "KAKAO_REST_API_KEY") {
		t.Fatalf("expected kakao api key error, got %v", err)
	}
}

func TestReverseGeocode_FallsBackWhenPreferredProviderHasNoResults(t *testing.T) {
	setEnv(t, "GOOGLE_MAPS_API_KEY", "google-test-key")
	setEnv(t, "KAKAO_REST_API_KEY", "kakao-test-key")

	stubGeocodeHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "maps.googleapis.com":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":"ZERO_RESULTS","results":[]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "dapi.kakao.com":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"documents":[{"address":{"address_name":"서울 강남구 역삼동 737"},"road_address":{"address_name":"서울 강남구 테헤란로 152"}}]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
	})

	res, err := ReverseGeocode(context.Background(), 37.50098, 127.03637, "", "")
	if err != nil {
		t.Fatalf("ReverseGeocode returned error: %v", err)
	}
	if res.Provider != ProviderKakao {
		t.Fatalf("expected fallback provider %q, got %q", ProviderKakao, res.Provider)
	}
	if res.DisplayName != "서울 강남구 테헤란로 152" {
		t.Fatalf("unexpected fallback display name: %q", res.DisplayName)
	}
}

func TestForwardGeocode_FreeModeUsesOSMOutsideKR(t *testing.T) {
	setEnv(t, "GOOGLE_MAPS_API_KEY", "google-test-key")
	setEnv(t, "KAKAO_REST_API_KEY", "kakao-test-key")

	stubGeocodeHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "nominatim.openstreetmap.org" {
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
		body := `[{"place_id":12345,"lat":"-33.868820","lon":"151.209296","display_name":"Sydney NSW, Australia","name":"Sydney"}]`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    req,
		}, nil
	})

	res, err := ForwardGeocode(context.Background(), "Sydney", "", "free")
	if err != nil {
		t.Fatalf("ForwardGeocode returned error: %v", err)
	}
	if res.Provider != ProviderOSM {
		t.Fatalf("expected provider %q, got %q", ProviderOSM, res.Provider)
	}
	if len(res.Results) != 1 || res.Results[0].Address != "Sydney NSW, Australia" {
		t.Fatalf("unexpected osm result: %+v", res.Results)
	}
}

func TestForwardGeocode_FreeModeKRFallsBackToOSM(t *testing.T) {
	setEnv(t, "KAKAO_REST_API_KEY", "kakao-test-key")

	stubGeocodeHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		switch req.URL.Host {
		case "dapi.kakao.com":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"documents":[]}`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		case "nominatim.openstreetmap.org":
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`[{"place_id":54321,"lat":"37.566500","lon":"126.978000","display_name":"Seoul, South Korea","name":"Seoul"}]`)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
	})

	res, err := ForwardGeocode(context.Background(), "서울", "kr", "free")
	if err != nil {
		t.Fatalf("ForwardGeocode returned error: %v", err)
	}
	if res.Provider != ProviderOSM {
		t.Fatalf("expected fallback provider %q, got %q", ProviderOSM, res.Provider)
	}
}

func TestReverseGeocode_FreeModeUsesOSMOutsideKR(t *testing.T) {
	stubGeocodeHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "nominatim.openstreetmap.org" {
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
		body := `{"place_id":12345,"lat":"-33.868820","lon":"151.209296","display_name":"Sydney NSW, Australia","name":"Sydney"}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    req,
		}, nil
	})

	res, err := ReverseGeocode(context.Background(), -33.86882, 151.209296, "", "free")
	if err != nil {
		t.Fatalf("ReverseGeocode returned error: %v", err)
	}
	if res.Provider != ProviderOSM {
		t.Fatalf("expected provider %q, got %q", ProviderOSM, res.Provider)
	}
	if res.DisplayName != "Sydney NSW, Australia" {
		t.Fatalf("unexpected display name: %q", res.DisplayName)
	}
}

func TestReverseGeocode_FreeModeKROffersKakaoFirst(t *testing.T) {
	setEnv(t, "KAKAO_REST_API_KEY", "kakao-test-key")

	stubGeocodeHTTPClient(t, func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "dapi.kakao.com" {
			return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
		}
		body := `{"documents":[{"address":{"address_name":"서울 강남구 역삼동 737"},"road_address":{"address_name":"서울 강남구 테헤란로 152"}}]}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request:    req,
		}, nil
	})

	res, err := ReverseGeocode(context.Background(), 37.50098, 127.03637, "kr", "free")
	if err != nil {
		t.Fatalf("ReverseGeocode returned error: %v", err)
	}
	if res.Provider != ProviderKakao {
		t.Fatalf("expected provider %q, got %q", ProviderKakao, res.Provider)
	}
}
