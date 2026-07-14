package arcade_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

type uploadTestFile struct {
	Filename string
	Content  []byte
}

type photoUploadResponse struct {
	Arcade  string `json:"arcade"`
	Summary struct {
		Total   int `json:"total"`
		Success int `json:"success"`
		Failed  int `json:"failed"`
	} `json:"summary"`
	Uploaded []struct {
		Index    int    `json:"index"`
		AtomID   string `json:"atomId"`
		Filename string `json:"filename"`
	} `json:"uploaded"`
	Failed []struct {
		Index    int    `json:"index"`
		Filename string `json:"filename"`
		Reason   string `json:"reason"`
	} `json:"failed"`
	XPFeedback struct {
		DiffExp int `json:"diff_exp"`
	} `json:"xp_feedback"`
}

func TestUploadArcadePhotos_Success(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var beforeCount int

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/photo/upload success",
		Method:         http.MethodPost,
		URL:            "/arcade/photo/upload",
		Headers:        headers,
		ExpectedStatus: http.StatusOK,
		ExpectedContent: []string{
			`"summary":{"total":2,"success":2,"failed":0}`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp { return newArcadeTestApp(tb) },
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Upload Arcade",
			Address:  "Upload Street",
			Nickname: []string{"Upload"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		beforeCount = countPhotoAtomsForArcade(tb, app, arcadeID)

		body, contentType := buildPhotoUploadMultipart(tb, arcadeID, []uploadTestFile{
			{Filename: "a.png", Content: pngFixtureBytes()},
			{Filename: "b.jpg", Content: jpegFixtureBytes()},
		})
		headers["Content-Type"] = contentType
		scenario.Body = bytes.NewReader(body)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload photoUploadResponse
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}

		if payload.Arcade != arcadeID {
			tb.Fatalf("expected arcade %q, got %q", arcadeID, payload.Arcade)
		}
		if payload.Summary.Total != 2 || payload.Summary.Success != 2 || payload.Summary.Failed != 0 {
			tb.Fatalf("unexpected summary: %+v", payload.Summary)
		}
		if len(payload.Uploaded) != 2 || len(payload.Failed) != 0 {
			tb.Fatalf("unexpected uploaded/failed size: uploaded=%d failed=%d", len(payload.Uploaded), len(payload.Failed))
		}
		for i, item := range payload.Uploaded {
			if item.AtomID == "" {
				tb.Fatalf("uploaded[%d].atomId is empty", i)
			}
		}
		if payload.XPFeedback.DiffExp != 5 {
			tb.Fatalf("expected xp diff 5, got %d", payload.XPFeedback.DiffExp)
		}

		afterCount := countPhotoAtomsForArcade(tb, app, arcadeID)
		if diff := afterCount - beforeCount; diff != 2 {
			tb.Fatalf("expected 2 uploaded atoms, got diff=%d", diff)
		}
	}

	scenario.Test(t)
}

