package arcade_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestCreateArcadeFlag_Success(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var atomID string
	var userID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag creates flag and links atom",
		Method:         http.MethodPost,
		URL:            "/arcade/flag",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"game_id":"`,
			`"flag":"`,
			`"game":{"id":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		userID = user.Id
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Flag Create Arcade",
			Address:  "Flag Street",
			Nickname: []string{"FlagCreate"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		atomID = seedGameAtomForFlag(tb, app, arcadeID)

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"game_id":"%s",
			"disruption":"major",
			"message":"coin acceptor jammed"
		}`, arcadeID, atomID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		if got, _ := payload["arcade"].(string); got != arcadeID {
			tb.Fatalf("expected arcade %q, got %v", arcadeID, payload["arcade"])
		}
		if got, _ := payload["game_id"].(string); got != atomID {
			tb.Fatalf("expected game_id %q, got %v", atomID, payload["game_id"])
		}
		flagID, _ := payload["flag"].(string)
		if flagID == "" {
			tb.Fatalf("expected flag id in response")
		}
		gameObj, ok := payload["game"].(map[string]any)
		if !ok {
			tb.Fatalf("expected expanded game object in response, got %T", payload["game"])
		}
		gameID, _ := gameObj["id"].(string)
		if gameID == "" {
			tb.Fatalf("expected game id in response")
		}

		flagRec, err := app.FindRecordById("arcade_flag", flagID)
		if err != nil {
			tb.Fatalf("failed to load arcade_flag: %v", err)
		}
		if got := flagRec.GetString("arcade"); got != arcadeID {
			tb.Fatalf("expected flag.arcade %q, got %q", arcadeID, got)
		}
		if got := flagRec.GetString("disruption"); got != "major" {
			tb.Fatalf("expected disruption major, got %q", got)
		}
		if got := flagRec.GetString("message"); got != "coin acceptor jammed" {
			tb.Fatalf("expected message set, got %q", got)
		}
		if got := flagRec.GetBool("solved"); got {
			tb.Fatalf("expected solved=false")
		}
		if got := flagRec.GetString("createdBy"); got != userID {
			tb.Fatalf("expected createdBy %q, got %q", userID, got)
		}
		if got := flagRec.GetString("game_id"); got != atomID {
			tb.Fatalf("expected flag.game_id %q, got %q", atomID, got)
		}
	}

	scenario.Test(t)
}

