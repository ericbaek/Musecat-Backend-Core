package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/ericbaek/musecat-backend-core/geo"
	"github.com/ericbaek/musecat-backend-core/handlers"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

func TestHelloRoute(t *testing.T) {
	scenario := tests.ApiScenario{
		Name:           "GET /hello returns greeting",
		Method:         http.MethodGet,
		URL:            "/hello",
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			"hello world!",
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			app := testutil.NewTestApp(tb)
			app.OnServe().BindFunc(func(se *core.ServeEvent) error {
				se.Router.GET("/hello", handlers.HelloHandler)
				return se.Next()
			})

			return app
		},
	}

	scenario.Test(t)
}

func TestDocumentationRoutes(t *testing.T) {
	t.Run("GET /openapi.yaml serves spec", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /openapi.yaml returns OpenAPI document",
			Method:         http.MethodGet,
			URL:            "/openapi.yaml",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`openapi: 3.1.0`,
				`title: Delta-DB API`,
				`/search:`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				app := testutil.NewTestApp(tb)
				app.OnServe().BindFunc(func(se *core.ServeEvent) error {
					registerDocumentationRoutes(se)
					return se.Next()
				})
				return app
			},
		}

		scenario.Test(t)
	})

	t.Run("GET /openapi.yaml uses configured spec path", func(t *testing.T) {
		specPath := t.TempDir() + "/openapi.yaml"
		if err := os.WriteFile(specPath, []byte("openapi: 3.1.0\ninfo:\n  title: Local spec\n"), 0o600); err != nil {
			t.Fatalf("failed to write OpenAPI fixture: %v", err)
		}
		setTestEnv(t, "MUSECAT_OPENAPI_SPEC_PATH", specPath)

		scenario := tests.ApiScenario{
			Name:           "GET /openapi.yaml returns configured OpenAPI document",
			Method:         http.MethodGet,
			URL:            "/openapi.yaml",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				"title: Local spec",
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				app := testutil.NewTestApp(tb)
				app.OnServe().BindFunc(func(se *core.ServeEvent) error {
					registerDocumentationRoutes(se)
					return se.Next()
				})
				return app
			},
		}

		scenario.Test(t)
	})

	t.Run("GET /docs redirects to trailing slash", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /docs redirects",
			Method:         http.MethodGet,
			URL:            "/docs",
			ExpectedStatus: http.StatusMovedPermanently,
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				app := testutil.NewTestApp(tb)
				app.OnServe().BindFunc(func(se *core.ServeEvent) error {
					registerDocumentationRoutes(se)
					return se.Next()
				})
				return app
			},
		}
		scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
			tb.Helper()

			if got := res.Header.Get("Location"); got != "/docs/" {
				tb.Fatalf("expected redirect location /docs/, got %q", got)
			}
		}

		scenario.Test(t)
	})

	t.Run("GET /docs/ serves Stoplight page", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /docs/ returns docs UI",
			Method:         http.MethodGet,
			URL:            "/docs/",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`Delta-DB API Reference`,
				`elements-api`,
				`/openapi.yaml`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				app := testutil.NewTestApp(tb)
				app.OnServe().BindFunc(func(se *core.ServeEvent) error {
					registerDocumentationRoutes(se)
					return se.Next()
				})
				return app
			},
		}

		scenario.Test(t)
	})

	t.Run("docs basic auth protects docs and spec when enabled", func(t *testing.T) {
		setTestEnv(t, "DOCS_BASIC_AUTH_USER", "docs-user")
		setTestEnv(t, "DOCS_BASIC_AUTH_PASS", "docs-pass")

		protectedApp := func(tb testing.TB) *tests.TestApp {
			app := testutil.NewTestApp(tb)
			app.OnServe().BindFunc(func(se *core.ServeEvent) error {
				registerDocumentationRoutes(se)
				return se.Next()
			})
			return app
		}

		t.Run("GET /docs/ without credentials returns unauthorized", func(t *testing.T) {
			scenario := tests.ApiScenario{
				Name:           "GET /docs/ unauthorized when docs auth enabled",
				Method:         http.MethodGet,
				URL:            "/docs/",
				ExpectedStatus: http.StatusUnauthorized,
				ExpectedContent: []string{
					`Unauthorized`,
				},
				TestAppFactory: protectedApp,
			}
			scenario.AfterTestFunc = func(tb testing.TB, _ *tests.TestApp, res *http.Response) {
				tb.Helper()

				if got := res.Header.Get("WWW-Authenticate"); !strings.Contains(got, "Basic") {
					tb.Fatalf("expected WWW-Authenticate Basic header, got %q", got)
				}
			}

			scenario.Test(t)
		})

		t.Run("GET /openapi.yaml with credentials returns spec", func(t *testing.T) {
			credentials := base64.StdEncoding.EncodeToString([]byte("docs-user:docs-pass"))
			scenario := tests.ApiScenario{
				Name:           "GET /openapi.yaml authorized when docs auth enabled",
				Method:         http.MethodGet,
				URL:            "/openapi.yaml",
				ExpectedStatus: http.StatusOK,
				Headers: map[string]string{
					"Authorization": "Basic " + credentials,
				},
				ExpectedContent: []string{
					`openapi: 3.1.0`,
				},
				TestAppFactory: protectedApp,
			}

			scenario.Test(t)
		})
	})
}

