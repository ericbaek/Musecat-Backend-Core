package arcade_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/types"
)

func addOwnedArcade(tb testing.TB, app *tests.TestApp, user *core.Record, arcadeID string) {
	tb.Helper()

	user.Set("owns", []string{arcadeID})
	if err := app.Save(user); err != nil {
		tb.Fatalf("failed to save owned arcades: %v", err)
	}
}

func createNoticeBody(arcadeID string) []byte {
	payload := map[string]any{
		"arcade":   arcadeID,
		"type":     "alert",
		"document": noticeDocument("**notice**"),
		"link":     "https://example.com/notice",
		"until":    time.Date(2026, 7, 7, 0, 0, 0, 0, time.UTC),
		"priority": 2,
	}

	body, _ := json.Marshal(payload)
	return body
}

func noticeDocument(text string) map[string]any {
	return map[string]any{
		"type": "doc",
		"content": []any{map[string]any{
			"type": "paragraph",
			"content": []any{map[string]any{
				"type": "text",
				"text": text,
			}},
		}},
	}
}

func noticeDocumentText(value any) string {
	if raw, ok := value.(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			return ""
		}
		return noticeDocumentText(parsed)
	}
	if raw, ok := value.(json.RawMessage); ok {
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return ""
		}
		return noticeDocumentText(parsed)
	}
	if raw, ok := value.(types.JSONRaw); ok {
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			return ""
		}
		return noticeDocumentText(parsed)
	}
	document, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	content, ok := document["content"].([]any)
	if !ok || len(content) == 0 {
		return ""
	}
	paragraph, ok := content[0].(map[string]any)
	if !ok {
		return ""
	}
	paragraphContent, ok := paragraph["content"].([]any)
	if !ok || len(paragraphContent) == 0 {
		return ""
	}
	textNode, ok := paragraphContent[0].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := textNode["text"].(string)
	return text
}

func noticeDocumentJSON(text string) string {
	body, _ := json.Marshal(noticeDocument(text))
	return string(body)
}

func decodeJSONMap(tb testing.TB, res *http.Response) map[string]any {
	tb.Helper()

	defer res.Body.Close()
	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		tb.Fatalf("failed to decode JSON: %v", err)
	}
	return payload
}

func TestArcadeNotice_OwnerCanCreateUpdateDelete(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{
		Name:     "Owned Arcade",
		Address:  "1 Owner St",
		Location: location{Lat: 37.5, Lon: 127.0},
	})

	token, user := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	addOwnedArcade(t, app, user, arcadeID)

	headers := map[string]string{"Authorization": "Bearer " + token}

	createResp := executeJSONRequest(t, app, http.MethodPost, "/arcade/notice", string(createNoticeBody(arcadeID)), headers)
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("expected create to succeed, got %d", createResp.StatusCode)
	}

	createPayload := decodeJSONMap(t, createResp)
	noticeID, _ := createPayload["id"].(string)
	if noticeID == "" {
		t.Fatalf("expected created notice id")
	}
	if got := createPayload["createdBy"]; got != user.Id {
		t.Fatalf("expected createdBy %q, got %v", user.Id, got)
	}

	updateBody, _ := json.Marshal(map[string]any{
		"id":       noticeID,
		"document": noticeDocument("**updated**"),
		"priority": 5,
	})
	updateResp := executeJSONRequest(t, app, http.MethodPut, "/arcade/notice", string(updateBody), headers)
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("expected update to succeed, got %d", updateResp.StatusCode)
	}

	deleteBody, _ := json.Marshal(map[string]any{"id": noticeID})
	deleteResp := executeJSONRequest(t, app, http.MethodDelete, "/arcade/notice", string(deleteBody), headers)
	if deleteResp.StatusCode != http.StatusOK {
		t.Fatalf("expected delete to succeed, got %d", deleteResp.StatusCode)
	}

	deletePayload := decodeJSONMap(t, deleteResp)
	if got := deletePayload["delete"]; got != true {
		t.Fatalf("expected delete flag true, got %v", got)
	}

	rec, err := app.FindRecordById("arcade_notice", noticeID)
	if err != nil {
		t.Fatalf("expected notice record to remain after soft delete: %v", err)
	}
	if !rec.GetBool("delete") {
		t.Fatalf("expected notice record delete flag to be true")
	}
}

