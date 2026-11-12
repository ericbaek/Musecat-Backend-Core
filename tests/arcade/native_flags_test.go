package arcade_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/readpolicy"
	"github.com/ericbaek/musecat-backend-core/service/xp"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func nativeFlagArcade(t *testing.T, app *tests.TestApp, owner string) (string, string) {
	t.Helper()
	id, _ := seedPublicArcade(t, app, owner, arcadeSeed{Name: "Canonical flag fixture", Address: "Public test road", Location: location{Lat: 37.5665, Lon: 126.978}})
	return id, seedGameAtomForFlag(t, app, id)
}
func nativeFlagRequest(t *testing.T, app *tests.TestApp, path, token string, body any, status int) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	res := executeJSONRequest(t, app, "POST", path, string(raw), headers)
	if res.StatusCode != status {
		t.Fatalf("%s status %d want %d", path, res.StatusCode, status)
	}
	if len(token) == 43 && !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("native flag response must not be cached")
	}
	return decodeJSONMap(t, res)
}
func nativeFlagState(t *testing.T, app *tests.TestApp, users ...string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, name := range []string{"arcade_flag", "arcade_flag_reaction", "user_level_log", "arcade_changelog"} {
		n, err := app.CountRecords(name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = int(n)
	}
	for _, id := range users {
		exp, err := xp.LoadCurrentExp(app, id)
		if err != nil {
			t.Fatal(err)
		}
		out[id] = exp
	}
	return out
}
func exportNativeFlagFixture(t *testing.T, name string, body map[string]any) {
	t.Helper()
	directory := os.Getenv("MUSECAT_CANONICAL_FLAG_FIXTURES")
	if directory == "" {
		return
	}
	var sensitive func(any) bool
	sensitive = func(value any) bool {
		switch value := value.(type) {
		case map[string]any:
			for key, item := range value {
				key = strings.ToLower(key)
				for _, word := range []string{"token", "password", "secret", "email"} {
					if strings.Contains(key, word) {
						return true
					}
				}
				if sensitive(item) {
					return true
				}
			}
		case []any:
			for _, item := range value {
				if sensitive(item) {
					return true
				}
			}
		}
		return false
	}
	if sensitive(body) {
		t.Fatal("domain fixture unexpectedly contains credential keys")
	}
	data, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name+".json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalNativeFlagsInteroperateWithWebAndReverseOnlyOwnXP(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUser(t, app)
	_, credentials := nativeNearbySession(t, app, owner)
	otherWeb, other := createAuthUser(t, app)
	_, otherCredentials := nativeNearbySession(t, app, other)
	arcade, game := nativeFlagArcade(t, app, owner.Id)
	calls := 0
	readpolicy.Register(app, func(re *core.RequestEvent, ids []string) error {
		calls++
		if re.Auth == nil || len(ids) != 1 || ids[0] != arcade {
			t.Fatal("read policy lost its account or target")
		}
		return apis.NewTooManyRequestsError("fixture restriction", nil)
	})
	for i, pair := range []struct{ report, react, undo, withdraw string }{{credentials.AccessToken, otherCredentials.AccessToken, otherCredentials.AccessToken, credentials.AccessToken}, {web, otherWeb, otherWeb, web}} {
		created := nativeFlagRequest(t, app, "/arcade/flag", pair.report, map[string]any{"arcade": arcade, "game_id": game, "disruption": "major", "message": " Canonical fault ", "user": other.Id}, 200)
		id := created["flag"].(string)
		saved, err := app.FindRecordById("arcade_flag", id)
		if err != nil {
			t.Fatal(err)
		}
		if created["arcade"] != arcade || created["game_id"] != game || saved.GetString("createdBy") != owner.Id || saved.GetString("message") != "Canonical fault" || created["xp_feedback"].(map[string]any)["diff_exp"] != float64(5) {
			t.Fatal("flag create changed canonical owner, normalization or reward")
		}
		added := nativeFlagRequest(t, app, "/arcade/flag/reaction", pair.react, map[string]any{"flag": id, "reaction": "fixed", "action": "add", "user": owner.Id}, 200)
		if added["flag"] != id || added["reaction"] != "fixed" || added["action"] != "add" || added["solved"] != false || added["xp_feedback"].(map[string]any)["diff_exp"] != float64(3) || added["resolution"] == nil || added["reportHistory"] == nil || added["game"] == nil {
			t.Fatal("reaction lost authoritative resolution or XP")
		}
		before := nativeFlagState(t, app, owner.Id, other.Id)
		nativeFlagRequest(t, app, "/arcade/flag/reaction", pair.undo, map[string]any{"flag": id, "reaction": "fixed", "action": "add"}, 400)
		if !reflect.DeepEqual(before, nativeFlagState(t, app, owner.Id, other.Id)) {
			t.Fatal("duplicate reaction changed ledger or state")
		}
		setRecordTimestamp(t, app, "arcade_flag_reaction", added["reaction_id"].(string), time.Now().UTC().Add(-14*time.Minute-59*time.Second))
		undone := nativeFlagRequest(t, app, "/arcade/flag/reaction", pair.undo, map[string]any{"flag": id, "reaction": "fixed", "action": "delete"}, 200)
		if undone["xp_feedback"].(map[string]any)["diff_exp"] != float64(-3) {
			t.Fatal("reaction withdrawal lost exact reward reversal")
		}
		setRecordTimestamp(t, app, "arcade_flag", id, time.Now().UTC().Add(-14*time.Minute-59*time.Second))
		deleted := nativeFlagRequest(t, app, "/arcade/flag/delete", pair.withdraw, map[string]any{"flag": id}, 200)
		if deleted["flag"] != id || deleted["deleted"] != true || deleted["xp_feedback"].(map[string]any)["diff_exp"] != float64(-5) || deleted["game"] == nil {
			t.Fatal("flag withdrawal wire or reward reversal changed")
		}
		if _, err := app.FindRecordById("arcade_flag", id); err == nil {
			t.Fatal("withdrawal did not remove own flag")
		}
		state := nativeFlagState(t, app, owner.Id, other.Id)
		if state[owner.Id] != 0 || state[other.Id] != 0 || state["arcade_flag_reaction"] != 0 || state["arcade_changelog"] != 0 {
			t.Fatal("flag flow crossed owner or arcade history scope")
		}
		if i == 0 {
			exportNativeFlagFixture(t, "flag-create", created)
			exportNativeFlagFixture(t, "flag-reaction-add", added)
			exportNativeFlagFixture(t, "flag-reaction-delete", undone)
			exportNativeFlagFixture(t, "flag-withdraw", deleted)
		}
	}
	if calls != 0 {
		t.Fatal("mutation acknowledgement changed the existing web read-policy boundary")
	}
	for _, token := range []string{web, credentials.AccessToken} {
		res := executeJSONRequest(t, app, "GET", "/arcade?id="+arcade+"&expand=all", "", map[string]string{"Authorization": "Bearer " + token})
		res.Body.Close()
		if res.StatusCode != 429 {
			t.Fatal("native flags bypassed deployment detail read policy")
		}
	}
	if calls != 2 {
		t.Fatal("selected detail policy was bypassed")
	}
}

func TestCanonicalNativeFlagsKeepPrivateExternalAndWithdrawalWindowPolicy(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUser(t, app)
	_, credentials := nativeNearbySession(t, app, owner)
	otherWeb, other := createAuthUser(t, app)
	_, otherCredentials := nativeNearbySession(t, app, other)
	arcade, game := nativeFlagArcade(t, app, owner.Id)
	body := map[string]any{"arcade": arcade, "game_id": game, "disruption": "minor", "message": "Policy fixture"}
	created := nativeFlagRequest(t, app, "/arcade/flag", credentials.AccessToken, body, 200)
	id := created["flag"].(string)
	before := nativeFlagState(t, app, owner.Id, other.Id)
	for _, token := range []string{otherWeb, otherCredentials.AccessToken} {
		nativeFlagRequest(t, app, "/arcade/flag/delete", token, map[string]any{"flag": id}, 400)
	}
	setRecordTimestamp(t, app, "arcade_flag", id, time.Now().UTC().Add(-16*time.Minute))
	nativeFlagRequest(t, app, "/arcade/flag/delete", credentials.AccessToken, map[string]any{"flag": id}, 400)
	if !reflect.DeepEqual(before, nativeFlagState(t, app, owner.Id, other.Id)) {
		t.Fatal("denied withdrawal changed ledger or flag")
	}
	reaction := nativeFlagRequest(t, app, "/arcade/flag/reaction", otherCredentials.AccessToken, map[string]any{"flag": id, "reaction": "fixed", "action": "add"}, 200)
	setRecordTimestamp(t, app, "arcade_flag_reaction", reaction["reaction_id"].(string), time.Now().UTC().Add(-16*time.Minute))
	before = nativeFlagState(t, app, owner.Id, other.Id)
	nativeFlagRequest(t, app, "/arcade/flag/reaction", otherCredentials.AccessToken, map[string]any{"flag": id, "reaction": "fixed", "action": "delete"}, 400)
	if !reflect.DeepEqual(before, nativeFlagState(t, app, owner.Id, other.Id)) {
		t.Fatal("expired reaction reversal changed state")
	}
	record, err := app.FindRecordById("arcade", arcade)
	if err != nil {
		t.Fatal(err)
	}
	record.Set("report_settings", map[string]any{"mode": "external"})
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{web, credentials.AccessToken} {
		denied := nativeFlagRequest(t, app, "/arcade/flag", token, body, 409)
		if denied["code"] != "REPORT_EXTERNAL" {
			t.Fatal("external-report error code changed")
		}
		denied = nativeFlagRequest(t, app, "/arcade/flag/reaction", token, map[string]any{"flag": id, "reaction": "issue_persist", "action": "add"}, 409)
		if denied["code"] != "REPORT_EXTERNAL" {
			t.Fatal("reaction external-report error code changed")
		}
	}
	record.Set("report_settings", map[string]any{"mode": "internal"})
	record.Set("public", false)
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{otherWeb, otherCredentials.AccessToken} {
		nativeFlagRequest(t, app, "/arcade/flag", token, body, 403)
		nativeFlagRequest(t, app, "/arcade/flag/reaction", token, map[string]any{"flag": id, "reaction": "issue_persist", "action": "add"}, 403)
	}
	privateCreated := nativeFlagRequest(t, app, "/arcade/flag", credentials.AccessToken, body, 200)
	if privateCreated["xp_feedback"].(map[string]any)["diff_exp"] != float64(0) {
		t.Fatal("private report unexpectedly awarded public XP")
	}
	other.Set("tags", []string{"moderator"})
	if err := app.Save(other); err != nil {
		t.Fatal(err)
	}
	nativeFlagRequest(t, app, "/arcade/flag", otherCredentials.AccessToken, body, 200)
	// Staff do not gain author-only withdrawal of another account's report.
	nativeFlagRequest(t, app, "/arcade/flag/delete", otherCredentials.AccessToken, map[string]any{"flag": privateCreated["flag"]}, 400)
}

func TestCanonicalNativeFlagMultipartAndExactBodyLimits(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, owner := createAuthUser(t, app)
	_, credentials := nativeNearbySession(t, app, owner)
	arcade, game := nativeFlagArcade(t, app, owner.Id)
	var photo bytes.Buffer
	if err := png.Encode(&photo, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"arcade": arcade, "game_id": game, "disruption": "major", "message": "Multipart fixture"} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := writer.CreateFormFile("photos", "synthetic.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(photo.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	res := executeJSONRequest(t, app, "POST", "/arcade/flag", body.String(), map[string]string{"Authorization": "Bearer " + credentials.AccessToken, "Content-Type": writer.FormDataContentType()})
	if res.StatusCode != 200 {
		t.Fatalf("native multipart status %d", res.StatusCode)
	}
	created := decodeJSONMap(t, res)
	record, err := app.FindRecordById("arcade_flag", created["flag"].(string))
	if err != nil || len(record.GetStringSlice("photos")) != 1 {
		t.Fatal("flag multipart did not preserve photo", err)
	}
	raw, err := json.Marshal(map[string]any{"arcade": arcade, "game_id": game, "disruption": "minor", "message": "Body bound fixture"})
	if err != nil {
		t.Fatal(err)
	}
	const maxBody = 46_048_576
	for _, sample := range []struct{ size, status int }{{maxBody, 200}, {maxBody + 1, 413}} {
		res := executeJSONRequest(t, app, "POST", "/arcade/flag", string(raw)+strings.Repeat(" ", sample.size-len(raw)), map[string]string{"Authorization": "Bearer " + credentials.AccessToken})
		res.Body.Close()
		if res.StatusCode != sample.status {
			t.Fatalf("flag body boundary status %d want %d", res.StatusCode, sample.status)
		}
	}
	for _, sample := range []struct{ count, size, status int }{{4, photo.Len(), 400}, {1, 15_000_001, 413}} {
		var data bytes.Buffer
		form := multipart.NewWriter(&data)
		for key, value := range map[string]string{"arcade": arcade, "game_id": game, "disruption": "major", "message": "Rejected media"} {
			if err := form.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < sample.count; i++ {
			part, err := form.CreateFormFile("photos", "synthetic.png")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(append(photo.Bytes(), make([]byte, sample.size-photo.Len())...)); err != nil {
				t.Fatal(err)
			}
		}
		if err := form.Close(); err != nil {
			t.Fatal(err)
		}
		before := nativeFlagState(t, app, owner.Id)
		res := executeJSONRequest(t, app, "POST", "/arcade/flag", data.String(), map[string]string{"Authorization": "Bearer " + credentials.AccessToken, "Content-Type": form.FormDataContentType()})
		res.Body.Close()
		if res.StatusCode != sample.status {
			t.Fatalf("flag image validation status %d want %d", res.StatusCode, sample.status)
		}
		if !reflect.DeepEqual(before, nativeFlagState(t, app, owner.Id)) {
			t.Fatal("rejected upload changed flag or XP")
		}
	}
}

func TestCanonicalNativeFlagWritesRejectInactiveAndInvalidCredentials(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUser(t, app)
	service, credentials := nativeNearbySession(t, app, owner)
	arcade, game := nativeFlagArcade(t, app, owner.Id)
	body := map[string]any{"arcade": arcade, "game_id": game, "disruption": "major", "message": "Authentication fixture"}
	flag := nativeFlagRequest(t, app, "/arcade/flag", web, body, 200)["flag"]
	ops := []struct {
		path string
		body any
	}{{"/arcade/flag", body}, {"/arcade/flag/reaction", map[string]any{"flag": flag, "reaction": "fixed", "action": "add"}}, {"/arcade/flag/delete", map[string]any{"flag": flag}}}
	reject := func(token string, status int) {
		t.Helper()
		for _, op := range ops {
			nativeFlagRequest(t, app, op.path, token, op.body, status)
		}
	}
	before := nativeFlagState(t, app, owner.Id)
	reject("", 401)
	reject(strings.Repeat("z", 43), 401)
	for _, op := range ops {
		raw, err := json.Marshal(op.body)
		if err != nil {
			t.Fatal(err)
		}
		res := executeJSONRequest(t, app, "POST", op.path, string(raw), map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatal("invalid flag bearer fell back to a cookie")
		}
	}
	for _, change := range []struct{ deny, restore string }{{"access_expires=0", "access_expires=unixepoch()+600"}, {"expires=0", "expires=unixepoch()+3600"}, {"client='removed-client'", "client='app.musecat.native.dev'"}} {
		for i, sql := range []string{change.deny, change.restore} {
			if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + sql + " WHERE user_id={:user}").Bind(dbx.Params{"user": owner.Id}).Execute(); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				reject(credentials.AccessToken, 401)
			}
		}
	}
	owner.Set("withdrawn", true)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	reject(credentials.AccessToken, 401)
	owner.Set("withdrawn", false)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	seedUserBan(t, app, owner.Id, "", "native_flag_fixture", time.Now().Add(time.Hour))
	reject(credentials.AccessToken, 403)
	reject(web, 403)
	if err := service.Revoke(app, credentials.RefreshToken); err != nil {
		t.Fatal(err)
	}
	reject(credentials.AccessToken, 401)
	if !reflect.DeepEqual(before, nativeFlagState(t, app, owner.Id)) {
		t.Fatal("denied Native flag operation changed domain or XP state")
	}
	saved, err := app.FindRecordById("arcade_flag", flag.(string))
	if err != nil || saved.GetBool("solved") {
		t.Fatal("denied flag mutation removed or solved report", err)
	}
	disabled := nativeNearbyApp(t, false)
	disabledWeb, disabledOwner := createAuthUser(t, disabled)
	disabledService, issued := nativeNearbySession(t, disabled, disabledOwner)
	if actor, err := disabledService.Authenticate(disabled, issued.AccessToken); err != nil || actor.Id != disabledOwner.Id {
		t.Fatal("disabled fixture must contain a valid same-database Native session", err)
	}
	disabledArcade, disabledGame := nativeFlagArcade(t, disabled, disabledOwner.Id)
	disabledBody := map[string]any{"arcade": disabledArcade, "game_id": disabledGame, "disruption": "minor", "message": "Disabled adapter fixture"}
	nativeFlagRequest(t, disabled, "/arcade/flag", disabledWeb, disabledBody, 200)
	for _, op := range ops {
		raw, err := json.Marshal(op.body)
		if err != nil {
			t.Fatal(err)
		}
		res := executeJSONRequest(t, disabled, "POST", op.path, string(raw), map[string]string{"Authorization": "Bearer " + issued.AccessToken})
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatal("disabled Native configuration granted a flag write")
		}
	}
}

