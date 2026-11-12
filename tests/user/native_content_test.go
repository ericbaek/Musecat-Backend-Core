package user_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"mime/multipart"
	"reflect"
	"strings"
	"testing"
	"time"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func nativeContentDocument(text string) map[string]any {
	return map[string]any{"type": "doc", "content": []any{map[string]any{"type": "customExtension", "attrs": map[string]any{"custom": true}, "content": []any{map[string]any{"type": "text", "text": text}}}}}
}

func TestCanonicalNativeMemoKeepsWebWirePrivateAccessAndImmutableNoOp(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	setUserLevelForCountriesTest(t, app, owner.Id, 0)
	foreignWeb, foreign := createAuthUser(t, app, true)
	token := nativeLoginIssue(t, app, web).AccessToken
	foreignToken := nativeLoginIssue(t, app, foreignWeb).AccessToken
	arcade := nativeEditorArcade(t, app, owner.Id)
	document := nativeContentDocument("Canonical memo")
	body := map[string]any{"arcade": arcade.Id, "document": document, "user": foreign.Id}
	first := decodeJSON(t, nativeReadRequest(t, app, "PUT", "/arcade/memo", token, body, 200))
	memo := first["memo"].(map[string]any)
	if first["changed"] != true || memo["by"] != owner.Id || !reflect.DeepEqual(memo["document"], document) {
		t.Fatal("memo lost canonical owner/document")
	}
	exportCanonicalReadFixture(t, "content-memo-write", first)
	before := editorState(t, app, arcade.Id, owner.Id, "memo")
	noOp := decodeJSON(t, nativeReadRequest(t, app, "PUT", "/arcade/memo", web, body, 200))
	nativeNoOp := decodeJSON(t, nativeReadRequest(t, app, "PUT", "/arcade/memo", token, body, 200))
	if noOp["changed"] != false || !reflect.DeepEqual(noOp, nativeNoOp) || !reflect.DeepEqual(before, editorState(t, app, arcade.Id, owner.Id, "memo")) {
		t.Fatal("memo retry changed wire, history or XP")
	}
	exportCanonicalReadFixture(t, "content-memo-noop", nativeNoOp)
	body["document"] = nativeContentDocument("Second canonical memo")
	nativeReadRequest(t, app, "PUT", "/arcade/memo", web, body, 200)
	saved, err := app.FindRecordById("arcade_memo", memo["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(saved.Get("document"))
	if err != nil {
		t.Fatal(err)
	}
	var original map[string]any
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, document) {
		t.Fatal("old memo revision changed")
	}
	private := nativeEditorArcade(t, app, owner.Id)
	private.Set("public", false)
	if err := app.Save(private); err != nil {
		t.Fatal(err)
	}
	body["arcade"] = private.Id
	for _, credential := range []string{foreignWeb, foreignToken} {
		nativeReadRequest(t, app, "PUT", "/arcade/memo", credential, body, 403)
	}
	nativeReadRequest(t, app, "PUT", "/arcade/memo", token, body, 200)
	foreign.Set("tags", []string{"moderator"})
	if err := app.Save(foreign); err != nil {
		t.Fatal(err)
	}
	nativeReadRequest(t, app, "PUT", "/arcade/memo", foreignToken, body, 200)
	before = editorState(t, app, private.Id, owner.Id, "memo")
	body["document"] = map[string]any{"type": "doc", "content": []any{map[string]any{"type": "heading", "attrs": map[string]any{"level": 7}}}}
	nativeReadRequest(t, app, "PUT", "/arcade/memo", token, body, 400)
	if !reflect.DeepEqual(before, editorState(t, app, private.Id, owner.Id, "memo")) {
		t.Fatal("invalid document changed memo/history/XP")
	}
}

func TestCanonicalNativeNoticeMultipartPartialWebParityAndSoftDelete(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	arcade := nativeEditorArcade(t, app, owner.Id)
	owner.Set("tags", []string{"arcade_owner"})
	owner.Set("owns", []string{arcade.Id})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	token := nativeLoginIssue(t, app, web).AccessToken
	document := nativeContentDocument("Canonical notice")
	var data bytes.Buffer
	writer := multipart.NewWriter(&data)
	doc, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"arcade": arcade.Id, "title": "Canonical notice", "document": string(doc), "display_mode": "normal"} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := writer.CreateFormFile("photos", "synthetic.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	response := doUserRequest(t, app, "POST", "/arcade/notice", map[string]string{"Authorization": "Bearer " + token, "Content-Type": writer.FormDataContentType()}, data.String())
	if response.StatusCode != 200 || !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("native multipart notice status %d", response.StatusCode)
	}
	created := decodeJSON(t, response)
	if created["createdBy"] != owner.Id || !reflect.DeepEqual(created["document"], document) || len(created["photos"].([]any)) != 1 {
		t.Fatal("notice media, author or extension document lost")
	}
	exportCanonicalReadFixture(t, "content-notice-create", created)
	id := created["id"].(string)
	update := map[string]any{"id": id, "title": "Updated canonical notice"}
	webReceipt := decodeJSON(t, nativeReadRequest(t, app, "PUT", "/arcade/notice", web, update, 200))
	nativeReceipt := decodeJSON(t, nativeReadRequest(t, app, "PUT", "/arcade/notice", token, update, 200))
	exportCanonicalReadFixture(t, "content-notice-update", nativeReceipt)
	delete(webReceipt, "updated")
	delete(nativeReceipt, "updated")
	if !reflect.DeepEqual(webReceipt, nativeReceipt) || !reflect.DeepEqual(nativeReceipt["photos"], created["photos"]) || !reflect.DeepEqual(nativeReceipt["document"], document) {
		t.Fatal("partial notice update changed omitted fields or web wire")
	}
	nativeDelete := decodeJSON(t, nativeReadRequest(t, app, "DELETE", "/arcade/notice", token, map[string]any{"id": id}, 200))
	webDelete := decodeJSON(t, nativeReadRequest(t, app, "DELETE", "/arcade/notice", web, map[string]any{"id": id}, 200))
	if !reflect.DeepEqual(nativeDelete, webDelete) || !reflect.DeepEqual(nativeDelete, map[string]any{"id": id, "delete": true}) {
		t.Fatal("notice delete wire differs")
	}
	exportCanonicalReadFixture(t, "content-notice-delete", nativeDelete)
	saved, err := app.FindRecordById("arcade_notice", id)
	if err != nil || !saved.GetBool("delete") || len(saved.GetStringSlice("photos")) != 1 {
		t.Fatal("delete did not preserve soft-deleted content", err)
	}
	for _, collection := range []string{"arcade_changelog", "user_level_log"} {
		if n, err := app.CountRecords(collection); err != nil || n != 0 {
			t.Fatal("notice wrote arcade history or XP", err)
		}
	}
}

