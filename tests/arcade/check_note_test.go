package arcade_test

import (
	"encoding/json"
	"github.com/ericbaek/musecat-backend-core/service/arcadechecknote"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestCheckNoteSharedLifecycleAndAuthorization(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{Name: "Check notes", Address: "1 Check St", Location: location{Lat: 37.5, Lon: 127}})
	token, author := createAuthUserWithTags(t, app, nil)
	visitorToken, _ := createAuthUserWithTags(t, app, nil)
	call := func(method, auth string, value map[string]any) *http.Response {
		t.Helper()
		b, _ := json.Marshal(value)
		h := map[string]string{}
		if auth != "" {
			h["Authorization"] = "Bearer " + auth
		}
		return executeJSONRequest(t, app, method, "/arcade/check-notes", string(b), h)
	}
	input := map[string]any{"arcade": arcadeID, "message": "Please check the quantity."}
	unauth := call(http.MethodPost, "", input)
	if unauth.StatusCode != 401 {
		t.Fatalf("anonymous write: %d", unauth.StatusCode)
	}
	unauth.Body.Close()
	created := call(http.MethodPost, token, input)
	if created.StatusCode != 200 {
		t.Fatalf("create: %d", created.StatusCode)
	}
	note := decodeJSONMap(t, created)
	id := note["id"].(string)
	again := call(http.MethodPost, token, input)
	if got := decodeJSONMap(t, again)["id"]; got != id {
		t.Fatalf("retry duplicated note: %v", got)
	}
	read := executeJSONRequest(t, app, http.MethodGet, "/arcade/check-notes?arcade="+arcadeID, "", nil)
	items := decodeJSONMap(t, read)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("shared read: %v", items)
	}
	forbidden := call(http.MethodDelete, visitorToken, map[string]any{"arcade": arcadeID, "id": id})
	if forbidden.StatusCode != 403 {
		t.Fatalf("other user's delete: %d", forbidden.StatusCode)
	}
	forbidden.Body.Close()
	blank := call(http.MethodPut, visitorToken, map[string]any{"arcade": arcadeID, "id": id, "answer": " "})
	if blank.StatusCode != 400 {
		t.Fatalf("blank answer: %d", blank.StatusCode)
	}
	blank.Body.Close()
	for _, fix := range []map[string]any{
		{"lat": 38.5, "lon": 127.0, "accuracy": 10, "captured_at": time.Now().UnixMilli()},
		{"lat": 37.5, "lon": 127.0, "accuracy": 151, "captured_at": time.Now().UnixMilli()},
		{"lat": 37.5, "lon": 127.0, "accuracy": 10, "captured_at": time.Now().Add(-2 * time.Minute).UnixMilli()},
	} {
		fix["arcade"] = arcadeID
		fix["id"] = id
		fix["answer"] = "There are three."
		denied := call(http.MethodPut, visitorToken, fix)
		if denied.StatusCode != 400 && denied.StatusCode != 403 {
			t.Fatalf("unverified completion: %d", denied.StatusCode)
		}
		denied.Body.Close()
	}
	done := call(http.MethodPut, visitorToken, map[string]any{"arcade": arcadeID, "id": id, "answer": "There are three.", "lat": 37.502, "lon": 127.0, "accuracy": 10, "captured_at": time.Now().UnixMilli()})
	if done.StatusCode != 200 {
		t.Fatalf("answer: %d", done.StatusCode)
	}
	answer := decodeJSONMap(t, done)
	if answer["completed"] != true || answer["answer"] != "There are three." {
		t.Fatalf("answer not persisted: %v", answer)
	}
	conflict := call(http.MethodPut, token, map[string]any{"arcade": arcadeID, "id": id, "answer": "Overwrite"})
	if conflict.StatusCode != 409 {
		t.Fatalf("completed overwrite: %d", conflict.StatusCode)
	}
	conflict.Body.Close()
	if err := arcadechecknote.EnsureSchema(app); err != nil {
		t.Fatal(err)
	}
	stored, err := app.FindRecordById(arcadechecknote.Collection, id)
	if err != nil || stored.GetString("author") != author.Id || stored.GetString("answer") != "There are three." {
		t.Fatalf("rerun lost notes: %v", err)
	}
	// A private draft cannot be exposed by either list or write endpoints.
	arcade, _ := app.FindRecordById("arcade", arcadeID)
	arcade.Set("public", false)
	arcade.Set("createdBy", author.Id)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	private := executeJSONRequest(t, app, http.MethodGet, "/arcade/check-notes?arcade="+arcadeID, "", nil)
	if private.StatusCode != 404 {
		t.Fatalf("private read leak: %d", private.StatusCode)
	}
	private.Body.Close()
	blocked := call(http.MethodPost, visitorToken, input)
	if blocked.StatusCode != 404 {
		t.Fatalf("private write: %d", blocked.StatusCode)
	}
	blocked.Body.Close()
	wrong := call(http.MethodPost, token, map[string]any{"arcade": arcadeID, "message": strings.Repeat("a", 1001)})
	if wrong.StatusCode != 400 {
		t.Fatalf("oversize message: %d", wrong.StatusCode)
	}
	wrong.Body.Close()
	removed := call(http.MethodDelete, token, map[string]any{"arcade": arcadeID, "id": id})
	if removed.StatusCode != 204 {
		t.Fatalf("author delete: %d", removed.StatusCode)
	}
	removed.Body.Close()
}
