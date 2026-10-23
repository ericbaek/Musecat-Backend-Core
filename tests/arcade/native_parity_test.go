package arcade_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func nativeParitySession(t testing.TB, app *tests.TestApp) func(*core.Record) string {
	t.Helper()
	clients := []nativeauth.Client{{ID: "ios", Callback: "musecat-test://auth/callback"}}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		coreapp.RegisterNativeAPIRoutes(e, clients)
		return e.Next()
	})
	return func(u *core.Record) string {
		t.Helper()
		verifier := strings.Repeat("p", 43)
		digest := sha256.Sum256([]byte(verifier))
		service := nativeauth.Service{Clients: clients}
		code, err := service.IssueCode(app, u, clients[0].ID, clients[0].Callback, base64.RawURLEncoding.EncodeToString(digest[:]))
		if err != nil {
			t.Fatal(err)
		}
		out, err := service.Exchange(app, code, verifier, clients[0].ID, clients[0].Callback)
		if err != nil {
			t.Fatal(err)
		}
		return out.AccessToken
	}
}

func nativeParityJSON(t testing.TB, app *tests.TestApp, method, path, token, body string, status int) map[string]any {
	t.Helper()
	headers := map[string]string{"Content-Type": "application/json"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	response := executeJSONRequest(t, app, method, path, body, headers)
	defer response.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(response.Body).Decode(&out)
	if response.StatusCode != status {
		t.Fatalf("%s %s got %d want %d: %v", method, path, response.StatusCode, status, out)
	}
	if strings.HasPrefix(path, "/native/") && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("native response is cacheable: %s", path)
	}
	return out
}

func TestNativePhotoUploadPublicationAndIsolation(t *testing.T) {
	app := newArcadeTestApp(t)
	web, owner := createAuthUser(t, app)
	_, other := createAuthUser(t, app)
	issue := nativeParitySession(t, app)
	token, foreign := issue(owner), issue(other)
	arcade, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Native Photos", Address: "Seoul", Location: location{Lat: 37.5, Lon: 127}})
	private, _ := seedArcade(t, app, owner.Id, arcadeSeed{Name: "Private", Address: "Seoul", Location: location{Lat: 37.5, Lon: 127}})
	for _, route := range [][2]string{{"POST", "/arcade/photo/upload"}, {"PUT", "/arcade/photo"}, {"GET", "/arcade/photo/atoms"}, {"DELETE", "/arcade/photo/atom"}, {"GET", "/arcade/photo/file"}} {
		for _, denied := range []string{"", web, "partner-token"} {
			nativeParityJSON(t, app, route[0], "/native"+route[1], denied, `{}`, 401)
		}
	}
	upload := func(id string) photoUploadResponse {
		t.Helper()
		// A valid PNG with trailing bytes exceeds the default 4 KiB native
		// envelope and exercises the upload route's larger body ceiling.
		image := append(pngFixtureBytes(), make([]byte, 5000)...)
		body, contentType := buildPhotoUploadMultipart(t, id, []uploadTestFile{{Filename: "local.png", Content: image}, {Filename: "other.jpg", Content: jpegFixtureBytes()}})
		res := executeRequest(t, app, "POST", "/native/arcade/photo/upload", bytes.NewReader(body), map[string]string{"Authorization": "Bearer " + token, "Content-Type": contentType})
		defer res.Body.Close()
		var out photoUploadResponse
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil || res.StatusCode != 200 || out.Summary.Success != 2 || out.XPFeedback.DiffExp != 0 {
			t.Fatalf("upload status %d: %+v (%v)", res.StatusCode, out, err)
		}
		return out
	}
	uploaded := upload(arcade)
	atom, pending := uploaded.Uploaded[0].AtomID, uploaded.Uploaded[1].AtomID
	nativeParityJSON(t, app, "DELETE", "/native/arcade/photo/atom?id="+pending, foreign, "", 403)
	nativeParityJSON(t, app, "DELETE", "/native/arcade/photo/atom?id="+pending, token, "", 200)
	list := nativeParityJSON(t, app, "GET", "/native/arcade/photo/atoms?arcade="+arcade, token, "", 200)
	if list["total"] != float64(1) {
		t.Fatal(list)
	}
	for _, public := range []bool{false, true} {
		if public {
			body := fmt.Sprintf(`{"arcade":%q,"photos":[%q]}`, arcade, atom)
			first := nativeParityJSON(t, app, "PUT", "/native/arcade/photo", token, body, 200)
			if first["count"] != float64(1) || !mustFindRecord(t, app, "arcade_photo_atoms", atom).GetBool("public") {
				t.Fatal(first)
			}
			before, _ := app.CountRecords("user_level_log")
			nativeParityJSON(t, app, "PUT", "/native/arcade/photo", token, body, 200)
			after, _ := app.CountRecords("user_level_log")
			if before != after {
				t.Fatal("replayed publication awarded duplicate XP")
			}
			nativeParityJSON(t, app, "DELETE", "/native/arcade/photo/atom?id="+atom, token, "", 409)
		}
		res := executeJSONRequest(t, app, "GET", "/native/arcade/photo/file?id="+atom, "", map[string]string{"Authorization": "Bearer " + token})
		res.Body.Close()
		if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
			t.Fatalf("photo access/cache status %d %v", res.StatusCode, res.Header)
		}
	}
	privateAtom := upload(private).Uploaded[0].AtomID
	nativeParityJSON(t, app, "GET", "/native/arcade/photo/atoms?arcade="+private, foreign, "", 404)
	nativeParityJSON(t, app, "GET", "/native/arcade/photo/file?id="+privateAtom, foreign, "", 404)
	nativeParityJSON(t, app, "PUT", "/arcade/photo", token, `{}`, 401)
}

