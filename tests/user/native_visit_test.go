package user_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestCanonicalNativeVisitMatchesWebDeduplicationAndXP(t *testing.T) {
	app := nativeReadApp(t, true)
	web, webOwner := createAuthUser(t, app, true)
	nativeWeb, nativeOwner := createAuthUser(t, app, true)
	token := nativeLoginIssue(t, app, nativeWeb).AccessToken
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	now := time.Date(2026, 7, 1, 14, 59, 0, 0, time.UTC)
	t.Cleanup(userhandler.SetVisitNowForTest(func() time.Time { return now }))
	body := map[string]any{"arcade": arcade.Id, "lat": 37.5665, "lon": 126.978, "accuracy": 150, "user": webOwner.Id}
	for step, gain := range []float64{5, 0, 2} {
		if step == 2 {
			now = now.Add(2 * time.Minute)
		}
		receipts := []map[string]any{}
		for _, credential := range []string{token, web} {
			response := nativeReadRequest(t, app, "POST", "/arcade/visit", credential, body, 200)
			var receipt map[string]any
			if err := json.NewDecoder(response.Body).Decode(&receipt); err != nil {
				t.Fatal(err)
			}
			if receipt["gained_exp"] != gain || receipt["first_visit_to_arcade"] != (step == 0) || receipt["already_visited"] != (step == 1) || receipt["visited"] != (step != 1) {
				t.Fatal("canonical visit receipt changed")
			}
			day := "2026-07-01"
			if step == 2 {
				day = "2026-07-02"
			}
			if !reflect.DeepEqual(receipt["visit"], map[string]any{"arcade": arcade.Id, "visit_day": day}) {
				t.Fatal("visit echo leaked verification data or lost local day")
			}
			receipts = append(receipts, receipt)
		}
		if !reflect.DeepEqual(receipts[0], receipts[1]) {
			t.Fatal("Native and web canonical receipts differ")
		}
		if step == 0 {
			exportCanonicalReadFixture(t, "visit-write", receipts[0])
		}
	}
	for _, owner := range []*core.Record{nativeOwner, webOwner} {
		rows, err := app.FindRecordsByFilter("arcade_visit", "user={:user}", "", 0, 0, dbx.Params{"user": owner.Id})
		if err != nil || len(rows) != 2 {
			t.Fatal("visit account scope or deduplication changed", err)
		}
		level, err := app.FindRecordById("user_level", owner.Id)
		if err != nil || level.GetInt("exp") != 7 {
			t.Fatal("visit XP changed", err)
		}
	}
	if count, err := app.CountRecords("arcade_changelog"); err != nil || count != 0 {
		t.Fatal("visit wrote arcade mutation history", err)
	}
	for _, route := range []struct{ method, path string }{{"PUT", "/moderation/supporter/request"}, {"PUT", "/arcade/report-subscription"}, {"POST", "/user/withdraw"}, {"POST", "/api/collections/user/auth-refresh"}} {
		response := doUserRequest(t, app, route.method, route.path, map[string]string{"Authorization": "Bearer " + token}, "{}")
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("visit auth widened unrelated capability")
		}
	}
	app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "POST /arcade/visit", MaxRequests: 1, Duration: 60}}}
	nativeReadRequest(t, app, "POST", "/arcade/visit", token, body, 200)
	nativeReadRequest(t, app, "POST", "/arcade/visit", token, body, 429)
	nativeReadRequest(t, app, "POST", "/native/arcade/visit", token, body, 404)
}

func TestCanonicalNativeVisitKeepsEligibilityAndPolicyTransaction(t *testing.T) {
	app := nativeReadApp(t, true)
	web, _ := createAuthUser(t, app, true)
	token := nativeLoginIssue(t, app, web).AccessToken
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	body := map[string]any{"arcade": arcade.Id, "lat": 37.5665, "lon": 126.978, "accuracy": 10}
	for _, sample := range []struct {
		key    string
		value  any
		status int
	}{{"accuracy", 151, 400}, {"accuracy", -1, 400}, {"lat", 91, 400}, {"lat", 37.57, 403}, {"arcade", "missingarcade00", 404}} {
		original := body[sample.key]
		body[sample.key] = sample.value
		nativeReadRequest(t, app, "POST", "/arcade/visit", token, body, sample.status)
		body[sample.key] = original
	}
	for _, sample := range []struct {
		field  string
		value  any
		status int
	}{{"public", false, 403}, {"closed", true, 403}, {"timezone", "Invalid/Zone", 409}, {"basic", "", 409}} {
		original := arcade.Get(sample.field)
		arcade.Set(sample.field, sample.value)
		if err := app.Save(arcade); err != nil {
			t.Fatal(err)
		}
		nativeReadRequest(t, app, "POST", "/arcade/visit", token, body, sample.status)
		arcade.Set(sample.field, original)
		if err := app.Save(arcade); err != nil {
			t.Fatal(err)
		}
	}
	userhandler.RegisterVisitPolicy(app, userhandler.VisitPolicy{Evaluate: func(core.App, userhandler.VisitAttempt) (string, error) { return "passport_restricted", nil }})
	response := nativeReadRequest(t, app, "POST", "/arcade/visit", token, body, 403)
	var denial map[string]any
	if err := json.NewDecoder(response.Body).Decode(&denial); err != nil {
		t.Fatal(err)
	}
	if denial["code"] != "passport_restricted" {
		t.Fatal("deployment policy code lost")
	}
	userhandler.RegisterVisitPolicy(app, userhandler.VisitPolicy{Evaluate: func(tx core.App, _ userhandler.VisitAttempt) (string, error) {
		r, err := tx.FindRecordById("arcade", arcade.Id)
		if err != nil {
			return "", err
		}
		r.Set("timezone", "UTC")
		if err := tx.Save(r); err != nil {
			return "", err
		}
		return "", errors.New("synthetic policy failure")
	}})
	nativeReadRequest(t, app, "POST", "/arcade/visit", token, body, 502)
	saved, err := app.FindRecordById("arcade", arcade.Id)
	if err != nil || saved.GetString("timezone") != "Asia/Seoul" {
		t.Fatal("deployment policy transaction did not roll back", err)
	}
	for _, collection := range []string{"arcade_visit", "user_level", "arcade_changelog"} {
		if n, err := app.CountRecords(collection); err != nil || n != 0 {
			t.Fatalf("denied visit changed %s", collection)
		}
	}
}

