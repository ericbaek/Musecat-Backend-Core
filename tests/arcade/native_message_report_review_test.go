package arcade_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func messageReviewPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 40, G: 90, B: 160, A: 255})
		}
	}
	var body bytes.Buffer
	if err := png.Encode(&body, img); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

func messageReviewRead(t *testing.T, app *tests.TestApp, path string, headers map[string]string, want int) (http.Header, []byte) {
	t.Helper()
	response := executeJSONRequest(t, app, "GET", path, "", headers)
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		t.Fatalf("report review status %d want %d", response.StatusCode, want)
	}
	if want == 200 && response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal("private report response cacheable")
	}
	if (want == 403 || want == 429) && len(strings.TrimPrefix(headers["Authorization"], "Bearer ")) == 43 && !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
		t.Fatal("Native denial became cacheable")
	}
	if want >= 400 {
		for _, secret := range []string{"Synthetic reported snapshot", "Unreported private content", "Synthetic private reason"} {
			if bytes.Contains(raw, []byte(secret)) {
				t.Fatal("denied review leaked private evidence")
			}
		}
	}
	return response.Header, raw
}
func reviewBearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}
func messageReviewState(t *testing.T, app *tests.TestApp) map[string]string {
	t.Helper()
	state := nativeMessagingState(t, app)
	rows, err := app.FindAllRecords(messaging.Reports)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(rows)
	if err != nil {
		t.Fatal(err)
	}
	state[messaging.Reports] = string(raw)
	return state
}

type nativeReviewFixture struct {
	app                                                             *tests.TestApp
	staff                                                           *core.Record
	staffWeb                                                        string
	staffSession                                                    nativeauth.Credentials
	readerSession                                                   nativeauth.Credentials
	reportID, reportID2, conversationID, photoName, unreportedPhoto string
}