func TestNativeFeedbackRequestsAndRoleIsolation(t *testing.T) {
	app := newArcadeTestApp(t)
	web, owner := createAuthUser(t, app)
	_, other := createAuthUser(t, app)
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	issue := nativeParitySession(t, app)
	token, foreign, moderator := issue(owner), issue(other), issue(staff)
	call := func(method, path, credential, body string, status int) map[string]any {
		return nativeParityJSON(t, app, method, path, credential, body, status)
	}
	for _, route := range [][2]string{{"POST", "/support_feedback"}, {"GET", "/user/feedback"}, {"GET", "/user/feedback/thread"}, {"POST", "/user/feedback/thread"}, {"PATCH", "/user/feedback/thread"}, {"GET", "/user/feedback/thread/photo"}, {"POST", "/arcade/request_admin"}, {"GET", "/user/passport/stamps"}, {"PUT", "/user/profile"}, {"POST", "/arcade/flag"}, {"GET", "/moderation/user/bans"}, {"POST", "/campaign"}, {"GET", "/moderation/game/catalog"}} {
		for _, denied := range []string{"", web, "partner-token"} {
			call(route[0], "/native"+route[1], denied, `{}`, 401)
		}
	}
	created := call("POST", "/native/support_feedback", token, `{"message":"Native local help"}`, 200)
	id := created["id"].(string)
	if mustFindRecord(t, app, "support_feedback", id).GetString("createdBy") != owner.Id {
		t.Fatal("feedback author changed")
	}
	call("GET", "/native/user/feedback/thread?id="+id, foreign, "", 404)
	replyBody := fmt.Sprintf(`{"feedbackId":%q,"message":"Read this local reply"}`, id)
	call("POST", "/native/user/feedback/thread", foreign, replyBody, 404)
	call("POST", "/native/user/feedback/thread", moderator, replyBody, 201)
	thread := call("GET", "/native/user/feedback/thread?id="+id, token, "", 200)
	if len(thread["items"].([]any)) != 1 {
		t.Fatal(thread)
	}
	call("PATCH", "/native/user/feedback/thread", token, fmt.Sprintf(`{"feedbackId":%q,"status":"solved"}`, id), 200)
	call("PATCH", "/native/user/feedback/thread", foreign, fmt.Sprintf(`{"feedbackId":%q,"status":"waiting"}`, id), 404)
	call("PATCH", "/native/user/feedback/thread", token, fmt.Sprintf(`{"feedbackId":%q,"status":"waiting"}`, id), 200)
	call("POST", "/user/feedback/thread", token, replyBody, 401)
	call("POST", "/native/arcade/request_admin", token, `{"message":"Native review request"}`, 200)
	call("POST", "/native/arcade/request_admin", token, `{"message":"`+strings.Repeat("🎮", 1200)+`"}`, 200)
	requests := call("GET", "/native/arcade/request_admin?createdBy="+owner.Id, foreign, "", 200)
	if requests["total"] != float64(0) {
		t.Fatal("foreign request scope leaked", requests)
	}
	for _, route := range [][2]string{{"GET", "/moderation/user/bans"}, {"GET", "/moderation/arcade/requests"}, {"GET", "/moderation/supporter/requests"}, {"POST", "/campaign"}, {"GET", "/moderation/game/catalog"}, {"GET", "/support_feedback"}} {
		call(route[0], "/native"+route[1], token, `{}`, http.StatusForbidden)
	}
	call("GET", "/native/moderation/arcade/requests", moderator, "", 200)
	staff.Set("tags", []string{})
	staff.Set("tag", []string{})
	if err := app.Save(staff); err != nil {
		t.Fatal(err)
	}
	call("GET", "/native/moderation/arcade/requests", moderator, "", 403)
	call("GET", "/native/user/feedback/thread?id="+id, moderator, "", 404)
	owner.Set("withdrawn", true)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	call("GET", "/native/user/feedback", token, "", 401)
}

