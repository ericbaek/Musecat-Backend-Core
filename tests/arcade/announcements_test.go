package arcade_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/announcement"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func announcementCall(t *testing.T, app *tests.TestApp, method, path, token string, input any, want int) map[string]any {
	t.Helper()
	body, _ := json.Marshal(input)
	response := executeJSONRequest(t, app, method, path, string(body), map[string]string{"Authorization": "Bearer " + token})
	defer response.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(response.Body).Decode(&result)
	if response.StatusCode != want {
		t.Fatalf("%s %s: status=%d want=%d body=%v", method, path, response.StatusCode, want, result)
	}
	return result
}

func uploadAnnouncement(t *testing.T, app *tests.TestApp, token string, locales []string) map[string]any {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 9, 16))); err != nil {
		t.Fatal(err)
	}
	localized := map[string]any{}
	for _, locale := range locales {
		localized[locale] = map[string]any{"title": locale + " title", "summary": locale + " summary", "slides": []map[string]string{{"image": "upload:0", "alt": locale + " first"}, {"image": "upload:1", "alt": locale + " second"}}}
		for index := 0; index < 2; index++ {
			part, err := writer.CreateFormFile("images_"+locale, fmt.Sprintf("%s-%d.png", locale, index))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(picture.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
	}
	metadata, _ := json.Marshal(localized)
	_ = writer.WriteField("localizations", string(metadata))
	_ = writer.Close()
	response := executeRequest(t, app, "POST", "/moderation/announcements", &buffer, map[string]string{"Authorization": "Bearer " + token, "Content-Type": writer.FormDataContentType()})
	defer response.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(response.Body).Decode(&result)
	if response.StatusCode != 201 {
		t.Fatalf("upload: %d %v", response.StatusCode, result)
	}
	return result
}

func TestAnnouncementsPublicationLocalizationViewsAndArchive(t *testing.T) {
	app := newArcadeTestApp(t)
	staffToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	userToken, user := createAuthUser(t, app)
	otherToken, _ := createAuthUser(t, app)
	call := func(method, path, token string, input any, want int) map[string]any {
		return announcementCall(t, app, method, path, token, input, want)
	}
	call("POST", "/moderation/announcements", userToken, map[string]any{"localizations": map[string]any{}}, 403)
	call("GET", "/moderation/announcements", "", nil, 401)
	partial := uploadAnnouncement(t, app, staffToken, []string{"ko"})
	partialID := partial["id"].(string)
	call("POST", "/moderation/announcements/"+partialID+"/publish", staffToken, map[string]any{"locales": announcement.Locales}, 400)
	call("GET", "/announcements/"+partialID, userToken, nil, 404)
	call("GET", "/announcements/"+partialID, staffToken, nil, 200)

	item := uploadAnnouncement(t, app, staffToken, announcement.Locales)
	id := item["id"].(string)
	localizations := item["localizations"].(map[string]any)
	ko := localizations["ko"].(map[string]any)
	slides := ko["slides"].([]any)
	imageURL := slides[0].(map[string]any)["imageUrl"].(string)
	response := executeJSONRequest(t, app, "GET", imageURL, "", nil)
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("draft image visible anonymously", response.StatusCode)
	}

	// Reordering and removing retained files uses metadata, without uploading again.
	input := map[string]any{}
	for locale, value := range localizations {
		content := value.(map[string]any)
		old := content["slides"].([]any)
		last := old[1].(map[string]any)
		input[locale] = map[string]any{"title": content["title"], "summary": content["summary"], "slides": []map[string]string{{"image": last["fileName"].(string), "alt": last["alt"].(string)}}}
	}
	updated := call("PUT", "/moderation/announcements/"+id, staffToken, map[string]any{"localizations": input}, 200)
	if len(updated["localizations"].(map[string]any)["ko"].(map[string]any)["slides"].([]any)) != 1 {
		t.Fatal(updated)
	}
	row, _ := app.FindRecordById(announcement.Collection, id)
	if len(row.GetStringSlice("images")) != 3 {
		t.Fatal("removed files were retained", row.GetStringSlice("images"))
	}

	call("POST", "/moderation/announcements/"+id+"/publish", staffToken, map[string]any{"locales": announcement.Locales}, 200)
	call("POST", "/moderation/announcements/"+id+"/publish", staffToken, map[string]any{"locales": announcement.Locales}, 200)
	call("PUT", "/moderation/announcements/"+id, staffToken, map[string]any{"localizations": input}, 409)
	list := call("GET", "/announcements", "", nil, 200)
	if len(list["items"].([]any)) != 1 {
		t.Fatal("draft leaked to public list", list)
	}
	public := call("GET", "/announcements/"+id, "", nil, 200)
	imageURL = public["localizations"].(map[string]any)["ja"].(map[string]any)["slides"].([]any)[0].(map[string]any)["imageUrl"].(string)
	response = executeJSONRequest(t, app, "GET", imageURL, "", nil)
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("published image unavailable", response.StatusCode)
	}
	if call("GET", "/announcements/unseen", userToken, nil, 200)["announcement"] == nil {
		t.Fatal("latest update was not offered")
	}

	if err := app.RunInTransaction(func(tx core.App) error {
		_, err := usernotification.UpsertDeviceTx(tx, user.Id, "https://fcm.googleapis.com/fcm/send/announcement", map[string]string{"endpoint": "unused"}, "ja")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 3; repeat++ {
		if err := usernotification.FanoutAnnouncementsBatch(context.Background(), app); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := app.FindRecordsByFilter(usernotification.Inbox, "kind='announcement'", "", 0, 0)
	if err != nil || len(rows) != 3 {
		t.Fatalf("broadcast duplicated or missed users: %d %v", len(rows), err)
	}
	deliveries, err := app.FindRecordsByFilter(usernotification.Deliveries, "notification.kind='announcement'", "", 0, 0)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("deliveries: %d %v", len(deliveries), err)
	}
	inbox := call("GET", "/user/notifications?locale=ja", userToken, nil, 200)
	notice := inbox["items"].([]any)[0].(map[string]any)
	if notice["title"] != "ja title" || notice["message"] != "ja summary" || notice["url"] != "/community/updates?announcement="+id {
		t.Fatal(notice)
	}
	call("POST", "/user/notifications/read", userToken, map[string]string{"id": notice["id"].(string)}, 200)
	if call("GET", "/announcements/unseen", userToken, nil, 200)["announcement"] == nil {
		t.Fatal("inbox read incorrectly marked story viewed")
	}
	call("POST", "/announcements/"+id+"/view", userToken, nil, 200)
	call("POST", "/announcements/"+id+"/view", userToken, nil, 200)
	if call("GET", "/announcements/unseen", userToken, nil, 200)["announcement"] != nil {
		t.Fatal("view not persisted")
	}
	if call("GET", "/announcements/unseen", otherToken, nil, 200)["announcement"] == nil {
		t.Fatal("view leaked across users")
	}
	if call("GET", "/announcements/"+id, userToken, nil, 200)["viewed"] != true {
		t.Fatal("replay lost account view state")
	}
	call("POST", "/moderation/announcements/"+id+"/archive", staffToken, nil, 200)
	call("POST", "/moderation/announcements/"+id+"/publish", staffToken, map[string]any{"locales": announcement.Locales}, 409)
	call("GET", "/announcements/"+id, userToken, nil, 404)
	call("GET", "/announcements?before="+id, userToken, nil, 200)
	if len(call("GET", "/user/notifications", otherToken, nil, 200)["items"].([]any)) != 0 {
		t.Fatal("archived event still visible")
	}
}

func TestAnnouncementFanoutBoundedResumableAndAudienceCutoff(t *testing.T) {
	app := newArcadeTestApp(t)
	staffToken, staff := createAuthUserWithTags(t, app, []string{"developer"})
	item := uploadAnnouncement(t, app, staffToken, announcement.Locales)
	id := item["id"].(string)
	for index := 0; index < 205; index++ {
		_, err := app.DB().NewQuery("INSERT INTO user (id, tokenKey, email, username, created, withdrawn) VALUES ({:id},{:id},{:email},{:username},'2020-01-01 00:00:00.000Z',{:withdrawn})").Bind(dbx.Params{"id": fmt.Sprintf("broadcast%06d", index), "email": fmt.Sprintf("broadcast%d@example.com", index), "username": fmt.Sprintf("broadcast%d", index), "withdrawn": index == 204}).Execute()
		if err != nil {
			t.Fatal(err)
		}
	}
	announcementCall(t, app, "POST", "/moderation/announcements/"+id+"/publish", staffToken, map[string]any{"locales": announcement.Locales}, 200)
	// A later account must not receive an old broadcast when a later batch runs.
	_, err := app.DB().NewQuery("INSERT INTO user (id,tokenKey,email,username,created) VALUES ('lateruser000001','latertokenkey001','later@example.com','lateruser','2099-01-01 00:00:00.000Z')").Execute()
	if err != nil {
		t.Fatal(err)
	}
	if err := usernotification.FanoutAnnouncementsBatch(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	rows, _ := app.FindRecordsByFilter(usernotification.Inbox, "kind='announcement'", "", 0, 0)
	if len(rows) != 100 {
		t.Fatalf("batch size=%d", len(rows))
	}
	rows[0].Set("read", true)
	if err := app.Save(rows[0]); err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 4; repeat++ {
		if err := usernotification.FanoutAnnouncementsBatch(context.Background(), app); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := app.FindRecordsByFilter(usernotification.Inbox, "kind='announcement'", "", 0, 0)
	if len(all) != 205 {
		t.Fatalf("expected 204 users plus staff %s, got %d", staff.Id, len(all))
	}
	kept, _ := app.FindRecordById(usernotification.Inbox, rows[0].Id)
	if !kept.GetBool("read") {
		t.Fatal("retry reset read state")
	}
	publication, _ := app.FindRecordById(announcement.Collection, id)
	if !publication.GetBool("broadcast_done") {
		t.Fatal("broadcast never completed")
	}
	publication.Set("broadcast_cursor", "")
	publication.Set("broadcast_done", false)
	if err := app.Save(publication); err != nil {
		t.Fatal(err)
	}
	if err := usernotification.FanoutAnnouncementsBatch(context.Background(), app); err != nil {
		t.Fatal(err)
	}
	all, _ = app.FindRecordsByFilter(usernotification.Inbox, "kind='announcement'", "", 0, 0)
	if len(all) != 205 {
		t.Fatal("replayed batch duplicated events")
	}
}

func TestAnnouncementSelectedLocalesConstrainPublicContentInboxAndDevices(t *testing.T) {
	app := newArcadeTestApp(t)
	staffToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	userToken, user := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	call := func(method, path, token string, input any, want int) map[string]any {
		return announcementCall(t, app, method, path, token, input, want)
	}
	// A release needs only its selected language, not all supported languages.
	english := uploadAnnouncement(t, app, staffToken, []string{"en"})
	call("POST", "/moderation/announcements/"+english["id"].(string)+"/publish", staffToken, map[string]any{"locales": []string{"en"}}, 200)
	item := uploadAnnouncement(t, app, staffToken, announcement.Locales)
	id := item["id"].(string)
	if len(item["locales"].([]any)) != 0 {
		t.Fatal("draft already targets users", item)
	}
	koImage := item["localizations"].(map[string]any)["ko"].(map[string]any)["slides"].([]any)[0].(map[string]any)["imageUrl"].(string)
	for _, locales := range [][]string{nil, {}, {"ja", "ja"}, {"fr"}} {
		call("POST", "/moderation/announcements/"+id+"/publish", staffToken, map[string]any{"locales": locales}, 400)
	}
	published := call("POST", "/moderation/announcements/"+id+"/publish", staffToken, map[string]any{"locales": []string{"ja"}}, 200)
	if len(published["locales"].([]any)) != 1 || published["locales"].([]any)[0] != "ja" {
		t.Fatal(published)
	}
	call("POST", "/moderation/announcements/"+id+"/publish", staffToken, map[string]any{"locales": []string{"ja"}}, 200)
	call("POST", "/moderation/announcements/"+id+"/publish", staffToken, map[string]any{"locales": []string{"ko"}}, 409)
	call("GET", "/announcements/"+id+"?locale=ko", "", nil, 404)
	call("GET", "/announcements?locale=fr", "", nil, 400)
	if len(call("GET", "/announcements?locale=ko", "", nil, 200)["items"].([]any)) != 0 {
		t.Fatal("foreign-language release in public list")
	}
	if call("GET", "/announcements/unseen?locale=ko", userToken, nil, 200)["announcement"] != nil {
		t.Fatal("foreign-language automatic story")
	}
	public := call("GET", "/announcements/"+id+"?locale=ja", "", nil, 200)
	if len(public["localizations"].(map[string]any)) != 1 || public["localizations"].(map[string]any)["ja"] == nil {
		t.Fatal("unpublished variants leaked", public)
	}
	response := executeJSONRequest(t, app, "GET", koImage, "", nil)
	response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatal("unselected image public", response.StatusCode)
	}
	response = executeJSONRequest(t, app, "GET", koImage, "", map[string]string{"Authorization": "Bearer " + staffToken})
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("staff cannot preview unselected image", response.StatusCode)
	}

	if err := app.RunInTransaction(func(tx core.App) error {
		for _, locale := range []string{"ko", "ja"} {
			endpoint := "https://fcm.googleapis.com/fcm/send/selected-" + locale
			if _, err := usernotification.UpsertDeviceTx(tx, user.Id, endpoint, map[string]string{"endpoint": endpoint}, locale); err != nil {
				return err
			}
		}
		return usernotification.EnsureArcadeSetupNoticeTx(tx, user)
	}); err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 4; repeat++ {
		if err := usernotification.FanoutAnnouncementsBatch(context.Background(), app); err != nil {
			t.Fatal(err)
		}
	}
	deliveries, err := app.FindRecordsByFilter(usernotification.Deliveries, "notification.kind='announcement'", "", 0, 0)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("untargeted device queued: %d %v", len(deliveries), err)
	}
	device, _ := app.FindRecordById(usernotification.Devices, deliveries[0].GetString("device"))
	if device.GetString("locale") != "ja" {
		t.Fatal("wrong device locale")
	}
	koInbox := call("GET", "/user/notifications?locale=ko", userToken, nil, 200)
	jaInbox := call("GET", "/user/notifications?locale=ja", userToken, nil, 200)
	if len(koInbox["items"].([]any)) != 1 || koInbox["unread"] != float64(1) || koInbox["items"].([]any)[0].(map[string]any)["kind"] != "arcade_setup" {
		t.Fatal("locale filtering altered unrelated notifications", koInbox)
	}
	if len(jaInbox["items"].([]any)) != 2 || jaInbox["unread"] != float64(2) {
		t.Fatal("targeted announcement hidden", jaInbox)
	}
	row, err := app.FindFirstRecordByFilter(usernotification.Inbox, "user={:user} && announcement={:id}", dbx.Params{"user": user.Id, "id": id})
	if err != nil || usernotification.AnnouncementLocaleAllowed(row, "ko") || !usernotification.AnnouncementLocaleAllowed(row, "ja") {
		t.Fatal("unselected notification copy persisted", err)
	}
}
