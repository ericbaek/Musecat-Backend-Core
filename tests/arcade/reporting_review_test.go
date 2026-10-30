package arcade_test

import (
	"bytes"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"testing"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
	"github.com/ericbaek/musecat-backend-core/handlers/arcade/reporting"
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestLegacySubscriptionTransfersUnifiedEndpointOwnership(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	oldToken, oldUser := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	newToken, newUser := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	arcadeID, _ := seedPublicArcade(t, app, oldUser.Id, arcadeSeed{Name: "Shared browser", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	for _, user := range []*core.Record{oldUser, newUser} {
		user.Set("owns", []string{arcadeID})
		if err := app.Save(user); err != nil {
			t.Fatal(err)
		}
	}
	x, y := elliptic.P256().ScalarBaseMult([]byte{1})
	key := base64.RawURLEncoding.EncodeToString(elliptic.Marshal(elliptic.P256(), x, y))
	auth := base64.RawURLEncoding.EncodeToString(make([]byte, 16))
	endpoint := "https://fcm.googleapis.com/fcm/send/shared-browser"
	subscription := fmt.Sprintf(`{"endpoint":%q,"keys":{"p256dh":%q,"auth":%q}}`, endpoint, key, auth)
	call := func(path, body, token string) {
		t.Helper()
		res := executeJSONRequest(t, app, http.MethodPut, path, body, map[string]string{"Authorization": "Bearer " + token})
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			var out any
			_ = json.NewDecoder(res.Body).Decode(&out)
			t.Fatalf("%s: status=%d body=%v", path, res.StatusCode, out)
		}
	}
	call("/user/notifications/device", `{"subscription":`+subscription+`}`, oldToken)
	call("/arcade/report-subscription", fmt.Sprintf(`{"arcade":%q,"subscription":%s}`, arcadeID, subscription), oldToken)
	call("/arcade/report-subscription", fmt.Sprintf(`{"arcade":%q,"subscription":%s}`, arcadeID, subscription), newToken)
	for _, collection := range []string{usernotification.Devices, arcadereport.Subscriptions} {
		filter := "user={:user} && endpoint={:endpoint}"
		if collection == arcadereport.Subscriptions {
			filter = "device.user={:user} && device.endpoint={:endpoint}"
		}
		rows, err := app.FindRecordsByFilter(collection, filter, "", 0, 0, map[string]any{"user": oldUser.Id, "endpoint": endpoint})
		if err != nil || len(rows) != 0 {
			t.Fatalf("previous account retained %s: count=%d err=%v", collection, len(rows), err)
		}
	}
	rows, err := app.FindRecordsByFilter(arcadereport.Subscriptions, "device.user={:user} && device.endpoint={:endpoint}", "", 0, 0, map[string]any{"user": newUser.Id, "endpoint": endpoint})
	if err != nil || len(rows) != 1 {
		t.Fatalf("new account registration missing: count=%d err=%v", len(rows), err)
	}
}

func TestReportingWritesRecheckPersistedAccount(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	_, author := createAuthUserWithTags(t, app, []string{})
	_, owner := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	arcadeID, _ := seedPublicArcade(t, app, author.Id, arcadeSeed{Name: "Private inquiry", Address: "Address", Location: location{Lat: 37.5, Lon: 127}})
	arcade, _ := app.FindRecordById("arcade", arcadeID)
	arcade.Set("public", false)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	owner.Set("owns", []string{arcadeID})
	if err := app.Save(owner); err != nil {
		t.Fatal(err)
	}
	feedbackID := seedSupportFeedback(t, app, author.Id, "Private inquiry", "waiting")
	inquiry, err := app.FindFirstRecordByFilter(messaging.Inquiries, "conversation={:c}", map[string]any{"c": feedbackID})
	if err != nil {
		t.Fatal(err)
	}
	inquiry.Set("inbox_type", "arcade")
	inquiry.Set("inbox_id", arcadeID)
	if err := app.Save(inquiry); err != nil {
		t.Fatal(err)
	}

	// Simulate ownership revocation after authentication populated the request.
	persisted, _ := app.FindRecordById("user", owner.Id)
	persisted.Set("owns", []string{})
	if err := app.Save(persisted); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, body string
		want       int
		handler    func(*core.RequestEvent) error
	}{
		{"/review/create", fmt.Sprintf(`{"arcade":%q,"message":"new inquiry"}`, arcadeID), 404, arcadeadmin.CreateSupportFeedback},
		{"/review/reply", fmt.Sprintf(`{"feedbackId":%q,"message":"reply"}`, feedbackID), 404, arcadeadmin.ReplySupportFeedback},
		{"/review/settings", fmt.Sprintf(`{"arcade":%q,"mode":"internal"}`, arcadeID), 403, reporting.UpdateSettings},
	} {
		t.Run(tc.path, func(t *testing.T) {
			app.OnServe().BindFunc(func(e *core.ServeEvent) error {
				e.Router.POST(tc.path, func(re *core.RequestEvent) error {
					re.Auth = owner
					return tc.handler(re)
				})
				return e.Next()
			})
			res := executeJSONRequest(t, app, http.MethodPost, tc.path, tc.body, nil)
			defer res.Body.Close()
			if res.StatusCode != tc.want {
				var out any
				_ = json.NewDecoder(res.Body).Decode(&out)
				t.Fatalf("status=%d want=%d body=%v", res.StatusCode, tc.want, out)
			}
		})
	}
	messages, err := app.FindRecordsByFilter(messaging.Messages, "conversation={:id} && sequence>1", "", 0, 0, map[string]any{"id": feedbackID})
	if err != nil || len(messages) != 0 {
		t.Fatal("revoked account posted a reply", err)
	}
}

func TestCreateSupportFeedbackRemovesSpilledMultipartFiles(t *testing.T) {
	app := newArcadeTestApp(t)
	defer app.Cleanup()
	// Exceed ParseMultipartForm's memory threshold so cleanup exercises disk files.
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("message", "Attachment cleanup"); err != nil {
		t.Fatal(err)
	}
	photo, err := writer.CreateFormFile("photos", "oversized.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := photo.Write([]byte(strings.Repeat("x", 33<<20))); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	checked := false
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		e.Router.POST("/review/multipart", func(re *core.RequestEvent) error {
			err := arcadeadmin.CreateSupportFeedback(re)
			if re.Request.MultipartForm == nil || len(re.Request.MultipartForm.File["photos"]) != 1 {
				t.Fatal("multipart form was not parsed")
			}
			file, openErr := re.Request.MultipartForm.File["photos"][0].Open()
			if openErr == nil {
				file.Close()
				t.Fatal("multipart temporary file retained after handler")
			}
			if !os.IsNotExist(openErr) {
				t.Fatalf("unexpected attachment error: %v", openErr)
			}
			checked = true
			return err
		}).Bind(apis.BodyLimit(arcadeadmin.MaxSupportFeedbackBodyBytes))
		return e.Next()
	})
	res := executeRequest(t, app, http.MethodPost, "/review/multipart", &body, map[string]string{"Content-Type": writer.FormDataContentType()})
	defer res.Body.Close()
	if !checked || res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("attachment check=%v status=%d", checked, res.StatusCode)
	}
}
