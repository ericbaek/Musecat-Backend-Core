package arcade_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"mime/multipart"
	"os"
	"testing"
)

func TestMessagingDMPhotosAndReportSnapshotScope(t *testing.T) {
	app := newArcadeTestApp(t)
	aToken, a := createAuthUserWithTags(t, app, []string{})
	bToken, b := createAuthUserWithTags(t, app, []string{})
	staffToken, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	otherToken, _ := createAuthUserWithTags(t, app, []string{})
	_ = messaging.Preference(app, b.Id, "everyone")
	c, err := messaging.Create(app, a.Id, messaging.CreateInput{Kind: "dm", PeerUserID: b.Id})
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, token string, want int) map[string]any {
		t.Helper()
		res := executeJSONRequest(t, app, method, path, body, map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s %d want%d %v", method, path, res.StatusCode, want, out)
		}
		return out
	}
	upload := func(key string) messaging.Message {
		t.Helper()
		var buf bytes.Buffer
		form := multipart.NewWriter(&buf)
		_ = form.WriteField("text", "evidence original")
		_ = form.WriteField("clientMessageId", key)
		f, _ := form.CreateFormFile("photos", "photo.gif")
		_, _ = f.Write(append([]byte("GIF89a\x01\x00\x01\x00"), make([]byte, 64)...))
		_ = form.Close()
		res := executeRequest(t, app, "POST", "/user/conversations/"+c.ID+"/messages", &buf, map[string]string{"Authorization": "Bearer " + aToken, "Content-Type": form.FormDataContentType()})
		defer res.Body.Close()
		var m messaging.Message
		if err := json.NewDecoder(res.Body).Decode(&m); err != nil || res.StatusCode != 201 {
			t.Fatal("upload", res.StatusCode, err)
		}
		return m
	}
	m := upload("photo-one")
	another := upload("photo-two")
	photo := func(path, token string, want int) {
		t.Helper()
		res := executeJSONRequest(t, app, "GET", path, "", map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("photo %s %d want%d", path, res.StatusCode, want)
		}
		if want == 200 && res.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("public photo cache")
		}
	}
	photo(m.Photos[0].URL, bToken, 200)
	photo(m.Photos[0].URL, staffToken, 404)
	photo(m.Photos[0].URL, otherToken, 404)
	report := call("POST", "/user/messages/"+m.ID+"/report", `{"reason":"harassment"}`, bToken, 201)
	id := report["id"].(string)
	path := "/moderation/messages/reports/" + id
	call("GET", path, "", bToken, 403)
	call("GET", "/user/conversations/"+c.ID, "", staffToken, 404)
	snapshot := call("GET", path, "", staffToken, 200)
	if snapshot["message"].(map[string]any)["text"] != "evidence original" {
		t.Fatal(snapshot)
	}
	photo(path+"/attachments/"+m.Photos[0].Name, staffToken, 200)
	photo(path+"/attachments/"+another.Photos[0].Name, staffToken, 404)
	photo(path+"/attachments/"+m.Photos[0].Name, bToken, 403)
	// Evidence remains a snapshot even if privileged operational code repairs a source.
	record, err := app.FindRecordById(messaging.Messages, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.Set("text", "changed later")
	if err = app.Save(record); err != nil {
		t.Fatal(err)
	}
	snapshot = call("GET", path, "", staffToken, 200)
	if snapshot["message"].(map[string]any)["text"] != "evidence original" {
		t.Fatal("evidence mutated")
	}
	listed := call("GET", "/moderation/messages/reports?limit=1", "", staffToken, 200)
	if len(listed["items"].([]any)) != 1 {
		t.Fatal(listed)
	}
	staff.Set("tags", []string{})
	if err = app.Save(staff); err != nil {
		t.Fatal(err)
	}
	photo(path+"/attachments/"+m.Photos[0].Name, staffToken, 403)
}
func TestMessagingMultipartFailureRemovesTemporaryFiles(t *testing.T) {
	app := newArcadeTestApp(t)
	token, a := createAuthUserWithTags(t, app, []string{})
	_, b := createAuthUserWithTags(t, app, []string{})
	_ = messaging.Preference(app, b.Id, "everyone")
	c, err := messaging.Create(app, a.Id, messaging.CreateInput{Kind: "dm", PeerUserID: b.Id})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	_ = form.WriteField("clientMessageId", "badphoto")
	file, _ := form.CreateFormFile("photos", "invalid.bin")
	_, _ = file.Write(make([]byte, 2<<20))
	_ = form.Close()
	res := executeRequest(t, app, "POST", fmt.Sprintf("/user/conversations/%s/messages", c.ID), &buf, map[string]string{"Authorization": "Bearer " + token, "Content-Type": form.FormDataContentType()})
	res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatal(res.StatusCode)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 0 {
		t.Fatal("temporary files retained", files, err)
	}
}
