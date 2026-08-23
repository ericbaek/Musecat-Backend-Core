package subway_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"

	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	"github.com/ericbaek/musecat-backend-core/handlers/subway"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/ericbaek/musecat-backend-core/testutil"
)

type subwayMapFileResponse struct {
	Name    string `json:"name"`
	FileURL string `json:"file_url"`
}

type subwayMapResponse struct {
	ID          string                 `json:"id"`
	Region      string                 `json:"region"`
	ReleaseDate string                 `json:"release_date"`
	Changelog   []string               `json:"changelog"`
	LightSVG    *subwayMapFileResponse `json:"light_svg"`
	DarkSVG     *subwayMapFileResponse `json:"dark_svg"`
	Image       *subwayMapFileResponse `json:"image"`
	Document    *subwayMapFileResponse `json:"document"`
	Created     string                 `json:"created"`
	Updated     string                 `json:"updated"`
}

type multipartField struct {
	Name  string
	Value string
}

type multipartFile struct {
	Field    string
	Filename string
	Content  []byte
}

func TestSubwayMapPublicReadAndFile(t *testing.T) {
	app := newSubwayTestApp(t)
	defer app.Cleanup()

	seedSubwayMap(t, app, "center", "2025-01-01", "lightSVG", "old.svg", svgFixture("old"), []string{"old"})
	latest := seedSubwayMap(t, app, "center", "2026-08-24", "lightSVG", "latest.svg", svgFixture("latest"), []string{"new", "accessible"})

	response := subwayRequest(t, app, http.MethodGet, "/subway/map?region=center", nil, "", "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("expected public map read 200, got %d: %s", response.StatusCode, readResponse(t, response))
	}
	payload := decodeSubwayMapResponse(t, response)
	if payload.ID != latest.Id || payload.Region != "center" || payload.ReleaseDate != "2026-08-24" {
		t.Fatalf("unexpected latest map payload: %+v", payload)
	}
	if len(payload.Changelog) != 2 || payload.Changelog[0] != "new" {
		t.Fatalf("unexpected changelog: %#v", payload.Changelog)
	}
	if payload.LightSVG == nil || payload.LightSVG.FileURL == "" || payload.DarkSVG != nil || payload.Image != nil || payload.Document != nil {
		t.Fatalf("unexpected file payloads: %+v", payload)
	}
	if payload.Created == "" || payload.Updated == "" {
		t.Fatalf("expected created and updated timestamps: %+v", payload)
	}

	fileResponse := subwayRequest(t, app, http.MethodGet, payload.LightSVG.FileURL, nil, "", "")
	if fileResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected public file read 200, got %d: %s", fileResponse.StatusCode, readResponse(t, fileResponse))
	}
	fileBody, err := io.ReadAll(fileResponse.Body)
	fileResponse.Body.Close()
	if err != nil {
		t.Fatalf("read map file: %v", err)
	}
	if !bytes.Equal(fileBody, svgFixture("latest")) {
		t.Fatalf("unexpected map file body: %q", fileBody)
	}

	invalidField := subwayRequest(t, app, http.MethodGet, "/subway/map/file?id="+latest.Id+"&field=created", nil, "", "")
	if invalidField.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid file field 400, got %d: %s", invalidField.StatusCode, readResponse(t, invalidField))
	}
	invalidField.Body.Close()

	missingRegion := subwayRequest(t, app, http.MethodGet, "/subway/map?region=busan", nil, "", "")
	if missingRegion.StatusCode != http.StatusNotFound {
		t.Fatalf("expected missing map 404, got %d: %s", missingRegion.StatusCode, readResponse(t, missingRegion))
	}
	missingRegion.Body.Close()

	invalidRegion := subwayRequest(t, app, http.MethodGet, "/subway/map?region=seoul", nil, "", "")
	if invalidRegion.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected invalid region 400, got %d: %s", invalidRegion.StatusCode, readResponse(t, invalidRegion))
	}
	invalidRegion.Body.Close()
}