func TestCreateArcadeFlag_MultipartWithPhotos(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var atomID string
	var userID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag multipart stores photos",
		Method:         http.MethodPost,
		URL:            "/arcade/flag",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"game_id":"`,
			`"flag":"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		userID = user.Id
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Flag Multipart Arcade",
			Address:  "Flag Multipart Street",
			Nickname: []string{"FlagMultipart"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		atomID = seedGameAtomForFlag(tb, app, arcadeID)

		multipartBody, multipartType := buildFlagMultipart(tb, arcadeID, atomID, "major", "screen flickers", []uploadTestFile{
			{Filename: "flag-a.png", Content: pngFixtureBytes()},
			{Filename: "flag-b.jpg", Content: jpegFixtureBytes()},
		})
		headers["Content-Type"] = multipartType
		scenario.Body = bytes.NewReader(multipartBody)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		if got, _ := payload["arcade"].(string); got != arcadeID {
			tb.Fatalf("expected arcade %q, got %v", arcadeID, payload["arcade"])
		}
		if got, _ := payload["game_id"].(string); got != atomID {
			tb.Fatalf("expected game_id %q, got %v", atomID, payload["game_id"])
		}
		flagID, _ := payload["flag"].(string)
		if flagID == "" {
			tb.Fatalf("expected flag id in response")
		}

		flagRec, err := app.FindRecordById("arcade_flag", flagID)
		if err != nil {
			tb.Fatalf("failed to load arcade_flag: %v", err)
		}
		if got := flagRec.GetString("createdBy"); got != userID {
			tb.Fatalf("expected createdBy %q, got %q", userID, got)
		}
		if got := len(flagRec.GetStringSlice("photos")); got != 2 {
			tb.Fatalf("expected 2 photos on flag, got %d", got)
		}
	}

	scenario.Test(t)
}

func TestCreateArcadeFlag_MultipartTooManyPhotos(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var atomID string

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag multipart blocks over 3 photos",
		Method:         http.MethodPost,
		URL:            "/arcade/flag",
		Headers:        headers,
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"validation failed"`,
			`"details":"photos must have at most 3 items"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Flag Photo Limit Arcade",
			Address:  "Flag Limit Street",
			Nickname: []string{"FlagLimit"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		atomID = seedGameAtomForFlag(tb, app, arcadeID)

		multipartBody, multipartType := buildFlagMultipart(tb, arcadeID, atomID, "major", "too many photos", []uploadTestFile{
			{Filename: "flag-1.png", Content: pngFixtureBytes()},
			{Filename: "flag-2.jpg", Content: jpegFixtureBytes()},
			{Filename: "flag-3.png", Content: pngFixtureBytes()},
			{Filename: "flag-4.jpg", Content: jpegFixtureBytes()},
		})
		headers["Content-Type"] = multipartType
		scenario.Body = bytes.NewReader(multipartBody)
	}

	scenario.Test(t)
}

func buildFlagMultipart(
	tb testing.TB,
	arcadeID string,
	atomID string,
	disruption string,
	message string,
	files []uploadTestFile,
) ([]byte, string) {
	tb.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("arcade", arcadeID); err != nil {
		tb.Fatalf("failed to write arcade field: %v", err)
	}
	if err := writer.WriteField("game_id", atomID); err != nil {
		tb.Fatalf("failed to write game_id field: %v", err)
	}
	if err := writer.WriteField("disruption", disruption); err != nil {
		tb.Fatalf("failed to write disruption field: %v", err)
	}
	if err := writer.WriteField("message", message); err != nil {
		tb.Fatalf("failed to write message field: %v", err)
	}

	for _, f := range files {
		part, err := writer.CreateFormFile("photos", f.Filename)
		if err != nil {
			tb.Fatalf("failed to create form file: %v", err)
		}
		if _, err := part.Write(f.Content); err != nil {
			tb.Fatalf("failed to write form file content: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		tb.Fatalf("failed to close multipart writer: %v", err)
	}

	return buf.Bytes(), writer.FormDataContentType()
}

func TestCreateArcadeFlag_Validation(t *testing.T) {
	type testCase struct {
		name   string
		body   string
		detail string
	}

	cases := []testCase{
		{
			name:   "missing arcade",
			body:   `{"game_id":"atom","disruption":"major","message":"broken"}`,
			detail: `"details":"arcade is required"`,
		},
		{
			name:   "missing game_id",
			body:   `{"arcade":"arc","disruption":"major","message":"broken"}`,
			detail: `"details":"game_id is required"`,
		},
		{
			name:   "missing disruption",
			body:   `{"arcade":"arc","game_id":"atom","message":"broken"}`,
			detail: `"details":"disruption is required"`,
		},
		{
			name:   "missing message",
			body:   `{"arcade":"arc","game_id":"atom","disruption":"major"}`,
			detail: `"details":"message is required"`,
		},
		{
			name:   "invalid disruption",
			body:   `{"arcade":"arc","game_id":"atom","disruption":"critical","message":"broken"}`,
			detail: `"details":"disruption must be one of unplayable, major, bearable, minor"`,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			scenario := tests.ApiScenario{
				Name:           "POST /arcade/flag " + tc.name,
				Method:         http.MethodPost,
				URL:            "/arcade/flag",
				Headers:        headers,
				Body:           strings.NewReader(tc.body),
				ExpectedStatus: http.StatusBadRequest,
				ExpectedContent: []string{
					`"error":"validation failed"`,
					tc.detail,
				},
				TestAppFactory: func(tb testing.TB) *tests.TestApp {
					return newArcadeTestApp(tb)
				},
			}

			scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				tb.Helper()
				token, _ := createAuthUser(tb, app)
				headers["Authorization"] = "Bearer " + token
			}

			scenario.Test(t)
		})
	}
}

func TestCreateArcadeFlag_NotFoundOrCrossArcade(t *testing.T) {
	t.Run("arcade not found", func(t *testing.T) {
		headers := map[string]string{}
		var atomID string

		scenario := tests.ApiScenario{
			Name:           "POST /arcade/flag arcade not found",
			Method:         http.MethodPost,
			URL:            "/arcade/flag",
			Headers:        headers,
			ExpectedStatus: http.StatusBadGateway,
			ExpectedContent: []string{
				`"error":"transaction failed"`,
				`"details":"arcade not found:`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newArcadeTestApp(tb)
			},
		}

		scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			tb.Helper()
			token, user := createAuthUser(tb, app)
			headers["Authorization"] = "Bearer " + token

			realArcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
				Name:     "Real Arcade",
				Address:  "Real Street",
				Nickname: []string{"Real"},
				Location: location{Lat: 37.5665, Lon: 126.978},
			})
			atomID = seedGameAtomForFlag(tb, app, realArcadeID)

			scenario.Body = strings.NewReader(fmt.Sprintf(`{
				"arcade":"nonexistent_arcade",
				"game_id":"%s",
				"disruption":"minor",
				"message":"broken"
			}`, atomID))
		}

		scenario.Test(t)
	})

	t.Run("game id not found", func(t *testing.T) {
		headers := map[string]string{}
		var arcadeID string

		scenario := tests.ApiScenario{
			Name:           "POST /arcade/flag game id not found",
			Method:         http.MethodPost,
			URL:            "/arcade/flag",
			Headers:        headers,
			ExpectedStatus: http.StatusBadGateway,
			ExpectedContent: []string{
				`"error":"transaction failed"`,
				`"details":"game entry not found:`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newArcadeTestApp(tb)
			},
		}

		scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			tb.Helper()
			token, user := createAuthUser(tb, app)
			headers["Authorization"] = "Bearer " + token

			arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
				Name:     "Atom Missing Arcade",
				Address:  "Missing Atom Street",
				Nickname: []string{"MissingAtom"},
				Location: location{Lat: 37.5665, Lon: 126.978},
			})

			scenario.Body = strings.NewReader(fmt.Sprintf(`{
				"arcade":"%s",
				"game_id":"nonexistent_atom",
				"disruption":"minor",
				"message":"broken"
			}`, arcadeID))
		}

		scenario.Test(t)
	})

	t.Run("game id belongs to another arcade", func(t *testing.T) {
		headers := map[string]string{}
		var targetArcadeID string
		var foreignAtomID string

		scenario := tests.ApiScenario{
			Name:           "POST /arcade/flag cross arcade atom",
			Method:         http.MethodPost,
			URL:            "/arcade/flag",
			Headers:        headers,
			ExpectedStatus: http.StatusBadGateway,
			ExpectedContent: []string{
				`"error":"transaction failed"`,
				`"details":"game_id does not belong to arcade"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp {
				return newArcadeTestApp(tb)
			},
		}

		scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			tb.Helper()
			token, user := createAuthUser(tb, app)
			headers["Authorization"] = "Bearer " + token

			targetArcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
				Name:     "Target Arcade",
				Address:  "Target Street",
				Nickname: []string{"Target"},
				Location: location{Lat: 37.5665, Lon: 126.978},
			})
			foreignArcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
				Name:     "Foreign Arcade",
				Address:  "Foreign Street",
				Nickname: []string{"Foreign"},
				Location: location{Lat: 35.1796, Lon: 129.0756},
			})
			foreignAtomID = seedGameAtomForFlag(tb, app, foreignArcadeID)

			scenario.Body = strings.NewReader(fmt.Sprintf(`{
				"arcade":"%s",
				"game_id":"%s",
				"disruption":"minor",
				"message":"broken"
			}`, targetArcadeID, foreignAtomID))
		}

		scenario.Test(t)
	})
}

func TestCreateArcadeFlag_BlocksWithdrawnUser(t *testing.T) {
	headers := map[string]string{}

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/flag returns 403 for withdrawn users",
		Method:         http.MethodPost,
		URL:            "/arcade/flag",
		Headers:        headers,
		Body:           strings.NewReader(`{"arcade":"x","game_id":"y","disruption":"major","message":"broken"}`),
		ExpectedStatus: http.StatusForbidden,
		ExpectedContent: []string{
			`"code":"ACCOUNT_WITHDRAWN"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newWithdrawTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()
		ensureWithdrawFields(tb, app)
		_, userRec := createAuthUser(tb, app)
		userRec.Set("withdrawn", true)
		userRec.Set("withdrawnAt", time.Now().UTC())
		if err := app.Save(userRec); err != nil {
			tb.Fatalf("failed to mark user withdrawn: %v", err)
		}

		token, err := userRec.NewAuthToken()
		if err != nil {
			tb.Fatalf("failed to create token: %v", err)
		}
		headers["Authorization"] = "Bearer " + token
	}

	scenario.Test(t)
}

func seedGameAtomForFlag(tb testing.TB, app *tests.TestApp, arcadeID string) string {
	tb.Helper()

	versionID := seedGameSeriesVersion(tb, app)
	version, err := app.FindRecordById("game_series_version", versionID)
	if err != nil {
		tb.Fatalf("failed to load game_series_version: %v", err)
	}
	entryColl, err := app.FindCollectionByNameOrId("arcade_game_id")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_id: %v", err)
	}
	entry := core.NewRecord(entryColl)
	entry.Set("arcade", arcadeID)
	entry.Set("series", version.GetString("series"))
	if err := app.Save(entry); err != nil {
		tb.Fatalf("failed to create arcade_game_id: %v", err)
	}
	batchColl, err := app.FindCollectionByNameOrId("arcade_game_history_batch")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_history_batch: %v", err)
	}
	batch := core.NewRecord(batchColl)
	batch.Set("arcade", arcadeID)
	batch.Set("reason", "flag test")
	if err := app.Save(batch); err != nil {
		tb.Fatalf("failed to create arcade_game_history_batch: %v", err)
	}
	revisionColl, err := app.FindCollectionByNameOrId("arcade_game_history")
	if err != nil {
		tb.Fatalf("failed to load arcade_game_history: %v", err)
	}
	revision := core.NewRecord(revisionColl)
	revision.Set("batch", batch.Id)
	revision.Set("entry", entry.Id)
	revision.Set("version", versionID)
	revision.Set("location", "1F")
	revision.Set("quantity", 1)
	revision.Set("price", map[string]any{
		"currency": "KRW",
		"type":     "credit",
		"list":     []map[string]any{{"value": 500}},
		"accept":   []string{},
	})
	revision.Set("tag", []map[string]any{{"category": "기타", "note": "ok"}})
	if err := app.Save(revision); err != nil {
		tb.Fatalf("failed to create arcade_game_history: %v", err)
	}

	arcadeRec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}
	arcadeRec.Set("game_v2", batch.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.game_v2: %v", err)
	}

	return entry.Id
}