func TestArcadeNotice_SupporterCreateAndAuthorOnlyMutation(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{
		Name:     "Community Arcade",
		Address:  "6 Supporter St",
		Location: location{Lat: 37.91, Lon: 127.41},
	})

	token, user := createAuthUserWithTags(t, app, []string{"supporter"})
	setUserLevel(t, app, user.Id, 30)
	headers := map[string]string{"Authorization": "Bearer " + token}
	createResp := executeJSONRequest(t, app, http.MethodPost, "/arcade/notice", string(createNoticeBody(arcadeID)), headers)
	if createResp.StatusCode != http.StatusOK {
		t.Fatalf("expected supporter create to succeed, got %d", createResp.StatusCode)
	}
	noticeID, _ := decodeJSONMap(t, createResp)["id"].(string)
	if noticeID == "" {
		t.Fatal("expected supporter notice id")
	}

	otherToken, otherUser := createAuthUserWithTags(t, app, []string{"supporter"})
	setUserLevel(t, app, otherUser.Id, 30)
	otherHeaders := map[string]string{"Authorization": "Bearer " + otherToken}
	updateBody, _ := json.Marshal(map[string]any{"id": noticeID, "document": noticeDocument("**blocked**")})
	if res := executeJSONRequest(t, app, http.MethodPut, "/arcade/notice", string(updateBody), otherHeaders); res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected other supporter update to be forbidden, got %d", res.StatusCode)
	} else {
		res.Body.Close()
	}
	deleteBody, _ := json.Marshal(map[string]any{"id": noticeID})
	if res := executeJSONRequest(t, app, http.MethodDelete, "/arcade/notice", string(deleteBody), otherHeaders); res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected other supporter delete to be forbidden, got %d", res.StatusCode)
	} else {
		res.Body.Close()
	}

	if res := executeJSONRequest(t, app, http.MethodPut, "/arcade/notice", string(updateBody), headers); res.StatusCode != http.StatusOK {
		t.Fatalf("expected author update to succeed, got %d", res.StatusCode)
	} else {
		res.Body.Close()
	}
	if res := executeJSONRequest(t, app, http.MethodDelete, "/arcade/notice", string(deleteBody), headers); res.StatusCode != http.StatusOK {
		t.Fatalf("expected author delete to succeed, got %d", res.StatusCode)
	} else {
		res.Body.Close()
	}
}

func TestArcadeNotice_SupporterRequiresLevel30ForMutations(t *testing.T) {
	for _, role := range []string{"supporter", "founding_supporter"} {
		t.Run(role, func(t *testing.T) {
			app := newArcadeTestApp(t)
			arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{
				Name:     "Level-Gated Arcade",
				Address:  "6 Level St",
				Location: location{Lat: 37.91, Lon: 127.41},
			})

			token, user := createAuthUserWithTags(t, app, []string{role})
			headers := map[string]string{"Authorization": "Bearer " + token}

			setUserLevel(t, app, user.Id, 29)
			res := executeJSONRequest(t, app, http.MethodPost, "/arcade/notice", string(createNoticeBody(arcadeID)), headers)
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("expected level 29 %s to receive 403, got %d", role, res.StatusCode)
			}
			res.Body.Close()

			setUserLevel(t, app, user.Id, 30)
			res = executeJSONRequest(t, app, http.MethodPost, "/arcade/notice", string(createNoticeBody(arcadeID)), headers)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("expected level 30 %s to create notice, got %d", role, res.StatusCode)
			}
			noticeID, _ := decodeJSONMap(t, res)["id"].(string)

			setUserLevel(t, app, user.Id, 29)
			updateBody, _ := json.Marshal(map[string]any{"id": noticeID, "document": noticeDocument("**blocked**")})
			res = executeJSONRequest(t, app, http.MethodPut, "/arcade/notice", string(updateBody), headers)
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("expected level 29 %s update to receive 403, got %d", role, res.StatusCode)
			}
			res.Body.Close()

			deleteBody, _ := json.Marshal(map[string]any{"id": noticeID})
			res = executeJSONRequest(t, app, http.MethodDelete, "/arcade/notice", string(deleteBody), headers)
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("expected level 29 %s delete to receive 403, got %d", role, res.StatusCode)
			}
			res.Body.Close()
		})
	}
}

func TestArcadeNotice_SupporterCannotCreateForOfficialManagedArcade(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{
		Name:     "Official Arcade",
		Address:  "7 Official St",
		Location: location{Lat: 37.92, Lon: 127.42},
	})

	_, official := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	addOwnedArcade(t, app, official, arcadeID)
	token, _ := createAuthUserWithTags(t, app, []string{"founding_supporter"})
	res := executeJSONRequest(t, app, http.MethodPost, "/arcade/notice", string(createNoticeBody(arcadeID)), map[string]string{"Authorization": "Bearer " + token})
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("expected supporter create on official arcade to be forbidden, got %d", res.StatusCode)
	}
	res.Body.Close()
}

