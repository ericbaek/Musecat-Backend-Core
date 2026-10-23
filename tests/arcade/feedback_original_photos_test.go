package arcade_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"testing"
)

func TestOriginalInquiryPhotosRequireCurrentParticipants(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	authorToken, author := createAuthUserWithTags(t, app, []string{})
	ownerToken, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	staffToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	otherToken, _ := createAuthUserWithTags(t, app, []string{})
	arcadeID, _ := seedPublicArcade(t, app, author.Id, arcadeSeed{Name: "Original photos", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
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
	create := func(token string) map[string]any {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("message", "Private original attachment")
		_ = form.WriteField("arcade", arcadeID)
		photo, err := form.CreateFormFile("photos", "private.gif")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = photo.Write(append([]byte("GIF89a\x01\x00\x01\x00"), make([]byte, 54)...))
		_ = form.Close()
		res := executeRequest(t, app, "POST", "/support_feedback", &body, map[string]string{"Authorization": "Bearer " + token, "Content-Type": form.FormDataContentType()})
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != 200 {
			t.Fatalf("create: %d %v", res.StatusCode, out)
		}
		return out
	}
	get := func(path, token string, want int) {
		t.Helper()
		res := executeJSONRequest(t, app, "GET", path, "", map[string]string{"Authorization": "Bearer " + token})
		data, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("GET %s: %d want %d: %s", path, res.StatusCode, want, data)
		}
		if want == 200 && (!bytes.HasPrefix(data, []byte("GIF89a")) || res.Header.Get("Cache-Control") != "private, no-store") {
			t.Fatal("photo bytes or private cache policy missing")
		}
	}
	feedback := create(authorToken)
	id := feedback["id"].(string)
	filename := feedback["photos"].([]any)[0].(string)
	path := "/user/feedback/thread/photo?feedbackId=" + id + "&file=" + url.QueryEscape(filename)
	for _, token := range []string{authorToken, ownerToken, staffToken} {
		get(path, token, 200)
	}
	get(path, otherToken, 404)
	get(path, "", 401)
	get(path+"wrong", authorToken, 404)
	get(path+"&id=ambiguous", authorToken, 400)
	get("/user/feedback/thread/photo?file="+url.QueryEscape(filename), authorToken, 400)
	get(fmt.Sprintf("/api/files/support_feedback/%s/%s", id, filename), "", 404)
	get(fmt.Sprintf("/api/files/support_feedback/%s/%s", id, filename), authorToken, 404)
	// Guests receive a submission receipt; its id and filename grant no read capability.
	guest := create("")
	guestID := guest["id"].(string)
	guestFile := guest["photos"].([]any)[0].(string)
	guestPath := "/user/feedback/thread/photo?feedbackId=" + guestID + "&file=" + url.QueryEscape(guestFile)
	get(guestPath, "", 401)
	get(guestPath, authorToken, 404)
	get(guestPath, ownerToken, 200)
	get(guestPath, staffToken, 200)
	get(fmt.Sprintf("/api/files/support_feedback/%s/%s", guestID, guestFile), "", 404)
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	get(path, ownerToken, 404)
	get(guestPath, ownerToken, 404)
}
