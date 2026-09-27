package coreapp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	arcadeadmin "github.com/ericbaek/musecat-backend-core/handlers/arcade/admin"
	"github.com/ericbaek/musecat-backend-core/testutil"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

func TestSupportFeedbackUploadContract(t *testing.T) {
	for _, tc := range []struct {
		name                string
		count, size, status int
		image               bool
	}{
		{"three maximum size photos", 3, 15_000_000, 200, true},
		{"four photos", 4, 100, 400, true},
		{"oversized file", 1, 15_000_001, 413, true},
		{"non image disguised as jpeg", 1, 100, 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := testutil.NewTestApp(t)
			router, err := apis.NewRouter(app)
			if err != nil {
				t.Fatal(err)
			}
			RegisterAPIRoutes(&core.ServeEvent{App: app, Router: router})
			mux, err := router.BuildMux()
			if err != nil {
				t.Fatal(err)
			}
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			if err := form.WriteField("message", "upload contract"); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tc.count; i++ {
				part, err := form.CreateFormFile("photos", fmt.Sprintf("photo%d.jpg", i))
				if err != nil {
					t.Fatal(err)
				}
				data := bytes.Repeat([]byte{0}, tc.size)
				if tc.image {
					copy(data, []byte("GIF89a\x01\x00\x01\x00"))
				}
				if _, err := part.Write(data); err != nil {
					t.Fatal(err)
				}
			}
			if err := form.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest("POST", "/support_feedback", &body)
			req.Header.Set("Content-Type", form.FormDataContentType())
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			if response.Code != tc.status {
				t.Fatalf("got %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if tc.status == 200 {
				var value map[string]any
				if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
					t.Fatal(err)
				}
				if value["created"] == nil || value["created"] == "" || value["updated"] == nil || value["updated"] == "" {
					t.Fatal("missing creation timestamps required by frontend")
				}
			}
		})
	}
}

func TestSupportFeedbackBodyLimit(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		t.Run(fmt.Sprintf("chunked=%v", chunked), func(t *testing.T) {
			app := testutil.NewTestApp(t)
			router, err := apis.NewRouter(app)
			if err != nil {
				t.Fatal(err)
			}
			RegisterAPIRoutes(&core.ServeEvent{App: app, Router: router})
			mux, err := router.BuildMux()
			if err != nil {
				t.Fatal(err)
			}
			// A long JSON string forces the decoder to read beyond the body ceiling.
			body := io.MultiReader(bytes.NewBufferString(`{"message":"`), io.LimitReader(zeroReader{}, arcadeadmin.MaxSupportFeedbackBodyBytes+1))
			req := httptest.NewRequest(http.MethodPost, "/support_feedback", body)
			req.Header.Set("Content-Type", "application/json")
			if !chunked {
				req.ContentLength = arcadeadmin.MaxSupportFeedbackBodyBytes + 20
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, req)
			if response.Code != 413 {
				t.Fatalf("got %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}