func TestNativeReportReactionAndAccountScope(t *testing.T) {
	app := newArcadeTestApp(t)
	_, author := createAuthUser(t, app)
	_, other := createAuthUser(t, app)
	issue := nativeParitySession(t, app)
	token, foreign := issue(author), issue(other)
	call := func(method, path, credential, body string, status int) map[string]any {
		return nativeParityJSON(t, app, method, path, credential, body, status)
	}
	arcade, _ := seedPublicArcade(t, app, author.Id, arcadeSeed{Name: "Native Reports", Address: "Seoul", Location: location{Lat: 37.5, Lon: 127}})
	game := seedGameAtomForFlag(t, app, arcade)
	report := call("POST", "/native/arcade/flag", token, fmt.Sprintf(`{"arcade":%q,"game_id":%q,"disruption":"minor","message":"Native report"}`, arcade, game), 200)
	id := report["flag"].(string)
	reaction := fmt.Sprintf(`{"flag":%q,"reaction":"issue_persist","action":"add"}`, id)
	call("POST", "/native/arcade/flag/reaction", foreign, reaction, 200)
	before, _ := app.CountRecords("arcade_flag_reaction")
	call("POST", "/native/arcade/flag/reaction", foreign, reaction, 400)
	after, _ := app.CountRecords("arcade_flag_reaction")
	if before != after {
		t.Fatal("duplicate reaction persisted")
	}
	call("POST", "/native/arcade/flag/reaction", foreign, fmt.Sprintf(`{"flag":%q,"reaction":"issue_persist","action":"delete"}`, id), 200)
	call("POST", "/native/arcade/flag/delete", foreign, fmt.Sprintf(`{"flag":%q}`, id), 400)
	call("POST", "/native/arcade/flag/delete", token, fmt.Sprintf(`{"flag":%q}`, id), 200)
	call("POST", "/native/arcade/flag/delete", token, fmt.Sprintf(`{"flag":%q}`, id), 400)
	// Main account mutations scope every update to re.Auth, ignoring caller IDs.
	for _, update := range []struct{ path, body, field, value string }{
		{"/user/visit-visibility", `{"visit_visibility":"private","user":"` + other.Id + `"}`, "visit_visibility", "private"},
		{"/user/favorite-visibility", `{"favorite_visibility":"private","user":"` + other.Id + `"}`, "favorite_visibility", "private"},
	} {
		foreignBefore := mustFindRecord(t, app, "user_info", other.Id).GetString(update.field)
		call("PUT", "/native"+update.path, token, update.body, 200)
		if mustFindRecord(t, app, "user_info", author.Id).GetString(update.field) != update.value || mustFindRecord(t, app, "user_info", other.Id).GetString(update.field) != foreignBefore {
			t.Fatal("account scope mismatch", update.path)
		}
	}
	call("PUT", "/native/user/countries", token, `{"countries":["KR","JP"],"country_mode":"manual"}`, 403)
	call("PUT", "/native/user/countries", token, `{"countries":["KR"],"country_mode":"manual"}`, 200)
	call("GET", "/native/user/passport/stamps", token, "", 200)
	call("GET", "/native/arcade/changelog?arcade="+arcade, token, "", 200)
	call("PUT", "/native/user/visit-visibility", token, `{"visit_visibility":"invalid"}`, 400)
}
