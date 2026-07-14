package arcade_test

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	"github.com/ericbaek/musecat-backend-core/geo"
	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
	arcadebasic "github.com/ericbaek/musecat-backend-core/handlers/arcade/basic"
	arcadeflag "github.com/ericbaek/musecat-backend-core/handlers/arcade/flag"
	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
	arcadegtk "github.com/ericbaek/musecat-backend-core/handlers/arcade/gtk"
	arcadehour "github.com/ericbaek/musecat-backend-core/handlers/arcade/hour"
	arcadenotice "github.com/ericbaek/musecat-backend-core/handlers/arcade/notice"
	arcadephoto "github.com/ericbaek/musecat-backend-core/handlers/arcade/photo"
	arcadepublic "github.com/ericbaek/musecat-backend-core/handlers/arcade/public"
	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	arcadesns "github.com/ericbaek/musecat-backend-core/handlers/arcade/sns"
	arcadeversion "github.com/ericbaek/musecat-backend-core/handlers/arcade/version"
	searchhandler "github.com/ericbaek/musecat-backend-core/handlers/search"
	statshandler "github.com/ericbaek/musecat-backend-core/handlers/stats"
	"github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func stubGeoLookup(tb testing.TB) {
	stubGeoLookupWithResolver(tb, func(req *http.Request) (string, string, error) {
		switch req.URL.Host {
		case "api.bigdatacloud.net":
			return "KR", "Asia/Seoul", nil
		case "timeapi.io":
			return "KR", "Asia/Seoul", nil
		default:
			return "", "", fmt.Errorf("unexpected geo host: %s", req.URL.Host)
		}
	})
}

func stubGeoLookupWithResolver(tb testing.TB, resolve func(*http.Request) (string, string, error)) {
	tb.Helper()

	client := &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			country, timezone, err := resolve(req)
			if err != nil {
				return nil, err
			}

			var body string
			switch req.URL.Host {
			case "api.bigdatacloud.net":
				body = fmt.Sprintf(`{"countryCode":%q}`, country)
			case "timeapi.io":
				body = fmt.Sprintf(`{"timeZone":%q}`, timezone)
			default:
				return nil, fmt.Errorf("unexpected geo host: %s", req.URL.Host)
			}

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Request:    req,
			}, nil
		}),
		Timeout: time.Second,
	}

	restore := geo.SetHTTPClient(client)
	tb.Cleanup(restore)
}

func stubGeoLookupByLocation(tb testing.TB, resolve func(lat, lon float64) (country, timezone string)) {
	tb.Helper()

	stubGeoLookupWithResolver(tb, func(req *http.Request) (string, string, error) {
		q := req.URL.Query()
		switch req.URL.Host {
		case "api.bigdatacloud.net":
			lat, err := strconv.ParseFloat(q.Get("latitude"), 64)
			if err != nil {
				return "", "", err
			}
			lon, err := strconv.ParseFloat(q.Get("longitude"), 64)
			if err != nil {
				return "", "", err
			}
			country, timezone := resolve(lat, lon)
			return country, timezone, nil
		case "timeapi.io":
			lat, err := strconv.ParseFloat(q.Get("latitude"), 64)
			if err != nil {
				return "", "", err
			}
			lon, err := strconv.ParseFloat(q.Get("longitude"), 64)
			if err != nil {
				return "", "", err
			}
			country, timezone := resolve(lat, lon)
			return country, timezone, nil
		default:
			return "", "", fmt.Errorf("unexpected geo host: %s", req.URL.Host)
		}
	})
}