func TestCanonicalNativeVisitRejectsInvalidSessionAndKeepsRateLimit(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	credentials := nativeLoginIssue(t, app, web)
	arcade := seedVisitArcade(t, app, "Asia/Seoul")
	body := map[string]any{"arcade": arcade.Id, "lat": 37.5665, "lon": 126.978, "accuracy": 10}
	for _, token := range []string{"", strings.Repeat("z", 43)} {
		nativeReadRequest(t, app, "POST", "/arcade/visit", token, body, 401)
	}
	for _, change := range []struct {
		sql     string
		restore string
	}{{"access_expires=0", "access_expires=unixepoch()+600"}, {"expires=0", "expires=unixepoch()+3600"}, {"client='removed-client'", "client='app.musecat.native.dev'"}} {
		for i, sql := range []string{change.sql, change.restore} {
			if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + sql + " WHERE user_id={:user}").Bind(dbx.Params{"user": owner.Id}).Execute(); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				nativeReadRequest(t, app, "POST", "/arcade/visit", credentials.AccessToken, body, 401)
			}
		}
	}
	owner.Set("withdrawn", true)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	nativeReadRequest(t, app, "POST", "/arcade/visit", credentials.AccessToken, body, 401)
	owner.Set("withdrawn", false)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	seedUserBanForUserTest(t, app, owner.Id, hashNormalizedEmailForUserTest(owner.Email()), "native_visit_fixture", time.Now().Add(time.Hour))
	nativeReadRequest(t, app, "POST", "/arcade/visit", credentials.AccessToken, body, 403)
	nativeLoginRequest(t, app, "POST", "/native/session/revoke", "", map[string]string{"refresh_token": credentials.RefreshToken}, 204)
	nativeReadRequest(t, app, "POST", "/arcade/visit", credentials.AccessToken, body, 401)
	if n, err := app.CountRecords("arcade_visit"); err != nil || n != 0 {
		t.Fatal("denied session persisted a visit")
	}
	disabled := nativeReadApp(t, false)
	disabledWeb, disabledOwner := createAuthUser(t, disabled, true)
	disabledArcade := seedVisitArcade(t, disabled, "Asia/Seoul")
	body["arcade"] = disabledArcade.Id
	if err := nativeauth.InstallSchema(disabled); err != nil {
		t.Fatal(err)
	}
	service := nativeauth.Service{Clients: nativeLoginClients}
	verifier := strings.Repeat("n", 43)
	hash := sha256.Sum256([]byte(verifier))
	code, err := service.IssueCode(disabled, disabledOwner, nativeLoginClients[0].ID, nativeLoginClients[0].Callback, base64.RawURLEncoding.EncodeToString(hash[:]))
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Exchange(disabled, code, verifier, nativeLoginClients[0].ID, nativeLoginClients[0].Callback)
	if err != nil {
		t.Fatal(err)
	}
	disabledToken := issued.AccessToken
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	denied := doUserRequest(t, disabled, "POST", "/arcade/visit", map[string]string{"Authorization": "Bearer " + disabledToken, "Content-Type": "application/json"}, string(payload))
	denied.Body.Close()
	if denied.StatusCode != 401 {
		t.Fatal("disabled allowlist accepted Native visit")
	}
	disabled.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "POST /arcade/visit", MaxRequests: 1, Duration: 60}}}
	nativeReadRequest(t, disabled, "POST", "/arcade/visit", disabledWeb, body, 200)
	nativeReadRequest(t, disabled, "POST", "/arcade/visit", disabledWeb, body, 429)
}