func TestSubwayMapCreateAuthorization(t *testing.T) {
	tests := []struct {
		name       string
		tags       []string
		auth       bool
		wantStatus int
	}{
		{name: "anonymous", auth: false, wantStatus: http.StatusUnauthorized},
		{name: "contributor", auth: true, wantStatus: http.StatusForbidden},
		{name: "supporter", tags: []string{"supporter"}, auth: true, wantStatus: http.StatusForbidden},
		{name: "moderator", tags: []string{"moderator"}, auth: true, wantStatus: http.StatusCreated},
		{name: "developer", tags: []string{"developer"}, auth: true, wantStatus: http.StatusCreated},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := newSubwayTestApp(t)
			defer app.Cleanup()

			token := ""
			if test.auth {
				token, _ = createSubwayAuthUser(t, app, test.tags)
			}
			body, contentType := buildSubwayMultipart(t,
				[]multipartField{{Name: "region", Value: "center"}, {Name: "releaseDate", Value: "2026-08-24"}},
				[]multipartFile{{Field: "lightSVG", Filename: "map.svg", Content: svgFixture("auth")}},
			)
			response := subwayRequest(t, app, http.MethodPost, "/subway/map", bytes.NewReader(body), contentType, token)
			if response.StatusCode != test.wantStatus {
				t.Fatalf("expected %d, got %d: %s", test.wantStatus, response.StatusCode, readResponse(t, response))
			}
			response.Body.Close()
		})
	}
}

func TestSubwayMapCreateUpdateDeleteLifecycle(t *testing.T) {
	app := newSubwayTestApp(t)
	defer app.Cleanup()

	previous := seedSubwayMap(t, app, "center", "2025-01-01", "image", "previous.png", pngFixture(), []string{"previous"})
	token, _ := createSubwayAuthUser(t, app, []string{"moderator"})

	createBody, createType := buildSubwayMultipart(t,
		[]multipartField{
			{Name: "region", Value: "center"},
			{Name: "releaseDate", Value: "2026-08-24"},
			{Name: "changelog", Value: `["initial release"]`},
		},
		[]multipartFile{{Field: "lightSVG", Filename: "light.svg", Content: svgFixture("light")}},
	)
	createResponse := subwayRequest(t, app, http.MethodPost, "/subway/map", bytes.NewReader(createBody), createType, token)
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("expected create 201, got %d: %s", createResponse.StatusCode, readResponse(t, createResponse))
	}
	created := decodeSubwayMapResponse(t, createResponse)
	if created.ID == "" || created.LightSVG == nil || created.DarkSVG != nil || created.ReleaseDate != "2026-08-24" {
		t.Fatalf("unexpected create response: %+v", created)
	}

	updateBody, updateType := buildSubwayMultipart(t,
		[]multipartField{
			{Name: "id", Value: created.ID},
			{Name: "releaseDate", Value: "2026-09-01"},
			{Name: "changelog", Value: `[]`},
		},
		[]multipartFile{{Field: "darkSVG", Filename: "dark.svg", Content: svgFixture("dark")}},
	)
	updateResponse := subwayRequest(t, app, http.MethodPut, "/subway/map", bytes.NewReader(updateBody), updateType, token)
	if updateResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected update 200, got %d: %s", updateResponse.StatusCode, readResponse(t, updateResponse))
	}
	updated := decodeSubwayMapResponse(t, updateResponse)
	if updated.ID != created.ID || updated.Region != "center" || updated.ReleaseDate != "2026-09-01" {
		t.Fatalf("unexpected update metadata: %+v", updated)
	}
	if updated.LightSVG == nil || updated.DarkSVG == nil {
		t.Fatalf("omitted light SVG should be retained while dark SVG is added: %+v", updated)
	}
	if len(updated.Changelog) != 0 {
		t.Fatalf("expected empty replacement changelog, got %#v", updated.Changelog)
	}

	latestResponse := subwayRequest(t, app, http.MethodGet, "/subway/map?region=center", nil, "", "")
	if latestResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected latest read 200, got %d: %s", latestResponse.StatusCode, readResponse(t, latestResponse))
	}
	latest := decodeSubwayMapResponse(t, latestResponse)
	if latest.ID != created.ID {
		t.Fatalf("expected updated map to be latest, got %q", latest.ID)
	}

	deleteResponse := subwayRequest(t, app, http.MethodDelete, "/subway/map?id="+created.ID, nil, "", token)
	if deleteResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected delete 200, got %d: %s", deleteResponse.StatusCode, readResponse(t, deleteResponse))
	}
	var deleted struct {
		ID      string `json:"id"`
		Deleted bool   `json:"deleted"`
	}
	if err := json.NewDecoder(deleteResponse.Body).Decode(&deleted); err != nil {
		deleteResponse.Body.Close()
		t.Fatalf("decode delete response: %v", err)
	}
	deleteResponse.Body.Close()
	if deleted.ID != created.ID || !deleted.Deleted {
		t.Fatalf("unexpected delete response: %+v", deleted)
	}

	fallbackResponse := subwayRequest(t, app, http.MethodGet, "/subway/map?region=center", nil, "", "")
	if fallbackResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected fallback read 200, got %d: %s", fallbackResponse.StatusCode, readResponse(t, fallbackResponse))
	}
	fallback := decodeSubwayMapResponse(t, fallbackResponse)
	if fallback.ID != previous.Id {
		t.Fatalf("expected previous version %q after delete, got %q", previous.Id, fallback.ID)
	}
}

