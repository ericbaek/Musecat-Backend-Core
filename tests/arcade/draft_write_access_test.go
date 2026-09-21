package arcade_test

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func TestArcadeMutations_RejectOtherUsersPrivateDraft(t *testing.T) {
	app := newArcadeTestApp(t)
	_, creator := createAuthUser(t, app)
	otherToken, _ := createAuthUser(t, app)
	arcadeID, basicID := seedArcade(t, app, creator.Id, arcadeSeed{
		Name: "Private draft", Address: "Seoul", Location: location{Lat: 37.5665, Lon: 126.978},
	})
	headers := map[string]string{"Authorization": "Bearer " + otherToken}

	cases := []struct {
		path string
		body string
	}{
		{"/arcade/basic", fmt.Sprintf(`{"arcade":%q,"name":"Changed"}`, arcadeID)},
		{"/arcade/basic", fmt.Sprintf(`{"arcade":%q,"name":"Private draft"}`, arcadeID)},
		{"/arcade/hour", fmt.Sprintf(`{"arcade":%q,"Monday":499}`, arcadeID)},
		{"/arcade/sns", fmt.Sprintf(`{"arcade":%q,"sns":[]}`, arcadeID)},
		{"/arcade/gtk", fmt.Sprintf(`{"arcade":%q,"gtk":[]}`, arcadeID)},
		{"/arcade/game", fmt.Sprintf(`{"arcade":%q,"base_state_id":"","add":[],"modify":[],"remove":["unused"]}`, arcadeID)},
		{"/arcade/photo", fmt.Sprintf(`{"arcade":%q,"photos":["unused"]}`, arcadeID)},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			res := executeJSONRequest(t, app, http.MethodPut, tc.path, tc.body, headers)
			defer res.Body.Close()
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", res.StatusCode)
			}
		})
	}

	rollback := executeJSONRequest(t, app, http.MethodPost, "/arcade/rollback", fmt.Sprintf(`{"arcade":%q,"part":"basic","value":%q}`, arcadeID, basicID), headers)
	defer rollback.Body.Close()
	if rollback.StatusCode != http.StatusForbidden {
		t.Fatalf("expected rollback 403, got %d", rollback.StatusCode)
	}

	body, contentType := buildPhotoUploadMultipart(t, arcadeID, []uploadTestFile{{Filename: "a.png", Content: pngFixtureBytes()}})
	uploadHeaders := map[string]string{"Authorization": "Bearer " + otherToken, "Content-Type": contentType}
	upload := executeRequest(t, app, http.MethodPost, "/arcade/photo/upload", bytes.NewReader(body), uploadHeaders)
	defer upload.Body.Close()
	if upload.StatusCode != http.StatusForbidden {
		t.Fatalf("expected upload 403, got %d", upload.StatusCode)
	}
	if got := countPhotoAtomsForArcade(t, app, arcadeID); got != 0 {
		t.Fatalf("denied upload created %d photo atoms", got)
	}

	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatal(err)
	}
	if arcade.GetString("basic") != basicID || arcade.GetString("hour") != "" || arcade.GetString("game_v2") != "" {
		t.Fatalf("denied edits changed private draft: %#v", arcade)
	}
}

func TestArcadeBasic_PublicEditDoesNotRequireLevel(t *testing.T) {
	app := newArcadeTestApp(t)
	_, creator := createAuthUser(t, app)
	contributorToken, _ := createAuthUser(t, app)
	arcadeID, _ := seedPublicArcade(t, app, creator.Id, arcadeSeed{
		Name: "Public arcade", Address: "Seoul", Location: location{Lat: 37.5665, Lon: 126.978},
	})
	res := executeJSONRequest(t, app, http.MethodPut, "/arcade/basic", fmt.Sprintf(`{"arcade":%q,"name":"Community edit"}`, arcadeID), map[string]string{"Authorization": "Bearer " + contributorToken})
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected unrestricted public edit to succeed, got %d", res.StatusCode)
	}
}

func TestArcadeFlagAndNotice_RejectOtherUsersPrivateDraft(t *testing.T) {
	app := newArcadeTestApp(t)
	_, creator := createAuthUser(t, app)
	otherToken, other := createAuthUserWithTags(t, app, []string{"supporter"})
	setUserLevel(t, app, other.Id, 30)
	arcadeID, _ := seedArcade(t, app, creator.Id, arcadeSeed{
		Name: "Private draft", Address: "Seoul", Location: location{Lat: 37.5665, Lon: 126.978},
	})
	gameID := seedGameAtomForFlag(t, app, arcadeID)
	headers := map[string]string{"Authorization": "Bearer " + otherToken}
	createFlag := executeJSONRequest(t, app, http.MethodPost, "/arcade/flag", fmt.Sprintf(`{"arcade":%q,"game_id":%q,"disruption":"major","message":"broken"}`, arcadeID, gameID), headers)
	createFlag.Body.Close()
	if createFlag.StatusCode != http.StatusForbidden {
		t.Fatalf("expected flag creation 403, got %d", createFlag.StatusCode)
	}
	flagID := createFlagWithReactions(t, app, arcadeID, creator.Id, time.Now().UTC(), nil)
	reaction := postFlagReaction(t, app, otherToken, flagID, "fixed", "add")
	reaction.Body.Close()
	if reaction.StatusCode != http.StatusForbidden {
		t.Fatalf("expected flag reaction 403, got %d", reaction.StatusCode)
	}

	createNotice := executeJSONRequest(t, app, http.MethodPost, "/arcade/notice", string(createNoticeBody(arcadeID)), headers)
	createNotice.Body.Close()
	if createNotice.StatusCode != http.StatusForbidden {
		t.Fatalf("expected notice creation 403, got %d", createNotice.StatusCode)
	}
}
