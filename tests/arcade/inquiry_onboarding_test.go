package arcade_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
)

func TestInquiryDefaultOffAndOwnerOnboarding(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	token, user := createAuthUserWithTags(t, app, []string{})
	ownerToken, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	staffToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	foreignToken, _ := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	id, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "Owned private arcade", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{id})
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
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d want %d %v", method, path, res.StatusCode, want, out)
		}
		return out
	}
	onboarding := func(auth string) []any {
		return call("GET", "/user/arcade-inquiry-onboarding", "", auth, 200)["items"].([]any)
	}
	call("GET", "/user/arcade-inquiry-onboarding", "", "", 401)
	for _, auth := range []string{token, staffToken, foreignToken} {
		if len(onboarding(auth)) != 0 {
			t.Fatal("foreign onboarding leaked")
		}
	}
	arcade, _ := app.FindRecordById("arcade", id)
	for _, stored := range []any{nil, map[string]any{}, map[string]any{"mode": "invalid", "configured": true}} {
		arcade.Set("inquiry_settings", stored)
		if err := app.Save(arcade); err != nil {
			t.Fatal(err)
		}
		settings := supportfeedback.InquirySettings(arcade)
		if settings.Mode != "disabled" || settings.Configured {
			t.Fatal("invalid default", settings)
		}
		if len(onboarding(ownerToken)) != 1 {
			t.Fatal("unconfigured arcade missing")
		}
	}
	body := fmt.Sprintf(`{"arcade":%q,"message":"Inquiry"}`, id)
	rejected := call("POST", "/support_feedback", body, token, 409)
	if strings.TrimSuffix(rejected["message"].(string), ".") != "INQUIRY_DISABLED" {
		t.Fatal(rejected)
	}
	// Private owned names are scoped to the current owner only.
	arcade.Set("public", false)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	item := onboarding(ownerToken)[0].(map[string]any)
	if item["reportSettings"].(map[string]any)["mode"] != "internal" {
		t.Fatal("report settings missing from owner setup", item)
	}
	if item["arcadeName"] != "Owned private arcade" || item["settings"].(map[string]any)["configured"] != false {
		t.Fatal(item)
	}
	for _, mode := range []string{"internal", "external", "disabled"} {
		setting := fmt.Sprintf(`{"arcade":%q,"mode":%q,"configured":false,"contactType":"url","contactValue":"https://example.com"}`, id, mode)
		if saved := call("PUT", "/arcade/inquiry-settings", setting, ownerToken, 200); saved["configured"] != true {
			t.Fatal(saved)
		}
		if len(onboarding(ownerToken)) != 1 {
			t.Fatal("configured arcade missing from setup")
		}
		arcade, _ = app.FindRecordById("arcade", id)
		if strings.Contains(arcade.GetString("inquiry_settings"), "configured") {
			t.Fatal("derived flag persisted")
		}
	}
	// OFF accepts no external contact and never prevents access to existing threads.
	call("PUT", "/arcade/inquiry-settings", fmt.Sprintf(`{"arcade":%q,"mode":"internal"}`, id), ownerToken, 200)
	thread := call("POST", "/support_feedback", body, ownerToken, 200)
	call("PUT", "/arcade/inquiry-settings", fmt.Sprintf(`{"arcade":%q,"mode":"disabled"}`, id), ownerToken, 200)
	call("GET", "/user/feedback/thread?id="+thread["id"].(string), "", ownerToken, 200)
	arcade.Set("public", true)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	if len(onboarding(ownerToken)) != 0 {
		t.Fatal("revoked ownership retained")
	}
	call("POST", "/support_feedback", body, "", 200)
}