type geocodeRoundTripFunc func(*http.Request) (*http.Response, error)

func (f geocodeRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func setTestEnv(tb testing.TB, key, value string) {
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

func stubGeocodeClient(tb testing.TB, fn geocodeRoundTripFunc) {
	tb.Helper()

	restore := geo.SetHTTPClient(&http.Client{
		Transport: fn,
		Timeout:   time.Second,
	})
	tb.Cleanup(restore)
}

func newGeoTestApp(tb testing.TB) *tests.TestApp {
	app := testutil.NewTestApp(tb)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/geo", handlers.GeoLookupHandler)
		se.Router.GET("/geocode", handlers.GeocodeHandler)
		se.Router.GET("/reverse_geocode", handlers.ReverseGeocodeHandler)
		return se.Next()
	})
	return app
}

func TestGeocodeRoute(t *testing.T) {
	t.Run("missing query returns bad request", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /geocode missing query",
			Method:         http.MethodGet,
			URL:            "/geocode",
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"error":"missing query"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.Test(t)
	})

	t.Run("default provider uses google", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /geocode defaults to google",
			Method:         http.MethodGet,
			URL:            "/geocode?query=1600+Amphitheatre+Parkway",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"provider":"google"`,
				`"query":"1600 Amphitheatre Parkway"`,
				`"place_id":"google-place-1"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			setTestEnv(tb, "GOOGLE_MAPS_API_KEY", "google-test-key")
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "maps.googleapis.com" {
					return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
				}
				body := `{"status":"OK","results":[{"formatted_address":"1600 Amphitheatre Pkwy, Mountain View, CA 94043, USA","place_id":"google-place-1","geometry":{"location":{"lat":37.422,"lng":-122.084}}}]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Request:    req,
				}, nil
			})
		}
		scenario.Test(t)
	})

	t.Run("region kr uses kakao", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /geocode region kr uses kakao",
			Method:         http.MethodGet,
			URL:            "/geocode?query=%EC%84%9C%EC%9A%B8&region=kr",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"provider":"kakao"`,
				`"road_address":"서울 강남구 테헤란로 152"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			setTestEnv(tb, "KAKAO_REST_API_KEY", "kakao-test-key")
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "dapi.kakao.com" {
					return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
				}
				body := `{"documents":[{"address_name":"서울 강남구 역삼동 737","x":"127.03637","y":"37.50098","road_address":{"address_name":"서울 강남구 테헤란로 152"}}]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Request:    req,
				}, nil
			})
		}
		scenario.Test(t)
	})

	t.Run("google upstream non-200 returns bad gateway", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /geocode google upstream error",
			Method:         http.MethodGet,
			URL:            "/geocode?query=Sydney",
			ExpectedStatus: http.StatusBadGateway,
			ExpectedContent: []string{
				`google geocoding http 500`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			setTestEnv(tb, "GOOGLE_MAPS_API_KEY", "google-test-key")
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Request:    req,
				}, nil
			})
		}
		scenario.Test(t)
	})

	t.Run("kakao upstream non-200 returns bad gateway", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /geocode kakao upstream error",
			Method:         http.MethodGet,
			URL:            "/geocode?query=%EC%84%9C%EC%9A%B8&region=kr",
			ExpectedStatus: http.StatusBadGateway,
			ExpectedContent: []string{
				`kakao geocoding http 503`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			setTestEnv(tb, "KAKAO_REST_API_KEY", "kakao-test-key")
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusServiceUnavailable,
					Body:       io.NopCloser(strings.NewReader(`{"error":"down"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Request:    req,
				}, nil
			})
		}
		scenario.Test(t)
	})

	t.Run("empty results returns ok with empty list", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /geocode empty results",
			Method:         http.MethodGet,
			URL:            "/geocode?query=Nowhere",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"provider":"google"`,
				`"results":[]`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			setTestEnv(tb, "GOOGLE_MAPS_API_KEY", "google-test-key")
			setTestEnv(tb, "KAKAO_REST_API_KEY", "kakao-test-key")
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
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
						Body:       io.NopCloser(strings.NewReader(`{"documents":[]}`)),
						Header:     http.Header{"Content-Type": []string{"application/json"}},
						Request:    req,
					}, nil
				default:
					return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
				}
			})
		}
		scenario.Test(t)
	})

	t.Run("region kr falls back to google when kakao has no results", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /geocode region kr falls back to google",
			Method:         http.MethodGet,
			URL:            "/geocode?query=%EC%84%9C%EC%9A%B8&region=kr",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"provider":"google"`,
				`"place_id":"google-place-1"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			setTestEnv(tb, "GOOGLE_MAPS_API_KEY", "google-test-key")
			setTestEnv(tb, "KAKAO_REST_API_KEY", "kakao-test-key")
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
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
		}
		scenario.Test(t)
	})

	t.Run("mode free uses osm outside kr", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /geocode mode free uses osm",
			Method:         http.MethodGet,
			URL:            "/geocode?query=Sydney&mode=free",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"provider":"osm"`,
				`"address":"Sydney NSW, Australia"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "nominatim.openstreetmap.org" {
					return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`[{"place_id":12345,"lat":"-33.868820","lon":"151.209296","display_name":"Sydney NSW, Australia","name":"Sydney"}]`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Request:    req,
				}, nil
			})
		}
		scenario.Test(t)
	})
}