func messageReviewFixture(t *testing.T, enabled bool) nativeReviewFixture {
	t.Helper()
	app := nativeNearbyApp(t, enabled)
	aWeb, _ := createAuthUser(t, app)
	bWeb, b := createAuthUser(t, app)
	staffWeb, staff := createAuthUserWithTags(t, app, []string{"moderator"})
	_, staffSession := nativeNearbySession(t, app, staff)
	_, readerSession := nativeNearbySession(t, app, b)
	conversation := nativeBulkRequest(t, app, "POST", "/user/conversations", aWeb, map[string]any{"kind": "dm", "peerUserId": b.Id}, 200).(map[string]any)
	id := conversation["id"].(string)
	upload := func(text, key string) messaging.Message {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		_ = form.WriteField("text", text)
		_ = form.WriteField("clientMessageId", key)
		part, err := form.CreateFormFile("photos", "synthetic.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write(messageReviewPNG(t)); err != nil {
			t.Fatal(err)
		}
		if err = form.Close(); err != nil {
			t.Fatal(err)
		}
		response := executeRequest(t, app, "POST", "/user/conversations/"+id+"/messages", &body, map[string]string{"Authorization": "Bearer " + aWeb, "Content-Type": form.FormDataContentType()})
		defer response.Body.Close()
		var result messaging.Message
		if response.StatusCode != 201 || json.NewDecoder(response.Body).Decode(&result) != nil {
			t.Fatal("synthetic photo setup failed")
		}
		return result
	}
	message := upload("Synthetic reported snapshot", "reported-photo")
	unreported := upload("Unreported private content", "unreported-photo")
	report := nativeBulkRequest(t, app, "POST", "/user/messages/"+message.ID+"/report", bWeb, map[string]any{"reason": "Synthetic private reason"}, 201).(map[string]any)
	second := nativeBulkRequest(t, app, "POST", "/user/conversations/"+id+"/messages", aWeb, map[string]any{"text": "Second synthetic report", "clientMessageId": "second-report"}, 201).(map[string]any)
	report2 := nativeBulkRequest(t, app, "POST", "/user/messages/"+second["id"].(string)+"/report", bWeb, map[string]any{"reason": "Synthetic second reason"}, 201).(map[string]any)
	return nativeReviewFixture{app, staff, staffWeb, staffSession, readerSession, report["id"].(string), report2["id"].(string), id, message.Photos[0].Name, unreported.Photos[0].Name}
}
func (f nativeReviewFixture) paths() []string {
	return []string{"/moderation/messages/reports?limit=1", "/moderation/messages/reports/" + f.reportID, "/moderation/messages/reports/" + f.reportID + "/attachments/" + f.photoName}
}
func TestCanonicalNativeMessageReviewMatchesWebAndPrivateSnapshotScope(t *testing.T) {
	f := messageReviewFixture(t, true)
	before := messageReviewState(t, f.app)
	var page map[string]any
	for i, path := range f.paths() {
		header, actual := messageReviewRead(t, f.app, path, reviewBearer(f.staffSession.AccessToken), 200)
		_, web := messageReviewRead(t, f.app, path, reviewBearer(f.staffWeb), 200)
		if i == 2 {
			img, err := png.Decode(bytes.NewReader(actual))
			if err != nil || img.Bounds().Dx() != 4 || img.Bounds().Dy() != 3 {
				t.Fatal("actual private evidence is not the synthetic image")
			}
			if !bytes.Equal(actual, messageReviewPNG(t)) || !bytes.Equal(actual, web) || header.Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("private snapshot photo changed")
			}
			if dir := os.Getenv("MUSECAT_CANONICAL_MESSAGING_FIXTURES"); dir != "" {
				if err := os.WriteFile(filepath.Join(dir, "message-review-photo.png"), actual, 0600); err != nil {
					t.Fatal(err)
				}
			}
			continue
		}
		var value, expected map[string]any
		if json.Unmarshal(actual, &value) != nil || json.Unmarshal(web, &expected) != nil || !reflect.DeepEqual(value, expected) {
			t.Fatal("Native/Web report DTO mismatch")
		}
		for _, field := range []string{"legacyCollection", "legacyRecord", "legacyPhotos", "guest_key_hash", "payload_hash", "access_token", "refresh_token", "email", "password"} {
			if bytes.Contains(actual, []byte(`"`+field+`"`)) {
				t.Fatal("internal field in public review DTO")
			}
		}
		if i == 0 {
			page = value
			exportNativeMessaging(t, "message-review-list", value)
		} else {
			message := value["message"].(map[string]any)
			if message["text"] != "Synthetic reported snapshot" || len(message["photos"].([]any)) != 1 || message["photos"].([]any)[0].(map[string]any)["url"] != f.paths()[2] {
				t.Fatal("evidence snapshot mismatch")
			}
			exportNativeMessaging(t, "message-review-detail", value)
		}
	}
	cursor, ok := page["nextCursor"].(string)
	if !ok || len(page["items"].([]any)) != 1 {
		t.Fatal("report cursor fixture incomplete")
	}
	_, nextRaw := messageReviewRead(t, f.app, "/moderation/messages/reports?limit=1&cursor="+cursor, reviewBearer(f.staffSession.AccessToken), 200)
	var next map[string]any
	_ = json.Unmarshal(nextRaw, &next)
	if len(next["items"].([]any)) != 1 || next["items"].([]any)[0].(map[string]any)["id"] == page["items"].([]any)[0].(map[string]any)["id"] {
		t.Fatal("cursor did not advance")
	}
	for _, query := range []string{"?limit=0", "?limit=51", "?limit=wrong", "?cursor=missing"} {
		messageReviewRead(t, f.app, "/moderation/messages/reports"+query, reviewBearer(f.staffSession.AccessToken), 400)
	}
	for _, path := range f.paths() {
		messageReviewRead(t, f.app, path, reviewBearer(f.readerSession.AccessToken), 403)
	}
	for _, path := range []string{"/user/conversations/" + f.conversationID, "/user/conversations/" + f.conversationID + "/messages", "/moderation/messages/reports/" + f.reportID + "/attachments/" + f.unreportedPhoto, "/moderation/messages/reports/" + f.reportID2 + "/attachments/" + f.photoName, "/moderation/messages/reports/" + f.reportID + "/attachments/missing.png"} {
		messageReviewRead(t, f.app, path, reviewBearer(f.staffSession.AccessToken), 404)
	}
	if !reflect.DeepEqual(before, messageReviewState(t, f.app)) {
		t.Fatal("report reads/denials mutated messages, reports, XP or history")
	}
	// Operational source edits never change the captured evidence.
	report, err := f.app.FindRecordById(messaging.Reports, f.reportID)
	if err != nil {
		t.Fatal(err)
	}
	source, err := f.app.FindRecordById(messaging.Messages, report.GetString("message"))
	if err != nil {
		t.Fatal(err)
	}
	source.Set("text", "Source repaired later")
	if err = f.app.Save(source); err != nil {
		t.Fatal(err)
	}
	_, snapshot := messageReviewRead(t, f.app, f.paths()[1], reviewBearer(f.staffSession.AccessToken), 200)
	if !bytes.Contains(snapshot, []byte("Synthetic reported snapshot")) || bytes.Contains(snapshot, []byte("Source repaired later")) {
		t.Fatal("captured evidence no longer immutable")
	}
}
func TestCanonicalNativeMessageReviewRechecksCurrentStaffAndRejectsCookieFallback(t *testing.T) {
	f := messageReviewFixture(t, true)
	for _, path := range f.paths() {
		messageReviewRead(t, f.app, path, nil, 401)
		messageReviewRead(t, f.app, path, map[string]string{"Cookie": "pb_auth=" + f.staffWeb}, 401)
		header, _ := messageReviewRead(t, f.app, path, map[string]string{"Cookie": "pb_auth=" + f.staffWeb, "Authorization": "Bearer " + strings.Repeat("z", 43)}, 401)
		if !strings.Contains(header.Get("Cache-Control"), "no-store") {
			t.Fatal("invalid Native response cacheable")
		}
	}
	for _, tags := range [][]string{{}, {"supporter"}, {"designer"}, {"developer"}, {"moderator"}} {
		f.staff.Set("tags", tags)
		if err := f.app.Save(f.staff); err != nil {
			t.Fatal(err)
		}
		want := 403
		if len(tags) > 0 && (tags[0] == "developer" || tags[0] == "moderator") {
			want = 200
		}
		before := messageReviewState(t, f.app)
		for _, path := range f.paths() {
			messageReviewRead(t, f.app, path, reviewBearer(f.staffSession.AccessToken), want)
			messageReviewRead(t, f.app, path, reviewBearer(f.staffWeb), want)
		}
		if !reflect.DeepEqual(before, messageReviewState(t, f.app)) {
			t.Fatal("role recheck changed domain")
		}
	}
}
func TestCanonicalNativeMessageReviewRejectsExpiredRevokedInactiveAndDisabled(t *testing.T) {
	paths := []string{"/moderation/messages/reports", "/moderation/messages/reports/missing", "/moderation/messages/reports/missing/attachments/missing.png"}
	for _, mode := range []string{"invalid", "expired", "family-expired", "removed-client", "revoked", "withdrawn", "ban", "account-key", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			app := nativeNearbyApp(t, mode != "disabled")
			web, user := createAuthUserWithTags(t, app, []string{"moderator"})
			service, s := nativeNearbySession(t, app, user)
			token := s.AccessToken
			if mode == "disabled" {
				if got, err := service.Authenticate(app, token); err != nil || got.Id != user.Id {
					t.Fatal("disabled fixture lacks valid same-DB session")
				}
			}
			switch mode {
			case "invalid":
				token = strings.Repeat("x", 43)
			case "expired", "family-expired", "removed-client":
				clause := map[string]string{"expired": "access_expires=0", "family-expired": "expires=0", "removed-client": "client='removed'"}[mode]
				if _, err := app.DB().NewQuery("UPDATE native_auth_session SET " + clause + " WHERE user_id={:user}").Bind(dbx.Params{"user": user.Id}).Execute(); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if err := service.Revoke(app, s.RefreshToken); err != nil {
					t.Fatal(err)
				}
			case "withdrawn":
				user.Set("withdrawn", true)
				if err := app.Save(user); err != nil {
					t.Fatal(err)
				}
			case "ban":
				seedUserBan(t, app, user.Id, "", "Synthetic review ban", time.Now().Add(time.Hour))
			case "account-key":
				user.RefreshTokenKey()
				if err := app.Save(user); err != nil {
					t.Fatal(err)
				}
			}
			before := messageReviewState(t, app)
			want := 401
			if mode == "ban" {
				want = 403
			}
			for _, path := range paths {
				header, _ := messageReviewRead(t, app, path, reviewBearer(token), want)
				if mode != "disabled" && !strings.Contains(header.Get("Cache-Control"), "no-store") {
					t.Fatal("enabled Native error cacheable")
				}
			}
			if mode == "disabled" {
				messageReviewRead(t, app, paths[0], reviewBearer(web), 200)
			}
			if !reflect.DeepEqual(before, messageReviewState(t, app)) {
				t.Fatal("rejected review session changed domain")
			}
		})
	}
}
func TestCanonicalNativeMessageReviewPreservesRateAndUnselectedBoundaries(t *testing.T) {
	f := messageReviewFixture(t, true)
	for _, path := range f.paths() {
		label := strings.Split(path, "?")[0]
		f.app.Settings().RateLimits = core.RateLimitsConfig{Enabled: true, Rules: []core.RateLimitRule{{Label: "GET " + label, MaxRequests: 1, Duration: 60}}}
		messageReviewRead(t, f.app, path, reviewBearer(f.staffSession.AccessToken), 200)
		messageReviewRead(t, f.app, path, reviewBearer(f.staffSession.AccessToken), 429)
	}
	f.app.Settings().RateLimits.Enabled = false
	for _, op := range []struct {
		method, path string
		status       int
	}{{"GET", "/native/moderation/messages/reports", 404}, {"GET", "/user/connections", 401}, {"PUT", "/user/inquiry-bots/arcade/missing", 401}, {"GET", "/api/collections/messaging_report/records", 403}} {
		r := executeJSONRequest(t, f.app, op.method, op.path, "{}", reviewBearer(f.staffSession.AccessToken))
		defer r.Body.Close()
		if r.StatusCode != op.status {
			t.Fatal("selected review auth expanded unrelated scope")
		}
	}
}
