package arcade_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
)

func TestNativeArcadeMemoAndNoticeDomainParity(t *testing.T) {
	app := newArcadeTestApp(t)
	web, creator := createAuthUser(t, app)
	_, other := createAuthUser(t, app)
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, official := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	_, supporter := createAuthUserWithTags(t, app, []string{"supporter"})
	clients := []nativeauth.Client{{ID: "ios", Callback: "musecat-test://auth/callback"}}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		coreapp.RegisterNativeAPIRoutes(e, clients)
		return e.Next()
	})
	issue := func(user *core.Record) string {
		t.Helper()
		verifier := strings.Repeat("m", 43)
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
	token, foreign, moderator, owner, lowSupporter := issue(creator), issue(other), issue(staff), issue(official), issue(supporter)
	public, _ := seedPublicArcade(t, app, creator.Id, arcadeSeed{Name: "Native Content", Address: "Seoul", Location: location{Lat: 37.5, Lon: 127}})
	private, _ := seedArcade(t, app, creator.Id, arcadeSeed{Name: "Private Content", Address: "Seoul", Location: location{Lat: 37.5, Lon: 127}})
	addOwnedArcade(t, app, official, public)
	request := func(method, path, credential string, body any, status int) map[string]any {
		t.Helper()
		encoded := ""
		if body != nil {
			data, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			encoded = string(data)
		}
		headers := map[string]string{}
		if credential != "" {
			headers["Authorization"] = "Bearer " + credential
		}
		res := executeJSONRequest(t, app, method, path, encoded, headers)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != status {
			t.Fatalf("%s %s got %d want %d: %v", method, path, res.StatusCode, status, out)
		}
		if status == 200 && strings.HasPrefix(path, "/native/") && !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("native content response was cacheable")
		}
		return out
	}
	count := func(collection string) int64 {
		t.Helper()
		n, err := app.CountRecords(collection)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, operation := range [][2]string{{"GET", "memo"}, {"PUT", "memo"}, {"GET", "notice"}, {"POST", "notice"}, {"PUT", "notice"}, {"DELETE", "notice"}} {
		for _, rejected := range []string{"", web, "invalid-partner-credential"} {
			request(operation[0], "/native/arcade/"+operation[1]+"?arcade="+public, rejected, map[string]any{}, 401)
		}
	}
	if empty := request("GET", "/native/arcade/memo?arcade="+public, token, nil, 200); empty["memo"] != nil {
		t.Fatal(empty)
	}
	initialXP := count("user_level_log")
	initialLogs := count("arcade_changelog")
	// This exceeds the common native 4 KiB envelope but fits the memo document limit.
	doc := noticeDocument(strings.Repeat("memo", 1600))
	doc["future_extension"] = map[string]any{"preserved": true}
	memoBody := map[string]any{"arcade": public, "document": doc}
	first := request("PUT", "/native/arcade/memo", token, memoBody, 200)
	firstID := first["memo"].(map[string]any)["id"].(string)
	if first["changed"] != true || first["memo"].(map[string]any)["by"] != creator.Id {
		t.Fatal(first)
	}
	repeated := request("PUT", "/native/arcade/memo", token, memoBody, 200)
	if repeated["changed"] != false || repeated["memo"].(map[string]any)["id"] != firstID || count("arcade_changelog") != initialLogs+1 {
		t.Fatal("identical memo replay created a revision/history", repeated)
	}
	second := request("PUT", "/native/arcade/memo", foreign, map[string]any{"arcade": public, "document": noticeDocument("another active editor")}, 200)
	if second["memo"].(map[string]any)["id"] == firstID || noticeDocumentText(mustFindRecord(t, app, "arcade_memo", firstID).Get("document")) != strings.Repeat("memo", 1600) {
		t.Fatal("last-save-wins rewrote immutable history", second)
	}
	request("GET", "/arcade/memo?arcade="+public, "", nil, 200)
	request("PUT", "/arcade/memo", token, memoBody, 401)
	request("GET", "/native/arcade/memo?arcade="+private, foreign, nil, 404)
	request("PUT", "/native/arcade/memo", foreign, map[string]any{"arcade": private, "document": doc}, 403)
	request("PUT", "/native/arcade/memo", token, map[string]any{"arcade": private, "document": doc}, 200)
	request("GET", "/native/arcade/memo?arcade="+private, moderator, nil, 200)
	beforeInvalid := count("arcade_memo")
	request("PUT", "/native/arcade/memo", token, map[string]any{"arcade": public, "document": map[string]any{"type": "doc", "content": "bad"}}, 400)
	request("PUT", "/native/arcade/memo", token, map[string]any{"arcade": public, "document": noticeDocument(strings.Repeat("x", 200001))}, 400)
	if count("arcade_memo") != beforeInvalid || count("user_level_log") != initialXP {
		t.Fatal("invalid memo or memo editing changed revisions/XP unexpectedly")
	}
	// Notice creation remains role-gated, with exact official ownership and supporter level checks.
	beforeMissingTitle, beforeMissingTitleXP := count("arcade_notice"), count("user_level_log")
	missingTitle := request("POST", "/native/arcade/notice", owner, map[string]any{"arcade": public, "type": "alert", "document": doc}, 400)
	if missingTitle["error"] != "title is required" || count("arcade_notice") != beforeMissingTitle || count("user_level_log") != beforeMissingTitleXP {
		t.Fatal("missing native notice title changed state or returned the wrong validation error", missingTitle)
	}
	noticeBody := map[string]any{"arcade": public, "title": "Native notice", "display_mode": "normal", "type": "alert", "document": doc, "link": "https://example.com", "priority": 2}
	request("POST", "/native/arcade/notice", token, noticeBody, 403)
	request("POST", "/native/arcade/notice", lowSupporter, noticeBody, 403)
	setUserLevel(t, app, supporter.Id, 30)
	request("POST", "/native/arcade/notice", lowSupporter, noticeBody, 403) // another official manages this arcade
	request("POST", "/native/arcade/notice", owner, map[string]any{"arcade": private, "title": "Private notice", "type": "alert", "document": doc}, 403)
	created := request("POST", "/native/arcade/notice", owner, noticeBody, 200)
	id := created["id"].(string)
	if created["createdBy"] != official.Id || created["arcade"] != public || created["title"] != "Native notice" || created["display_mode"] != "normal" {
		t.Fatal("notice author attribution changed", created)
	}
	// POST deliberately has no idempotency key: it is not safe for automatic client replay.
	duplicate := request("POST", "/native/arcade/notice", owner, noticeBody, 200)
	if duplicate["id"] == id {
		t.Fatal("unexpected notice POST idempotency contract")
	}
	beforeDenied := mustFindRecord(t, app, "arcade_notice", id).GetString("updated")
	request("PUT", "/native/arcade/notice", foreign, map[string]any{"id": id, "document": noticeDocument("forbidden")}, 403)
	request("DELETE", "/native/arcade/notice", foreign, map[string]any{"id": id}, 403)
	if notice := mustFindRecord(t, app, "arcade_notice", id); notice.GetBool("delete") || notice.GetString("updated") != beforeDenied {
		t.Fatal("denied notice mutation changed persisted state")
	}
	updated := request("PUT", "/native/arcade/notice", owner, map[string]any{"id": id, "link": ""}, 200)
	if updated["link"] != "" || updated["type"] != "alert" || updated["title"] != "Native notice" || updated["display_mode"] != "normal" || noticeDocumentText(updated["document"]) != strings.Repeat("memo", 1600) {
		t.Fatal("notice metadata omission did not preserve content", updated)
	}
	request("PUT", "/native/arcade/notice", moderator, map[string]any{"id": id, "priority": 1}, 200)
	request("GET", "/native/arcade/notice?arcade="+private, moderator, nil, 404)
	request("GET", "/native/arcade/notice?arcade="+public, token, nil, 200)
	request("GET", "/arcade/notice?arcade="+public, "", nil, 200)
	beforeNoticeLogs, beforeNoticeXP := count("arcade_changelog"), count("user_level_log")
	deleted := request("DELETE", "/native/arcade/notice", owner, map[string]any{"id": id}, 200)
	if deleted["delete"] != true || !mustFindRecord(t, app, "arcade_notice", id).GetBool("delete") {
		t.Fatal("notice was not soft-deleted", deleted)
	}
	listed := request("GET", "/native/arcade/notice?arcade="+public, token, nil, 200)
	if listed["total"] != float64(1) || count("arcade_changelog") != beforeNoticeLogs || count("user_level_log") != beforeNoticeXP {
		t.Fatal("notice deletion/list changed domain history or XP", listed)
	}
	creator.Set("withdrawn", true)
	if err := app.Save(creator); err != nil {
		t.Fatal(err)
	}
	request("GET", "/native/arcade/memo?arcade="+public, token, nil, 401)
	request("GET", "/native/arcade/notice?arcade="+public, token, nil, 401)
}