func TestSubwayMapMutationValidation(t *testing.T) {
	tests := []struct {
		name   string
		fields []multipartField
		files  []multipartFile
	}{
		{
			name:   "invalid region",
			fields: []multipartField{{Name: "region", Value: "seoul"}, {Name: "releaseDate", Value: "2026-08-24"}},
			files:  []multipartFile{{Field: "lightSVG", Filename: "map.svg", Content: svgFixture("map")}},
		},
		{
			name:   "invalid release date",
			fields: []multipartField{{Name: "region", Value: "center"}, {Name: "releaseDate", Value: "24-08-2026"}},
			files:  []multipartFile{{Field: "lightSVG", Filename: "map.svg", Content: svgFixture("map")}},
		},
		{
			name:   "no display asset",
			fields: []multipartField{{Name: "region", Value: "center"}, {Name: "releaseDate", Value: "2026-08-24"}},
			files:  []multipartFile{{Field: "file", Filename: "map.pdf", Content: pdfFixture()}},
		},
		{
			name:   "invalid SVG",
			fields: []multipartField{{Name: "region", Value: "center"}, {Name: "releaseDate", Value: "2026-08-24"}},
			files:  []multipartFile{{Field: "lightSVG", Filename: "map.svg", Content: []byte("not an SVG")}},
		},
		{
			name:   "invalid changelog",
			fields: []multipartField{{Name: "region", Value: "center"}, {Name: "releaseDate", Value: "2026-08-24"}, {Name: "changelog", Value: `{}`}},
			files:  []multipartFile{{Field: "lightSVG", Filename: "map.svg", Content: svgFixture("map")}},
		},
		{
			name:   "oversized display asset",
			fields: []multipartField{{Name: "region", Value: "center"}, {Name: "releaseDate", Value: "2026-08-24"}},
			files:  []multipartFile{{Field: "lightSVG", Filename: "map.svg", Content: bytes.Repeat([]byte("x"), (5<<20)+1)}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app := newSubwayTestApp(t)
			defer app.Cleanup()
			token, _ := createSubwayAuthUser(t, app, []string{"developer"})
			body, contentType := buildSubwayMultipart(t, test.fields, test.files)
			response := subwayRequest(t, app, http.MethodPost, "/subway/map", bytes.NewReader(body), contentType, token)
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected validation 400, got %d: %s", response.StatusCode, readResponse(t, response))
			}
			response.Body.Close()
		})
	}
}

func TestSubwayMapUpdateAndDeleteAuthorization(t *testing.T) {
	app := newSubwayTestApp(t)
	defer app.Cleanup()

	record := seedSubwayMap(t, app, "center", "2026-08-24", "lightSVG", "map.svg", svgFixture("map"), nil)
	token, _ := createSubwayAuthUser(t, app, []string{"supporter"})

	updateBody, updateType := buildSubwayMultipart(t, []multipartField{{Name: "id", Value: record.Id}}, nil)
	updateResponse := subwayRequest(t, app, http.MethodPut, "/subway/map", bytes.NewReader(updateBody), updateType, token)
	if updateResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("expected supporter update 403, got %d: %s", updateResponse.StatusCode, readResponse(t, updateResponse))
	}
	updateResponse.Body.Close()

	deleteResponse := subwayRequest(t, app, http.MethodDelete, "/subway/map?id="+record.Id, nil, "", token)
	if deleteResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("expected supporter delete 403, got %d: %s", deleteResponse.StatusCode, readResponse(t, deleteResponse))
	}
	deleteResponse.Body.Close()
}

func newSubwayTestApp(tb testing.TB) *tests.TestApp {
	tb.Helper()
	app := testutil.NewTestApp(tb)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/subway/map", subway.GetMap)
		se.Router.GET("/subway/map/file", subway.DownloadMapFile)
		se.Router.POST("/subway/map", subway.CreateMap).Bind(
			apis.BodyLimit(24<<20),
			apis.RequireAuth("user"),
			userhandler.RequireActiveUser(),
			arcadequery.RequireAdminAccess(),
		)
		se.Router.PUT("/subway/map", subway.UpdateMap).Bind(
			apis.BodyLimit(24<<20),
			apis.RequireAuth("user"),
			userhandler.RequireActiveUser(),
			arcadequery.RequireAdminAccess(),
		)
		se.Router.DELETE("/subway/map", subway.DeleteMap).Bind(
			apis.RequireAuth("user"),
			userhandler.RequireActiveUser(),
			arcadequery.RequireAdminAccess(),
		)
		return se.Next()
	})
	return app
}

