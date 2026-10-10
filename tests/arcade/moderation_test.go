package arcade_test

import (
	"fmt"
	"net/http"
	"testing"
)

func TestModerationRequestsAndArcadeStatus(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUser(t, app)
	modToken, moderator := createAuthUserWithTags(t, app, []string{"moderator"})
	supporterToken, _ := createAuthUserWithTags(t, app, []string{"supporter"})
	headers := map[string]string{"Authorization": "Bearer " + token}
	modHeaders := map[string]string{"Authorization": "Bearer " + modToken}
	supporterHeaders := map[string]string{"Authorization": "Bearer " + supporterToken}
	arcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "Moderation Arcade", Address: "Test", Location: location{Lat: 37.5, Lon: 127}})
	created := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodPost, "/arcade/request_admin", fmt.Sprintf(`{"arcade":%q,"message":"close duplicate arcade"}`, arcadeID), headers))
	id := created["id"].(string)
	for _, path := range []string{"/moderation/arcade/requests", "/moderation/arcade/status?arcade=" + arcadeID} {
		assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, path, "", nil), http.StatusUnauthorized)
		assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, path, "", headers), http.StatusForbidden)
		assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, path, "", supporterHeaders), http.StatusForbidden)
	}
	queue := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/moderation/arcade/requests", "", modHeaders))
	if !contractItemsContainID(queue["items"], id) {
		t.Fatal("moderator cannot see another creator's general request")
	}
	review := fmt.Sprintf(`{"id":%q,"outcome":"actioned","note":"Duplicate closed"}`, id)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/arcade/request", review, headers), http.StatusForbidden)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/arcade/request", review, modHeaders), http.StatusOK)
	record := mustFindRecord(t, app, "arcade_request_admin", id)
	if record.GetString("status") != "done" || record.GetString("reviewed_by") != moderator.Id || record.GetString("reviewed_at") == "" || record.GetString("review_note") != "Duplicate closed" {
		t.Fatal("review metadata was not persisted")
	}
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/arcade/request", review, modHeaders), http.StatusConflict)
	for _, flags := range []struct{ public, closed bool }{{false, true}, {true, false}} {
		body := fmt.Sprintf(`{"arcade":%q,"public":%t,"closed":%t,"country":" jp ","timezone":" Asia/Tokyo "}`, arcadeID, flags.public, flags.closed)
		assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/arcade/status", body, supporterHeaders), http.StatusForbidden)
		assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/arcade/status", body, modHeaders), http.StatusOK)
		result := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/moderation/arcade/status?arcade="+arcadeID, "", modHeaders))
		saved := mustFindRecord(t, app, "arcade", arcadeID)
		if result["public"] != flags.public || result["closed"] != flags.closed || saved.GetBool("public") != flags.public || saved.GetBool("closed") != flags.closed || result["country"] != "JP" || result["timezone"] != "Asia/Tokyo" || saved.GetString("country") != "JP" || saved.GetString("timezone") != "Asia/Tokyo" {
			t.Fatal("status response differs from stored settings")
		}
	}
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/arcade/status", fmt.Sprintf(`{"arcade":%q,"public":true}`, arcadeID), modHeaders), http.StatusBadRequest)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/arcade/request", fmt.Sprintf(`{"id":%q,"outcome":"invalid"}`, id), modHeaders), http.StatusBadRequest)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, "/moderation/arcade/status?arcade=xxxxxxxxxxxxxxx", "", modHeaders), http.StatusNotFound)
}

func TestModerationArcadeStatusRejectsInvalidGeography(t *testing.T) {
	app := newArcadeTestApp(t)
	token, user := createAuthUserWithTags(t, app, []string{"developer"})
	headers := map[string]string{"Authorization": "Bearer " + token}
	arcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "Admin Geo", Address: "Test", Location: location{Lat: 37.5, Lon: 127}})
	before := mustFindRecord(t, app, "arcade", arcadeID)
	for _, values := range []struct{ country, timezone string }{
		{"", "Asia/Seoul"}, {"KOR", "Asia/Seoul"}, {"ZZ", "Asia/Seoul"},
		{"KR", ""}, {"KR", "Invalid/Timezone"}, {"KR", "Local"},
	} {
		body := fmt.Sprintf(`{"arcade":%q,"public":false,"closed":true,"country":%q,"timezone":%q}`, arcadeID, values.country, values.timezone)
		assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/arcade/status", body, headers), http.StatusBadRequest)
	}
	after := mustFindRecord(t, app, "arcade", arcadeID)
	for _, field := range []string{"country", "timezone", "public", "closed"} {
		if fmt.Sprint(before.Get(field)) != fmt.Sprint(after.Get(field)) {
			t.Fatalf("invalid input changed %s", field)
		}
	}
}
