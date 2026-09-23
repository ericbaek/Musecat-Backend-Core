package user_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

func TestProfileUpdateTextAndValidation(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, user := createAuthUser(t, app, true)
	headers := map[string]string{"Authorization": "Bearer " + token}
	body := `{"nickname":"  New name  ","bio":"First\r\nSecond","sns":{"items":[{"type":"website","link":"https://example.com"}]}}`
	res := doUserRequest(t, app, http.MethodPut, "/user/profile", headers, body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update status=%d", res.StatusCode)
	}
	profile := decodeJSON(t, res)
	if profile["id"] != user.Id || profile["nickname"] != "New name" || profile["bio"] != "First\nSecond" {
		t.Fatalf("unexpected profile: %#v", profile)
	}
	for _, invalid := range []string{
		`{"nickname":"   "}`,
		fmt.Sprintf(`{"nickname":%q}`, strings.Repeat("a", 26)),
		`{"nickname":"🇰🇷"}`,
		`{"nickname":"ok","bio":"1\n2\n3\n4\n5\n6"}`,
		`{"nickname":"ok","sns":{"items":[{"type":"website","link":"bad"}]}}`,
		`{"nickname":"ok","sns":{"items":[{"type":"discord","link":"abc"},{"type":"discord","link":"def"}]}}`,
	} {
		res = doUserRequest(t, app, http.MethodPut, "/user/profile", headers, invalid)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid %s status=%d", invalid, res.StatusCode)
		}
	}
	res = doUserRequest(t, app, http.MethodPut, "/user/profile", nil, body)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", res.StatusCode)
	}
}

func TestProfileUpdateBackgroundAccessAndFiles(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, user := createAuthUser(t, app, true)
	headers := map[string]string{"Authorization": "Bearer " + token}
	for _, body := range []string{
		`{"nickname":"ok","background_position":{"x":50,"y":40}}`,
		`{"nickname":"ok","background":null}`,
	} {
		res := doUserRequest(t, app, http.MethodPut, "/user/profile", headers, body)
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("low-level status=%d", res.StatusCode)
		}
	}
	setUserLevelForCountriesTest(t, app, user.Id, userhandler.LevelBaseExp(15))
	res := doUserRequest(t, app, http.MethodPut, "/user/profile", headers, `{"nickname":"ok","background_position":{"x":101,"y":40}}`)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid position status=%d", res.StatusCode)
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	_ = writer.WriteField("nickname", "Image user")
	file, err := writer.CreateFormFile("background", "background.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(pngFixtureBytes()); err != nil {
		t.Fatal(err)
	}
	avatar, err := writer.CreateFormFile("avatar", "avatar.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := avatar.Write(pngFixtureBytes()); err != nil {
		t.Fatal(err)
	}
	_ = writer.WriteField("background_position", `{"x":25,"y":75}`)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	headers["Content-Type"] = writer.FormDataContentType()
	res = doUserRequest(t, app, http.MethodPut, "/user/profile", headers, buf.String())
	if res.StatusCode != http.StatusOK {
		t.Fatalf("upload status=%d", res.StatusCode)
	}
	profile := decodeJSON(t, res)
	if profile["background"] == "" || profile["avatar"] == "" {
		t.Fatalf("images missing: %#v", profile)
	}
	position := profile["background_position"].(map[string]any)
	if position["x"] != float64(25) || position["y"] != float64(75) {
		t.Fatalf("position=%#v", position)
	}
	delete(headers, "Content-Type")
	res = doUserRequest(t, app, http.MethodPut, "/user/profile", headers, `{"nickname":"Image user","background":null}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("delete status=%d", res.StatusCode)
	}
	profile = decodeJSON(t, res)
	if profile["background"] != "" {
		t.Fatalf("background retained: %#v", profile)
	}
	if profile["avatar"] == "" {
		t.Fatal("omitted avatar was not preserved")
	}
	buf.Reset()
	writer = multipart.NewWriter(&buf)
	_ = writer.WriteField("nickname", "Image user")
	_ = writer.WriteField("avatar", "")
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	headers["Content-Type"] = writer.FormDataContentType()
	res = doUserRequest(t, app, http.MethodPut, "/user/profile", headers, buf.String())
	if res.StatusCode != http.StatusOK {
		t.Fatalf("avatar delete status=%d", res.StatusCode)
	}
	if decodeJSON(t, res)["avatar"] != "" {
		t.Fatal("avatar was not deleted")
	}
}

func TestProfileUpdateGamePreferences(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, user := createAuthUser(t, app, true)
	headers := map[string]string{"Authorization": "Bearer " + token}
	series := seedGameSeries(t, app, 99, "test", "test", "test")
	body, _ := json.Marshal(map[string]any{"series": []string{series.Id, " ", series.Id}, "series_public": false, "warp": true})
	res := doUserRequest(t, app, http.MethodPut, "/user/game-preferences", headers, string(body))
	if res.StatusCode != http.StatusOK {
		t.Fatalf("preferences status=%d", res.StatusCode)
	}
	payload := decodeJSON(t, res)
	ids := payload["series"].([]any)
	if len(ids) != 1 || ids[0] != series.Id || payload["warp"] != true || payload["series_public"] != false {
		t.Fatalf("preferences=%#v", payload)
	}
	rec, err := app.FindRecordById("user_info", user.Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.GetStringSlice("series")) != 1 {
		t.Fatalf("stored series=%#v", rec.GetStringSlice("series"))
	}
	res = doUserRequest(t, app, http.MethodPut, "/user/game-preferences", headers, `{"series":[],"series_public":true}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("preserve warp status=%d", res.StatusCode)
	}
	if decodeJSON(t, res)["warp"] != true {
		t.Fatal("warp was not preserved")
	}
	for _, invalid := range []string{`{"series":null,"series_public":true}`, `{"series":[],"series_public":"true"}`, `{"series":["missing"],"series_public":true}`, `{"series":[],"series_public":true,"warp":null}`} {
		res = doUserRequest(t, app, http.MethodPut, "/user/game-preferences", headers, invalid)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("invalid %s status=%d", invalid, res.StatusCode)
		}
	}
}

func TestProfileUpdateAccessBoundary(t *testing.T) {
	app := newUserFetchTestApp(t)
	collection, err := app.FindCollectionByNameOrId("user_info")
	if err != nil {
		t.Fatal(err)
	}
	if collection.UpdateRule != nil {
		t.Fatal("raw user_info updates must be locked")
	}
	token, _ := createAuthUser(t, app, false)
	headers := map[string]string{"Authorization": "Bearer " + token}
	res := doUserRequest(t, app, http.MethodPut, "/user/profile", headers, `{"nickname":"ok"}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("missing profile status=%d", res.StatusCode)
	}
	res = doUserRequest(t, app, http.MethodPut, "/user/game-preferences", headers, `{"series":[],"series_public":true}`)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("missing preferences status=%d", res.StatusCode)
	}
	ensureWithdrawFields(t, app)
	token, user := createAuthUser(t, app, true)
	user.Set("withdrawn", true)
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	headers["Authorization"] = "Bearer " + token
	res = doUserRequest(t, app, http.MethodPut, "/user/profile", headers, `{"nickname":"ok"}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("withdrawn profile status=%d", res.StatusCode)
	}
	res = doUserRequest(t, app, http.MethodPut, "/user/game-preferences", headers, `{"series":[],"series_public":true}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("withdrawn preferences status=%d", res.StatusCode)
	}
}