func newArcadeTestApp(tb testing.TB) *tests.TestApp {
	app := testutil.NewTestApp(tb)
	arcadeversion.RegisterHooks(app)
	arcadequery.RegisterCandidateSnapshotHooks(app)
	user.RegisterHooks(app)
	arcadeadmin.RegisterTelegramNotifyHooks(app)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/search", searchhandler.Search)
		se.Router.GET("/stats", statshandler.GetStats)
		se.Router.GET("/arcade", arcadequery.GetArcadeValues)
		se.Router.GET("/arcades", arcadequery.ListArcades)
		se.Router.GET("/arcades/nearby", arcadequery.ListArcadesBySeriesAndLocation)
		se.Router.GET("/arcades/updates", arcadequery.ListArcadeUpdates)
		se.Router.GET("/arcade/games", arcadequery.ListArcadeGames).Bind(
			apis.RequireAuth("user"),
			arcadequery.RequireModeratorAccess(),
		)
		se.Router.GET("/game_series_version", arcadequery.GetGameSeriesVersion)
		se.Router.POST("/game_series_version", arcadequery.CreateGameSeriesVersion).Bind(
			apis.RequireAuth("user"),
			arcadequery.RequireModeratorAccess(),
		)
		se.Router.PUT("/game_series_version", arcadequery.UpdateGameSeriesVersion).Bind(
			apis.RequireAuth("user"),
			arcadequery.RequireModeratorAccess(),
		)
		se.Router.GET("/support_feedback", arcadeadmin.ListSupportFeedback)
		se.Router.POST("/support_feedback", arcadeadmin.CreateSupportFeedback)
		se.Router.GET("/arcade/notice", arcadenotice.ListArcadeNotice)
		group := se.Router.Group("/arcade").Bind(
			apis.RequireAuth("user"),
			user.RequireActiveUser(),
		)
		group.POST("/new", arcadebasic.NewArcade)
		group.GET("/request_admin", arcadeadmin.ListArcadeRequestAdmin)
		group.POST("/request_admin", arcadeadmin.CreateArcadeRequestAdmin)
		group.POST("/rollback", arcadeadmin.RollbackArcadePart)
		group.POST("/game/bulk_version", arcadeadmin.BulkUpdateArcadeGameVersion).Bind(arcadequery.RequireModeratorAccess())
		group.POST("/game/rollback", arcadeadmin.RollbackArcadeGameUncertain)
		group.POST("/game/confirm", arcadeadmin.ConfirmArcadeGameUncertain)
		group.POST("/game/information/confirm", arcadegame.ConfirmArcadeGameInformation)
		group.PUT("/basic", arcadebasic.UpdateArcadeBasic)
		group.PUT("/gtk", arcadegtk.UpdateArcadeGTK)
		group.PUT("/sns", arcadesns.UpdateArcadeSNS)
		group.POST("/flag", arcadeflag.CreateArcadeFlag)
		group.POST("/flag/delete", arcadeflag.DeleteArcadeFlag)
		group.POST("/flag/reaction", arcadeflag.UpdateArcadeFlagReaction)
		group.POST("/notice", arcadenotice.CreateArcadeNotice)
		group.PUT("/notice", arcadenotice.UpdateArcadeNotice)
		group.DELETE("/notice", arcadenotice.DeleteArcadeNotice)
		group.PUT("/game", arcadegame.UpdateArcadeGame)
		group.PUT("/hour", arcadehour.UpdateArcadeHour)
		group.PUT("/public", arcadepublic.RequestPublicArcade)
		group.PUT("/photo", arcadephoto.UpdateArcadePhoto)
		group.POST("/photo/upload", arcadephoto.UploadArcadePhotos)

		authUser := se.Router.Group("/user").Bind(apis.RequireAuth("user"))
		authUser.GET("/report", arcadeadmin.ListUserReport).Bind(user.RequireActiveUser())
		authUser.POST("/report", arcadeadmin.CreateUserReport).Bind(user.RequireActiveUser())
		supporter := se.Router.Group("/supporter").Bind(apis.RequireAuth("user"), user.RequireActiveUser())
		supporter.GET("/score", arcadeadmin.GetSupporterScore)
		supporter.POST("/request", arcadeadmin.CreateSupporterRequest)
		return se.Next()
	})
	return app
}

func newArcadeScenario(body string) tests.ApiScenario {
	headers := map[string]string{}

	scenario := tests.ApiScenario{
		Method:  http.MethodPost,
		URL:     "/arcade/new",
		Body:    strings.NewReader(body),
		Headers: headers,
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		stubGeoLookup(tb)
		token, _ := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token
	}

	return scenario
}

func createAuthUser(tb testing.TB, app *tests.TestApp) (string, *core.Record) {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		tb.Fatalf("failed to load user collection: %v", err)
	}

	rec := core.NewRecord(coll)
	unique := time.Now().UnixNano()
	rec.SetEmail(fmt.Sprintf("arcade_test_%d@example.com", unique))
	rec.Set("username", fmt.Sprintf("arcade_test_%d", unique))
	rec.SetPassword("secret123")

	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to create auth record: %v", err)
	}
	ensureUserInfo(tb, app, rec.Id)

	token, err := rec.NewAuthToken()
	if err != nil {
		tb.Fatalf("failed to create auth token: %v", err)
	}

	return token, rec
}

func createAuthUserWithTags(tb testing.TB, app *tests.TestApp, tags []string) (string, *core.Record) {
	tb.Helper()

	token, rec := createAuthUser(tb, app)
	rec.Set("tags", tags)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to update auth user tags: %v", err)
	}

	token, err := rec.NewAuthToken()
	if err != nil {
		tb.Fatalf("failed to refresh auth token: %v", err)
	}

	return token, rec
}

func ensureUserInfo(tb testing.TB, app *tests.TestApp, userID string) *core.Record {
	tb.Helper()

	rec, err := app.FindRecordById("user_info", userID)
	if err == nil {
		return rec
	}

	coll, collErr := app.FindCollectionByNameOrId("user_info")
	if collErr != nil {
		tb.Fatalf("failed to load user_info collection: %v", collErr)
	}

	rec = core.NewRecord(coll)
	rec.Set("id", userID)
	if saveErr := app.Save(rec); saveErr != nil {
		tb.Fatalf("failed to create user_info record: %v", saveErr)
	}

	return rec
}

