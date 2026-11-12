package arcade_test

import (
	"bytes"
	"encoding/json"
	"image"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/xp"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func nativePhotoUpload(t *testing.T, app *tests.TestApp, arcade, token string, files []uploadTestFile, status int) map[string]any {
	t.Helper()
	var data bytes.Buffer
	form := multipart.NewWriter(&data)
	for key, value := range map[string]string{"arcade": arcade, "captured_at": string(mustJSONPhoto(t, make([]string, len(files)))), "createdBy": "ignored-actor", "future_upload_hint": "ignored"} {
		if err := form.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range files {
		part, err := form.CreateFormFile("photos", file.Filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(file.Content); err != nil {
			t.Fatal(err)
		}
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	res := executeJSONRequest(t, app, "POST", "/arcade/photo/upload", data.String(), map[string]string{"Authorization": "Bearer " + token, "Content-Type": form.FormDataContentType()})
	if res.StatusCode != status {
		t.Fatalf("Native photo upload status %d want %d", res.StatusCode, status)
	}
	if !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("Native upload response is cacheable")
	}
	return decodeJSONMap(t, res)
}
func mustJSONPhoto(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func nativePhotoBytes(t *testing.T, app *tests.TestApp, path, token string, status int, cache string) []byte {
	t.Helper()
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	res := executeJSONRequest(t, app, "GET", path, "", headers)
	defer res.Body.Close()
	if res.StatusCode != status {
		t.Fatalf("photo bytes status %d want %d", res.StatusCode, status)
	}
	if cache != "" && res.Header.Get("Cache-Control") != cache {
		t.Fatal("photo file cache policy changed")
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func nativePhotoState(t *testing.T, app core.App, owner string) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, name := range []string{"arcade_photo_atoms", "arcade_photo", "arcade_changelog", "user_level_log"} {
		n, err := app.CountRecords(name)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = int(n)
	}
	exp, err := xp.LoadCurrentExp(app, owner)
	if err != nil {
		t.Fatal(err)
	}
	out["xp"] = exp
	return out
}
func exportNativePhotoFixture(t *testing.T, name string, value any) {
	t.Helper()
	dir := os.Getenv("MUSECAT_CANONICAL_PHOTO_FIXTURES")
	if dir == "" {
		return
	}
	raw := mustJSONPhoto(t, value)
	var obj any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"\"password\"", "\"access_token\"", "\"refresh_token\"", "\"email\""} {
		if bytes.Contains(bytes.ToLower(raw), []byte(key)) {
			t.Fatal("credential field in photo domain fixture")
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestCanonicalNativePhotosKeepActualMultipartWireOrderAndPublicationXP(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, owner)
	arcade, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Synthetic photo venue", Address: "Synthetic road", Location: location{Lat: 37.5665, Lon: 126.978}})
	partial := nativePhotoUpload(t, app, arcade, session.AccessToken, []uploadTestFile{{"first.png", pngFixtureBytes()}, {"second.png", pngFixtureBytes()}, {"unsupported.gif", gifFixtureBytes()}}, 207)
	uploaded := partial["uploaded"].([]any)
	if len(uploaded) != 2 || len(partial["failed"].([]any)) != 1 || partial["summary"].(map[string]any)["total"] != float64(3) {
		t.Fatal("partial upload index contract changed")
	}
	if nativePhotoState(t, app, owner.Id)["xp"] != 0 {
		t.Fatal("upload awarded publication XP")
	}
	ids := []string{uploaded[0].(map[string]any)["atomId"].(string), uploaded[1].(map[string]any)["atomId"].(string)}
	for _, id := range ids {
		atom, err := app.FindRecordById("arcade_photo_atoms", id)
		if err != nil || atom.GetBool("public") || atom.GetString("createdBy") != owner.Id {
			t.Fatal("upload lost authenticated owner or pending status", err)
		}
	}
	exportNativePhotoFixture(t, "photo-upload-partial", partial)
	listed := nativeDraftRequest(t, app, "GET", "/arcade/photo/atoms?arcade="+arcade, session.AccessToken, nil, 200)
	if !reflect.DeepEqual(listed, nativeDraftRequest(t, app, "GET", "/arcade/photo/atoms?arcade="+arcade, web, nil, 200)) {
		t.Fatal("Web/Native atom DTO differs")
	}
	exportNativePhotoFixture(t, "photo-atoms-pending", listed)
	filePath := "/arcade/photo/file?id=" + ids[0]
	original := nativePhotoBytes(t, app, filePath, session.AccessToken, 200, "private, no-store")
	if !bytes.Equal(original, pngFixtureBytes()) || !bytes.Equal(original, nativePhotoBytes(t, app, filePath, web, 200, "private, no-store")) {
		t.Fatal("canonical pending file bytes differ")
	}
	nativePhotoBytes(t, app, filePath, "", 404, "")
	if dir := os.Getenv("MUSECAT_CANONICAL_PHOTO_FIXTURES"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, "photo-file.png"), original, 0600); err != nil {
			t.Fatal(err)
		}
	}
	order := []string{ids[1], ids[0]}
	selected := nativeDraftRequest(t, app, "PUT", "/arcade/photo", session.AccessToken, map[string]any{"arcade": arcade, "photos": order, "createdBy": "ignored-actor", "future_selection": true}, 200)
	if selected["count"] != float64(2) || selected["xp_feedback"].(map[string]any)["diff_exp"] != float64(8) {
		t.Fatal("initial automatic-photo-campaign reward changed")
	}
	saved, err := app.FindRecordById("arcade_photo", selected["photo"].(string))
	if err != nil || saved.GetString("createdBy") != owner.Id || !reflect.DeepEqual(saved.GetStringSlice("photos"), order) {
		t.Fatal("ordered publication lost owner or order", err)
	}
	exportNativePhotoFixture(t, "photo-selection", selected)
	if !bytes.Equal(original, nativePhotoBytes(t, app, filePath, "", 200, "private, max-age=300")) || !bytes.Equal(original, nativePhotoBytes(t, app, filePath, session.AccessToken, 200, "private, max-age=300")) {
		t.Fatal("published guest/Native bytes changed")
	}
	reordered := nativeDraftRequest(t, app, "PUT", "/arcade/photo", web, map[string]any{"arcade": arcade, "photos": ids}, 200)
	if reordered["xp_feedback"].(map[string]any)["diff_exp"] != float64(0) {
		t.Fatal("Web reorder awarded XP")
	}
	latest, err := app.FindRecordById("arcade_photo", reordered["photo"].(string))
	if err != nil || !reflect.DeepEqual(latest.GetStringSlice("photos"), ids) {
		t.Fatal("Web reorder did not persist order", err)
	}
	old, err := app.FindRecordById("arcade_photo", saved.Id)
	if err != nil || !reflect.DeepEqual(old.GetStringSlice("photos"), order) {
		t.Fatal("old gallery revision mutated", err)
	}
	nativeDraftRequest(t, app, "DELETE", "/arcade/photo/atom?id="+ids[0], session.AccessToken, nil, 409)
	after := nativePhotoState(t, app, owner.Id)
	failed := nativePhotoUpload(t, app, arcade, session.AccessToken, []uploadTestFile{{"unsupported.gif", gifFixtureBytes()}}, 422)
	if !reflect.DeepEqual(after, nativePhotoState(t, app, owner.Id)) {
		t.Fatal("all-failed upload wrote atoms or XP")
	}
	exportNativePhotoFixture(t, "photo-upload-failed", failed)
	publishedList := nativeDraftRequest(t, app, "GET", "/arcade/photo/atoms?arcade="+arcade, session.AccessToken, nil, 200)
	exportNativePhotoFixture(t, "photo-atoms-published", publishedList)
}
func TestCanonicalNativePhotoACLKeepsContributorCreatorReviewerAndUploaderPolicy(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUser(t, app)
	_, native := nativeNearbySession(t, app, owner)
	_, foreign := createAuthUser(t, app)
	_, other := nativeNearbySession(t, app, foreign)
	_, reviewer := createAuthUser(t, app)
	reviewer.Set("tags", []string{"moderator"})
	if err := app.Save(reviewer); err != nil {
		t.Fatal(err)
	}
	_, review := nativeNearbySession(t, app, reviewer)
	public, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Public photo policy", Address: "Synthetic", Location: location{Lat: 37.5665, Lon: 126.978}})
	private, _ := seedArcade(t, app, owner.Id, arcadeSeed{Name: "Private photo policy", Address: "Synthetic", Location: location{Lat: 37.5665, Lon: 126.978}})
	pending := seedSizedPhotoAtom(t, app, public, owner.Id, false, 1200, 900)
	hidden := seedSizedPhotoAtom(t, app, private, owner.Id, false, 1, 1)
	nativeDraftRequest(t, app, "GET", "/arcade/photo/atoms?arcade="+public, other.AccessToken, nil, 200)
	nativePhotoBytes(t, app, "/arcade/photo/file?id="+pending, other.AccessToken, 200, "private, no-store")
	nativePhotoBytes(t, app, "/arcade/photo/file?id="+pending, "", 404, "")
	nativeDraftRequest(t, app, "GET", "/arcade/photo/atoms?arcade="+private, other.AccessToken, nil, 404)
	nativePhotoBytes(t, app, "/arcade/photo/file?id="+hidden, other.AccessToken, 404, "")
	for _, token := range []string{native.AccessToken, review.AccessToken} {
		nativeDraftRequest(t, app, "GET", "/arcade/photo/atoms?arcade="+private, token, nil, 200)
		nativePhotoBytes(t, app, "/arcade/photo/file?id="+hidden, token, 200, "private, no-store")
	}
	nativeDraftRequest(t, app, "DELETE", "/arcade/photo/atom?id="+pending, other.AccessToken, nil, 403)
	nativeDraftRequest(t, app, "DELETE", "/arcade/photo/atom?id="+pending, review.AccessToken, nil, 200)
	deletion := nativeDraftRequest(t, app, "DELETE", "/arcade/photo/atom?id="+hidden, native.AccessToken, nil, 200)
	exportNativePhotoFixture(t, "photo-delete", deletion)
	thumbID := seedSizedPhotoAtom(t, app, public, owner.Id, true, 1200, 900)
	thumb := nativePhotoBytes(t, app, "/arcade/photo/file?id="+thumbID+"&thumb=96x96", native.AccessToken, 200, "private, max-age=300")
	decoded, _, err := image.Decode(bytes.NewReader(thumb))
	if err != nil || decoded.Bounds().Dx() != 96 || decoded.Bounds().Dy() != 96 {
		t.Fatal("Native thumbnail lost canonical dimensions", err)
	}
	if !bytes.Equal(thumb, nativePhotoBytes(t, app, "/arcade/photo/file?id="+thumbID+"&thumb=96x96", web, 200, "private, max-age=300")) {
		t.Fatal("Web/Native thumbnail differs")
	}
	nativePhotoBytes(t, app, "/arcade/photo/file?id="+thumbID+"&thumb=48x0", native.AccessToken, 400, "")
	nativePhotoBytes(t, app, "/arcade/photo/file?id=../invalid", native.AccessToken, 404, "")
}
func TestCanonicalNativePhotoCredentialsFailClosedWithoutCookieOrGuestFallback(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, owner := createAuthUser(t, app)
	service, session := nativeNearbySession(t, app, owner)
	arcade, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Auth photo policy", Address: "Synthetic", Location: location{Lat: 37.5665, Lon: 126.978}})
	public := seedSizedPhotoAtom(t, app, arcade, owner.Id, true, 1, 1)
	pending := seedSizedPhotoAtom(t, app, arcade, owner.Id, false, 1, 1)
	ops := []struct {
		method, path string
		body         any
	}{{"GET", "/arcade/photo/atoms?arcade=" + arcade, nil}, {"GET", "/arcade/photo/file?id=" + public, nil}, {"POST", "/arcade/photo/upload", map[string]any{}}, {"DELETE", "/arcade/photo/atom?id=" + pending, nil}, {"PUT", "/arcade/photo", map[string]any{"arcade": arcade, "photos": []string{pending}}}}
	reject := func(token string, status int) {
		t.Helper()
		for _, op := range ops {
			res := executeJSONRequest(t, app, op.method, op.path, string(mustJSONPhoto(t, op.body)), map[string]string{"Authorization": "Bearer " + token})
			res.Body.Close()
			if res.StatusCode != status {
				t.Fatal("invalid Native photo credential reached an operation")
			}
		}
	}
	before := nativePhotoState(t, app, owner.Id)
	reject(strings.Repeat("z", 43), 401)
	for _, op := range ops {
		res := executeJSONRequest(t, app, op.method, op.path, string(mustJSONPhoto(t, op.body)), map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web})
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatal("invalid Native token fell back to Web cookie or public file")
		}
	}
	for _, change := range []struct{ deny, restore string }{{"access_expires=0", "access_expires=unixepoch()+600"}, {"expires=0", "expires=unixepoch()+3600"}, {"client='removed-client'", "client='app.musecat.native.dev'"}} {
		for i, sql := range []string{change.deny, change.restore} {
			if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + sql + " WHERE user_id={:user}").Bind(dbx.Params{"user": owner.Id}).Execute(); err != nil {
				t.Fatal(err)
			}
			if i == 0 {
				reject(session.AccessToken, 401)
			}
		}
	}
	owner.Set("withdrawn", true)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	reject(session.AccessToken, 401)
	owner.Set("withdrawn", false)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	seedUserBan(t, app, owner.Id, "", "native_photo_fixture", time.Now().Add(time.Hour))
	reject(session.AccessToken, 403)
	// The existing public blob route has no new Web active-account policy added.
	nativePhotoBytes(t, app, "/arcade/photo/file?id="+public, web, 200, "private, max-age=300")
	if err := service.Revoke(app, session.RefreshToken); err != nil {
		t.Fatal(err)
	}
	reject(session.AccessToken, 401)
	if !reflect.DeepEqual(before, nativePhotoState(t, app, owner.Id)) {
		t.Fatal("denied photo operations changed business state")
	}
	disabled := nativeNearbyApp(t, false)
	_, disabledOwner := createAuthUser(t, disabled)
	s, issued := nativeNearbySession(t, disabled, disabledOwner)
	if actor, err := s.Authenticate(disabled, issued.AccessToken); err != nil || actor.Id != disabledOwner.Id {
		t.Fatal("disabled fixture lacks valid same-DB session")
	}
	id, _ := seedPublicArcade(t, disabled, disabledOwner.Id, arcadeSeed{Name: "Disabled photos", Address: "Synthetic", Location: location{Lat: 37.5665, Lon: 126.978}})
	pub := seedSizedPhotoAtom(t, disabled, id, disabledOwner.Id, true, 1, 1)
	priv := seedSizedPhotoAtom(t, disabled, id, disabledOwner.Id, false, 1, 1)
	nativePhotoBytes(t, disabled, "/arcade/photo/file?id="+pub, issued.AccessToken, 200, "private, max-age=300")
	nativePhotoBytes(t, disabled, "/arcade/photo/file?id="+priv, issued.AccessToken, 404, "")
	for _, op := range ops {
		if strings.Contains(op.path, "/photo/file") {
			continue
		}
		nativeDraftRequest(t, disabled, op.method, op.path, issued.AccessToken, op.body, 401, false)
	}
}
func TestCanonicalNativePhotosKeepUploadBodyFileAndCountLimits(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, owner := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, owner)
	id, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Photo limits", Address: "Synthetic", Location: location{Lat: 37.5665, Lon: 126.978}})
	eleven := make([]uploadTestFile, 11)
	for i := range eleven {
		eleven[i] = uploadTestFile{"synthetic.png", pngFixtureBytes()}
	}
	nativePhotoUpload(t, app, id, session.AccessToken, eleven, 400)
	for _, sample := range []struct{ size, status int }{{20_000_000, 200}, {20_000_001, 422}} {
		content := append(append([]byte{}, pngFixtureBytes()...), make([]byte, sample.size-len(pngFixtureBytes()))...)
		before := nativePhotoState(t, app, owner.Id)
		out := nativePhotoUpload(t, app, id, session.AccessToken, []uploadTestFile{{"synthetic.png", content}}, sample.status)
		if sample.status == 422 && (out["summary"].(map[string]any)["success"] != float64(0) || !reflect.DeepEqual(before, nativePhotoState(t, app, owner.Id))) {
			t.Fatal("oversized photo persisted atom or XP")
		}
	}
	router, err := apis.NewRouter(app)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.OnServe().Trigger(&core.ServeEvent{App: app, Router: router}, func(e *core.ServeEvent) error { return e.Next() }); err != nil {
		t.Fatal(err)
	}
	mux, err := router.BuildMux()
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		size   int64
		status int
	}{{220 << 20, 400}, {(220 << 20) + 1, 413}} {
		req := httptest.NewRequest("POST", "/arcade/photo/upload", strings.NewReader("{}"))
		req.ContentLength = sample.size
		req.Header.Set("Authorization", "Bearer "+session.AccessToken)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		mux.ServeHTTP(res, req)
		if res.Code != sample.status {
			t.Fatal("registered upload body boundary changed")
		}
	}
}
func TestCanonicalNativePhotosKeepRateLimitsAndUnselectedFlagFileScope(t *testing.T) {
	app := nativeNearbyApp(t, true)
	_, owner := createAuthUser(t, app)
	_, session := nativeNearbySession(t, app, owner)
	for _, op := range []struct{ method, path string }{{"GET", "/arcade/photo/atoms"}, {"GET", "/arcade/photo/file"}, {"POST", "/arcade/photo/upload"}, {"DELETE", "/arcade/photo/atom"}, {"PUT", "/arcade/photo"}} {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: op.method + " " + op.path, MaxRequests: 1, Duration: 60}}}
		nativeDraftRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 400)
		nativeDraftRequest(t, app, op.method, op.path, session.AccessToken, map[string]any{}, 429)
	}
	for _, op := range []struct {
		method, path string
		status       int
	}{{"GET", "/arcade/flag/photo", 404}, {"GET", "/native/arcade/photo/file", 404}, {"POST", "/native/arcade/photo/upload", 404}, {"POST", "/campaign/end", 401}, {"GET", "/api/collections/arcade_photo_atoms/records", 403}} {
		nativeDraftRequest(t, app, op.method, op.path, session.AccessToken, nil, op.status, false)
	}
}
