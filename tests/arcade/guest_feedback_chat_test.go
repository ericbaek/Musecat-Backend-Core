package arcade_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/url"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
)

func TestGuestFeedbackChat(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	_, user := createAuthUserWithTags(t, app, []string{})
	ownerToken, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	outsiderToken, _ := createAuthUserWithTags(t, app, []string{})
	arcadeID, _ := seedPublicArcade(t, app, user.Id, arcadeSeed{Name: "Guest chat", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	owner.Set("owns", []string{arcadeID})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64)
	other := strings.Repeat("b", 64)
	call := func(method, path, body, guestKey, token string, want int) map[string]any {
		t.Helper()
		headers := map[string]string{}
		if guestKey != "" {
			headers[supportfeedback.GuestHeader] = guestKey
		}
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		res := executeJSONRequest(t, app, method, path, body, headers)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d want %d %v", method, path, res.StatusCode, want, out)
		}
		if strings.Contains(fmt.Sprint(out), key) || strings.Contains(fmt.Sprint(out), supportfeedback.GuestKeyHash(key)) {
			t.Fatal("guest credential exposed", out)
		}
		return out
	}
	call("PUT", "/arcade/inquiry-settings", fmt.Sprintf(`{"arcade":%q,"mode":"internal"}`, arcadeID), "", ownerToken, 200)
	inquiry := call("POST", "/support_feedback", fmt.Sprintf(`{"arcade":%q,"message":"Guest question"}`, arcadeID), key, "", 200)
	id := inquiry["id"].(string)
	record, err := app.FindFirstRecordByFilter(messaging.Inquiries, "legacy_feedback_id={:id}", map[string]any{"id": id})
	if err != nil {
		t.Fatal(err)
	}
	if record.GetString("guest_key_hash") != supportfeedback.GuestKeyHash(key) || record.GetString("requester_type") != "guest" {
		t.Fatal("guest credential not stored independently")
	}
	path := "/guest/feedback/thread?id=" + id
	call("GET", path, "", "", "", 401)
	call("GET", path, "", "bad", "", 401)
	call("GET", path, "", other, "", 404)
	call("GET", "/user/feedback/thread?id="+id, "", "", outsiderToken, 404)
	list := call("GET", "/guest/feedback?arcade="+arcadeID, "", key, "", 200)
	if len(list["items"].([]any)) != 1 {
		t.Fatal("guest cannot reopen own conversation", list)
	}
	if len(call("GET", "/guest/feedback", "", other, "", 200)["items"].([]any)) != 0 {
		t.Fatal("foreign inbox exposed")
	}
	thread := call("GET", path, "", key, "", 200)
	if thread["feedback"].(map[string]any)["guestChat"] != true {
		t.Fatal("missing guest chat capability")
	}
	body := fmt.Sprintf(`{"feedbackId":%q,"message":"Official answer"}`, id)
	call("POST", "/user/feedback/thread", body, "", ownerToken, 201)
	thread = call("GET", path, "", key, "", 200)
	if items := thread["items"].([]any); len(items) != 1 || items[0].(map[string]any)["message"] != "Official answer" {
		t.Fatal("guest did not receive answer", thread)
	}
	body = fmt.Sprintf(`{"feedbackId":%q,"message":"Follow-up","author":%q,"staff":true}`, id, owner.Id)
	call("POST", "/guest/feedback/thread", body, other, "", 404)
	reply := call("POST", "/guest/feedback/thread", body, key, "", 201)
	if reply["author"] != "" || reply["staff"] != false || reply["official"] != false {
		t.Fatal("guest forged author or role", reply)
	}
	if call("GET", path, "", key, "", 200)["feedback"].(map[string]any)["status"] != "waiting" {
		t.Fatal("guest follow-up did not reopen queue")
	}
	status := fmt.Sprintf(`{"feedbackId":%q,"status":"solved"}`, id)
	call("PATCH", "/guest/feedback/thread", status, other, "", 404)
	call("PATCH", "/guest/feedback/thread", status, key, "", 200)
	// Changing intake policy must preserve access to existing guest chats.
	call("PUT", "/arcade/inquiry-settings", fmt.Sprintf(`{"arcade":%q,"mode":"internal","loginRequired":true}`, arcadeID), "", ownerToken, 200)
	call("POST", "/support_feedback", fmt.Sprintf(`{"arcade":%q,"message":"New question"}`, arcadeID), key, "", 401)
	call("GET", path, "", key, "", 200)
	call("POST", "/guest/feedback/thread", body, key, "", 201)
	owner.Set("owns", []string{})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	call("GET", "/user/feedback/thread?id="+id, "", "", ownerToken, 404)
}

func TestGuestFeedbackPhotoPrivacy(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	staffToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	key := strings.Repeat("c", 64)
	other := strings.Repeat("d", 64)
	upload := func(path, id, guestKey, token string, want int) map[string]any {
		t.Helper()
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("message", "Photo inquiry")
		if id != "" {
			_ = form.WriteField("feedbackId", id)
		}
		part, _ := form.CreateFormFile("photos", "photo.gif")
		_, _ = part.Write([]byte("GIF89a\x01\x00\x01\x00"))
		_ = form.Close()
		headers := map[string]string{"Content-Type": form.FormDataContentType(), supportfeedback.GuestHeader: guestKey}
		if token != "" {
			headers["Authorization"] = "Bearer " + token
		}
		res := executeRequest(t, app, "POST", path, &body, headers)
		defer res.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != want {
			t.Fatalf("upload %s: %d %v", path, res.StatusCode, out)
		}
		return out
	}
	original := upload("/support_feedback", "", key, "", 200)
	id := original["id"].(string)
	guestReply := upload("/guest/feedback/thread", id, key, "", 201)
	staffReply := upload("/user/feedback/thread", id, "", staffToken, 201)
	for _, item := range []struct {
		record   map[string]any
		selector string
	}{{original, "feedbackId"}, {guestReply, "id"}, {staffReply, "id"}} {
		path := "/guest/feedback/thread/photo?" + item.selector + "=" + item.record["id"].(string) + "&file=" + url.QueryEscape(item.record["photos"].([]any)[0].(string))
		for _, tc := range []struct {
			key    string
			status int
		}{{key, 200}, {other, 404}, {"", 401}} {
			res := executeJSONRequest(t, app, "GET", path, "", map[string]string{supportfeedback.GuestHeader: tc.key})
			data, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != tc.status {
				t.Fatalf("photo %s: %d want %d", item.selector, res.StatusCode, tc.status)
			}
			if tc.status == 200 && (!bytes.HasPrefix(data, []byte("GIF89a")) || res.Header.Get("Cache-Control") != "private, no-store") {
				t.Fatal("incorrect private photo response")
			}
		}
	}
}
