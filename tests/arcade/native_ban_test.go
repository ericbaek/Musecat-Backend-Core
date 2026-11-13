package arcade_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	pbtypes "github.com/pocketbase/pocketbase/tools/types"
)

func nativeBanRequest(t *testing.T, app *tests.TestApp, method, path, token, cookie string, body any, want int) map[string]any {
	t.Helper()
	text := ""
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		text = string(raw)
	}
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if cookie != "" {
		headers["Cookie"] = "pb_auth=" + cookie
	}
	res := executeJSONRequest(t, app, method, path, text, headers)
	defer res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("ban %s status%d want%d", method, res.StatusCode, want)
	}
	if len(token) == 43 && want != 401 && !strings.HasPrefix(path, "/native/") && !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("Native ban response cacheable")
	}
	var v map[string]any
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}
func nativeBanProtected(t *testing.T, app *tests.TestApp, token string, want int) {
	t.Helper()
	res := executeJSONRequest(t, app, "GET", "/user/notifications", "", map[string]string{"Authorization": "Bearer " + token})
	res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("protected request status%d want%d", res.StatusCode, want)
	}
}
func nativeBanFamilies(t *testing.T, app *tests.TestApp, user string) []int {
	t.Helper()
	var out struct {
		Families int `db:"families"`
		Revoked  int `db:"revoked"`
	}
	if err := app.DB().NewQuery("SELECT COUNT(*) AS families, COALESCE(SUM(revoked),0) AS revoked FROM native_auth_session WHERE user_id={:user}").Bind(dbx.Params{"user": user}).One(&out); err != nil {
		t.Fatal(err)
	}
	return []int{out.Families, out.Revoked}
}
func exportNativeBan(t *testing.T, name string, v any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_BAN_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "email", "hashed_email", "access_token", "refresh_token"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+key+`"`) {
			t.Fatal("private field in ban fixture")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestCanonicalNativeBanActualUpsertReadbackAndRequestDenial(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, actor := nativeNearbySession(t, app, staff)
	targetWeb, target := createAuthUser(t, app)
	_, subject := nativeNearbySession(t, app, target)
	beforeXP := nativeSubwayState(t, app)
	families := nativeBanFamilies(t, app, target.Id)
	nativeBanProtected(t, app, subject.AccessToken, 200)
	permanent := nativeBanRequest(t, app, "POST", "/moderation/user/ban", actor.AccessToken, "", map[string]any{"user": " " + target.Id + " ", "reason": " Permanent review ", "id": staff.Id, "hashed_email": "spoof", "createdBy": staff.Id}, 200)
	if permanent["id"] != target.Id || permanent["user"] != target.Id || permanent["reason"] != "Permanent review" || permanent["until"] != "" || permanent["permanent"] != true {
		t.Fatal("permanent ban target/normalization changed")
	}
	stored := mustFindRecord(t, app, "user_ban", target.Id)
	digest := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(target.Email()))))
	if stored.GetString("hashed_email") != hex.EncodeToString(digest[:]) {
		t.Fatal("server normalized email association changed")
	}
	list := moderationRead(t, app, "/moderation/user/bans", web, "", 200)
	if len(list["items"].([]any)) != 1 || !reflect.DeepEqual(permanent, list["items"].([]any)[0]) {
		t.Fatal("Native ban/Web canonical readback differs")
	}
	nativeBanProtected(t, app, targetWeb, 403)
	nativeBanProtected(t, app, subject.AccessToken, 403)
	if !reflect.DeepEqual(families, nativeBanFamilies(t, app, target.Id)) {
		t.Fatal("ban physically changed Native families")
	}
	until := time.Now().Add(time.Hour).Truncate(time.Second).In(time.FixedZone("synthetic-offset", 9*3600))
	updated := nativeBanRequest(t, app, "POST", "/moderation/user/ban", actor.AccessToken, "", map[string]any{"user": target.Id, "reason": " Updated review ", "until": until.Format(time.RFC3339)}, 200)
	parsed, err := pbtypes.ParseDateTime(updated["until"])
	if err != nil || !parsed.Time().Equal(until.UTC()) || updated["permanent"] != false || updated["reason"] != "Updated review" || updated["id"] != target.Id {
		t.Fatal("accepted upsert/UTC expiry changed")
	}
	repeated := nativeBanRequest(t, app, "POST", "/moderation/user/ban", web, "", map[string]any{"user": target.Id, "reason": "Updated review", "until": until.Format(time.RFC3339)}, 200)
	for _, k := range []string{"id", "user", "username", "reason", "until", "permanent", "created"} {
		if repeated[k] != updated[k] {
			t.Fatal("accepted Web repeat changed content")
		}
	}
	deleted := nativeBanRequest(t, app, "DELETE", "/moderation/user/ban?user="+target.Id, actor.AccessToken, "", nil, 200)
	if deleted["user"] != target.Id || deleted["deleted"] != true {
		t.Fatal("delete confirmation changed")
	}
	nativeBanRequest(t, app, "DELETE", "/moderation/user/ban?user="+target.Id, web, "", nil, 404)
	nativeBanProtected(t, app, targetWeb, 200)
	nativeBanProtected(t, app, subject.AccessToken, 200)
	if !reflect.DeepEqual(families, nativeBanFamilies(t, app, target.Id)) || beforeXP != nativeSubwayState(t, app) {
		t.Fatal("ban/unban changed sessions or XP/history")
	}
	exportNativeBan(t, "ban-permanent", permanent)
	exportNativeBan(t, "ban-updated", updated)
	exportNativeBan(t, "ban-delete", deleted)
	// The canonical Web body fallback remains available; Native continues using query.
	nativeBanRequest(t, app, "POST", "/moderation/user/ban", web, "", map[string]any{"user": target.Id, "reason": "Body fallback"}, 200)
	nativeBanRequest(t, app, "DELETE", "/moderation/user/ban", web, "", map[string]any{"user": target.Id}, 200)
}
func TestCanonicalNativeBanNormalizedEmailAndExistingSelfStaffPolicy(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, actor := nativeNearbySession(t, app, staff)
	_, target := createAuthUser(t, app)
	originalEmail := target.Email()
	nativeBanRequest(t, app, "POST", "/moderation/user/ban", actor.AccessToken, "", map[string]any{"user": target.Id, "reason": "Synthetic email restriction"}, 200)
	target.SetEmail("changed-" + originalEmail)
	if err := app.Save(target); err != nil {
		t.Fatal(err)
	}
	_, shadow := createAuthUser(t, app)
	shadow.SetEmail(strings.ToUpper(originalEmail))
	if err := app.Save(shadow); err != nil {
		t.Fatal(err)
	}
	shadowWeb, err := shadow.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	_, shadowSession := nativeNearbySession(t, app, shadow)
	nativeBanProtected(t, app, shadowWeb, 403)
	nativeBanProtected(t, app, shadowSession.AccessToken, 403)
	if n, err := app.CountRecords("user_ban"); err != nil || n != 1 {
		t.Fatal("email restriction created another ban record")
	}
	nativeBanRequest(t, app, "DELETE", "/moderation/user/ban?user="+target.Id, actor.AccessToken, "", nil, 200)
	nativeBanProtected(t, app, shadowWeb, 200)
	nativeBanProtected(t, app, shadowSession.AccessToken, 200)
	otherWeb, other := createAuthUserWithTags(t, app, []string{"developer"})
	_, otherSession := nativeNearbySession(t, app, other)
	nativeBanRequest(t, app, "POST", "/moderation/user/ban", actor.AccessToken, "", map[string]any{"user": other.Id, "reason": "Existing staff target policy"}, 200)
	nativeBanProtected(t, app, otherSession.AccessToken, 403)
	nativeBanRequest(t, app, "DELETE", "/moderation/user/ban?user="+other.Id, actor.AccessToken, "", nil, 200)
	nativeBanRequest(t, app, "POST", "/moderation/user/ban", actor.AccessToken, "", map[string]any{"user": staff.Id, "reason": "Existing self target policy"}, 200)
	nativeBanRequest(t, app, "DELETE", "/moderation/user/ban?user="+staff.Id, actor.AccessToken, "", nil, 403)
	nativeBanRequest(t, app, "DELETE", "/moderation/user/ban?user="+staff.Id, otherWeb, "", nil, 200)
	nativeBanProtected(t, app, web, 200)
}
func TestCanonicalNativeBanInvalidInputAndFailedPersistenceUnchanged(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, staff := createAuthUserWithTags(t, app, []string{"developer"})
	_, s := nativeNearbySession(t, app, staff)
	_, target := createAuthUser(t, app)
	before := nativeGrantStatusState(t, app)
	for _, body := range []map[string]any{{"user": target.Id, "reason": " "}, {"user": "short", "reason": "review"}, {"user": target.Id, "reason": "review", "until": "invalid"}, {"user": target.Id, "reason": "review", "until": time.Now().Add(-time.Hour).Format(time.RFC3339)}, {"user": "missingrecord01", "reason": "review"}} {
		want := 400
		if body["user"] == "missingrecord01" {
			want = 404
		}
		nativeBanRequest(t, app, "POST", "/moderation/user/ban", s.AccessToken, "", body, want)
	}
	nativeBanRequest(t, app, "DELETE", "/moderation/user/ban?user=short", s.AccessToken, "", nil, 400)
	nativeBanRequest(t, app, "DELETE", "/moderation/user/ban?user="+target.Id, s.AccessToken, "", nil, 404)
	if before != nativeGrantStatusState(t, app) {
		t.Fatal("invalid ban request changed state")
	}
	for _, mode := range []string{"create", "update", "delete"} {
		t.Run(mode, func(t *testing.T) {
			isolated := nativeNearbyApp(t, true)
			web, u := createAuthUserWithTags(t, isolated, []string{"moderator"})
			_, session := nativeNearbySession(t, isolated, u)
			_, subject := createAuthUser(t, isolated)
			if mode != "create" {
				nativeBanRequest(t, isolated, "POST", "/moderation/user/ban", web, "", map[string]any{"user": subject.Id, "reason": "Baseline"}, 200)
			}
			before := nativeGrantStatusState(t, isolated)
			fail := func(*core.RecordEvent) error { return errors.New("synthetic ban persistence failure") }
			switch mode {
			case "create":
				isolated.OnRecordCreate("user_ban").BindFunc(fail)
			case "update":
				isolated.OnRecordUpdate("user_ban").BindFunc(fail)
			case "delete":
				isolated.OnRecordDelete("user_ban").BindFunc(fail)
			}
			if mode == "delete" {
				nativeBanRequest(t, isolated, "DELETE", "/moderation/user/ban?user="+subject.Id, session.AccessToken, "", nil, 502)
			} else {
				nativeBanRequest(t, isolated, "POST", "/moderation/user/ban", session.AccessToken, "", map[string]any{"user": subject.Id, "reason": "Updated"}, 502)
			}
			if before != nativeGrantStatusState(t, isolated) {
				t.Fatal("failed Save/Delete changed ban/account/XP/history")
			}
		})
	}
}
func TestCanonicalNativeBanRolesLifetimesDisabledAndScope(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, target := createAuthUser(t, app)
	for _, tags := range [][]string{nil, {"supporter"}, {"founding_supporter"}, {"designer"}, {"arcade_owner"}} {
		web, u := createAuthUserWithTags(t, app, tags)
		_, s := nativeNearbySession(t, app, u)
		before := nativeGrantStatusState(t, app)
		for _, token := range []string{web, s.AccessToken} {
			nativeBanRequest(t, app, "POST", "/moderation/user/ban", token, "", map[string]any{"user": target.Id, "reason": "review"}, 403)
			nativeBanRequest(t, app, "DELETE", "/moderation/user/ban?user="+target.Id, token, "", nil, 403)
		}
		if before != nativeGrantStatusState(t, app) {
			t.Fatal("denied role changed bans")
		}
	}
	web, u := createAuthUserWithTags(t, app, []string{"developer"})
	_, s := nativeNearbySession(t, app, u)
	for _, method := range []string{"POST", "DELETE"} {
		for _, token := range []string{"", strings.Repeat("z", 43)} {
			nativeBanRequest(t, app, method, "/moderation/user/ban", token, web, map[string]any{}, 401)
		}
		nativeBanRequest(t, app, method, "/native/moderation/user/ban", s.AccessToken, "", nil, 404)
	}
	for _, r := range []struct {
		method, path string
		want         int
	}{{"POST", "/user/signup", 401}, {"POST", "/api/collections/user/auth-refresh", 401}, {"DELETE", "/user/notifications/device", 401}, {"PUT", "/moderation/arcade/edit-report", 401}, {"POST", "/developer/v1/account/moderation/user/ban", 403}, {"DELETE", "/developer/v1/account/moderation/user/ban", 403}, {"GET", "/api/collections/arcade/records", 403}} {
		res := executeJSONRequest(t, app, r.method, r.path, "{}", map[string]string{"Authorization": "Bearer " + s.AccessToken})
		res.Body.Close()
		if res.StatusCode != r.want {
			t.Fatal("ban auth widened another boundary")
		}
	}
	for _, mode := range []string{"disabled", "revoked", "expired", "family-expired", "removed-client", "account-key", "withdrawn", "ban"} {
		t.Run(mode, func(t *testing.T) {
			isolated := nativeNearbyApp(t, mode != "disabled")
			web, operator := createAuthUserWithTags(t, isolated, []string{"moderator"})
			service, session := nativeNearbySession(t, isolated, operator)
			_, subject := createAuthUser(t, isolated)
			if owner, err := service.Authenticate(isolated, session.AccessToken); err != nil || owner.Id != operator.Id {
				t.Fatal("fixture Native session invalid")
			}
			switch mode {
			case "revoked":
				if err := service.Revoke(isolated, session.RefreshToken); err != nil {
					t.Fatal(err)
				}
			case "account-key":
				operator.RefreshTokenKey()
				if err := isolated.Save(operator); err != nil {
					t.Fatal(err)
				}
			case "withdrawn":
				operator.Set("withdrawn", true)
				if err := isolated.Save(operator); err != nil {
					t.Fatal(err)
				}
			case "ban":
				seedUserBan(t, isolated, operator.Id, "", "Synthetic operator ban", time.Now().Add(time.Hour))
			case "expired", "family-expired", "removed-client":
				clause := map[string]string{"expired": "access_expires=0", "family-expired": "expires=0", "removed-client": "client='removed'"}[mode]
				if _, err := isolated.DB().NewQuery("UPDATE native_auth_session SET " + clause + " WHERE user_id={:user}").Bind(dbx.Params{"user": operator.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
			}
			before := nativeGrantStatusState(t, isolated)
			want := 401
			if mode == "ban" {
				want = 403
			}
			nativeBanRequest(t, isolated, "POST", "/moderation/user/ban", session.AccessToken, "", map[string]any{"user": subject.Id, "reason": "review"}, want)
			nativeBanRequest(t, isolated, "DELETE", "/moderation/user/ban?user="+subject.Id, session.AccessToken, "", nil, want)
			if before != nativeGrantStatusState(t, isolated) {
				t.Fatal("inactive operator changed bans")
			}
			if mode == "disabled" {
				nativeBanRequest(t, isolated, "POST", "/moderation/user/ban", web, "", map[string]any{"user": subject.Id, "reason": "review"}, 200)
				nativeBanRequest(t, isolated, "DELETE", "/moderation/user/ban?user="+subject.Id, web, "", nil, 200)
			}
		})
	}
}
func TestCanonicalNativeBanRateAndExpiredRowPolicy(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, staff)
	web, subject := createAuthUser(t, app)
	_, family := nativeNearbySession(t, app, subject)
	seedUserBan(t, app, subject.Id, "", "Synthetic expired row", time.Now().Add(-time.Hour))
	before := nativeGrantStatusState(t, app)
	nativeBanProtected(t, app, web, 200)
	nativeBanProtected(t, app, family.AccessToken, 200)
	if before != nativeGrantStatusState(t, app) {
		t.Fatal("expired-row request changed persisted ban")
	}
	app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "POST /moderation/user/ban", MaxRequests: 1, Duration: 60}, {Label: "DELETE /moderation/user/ban", MaxRequests: 1, Duration: 60}}}
	for _, method := range []string{"POST", "DELETE"} {
		nativeBanRequest(t, app, method, "/moderation/user/ban", s.AccessToken, "", map[string]any{}, 400)
		nativeBanRequest(t, app, method, "/moderation/user/ban", s.AccessToken, "", map[string]any{}, 429)
	}
	if before != nativeGrantStatusState(t, app) {
		t.Fatal("quota changed ban/account state")
	}
}
