package arcade_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestInquiryLoginRequiredAndGuestRouting(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	token, user := createAuthUserWithTags(t, app, []string{})
	ownerToken, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	staffToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	arcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "Guest inquiry", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{arcadeID})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, auth string, want int) map[string]any {
		t.Helper()
		headers := map[string]string{}
		if auth != "" {
			headers["Authorization"] = "Bearer " + auth
		}
		res := executeJSONRequest(t, app, method, path, body, headers)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d want %d %v", method, path, res.StatusCode, want, out)
		}
		return out
	}
	readFlag := func(want bool) {
		t.Helper()
		detail := call("GET", "/arcade?id="+arcadeID, "", "", 200)
		if detail["admin"].(map[string]any)["inquirySettings"].(map[string]any)["loginRequired"] != want {
			t.Fatal("public capability missing or wrong", detail["admin"])
		}
	}
	readFlag(false)
	call("PUT", "/arcade/inquiry-settings", fmt.Sprintf(`{"arcade":%q,"mode":"internal"}`, arcadeID), ownerToken, 200)
	body := fmt.Sprintf(`{"arcade":%q,"message":"Guest inquiry"}`, arcadeID)
	guest := call("POST", "/support_feedback", body, "", 200)
	id := guest["id"].(string)
	if guest["createdBy"] != "" {
		t.Fatal("guest author fabricated", guest)
	}
	call("GET", "/user/feedback/thread?id="+id, "", "", 401)
	call("GET", "/user/feedback/thread?id="+id, "", token, 404)
	for _, auth := range []string{ownerToken, staffToken} {
		thread := call("GET", "/user/feedback/thread?id="+id, "", auth, 200)
		if thread["feedback"].(map[string]any)["createdBy"] != "" {
			t.Fatal("guest history not preserved")
		}
	}
	if inbox := call("GET", "/user/notifications", "", ownerToken, 200); inbox["unread"] != float64(1) {
		t.Fatal("guest inquiry did not notify official", inbox)
	}
	if list := call("GET", "/user/feedback?arcade="+arcadeID, "", ownerToken, 200); len(list["items"].([]any)) != 1 {
		t.Fatal("guest inquiry missing owner dashboard", list)
	}
	setting := fmt.Sprintf(`{"arcade":%q,"mode":"internal","loginRequired":true}`, arcadeID)
	call("PUT", "/arcade/inquiry-settings", setting, token, 403)
	saved := call("PUT", "/arcade/inquiry-settings", setting, ownerToken, 200)
	if saved["loginRequired"] != true {
		t.Fatal("flag not persisted", saved)
	}
	readFlag(true)
	rejected := call("POST", "/support_feedback", body, "", 401)
	if strings.TrimSuffix(rejected["message"].(string), ".") != "LOGIN_REQUIRED" {
		t.Fatal("missing login reason", rejected)
	}
	call("POST", "/support_feedback", body, token, 200)
	// Changing reception policy never locks responders out of historic guest inquiries.
	call("POST", "/user/feedback/thread", fmt.Sprintf(`{"feedbackId":%q,"message":"Official reply"}`, id), ownerToken, 201)
	call("PATCH", "/user/feedback/thread", fmt.Sprintf(`{"feedbackId":%q,"status":"solved"}`, id), staffToken, 200)
	external := fmt.Sprintf(`{"arcade":%q,"mode":"external","contactType":"url","contactValue":"https://example.com/contact","loginRequired":true}`, arcadeID)
	call("PUT", "/arcade/inquiry-settings", external, ownerToken, 200)
	call("POST", "/support_feedback", body, "", 409)
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	// With no current official account, retained owner settings do not block moderator contact.
	call("POST", "/support_feedback", body, "", 200)
	arcade, _ := app.FindRecordById("arcade", arcadeID)
	arcade.Set("public", false)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	call("POST", "/support_feedback", body, "", 404)
}
