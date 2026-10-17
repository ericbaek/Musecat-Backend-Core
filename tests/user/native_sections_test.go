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
		if body != nil {
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
		var out map[string]any
		_ = json.Unmarshal(data, &out)
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
	// Empty relation tokens are explicit and permit a first save only once.
	empty := request("hours", "GET", nil, 200)
	if empty["revision"] != "" {
		t.Fatal(empty)
	}
	request("hours", "PATCH", map[string]any{"changes": map[string]any{"Monday": 499}}, 400)
	for _, value := range []any{500, map[string]any{"start": 900}, map[string]any{"start": 1260, "end": 2100}, map[string]any{"start": 900, "end": 900}, map[string]any{"start": 900, "end": 2100, "typo": true}} {
		request("hours", "PATCH", patch("", map[string]any{"Monday": value}), 400)
	}
	request("hours", "PATCH", patch("", map[string]any{"Monday": map[string]int{"start": 2200, "end": 200}, "Tuesday": 499, "Wednesday": nil, "Thursday": map[string]int{"start": 0, "end": 2400}, "note": "Keep this note"}), 200)
	first := request("hours", "GET", nil, 200)
	values := first["values"].(map[string]any)
	if values["Tuesday"] != float64(499) || values["Wednesday"] != nil || first["timezone"] != "Asia/Seoul" {
		t.Fatal(first)
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
	logs, xp := count("arcade_changelog"), count("user_level_log")
	request("hours", "PATCH", patch(first["revision"], map[string]any{"Friday": nil}), 409)
	request("hours", "PATCH", patch(after["revision"], map[string]any{"Friday": 499}), 400)
	if count("arcade_changelog") != logs || count("user_level_log") != xp {
		t.Fatal("failed hours write changed history/XP")
	}
	request("hours", "PATCH", patch(after["revision"], map[string]any{"Monday": map[string]int{"start": 2100, "end": 200}, "Tuesday": nil, "note": ""}), 200)
	after = request("hours", "GET", nil, 200)
	values = after["values"].(map[string]any)
	if values["note"] != "" || values["Tuesday"] != nil || values["Monday"].(map[string]any)["breaks"] == nil {
		t.Fatal("clear/forward-compatible object patch failed")
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
	logs, xp = count("arcade_changelog"), count("user_level_log")
	request("contacts", "PATCH", map[string]any{"revision": revision, "remove": []string{phoneID}}, 409)
	for _, change := range []map[string]any{{"link": "javascript:alert(1)"}, {"link": nil}, {"type": "website"}} {
		request("contacts", "PATCH", map[string]any{"revision": contacts["revision"], "update": []any{map[string]any{"id": phoneID, "changes": change}}}, 400)
	}
	request("contacts", "PATCH", map[string]any{"revision": contacts["revision"], "remove": []string{phoneID, phoneID}}, 400)
	if count("arcade_changelog") != logs || count("user_level_log") != xp {
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