func TestArcadeNotice_ModeratorCanMutateAnotherAuthorNotice(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{
		Name:     "Moderated Arcade",
		Address:  "8 Moderator St",
		Location: location{Lat: 37.93, Lon: 127.43},
	})
	token, user := createAuthUserWithTags(t, app, []string{"supporter"})
	setUserLevel(t, app, user.Id, 30)
	headers := map[string]string{"Authorization": "Bearer " + token}
	created := executeJSONRequest(t, app, http.MethodPost, "/arcade/notice", string(createNoticeBody(arcadeID)), headers)
	if created.StatusCode != http.StatusOK {
		t.Fatalf("expected supporter create to succeed, got %d", created.StatusCode)
	}
	noticeID, _ := decodeJSONMap(t, created)["id"].(string)

	moderatorToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	moderatorHeaders := map[string]string{"Authorization": "Bearer " + moderatorToken}
	updateBody, _ := json.Marshal(map[string]any{"id": noticeID, "document": noticeDocument("**moderated**")})
	if res := executeJSONRequest(t, app, http.MethodPut, "/arcade/notice", string(updateBody), moderatorHeaders); res.StatusCode != http.StatusOK {
		t.Fatalf("expected moderator update to succeed, got %d", res.StatusCode)
	} else {
		res.Body.Close()
	}
	developerToken, _ := createAuthUserWithTags(t, app, []string{"developer"})
	deleteBody, _ := json.Marshal(map[string]any{"id": noticeID})
	if res := executeJSONRequest(t, app, http.MethodDelete, "/arcade/notice", string(deleteBody), map[string]string{"Authorization": "Bearer " + developerToken}); res.StatusCode != http.StatusOK {
		t.Fatalf("expected developer delete to succeed, got %d", res.StatusCode)
	} else {
		res.Body.Close()
	}
}

func TestArcadeNotice_ArcadeOwnerRequiresOwnership(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{
		Name:     "Unowned Arcade",
		Address:  "2 Owner St",
		Location: location{Lat: 37.6, Lon: 127.1},
	})

	token, _ := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	headers := map[string]string{"Authorization": "Bearer " + token}

	createResp := executeJSONRequest(t, app, http.MethodPost, "/arcade/notice", string(createNoticeBody(arcadeID)), headers)
	if createResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected create to be forbidden, got %d", createResp.StatusCode)
	}
	createPayload := decodeJSONMap(t, createResp)
	if got := createPayload["error"]; got != "notice creation is not allowed for this arcade" {
		t.Fatalf("unexpected create error: %v", got)
	}

	seededNoticeID := seedNotice(t, app, arcadeID)

	updateBody, _ := json.Marshal(map[string]any{
		"id":       seededNoticeID,
		"document": noticeDocument("**blocked**"),
	})
	updateResp := executeJSONRequest(t, app, http.MethodPut, "/arcade/notice", string(updateBody), headers)
	if updateResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected update to be forbidden, got %d", updateResp.StatusCode)
	}

	deleteBody, _ := json.Marshal(map[string]any{"id": seededNoticeID})
	deleteResp := executeJSONRequest(t, app, http.MethodDelete, "/arcade/notice", string(deleteBody), headers)
	if deleteResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected delete to be forbidden, got %d", deleteResp.StatusCode)
	}
}

func TestArcadeNotice_ModeratorAndDeveloperBypassOwnership(t *testing.T) {
	cases := []struct {
		name string
		tags []string
	}{
		{name: "moderator", tags: []string{"moderator"}},
		{name: "developer", tags: []string{"developer"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app := newArcadeTestApp(t)
			arcadeID, _ := seedArcade(t, app, "", arcadeSeed{
				Name:     "Shared Arcade",
				Address:  "3 Owner St",
				Location: location{Lat: 37.7, Lon: 127.2},
			})

			token, _ := createAuthUserWithTags(t, app, tc.tags)
			headers := map[string]string{"Authorization": "Bearer " + token}

			createResp := executeJSONRequest(t, app, http.MethodPost, "/arcade/notice", string(createNoticeBody(arcadeID)), headers)
			if createResp.StatusCode != http.StatusOK {
				t.Fatalf("expected create to succeed, got %d", createResp.StatusCode)
			}
		})
	}
}

