package arcade_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/tests"
)

func TestNewArcade_Success(t *testing.T) {
	scenario := newArcadeScenario(`{
		"name": "Test Arcade",
		"location": {"lat": 37.5665, "lon": 126.9780},
		"address": "Seoul, Korea",
		"direction": "B1F",
		"nickname": ["Arcade", "Fun"]
	}`)

	scenario.Name = "POST /arcade/new success"
	scenario.ExpectedStatus = http.StatusOK
	scenario.ExpectedContent = []string{
		`"name":"Test Arcade"`,
		`"country":"KR"`,
		`"timezone":"Asia/Seoul"`,
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		id, _ := payload["id"].(string)
		if id == "" {
			tb.Fatalf("expected arcade id in response")
		}

		arcadeRec, err := app.FindRecordById("arcade", id)
		if err != nil {
			tb.Fatalf("failed to load arcade record: %v", err)
		}

		if got := arcadeRec.GetString("country"); got != "KR" {
			tb.Fatalf("expected country KR, got %q", got)
		}
		if got := arcadeRec.GetString("timezone"); got != "Asia/Seoul" {
			tb.Fatalf("expected timezone Asia/Seoul, got %q", got)
		}

		basicID := arcadeRec.GetString("basic")
		if basicID == "" {
			tb.Fatalf("arcade.basic relation empty")
		}

		basicRec, err := app.FindRecordById("arcade_basic", basicID)
		if err != nil {
			tb.Fatalf("failed to load arcade_basic: %v", err)
		}
		if got := basicRec.GetString("name"); got != "Test Arcade" {
			tb.Fatalf("expected name Test Arcade, got %q", got)
		}
		if got := basicRec.GetString("address"); got != "Seoul, Korea" {
			tb.Fatalf("expected address Seoul, Korea, got %q", got)
		}
		if got := basicRec.GetString("direction"); got != "B1F" {
			tb.Fatalf("expected direction B1F, got %q", got)
		}
		if nicks := basicRec.GetStringSlice("nickname"); len(nicks) != 2 || nicks[0] != "Arcade" || nicks[1] != "Fun" {
			tb.Fatalf("unexpected nickname slice: %#v", nicks)
		}

		loc := decodeLocation(tb, basicRec.Get("location"))
		if !floatAlmostEq(loc.Lat, 37.5665) || !floatAlmostEq(loc.Lon, 126.9780) {
			tb.Fatalf("unexpected location: %+v", loc)
		}

		changes := loadChangelogRecords(tb, app, id, "basic")
		if len(changes) != 1 {
			tb.Fatalf("expected 1 basic changelog row, got %d", len(changes))
		}
		logObj := decodeLogObject(tb, changes[0].Get("log"))
		if got, _ := logObj["type"].(string); got != "basic_diff" {
			tb.Fatalf("expected changelog.log.type=basic_diff, got %v", logObj["type"])
		}
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected 1 basic log item, got %T %#v", logObj["items"], logObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected basic log item object, got %T", items[0])
		}
		if got, _ := item["change_type"].(string); got != "added" {
			tb.Fatalf("expected basic creation change_type=added, got %v", item["change_type"])
		}
	}

	scenario.Test(t)
}

func TestNewArcade_InvalidPayloads(t *testing.T) {
	type testCase struct {
		name            string
		body            string
		status          int
		expectedContent []string
	}

	cases := []testCase{
		{
			name:   "missing name",
			body:   `{"address":"Seoul, Korea","location":{"lat":37.5665,"lon":126.9780}}`,
			status: http.StatusBadRequest,
			expectedContent: []string{
				`"error":"validation failed"`,
				`"details":"name is required"`,
			},
		},
		{
			name:   "missing address",
			body:   `{"name":"Arcade","location":{"lat":37.5665,"lon":126.9780}}`,
			status: http.StatusBadRequest,
			expectedContent: []string{
				`"error":"validation failed"`,
				`"details":"address is required"`,
			},
		},
		{
			name:   "missing location",
			body:   `{"name":"Arcade","address":"Seoul, Korea"}`,
			status: http.StatusBadRequest,
			expectedContent: []string{
				`"error":"validation failed"`,
				`"details":"location is required"`,
			},
		},
		{
			name:   "location null",
			body:   `{"name":"Arcade","address":"Seoul, Korea","location":null}`,
			status: http.StatusBadRequest,
			expectedContent: []string{
				`"error":"validation failed"`,
				`"details":"location is required"`,
			},
		},
		{
			name:   "invalid latitude",
			body:   `{"name":"Arcade","address":"Seoul, Korea","location":{"lat":0,"lon":126.9780}}`,
			status: http.StatusBadRequest,
			expectedContent: []string{
				`"error":"validation failed"`,
				`location.lat out of range 0.000000`,
			},
		},
		{
			name:   "invalid longitude",
			body:   `{"name":"Arcade","address":"Seoul, Korea","location":{"lat":37.5665,"lon":0}}`,
			status: http.StatusBadRequest,
			expectedContent: []string{
				`"error":"validation failed"`,
				`location.lon out of rang 0.000000`,
			},
		},
		{
			name:   "malformed location type",
			body:   `{"name":"Arcade","address":"Seoul, Korea","location":"wrong"}`,
			status: http.StatusBadRequest,
			expectedContent: []string{
				`"error":"invalid JSON body"`,
				`cannot unmarshal string into Go struct field`,
			},
		},
		{
			name:   "nickname not array",
			body:   `{"name":"Arcade","address":"Seoul, Korea","location":{"lat":37.5665,"lon":126.9780},"nickname":"Arcade"}`,
			status: http.StatusBadRequest,
			expectedContent: []string{
				`"error":"invalid JSON body"`,
				`cannot unmarshal string into Go struct field`,
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			scenario := newArcadeScenario(tc.body)
			scenario.Name = tc.name
			scenario.ExpectedStatus = tc.status
			scenario.ExpectedContent = append([]string(nil), tc.expectedContent...)

			scenario.Test(t)
		})
	}
}
