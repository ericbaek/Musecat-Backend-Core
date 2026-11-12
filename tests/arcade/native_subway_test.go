package arcade_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/tests"
)

func nativeSubwayMultipart(t *testing.T, fields map[string]string, files map[string][]byte) ([]byte, string) {
	t.Helper()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	for k, v := range files {
		p, err := w.CreateFormFile(k, k+".svg")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.Write(v); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes(), w.FormDataContentType()
}
func nativeSubwayRequest(t *testing.T, app *tests.TestApp, method, path, token, cookie string, fields map[string]string, files map[string][]byte, want int) map[string]any {
	t.Helper()
	headers := map[string]string{}
	body := ""
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if cookie != "" {
		headers["Cookie"] = "pb_auth=" + cookie
	}
	if fields != nil || files != nil {
		raw, ct := nativeSubwayMultipart(t, fields, files)
		body = string(raw)
		headers["Content-Type"] = ct
	}
	res := executeJSONRequest(t, app, method, path, body, headers)
	defer res.Body.Close()
	if res.StatusCode != want {
		t.Fatalf("%s %s status %d want %d", method, path, res.StatusCode, want)
	}
	if want < 400 && len(token) == 43 && !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("native subway response cacheable")
	}
	var out map[string]any
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}
func nativeSubwayState(t *testing.T, app *tests.TestApp) string {
	t.Helper()
	out := map[string]any{}
	for _, name := range []string{"subwayMap", "user_level_log", "arcade_changelog"} {
		rows, err := app.FindAllRecords(name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = rows
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func exportNativeSubway(t *testing.T, name string, value any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_SUBWAY_FIXTURES")
	if dir == "" {
		return
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"password", "email", "access_token", "refresh_token"} {
		if strings.Contains(strings.ToLower(string(raw)), `"`+key+`"`) {
			t.Fatal("credential in subway fixture")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}

var nativeSubwaySVG = []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><path d="M0 0h10v10H0z"/></svg>`)

func TestCanonicalNativeSubwayActualLifecycleWebParityAndDownloads(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, u := createAuthUserWithTags(t, app, []string{"developer"})
	_, s := nativeNearbySession(t, app, u)
	old := nativeSubwayRequest(t, app, "POST", "/subway/map", web, "", map[string]string{"region": "center", "releaseDate": "2025-01-01"}, map[string][]byte{"lightSVG": nativeSubwaySVG}, 201)
	nativeSubwayRequest(t, app, "POST", "/subway/map", web, "", map[string]string{"region": "busan", "releaseDate": "2026-01-01"}, map[string][]byte{"lightSVG": nativeSubwaySVG}, 201)
	created := nativeSubwayRequest(t, app, "POST", "/subway/map", s.AccessToken, "", map[string]string{"region": "center", "releaseDate": "2026-02-01", "changelog": `["Synthetic canonical subway"]`}, map[string][]byte{"lightSVG": nativeSubwaySVG}, 201)
	exportNativeSubway(t, "subway-create", created)
	id := created["id"].(string)
	history := nativeSubwayRequest(t, app, "GET", "/subway/map/history?region=center", s.AccessToken, "", nil, nil, 200)
	if !reflect.DeepEqual(history, nativeSubwayRequest(t, app, "GET", "/subway/map/history?region=center", web, "", nil, nil, 200)) {
		t.Fatal("Web/Native history differs")
	}
	items := history["items"].([]any)
	if history["total"] != float64(2) || items[0].(map[string]any)["id"] != id || items[1].(map[string]any)["id"] != old["id"] {
		t.Fatal("regional newest-first history changed")
	}
	exportNativeSubway(t, "subway-history", history)
	updated := nativeSubwayRequest(t, app, "PUT", "/subway/map", s.AccessToken, "", map[string]string{"id": id, "changelog": `[]`}, map[string][]byte{"darkSVG": nativeSubwaySVG}, 200)
	if updated["region"] != created["region"] || updated["release_date"] != created["release_date"] || !reflect.DeepEqual(updated["light_svg"], created["light_svg"]) || updated["dark_svg"] == nil || len(updated["changelog"].([]any)) != 0 {
		t.Fatal("omitted fields/assets not preserved")
	}
	exportNativeSubway(t, "subway-update", updated)
	for _, field := range []string{"light_svg", "dark_svg"} {
		asset := updated[field].(map[string]any)
		res := executeJSONRequest(t, app, "GET", asset["file_url"].(string), "", nil)
		raw, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || res.StatusCode != 200 || !bytes.Equal(raw, nativeSubwaySVG) {
			t.Fatal("public asset bytes changed")
		}
	}
	current := nativeSubwayRequest(t, app, "GET", "/subway/map?region=center", "", "", nil, nil, 200)
	if current["id"] != id {
		t.Fatal("new version not current")
	}
	deleted := nativeSubwayRequest(t, app, "DELETE", "/subway/map?id="+id, s.AccessToken, "", nil, nil, 200)
	if deleted["id"] != id || deleted["deleted"] != true {
		t.Fatal("delete identity changed")
	}
	exportNativeSubway(t, "subway-delete", deleted)
	current = nativeSubwayRequest(t, app, "GET", "/subway/map?region=center", "", "", nil, nil, 200)
	if current["id"] != old["id"] {
		t.Fatal("hard-delete did not reveal prior version")
	}
}
func TestCanonicalNativeSubwayRolesAndRejectedRequestsDoNotMutate(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, admin := createAuthUserWithTags(t, app, []string{"developer"})
	_, adminS := nativeNearbySession(t, app, admin)
	row := nativeSubwayRequest(t, app, "POST", "/subway/map", web, "", map[string]string{"region": "center", "releaseDate": "2026-01-01"}, map[string][]byte{"lightSVG": nativeSubwaySVG}, 201)
	id := row["id"].(string)
	for _, tags := range [][]string{nil, {"supporter"}, {"designer"}, {"moderator"}, {"developer"}} {
		w, u := createAuthUserWithTags(t, app, tags)
		_, s := nativeNearbySession(t, app, u)
		want := 403
		if len(tags) > 0 && (tags[0] == "moderator" || tags[0] == "developer") {
			want = 200
		}
		for _, token := range []string{w, s.AccessToken} {
			before := nativeSubwayState(t, app)
			nativeSubwayRequest(t, app, "GET", "/subway/map/history", token, "", nil, nil, want)
			if want == 403 {
				for _, method := range []string{"POST", "PUT", "DELETE"} {
					nativeSubwayRequest(t, app, method, "/subway/map?id="+id, token, "", map[string]string{"id": id}, nil, 403)
				}
			}
			if before != nativeSubwayState(t, app) {
				t.Fatal("denied/readonly role changed domain state")
			}
		}
	}
	before := nativeSubwayState(t, app)
	for _, request := range []struct{ method, path string }{{"GET", "/subway/map/history"}, {"POST", "/subway/map"}, {"PUT", "/subway/map"}, {"DELETE", "/subway/map?id=" + id}} {
		for _, token := range []string{"", strings.Repeat("z", 43)} {
			nativeSubwayRequest(t, app, request.method, request.path, token, web, map[string]string{"id": id}, nil, 401)
		}
	}
	for _, test := range []struct {
		fields map[string]string
		files  map[string][]byte
	}{{map[string]string{"region": "unknown", "releaseDate": "2026-01-01"}, map[string][]byte{"lightSVG": nativeSubwaySVG}}, {map[string]string{"region": "center", "releaseDate": "2026-02-30"}, map[string][]byte{"lightSVG": nativeSubwaySVG}}, {map[string]string{"region": "center", "releaseDate": "2026-01-01"}, nil}, {map[string]string{"region": "center", "releaseDate": "2026-01-01"}, map[string][]byte{"lightSVG": []byte("invalid SVG")}}, {map[string]string{"region": "center", "releaseDate": "2026-01-01"}, map[string][]byte{"lightSVG": bytes.Repeat([]byte("x"), (5<<20)+1)}}} {
		nativeSubwayRequest(t, app, "POST", "/subway/map", adminS.AccessToken, "", test.fields, test.files, 400)
	}
	if before != nativeSubwayState(t, app) {
		t.Fatal("invalid auth/input changed domain state")
	}
}
func TestCanonicalNativeSubwayAccountLifetimeAndDisabledRuntime(t *testing.T) {
	for _, mode := range []string{"disabled", "revoked", "withdrawn", "expired", "removed-client", "ban"} {
		t.Run(mode, func(t *testing.T) {
			app := nativeNearbyApp(t, mode != "disabled")
			web, u := createAuthUserWithTags(t, app, []string{"developer"})
			service, s := nativeNearbySession(t, app, u)
			if owner, err := service.Authenticate(app, s.AccessToken); err != nil || owner.Id != u.Id {
				t.Fatal("fixture session invalid")
			}
			switch mode {
			case "revoked":
				if err := service.Revoke(app, s.RefreshToken); err != nil {
					t.Fatal(err)
				}
			case "withdrawn":
				u.Set("withdrawn", true)
				if err := app.Save(u); err != nil {
					t.Fatal(err)
				}
			case "expired", "removed-client":
				clause := "access_expires=0"
				if mode == "removed-client" {
					clause = "client='removed-client'"
				}
				if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + clause + " WHERE user_id={:user}").Bind(dbx.Params{"user": u.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
			case "ban":
				seedUserBan(t, app, u.Id, "", "subway_canary", time.Now().Add(time.Hour))
			}
			before := nativeSubwayState(t, app)
			want := 401
			if mode == "ban" {
				want = 403
			}
			for _, r := range []struct{ method, path string }{{"GET", "/subway/map/history"}, {"POST", "/subway/map"}, {"PUT", "/subway/map"}, {"DELETE", "/subway/map?id=synthetic"}} {
				nativeSubwayRequest(t, app, r.method, r.path, s.AccessToken, "", nil, nil, want)
			}
			if before != nativeSubwayState(t, app) {
				t.Fatal("inactive session mutated subway domain")
			}
			if mode == "disabled" {
				nativeSubwayRequest(t, app, "GET", "/subway/map/history", web, "", nil, nil, 200)
			}
		})
	}
}
func TestCanonicalNativeSubwayExactRequestLimit(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, u := createAuthUserWithTags(t, app, []string{"developer"})
	_, s := nativeNearbySession(t, app, u)
	before := nativeSubwayState(t, app)
	for _, method := range []string{"POST", "PUT"} {
		for _, n := range []int{24 << 20, (24 << 20) + 1} {
			raw, ct := nativeSubwayMultipart(t, map[string]string{"unused": ""}, nil)
			body := strings.Replace(string(raw), "\r\n\r\n\r\n", "\r\n\r\n"+strings.Repeat("x", n-len(raw))+"\r\n", 1)
			if len(body) != n {
				t.Fatal("request boundary fixture wrong")
			}
			res := executeJSONRequest(t, app, method, "/subway/map", body, map[string]string{"Authorization": "Bearer " + s.AccessToken, "Content-Type": ct})
			res.Body.Close()
			want := 400
			if n > 24<<20 {
				want = 413
			}
			if res.StatusCode != want {
				t.Fatalf("%s bytes%d got%d want%d", method, n, res.StatusCode, want)
			}
		}
	}
	if before != nativeSubwayState(t, app) {
		t.Fatal("oversized request changed domain")
	}
}

func TestCanonicalNativeSubwayFileLimitAndDeniedUpdatePreserveAssets(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, u := createAuthUserWithTags(t, app, []string{"moderator"})
	_, s := nativeNearbySession(t, app, u)
	exact := append(append([]byte{}, nativeSubwaySVG...), bytes.Repeat([]byte(" "), (5<<20)-len(nativeSubwaySVG))...)
	created := nativeSubwayRequest(t, app, "POST", "/subway/map", s.AccessToken, "", map[string]string{"region": "center", "releaseDate": "2026-01-01"}, map[string][]byte{"lightSVG": exact}, 201)
	id := created["id"].(string)
	before := nativeSubwayState(t, app)
	for _, file := range [][]byte{append(append([]byte{}, exact...), ' '), []byte("not SVG")} {
		nativeSubwayRequest(t, app, "PUT", "/subway/map", s.AccessToken, "", map[string]string{"id": id, "region": "busan"}, map[string][]byte{"lightSVG": file}, 400)
	}
	nativeSubwayRequest(t, app, "GET", "/subway/map/history?region=invalid", s.AccessToken, "", nil, nil, 400)
	nativeSubwayRequest(t, app, "DELETE", "/subway/map?id=missing", s.AccessToken, "", nil, nil, 404)
	if before != nativeSubwayState(t, app) {
		t.Fatal("rejected update changed map metadata or side effects")
	}
	res := executeJSONRequest(t, app, "GET", created["light_svg"].(map[string]any)["file_url"].(string), "", nil)
	raw, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || res.StatusCode != 200 || !bytes.Equal(raw, exact) {
		t.Fatal("rejected update replaced old file")
	}
}