func TestCanonicalNativeNoticeKeepsRolesOwnershipAndDisplayConflict(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	arcade := nativeEditorArcade(t, app, owner.Id)
	token := nativeLoginIssue(t, app, web).AccessToken
	body := map[string]any{"arcade": arcade.Id, "title": "Canonical notice", "document": nativeContentDocument("Role policy"), "display_mode": "required"}
	for _, credential := range []string{web, token} {
		nativeReadRequest(t, app, "POST", "/arcade/notice", credential, body, 403)
	}
	owner.Set("tags", []string{"supporter"})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	setUserLevelForCountriesTest(t, app, owner.Id, userhandler.LevelBaseExp(29))
	for _, credential := range []string{web, token} {
		nativeReadRequest(t, app, "POST", "/arcade/notice", credential, body, 403)
	}
	setUserLevelForCountriesTest(t, app, owner.Id, userhandler.LevelBaseExp(30))
	created := decodeJSON(t, nativeReadRequest(t, app, "POST", "/arcade/notice", token, body, 200))
	for _, credential := range []string{web, token} {
		overflow := decodeJSON(t, nativeReadRequest(t, app, "POST", "/arcade/notice", credential, body, 409))
		if overflow["code"] != "notice_display_limit_exceeded" || overflow["limit"] != float64(1) {
			t.Fatal("display conflict details changed")
		}
	}
	body["display_mode"] = "normal"
	normal := decodeJSON(t, nativeReadRequest(t, app, "POST", "/arcade/notice", web, body, 200))
	nativeReadRequest(t, app, "PUT", "/arcade/notice", token, map[string]any{"id": normal["id"], "display_mode": "required", "title": "Rejected promotion"}, 409)
	saved, err := app.FindRecordById("arcade_notice", normal["id"].(string))
	if err != nil || saved.GetString("title") != "Canonical notice" || saved.GetString("display_mode") != "normal" {
		t.Fatal("conflict partially mutated notice", err)
	}
	officialWeb, official := createAuthUser(t, app, true)
	official.Set("tags", []string{"arcade_owner"})
	official.Set("owns", []string{arcade.Id})
	if err := app.Save(official); err != nil {
		t.Fatal(err)
	}
	officialToken := nativeLoginIssue(t, app, officialWeb).AccessToken
	for _, credential := range []string{web, token} {
		nativeReadRequest(t, app, "POST", "/arcade/notice", credential, body, 403)
	}
	for _, credential := range []string{officialWeb, officialToken} {
		nativeReadRequest(t, app, "PUT", "/arcade/notice", credential, map[string]any{"id": created["id"], "title": "Foreign author"}, 403)
		nativeReadRequest(t, app, "DELETE", "/arcade/notice", credential, map[string]any{"id": created["id"]}, 403)
	}
	ownNotice := decodeJSON(t, nativeReadRequest(t, app, "POST", "/arcade/notice", officialToken, body, 200))
	foreignArcade := nativeEditorArcade(t, app, owner.Id)
	body["arcade"] = foreignArcade.Id
	nativeReadRequest(t, app, "POST", "/arcade/notice", officialToken, body, 403)
	// Official ownership does not bypass a foreign private draft's creator gate.
	official.Set("owns", []string{arcade.Id, foreignArcade.Id})
	if err := app.Save(official); err != nil {
		t.Fatal(err)
	}
	foreignArcade.Set("public", false)
	if err := app.Save(foreignArcade); err != nil {
		t.Fatal(err)
	}
	nativeReadRequest(t, app, "POST", "/arcade/notice", officialToken, body, 403)
	foreignArcade.Set("createdBy", official.Id)
	if err := app.Save(foreignArcade); err != nil {
		t.Fatal(err)
	}
	privateNotice := decodeJSON(t, nativeReadRequest(t, app, "POST", "/arcade/notice", officialToken, body, 200))
	nativeReadRequest(t, app, "GET", "/arcade/notice?arcade="+foreignArcade.Id, officialToken, nil, 404)
	// Native official callers keep the existing authorless-legacy exception.
	legacy, err := app.FindRecordById("arcade_notice", privateNotice["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	legacy.Set("createdBy", "")
	if err := app.Save(legacy); err != nil {
		t.Fatal(err)
	}
	repaired := decodeJSON(t, nativeReadRequest(t, app, "PUT", "/arcade/notice", officialToken, map[string]any{"id": legacy.Id, "title": "Legacy repair"}, 200))
	if repaired["createdBy"] != "" {
		t.Fatal("legacy notice author was fabricated")
	}
	nativeReadRequest(t, app, "DELETE", "/arcade/notice", officialToken, map[string]any{"id": legacy.Id}, 200)
	owner.Set("tags", []string{"moderator"})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	nativeReadRequest(t, app, "PUT", "/arcade/notice", token, map[string]any{"id": ownNotice["id"], "title": "Staff correction"}, 200)
	nativeReadRequest(t, app, "DELETE", "/arcade/notice", token, map[string]any{"id": ownNotice["id"]}, 200)
	// Once an authored notice exists, denied mutation must not alter its author or state.
	saved, err = app.FindRecordById("arcade_notice", created["id"].(string))
	if err != nil || saved.GetBool("delete") || saved.GetString("createdBy") != owner.Id || saved.GetString("title") != "Canonical notice" {
		t.Fatal("denied foreign mutation changed notice", err)
	}
}

func TestCanonicalNativeContentRejectsInvalidSessionsLimitsAndUnselectedRoutes(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	owner.Set("tags", []string{"moderator"})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	credentials := nativeLoginIssue(t, app, web)
	arcade := nativeEditorArcade(t, app, owner.Id)
	create := map[string]any{"arcade": arcade.Id, "title": "Original", "document": nativeContentDocument("Protected notice")}
	notice := decodeJSON(t, nativeReadRequest(t, app, "POST", "/arcade/notice", web, create, 200))
	ops := []struct {
		method, path string
		body         any
	}{
		{"PUT", "/arcade/memo", map[string]any{"arcade": arcade.Id, "document": nativeContentDocument("Protected memo")}},
		{"POST", "/arcade/notice", create},
		{"PUT", "/arcade/notice", map[string]any{"id": notice["id"], "title": "Denied change"}},
		{"DELETE", "/arcade/notice", map[string]any{"id": notice["id"]}},
	}
	reject := func(token string, status int) {
		t.Helper()
		for _, op := range ops {
			nativeReadRequest(t, app, op.method, op.path, token, op.body, status)
		}
	}
	reject("", 401)
	reject(strings.Repeat("z", 43), 401)
	for _, op := range ops {
		payload, err := json.Marshal(op.body)
		if err != nil {
			t.Fatal(err)
		}
		response := doUserRequest(t, app, op.method, op.path, map[string]string{"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web}, string(payload))
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("invalid bearer fell back to a cookie")
		}
	}
	for _, method := range []string{"POST", "PUT"} {
		response := doUserRequest(t, app, method, "/arcade/notice", map[string]string{"Authorization": "Bearer " + credentials.AccessToken}, strings.Repeat(" ", (16<<20)+1))
		response.Body.Close()
		if response.StatusCode != 413 {
			t.Fatal("notice body limit changed")
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
	seedUserBanForUserTest(t, app, owner.Id, hashNormalizedEmailForUserTest(owner.Email()), "native_content_fixture", time.Now().Add(time.Hour))
	reject(credentials.AccessToken, 403)
	reject(web, 403)
	nativeLoginRequest(t, app, "POST", "/native/session/revoke", "", map[string]string{"refresh_token": credentials.RefreshToken}, 204)
	reject(credentials.AccessToken, 401)
	saved, err := app.FindRecordById("arcade_notice", notice["id"].(string))
	if err != nil || saved.GetBool("delete") || saved.GetString("title") != "Original" {
		t.Fatal("denied content mutation changed notice", err)
	}
	for _, item := range []struct {
		name string
		want int
	}{{"arcade_memo", 0}, {"arcade_notice", 1}, {"arcade_changelog", 0}, {"user_level_log", 0}} {
		if n, err := app.CountRecords(item.name); err != nil || int(n) != item.want {
			t.Fatal("denied mutation persisted domain state", err)
		}
	}
	disabled := nativeReadApp(t, false)
	_, disabledOwner := createAuthUser(t, disabled, true)
	if err := nativeauth.InstallSchema(disabled); err != nil {
		t.Fatal(err)
	}
	service := nativeauth.Service{Clients: nativeLoginClients}
	verifier := strings.Repeat("n", 43)
	challenge := sha256.Sum256([]byte(verifier))
	code, err := service.IssueCode(disabled, disabledOwner, nativeLoginClients[0].ID, nativeLoginClients[0].Callback, base64.RawURLEncoding.EncodeToString(challenge[:]))
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Exchange(disabled, code, verifier, nativeLoginClients[0].ID, nativeLoginClients[0].Callback)
	if err != nil {
		t.Fatal(err)
	}
	if actor, err := service.Authenticate(disabled, issued.AccessToken); err != nil || actor.Id != disabledOwner.Id {
		t.Fatal("disabled fixture has no valid same-database session", err)
	}
	for _, op := range ops {
		payload, err := json.Marshal(op.body)
		if err != nil {
			t.Fatal(err)
		}
		response := doUserRequest(t, disabled, op.method, op.path, map[string]string{"Authorization": "Bearer " + issued.AccessToken}, string(payload))
		response.Body.Close()
		if response.StatusCode != 401 {
			t.Fatal("disabled adapter accepted native content mutation")
		}
	}
}

func TestCanonicalNativeContentRetainsRouteRateLimits(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	owner.Set("tags", []string{"moderator"})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	token := nativeLoginIssue(t, app, web).AccessToken
	arcade := nativeEditorArcade(t, app, owner.Id)
	create := map[string]any{"arcade": arcade.Id, "title": "Rate fixture", "document": nativeContentDocument("Rate fixture")}
	notice := decodeJSON(t, nativeReadRequest(t, app, "POST", "/arcade/notice", web, create, 200))
	for _, op := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/arcade/memo", map[string]any{"arcade": arcade.Id, "document": nativeContentDocument("Rate memo")}},
		{"POST", "/arcade/notice", create},
		{"PUT", "/arcade/notice", map[string]any{"id": notice["id"], "title": "Rate update"}},
		{"DELETE", "/arcade/notice", map[string]any{"id": notice["id"]}},
	} {
		app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: op.method + " " + op.path, MaxRequests: 1, Duration: 60}}}
		nativeReadRequest(t, app, op.method, op.path, token, op.body, 200)
		nativeReadRequest(t, app, op.method, op.path, token, op.body, 429)
	}
}