func subwayRequest(tb testing.TB, app *tests.TestApp, method, target string, body io.Reader, contentType, token string) *http.Response {
	tb.Helper()
	router, err := apis.NewRouter(app)
	if err != nil {
		tb.Fatalf("create router: %v", err)
	}
	serveEvent := &core.ServeEvent{App: app, Router: router}
	if err := app.OnServe().Trigger(serveEvent, func(event *core.ServeEvent) error { return event.Next() }); err != nil {
		tb.Fatalf("register routes: %v", err)
	}
	mux, err := serveEvent.Router.BuildMux()
	if err != nil {
		tb.Fatalf("build router: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, body)
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	mux.ServeHTTP(recorder, request)
	return recorder.Result()
}

func createSubwayAuthUser(tb testing.TB, app *tests.TestApp, tags []string) (string, *core.Record) {
	tb.Helper()
	collection, err := app.FindCollectionByNameOrId("user")
	if err != nil {
		tb.Fatalf("load user collection: %v", err)
	}
	record := core.NewRecord(collection)
	unique := time.Now().UnixNano()
	record.SetEmail(fmt.Sprintf("subway_%d@example.com", unique))
	record.Set("username", fmt.Sprintf("subway_%d", unique))
	record.Set("tags", tags)
	record.SetPassword("secret123")
	if err := app.Save(record); err != nil {
		tb.Fatalf("create auth user: %v", err)
	}
	token, err := record.NewAuthToken()
	if err != nil {
		tb.Fatalf("create auth token: %v", err)
	}
	return token, record
}

func seedSubwayMap(tb testing.TB, app *tests.TestApp, region, releaseDate, field, filename string, content []byte, changelog []string) *core.Record {
	tb.Helper()
	collection, err := app.FindCollectionByNameOrId("subwayMap")
	if err != nil {
		tb.Fatalf("load subway map collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("region", region)
	parsed, err := time.Parse("2006-01-02", releaseDate)
	if err != nil {
		tb.Fatalf("parse release date: %v", err)
	}
	record.Set("releaseDate", parsed)
	if changelog != nil {
		raw, err := json.Marshal(changelog)
		if err != nil {
			tb.Fatalf("marshal changelog: %v", err)
		}
		record.Set("changelog", types.JSONRaw(raw))
	}
	file, err := filesystem.NewFileFromBytes(content, filename)
	if err != nil {
		tb.Fatalf("create subway file: %v", err)
	}
	record.Set(field, file)
	if err := app.Save(record); err != nil {
		tb.Fatalf("save subway map: %v", err)
	}
	return record
}

func buildSubwayMultipart(tb testing.TB, fields []multipartField, files []multipartFile) ([]byte, string) {
	tb.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range fields {
		if err := writer.WriteField(field.Name, field.Value); err != nil {
			tb.Fatalf("write multipart field: %v", err)
		}
	}
	for _, file := range files {
		part, err := writer.CreateFormFile(file.Field, file.Filename)
		if err != nil {
			tb.Fatalf("create multipart file: %v", err)
		}
		if _, err := part.Write(file.Content); err != nil {
			tb.Fatalf("write multipart file: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		tb.Fatalf("close multipart writer: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func decodeSubwayMapResponse(tb testing.TB, response *http.Response) subwayMapResponse {
	tb.Helper()
	defer response.Body.Close()
	var payload subwayMapResponse
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		tb.Fatalf("decode subway map response: %v", err)
	}
	return payload
}

func readResponse(tb testing.TB, response *http.Response) string {
	tb.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		tb.Fatalf("read response: %v", err)
	}
	return strings.TrimSpace(string(body))
}

func svgFixture(label string) []byte {
	return []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><title>` + label + `</title><path d="M0 0h10v10H0z"/></svg>`)
}

func pngFixture() []byte {
	return []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
		0x89, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xd7, 0x63, 0xf8, 0xcf, 0xc0, 0xf0,
		0x1f, 0x00, 0x05, 0x00, 0x01, 0xff, 0x89, 0x99,
		0x3d, 0x1d, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
		0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
	}
}

func pdfFixture() []byte {
	return []byte("%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\n%%EOF\n")
}
