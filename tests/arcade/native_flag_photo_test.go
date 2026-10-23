package arcade_test

import (
	"bytes"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

func TestNativeFlagPhotoPrivateScopeAndExactMembership(t *testing.T) {
	app := newArcadeTestApp(t)
	web, owner := createAuthUser(t, app)
	_, other := createAuthUser(t, app)
	_, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	issue := nativeParitySession(t, app)
	token, foreign, moderator := issue(owner), issue(other), issue(staff)
	arcade, _ := seedArcade(t, app, owner.Id, arcadeSeed{Name: "Private photo", Address: "Seoul", Location: location{Lat: 37.5, Lon: 127}})
	game := seedGameAtomForFlag(t, app, arcade)
	body, contentType := buildFlagMultipart(t, arcade, game, "minor", "Native image", []uploadTestFile{{Filename: "local.png", Content: pngFixtureBytes()}})
	response := executeRequest(t, app, "POST", "/native/arcade/flag", bytes.NewReader(body), map[string]string{"Authorization": "Bearer " + token, "Content-Type": contentType})
	var out map[string]any
	_ = json.NewDecoder(response.Body).Decode(&out)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode, out)
	}
	id := out["flag"].(string)
	filename := mustFindRecord(t, app, "arcade_flag", id).GetStringSlice("photos")[0]
	path := "/native/arcade/flag/photo?id=" + id + "&file=" + url.QueryEscape(filename)
	for _, tc := range []struct {
		credential string
		status     int
	}{{"", 401}, {web, 401}, {foreign, 404}, {token, 200}, {moderator, 200}} {
		res := executeJSONRequest(t, app, "GET", path, "", map[string]string{"Authorization": "Bearer " + tc.credential})
		res.Body.Close()
		if res.StatusCode != tc.status || !strings.Contains(res.Header.Get("Cache-Control"), "no-store") {
			t.Fatalf("private flag photo status %d want %d", res.StatusCode, tc.status)
		}
	}
	nativeParityJSON(t, app, "GET", path+"invalid", token, "", 404)
	nativeParityJSON(t, app, "GET", "/native/arcade/flag/photo?id="+id+"&file=../data.db", token, "", 404)
}
