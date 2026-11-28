package user_test

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/readpolicy"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func canonicalReadJSON(t *testing.T, app *tests.TestApp, path, token string) map[string]any {
	t.Helper()
	response := nativeReadRequest(t, app, "GET", path, token, nil, 200)
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

// Optional fixture export contains only synthetic domain response bodies, never
// request headers, authentication responses or credentials. Swift wire tests
// consume these exact canonical HTTP payloads rather than a parallel mock DTO.
func exportCanonicalReadFixture(t *testing.T, name string, response map[string]any) {
	t.Helper()
	directory := os.Getenv("MUSECAT_CANONICAL_READ_FIXTURES")
	if directory == "" {
		return
	}
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	payload, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name+".json"), append(payload, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalOrdinaryNativeReadsMatchWebAndPreservePrivateBoundaries(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	otherWeb, other := createAuthUser(t, app, true)
	native := nativeLoginIssue(t, app, web).AccessToken
	nativeReadRequest(t, app, "POST", "/user/check-in", web, map[string]any{}, 200)
	foreign := nativeLoginIssue(t, app, otherWeb).AccessToken
	public := seedUserChangelogArcade(t, app, owner.Id, true, "Canonical Public Arcade")
	private := seedUserChangelogArcade(t, app, owner.Id, false, "Canonical Private Arcade")
	otherPrivate := seedUserChangelogArcade(t, app, other.Id, false, "Foreign Private Arcade")
	for _, arcade := range []string{public, private, otherPrivate} {
		seedUserChangelog(t, app, arcade, "basic", owner.Id)
	}
	// Existing web writes seed realistic immutable memo/changelog and request bodies
	// in this fresh bootstrap only; native write capabilities remain unchanged.
	document := map[string]any{"type": "doc", "content": []any{map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": "Canonical memo"}}}}}
	for _, arcade := range []string{public, private} {
		nativeReadRequest(t, app, "PUT", "/arcade/memo", web, map[string]any{"arcade": arcade, "document": document}, 200)
	}
	for _, actor := range []string{web, otherWeb} {
		nativeReadRequest(t, app, "POST", "/arcade/request_admin", actor, map[string]any{"arcade": public, "message": "Canonical own request"}, 200)
	}
	notices, err := app.FindCollectionByNameOrId("arcade_notice")
	if err != nil {
		t.Fatal(err)
	}
	notice := core.NewRecord(notices)
	for key, value := range map[string]any{"arcade": public, "createdBy": owner.Id, "title": "Canonical notice", "type": "alert", "display_mode": "pinned", "document": document, "delete": false} {
		notice.Set(key, value)
	}
	if err := app.Save(notice); err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{
		"profile":        "/user?username=" + url.QueryEscape(owner.GetString("username")),
		"activity":       "/user/activity?id=" + owner.Id + "&days=2&tz=Australia%2FSydney",
		"user-history":   "/user/changelog?user=" + owner.Id + "&page=1&per_page=20",
		"level":          "/supporter/score",
		"arcade-history": "/arcade/changelog?arcade=" + public + "&page=1&per_page=10",
		"requests":       "/arcade/request_admin?arcade=" + public,
		"drafts":         "/arcade/drafts",
		"draft":          "/arcade/draft?id=" + private + "&expand=all",
		"memo":           "/arcade/memo?arcade=" + public,
		"notices":        "/arcade/notice?arcade=" + public,
	}
	for name, path := range paths {
		actual := canonicalReadJSON(t, app, path, native)
		expected := canonicalReadJSON(t, app, path, web)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("%s native wire differs from web handler", name)
		}
		exportCanonicalReadFixture(t, name, actual)
		switch name {
		case "drafts":
			items := actual["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["id"] != private {
				t.Fatal("draft list crossed creator boundary")
			}
		case "requests":
			items := actual["items"].([]any)
			if len(items) != 1 || items[0].(map[string]any)["createdBy"] != owner.Id {
				t.Fatal("request list crossed creator boundary")
			}
		case "user-history":
			for _, raw := range actual["items"].([]any) {
				if raw.(map[string]any)["arcade"] == otherPrivate {
					t.Fatal("foreign private history exposed")
				}
			}
		}
	}
	// Guest profiles/activity remain public; history summaries omit revision data.
	canonicalReadJSON(t, app, paths["profile"], "")
	canonicalReadJSON(t, app, paths["activity"], "")
	guest := canonicalReadJSON(t, app, paths["user-history"], "")
	for _, raw := range guest["items"].([]any) {
		item := raw.(map[string]any)
		if item["arcade"] != public || len(item) != 4 {
			t.Fatal("guest history gained private rows or details")
		}
	}
	exportCanonicalReadFixture(t, "guest-user-history", guest)
	for _, query := range []string{"/arcade/changelog?arcade=", "/arcade/memo?arcade="} {
		canonicalReadJSON(t, app, query+private, native)
		nativeReadRequest(t, app, "GET", query+private, foreign, nil, 404)
		nativeReadRequest(t, app, "GET", query+private, "", nil, guestReadStatus(query))
	}
	// Notice lists stay public-only, including for the creator and strict reviewers.
	nativeReadRequest(t, app, "GET", "/arcade/notice?arcade="+private, native, nil, 404)
	nativeReadRequest(t, app, "GET", "/arcade/draft?id="+private, foreign, nil, 404)
	nativeReadRequest(t, app, "GET", "/arcade/draft?id="+otherPrivate, native, nil, 404)
	for _, query := range []struct{ name, key, value string }{
		{"arcade-history", "page", "0"}, {"arcade-history", "per_page", "101"},
		{"user-history", "changed", "unknown"}, {"activity", "days", "366"},
	} {
		parsed, _ := url.Parse(paths[query.name])
		values := parsed.Query()
		values.Set(query.key, query.value)
		parsed.RawQuery = values.Encode()
		nativeReadRequest(t, app, "GET", parsed.String(), native, nil, 400)
	}

	// Staff retain the same strict reviewer private-history policy as web, but
	// own-list endpoints must not become reviewer directories.
	other.Set("tags", []string{"moderator"})
	if err := app.Save(other); err != nil {
		t.Fatal(err)
	}
	canonicalReadJSON(t, app, "/arcade/changelog?arcade="+private, foreign)
	drafts := canonicalReadJSON(t, app, "/arcade/drafts", foreign)["items"].([]any)
	if len(drafts) != 1 || drafts[0].(map[string]any)["id"] != otherPrivate {
		t.Fatal("reviewer own drafts expanded")
	}
	for _, path := range paths {
		nativeReadRequest(t, app, "GET", path, strings.Repeat("z", 43), nil, 401)
	}
	for _, path := range []string{paths["level"], paths["arcade-history"], paths["requests"], paths["drafts"]} {
		nativeReadRequest(t, app, "GET", path, "", nil, 401)
	}
	// Active-account policy applies even to selected public read operations.
	seedUserBanForUserTest(t, app, owner.Id, hashNormalizedEmailForUserTest(owner.Email()), "canonical_read_fixture", time.Now().Add(time.Hour))
	for _, path := range paths {
		nativeReadRequest(t, app, "GET", path, native, nil, 403)
	}
}

func guestReadStatus(path string) int {
	if strings.Contains(path, "changelog") {
		return 401
	}
	return 404
}

func TestCanonicalNativeReadsKeepDeploymentPublicReadPolicy(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	native := nativeLoginIssue(t, app, web).AccessToken
	arcade := nativeEditorArcade(t, app, owner.Id)
	calls := 0
	readpolicy.Register(app, func(re *core.RequestEvent, ids []string) error {
		calls++
		if re.Auth == nil || re.Auth.Id != owner.Id || len(ids) != 1 || ids[0] != arcade.Id {
			t.Fatal("read policy lost canonical account or arcade scope")
		}
		return apis.NewTooManyRequestsError("test restriction", nil)
	})
	for _, token := range []string{web, native} {
		nativeReadRequest(t, app, "GET", "/arcade?id="+arcade.Id+"&expand=all", token, nil, 429)
		nativeReadRequest(t, app, "GET", "/arcade/changelog?arcade="+arcade.Id, token, nil, 429)
		nativeReadRequest(t, app, "GET", "/arcade?id="+arcade.Id+"&expand=basic", token, nil, 200)
	}
	if calls != 4 {
		t.Fatal("public detail policy was bypassed or charged basic-only reads")
	}
}
