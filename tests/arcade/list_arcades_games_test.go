package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/readpolicy"
	"github.com/pocketbase/pocketbase/core"
)

func listArcadeItems(t *testing.T, res *http.Response) []map[string]any {
	t.Helper()
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from /arcades, got %d", res.StatusCode)
	}
	var payload struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("decode /arcades: %v", err)
	}
	if len(payload.Items) == 0 {
		t.Fatal("expected at least one arcade")
	}
	return payload.Items
}

func assertArcadeGameKeys(t *testing.T, items []map[string]any, want bool) {
	t.Helper()
	for _, item := range items {
		if _, ok := item["game_series"]; ok != want {
			t.Fatalf("game_series present=%v, want %v: %#v", ok, want, item)
		}
		if !want {
			if _, ok := item["game"]; ok {
				t.Fatalf("guest exposed game record: %#v", item)
			}
		}
	}
}

func TestListArcades_GameMembershipIsMemberOnly(t *testing.T) {
	app := newArcadeTestApp(t)
	token, owner := createAuthUser(t, app)
	seedPublicArcade(t, app, owner.Id, arcadeSeed{
		Name:     "Games Gate Arcade",
		Address:  "Seoul",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})

	guest := listArcadeItems(t, executeJSONRequest(t, app, http.MethodGet, "/arcades", "", nil))
	assertArcadeGameKeys(t, guest, false)
	if guest[0]["name"] != "Games Gate Arcade" {
		t.Fatalf("guest lost public summary fields: %#v", guest[0])
	}

	member := listArcadeItems(t, executeJSONRequest(t, app, http.MethodGet, "/arcades", "", map[string]string{"Authorization": "Bearer " + token}))
	assertArcadeGameKeys(t, member, true)
}

func TestListArcades_TrustedReaderReceivesGames(t *testing.T) {
	app := newArcadeTestApp(t)
	_, owner := createAuthUser(t, app)
	seedPublicArcade(t, app, owner.Id, arcadeSeed{
		Name:     "Trusted Reader Arcade",
		Address:  "Seoul",
		Location: location{Lat: 37.5665, Lon: 126.978},
	})
	readpolicy.RegisterTrustedReader(app, func(re *core.RequestEvent) bool {
		return re.Request.Header.Get("X-Test-Trusted-Reader") == "yes"
	})

	trusted := listArcadeItems(t, executeJSONRequest(t, app, http.MethodGet, "/arcades", "", map[string]string{"X-Test-Trusted-Reader": "yes"}))
	assertArcadeGameKeys(t, trusted, true)

	untrusted := listArcadeItems(t, executeJSONRequest(t, app, http.MethodGet, "/arcades", "", map[string]string{"X-Test-Trusted-Reader": "no"}))
	assertArcadeGameKeys(t, untrusted, false)
}