func TestArcadeNotice_ListFiltersAndSorts(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{
		Name:     "Listed Arcade",
		Address:  "4 Owner St",
		Location: location{Lat: 37.8, Lon: 127.3},
	})

	baseTime := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	seedNoticeWithPriority(t, app, arcadeID, 0, "**zero**", baseTime)
	seedNoticeWithPriority(t, app, arcadeID, 2, "*older high*", baseTime.Add(1*time.Minute))
	seedNoticeWithPriority(t, app, arcadeID, 2, "*newer high*", baseTime.Add(2*time.Minute))
	seedNoticeWithPriority(t, app, arcadeID, 1, "**mid**", baseTime.Add(3*time.Minute))
	deletedNoticeID := seedNoticeWithPriority(t, app, arcadeID, 1, "**deleted**", baseTime.Add(4*time.Minute))
	deletedRec, err := app.FindRecordById("arcade_notice", deletedNoticeID)
	if err != nil {
		t.Fatalf("failed to load deleted notice: %v", err)
	}
	deletedRec.Set("delete", true)
	if err := app.Save(deletedRec); err != nil {
		t.Fatalf("failed to soft delete notice: %v", err)
	}

	res := executeJSONRequest(t, app, http.MethodGet, "/arcade/notice?arcade="+arcadeID, "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected list to succeed, got %d", res.StatusCode)
	}

	payload := decodeJSONMap(t, res)
	items, ok := payload["items"].([]any)
	if !ok {
		t.Fatalf("expected items array, got %T", payload["items"])
	}
	if len(items) != 4 {
		t.Fatalf("expected 4 notices including priority 0 and excluding deleted, got %d", len(items))
	}

	first := items[0].(map[string]any)
	second := items[1].(map[string]any)
	third := items[2].(map[string]any)
	fourth := items[3].(map[string]any)

	if got, ok := first["priority"].(float64); !ok || got != 1 {
		t.Fatalf("expected first priority 1, got %v", got)
	}
	if noticeDocumentText(first["document"]) != "**mid**" {
		t.Fatalf("expected priority 1 notice first, got %v", first["document"])
	}
	if got, ok := second["priority"].(float64); !ok || got != 2 {
		t.Fatalf("expected second priority 2, got %v", got)
	}
	if got, ok := third["priority"].(float64); !ok || got != 2 {
		t.Fatalf("expected third priority 2, got %v", got)
	}
	if got, ok := fourth["priority"].(float64); !ok || got != 0 {
		t.Fatalf("expected fourth priority 0, got %v", got)
	}
	if noticeDocumentText(second["document"]) != "*newer high*" {
		t.Fatalf("expected newer same-priority notice second, got %v", second["document"])
	}
	if noticeDocumentText(third["document"]) != "*older high*" {
		t.Fatalf("expected older same-priority notice third, got %v", third["document"])
	}
	if noticeDocumentText(fourth["document"]) != "**zero**" {
		t.Fatalf("expected priority 0 notice last, got %v", fourth["document"])
	}
}

func TestArcadeNotice_ListExpiresPastNotices(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedPublicArcade(t, app, "", arcadeSeed{
		Name:     "Expiry Arcade",
		Address:  "5 Expiry St",
		Location: location{Lat: 37.9, Lon: 127.4},
	})

	expiredID := seedNotice(t, app, arcadeID)
	expiredRec, err := app.FindRecordById("arcade_notice", expiredID)
	if err != nil {
		t.Fatalf("failed to load expired notice: %v", err)
	}
	expiredRec.Set("until", time.Now().UTC().Add(-time.Minute))
	if err := app.Save(expiredRec); err != nil {
		t.Fatalf("failed to set notice expiry: %v", err)
	}

	res := executeJSONRequest(t, app, http.MethodGet, "/arcade/notice?arcade="+arcadeID, "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected list to succeed, got %d", res.StatusCode)
	}

	payload := decodeJSONMap(t, res)
	items, ok := payload["items"].([]any)
	if !ok || len(items) != 0 {
		t.Fatalf("expected expired notice to be excluded, got %#v", payload["items"])
	}

	expiredRec, err = app.FindRecordById("arcade_notice", expiredID)
	if err != nil {
		t.Fatalf("failed to reload expired notice: %v", err)
	}
	if !expiredRec.GetBool("delete") {
		t.Fatal("expected expired notice to be soft-deleted")
	}
}