func TestUploadArcadePhotos_PartialSuccess(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var beforeCount int

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/photo/upload partial success",
		Method:         http.MethodPost,
		URL:            "/arcade/photo/upload",
		Headers:        headers,
		ExpectedStatus: http.StatusMultiStatus,
		ExpectedContent: []string{
			`"summary":{"total":2,"success":1,"failed":1}`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp { return newArcadeTestApp(tb) },
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Upload Partial Arcade",
			Address:  "Upload Street",
			Nickname: []string{"Upload"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		beforeCount = countPhotoAtomsForArcade(tb, app, arcadeID)

		body, contentType := buildPhotoUploadMultipart(tb, arcadeID, []uploadTestFile{
			{Filename: "ok.png", Content: pngFixtureBytes()},
			{Filename: "bad.gif", Content: gifFixtureBytes()},
		})
		headers["Content-Type"] = contentType
		scenario.Body = bytes.NewReader(body)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload photoUploadResponse
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if payload.Summary.Total != 2 || payload.Summary.Success != 1 || payload.Summary.Failed != 1 {
			tb.Fatalf("unexpected summary: %+v", payload.Summary)
		}
		if len(payload.Uploaded) != 1 || len(payload.Failed) != 1 {
			tb.Fatalf("unexpected uploaded/failed size: uploaded=%d failed=%d", len(payload.Uploaded), len(payload.Failed))
		}
		if payload.Failed[0].Reason == "" {
			tb.Fatalf("expected failed reason")
		}
		if payload.XPFeedback.DiffExp != 5 {
			tb.Fatalf("expected xp diff 5, got %d", payload.XPFeedback.DiffExp)
		}

		afterCount := countPhotoAtomsForArcade(tb, app, arcadeID)
		if diff := afterCount - beforeCount; diff != 1 {
			tb.Fatalf("expected 1 uploaded atom, got diff=%d", diff)
		}
	}

	scenario.Test(t)
}

func TestUploadArcadePhotos_AllFailed(t *testing.T) {
	headers := map[string]string{}
	var arcadeID string
	var beforeCount int

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/photo/upload all failed",
		Method:         http.MethodPost,
		URL:            "/arcade/photo/upload",
		Headers:        headers,
		ExpectedStatus: http.StatusUnprocessableEntity,
		ExpectedContent: []string{
			`"summary":{"total":2,"success":0,"failed":2}`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp { return newArcadeTestApp(tb) },
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, user := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		arcadeID, _ = seedArcade(tb, app, user.Id, arcadeSeed{
			Name:     "Upload Fail Arcade",
			Address:  "Upload Street",
			Nickname: []string{"Upload"},
			Location: location{Lat: 37.5665, Lon: 126.978},
		})
		beforeCount = countPhotoAtomsForArcade(tb, app, arcadeID)

		body, contentType := buildPhotoUploadMultipart(tb, arcadeID, []uploadTestFile{
			{Filename: "bad1.gif", Content: gifFixtureBytes()},
			{Filename: "bad2.gif", Content: gifFixtureBytes()},
		})
		headers["Content-Type"] = contentType
		scenario.Body = bytes.NewReader(body)
	}

	scenario.AfterTestFunc = func(tb testing.TB, app *tests.TestApp, res *http.Response) {
		tb.Helper()
		defer res.Body.Close()

		var payload photoUploadResponse
		if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
			tb.Fatalf("failed to decode response: %v", err)
		}
		if payload.Summary.Total != 2 || payload.Summary.Success != 0 || payload.Summary.Failed != 2 {
			tb.Fatalf("unexpected summary: %+v", payload.Summary)
		}
		if len(payload.Uploaded) != 0 || len(payload.Failed) != 2 {
			tb.Fatalf("unexpected uploaded/failed size: uploaded=%d failed=%d", len(payload.Uploaded), len(payload.Failed))
		}
		if payload.XPFeedback.DiffExp != 0 {
			tb.Fatalf("expected xp diff 0, got %d", payload.XPFeedback.DiffExp)
		}

		afterCount := countPhotoAtomsForArcade(tb, app, arcadeID)
		if diff := afterCount - beforeCount; diff != 0 {
			tb.Fatalf("expected 0 uploaded atoms, got diff=%d", diff)
		}
	}

	scenario.Test(t)
}

func TestUploadArcadePhotos_ValidationErrors(t *testing.T) {
	t.Run("missing photos", func(t *testing.T) {
		headers := map[string]string{}
		scenario := tests.ApiScenario{
			Name:           "POST /arcade/photo/upload missing photos",
			Method:         http.MethodPost,
			URL:            "/arcade/photo/upload",
			Headers:        headers,
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"error":"validation failed"`,
				`"details":"photos must have at least one item"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp { return newArcadeTestApp(tb) },
		}

		scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			tb.Helper()
			token, user := createAuthUser(tb, app)
			headers["Authorization"] = "Bearer " + token
			arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
				Name:     "Upload Validation Arcade",
				Address:  "Upload Street",
				Nickname: []string{"Upload"},
				Location: location{Lat: 37.5665, Lon: 126.978},
			})

			body, contentType := buildPhotoUploadMultipart(tb, arcadeID, nil)
			headers["Content-Type"] = contentType
			scenario.Body = bytes.NewReader(body)
		}

		scenario.Test(t)
	})

	t.Run("too many photos", func(t *testing.T) {
		headers := map[string]string{}
		scenario := tests.ApiScenario{
			Name:           "POST /arcade/photo/upload too many photos",
			Method:         http.MethodPost,
			URL:            "/arcade/photo/upload",
			Headers:        headers,
			ExpectedStatus: http.StatusBadRequest,
			ExpectedContent: []string{
				`"error":"validation failed"`,
				`"details":"photos must have at most 10 items"`,
			},
			TestAppFactory: func(tb testing.TB) *tests.TestApp { return newArcadeTestApp(tb) },
		}

		scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
			tb.Helper()
			token, user := createAuthUser(tb, app)
			headers["Authorization"] = "Bearer " + token
			arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{
				Name:     "Upload Validation Arcade",
				Address:  "Upload Street",
				Nickname: []string{"Upload"},
				Location: location{Lat: 37.5665, Lon: 126.978},
			})

			files := make([]uploadTestFile, 0, 11)
			for i := 0; i < 11; i++ {
				files = append(files, uploadTestFile{
					Filename: fmt.Sprintf("n%d.png", i),
					Content:  pngFixtureBytes(),
				})
			}
			body, contentType := buildPhotoUploadMultipart(tb, arcadeID, files)
			headers["Content-Type"] = contentType
			scenario.Body = bytes.NewReader(body)
		}

		scenario.Test(t)
	})
}

