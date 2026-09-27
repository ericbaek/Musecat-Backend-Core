package arcade_test

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
)

func TestSupportFeedbackMessageBoundary(t *testing.T) {
	for _, length := range []int{12000, 13000, 13001} {
		status := 200
		if length > 13000 {
			status = 400
		}
		scenario := tests.ApiScenario{
			Name: fmt.Sprintf("message length %d", length), Method: "POST", URL: "/support_feedback",
			Body:           strings.NewReader(`{"message":"` + strings.Repeat("한", length) + `"}`),
			ExpectedStatus: status, ExpectedContent: []string{`"message"`}, TestAppFactory: newArcadeTestApp,
		}
		if status == 400 {
			scenario.ExpectedContent = []string{`"error":"validation failed"`}
		}
		scenario.Test(t)
	}
}

func TestMyFeedbackOwnership(t *testing.T) {
	for _, mode := range []string{"active", "empty", "anonymous", "withdrawn", "banned"} {
		t.Run(mode, func(t *testing.T) {
			headers := map[string]string{}
			status := 200
			if mode == "anonymous" {
				status = 401
			}
			if mode == "withdrawn" || mode == "banned" {
				status = 403
			}
			scenario := tests.ApiScenario{Name: mode, Method: "GET", URL: "/user/feedback", Headers: headers, ExpectedStatus: status, ExpectedContent: []string{"{"}, TestAppFactory: newArcadeTestApp}
			if mode == "active" {
				scenario.ExpectedContent = []string{`"total":2`, "own-waiting", "own-solved"}
				scenario.NotExpectedContent = []string{"other-private", "anonymous-private"}
			}
			if mode == "empty" {
				scenario.ExpectedContent = []string{`"items":[]`, `"total":0`}
			}
			scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				token, user := createAuthUser(tb, app)
				if mode != "anonymous" {
					headers["Authorization"] = "Bearer " + token
				}
				_, other := createAuthUser(tb, app)
				scenario.URL = "/user/feedback?createdBy=" + other.Id
				seedSupportFeedback(tb, app, other.Id, "other-private", "waiting")
				seedSupportFeedback(tb, app, "", "anonymous-private", "waiting")
				if mode != "empty" {
					seedSupportFeedback(tb, app, user.Id, "own-waiting", "waiting")
					seedSupportFeedback(tb, app, user.Id, "own-solved", "solved")
				}
				if mode == "withdrawn" {
					user.Set("withdrawn", true)
					user.Set("withdrawnAt", time.Now().UTC())
					if err := app.Save(user); err != nil {
						tb.Fatal(err)
					}
				}
				if mode == "banned" {
					seedUserBan(tb, app, user.Id, "", "test", time.Now().Add(time.Hour))
				}
			}
			scenario.Test(t)
		})
	}
}

func TestFlagAndNoticeUploadLimits(t *testing.T) {
	for _, route := range []struct {
		path, method string
		max          int
	}{
		{"/arcade/flag", "POST", 15_000_000},
		{"/arcade/notice", "POST", 5 << 20},
		{"/arcade/notice", "PUT", 5 << 20},
	} {
		for _, tc := range []struct {
			name                string
			count, size, status int
			image               bool
		}{
			{"three maximum files", 3, route.max, 200, true},
			{"four files", 4, 100, 400, true},
			{"oversize file", 1, route.max + 1, 413, true},
			{"disguised text", 1, 100, 400, false},
		} {
			t.Run(route.method+route.path+tc.name, func(t *testing.T) {
				headers := map[string]string{}
				scenario := tests.ApiScenario{Name: tc.name, Method: route.method, URL: route.path, Headers: headers, ExpectedStatus: tc.status, ExpectedContent: []string{"{"}, TestAppFactory: newArcadeTestApp}
				scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
					token, user := createAuthUserWithTags(tb, app, []string{"moderator"})
					headers["Authorization"] = "Bearer " + token
					arcadeID, _ := seedArcade(tb, app, user.Id, arcadeSeed{Name: "Upload limits", Address: "Test", Location: location{Lat: 37, Lon: 127}})
					var body bytes.Buffer
					form := multipart.NewWriter(&body)
					fields := map[string]string{"arcade": arcadeID, "message": "broken", "disruption": "major", "type": "alert", "document": `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"notice"}]}]}`}
					if route.path == "/arcade/flag" {
						fields["game_id"] = seedGameAtomForFlag(tb, app, arcadeID)
					}
					if route.method == "PUT" {
						fields["id"] = seedNotice(tb, app, arcadeID)
					}
					for k, v := range fields {
						if err := form.WriteField(k, v); err != nil {
							tb.Fatal(err)
						}
					}
					for i := 0; i < tc.count; i++ {
						part, err := form.CreateFormFile("photos", fmt.Sprintf("photo%d.png", i))
						if err != nil {
							tb.Fatal(err)
						}
						data := bytes.Repeat([]byte("x"), tc.size)
						if tc.image {
							copy(data, pngFixtureBytes())
						}
						if _, err := part.Write(data); err != nil {
							tb.Fatal(err)
						}
					}
					if err := form.Close(); err != nil {
						tb.Fatal(err)
					}
					headers["Content-Type"] = form.FormDataContentType()
					scenario.Body = bytes.NewReader(body.Bytes())
				}
				scenario.Test(t)
			})
		}
	}
}

func TestFlagAndNoticeBodyLimit(t *testing.T) {
	for _, tc := range []struct {
		path, method string
		limit        int
	}{
		{"/arcade/flag", "POST", 46_048_576},
		{"/arcade/notice", "POST", 16 << 20},
		{"/arcade/notice", "PUT", 16 << 20},
	} {
		for _, declared := range []bool{true, false} {
			headers := map[string]string{"Content-Type": "application/json"}
			if declared {
				headers["Content-Length"] = fmt.Sprint(tc.limit + 1)
			}
			scenario := tests.ApiScenario{Name: fmt.Sprintf("%s %s declared=%t", tc.method, tc.path, declared), Method: tc.method, URL: tc.path, Headers: headers, ExpectedStatus: http.StatusRequestEntityTooLarge, ExpectedContent: []string{`"status":413`}, TestAppFactory: newArcadeTestApp}
			scenario.BeforeTestFunc = func(tb testing.TB, app *tests.TestApp, _ *core.ServeEvent) {
				token, _ := createAuthUserWithTags(tb, app, []string{"moderator"})
				headers["Authorization"] = "Bearer " + token
				scenario.Body = strings.NewReader(`{"message":"` + strings.Repeat("x", tc.limit))
				if !declared {
					// Prevent httptest.NewRequest from inferring ContentLength.
					scenario.Body = io.MultiReader(scenario.Body)
				}
			}
			scenario.Test(t)
		}
	}
}
