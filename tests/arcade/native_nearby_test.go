package arcade_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/geo"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/ericbaek/musecat-backend-core/service/readpolicy"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

var nearbyNativeClients = []nativeauth.Client{{ID: "app.musecat.native.dev", Callback: "musecat-dev://auth/callback"}}

func nativeNearbyApp(t *testing.T, enabled bool) *tests.TestApp {
	t.Helper()
	app := testutil.NewTestApp(t)
	config := coreapp.Config{}
	if enabled {
		config.NativeAuthClients = nearbyNativeClients
	}
	coreapp.Configure(&pocketbase.PocketBase{App: app}, config)
	t.Cleanup(func() { geo.SetOfflineResolver(nil, false) })
	return app
}

func nativeNearbySession(t *testing.T, app core.App, owner *core.Record) (nativeauth.Service, nativeauth.Credentials) {
	t.Helper()
	service := nativeauth.Service{Clients: nearbyNativeClients}
	verifier := strings.Repeat("n", 43)
	hash := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])
	code, err := service.IssueCode(app, owner, nearbyNativeClients[0].ID, nearbyNativeClients[0].Callback, challenge)
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.Exchange(app, code, verifier, nearbyNativeClients[0].ID, nearbyNativeClients[0].Callback)
	if err != nil {
		t.Fatal(err)
	}
	return service, credentials
}

func TestCanonicalNativeNearbyMatchesWebAndPreservesGuestAndCabinetPolicy(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUser(t, app)
	_, credentials := nativeNearbySession(t, app, owner)
	series := seedNearbyGameSeries(t, app, "Native nearby series")
	version := seedNearbyGameSeriesVersion(t, app, series, "Native nearby version")
	wanted := seedNearbyGameCabinet(t, app, "Wanted cabinet")
	other := seedNearbyGameCabinet(t, app, "Other cabinet")
	var matching string
	for _, item := range []struct {
		name, cabinet string
		public        bool
	}{{"Matching", wanted, true}, {"Other", other, true}, {"Private", wanted, false}} {
		id, _ := seedArcade(t, app, owner.Id, arcadeSeed{Name: item.name, Address: "Nearby road", Location: location{Lat: 37.5665, Lon: 126.9780}})
		setArcadeVisibility(t, app, id, item.public, false)
		batch := seedNearbyGameMolecule(t, app, id)
		seedNearbyGameAtomWithCabinet(t, app, batch, version, item.cabinet)
		if item.name == "Matching" {
			matching = id
		}
	}
	read := func(path, token string, want int) map[string]any {
		t.Helper()
		headers := map[string]string{}
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		response := executeJSONRequest(t, app, http.MethodGet, path, "", headers)
		defer response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("nearby status %d want %d", response.StatusCode, want)
		}
		if token == credentials.AccessToken && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("native nearby response is cacheable")
		}
		var body map[string]any
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	base := "/arcades/nearby?lat=37.5665&lon=126.9780&expand=true&game_series=" + series
	member := read(base, web, 200)
	if !reflect.DeepEqual(member, read(base, credentials.AccessToken, 200)) {
		t.Fatal("native nearby changed canonical web result")
	}
	items := member["items"].([]any)
	if len(items) != 2 {
		t.Fatal("nearby lost public/private venue boundary")
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		if !reflect.DeepEqual(item["game_series"], []any{series}) {
			t.Fatal("native nearby lost member series metadata")
		}
		game, ok := item["game"].(map[string]any)
		if !ok || len(game["items"].([]any)) != 1 {
			t.Fatal("native nearby lost expanded inventory")
		}
	}
	guest := read(base, "", 200)
	for _, raw := range guest["items"].([]any) {
		item := raw.(map[string]any)
		for _, field := range []string{"game_series", "game"} {
			if _, exists := item[field]; exists {
				t.Fatal("guest nearby disclosed member metadata")
			}
		}
	}
	if len(guest["campaigns"].([]any)) != 0 {
		t.Fatal("guest nearby disclosed campaign metadata")
	}
	filter := url.QueryEscape(`{"series":"` + series + `","cabinet":"` + wanted + `"}`)
	filtered := "/arcades/nearby?lat=37.5665&lon=126.9780&expand=true&game_filter=" + filter
	read(filtered, "", 401)
	selected := read(filtered, credentials.AccessToken, 200)
	if !reflect.DeepEqual(selected, read(filtered, web, 200)) {
		t.Fatal("native cabinet query changed web contract")
	}
	result := selected["items"].([]any)
	if len(result) != 1 || result[0].(map[string]any)["id"] != matching {
		t.Fatal("native cabinet filter did not filter venue results")
	}
	calls := 0
	readpolicy.Register(app, func(re *core.RequestEvent, ids []string) error {
		calls++
		if re.Auth == nil || re.Auth.Id != owner.Id || !reflect.DeepEqual(ids, []string{matching}) {
			t.Fatal("nearby read policy lost account or exposed arcade scope")
		}
		return apis.NewTooManyRequestsError("test restriction", nil)
	})
	read(filtered, web, 429)
	read(filtered, credentials.AccessToken, 429)
	read(base, "", 200)
	if calls != 2 {
		t.Fatal("native nearby bypassed quota or guest summary consumed game quota")
	}
	for _, route := range []struct {
		method, path string
		status       int
	}{{"PUT", "/arcade/memo", 401}, {"GET", "/arcade/games", 401}, {"GET", "/native/arcades/nearby", 404}} {
		response := executeJSONRequest(t, app, route.method, route.path, "{}", map[string]string{"Authorization": "Bearer " + credentials.AccessToken})
		response.Body.Close()
		if response.StatusCode != route.status {
			t.Fatal("nearby adapter expanded unrelated credential scope")
		}
	}
}

func TestCanonicalNativeNearbyRejectsInvalidSessions(t *testing.T) {
	for _, reason := range []string{"invalid", "expired", "revoked", "banned", "removed-client", "disabled"} {
		t.Run(reason, func(t *testing.T) {
			app := nativeNearbyApp(t, reason != "disabled")
			_, owner := createAuthUser(t, app)
			service, credentials := nativeNearbySession(t, app, owner)
			token := credentials.AccessToken
			want := 401
			path := "/arcades/nearby?lat=37.56&lon=126.97"
			switch reason {
			case "disabled":
				path += "&game_series=series000000001&game_cabinet=cabinet00000001"
			case "invalid":
				token = strings.Repeat("z", 43)
			case "expired":
				_, err := app.DB().NewQuery("UPDATE native_auth_session SET access_expires=0 WHERE user_id={:user}").Bind(dbx.Params{"user": owner.Id}).Execute()
				if err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if err := service.Revoke(app, credentials.RefreshToken); err != nil {
					t.Fatal(err)
				}
			case "banned":
				seedUserBan(t, app, owner.Id, "", "native_nearby_fixture", time.Now().Add(time.Hour))
				want = 403
			case "removed-client":
				_, err := app.DB().NewQuery("UPDATE native_auth_session SET client='removed-client' WHERE user_id={:user}").Bind(dbx.Params{"user": owner.Id}).Execute()
				if err != nil {
					t.Fatal(err)
				}
			}
			response := executeJSONRequest(t, app, "GET", path, "", map[string]string{"Authorization": "Bearer " + token})
			defer response.Body.Close()
			if response.StatusCode != want {
				t.Fatalf("nearby status %d want %d", response.StatusCode, want)
			}
			if reason != "disabled" && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
				t.Fatal("native denial is cacheable")
			}
		})
	}
}
