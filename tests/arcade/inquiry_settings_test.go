package arcade_test

import (
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"testing"
)

func TestInquirySettingsRoutingPermissionsAndHistory(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	userToken, user := createAuthUserWithTags(t, app, []string{})
	ownerToken, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	foreignToken, _ := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	staffToken, _ := createAuthUserWithTags(t, app, []string{"developer"})
	arcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "Inquiry routing", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{arcadeID})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, token string, want int) map[string]any {
		t.Helper()
		headers := map[string]string{"Authorization": "Bearer " + token}
		res := executeJSONRequest(t, app, method, path, body, headers)
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d want %d %v", method, path, res.StatusCode, want, out)
		}
		return out
	}
	call("PUT", "/arcade/inquiry-settings", fmt.Sprintf(`{"arcade":%q,"mode":"internal"}`, arcadeID), ownerToken, 200)
	body := fmt.Sprintf(`{"arcade":%q,"message":"Original inquiry"}`, arcadeID)
	inquiry := call("POST", "/support_feedback", body, userToken, 200)
	id := inquiry["id"].(string)
	setting := func(mode, typ, value string) string {
		return fmt.Sprintf(`{"arcade":%q,"mode":%q,"contactType":%q,"contactValue":%q,"guide":"Contact the arcade"}`, arcadeID, mode, typ, value)
	}
	external := setting("external", "url", "https://example.com/contact")
	call("PUT", "/arcade/inquiry-settings", external, userToken, 403)
	call("PUT", "/arcade/inquiry-settings", external, foreignToken, 403)
	call("PUT", "/arcade/inquiry-settings", setting("external", "url", "javascript:alert(1)"), ownerToken, 400)
	call("PUT", "/arcade/inquiry-settings", setting("external", "phone", ""), ownerToken, 400)
	call("PUT", "/arcade/inquiry-settings", external, ownerToken, 200)
	call("POST", "/support_feedback", body, userToken, 409)
	call("GET", "/user/feedback/thread?id="+id, "", ownerToken, 200)
	call("POST", "/user/feedback/thread", fmt.Sprintf(`{"feedbackId":%q,"message":"Existing reply"}`, id), ownerToken, 201)
	call("PUT", "/arcade/inquiry-settings", setting("internal", "url", ""), staffToken, 200)
	call("POST", "/support_feedback", body, userToken, 200)
	call("PUT", "/arcade/inquiry-settings", external, ownerToken, 200)
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	call("POST", "/support_feedback", body, userToken, 200)
	if err := supportfeedback.EnsureInquirySettingsSchema(app); err != nil {
		t.Fatal(err)
	}
	arcade, _ := app.FindRecordById("arcade", arcadeID)
	if supportfeedback.InquirySettings(arcade).ContactValue != "https://example.com/contact" {
		t.Fatal("migration lost configuration")
	}
}
