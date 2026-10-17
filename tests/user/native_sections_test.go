package user_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestNativeContactsAndHoursV1(t *testing.T) {
	app := newUserFetchTestApp(t)
	web, user := createAuthUser(t, app, true)
	clients := []nativeauth.Client{{ID: "ios", Callback: "musecat-test://auth/callback"}}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error { nativeauth.RegisterRoutes(e, clients); return e.Next() })
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	event := &core.ServeEvent{App: app, Router: router}
	if err = app.OnServe().Trigger(event, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(mux)
	defer server.Close()
	verifier := strings.Repeat("s", 43)
	hash := sha256.Sum256([]byte(verifier))
	service := nativeauth.Service{Clients: clients}
	code, err := service.IssueCode(app, user, "ios", clients[0].Callback, base64.RawURLEncoding.EncodeToString(hash[:]))
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.Exchange(app, code, verifier, "ios", clients[0].Callback)
	if err != nil {
		t.Fatal(err)
	}
	token := credentials.AccessToken
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	request := func(section, method string, body any, status int) map[string]any {
		t.Helper()
		var encoded []byte
		if raw, ok := body.([]byte); ok {
			encoded = raw
		} else if body != nil {
			encoded, _ = json.Marshal(body)
		}
		req, err := http.NewRequest(method, server.URL+"/native/v1/arcades/"+arcade.Id+"/"+section, strings.NewReader(string(encoded)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if response.StatusCode != status {
			t.Fatalf("%s %s got %d want %d: %s", method, section, response.StatusCode, status, data)
		}
		// Exactly one JSON document: a handler that keeps running after writing an
		// error would append a second body here.
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("%s %s %d returned a non-JSON body: %s", method, section, status, data)
		}
		return out
	}
	patch := func(revision any, changes any) map[string]any {
		return map[string]any{"revision": revision, "changes": changes}
	}
	count := func(collection string) int64 {
		n, err := app.CountRecords(collection)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Every native 400 uses the documented {error} envelope, including decode failures.
	rejected := func(section string, body any, code string) {
		t.Helper()
		if got := request(section, "PATCH", body, 400)["error"]; got != code {
			t.Fatalf("%s PATCH error %v want %s", section, got, code)
		}
	}
	// Empty relation tokens are explicit and permit a first save only once.
	empty := request("hours", "GET", nil, 200)
	if empty["revision"] != "" {
		t.Fatal(empty)
	}
	hours, logs, xp := count("arcade_hour"), count("arcade_changelog"), count("user_level_log")
	rejected("hours", map[string]any{"changes": map[string]any{"Monday": 499}}, "revision_and_changes_required")
	rejected("hours", patch("", map[string]any{}), "revision_and_changes_required")
	rejected("hours", []byte(`{"revision":"","changes":{"Monday":499}} {}`), "invalid_patch")
	rejected("hours", []byte(`{"revision":"","changes":{"Monday":499},"extra":1}`), "invalid_patch")
	rejected("hours", []byte(`{"revision":"","changes":{"Monday":499}`), "invalid_patch")
	rejected("hours", []byte(`not json`), "invalid_patch")
	rejected("hours", patch("", map[string]any{"Monday": 499, "Funday": 499}), "unsupported_field")
	for _, note := range []any{nil, 5, true, []string{"x"}, map[string]any{"text": "x"}} {
		rejected("hours", patch("", map[string]any{"note": note}), "invalid_note")
	}
	for _, value := range []any{500, map[string]any{"start": 900}, map[string]any{"start": 1260, "end": 2100}, map[string]any{"start": 900, "end": 900}, map[string]any{"start": 900, "end": 2100, "typo": true}} {
		rejected("hours", patch("", map[string]any{"Monday": value}), "invalid_day_hours")
	}
	if count("arcade_hour") != hours || count("arcade_changelog") != logs || count("user_level_log") != xp {
		t.Fatal("rejected hours request wrote state")
	}
	request("hours", "PATCH", patch("", map[string]any{"Monday": map[string]int{"start": 2200, "end": 200}, "Tuesday": 499, "Wednesday": nil, "Thursday": map[string]int{"start": 0, "end": 2400}, "note": "Keep this note"}), 200)
	first := request("hours", "GET", nil, 200)
	values := first["values"].(map[string]any)
	if values["Tuesday"] != float64(499) || values["Wednesday"] != nil || first["timezone"] != "Asia/Seoul" {
		t.Fatal(first)
	}
	// A whitespace-only note edit is a no-op: no revision, history, or XP.
	logs, xp = count("arcade_changelog"), count("user_level_log")
	rejected("hours", patch(first["revision"], map[string]any{"note": " Keep this note \n"}), "no_changes")
	if count("arcade_changelog") != logs || count("user_level_log") != xp || request("hours", "GET", nil, 200)["revision"] != first["revision"] {
		t.Fatal("whitespace-only note edit changed revision/history/XP")
	}
	// Simulate server fields and a future day representation unknown to this client.
	collection, _ := app.FindCollectionByNameOrId("arcade_hour")
	collection.Fields.Add(&core.TextField{Name: "future_policy"})
	if err = app.Save(collection); err != nil {
		t.Fatal(err)
	}
	stored, _ := app.FindRecordById("arcade_hour", first["revision"].(string))
	stored.Set("future_policy", "preserve")
	stored.Set("Sunday", map[string]any{"intervals": []any{map[string]int{"start": 1000, "end": 1200}}})
	stored.Set("Monday", map[string]any{"start": 2200, "end": 200, "breaks": []any{map[string]int{"start": 2300, "end": 2330}}})
	if err = app.Save(stored); err != nil {
		t.Fatal(err)
	}
	before := request("hours", "GET", nil, 200)
	request("hours", "PATCH", patch(first["revision"], map[string]any{"Friday": 499}), 200)
	after := request("hours", "GET", nil, 200)
	for key, value := range before["values"].(map[string]any) {
		if key != "Friday" && !reflect.DeepEqual(value, after["values"].(map[string]any)[key]) {
			t.Fatalf("lost omitted day/note %s", key)
		}
	}
	current, _ := app.FindRecordById("arcade_hour", after["revision"].(string))
	if current.GetString("future_policy") != "preserve" {
		t.Fatal("lost future column")
	}
	logs, xp = count("arcade_changelog"), count("user_level_log")
	if got := request("hours", "PATCH", patch(first["revision"], map[string]any{"Friday": nil}), 409)["error"]; got != "revision_conflict" {
		t.Fatal(got)
	}
	rejected("hours", patch(after["revision"], map[string]any{"Friday": 499}), "no_changes")
	// An open-time patch on a future day shape fails closed: neither a hybrid
	// object nor a silent replacement. Explicit null/499 remain deliberate replacements.
	rejected("hours", patch(after["revision"], map[string]any{"Sunday": map[string]int{"start": 1000, "end": 1800}}), "unsupported_day_shape")
	rejected("hours", patch(after["revision"], map[string]any{"Sunday": map[string]int{"start": 1000, "end": 1800}, "Friday": nil}), "unsupported_day_shape")
	if count("arcade_changelog") != logs || count("user_level_log") != xp {
		t.Fatal("failed hours write changed history/XP")
	}
	if got := request("hours", "GET", nil, 200)["values"].(map[string]any)["Sunday"].(map[string]any); got["start"] != nil || got["intervals"] == nil {
		t.Fatal("future day shape was altered by a rejected patch")
	}
	request("hours", "PATCH", patch(after["revision"], map[string]any{"Monday": map[string]int{"start": 2100, "end": 200}, "Tuesday": nil, "Sunday": 499, "note": ""}), 200)
	after = request("hours", "GET", nil, 200)
	values = after["values"].(map[string]any)
	if values["note"] != "" || values["Tuesday"] != nil || values["Sunday"] != float64(499) || values["Monday"].(map[string]any)["breaks"] == nil {
		t.Fatal("clear/forward-compatible object patch failed")
	}
	request("hours", "PATCH", patch(after["revision"], map[string]any{"Sunday": map[string]int{"start": 1000, "end": 1800}, "note": "  Trimmed note \t"}), 200)
	after = request("hours", "GET", nil, 200)
	values = after["values"].(map[string]any)
	if values["note"] != "Trimmed note" || !reflect.DeepEqual(values["Sunday"], map[string]any{"start": float64(1000), "end": float64(1800)}) {
		t.Fatal("closed day reopen or note trim failed")
	}
	// A hour revision pointing at another arcade is neither exposed nor copied.
	other := seedVisitArcade(t, app, "Asia/Seoul")
	foreign := core.NewRecord(collection)
	foreign.Set("arcade", other.Id)
	foreign.Set("Monday", 499)
	foreign.Set("Note", "foreign")
	if err = app.Save(foreign); err != nil {
		t.Fatal(err)
	}
	arcade, _ = app.FindRecordById("arcade", arcade.Id)
	arcade.Set("hour", foreign.Id)
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	hours, logs, xp = count("arcade_hour"), count("arcade_changelog"), count("user_level_log")
	request("hours", "GET", nil, 500)
	if got := request("hours", "PATCH", patch(foreign.Id, map[string]any{"Friday": 499}), 502)["error"]; got != "hours_unavailable" {
		t.Fatal(got)
	}
	if count("arcade_changelog") != logs || count("user_level_log") != xp || count("arcade_hour") != hours {
		t.Fatal("mispointed hour revision was copied into a new revision")
	}
	arcade.Set("hour", after["revision"])
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	if request("hours", "GET", nil, 200)["revision"] != after["revision"] {
		t.Fatal("hour relation restore failed")
	}
	// Contacts use explicit identities and operations, never an entire replacement list.
	request("contacts", "PATCH", map[string]any{"revision": "", "add": []any{map[string]any{"type": "phone", "link": "+82 (10) 1234-5678", "name": "Main"}, map[string]any{"type": "website", "link": "https://example.com", "name": "Site"}}}, 200)
	contacts := request("contacts", "GET", nil, 200)
	items := contacts["items"].([]any)
	var phoneID string
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["type"] == "phone" {
			phoneID = item["id"].(string)
			if item["link"] != "+821012345678" {
				t.Fatal(item)
			}
		}
	}
	atomCollection, _ := app.FindCollectionByNameOrId("arcade_sns_atoms")
	atomCollection.Fields.Add(&core.TextField{Name: "future_policy"})
	selectType := atomCollection.Fields.GetByName("type").(*core.SelectField)
	selectType.Values = append(selectType.Values, "future_messenger")
	if err = app.Save(atomCollection); err != nil {
		t.Fatal(err)
	}
	phone, _ := app.FindRecordById("arcade_sns_atoms", phoneID)
	phone.Set("future_policy", "phone metadata")
	if err = app.Save(phone); err != nil {
		t.Fatal(err)
	}
	// Unknown future type is invisible to an old editor, but must survive its save.
	future := core.NewRecord(atomCollection)
	future.Set("molecule", contacts["revision"])
	future.Set("type", "future_messenger")
	future.Set("link", "https://future.example.com")
	future.Set("future_policy", "future metadata")
	if err = app.Save(future); err != nil {
		t.Fatal(err)
	}
	revision := contacts["revision"]
	request("contacts", "PATCH", map[string]any{"revision": revision, "update": []any{map[string]any{"id": phoneID, "changes": map[string]any{"name": "Renamed"}}}}, 200)
	contacts = request("contacts", "GET", nil, 200)
	items = contacts["items"].([]any)
	if len(items) != 3 {
		t.Fatal("old client removed unknown contact")
	}
	for _, raw := range items {
		item := raw.(map[string]any)
		rec, _ := app.FindRecordById("arcade_sns_atoms", item["id"].(string))
		if item["type"] == "phone" {
			phoneID = item["id"].(string)
			if item["name"] != "Renamed" || item["link"] != "+821012345678" || rec.GetString("future_policy") != "phone metadata" {
				t.Fatal("phone fields lost")
			}
		}
		if item["type"] == "future_messenger" && rec.GetString("future_policy") != "future metadata" {
			t.Fatal("future contact lost")
		}
	}
	// A phone link update stores the normalized phone value and clears link.
	request("contacts", "PATCH", map[string]any{"revision": contacts["revision"], "update": []any{map[string]any{"id": phoneID, "changes": map[string]any{"link": " +82 (10) 9999-8888 "}}}}, 200)
	contacts = request("contacts", "GET", nil, 200)
	phoneSeen := false
	for _, raw := range contacts["items"].([]any) {
		item := raw.(map[string]any)
		if item["type"] != "phone" {
			continue
		}
		phoneSeen = true
		phoneID = item["id"].(string)
		rec, _ := app.FindRecordById("arcade_sns_atoms", phoneID)
		if item["link"] != "+821099998888" || item["name"] != "Renamed" || rec.GetString("phone") != "+821099998888" || rec.GetString("link") != "" || rec.GetString("future_policy") != "phone metadata" {
			t.Fatalf("phone link update failed: %v phone=%q link=%q", item, rec.GetString("phone"), rec.GetString("link"))
		}
	}
	if !phoneSeen || len(contacts["items"].([]any)) != 3 {
		t.Fatal(contacts)
	}
	logs, xp = count("arcade_changelog"), count("user_level_log")
	if got := request("contacts", "PATCH", map[string]any{"revision": revision, "remove": []string{phoneID}}, 409)["error"]; got != "revision_conflict" {
		t.Fatal(got)
	}
	for change, code := range map[string]string{`{"link":"javascript:alert(1)"}`: "invalid_contact_link", `{"link":"12345"}`: "invalid_contact_link", `{"link":null}`: "invalid_contact_value", `{"name":7}`: "invalid_contact_value", `{"type":"website"}`: "unsupported_field", `{}`: "invalid_contact_id"} {
		rejected("contacts", []byte(`{"revision":"`+contacts["revision"].(string)+`","update":[{"id":"`+phoneID+`","changes":`+change+`}]}`), code)
	}
	rejected("contacts", map[string]any{"revision": contacts["revision"], "remove": []string{phoneID, phoneID}}, "invalid_contact_id")
	rejected("contacts", map[string]any{"revision": contacts["revision"], "remove": []string{"missing"}}, "invalid_contact_id")
	rejected("contacts", map[string]any{"revision": contacts["revision"]}, "revision_and_operations_required")
	rejected("contacts", map[string]any{"add": []any{map[string]any{"type": "website", "link": "https://example.org"}}}, "revision_and_operations_required")
	rejected("contacts", []byte(`{"revision":"`+contacts["revision"].(string)+`","remove":["`+phoneID+`"]} {}`), "invalid_patch")
	rejected("contacts", []byte(`{"revision":"`+contacts["revision"].(string)+`","add":[{"type":"website","link":"https://example.org","extra":1}]}`), "invalid_patch")
	rejected("contacts", []byte(`{"revision":"`+contacts["revision"].(string)+`","remove":["`+phoneID+`"]`), "invalid_patch")
	// Native v1 cannot create a type it does not know, even when the schema already allows it.
	rejected("contacts", map[string]any{"revision": contacts["revision"], "add": []any{map[string]any{"type": "future_messenger", "link": "https://future.example.com"}}}, "unsupported_contact_type")
	rejected("contacts", map[string]any{"revision": contacts["revision"], "add": []any{map[string]any{"type": "instagram", "link": "https://example.com/profile"}}}, "invalid_contact_link")
	rejected("contacts", map[string]any{"revision": contacts["revision"], "add": []any{map[string]any{"type": "phone", "link": "12"}}}, "invalid_contact_link")
	if count("arcade_changelog") != logs || count("user_level_log") != xp || request("contacts", "GET", nil, 200)["revision"] != contacts["revision"] {
		t.Fatal("failed contact write changed history/XP")
	}
	request("contacts", "PATCH", map[string]any{"revision": contacts["revision"], "remove": []string{phoneID}}, 200)
	if len(request("contacts", "GET", nil, 200)["items"].([]any)) != 2 {
		t.Fatal("explicit removal failed")
	}
	token = web
	request("contacts", "GET", nil, 401)
	request("hours", "GET", nil, 401)
	token = credentials.AccessToken
	arcade, _ = app.FindRecordById("arcade", arcade.Id)
	arcade.Set("public", false)
	if err = app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	request("contacts", "GET", nil, 403)
	request("hours", "GET", nil, 403)
	request("contacts", "PATCH", map[string]any{"revision": "", "remove": []string{phoneID}}, 403)
	request("hours", "PATCH", patch("", map[string]any{"Monday": 499}), 403)
}