func TestReverseGeocodeRoute(t *testing.T) {
	t.Run("missing lat lon returns bad request", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /reverse_geocode missing lat lon",
			Method:         http.MethodGet,
			URL:            "/reverse_geocode",
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"error":"invalid or missing lat/lon"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.Test(t)
	})

	t.Run("default provider uses google", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /reverse_geocode defaults to google",
			Method:         http.MethodGet,
			URL:            "/reverse_geocode?lat=37.422&lon=-122.084",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"provider":"google"`,
				`"display_name":"1600 Amphitheatre Pkwy, Mountain View, CA 94043, USA"`,
				`"road_address":"1600 Amphitheatre Pkwy, Mountain View, CA 94043, USA"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			setTestEnv(tb, "GOOGLE_MAPS_API_KEY", "google-test-key")
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "maps.googleapis.com" {
					return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
				}
				body := `{"status":"OK","results":[{"formatted_address":"1600 Amphitheatre Pkwy, Mountain View, CA 94043, USA","place_id":"google-reverse-place-1"}]}`
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Request:    req,
				}, nil
			})
		}
		scenario.Test(t)
	})

	t.Run("region kr uses kakao", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /reverse_geocode region kr uses kakao",
			Method:         http.MethodGet,
			URL:            "/reverse_geocode?lat=37.50098&lon=127.03637&region=kr",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"provider":"kakao"`,
				`"address":"서울 강남구 역삼동 737"`,
				`"road_address":"서울 강남구 테헤란로 152"`,
				`"display_name":"서울 강남구 테헤란로 152"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			setTestEnv(tb, "KAKAO_REST_API_KEY", "kakao-test-key")
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
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
		}
		scenario.Test(t)
	})

	t.Run("google upstream non-200 returns bad gateway", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /reverse_geocode google upstream error",
			Method:         http.MethodGet,
			URL:            "/reverse_geocode?lat=37.422&lon=-122.084",
			ExpectedStatus: http.StatusBadGateway,
			ExpectedContent: []string{
				`google reverse geocoding http 500`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			setTestEnv(tb, "GOOGLE_MAPS_API_KEY", "google-test-key")
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusInternalServerError,
					Body:       io.NopCloser(strings.NewReader(`{"error":"boom"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Request:    req,
				}, nil
			})
		}
		scenario.Test(t)
	})

	t.Run("invalid coordinate range returns bad request", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /reverse_geocode invalid coordinate range",
			Method:         http.MethodGet,
			URL:            "/reverse_geocode?lat=120&lon=127",
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"error":"invalid coordinates"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.Test(t)
	})

	t.Run("default provider falls back to kakao when google has no results", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /reverse_geocode falls back to kakao",
			Method:         http.MethodGet,
			URL:            "/reverse_geocode?lat=37.50098&lon=127.03637",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"provider":"kakao"`,
				`"display_name":"서울 강남구 테헤란로 152"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			setTestEnv(tb, "GOOGLE_MAPS_API_KEY", "google-test-key")
			setTestEnv(tb, "KAKAO_REST_API_KEY", "kakao-test-key")
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
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
		}
		scenario.Test(t)
	})

	t.Run("mode free uses osm outside kr", func(t *testing.T) {
		scenario := tests.ApiScenario{
			Name:           "GET /reverse_geocode mode free uses osm",
			Method:         http.MethodGet,
			URL:            "/reverse_geocode?lat=-33.86882&lon=151.209296&mode=free",
			ExpectedStatus: http.StatusOK,
			ExpectedContent: []string{
				`"provider":"osm"`,
				`"display_name":"Sydney NSW, Australia"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newGeoTestApp(tb)
			},
		}
		scenario.BeforeTestFunc = func(tb testing.TB, _ *tests.TestApp, _ *core.ServeEvent) {
			stubGeocodeClient(tb, func(req *http.Request) (*http.Response, error) {
				if req.URL.Host != "nominatim.openstreetmap.org" {
					return nil, fmt.Errorf("unexpected host: %s", req.URL.Host)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"place_id":12345,"lat":"-33.868820","lon":"151.209296","display_name":"Sydney NSW, Australia","name":"Sydney"}`)),
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Request:    req,
				}, nil
			})
		}
		scenario.Test(t)
	})
}
