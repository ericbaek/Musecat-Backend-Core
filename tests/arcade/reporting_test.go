package arcade_test

import (
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
)

func TestReportSettingsPermissions(t *testing.T) {
	for _, tc := range []struct {
		name string
		tags []string
		owns bool
		want int
	}{
		{"visitor", nil, false, 403}, {"unowned official", []string{"arcade_owner"}, false, 403},
		{"owner", []string{"arcade_owner"}, true, 200}, {"developer", []string{"developer"}, false, 200}, {"moderator", []string{"moderator"}, false, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newArcadeTestApp(t)
			defer app.Cleanup()
			token, user := createAuthUserWithTags(t, app, tc.tags)
			_, creator := createAuthUser(t, app)
			arcadeID, _ := seedArcade(t, app, creator.Id, arcadeSeed{Name: "Managed", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
			if tc.owns {
				user.Set("owns", []string{arcadeID})
				if err := app.Save(user); err != nil {
					t.Fatal(err)
				}
			}
			headers := map[string]string{"Authorization": "Bearer " + token}
			body := fmt.Sprintf(`{"arcade":%q,"mode":"external","contactType":"url","contactValue":"https://example.com/reports","guide":"Contact us"}`, arcadeID)
			res := executeJSONRequest(t, app, "PUT", "/arcade/report-settings", body, headers)
			defer res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("save status %d, want %d", res.StatusCode, tc.want)
			}
			res = executeJSONRequest(t, app, "GET", "/arcade/report-settings?arcade="+arcadeID, "", headers)
			defer res.Body.Close()
			if res.StatusCode != tc.want {
				t.Fatalf("read status %d, want %d", res.StatusCode, tc.want)
			}
		})
	}
}

func TestExternalReportsPreserveFlagsAndRejectWrites(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	token, user := createAuthUserWithTags(t, app, []string{"developer"})
	arcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "External", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	gameID := seedGameAtomForFlag(t, app, arcadeID)
	headers := map[string]string{"Authorization": "Bearer " + token}
	create := func() *http.Response {
		return executeJSONRequest(t, app, "POST", "/arcade/flag", fmt.Sprintf(`{"arcade":%q,"game_id":%q,"disruption":"major","message":"broken button"}`, arcadeID, gameID), headers)
	}
	res := create()
	if res.StatusCode != 200 {
		t.Fatalf("create: %d", res.StatusCode)
	}
	var created struct {
		Flag string `json:"flag"`
	}
	if err := json.NewDecoder(res.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	switchMode := func(mode string) {
		res := executeJSONRequest(t, app, "PUT", "/arcade/report-settings", fmt.Sprintf(`{"arcade":%q,"mode":%q,"contactType":"phone","contactValue":"+82 10-1234-5678"}`, arcadeID, mode), headers)
		defer res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("switch: %d", res.StatusCode)
		}
	}
	assertVisible := func(want bool) {
		res := executeJSONRequest(t, app, "GET", "/arcade?id="+arcadeID+"&expand=game", "", headers)
		defer res.Body.Close()
		var value map[string]any
		if err := json.NewDecoder(res.Body).Decode(&value); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(value["game"])
		if strings.Contains(string(data), "broken button") != want {
			t.Fatalf("flag visibility mismatch: %s", data)
		}
	}
	assertVisible(true)
	switchMode("external")
	assertVisible(false)
	res = create()
	res.Body.Close()
	if res.StatusCode != 409 {
		t.Fatalf("external create: %d", res.StatusCode)
	}
	res = postFlagReaction(t, app, token, created.Flag, "issue_persist", "add")
	res.Body.Close()
	if res.StatusCode != 409 {
		t.Fatalf("external reaction: %d", res.StatusCode)
	}
	flag, err := app.FindRecordById("arcade_flag", created.Flag)
	if err != nil || flag.GetBool("solved") {
		t.Fatal("mode switch changed stored report")
	}
	switchMode("internal")
	assertVisible(true)
}

