package arcade_test

import (
	"bytes"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"io"
	"mime/multipart"
	"net/url"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
)

func TestFeedbackReplyPhotoAttachments(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	authorToken, author := createAuthUserWithTags(t, app, []string{})
	ownerToken, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	staffToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	otherToken, _ := createAuthUserWithTags(t, app, []string{})
	arcadeID, _ := seedPublicArcade(t, app, author.Id, arcadeSeed{Name: "Photo inquiry", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{arcadeID})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	// This conversation fixture explicitly enables reception for its official owner.
	configuredArcade, _ := app.FindRecordById("arcade", arcadeID)
	configuredArcade.Set("inquiry_settings", map[string]any{"mode": "internal"})
	if err := app.Save(configuredArcade); err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, token string, want int) map[string]any {
		t.Helper()
		res := executeJSONRequest(t, app, method, path, body, map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d want %d %v", method, path, res.StatusCode, want, out)
		}
		return out
	}
	feedback := call("POST", "/support_feedback", fmt.Sprintf(`{"message":"Original preserved","arcade":%q}`, arcadeID), authorToken, 200)
	id := feedback["id"].(string)
	upload := func(token, message string, count, size int, image bool, want int) map[string]any {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("feedbackId", id)
		_ = form.WriteField("message", message)
		for i := 0; i < count; i++ {
			part, err := form.CreateFormFile("photos", fmt.Sprintf("image%d.gif", i))
			if err != nil {
				t.Fatal(err)
			}
			data := make([]byte, size)
			if image {
				copy(data, []byte("GIF89a\x01\x00\x01\x00"))
			}
			_, _ = part.Write(data)
		}
		_ = form.Close()
		res := executeRequest(t, app, "POST", "/user/feedback/thread", &body, map[string]string{"Authorization": "Bearer " + token, "Content-Type": form.FormDataContentType()})
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("upload: %d want %d %v", res.StatusCode, want, out)
		}
		return out
	}
	upload(otherToken, "blocked", 1, 64, true, 404)
	upload("", "blocked", 1, 64, true, 401)
	upload(authorToken, "", 0, 0, true, 400)
	upload(authorToken, "invalid", 4, 64, true, 400)
	upload(authorToken, "invalid", 1, 64, false, 400)
	upload(authorToken, "invalid", 1, 15_000_001, true, 413)
	x, y := elliptic.P256().ScalarBaseMult([]byte{1})
	key := base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y))
	deviceAuth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	call("PUT", "/user/notifications/device", fmt.Sprintf(`{"subscription":{"endpoint":"https://fcm.googleapis.com/fcm/send/photo-author","keys":{"p256dh":%q,"auth":%q}},"locale":"en"}`, key, deviceAuth), authorToken, 200)
	replies := []map[string]any{}
	for _, token := range []string{authorToken, ownerToken, staffToken} {
		reply := upload(token, "", 3, 64, true, 201)
		if len(reply["photos"].([]any)) != 3 || reply["message"] != "" {
			t.Fatal(reply)
		}
		replies = append(replies, reply)
	}
	last := replies[len(replies)-1]
	path := "/user/feedback/thread/photo?id=" + last["id"].(string) + "&file=" + url.QueryEscape(last["photos"].([]any)[0].(string))
	for _, token := range []string{authorToken, ownerToken, staffToken} {
		res := executeJSONRequest(t, app, "GET", path, "", map[string]string{"Authorization": "Bearer " + token})
		data, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 || !bytes.HasPrefix(data, []byte("GIF89a")) {
			t.Fatalf("photo download: %d %s", res.StatusCode, data)
		}
	}
	call("GET", path, "", otherToken, 404)
	call("GET", path, "", "", 401)
	call("GET", path+"../wrong", "", authorToken, 404)
	record, err := app.FindRecordById(messaging.Messages, last["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	raw := "/api/files/" + record.Collection().Id + "/" + record.Id + "/" + last["photos"].([]any)[0].(string)
	call("GET", raw, "", "", 404)
	call("GET", raw, "", authorToken, 404)
	for i := 0; i < 2; i++ {
		if err := supportfeedback.EnsureReplyPhotosSchema(app); err != nil {
			t.Fatal(err)
		}
	}
	thread := call("GET", "/user/feedback/thread?id="+id, "", authorToken, 200)
	if len(thread["items"].([]any)) != 3 || len(thread["items"].([]any)[2].(map[string]any)["photos"].([]any)) != 3 {
		t.Fatal(thread)
	}
	original := thread["feedback"].(map[string]any)
	if original["message"] != "Original preserved" || original["status"] != "recognised" {
		t.Fatal("original/status changed")
	}
	notifications := call("GET", "/user/notifications", "", authorToken, 200)
	if notifications["unread"] != float64(2) {
		t.Fatal("image-only responder replies missing alerts", notifications)
	}
	notification, err := app.FindFirstRecordByFilter(usernotification.Inbox, "user={:user} && message={:message}", map[string]any{"user": author.Id, "message": last["id"]})
	if err != nil {
		t.Fatal("image-only reply notification missing", err)
	}
	deliveries, err := app.FindRecordsByFilter(usernotification.Deliveries, "notification={:notification}", "", 0, 0, map[string]any{"notification": notification.Id})
	if err != nil || len(deliveries) != 1 {
		t.Fatal("image-only reply push not queued", err)
	}
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	call("GET", path, "", ownerToken, 404)
}
