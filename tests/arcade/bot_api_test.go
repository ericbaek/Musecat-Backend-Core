package arcade_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/botaccess"
	"github.com/ericbaek/musecat-backend-core/service/contribution"
	"github.com/ericbaek/musecat-backend-core/service/xp"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func botRequest(t *testing.T, app *tests.TestApp, token, method, path, body string, status int) map[string]any {
	t.Helper()
	response := executeJSONRequest(t, app, method, path, body, map[string]string{"Authorization": "Bearer " + token, "Content-Type": "application/json"})
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != status {
		t.Fatalf("%s %s: got %d want %d: %s", method, path, response.StatusCode, status, raw)
	}
	if status == 204 {
		return nil
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func issueBotKey(t *testing.T, app *tests.TestApp, session, name string) (string, string) {
	t.Helper()
	result := botRequest(t, app, session, "POST", "/user/bot-keys", fmt.Sprintf(`{"name":%q}`, name), 201)
	return result["token"].(string), result["key"].(map[string]any)["id"].(string)
}

func TestBotKeysAreAccountBoundAndNotSessions(t *testing.T) {
	app := newArcadeTestApp(t)
	session, owner := createAuthUserWithTags(t, app, []string{"founding_supporter"})
	other, _ := createAuthUserWithTags(t, app, []string{"founding_supporter"})
	key, id := issueBotKey(t, app, session, "Importer")
	stored, err := app.FindRecordById(botaccess.Keys, id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored.GetString("key_hash"), key) || len(stored.GetString("key_hash")) != 64 {
		t.Fatal("key must be stored only as a hash")
	}
	listed := botRequest(t, app, session, "GET", "/user/bot-keys", "", 200)
	raw, _ := json.Marshal(listed)
	if strings.Contains(string(raw), key) || strings.Contains(string(raw), "key_hash") {
		t.Fatal("list leaks credentials")
	}
	botRequest(t, app, other, "DELETE", "/user/bot-keys/"+id, "", 404)
	botRequest(t, app, key, "POST", "/user/bot-keys", `{"name":"Escalation"}`, 401)
	botRequest(t, app, session, "GET", "/bot/v1/arcade/drafts", "", 401)
	botRequest(t, app, key, "GET", "/bot/v1/arcade/drafts", "", 200)
	botRequest(t, app, key, "GET", "/bot/v1/user/me", "", 404)
	botRequest(t, app, key, "PUT", "/bot/v1/moderation/arcade/status", `{}`, 404)
	botRequest(t, app, key, "GET", "/api/collections/user/records", "", 403)
	botRequest(t, app, session, "DELETE", "/user/bot-keys/"+id, "", 204)
	botRequest(t, app, session, "DELETE", "/user/bot-keys/"+id, "", 204)
	botRequest(t, app, key, "GET", "/bot/v1/arcade/drafts", "", 401)
	key, _ = issueBotKey(t, app, session, "Rotated account")
	owner.RefreshTokenKey()
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	botRequest(t, app, key, "GET", "/bot/v1/arcade/drafts", "", 401)
}

func TestBotKeyExpiryLimitAndAccountRestriction(t *testing.T) {
	app := newArcadeTestApp(t)
	session, owner := createAuthUserWithTags(t, app, []string{"founding_supporter"})
	key, id := issueBotKey(t, app, session, "Importer")
	stored, _ := app.FindRecordById(botaccess.Keys, id)
	stored.Set("expires", time.Now().Add(-time.Hour))
	if err := app.Save(stored); err != nil {
		t.Fatal(err)
	}
	botRequest(t, app, key, "GET", "/bot/v1/arcade/drafts", "", 401)
	key, id = issueBotKey(t, app, session, "Limited")
	stored, _ = app.FindRecordById(botaccess.Keys, id)
	stored.Set("window_start", time.Now().UTC())
	stored.Set("window_count", 60)
	if err := app.Save(stored); err != nil {
		t.Fatal(err)
	}
	botRequest(t, app, key, "GET", "/bot/v1/arcade/drafts", "", 429)
	key, _ = issueBotKey(t, app, session, "Withdrawn")
	owner.Set("withdrawn", true)
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	botRequest(t, app, key, "GET", "/bot/v1/arcade/drafts", "", 403)
}

func TestBotCreationAndEditsRetainAccountAndBotIdentity(t *testing.T) {
	app := newArcadeTestApp(t)
	stubGeoLookup(t)
	session, owner := createAuthUserWithTags(t, app, []string{"founding_supporter"})
	first, firstID := issueBotKey(t, app, session, "Importer")
	second, secondID := issueBotKey(t, app, session, "Updater")
	created := botRequest(t, app, first, "POST", "/bot/v1/arcade/new", `{"name":"Bot Arcade","address":"Seoul","location":{"lat":37.5665,"lon":126.978}}`, 200)
	id := created["id"].(string)
	arcade, _ := app.FindRecordById("arcade", id)
	if arcade.GetString("createdBy") != owner.Id || arcade.GetBool("public") || contribution.FromRecord(arcade).ID != firstID {
		t.Fatal("invalid draft owner or creation source")
	}
	botRequest(t, app, second, "PUT", "/bot/v1/arcade/basic", fmt.Sprintf(`{"arcade":%q,"direction":"Basement"}`, id), 200)
	changes := loadChangelogRecords(t, app, id, "basic")
	sources := map[string]bool{}
	for _, change := range changes {
		if change.GetString("by") != owner.Id {
			t.Fatal("bot must not replace the account actor")
		}
		source := contribution.FromRecord(change)
		if source == nil || source.Kind != "bot" {
			t.Fatal("missing bot source")
		}
		sources[source.ID] = true
	}
	if !sources[firstID] || !sources[secondID] {
		t.Fatal("distinct bots must be distinguishable")
	}
	for _, target := range []struct {
		bot   string
		count int
	}{{"all", 2}, {firstID, 1}, {secondID, 1}} {
		history := botRequest(t, app, session, "GET", "/user/changelog?user="+owner.Id+"&bot="+target.bot, "", 200)
		if history["total"] != float64(target.count) {
			t.Fatalf("wrong bot history count for %s: %v", target.bot, history["total"])
		}
	}
	other, _ := createAuthUserWithTags(t, app, []string{"founding_supporter"})
	otherKey, _ := issueBotKey(t, app, other, "Foreign bot")
	botRequest(t, app, other, "GET", "/user/changelog?user="+owner.Id+"&bot=all", "", 403)
	botRequest(t, app, other, "GET", "/user/changelog?user="+owner.Id+"&bot="+firstID, "", 403)
	botRequest(t, app, session, "GET", "/user/changelog?user="+owner.Id+"&bot=wrongkey", "", 404)
	botRequest(t, app, otherKey, "PUT", "/bot/v1/arcade/basic", fmt.Sprintf(`{"arcade":%q,"direction":"Forbidden"}`, id), 403)
	botRequest(t, app, session, "DELETE", "/user/bot-keys/"+firstID, "", 204)
	history := botRequest(t, app, session, "GET", "/arcade/changelog?arcade="+id, "", 200)
	raw, _ := json.Marshal(history)
	if !strings.Contains(string(raw), `"name":"Importer"`) || strings.Contains(string(raw), first) {
		t.Fatal("history must preserve bot name without its secret")
	}
	botRequest(t, app, session, "PUT", "/arcade/basic", fmt.Sprintf(`{"arcade":%q,"direction":"Web edit"}`, id), 200)
	filtered := botRequest(t, app, session, "GET", "/user/changelog?user="+owner.Id+"&bot=all&per_page=1&page=2", "", 200)
	if filtered["total"] != float64(2) || len(filtered["items"].([]any)) != 1 {
		t.Fatal("bot filtering must precede pagination and exclude actual web edits")
	}
	preview, err := xp.PreviewArcadePublicExp(app, owner.Id, id)
	if err != nil {
		t.Fatal(err)
	}
	if preview.PublicExp != 0 || preview.BackfillExp != 2 {
		t.Fatalf("bot creation must earn 0; actual web edit must earn 2: %+v", preview)
	}
}

func TestBotNamesArePrivateToTheirIssuingAccount(t *testing.T) {
	app := newArcadeTestApp(t)
	stubGeoLookup(t)
	ownerSession, owner := createAuthUserWithTags(t, app, []string{"founding_supporter"})
	visitorSession, _ := createAuthUserWithTags(t, app, nil)
	reviewerSession, _ := createAuthUserWithTags(t, app, []string{"developer"})
	const privateName = "PRIVATE_BOT_NAME_SENTINEL"
	key, keyID := issueBotKey(t, app, ownerSession, privateName)
	created := botRequest(t, app, key, "POST", "/bot/v1/arcade/new", `{"name":"Privacy fixture","address":"Seoul","location":{"lat":37.5665,"lon":126.978}}`, 200)
	arcadeID := created["id"].(string)
	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatal(err)
	}
	arcade.Set("public", true)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{visitorSession, reviewerSession} {
		for _, path := range []string{"/arcade/changelog?arcade=" + arcadeID, "/user/changelog?user=" + owner.Id} {
			response := botRequest(t, app, session, "GET", path, "", 200)
			items := response["items"].([]any)
			if len(items) != 1 {
				t.Fatalf("expected one visible bot edit: %v", response)
			}
			source := items[0].(map[string]any)["contribution_source"].(map[string]any)
			if source["kind"] != "bot" || source["id"] != keyID {
				t.Fatal("public BOT marker must retain verified attribution")
			}
			if _, present := source["name"]; present {
				t.Fatal("bot name must be omitted for visitors and reviewers")
			}
			raw, _ := json.Marshal(response)
			if strings.Contains(string(raw), privateName) {
				t.Fatal("private bot name leaked elsewhere in the response")
			}
		}
	}
	botRequest(t, app, ownerSession, "DELETE", "/user/bot-keys/"+keyID, "", 204)
	for _, path := range []string{"/arcade/changelog?arcade=" + arcadeID, "/user/changelog?user=" + owner.Id + "&bot=" + keyID} {
		response := botRequest(t, app, ownerSession, "GET", path, "", 200)
		source := response["items"].([]any)[0].(map[string]any)["contribution_source"].(map[string]any)
		if source["name"] != privateName {
			t.Fatal("owner must retain immutable bot names after redaction and revocation")
		}
	}
}

func TestBotPublicEditsEarnNoXPButWebEditsStillDo(t *testing.T) {
	app := newArcadeTestApp(t)
	stubGeoLookup(t)
	session, owner := createAuthUserWithTags(t, app, []string{"founding_supporter"})
	key, _ := issueBotKey(t, app, session, "Updater")
	id, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Arcade", Address: "Seoul", Location: location{Lat: 37.5665, Lon: 126.978}})
	before, _ := xp.LoadCurrentExp(app, owner.Id)
	botRequest(t, app, key, "PUT", "/bot/v1/arcade/basic", fmt.Sprintf(`{"arcade":%q,"direction":"Bot edit"}`, id), 200)
	after, _ := xp.LoadCurrentExp(app, owner.Id)
	if after != before {
		t.Fatal("bot edit granted XP")
	}
	botRequest(t, app, session, "PUT", "/arcade/basic", fmt.Sprintf(`{"arcade":%q,"direction":"Web edit"}`, id), 200)
	after, _ = xp.LoadCurrentExp(app, owner.Id)
	if after != before+2 {
		t.Fatal("bot edit must not suppress a later actual web contribution")
	}
}

func TestBotGamePhotoAndPublicationFlow(t *testing.T) {
	app := newArcadeTestApp(t)
	stubGeoLookup(t)
	session, owner := createAuthUserWithTags(t, app, []string{"founding_supporter"})
	key, _ := issueBotKey(t, app, session, "Importer")
	created := botRequest(t, app, key, "POST", "/bot/v1/arcade/new", `{"name":"Complete Bot Arcade","address":"Seoul","location":{"lat":37.5665,"lon":126.978}}`, 200)
	id := created["id"].(string)
	version := seedGameSeriesVersion(t, app)
	body := gameDeltaRequest(t, id, "", []map[string]any{testGameObject(version, "", "1F", 500, "")}, nil, nil)
	botRequest(t, app, key, "PUT", "/bot/v1/arcade/game", body, 200)
	photoBody, contentType := buildPhotoUploadMultipart(t, id, []uploadTestFile{{Filename: "bot.png", Content: pngFixtureBytes()}})
	response := executeRequest(t, app, "POST", "/bot/v1/arcade/photo/upload", bytes.NewReader(photoBody), map[string]string{"Authorization": "Bearer " + key, "Content-Type": contentType})
	defer response.Body.Close()
	var uploaded photoUploadResponse
	if err := json.NewDecoder(response.Body).Decode(&uploaded); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 200 || len(uploaded.Uploaded) != 1 {
		t.Fatalf("photo upload failed: %+v", uploaded)
	}
	atomID := uploaded.Uploaded[0].AtomID
	photo, _ := app.FindRecordById("arcade_photo_atoms", atomID)
	if contribution.FromRecord(photo) == nil {
		t.Fatal("uploaded atom lost bot source")
	}
	// Applying a bot upload from the website must not turn it into a human photo reward.
	botRequest(t, app, session, "PUT", "/arcade/photo", fmt.Sprintf(`{"arcade":%q,"photos":[%q]}`, id, atomID), 200)
	setUserLevel(t, app, owner.Id, 5)
	before, _ := xp.LoadCurrentExp(app, owner.Id)
	preview := botRequest(t, app, key, "GET", "/bot/v1/arcade/public?arcade="+id, "", 200)
	xpPreview := preview["xp_preview"].(map[string]any)
	if xpPreview["estimated_gain"] != float64(0) {
		t.Fatalf("bot public preview awards XP: %v", xpPreview)
	}
	botRequest(t, app, key, "PUT", "/bot/v1/arcade/public", fmt.Sprintf(`{"arcade":%q}`, id), 200)
	metadata := botRequest(t, app, "", "GET", "/api/collections/arcade_photo_atoms/records/"+atomID, "", 200)
	if _, present := metadata["contribution_source"]; present {
		t.Fatal("raw public photo metadata must not expose stored bot attribution")
	}
	after, _ := xp.LoadCurrentExp(app, owner.Id)
	if after != before {
		t.Fatalf("bot publication awarded XP: %d -> %d", before, after)
	}
	// A previously bot-uploaded photo cannot be rewarded by web reapplication either.
	botRequest(t, app, session, "PUT", "/arcade/photo", fmt.Sprintf(`{"arcade":%q,"photos":[%q]}`, id, atomID), 200)
	after, _ = xp.LoadCurrentExp(app, owner.Id)
	if after != before {
		t.Fatal("web reapplication of a bot upload awarded XP")
	}
}

func TestBotKeyRoleEligibility(t *testing.T) {
	for _, role := range []string{"", "arcade_owner", "founding_supporter", "supporter", "designer", "moderator", "developer"} {
		t.Run("role="+role, func(t *testing.T) {
			app := newArcadeTestApp(t)
			tags := []string{}
			if role != "" {
				tags = append(tags, role)
			}
			session, owner := createAuthUserWithTags(t, app, tags)
			if role == "" || role == "arcade_owner" {
				botRequest(t, app, session, "POST", "/user/bot-keys", `{"name":"Denied"}`, 403)
				count, err := app.CountRecords(botaccess.Keys)
				if err != nil || count != 0 {
					t.Fatalf("denied issuance created a credential: %d %v", count, err)
				}
				return
			}
			key, id := issueBotKey(t, app, session, "Eligible")
			botRequest(t, app, key, "GET", "/bot/v1/arcade/drafts", "", 200)
			owner.Set("tags", []string{})
			if err := app.Save(owner); err != nil {
				t.Fatal(err)
			}
			botRequest(t, app, key, "GET", "/bot/v1/arcade/drafts", "", 401)
			// Removing the entitlement must not prevent the owner from revoking a key.
			botRequest(t, app, session, "DELETE", "/user/bot-keys/"+id, "", 204)
		})
	}
}

func TestBotKeyListRetainsOldBotsAndEnforcesActiveLimit(t *testing.T) {
	app := newArcadeTestApp(t)
	session, owner := createAuthUserWithTags(t, app, []string{"founding_supporter"})
	collection, err := app.FindCollectionByNameOrId(botaccess.Keys)
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 101; index++ {
		record := core.NewRecord(collection)
		record.Set("owner", owner.Id)
		record.Set("name", fmt.Sprintf("Old bot %d", index))
		record.Set("prefix", "musecat_bot_test")
		record.Set("key_hash", fmt.Sprintf("%064x", index))
		record.Set("owner_key_hash", strings.Repeat("a", 64))
		record.Set("expires", time.Now().Add(time.Hour))
		record.Set("revoked", time.Now())
		if err := app.Save(record); err != nil {
			t.Fatal(err)
		}
	}
	listed := botRequest(t, app, session, "GET", "/user/bot-keys", "", 200)
	if len(listed["items"].([]any)) != 101 {
		t.Fatal("older bot keys were truncated")
	}
	botRequest(t, app, session, "GET", "/api/collections/user_bot_key/records", "", 403)
	var activeID string
	for index := 0; index < 5; index++ {
		_, activeID = issueBotKey(t, app, session, fmt.Sprintf("Active %d", index))
	}
	botRequest(t, app, session, "POST", "/user/bot-keys", `{"name":"Over limit"}`, 400)
	botRequest(t, app, session, "DELETE", "/user/bot-keys/"+activeID, "", 204)
	issueBotKey(t, app, session, "Replacement")
}
