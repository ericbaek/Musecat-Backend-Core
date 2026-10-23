package user_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	usernotifications "github.com/ericbaek/musecat-backend-core/handlers/user/notifications"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestNativeAPNSRegistrationScopeTransferAndDisabledProvider(t *testing.T) {
	app := newUserFetchTestApp(t)
	web, author := createAuthUser(t, app, true)
	_, other := createAuthUser(t, app, true)
	clients := []nativeauth.Client{{ID: "ios", Callback: "musecat-test://auth/callback"}}
	config := usernotifications.NativeAPNSConfig{Enabled: true, Topic: "test.invalid.native", Environment: "sandbox"}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		usernotifications.RegisterNativeAPNSRoutes(coreapp.RegisterNativeAPIRoutes(e, clients), config)
		return e.Next()
	})
	service := nativeauth.Service{Clients: clients}
	issue := func(user *core.Record) nativeauth.Credentials {
		t.Helper()
		verifier := strings.Repeat("a", 43)
		digest := sha256.Sum256([]byte(verifier))
		code, err := service.IssueCode(app, user, "ios", clients[0].Callback, base64.RawURLEncoding.EncodeToString(digest[:]))
		if err != nil {
			t.Fatal(err)
		}
		out, err := service.Exchange(app, code, verifier, "ios", clients[0].Callback)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	ownerCredentials, otherCredentials := issue(author), issue(other)
	call := func(method, token, body string, status int) map[string]any {
		t.Helper()
		headers := map[string]string{"Content-Type": "application/json"}
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		response := doUserRequest(t, app, method, "/native/user/notifications/apns", headers, body)
		defer response.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(response.Body).Decode(&out)
		if response.StatusCode != status {
			t.Fatalf("%s got %d want%d: %v", method, response.StatusCode, status, out)
		}
		if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("APNs response cacheable")
		}
		return out
	}
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		for _, denied := range []string{"", web, "partner-token"} {
			call(method, denied, `{}`, 401)
		}
	}
	capability := call("GET", ownerCredentials.AccessToken, "", 200)
	if capability["available"] != true || capability["topic"] != config.Topic {
		t.Fatal(capability)
	}
	token := strings.Repeat("AB", 32)
	body := fmt.Sprintf(`{"token":%q,"locale":"ja","user":%q,"session":"client-controlled","topic":"evil.invalid","environment":"production"}`, token, other.Id)
	registered := call("PUT", ownerCredentials.AccessToken, body, 200)
	id := registered["device"].(string)
	if registered["enabled"] != true || registered["token"] != nil {
		t.Fatal(registered)
	}
	device, err := app.FindRecordById(usernotification.Devices, id)
	if err != nil {
		t.Fatal(err)
	}
	stored, valid := usernotification.DecodeAPNSDevice(device)
	_, session, _ := service.AuthenticateSession(app, ownerCredentials.AccessToken)
	if !valid || device.GetString("user") != author.Id || stored.Token != strings.ToLower(token) || stored.Environment != "sandbox" || stored.Topic != config.Topic || stored.Session != session || device.GetString("locale") != "ja" {
		t.Fatal("registration trusted caller-owned scope")
	}
	if repeated := call("PUT", ownerCredentials.AccessToken, body, 200); repeated["device"] != id {
		t.Fatal("registration replay duplicated device")
	}
	for _, bad := range []string{`{"token":"bad-token","locale":"en"}`, `{"token":"a","locale":"ko"}`, `{"token":"aa","locale":"xx"}`} {
		call("PUT", ownerCredentials.AccessToken, bad, 400)
	}
	call("PUT", ownerCredentials.AccessToken, `{"token":"aa","locale":"en","padding":"`+strings.Repeat("x", 4096)+`"}`, 413)
	// Seed a queued event and preference to prove account transfer cascades
	// pending private work rather than reassigning it to the next account.
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	author.Set("tags", []string{"arcade_owner"})
	author.Set("owns", []string{arcade.Id})
	if err := app.Save(author); err != nil {
		t.Fatal(err)
	}
	preferenceBody := fmt.Sprintf(`{"arcade":%q,"device":%q}`, arcade.Id, id)
	prefCall := func(method, path, credential string, status int) map[string]any {
		t.Helper()
		res := doUserRequest(t, app, method, "/native/arcade/apns-subscription"+path, map[string]string{"Authorization": "Bearer " + credential, "Content-Type": "application/json"}, preferenceBody)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != status {
			t.Fatalf("native preference %s got%d want%d: %v", method, res.StatusCode, status, out)
		}
		return out
	}
	if out := prefCall("POST", "/status", ownerCredentials.AccessToken, 200); out["enabled"] != true {
		t.Fatal("owner delivery default lost", out)
	}
	prefCall("PUT", "", otherCredentials.AccessToken, 404)
	prefCall("DELETE", "", otherCredentials.AccessToken, 404)
	prefCall("DELETE", "", ownerCredentials.AccessToken, 200)
	if out := prefCall("POST", "/status", ownerCredentials.AccessToken, 200); out["enabled"] != false {
		t.Fatal("explicit opt-out ignored", out)
	}
	prefCall("PUT", "", ownerCredentials.AccessToken, 200)
	makeRecord := func(name string, values map[string]any) *core.Record {
		t.Helper()
		collection, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatal(err)
		}
		record := core.NewRecord(collection)
		for key, value := range values {
			record.Set(key, value)
		}
		if err := app.Save(record); err != nil {
			t.Fatal(err)
		}
		return record
	}
	notification := makeRecord(usernotification.Inbox, map[string]any{"user": author.Id, "kind": "arcade_setup", "event_key": "local-native-apns"})
	receipt := makeRecord(usernotification.Deliveries, map[string]any{"notification": notification.Id, "device": id})
	pref, err := app.FindFirstRecordByFilter(arcadereport.Subscriptions, "arcade={:arcade} && device={:device}", dbx.Params{"arcade": arcade.Id, "device": id})
	if err != nil {
		t.Fatal(err)
	}
	reportEvent := makeRecord(usernotification.Inbox, map[string]any{"user": author.Id, "kind": "report_created", "event_key": "local-native-report", "arcade": arcade.Id})
	reportReceipt := makeRecord(usernotification.Deliveries, map[string]any{"notification": reportEvent.Id, "device": id})
	prefCall("DELETE", "", ownerCredentials.AccessToken, 200)
	reportReceipt, _ = app.FindRecordById(usernotification.Deliveries, reportReceipt.Id)
	unchangedReceipt, _ := app.FindRecordById(usernotification.Deliveries, receipt.Id)
	if !reportReceipt.GetBool("done") || unchangedReceipt.GetBool("done") {
		t.Fatal("opt-out did not cancel only this arcade's queued report delivery")
	}
	rotated := call("PUT", ownerCredentials.AccessToken, fmt.Sprintf(`{"token":%q,"locale":"en"}`, strings.Repeat("cd", 48)), 200)
	if rotated["device"] != id {
		t.Fatal("token rotation replaced the session's stable device")
	}
	if out := prefCall("POST", "/status", ownerCredentials.AccessToken, 200); out["enabled"] != false {
		t.Fatal("token rotation discarded explicit opt-out", out)
	}
	unchangedReceipt, err = app.FindRecordById(usernotification.Deliveries, receipt.Id)
	if err != nil || unchangedReceipt.GetString("device") != id || unchangedReceipt.GetBool("done") {
		t.Fatal("token rotation discarded pending delivery")
	}
	if restored := call("PUT", ownerCredentials.AccessToken, body, 200); restored["device"] != id {
		t.Fatal("repeat token rotation changed the device ID")
	}
	call("DELETE", otherCredentials.AccessToken, fmt.Sprintf(`{"device":%q}`, id), 200)
	if _, err := app.FindRecordById(usernotification.Devices, id); err != nil {
		t.Fatal("foreign deletion removed owner device")
	}
	transferred := call("PUT", otherCredentials.AccessToken, fmt.Sprintf(`{"token":%q,"locale":"en"}`, strings.ToLower(token)), 200)
	newID := transferred["device"].(string)
	for name, recordID := range map[string]string{usernotification.Devices: id, usernotification.Deliveries: receipt.Id, arcadereport.Subscriptions: pref.Id} {
		if _, err := app.FindRecordById(name, recordID); err == nil {
			t.Fatal("transfer retained old account work", name)
		}
	}
	if _, err := app.FindRecordById(usernotification.Inbox, notification.Id); err != nil {
		t.Fatal("device transfer deleted durable inbox")
	}
	newDevice, _ := app.FindRecordById(usernotification.Devices, newID)
	if newDevice.GetString("user") != other.Id {
		t.Fatal("device was not transferred")
	}
	// New login families must not reuse IDs a stale client can still delete.
	otherCredentials = issue(other)
	newSession := call("PUT", otherCredentials.AccessToken, fmt.Sprintf(`{"token":%q,"locale":"en"}`, strings.ToLower(token)), 200)
	previousID := newID
	newID = newSession["device"].(string)
	if newID == previousID {
		t.Fatal("new session reused a prior session's cleanup ID")
	}
	call("DELETE", otherCredentials.AccessToken, fmt.Sprintf(`{"device":%q}`, previousID), 200)
	if _, err := app.FindRecordById(usernotification.Devices, newID); err != nil {
		t.Fatal("stale cleanup removed newer session registration")
	}
	config.Enabled = false
	if unavailable := call("GET", otherCredentials.AccessToken, "", 200); unavailable["available"] != false || unavailable["topic"] != nil {
		t.Fatal(unavailable)
	}
	call("PUT", otherCredentials.AccessToken, body, 503)
	call("DELETE", otherCredentials.AccessToken, fmt.Sprintf(`{"device":%q}`, newID), 200)
	if _, err := app.FindRecordById(usernotification.Devices, newID); err == nil {
		t.Fatal("disabled provider prevented device removal")
	}
	if err := service.Revoke(app, ownerCredentials.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SessionUser(app, session); err == nil {
		t.Fatal("revoked session retained background authorization")
	}
	call("GET", ownerCredentials.AccessToken, "", 401)
}
