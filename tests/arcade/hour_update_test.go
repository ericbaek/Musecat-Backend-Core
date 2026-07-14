package arcade_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func decodeHourExpanded(tb testing.TB, payload map[string]any) map[string]any {
	tb.Helper()

	rawHour, ok := payload["hour"]
	if !ok || rawHour == nil {
		tb.Fatalf("expected hour in response, got %#v", payload["hour"])
	}

	buf, err := json.Marshal(rawHour)
	if err != nil {
		tb.Fatalf("failed to marshal hour response: %v", err)
	}

	var hour map[string]any
	if err := json.Unmarshal(buf, &hour); err != nil {
		tb.Fatalf("failed to decode hour response: %v", err)
	}

	return hour
}

func TestUpdateArcadeHour_AllowsOvernightClosing(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/hour allows overnight closing",
		Method:         http.MethodPut,
		URL:            "/arcade/hour",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"hour":{"Friday":`,
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
			Name:     "Overnight Arcade",
			Address:  "Night Street",
			Nickname: []string{"Night"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"Monday":{"start":1000,"end":200}
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		hour := decodeHourExpanded(tb, payload)
		if got, _ := hour["id"].(string); got == "" {
			tb.Fatalf("expected hour.id in response")
		}

		hourID := hour["id"].(string)
		hourRec, err := app.FindRecordById("arcade_hour", hourID)
		if err != nil {
			tb.Fatalf("failed to load arcade_hour: %v", err)
		}

		var monday struct {
			Start int `json:"start"`
			End   int `json:"end"`
		}
		raw, _ := json.Marshal(hourRec.Get("Monday"))
		if err := json.Unmarshal(raw, &monday); err != nil {
			tb.Fatalf("failed to decode Monday hours: %v", err)
		}
		if monday.Start != 1000 || monday.End != 200 {
			tb.Fatalf("expected Monday {start:1000,end:200}, got %+v", monday)
		}

		arcadeRec, err := app.FindRecordById("arcade", arcadeID)
		if err != nil {
			tb.Fatalf("failed to load arcade: %v", err)
		}
		if got := arcadeRec.GetString("hour"); got != hourID {
			tb.Fatalf("expected arcade.hour %q, got %q", hourID, got)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeHour_AllowsFullDayByZeroZero(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/hour allows full day hours as 00:00-00:00",
		Method:         http.MethodPut,
		URL:            "/arcade/hour",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"hour":{"Friday":`,
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
			Name:     "Full Day Zero Arcade",
			Address:  "Zero Street",
			Nickname: []string{"Zero"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"Monday":{"start":0,"end":0}
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		hour := decodeHourExpanded(tb, payload)
		if got, _ := hour["id"].(string); got == "" {
			tb.Fatalf("expected hour.id in response")
		}

		hourID := hour["id"].(string)
		hourRec, err := app.FindRecordById("arcade_hour", hourID)
		if err != nil {
			tb.Fatalf("failed to load arcade_hour: %v", err)
		}

		var monday struct {
			Start int `json:"start"`
			End   int `json:"end"`
		}
		raw, _ := json.Marshal(hourRec.Get("Monday"))
		if err := json.Unmarshal(raw, &monday); err != nil {
			tb.Fatalf("failed to decode Monday hours: %v", err)
		}
		if monday.Start != 0 || monday.End != 0 {
			tb.Fatalf("expected Monday {start:0,end:0}, got %+v", monday)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeHour_AllowsFullDayByZeroTwentyFourHundred(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/hour allows full day hours as 00:00-24:00",
		Method:         http.MethodPut,
		URL:            "/arcade/hour",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"hour":{"Friday":`,
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
			Name:     "Full Day TwentyFour Arcade",
			Address:  "TwentyFour Street",
			Nickname: []string{"TwentyFour"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"Monday":{"start":0,"end":2400}
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload map[string]any
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		hour := decodeHourExpanded(tb, payload)
		if got, _ := hour["id"].(string); got == "" {
			tb.Fatalf("expected hour.id in response")
		}

		hourID := hour["id"].(string)
		hourRec, err := app.FindRecordById("arcade_hour", hourID)
		if err != nil {
			tb.Fatalf("failed to load arcade_hour: %v", err)
		}

		var monday struct {
			Start int `json:"start"`
			End   int `json:"end"`
		}
		raw, _ := json.Marshal(hourRec.Get("Monday"))
		if err := json.Unmarshal(raw, &monday); err != nil {
			tb.Fatalf("failed to decode Monday hours: %v", err)
		}
		if monday.Start != 0 || monday.End != 2400 {
			tb.Fatalf("expected Monday {start:0,end:2400}, got %+v", monday)
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeHour_RejectsEqualStartEnd(t *testing.T) {
	headers := map[string]string{}

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/hour rejects equal start and end",
		Method:         http.MethodPut,
		URL:            "/arcade/hour",
		Headers:        headers,
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"validation failed"`,
			`"details":"Monday start and end must differ; use 499 for closed or 00:00-00:00 / 00:00-24:00 for 24-hour operation"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Equal Time Arcade",
			Address:  "Equal Street",
			Nickname: []string{"Equal"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"Monday":{"start":1000,"end":1000}
		}`, arcadeID))
	}

	scenario.Test(t)
}

func TestUpdateArcadeHour_RejectsInvalidClosedNumberWithGuidance(t *testing.T) {
	headers := map[string]string{}

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/hour rejects invalid closed number with guidance",
		Method:         http.MethodPut,
		URL:            "/arcade/hour",
		Headers:        headers,
		ExpectedStatus: http.StatusBadRequest,
		ExpectedContent: []string{
			`"error":"validation failed"`,
			`day hours must be 499 for closed, null for unknown`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp {
			return newArcadeTestApp(tb)
		},
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Invalid Closed Arcade",
			Address:  "Invalid Street",
			Nickname: []string{"Invalid"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"Monday":123
		}`, arcadeID))
	}

	scenario.Test(t)
}

func TestUpdateArcadeHour_WritesStructuredChangelogForUpdatedAndUnchanged(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/hour writes structured changelog",
		Method:         http.MethodPut,
		URL:            "/arcade/hour",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"hour":{"Friday":`,
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
			Name:     "Hour Change Arcade",
			Address:  "Hour Street",
			Nickname: []string{"Hour"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"Monday":{"start":900,"end":1800},
			"Note":"weekday"
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		changes := loadChangelogRecords(tb, app, arcadeID, "hour")
		if len(changes) != 1 {
			tb.Fatalf("expected 1 hour changelog row after first update, got %d", len(changes))
		}
		logObj := decodeLogObject(tb, changes[0].Get("log"))
		if got, _ := logObj["type"].(string); got != "hour_diff" {
			tb.Fatalf("expected changelog.log.type=hour_diff, got %v", logObj["type"])
		}
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected single hour log item, got %T %#v", logObj["items"], logObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected hour log item object, got %T", items[0])
		}
		if got, _ := item["change_type"].(string); got != "added" {
			tb.Fatalf("expected first hour change_type=added, got %v", item["change_type"])
		}
		diff, ok := item["diff"].([]any)
		if !ok || len(diff) != 2 {
			tb.Fatalf("expected 2 hour diff entries, got %T %#v", item["diff"], item["diff"])
		}

		res2 := executeJSONRequest(tb, app, http.MethodPut, "/arcade/hour", fmt.Sprintf(`{
			"arcade":"%s",
			"Monday":{"start":900,"end":1800},
			"Note":"weekday"
		}`, arcadeID), headers)
		defer res2.Body.Close()
		if res2.StatusCode != http.StatusOK {
			tb.Fatalf("expected second update status 200, got %d", res2.StatusCode)
		}

		changes = loadChangelogRecords(tb, app, arcadeID, "hour")
		if len(changes) != 2 {
			tb.Fatalf("expected 2 hour changelog rows after second update, got %d", len(changes))
		}
		logObj = decodeLogObject(tb, changes[0].Get("log"))
		items, ok = logObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected single latest hour log item, got %T %#v", logObj["items"], logObj["items"])
		}
		item, ok = items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected latest hour log item object, got %T", items[0])
		}
		if got, _ := item["change_type"].(string); got != "unchanged" {
			tb.Fatalf("expected second hour change_type=unchanged, got %v", item["change_type"])
		}
		bullets, ok := item["bullets"].([]any)
		if !ok || len(bullets) == 0 {
			tb.Fatalf("expected latest hour bullets, got %T %#v", item["bullets"], item["bullets"])
		}
		keys := i18nBulletKeySet(bullets)
		if !keys["arcade.changelog.hour.no_changes"] {
			tb.Fatalf("expected no_changes bullet key, got %#v", keys)
		}
		if _, exists := item["diff"]; exists {
			tb.Fatalf("expected unchanged hour item to omit diff field, got %#v", item["diff"])
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeHour_LogsNullToFullDayAsUpdated(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/hour logs null to full day as updated",
		Method:         http.MethodPut,
		URL:            "/arcade/hour",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"hour":{"Friday":`,
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
			Name:     "Null To Full Day Arcade",
			Address:  "Full Day Street",
			Nickname: []string{"FullDay"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		seedHourMolecule(tb, app, arcadeID, user.Id, map[string]any{})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"Thursday":{"start":0,"end":0}
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		changes := loadChangelogRecords(tb, app, arcadeID, "hour")
		if len(changes) != 1 {
			tb.Fatalf("expected 1 hour changelog row, got %d", len(changes))
		}
		logObj := decodeLogObject(tb, changes[0].Get("log"))
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected single hour log item, got %T %#v", logObj["items"], logObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected hour log item object, got %T", items[0])
		}
		if got, _ := item["change_type"].(string); got != "updated" {
			tb.Fatalf("expected change_type=updated, got %v", item["change_type"])
		}
		bullets, ok := item["bullets"].([]any)
		if !ok || len(bullets) == 0 {
			tb.Fatalf("expected hour bullets, got %T %#v", item["bullets"], item["bullets"])
		}
		keys := i18nBulletKeySet(bullets)
		if !keys["arcade.changelog.hour.thursday.changed"] {
			tb.Fatalf("expected thursday.changed bullet, got %#v", keys)
		}
		diff, ok := item["diff"].([]any)
		if !ok || len(diff) != 1 {
			tb.Fatalf("expected single diff entry, got %T %#v", item["diff"], item["diff"])
		}
		entry, ok := diff[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected diff entry object, got %T", diff[0])
		}
		if got, _ := entry["field"].(string); got != "Thursday" {
			tb.Fatalf("expected Thursday diff field, got %v", entry["field"])
		}
		if entry["from"] != nil {
			tb.Fatalf("expected Thursday diff from=nil, got %#v", entry["from"])
		}
		to, ok := entry["to"].(map[string]any)
		if !ok {
			tb.Fatalf("expected Thursday diff to object, got %T %#v", entry["to"], entry["to"])
		}
		if start, _ := to["start"].(float64); start != 0 {
			tb.Fatalf("expected Thursday diff to.start=0, got %#v", to["start"])
		}
		if end, _ := to["end"].(float64); end != 0 {
			tb.Fatalf("expected Thursday diff to.end=0, got %#v", to["end"])
		}
	}

	scenario.Test(t)
}

func TestUpdateArcadeHour_LogsNullToClosedAsUpdated(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string

	scenario := tests.ApiScenario{
		Name:           "PUT /arcade/hour logs null to closed as updated",
		Method:         http.MethodPut,
		URL:            "/arcade/hour",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"arcade":"`,
			`"hour":{"Friday":`,
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
			Name:     "Null To Closed Arcade",
			Address:  "Closed Street",
			Nickname: []string{"Closed"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		seedHourMolecule(tb, app, arcadeID, user.Id, map[string]any{})

		scenario.Body = strings.NewReader(fmt.Sprintf(`{
			"arcade":"%s",
			"Thursday":499
		}`, arcadeID))
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		changes := loadChangelogRecords(tb, app, arcadeID, "hour")
		if len(changes) != 1 {
			tb.Fatalf("expected 1 hour changelog row, got %d", len(changes))
		}
		logObj := decodeLogObject(tb, changes[0].Get("log"))
		items, ok := logObj["items"].([]any)
		if !ok || len(items) != 1 {
			tb.Fatalf("expected single hour log item, got %T %#v", logObj["items"], logObj["items"])
		}
		item, ok := items[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected hour log item object, got %T", items[0])
		}
		if got, _ := item["change_type"].(string); got != "updated" {
			tb.Fatalf("expected change_type=updated, got %v", item["change_type"])
		}
		diff, ok := item["diff"].([]any)
		if !ok || len(diff) != 1 {
			tb.Fatalf("expected single diff entry, got %T %#v", item["diff"], item["diff"])
		}
		entry, ok := diff[0].(map[string]any)
		if !ok {
			tb.Fatalf("expected diff entry object, got %T", diff[0])
		}
		if entry["from"] != nil {
			tb.Fatalf("expected Thursday diff from=nil, got %#v", entry["from"])
		}
		if got, _ := entry["to"].(float64); got != 499 {
			tb.Fatalf("expected Thursday diff to=499, got %#v", entry["to"])
		}
	}

		scenario.Test(t)
}

func TestGetArcadeValues_ExpandsHourWithSameShapeAsUpdateResponse(t *testing.T) {
	app := newArcadeTestApp(t)
	headers := map[string]string{}

	token, user := createAuthUser(t, app)
	headers["Authorization"] = "Bearer " + token

	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Expand Hour Arcade",
		Address:  "Expand Street",
		Nickname: []string{"Expand"},
		Location: location{Lat: 37.5665, Lon: 126.978},
	})

	updateRes := executeJSONRequest(t, app, http.MethodPut, "/arcade/hour", fmt.Sprintf(`{
		"arcade":"%s",
		"Monday":{"start":1000,"end":2300},
		"Sunday":499,
		"note":"Weekdays only"
	}`, arcadeID), headers)
	defer updateRes.Body.Close()
	if updateRes.StatusCode != http.StatusOK {
		t.Fatalf("expected hour update to succeed, got %d", updateRes.StatusCode)
	}

	var updatePayload map[string]any
	if err := json.NewDecoder(updateRes.Body).Decode(&updatePayload); err != nil {
		t.Fatalf("failed to decode update response: %v", err)
	}
	expectedHour := decodeHourExpanded(t, updatePayload)

	res := executeJSONRequest(t, app, http.MethodGet, "/arcade?id="+arcadeID+"&expand=hour", "", headers)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected get arcade to succeed, got %d", res.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		t.Fatalf("failed to decode get arcade response: %v", err)
	}

	hour := decodeHourExpanded(t, payload)
	if got, _ := hour["id"].(string); got == "" {
		t.Fatalf("expected expanded hour id")
	}
	if got, want := hour["id"].(string), expectedHour["id"].(string); got != want {
		t.Fatalf("expected same hour id as update response, got %q want %q", got, want)
	}
	if got, _ := hour["Monday"].(map[string]any); got == nil {
		t.Fatalf("expected Monday hours in expanded hour")
	}
	if got := hour["Sunday"]; got != float64(499) {
		t.Fatalf("expected Sunday=499, got %#v", got)
	}
	if got, _ := hour["Note"].(string); got != "Weekdays only" {
		t.Fatalf("expected Note %q, got %#v", "Weekdays only", got)
	}
}

func seedHourMolecule(tb testing.TB, app *tests.TestApp, arcadeID, createdBy string, fields map[string]any) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_hour")
	if err != nil {
		tb.Fatalf("failed to load arcade_hour collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	for field, value := range fields {
		rec.Set(field, value)
	}
	for _, field := range []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"} {
		if _, ok := fields[field]; !ok {
			rec.Set(field, nil)
		}
	}
	if createdBy != "" {
		rec.Set("createdBy", createdBy)
	}
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_hour record: %v", err)
	}

	arcadeRec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}
	arcadeRec.Set("hour", rec.Id)
	if err := app.Save(arcadeRec); err != nil {
		tb.Fatalf("failed to link arcade.hour: %v", err)
	}

	return rec.Id
}
