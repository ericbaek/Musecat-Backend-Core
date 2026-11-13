package arcade_test

import (
	"bytes"
	"image/png"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/service/announcement"
	"github.com/pocketbase/dbx"
)

const announcementAdminPath = "/moderation/announcements"

func TestNativeAnnouncementManagementSharesWebStoryAndImages(t *testing.T) {
	app := nativeNearbyApp(t, true)
	web, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, session := nativeNearbySession(t, app, staff)
	_, reader := createAuthUser(t, app)
	_, readerSession := nativeNearbySession(t, app, reader)
	native := session.AccessToken
	item := uploadAnnouncement(t, app, native, []string{"ko", "ja"})
	id := item["id"].(string)
	path := announcementAdminPath + "/" + id
	checkList := func(item map[string]any) {
		t.Helper()
		list := nativeBulkRequest(t, app, "GET", announcementAdminPath, native, nil, 200).(map[string]any)
		if !reflect.DeepEqual(list, announcementCall(t, app, "GET", announcementAdminPath, web, nil, 200)) ||
			!reflect.DeepEqual(list["items"], []any{item}) {
			t.Fatal("Native mutation response and both staff lists must show the same canonical story")
		}
	}
	checkList(item)
	if item["status"] != "draft" {
		t.Fatal("new story must be a draft")
	}
	var koImage, jaImage, removed string
	localizations := map[string]any{}
	for locale, raw := range item["localizations"].(map[string]any) {
		content := raw.(map[string]any)
		slides := content["slides"].([]any)
		retained := slides[1].(map[string]any)
		localizations[locale] = map[string]any{"title": "Updated " + locale, "summary": content["summary"],
			"slides": []map[string]any{{"image": retained["fileName"], "alt": retained["alt"]}}}
		if locale == "ko" {
			koImage = retained["imageUrl"].(string)
			removed = slides[0].(map[string]any)["imageUrl"].(string)
		} else {
			jaImage = retained["imageUrl"].(string)
		}
	}
	updated := announcementCall(t, app, "PUT", path, native, map[string]any{"localizations": localizations}, 200)
	checkList(updated)
	row, err := app.FindRecordById(announcement.Collection, id)
	if err != nil || row.GetString("created_by") != staff.Id || len(row.GetStringSlice("images")) != 2 ||
		announcement.Read(row)["ko"].Title != "Updated ko" {
		t.Fatal("draft update lost actor, retained files, or localized copy", err)
	}
	nativePhotoBytes(t, app, removed, native, 404, "")
	response := executeJSONRequest(t, app, "GET", koImage, "", map[string]string{"Authorization": "Bearer " + native})
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || response.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(response.Header.Get("Cache-Control"), "no-store") ||
		!bytes.Equal(body, nativePhotoBytes(t, app, koImage, web, 200, "private, no-store")) {
		t.Fatal("draft image bytes or private response headers differ", err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(body))
	if err != nil || config.Width != 9 || config.Height != 16 {
		t.Fatal("draft image differs from uploaded PNG", err)
	}
	nativePhotoBytes(t, app, koImage, "", 404, "")
	nativePhotoBytes(t, app, koImage, readerSession.AccessToken, 404, "")
	published := announcementCall(t, app, "POST", path+"/publish", native, map[string]any{"locales": []string{"ko"}, "sendPush": false}, 200)
	checkList(published)
	retried := announcementCall(t, app, "POST", path+"/publish", web, map[string]any{"locales": []string{"ko"}, "sendPush": true}, 200)
	row, err = app.FindRecordById(announcement.Collection, id)
	if err != nil || !row.GetBool("suppress_device_push") || published["status"] != "published" ||
		!reflect.DeepEqual(published["locales"], []any{"ko"}) || !reflect.DeepEqual(published, retried) {
		t.Fatal("publish retry changed publication identity, locale, or delivery choice", err)
	}
	announcementCall(t, app, "POST", path+"/publish", native, map[string]any{"locales": []string{"ja"}}, 409)
	invalid := executeJSONRequest(t, app, "GET", koImage, "", map[string]string{
		"Authorization": "Bearer " + strings.Repeat("z", 43), "Cookie": "pb_auth=" + web,
	})
	invalid.Body.Close()
	if invalid.StatusCode != 401 || !strings.Contains(invalid.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("invalid Native bearer must not fall back to public image access or a Web cookie")
	}
	if !bytes.Equal(body, nativePhotoBytes(t, app, koImage, "", 200, "private, no-store")) {
		t.Fatal("selected public image changed")
	}
	nativePhotoBytes(t, app, jaImage, "", 404, "")
	nativePhotoBytes(t, app, jaImage, readerSession.AccessToken, 404, "")
	archived := announcementCall(t, app, "POST", path+"/archive", native, nil, 200)
	checkList(archived)
	if archived["status"] != "archived" || !reflect.DeepEqual(archived, announcementCall(t, app, "POST", path+"/archive", web, nil, 200)) {
		t.Fatal("archive must be terminal and idempotent across clients")
	}
	nativePhotoBytes(t, app, koImage, "", 404, "")
	announcementCall(t, app, "PUT", path, native, map[string]any{"localizations": localizations}, 409)
}

func TestNativeAnnouncementManagementCurrentCredentialsAndRouteScope(t *testing.T) {
	for _, mode := range []string{"role", "invalid", "expired", "revoked", "banned", "withdrawn", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			app := nativeNearbyApp(t, mode != "disabled")
			web, staff := createAuthUserWithTags(t, app, []string{"developer"})
			service, session := nativeNearbySession(t, app, staff)
			item := uploadAnnouncement(t, app, web, []string{"ko"})
			id := item["id"].(string)
			path := announcementAdminPath + "/" + id
			image := item["localizations"].(map[string]any)["ko"].(map[string]any)["slides"].([]any)[0].(map[string]any)["imageUrl"].(string)
			token := session.AccessToken
			status, imageStatus := 401, 401
			switch mode {
			case "role":
				nativeBulkRequest(t, app, "GET", announcementAdminPath, token, nil, 200)
				staff.Set("tags", []string{})
				if err := app.Save(staff); err != nil {
					t.Fatal(err)
				}
				status, imageStatus = 403, 404
			case "invalid":
				token = strings.Repeat("z", 43)
			case "expired":
				if _, err := app.DB().NewQuery("UPDATE native_auth_session SET access_expires=0 WHERE user_id={:id}").Bind(dbx.Params{"id": staff.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if err := service.Revoke(app, session.RefreshToken); err != nil {
					t.Fatal(err)
				}
			case "banned":
				seedUserBan(t, app, staff.Id, "", "announcement policy fixture", time.Now().Add(time.Hour))
				status, imageStatus = 403, 403
			case "withdrawn":
				staff.Set("withdrawn", true)
				if err := app.Save(staff); err != nil {
					t.Fatal(err)
				}
			case "disabled":
				if user, err := service.Authenticate(app, token); err != nil || user.Id != staff.Id {
					t.Fatal("disabled client fixture must have a valid session", err)
				}
				imageStatus = 404
			}
			headers := map[string]string{"Authorization": "Bearer " + token}
			if mode == "invalid" {
				headers["Cookie"] = "pb_auth=" + web
			}
			for _, op := range []struct {
				method, path string
				want         int
			}{
				{"GET", announcementAdminPath, status}, {"POST", announcementAdminPath, status}, {"PUT", path, status},
				{"POST", path + "/publish", status}, {"POST", path + "/archive", status}, {"GET", image, imageStatus},
			} {
				res := executeJSONRequest(t, app, op.method, op.path, "{}", headers)
				res.Body.Close()
				if res.StatusCode != op.want || (mode != "disabled" && !strings.Contains(res.Header.Get("Cache-Control"), "no-store")) {
					t.Fatalf("%s %s status=%d want=%d cache=%q", op.method, op.path, res.StatusCode, op.want, res.Header.Get("Cache-Control"))
				}
			}
			count, err := app.CountRecords(announcement.Collection)
			if err != nil || count != 1 {
				t.Fatal("denied management operations changed the story count", count, err)
			}
			row, err := app.FindRecordById(announcement.Collection, id)
			if err != nil || row.GetString("status") != "draft" || len(row.GetStringSlice("images")) != 2 {
				t.Fatal("denied management operations changed the draft", err)
			}
			if mode == "role" {
				staff.Set("tags", []string{"moderator"})
				if err := app.Save(staff); err != nil {
					t.Fatal(err)
				}
				nativeBulkRequest(t, app, "GET", announcementAdminPath, token, nil, 200)
				for _, op := range []struct {
					method, path string
					want         int
				}{
					{"GET", "/native" + announcementAdminPath, 404}, {"POST", "/native" + announcementAdminPath, 404},
					{"PUT", "/native" + path, 404}, {"POST", "/native" + path + "/publish", 404}, {"POST", "/native" + path + "/archive", 404},
					{"GET", "/native" + image, 404}, {"GET", "/api/collections/announcement/records", 403},
					{"GET", "/announcements/" + id, 404}, {"GET", "/announcements/unseen", 401}, {"POST", "/announcements/" + id + "/view", 401},
				} {
					res := executeJSONRequest(t, app, op.method, op.path, "{}", headers)
					res.Body.Close()
					if res.StatusCode != op.want {
						t.Fatalf("%s %s status=%d want=%d", op.method, op.path, res.StatusCode, op.want)
					}
				}
			}
		})
	}
}
