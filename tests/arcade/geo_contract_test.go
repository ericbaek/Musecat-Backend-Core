package arcade_test

import (
	"fmt"
	"net/http"
	"testing"
)

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