type location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

func decodeLocation(tb testing.TB, raw any) location {
	tb.Helper()

	if raw == nil {
		tb.Fatalf("location value missing")
	}

	buf, err := json.Marshal(raw)
	if err != nil {
		tb.Fatalf("failed to marshal location: %v", err)
	}

	var loc location
	if err := json.Unmarshal(buf, &loc); err != nil {
		tb.Fatalf("failed to unmarshal location: %v", err)
	}

	return loc
}

func floatAlmostEq(a, b float64) bool {
	const epsilon = 1e-6
	return math.Abs(a-b) < epsilon
}

type arcadeSeed struct {
	Name       string
	Address    string
	Direction  string
	Nickname   []string
	SubwayLine []string
	Location   location
	Country    string
	Timezone   string
}

func seedArcade(tb testing.TB, app *tests.TestApp, createdBy string, seed arcadeSeed) (arcadeID, basicID string) {
	tb.Helper()

	if seed.Country == "" {
		seed.Country = "KR"
	}
	if seed.Timezone == "" {
		seed.Timezone = "Asia/Seoul"
	}

	arcadeColl, err := app.FindCollectionByNameOrId("arcade")
	if err != nil {
		tb.Fatalf("failed to load arcade collection: %v", err)
	}

	arcadeRec := core.NewRecord(arcadeColl)
	arcadeRec.Set("country", seed.Country)
	arcadeRec.Set("timezone", seed.Timezone)
	if createdBy != "" {
		arcadeRec.Set("createdBy", createdBy)
	}

	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to save arcade: %v", err)
	}

	basicColl, err := app.FindCollectionByNameOrId("arcade_basic")
	if err != nil {
		tb.Fatalf("failed to load arcade_basic collection: %v", err)
	}

	basicRec := core.NewRecord(basicColl)
	basicRec.Set("name", seed.Name)
	basicRec.Set("address", seed.Address)
	basicRec.Set("direction", seed.Direction)
	basicRec.Set("nickname", seed.Nickname)
	basicRec.Set("subway_line", seed.SubwayLine)
	basicRec.Set("arcade", arcadeRec.Id)
	basicRec.Set("location", map[string]any{"lat": seed.Location.Lat, "lon": seed.Location.Lon})
	if createdBy != "" {
		basicRec.Set("createdBy", createdBy)
	}

	if err := app.Save(basicRec); err != nil {
		tb.Fatalf("failed to save arcade_basic: %v", err)
	}

	arcadeRec.Set("basic", basicRec.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.basic: %v", err)
	}

	return arcadeRec.Id, basicRec.Id
}

func loadChangelogRecords(tb testing.TB, app *tests.TestApp, arcadeID, field string) []*core.Record {
	tb.Helper()

	changes, err := app.FindRecordsByFilter("arcade_changelog", "arcade={:id} && changed={:field}", "-created", 0, 0, map[string]any{
		"id":    arcadeID,
		"field": field,
	})
	if err != nil {
		tb.Fatalf("failed to load arcade_changelog: %v", err)
	}
	return changes
}

func decodeLogObject(tb testing.TB, raw any) map[string]any {
	tb.Helper()

	var logObj map[string]any
	buf, err := json.Marshal(raw)
	if err != nil {
		tb.Fatalf("failed to marshal changelog.log: %v", err)
	}
	if err := json.Unmarshal(buf, &logObj); err != nil {
		tb.Fatalf("failed to decode changelog.log: %v", err)
	}
	return logObj
}

func executeJSONRequest(tb testing.TB, app *tests.TestApp, method, url, body string, headers map[string]string) *http.Response {
	tb.Helper()

	baseRouter, err := apis.NewRouter(app)
	if err != nil {
		tb.Fatalf("failed to initialize router: %v", err)
	}

	serveEvent := &core.ServeEvent{
		App:    app,
		Router: baseRouter,
	}
	if err := app.OnServe().Trigger(serveEvent, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		tb.Fatalf("failed to register routes: %v", err)
	}

	mux, err := serveEvent.Router.BuildMux()
	if err != nil {
		tb.Fatalf("failed to build router mux: %v", err)
	}

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("content-type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, strings.TrimSpace(v))
	}
	mux.ServeHTTP(recorder, req)
	return recorder.Result()
}

func seedGameSeriesVersion(tb testing.TB, app *tests.TestApp) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("game_series_version")
	if err != nil {
		tb.Fatalf("failed to load game_series_version collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("en", "Test Version")
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save game_series_version: %v", err)
	}

	return rec.Id
}