func TestCanonicalNativeFlagWritesKeepRateLimitsAndDoNotEnablePhotoReads(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUser(t, app)
	_, credentials := nativeNearbySession(t, app, owner)
	arcade, game := nativeFlagArcade(t, app, owner.Id)
	body := map[string]any{"arcade": arcade, "game_id": game, "disruption": "major", "message": "Rate fixture"}
	flag := nativeFlagRequest(t, app, "/arcade/flag", web, body, 200)["flag"]
	for _, op := range []struct {
		path string
		body any
	}{{"/arcade/flag", body}, {"/arcade/flag/reaction", map[string]any{"flag": flag, "reaction": "issue_persist", "action": "add"}}, {"/arcade/flag/delete", map[string]any{"flag": flag}}} {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "POST " + op.path, MaxRequests: 1, Duration: 60}}}
		nativeFlagRequest(t, app, op.path, credentials.AccessToken, op.body, 200)
		nativeFlagRequest(t, app, op.path, credentials.AccessToken, op.body, 429)
	}
	for _, op := range []struct {
		method, path string
		status       int
	}{{"POST", "/arcade/check-notes", 401}, {"POST", "/campaign/check", 401}, {"GET", "/arcade/flag/photo", 404}, {"GET", "/native/arcade/flag/photo", 404}, {"POST", "/native/arcade/flag", 404}} {
		res := executeJSONRequest(t, app, op.method, op.path, "{}", map[string]string{"Authorization": "Bearer " + credentials.AccessToken})
		res.Body.Close()
		if res.StatusCode != op.status {
			t.Fatal("flag write bindings widened unselected routes")
		}
	}
}