func TestArcadeNotice_MultipartDocumentRoundTripsAndStoresPhotos(t *testing.T) {
	app := newArcadeTestApp(t)
	arcadeID, _ := seedArcade(t, app, "", arcadeSeed{
		Name:     "Multipart Arcade",
		Address:  "5 Owner St",
		Location: location{Lat: 37.9, Lon: 127.4},
	})

	token, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	headers := map[string]string{"Authorization": "Bearer " + token}

	documentText := "first line\n**second line**"
	body, contentType := buildNoticeMultipart(t, arcadeID, documentText, []uploadTestFile{
		{Filename: "notice-a.png", Content: pngFixtureBytes()},
		{Filename: "notice-b.jpg", Content: jpegFixtureBytes()},
	})
	headers["Content-Type"] = contentType

	res := executeJSONRequest(t, app, http.MethodPost, "/arcade/notice", string(body), headers)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected multipart create to succeed, got %d", res.StatusCode)
	}

	payload := decodeJSONMap(t, res)
	noticeID, _ := payload["id"].(string)
	if noticeID == "" {
		t.Fatalf("expected notice id in response")
	}

	if got := noticeDocumentText(payload["document"]); got != documentText {
		t.Fatalf("expected document to round-trip unchanged, got %v", payload["document"])
	}
	photos, ok := payload["photos"].([]any)
	if !ok || len(photos) != 2 {
		t.Fatalf("expected two photos in response, got %T %#v", payload["photos"], payload["photos"])
	}

	rec, err := app.FindRecordById("arcade_notice", noticeID)
	if err != nil {
		t.Fatalf("failed to load arcade_notice: %v", err)
	}
	if rec.Collection().Fields.GetByName("message") != nil {
		t.Fatal("expected arcade_notice.message to be removed by migration")
	}
	if got := noticeDocumentText(rec.Get("document")); got != documentText {
		t.Fatalf("expected stored document, got %q (%T %#v)", got, rec.Get("document"), rec.Get("document"))
	}
	if got := rec.GetStringSlice("photos"); len(got) != 2 {
		t.Fatalf("expected 2 stored photos, got %#v", got)
	}
}

func seedNotice(tb testing.TB, app *tests.TestApp, arcadeID string) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_notice")
	if err != nil {
		tb.Fatalf("failed to load arcade_notice collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("type", "alert")
	rec.Set("document", noticeDocument("**seeded**"))
	rec.Set("priority", 1)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_notice: %v", err)
	}

	return rec.Id
}

func seedNoticeWithPriority(tb testing.TB, app *tests.TestApp, arcadeID string, priority float64, message string, created time.Time) string {
	tb.Helper()

	coll, err := app.FindCollectionByNameOrId("arcade_notice")
	if err != nil {
		tb.Fatalf("failed to load arcade_notice collection: %v", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("arcade", arcadeID)
	rec.Set("type", "alert")
	rec.Set("document", noticeDocument(message))
	rec.Set("priority", priority)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade_notice: %v", err)
	}

	when := created.UTC().Format(types.DefaultDateLayout)
	if _, err := app.NonconcurrentDB().
		NewQuery("UPDATE arcade_notice SET created={:created} WHERE id={:id}").
		Bind(dbx.Params{"created": when, "id": rec.Id}).
		Execute(); err != nil {
		tb.Fatalf("failed to update arcade_notice.created for %s: %v", rec.Id, err)
	}

	return rec.Id
}

func buildNoticeMultipart(tb testing.TB, arcadeID, message string, files []uploadTestFile) ([]byte, string) {
	tb.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	if err := writer.WriteField("arcade", arcadeID); err != nil {
		tb.Fatalf("failed to write arcade field: %v", err)
	}
	if err := writer.WriteField("document", noticeDocumentJSON(message)); err != nil {
		tb.Fatalf("failed to write document field: %v", err)
	}
	if err := writer.WriteField("type", "alert"); err != nil {
		tb.Fatalf("failed to write type field: %v", err)
	}
	if err := writer.WriteField("priority", "2"); err != nil {
		tb.Fatalf("failed to write priority field: %v", err)
	}

	for _, file := range files {
		part, err := writer.CreateFormFile("photos", file.Filename)
		if err != nil {
			tb.Fatalf("failed to create multipart file part: %v", err)
		}
		if _, err := part.Write(file.Content); err != nil {
			tb.Fatalf("failed to write multipart file content: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		tb.Fatalf("failed to close multipart writer: %v", err)
	}

	return buf.Bytes(), writer.FormDataContentType()
}
