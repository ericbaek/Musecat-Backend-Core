package arcade_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/coreapp"
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
)

func TestNativeDraftDeletePreservesCampaignAuditReferences(t *testing.T) {
	app := newArcadeTestApp(t)
	web, id, campaign, game, _ := seedCampaignCheckFixture(t, app, []string{"moderator"})
	response := executeJSONRequest(t, app, "POST", "/campaign/check", fmt.Sprintf(`{"campaign":%q,"arcade":%q,"game_id":%q,"result":"updated","bypass_location":true}`, campaign, id, game), map[string]string{"Authorization": "Bearer " + web})
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("campaign fixture check: %d", response.StatusCode)
	}
	arcade := mustFindRecord(t, app, "arcade", id)
	arcade.Set("public", false)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	creator := mustFindRecord(t, app, "user", arcade.GetString("createdBy"))
	clients := []nativeauth.Client{{ID: "ios", Callback: "musecat-test://auth/callback"}}
	app.OnServe().BindFunc(func(e *core.ServeEvent) error { coreapp.RegisterNativeAPIRoutes(e, clients); return e.Next() })
	verifier := strings.Repeat("r", 43)
	digest := sha256.Sum256([]byte(verifier))
	service := nativeauth.Service{Clients: clients}
	code, err := service.IssueCode(app, creator, "ios", clients[0].Callback, base64.RawURLEncoding.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := service.Exchange(app, code, verifier, "ios", clients[0].Callback)
	if err != nil {
		t.Fatal(err)
	}
	count := func(collection string) int64 {
		t.Helper()
		n, err := app.CountRecords(collection)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	checks, revisions, xp := count("arcade_campaign_check"), count("arcade_game_history"), count("user_level_log")
	for _, route := range []struct{ path, token string }{{"/native/arcade/draft?id=" + id, credentials.AccessToken}, {"/arcade/draft?id=" + id, web}} {
		response = executeJSONRequest(t, app, "DELETE", route.path, "", map[string]string{"Authorization": "Bearer " + route.token})
		var body map[string]any
		json.NewDecoder(response.Body).Decode(&body)
		response.Body.Close()
		if response.StatusCode != 409 || body["code"] != "draft_referenced" {
			t.Fatalf("%s status=%d body=%v", route.path, response.StatusCode, body)
		}
		if strings.HasPrefix(route.path, "/native/") && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("conflict response must be private")
		}
		mustFindRecord(t, app, "arcade", id)
		mustFindRecord(t, app, "arcade_game_id", game)
		if count("arcade_campaign_check") != checks || count("arcade_game_history") != revisions || count("user_level_log") != xp {
			t.Fatal("failed deletion changed campaign history, game revisions or XP")
		}
	}
	app.OnRecordDelete("arcade").BindFunc(func(e *core.RecordEvent) error {
		return errors.New("simulated storage failure")
	})
	response = executeJSONRequest(t, app, "DELETE", "/native/arcade/draft?id="+id, "", map[string]string{"Authorization": "Bearer " + credentials.AccessToken})
	response.Body.Close()
	if response.StatusCode != 502 {
		t.Fatalf("storage failure was misclassified: %d", response.StatusCode)
	}
	mustFindRecord(t, app, "arcade", id)
	if count("arcade_campaign_check") != checks || count("arcade_game_history") != revisions || count("user_level_log") != xp {
		t.Fatal("storage failure changed audit data")
	}
}
