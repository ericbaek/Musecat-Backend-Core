package arcade_test

import (
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"net/http"
	"testing"
)

func TestUnifiedNotificationsLifecycleAndIsolation(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	authorToken, author := createAuthUserWithTags(t, app, []string{})
	actorToken, _ := createAuthUserWithTags(t, app, []string{})
	ownerToken, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	arcadeID, _ := seedPublicArcade(t, app, author.Id, arcadeSeed{Name: "Notification arcade", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{arcadeID})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	game := seedGameAtomForFlag(t, app, arcadeID)
	headers := func(token string) map[string]string { return map[string]string{"Authorization": "Bearer " + token} }
	call := func(method, path, body, token string, want int) map[string]any {
		t.Helper()
		res := executeJSONRequest(t, app, method, path, body, headers(token))
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s status=%d want=%d body=%v", method, path, res.StatusCode, want, out)
		}
		return out
	}
	x, y := elliptic.P256().ScalarBaseMult([]byte{1})
	key := base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y))
	auth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	deviceBody := func(endpoint string) string {
		return fmt.Sprintf(`{"subscription":{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q}},"locale":"en"}`, endpoint, key, auth)
	}
	endpoint := "https://fcm.googleapis.com/fcm/send/author"
	call("PUT", "/user/notifications/device", deviceBody(endpoint), authorToken, 200)
	call("PUT", "/user/notifications/device", deviceBody(endpoint+"-phone"), authorToken, 200)
	call("PUT", "/user/notifications/device", deviceBody(endpoint+"-owner"), ownerToken, 200)
	create := call("POST", "/arcade/flag", fmt.Sprintf(`{"arcade":%q,"game_id":%q,"disruption":"minor","message":"broken button"}`, arcadeID, game), authorToken, 200)
	flag := create["flag"].(string)
	manager := call("GET", "/user/notifications", "", ownerToken, 200)
	if manager["unread"] != float64(1) {
		t.Fatal("owner must receive an in-app notification", manager)
	}
	// Self-reactions never notify the reporter.
	react := fmt.Sprintf(`{"flag":%q,"reaction":"issue_persist","action":"add"}`, flag)
	call("POST", "/arcade/flag/reaction", react, authorToken, 200)
	if got := call("GET", "/user/notifications", "", authorToken, 200); got["unread"] != float64(0) {
		t.Fatal("self reaction notified", got)
	}
	call("POST", "/arcade/flag/reaction", react, actorToken, 200)
	inbox := call("GET", "/user/notifications", "", authorToken, 200)
	items := inbox["items"].([]any)
	if len(items) != 1 || inbox["unread"] != float64(1) {
		t.Fatal(inbox)
	}
	item := items[0].(map[string]any)
	id := item["id"].(string)
	if item["reaction"] != "issue_persist" || item["kind"] != "report_reaction" {
		t.Fatal(item)
	}
	if got := call("GET", "/user/notifications", "", actorToken, 200); got["unread"] != float64(0) {
		t.Fatal("another user saw notification", got)
	}
	// Forging a read id cannot change another account's inbox.
	call("POST", "/user/notifications/read", fmt.Sprintf(`{"id":%q}`, id), actorToken, 200)
	if got := call("GET", "/user/notifications", "", authorToken, 200); got["unread"] != float64(1) {
		t.Fatal("foreign read modified inbox")
	}
	rows, err := app.FindRecordsByFilter(usernotification.Deliveries, "notification={:id}", "", 0, 0, map[string]any{"id": id})
	if err != nil || len(rows) != 2 {
		t.Fatalf("reporter devices: %v %d", err, len(rows))
	}
	call("POST", "/user/notifications/read", fmt.Sprintf(`{"id":%q}`, id), authorToken, 200)
	if got := call("GET", "/user/notifications", "", authorToken, 200); got["unread"] != float64(0) {
		t.Fatal("read not persisted")
	}
	// Device state is per account; removal affects only that device and cancels pending deliveries.
	statusBody := fmt.Sprintf(`{"endpoint":%q}`, endpoint)
	if got := call("POST", "/user/notifications/device", statusBody, authorToken, 200); got["enabled"] != true {
		t.Fatal(got)
	}
	if got := call("POST", "/user/notifications/device", statusBody, actorToken, 200); got["enabled"] != false {
		t.Fatal("device leaked to other account")
	}
	// Locale sync changes only an already-registered device belonging to this account.
	localeBody := fmt.Sprintf(`{"endpoint":%q,"locale":"en"}`, endpoint)
	call("PATCH", "/user/notifications/device", localeBody, actorToken, 200)
	current, _ := app.FindFirstRecordByFilter(usernotification.Devices, "endpoint={:endpoint}", map[string]any{"endpoint": endpoint})
	if current.GetString("locale") != "en" {
		t.Fatal("foreign account changed device locale")
	}
	call("PATCH", "/user/notifications/device", fmt.Sprintf(`{"endpoint":%q,"locale":"ja"}`, endpoint), authorToken, 200)
	current, _ = app.FindRecordById(usernotification.Devices, current.Id)
	if current.GetString("locale") != "ja" {
		t.Fatal("device locale not updated")
	}
	unknown := call("PATCH", "/user/notifications/device", fmt.Sprintf(`{"endpoint":%q,"locale":"en"}`, endpoint+"-missing"), authorToken, 200)
	if unknown["enabled"] != false {
		t.Fatal("locale sync enabled a new device")
	}
	call("DELETE", "/user/notifications/device", statusBody, authorToken, 200)
	devices, _ := app.FindRecordsByFilter(usernotification.Devices, "user={:user}", "", 0, 0, map[string]any{"user": author.Id})
	if len(devices) != 1 {
		t.Fatal("other device removed")
	}
	// Retraction removes the reaction notification and its queue entries.
	call("POST", "/arcade/flag/reaction", fmt.Sprintf(`{"flag":%q,"reaction":"issue_persist","action":"delete"}`, flag), actorToken, 200)
	if got := call("GET", "/user/notifications", "", authorToken, 200); len(got["items"].([]any)) != 0 {
		t.Fatal("retracted notification remains")
	}
	res := executeJSONRequest(t, app, "GET", "/user/notifications", "", nil)
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatal("anonymous inbox access")
	}
}
