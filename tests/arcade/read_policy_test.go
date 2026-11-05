package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/readpolicy"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestPublicReadPolicyCoversActualNearbyPageAndDetail(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	id, _ := seedArcade(t, app, user.Id, arcadeSeed{Name: "Policy Arcade", Address: "Address", Location: location{Lat: 37.56, Lon: 126.97}})
	setArcadeVisibility(t, app, id, true, false)
	series := seedNearbyGameSeries(t, app, "Policy Series")
	version := seedNearbyGameSeriesVersion(t, app, series, "Version")
	game := seedNearbyGameMolecule(t, app, id)
	seedNearbyGameAtom(t, app, game, version)
	calls := 0
	readpolicy.Register(app, func(re *core.RequestEvent, ids []string) error {
		calls++
		if len(ids) != 1 || ids[0] != id {
			t.Fatalf("wrong charged IDs: %v", ids)
		}
		return apis.NewTooManyRequestsError("test restriction", nil)
	})
	for _, path := range []string{"/arcade?id=" + id, "/arcades/nearby?lat=37.56&lon=126.97&expand=true", "/arcade/changelog?arcade=" + id} {
		res := executeJSONRequest(t, app, http.MethodGet, path, "", map[string]string{"Authorization": "Bearer " + token})
		res.Body.Close()
		if res.StatusCode != 429 {
			t.Fatalf("%s returned %d", path, res.StatusCode)
		}
	}
	if calls != 3 {
		t.Fatal(calls)
	}
	// Non-expanded and guest nearby reads contain no game details and do not charge.
	for _, path := range []string{"/arcades/nearby?lat=37.56&lon=126.97", "/arcades/nearby?lat=37.56&lon=126.97&expand=true"} {
		res := executeJSONRequest(t, app, http.MethodGet, path, "", nil)
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode)
		}
		var body struct {
			Items []map[string]any `json:"items"`
		}
		if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Items) != 1 {
			t.Fatal(body)
		}
		for _, item := range body.Items {
			for _, key := range []string{"game_series", "game"} {
				if _, ok := item[key]; ok {
					t.Fatalf("guest exposed %s", key)
				}
			}
		}
	}
	if calls != 3 {
		t.Fatal("guest nearby charged", calls)
	}
}
