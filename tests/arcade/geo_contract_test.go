package arcade_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/geo"
)

func TestContractV2_FranceCountryFallbackCreatesValidArcadeOrPersistsNothing(t *testing.T) {
	resolver, err := geo.LoadEmbeddedResolver()
	if err != nil {
		t.Fatal(err)
	}
	for _, success := range []bool{true, false} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			app := newArcadeTestApp(t)
			token, user := createAuthUser(t, app)
			geo.SetOfflineResolver(resolver, true)
			t.Cleanup(func() { geo.SetOfflineResolver(nil, false) })
			restore := geo.SetHTTPClient(&http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if !success {
					return nil, fmt.Errorf("country providers unavailable")
				}
				if req.URL.Host != "api.bigdatacloud.net" {
					t.Fatalf("unexpected HTTP provider %s", req.URL.Host)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"countryCode":"FR"}`))}, nil
			})})
			t.Cleanup(restore)
			res := executeJSONRequest(t, app, http.MethodPost, "/arcade/new", `{"name":"France Regression","address":"39 Rue de Nanterre","location":{"lat":48.910027800911,"lon":2.284050484981}}`, map[string]string{"Authorization": "Bearer " + token})
			if !success {
				assertContractStatus(t, res, http.StatusServiceUnavailable)
			} else {
				assertContractStatus(t, res, http.StatusOK)
				body := decodeJSONMap(t, res)
				record := mustFindRecord(t, app, "arcade", body["id"].(string))
				if record.GetString("country") != "FR" || record.GetString("timezone") != "Europe/Paris" {
					t.Fatalf("invalid stored geography: %v", record.PublicExport())
				}
			}
			records, err := app.FindRecordsByFilter("arcade", "createdBy={:id}", "", 0, 0, map[string]any{"id": user.Id})
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if success {
				want = 1
			}
			if len(records) != want {
				t.Fatalf("arcades=%d want=%d", len(records), want)
			}
		})
	}
}

func TestContractV2_NewArcadeGeoFailureDoesNotPersist(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	stubGeoLookupWithResolver(t, func(_ *http.Request) (string, string, error) {
		return "", "", fmt.Errorf("geo provider unavailable")
	})

	headers := map[string]string{"Authorization": "Bearer " + token}
	body := `{"name":"Geo Failure Arcade","address":"Unavailable Street","location":{"lat":37.5665,"lon":126.978}}`
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPost, "/arcade/new", body, headers), http.StatusServiceUnavailable)

	records, err := app.FindRecordsByFilter("arcade", "createdBy={:id}", "", 0, 0, map[string]any{"id": user.Id})
	if err != nil {
		t.Fatalf("failed to load owner arcades: %v", err)
	}
	if len(records) != 0 {
		t.Fatalf("geo failure created an arcade unexpectedly: %#v", records)
	}
}

func TestContractV2_LocationGeoFailureDoesNotPersist(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	arcadeID, basicID := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Location Failure Arcade",
		Address:  "Stable Street",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	stubGeoLookupWithResolver(t, func(_ *http.Request) (string, string, error) {
		return "", "", fmt.Errorf("geo provider unavailable")
	})

	headers := map[string]string{"Authorization": "Bearer " + token}
	body := fmt.Sprintf(`{"arcade":%q,"location":{"lat":35.1796,"lon":129.0756}}`, arcadeID)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/arcade/basic", body, headers), http.StatusServiceUnavailable)
	if got := mustFindRecord(t, app, "arcade", arcadeID).GetString("basic"); got != basicID {
		t.Fatalf("geo failure changed basic relation: got %q want %q", got, basicID)
	}
}
