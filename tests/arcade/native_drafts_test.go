package arcade_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
)

func TestNativeDraftLifecycleOwnershipAndPublication(t *testing.T) {
	app := newArcadeTestApp(t)
	stubGeoLookup(t)
	web, creator := createAuthUser(t, app)
	_, foreign := createAuthUser(t, app)
	_, reviewer := createAuthUser(t, app)
	reviewer.Set("tags", []string{"moderator"})
	if err := app.Save(reviewer); err != nil {
		t.Fatal(err)
	}
	clients := []nativeauth.Client{{ID: "ios", Callback: "musecat-test://auth/callback"}}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		coreapp.RegisterNativeAPIRoutes(e, clients)
		return e.Next()
	})
	credentials := func(user *core.Record) string {
		t.Helper()
		verifier := strings.Repeat("d", 43)
		digest := sha256.Sum256([]byte(verifier))
		service := nativeauth.Service{Clients: clients}
		code, err := service.IssueCode(app, user, "ios", clients[0].Callback, base64.RawURLEncoding.EncodeToString(digest[:]))
		if err != nil {
			t.Fatal(err)
		}
		out, err := service.Exchange(app, code, verifier, "ios", clients[0].Callback)
		if err != nil {
			t.Fatal(err)
		}
		return out.AccessToken
	}
	token, other, staff := credentials(creator), credentials(foreign), credentials(reviewer)
	request := func(method, path, credential, body string, status int) map[string]any {
		t.Helper()
		headers := map[string]string{}
		if credential != "" {
			headers["Authorization"] = "Bearer " + credential
		}
		response := executeJSONRequest(t, app, method, path, body, headers)
		defer response.Body.Close()
		var result map[string]any
		_ = json.NewDecoder(response.Body).Decode(&result)
		if response.StatusCode != status {
			t.Fatalf("%s %s got %d want %d: %v", method, path, response.StatusCode, status, result)
		}
		if strings.HasPrefix(path, "/native/") && status == 200 && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("private native response was cacheable")
		}
		return result
	}
	for _, operation := range [][2]string{{"POST", "new"}, {"GET", "drafts"}, {"GET", "draft"}, {"DELETE", "draft"}, {"GET", "public"}, {"PUT", "public"}, {"POST", "location-verification"}} {
		for _, rejected := range []string{"", web, "invalid-partner-credential"} {
			request(operation[0], "/native/arcade/"+operation[1], rejected, `{}`, 401)
		}
	}
	body := `{"name":"Native Draft","address":"Seoul, Korea","location":{"lat":37.5665,"lon":126.978},"direction":"B1F","nickname":["Alias"],"subway_line":["2"]}`
	created := request("POST", "/native/arcade/new", token, body, 200)
	id, ok := created["id"].(string)
	if !ok || id == "" || created["country"] != "KR" || created["timezone"] != "Asia/Seoul" {
		t.Fatal("invalid creation result", created)
	}
	arcade := mustFindRecord(t, app, "arcade", id)
	if arcade.GetString("createdBy") != creator.Id || arcade.GetBool("public") {
		t.Fatal("creation ownership/visibility changed")
	}
	list := request("GET", "/native/arcade/drafts", token, "", 200)
	if list["total"] != float64(1) || list["items"].([]any)[0].(map[string]any)["id"] != id {
		t.Fatal(list)
	}
	if otherList := request("GET", "/native/arcade/drafts?createdBy="+creator.Id, other, "", 200); otherList["total"] != float64(0) {
		t.Fatal("foreign list scope changed", otherList)
	}
	request("GET", "/arcade?id="+id, "", "", 404)
	request("GET", "/arcade/draft?id="+id, token, "", 401)
	detail := request("GET", "/native/arcade/draft?id="+id+"&expand=all", token, "", 200)
	if detail["admin"].(map[string]any)["id"] != id || detail["basic"].(map[string]any)["name"] != "Native Draft" {
		t.Fatal("draft DTO changed", detail)
	}
	request("GET", "/native/arcade/draft?id="+id, other, "", 404)
	request("GET", "/native/arcade/draft?id="+id, staff, "", 200)
	for _, noncreator := range []string{other, staff} {
		request("GET", "/native/arcade/public?arcade="+id, noncreator, "", 403)
		request("PUT", "/native/arcade/public", noncreator, `{"arcade":"`+id+`"}`, 403)
		request("POST", "/native/arcade/location-verification", noncreator, `{"arcade":"`+id+`","lat":37.5665,"lon":126.978,"accuracy":5}`, 403)
		request("DELETE", "/native/arcade/draft?id="+id, noncreator, "", 403)
	}
	count := func(collection string) int64 {
		t.Helper()
		n, err := app.CountRecords(collection)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	xpBefore := count("user_level_log")
	preview := request("GET", "/native/arcade/public?arcade="+id, token, "", 200)
	if preview["level"] != float64(0) || len(preview["requirements"].([]any)) != 3 || preview["location_verified"] != false || count("user_level_log") != xpBefore {
		t.Fatal("preview changed domain policy or XP", preview)
	}
	request("PUT", "/native/arcade/public", token, `{"arcade":"`+id+`"}`, 400)
	request("POST", "/native/arcade/location-verification", token, `{"arcade":"`+id+`","lat":37,"lon":127,"accuracy":5}`, 403)
	request("POST", "/native/arcade/location-verification", token, `{"arcade":"`+id+`","lat":37.5665,"lon":126.978,"accuracy":101}`, 400)
	verifyBody := `{"arcade":"` + id + `","lat":37.5665,"lon":126.978,"accuracy":5}`
	verified := request("POST", "/native/arcade/location-verification", token, verifyBody, 200)
	if verified["verified"] != true || verified["basic"] != arcade.GetString("basic") || count("user_level_log") != xpBefore {
		t.Fatal("verification changed XP or revision contract", verified)
	}
	request("PATCH", "/native/v1/arcades/"+id+"/basic", token, fmt.Sprintf(`{"revision":%q,"changes":{"direction":"2F"}}`, arcade.GetString("basic")), 200)
	if changed := request("GET", "/native/arcade/public?arcade="+id, token, "", 200); changed["location_verified"] != false {
		t.Fatal("basic edit retained stale verification", changed)
	}
	request("POST", "/native/arcade/location-verification", token, verifyBody, 200)
	version := seedGameSeriesVersion(t, app)
	request("PATCH", "/native/v1/arcades/"+id+"/games", token, fmt.Sprintf(`{"revision":"","add":[{"game":%q,"location":"1F","quantity":1,"price":{"currency":"KRW","type":"credit","list":[{"title":"1","value":500}],"accept":["Cash"]},"tag":[]}]}`, version), 200)
	request("PATCH", "/native/v1/arcades/"+id+"/hours", token, `{"revision":"","changes":{"Monday":{"start":1000,"end":2200}}}`, 200)
	preview = request("GET", "/native/arcade/public?arcade="+id, token, "", 200)
	for _, requirement := range preview["requirements"].([]any) {
		if requirement.(map[string]any)["satisfied"] != true {
			t.Fatal("completed draft failed requirements", preview)
		}
	}
	published := request("PUT", "/native/arcade/public", token, `{"arcade":"`+id+`"}`, 200)
	if published["public"] != true || published["xp_feedback"].(map[string]any)["diff_exp"] != float64(11) {
		t.Fatal("publication XP/response changed", published)
	}
	xpPublished := count("user_level_log")
	request("PUT", "/native/arcade/public", token, `{"arcade":"`+id+`"}`, 400)
	request("DELETE", "/native/arcade/draft?id="+id, token, "", 409)
	request("GET", "/native/arcade/draft?id="+id, token, "", 404)
	if remaining := request("GET", "/native/arcade/drafts", token, "", 200); remaining["total"] != float64(0) || count("user_level_log") != xpPublished {
		t.Fatal("publication replay changed state", remaining)
	}
	request("GET", "/arcade?id="+id, "", "", 200)
	second := request("POST", "/native/arcade/new", token, body, 200)["id"].(string)
	if removed := request("DELETE", "/native/arcade/draft?id="+second, token, "", 200); removed["deleted"] != true || removed["id"] != second {
		t.Fatal("delete result changed", removed)
	}
	request("GET", "/native/arcade/draft?id="+second, token, "", 404)
	beforeOverflow := count("arcade")
	request("POST", "/native/arcade/new", token, `{"name":"`+strings.Repeat("x", 4096)+`"}`, 413)
	if count("arcade") != beforeOverflow {
		t.Fatal("oversized create persisted")
	}
	creator.Set("withdrawn", true)
	if err := app.Save(creator); err != nil {
		t.Fatal(err)
	}
	request("GET", "/native/arcade/drafts", token, "", 401)
}