func TestReportContactValidation(t *testing.T) {
	for _, s := range []arcadereport.Settings{
		{Mode: "external"}, {Mode: "external", ContactType: "url", ContactValue: "javascript:alert(1)"},
		{Mode: "external", ContactType: "phone", ContactValue: "not a number"}, {Mode: "bad"},
	} {
		if s.Validate() == nil {
			t.Fatalf("accepted %#v", s)
		}
	}
	for _, s := range []arcadereport.Settings{{Mode: "internal"}, {Mode: "external", ContactType: "url", ContactValue: "https://example.com/report"}, {Mode: "external", ContactType: "phone", ContactValue: "+82 10-1234-5678"}} {
		if err := s.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReportSubscriptionAndDurableDelivery(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	token, user := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	arcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "Push", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	user.Set("owns", []string{arcadeID})
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	gameID := seedGameAtomForFlag(t, app, arcadeID)
	x, y := elliptic.P256().ScalarBaseMult([]byte{1})
	key := elliptic.Marshal(elliptic.P256(), x, y)
	endpoint := "https://fcm.googleapis.com/fcm/send/test"
	body := fmt.Sprintf(`{"arcade":%q,"subscription":{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q}}}`, arcadeID, endpoint, base64.RawURLEncoding.EncodeToString(key), base64.RawURLEncoding.EncodeToString(make([]byte, 16)))
	headers := map[string]string{"Authorization": "Bearer " + token}
	for i := 0; i < 2; i++ {
		res := executeJSONRequest(t, app, "PUT", "/arcade/report-subscription", body, headers)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("subscribe: %d", res.StatusCode)
		}
	}

	for _, tc := range []struct {
		endpoint string
		enabled  bool
	}{{endpoint, true}, {endpoint + "-other-device", false}} {
		res := executeJSONRequest(t, app, "POST", "/arcade/report-subscription/status", fmt.Sprintf(`{"arcade":%q,"endpoint":%q}`, arcadeID, tc.endpoint), headers)
		var status struct {
			Enabled bool `json:"enabled"`
		}
		err := json.NewDecoder(res.Body).Decode(&status)
		res.Body.Close()
		if res.StatusCode != 200 || err != nil || status.Enabled != tc.enabled {
			t.Fatalf("status: %d %v %#v", res.StatusCode, err, status)
		}
	}
	otherToken, _ := createAuthUserWithTags(t, app, []string{"developer"})
	otherStatus := executeJSONRequest(t, app, "POST", "/arcade/report-subscription/status", fmt.Sprintf(`{"arcade":%q,"endpoint":%q}`, arcadeID, endpoint), map[string]string{"Authorization": "Bearer " + otherToken})
	var otherResult struct {
		Enabled bool `json:"enabled"`
	}
	errStatus := json.NewDecoder(otherStatus.Body).Decode(&otherResult)
	otherStatus.Body.Close()
	if otherStatus.StatusCode != 200 || errStatus != nil || otherResult.Enabled {
		t.Fatal("status leaked another account subscription")
	}
	subs, err := app.FindRecordsByFilter(arcadereport.Subscriptions, "", "", 0, 0)
	if err != nil || len(subs) != 1 {
		t.Fatalf("expected one device: %v %d", err, len(subs))
	}
	secondBody := strings.Replace(body, endpoint, endpoint+"-phone", 1)
	secondRes := executeJSONRequest(t, app, "PUT", "/arcade/report-subscription", secondBody, headers)
	secondRes.Body.Close()
	if secondRes.StatusCode != 200 {
		t.Fatalf("second device subscribe: %d", secondRes.StatusCode)
	}
	subs, err = app.FindRecordsByFilter(arcadereport.Subscriptions, "", "", 0, 0)
	if err != nil || len(subs) != 2 {
		t.Fatalf("expected two devices for one account: %v %d", err, len(subs))
	}
	res := executeJSONRequest(t, app, "POST", "/arcade/flag", fmt.Sprintf(`{"arcade":%q,"game_id":%q,"disruption":"minor","message":"fault"}`, arcadeID, gameID), headers)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("create: %d", res.StatusCode)
	}
	rows, err := app.FindRecordsByFilter(usernotification.Deliveries, "done=false", "", 0, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("expected queued push for both devices: %v %d", err, len(rows))
	}
	if err := arcadereport.EnsureSchema(app); err != nil {
		t.Fatal("idempotent schema:", err)
	}
	res = executeJSONRequest(t, app, "DELETE", "/arcade/report-subscription", fmt.Sprintf(`{"arcade":%q,"endpoint":%q}`, arcadeID, endpoint), headers)
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	rows, err = app.FindRecordsByFilter(usernotification.Deliveries, "done=false", "", 0, 0)
	if err != nil || len(rows) != 1 {
		t.Fatal("disabling one device must preserve the other device delivery")
	}

	devices, err := app.FindRecordsByFilter(usernotification.Devices, "user={:user}", "", 0, 0, map[string]any{"user": user.Id})
	if err != nil || len(devices) != 2 {
		t.Fatalf("per-arcade opt-out removed global device: %v count=%d", err, len(devices))
	}
	prefs, err := app.FindRecordsByFilter(arcadereport.Subscriptions, "arcade={:arcade} && enabled=false", "", 0, 0, map[string]any{"arcade": arcadeID})
	if err != nil || len(prefs) != 1 {
		t.Fatalf("explicit arcade opt-out not retained: %v count=%d", err, len(prefs))
	}
	reporterToken, _ := createAuthUserWithTags(t, app, []string{})
	res = executeJSONRequest(t, app, "POST", "/arcade/flag", fmt.Sprintf(`{"arcade":%q,"game_id":%q,"disruption":"minor","message":"another fault"}`, arcadeID, gameID), map[string]string{"Authorization": "Bearer " + reporterToken})
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("new report: %d", res.StatusCode)
	}
	rows, err = app.FindRecordsByFilter(usernotification.Deliveries, "done=false", "", 0, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("disabled device received subsequent report: %v count=%d", err, len(rows))
	}
	archived, err := app.FindRecordsByFilter(arcadereport.Deliveries, "", "", 0, 0)
	if err != nil || len(archived) != 0 {
		t.Fatalf("legacy outbox still enqueued: %v count=%d", err, len(archived))
	}
	col, err := app.FindCollectionByNameOrId(arcadereport.Subscriptions)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"user", "endpoint", "subscription"} {
		if col.Fields.GetByName(field) != nil {
			t.Fatalf("duplicated credentials retained: %s", field)
		}
	}
}