func TestUploadArcadePhotos_ArcadeNotFound(t *testing.T) {
	headers := map[string]string{}

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/photo/upload arcade not found",
		Method:         http.MethodPost,
		URL:            "/arcade/photo/upload",
		Headers:        headers,
		ExpectedStatus: http.StatusNotFound,
		ExpectedContent: []string{
			`"error":"arcade not found"`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp { return newArcadeTestApp(tb) },
	}

	scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
		tb.Helper()

		token, _ := createAuthUser(tb, app)
		headers["Authorization"] = "Bearer " + token

		body, contentType := buildPhotoUploadMultipart(tb, "does-not-exist", []uploadTestFile{
			{Filename: "x.png", Content: pngFixtureBytes()},
		})
		headers["Content-Type"] = contentType
		scenario.Body = bytes.NewReader(body)
	}

	scenario.Test(t)
}

func TestUploadArcadePhotos_Unauthorized(t *testing.T) {
	body, contentType := buildPhotoUploadMultipart(t, "irrelevant", []uploadTestFile{
		{Filename: "x.png", Content: pngFixtureBytes()},
	})

	scenario := tests.ApiScenario{
		Name:           "POST /arcade/photo/upload unauthorized",
		Method:         http.MethodPost,
		URL:            "/arcade/photo/upload",
		Body:           bytes.NewReader(body),
		Headers:        map[string]string{"Content-Type": contentType},
		ExpectedStatus: http.StatusUnauthorized,
		ExpectedContent: []string{
			`"status":401`,
		},
		TestAppFactory: func(tb testing.TB) *tests.TestApp { return newArcadeTestApp(tb) },
	}

	scenario.Test(t)
}

func buildPhotoUploadMultipart(tb testing.TB, arcadeID string, files []uploadTestFile) ([]byte, string) {
	tb.Helper()

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	if err := writer.WriteField("arcade", arcadeID); err != nil {
		tb.Fatalf("failed to write arcade field: %v", err)
	}
	for _, f := range files {
		part, err := writer.CreateFormFile("photos", f.Filename)
		if err != nil {
			tb.Fatalf("failed to create form file: %v", err)
		}
		if _, err := part.Write(f.Content); err != nil {
			tb.Fatalf("failed to write form file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		tb.Fatalf("failed to close multipart writer: %v", err)
	}

	return buf.Bytes(), writer.FormDataContentType()
}

func countPhotoAtomsForArcade(tb testing.TB, app *tests.TestApp, arcadeID string) int {
	tb.Helper()

	recs, err := app.FindRecordsByFilter("arcade_photo_atoms", "arcade={:id}", "", 0, 0, dbx.Params{"id": arcadeID})
	if err != nil {
		tb.Fatalf("failed to count photo atoms: %v", err)
	}
	return len(recs)
}

func pngFixtureBytes() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01,
	}
}

func jpegFixtureBytes() []byte {
	return []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46,
		0x49, 0x46, 0x00, 0x01, 0x01, 0x01,
	}
}

func gifFixtureBytes() []byte {
	return []byte{
		0x47, 0x49, 0x46, 0x38, 0x39, 0x61,
		0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00,
	}
}
