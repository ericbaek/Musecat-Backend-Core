package arcade_test

import (
	"fmt"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
	"slices"
	"testing"
)

func TestSupporterReview(t *testing.T) {
	app := newArcadeTestApp(t)
	applicantToken, applicant := createAuthUserWithTags(t, app, []string{"arcade_owner"})
	moderatorToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	supporterToken, _ := createAuthUserWithTags(t, app, []string{"supporter"})
	headers := map[string]string{"Authorization": "Bearer " + moderatorToken}
	coll, err := app.FindCollectionByNameOrId("supporter_request")
	if err != nil {
		t.Fatal(err)
	}
	request := core.NewRecord(coll)
	request.Set("user", applicant.Id)
	request.Set("createdBy", applicant.Id)
	request.Set("status", "pending")
	if err := app.Save(request); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/moderation/supporter/requests", "/moderation/supporter/request?id=" + request.Id} {
		assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, path, "", nil), http.StatusUnauthorized)
		for _, token := range []string{applicantToken, supporterToken} {
			assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, path, "", map[string]string{"Authorization": "Bearer " + token}), http.StatusForbidden)
		}
		assertContractStatus(t, executeJSONRequest(t, app, http.MethodGet, path, "", headers), http.StatusOK)
	}
	detail := decodeJSONMap(t, executeJSONRequest(t, app, http.MethodGet, "/moderation/supporter/request?id="+request.Id, "", headers))
	if detail["joined"] != applicant.GetString("created") || detail["edit_count"] != float64(0) || detail["score"] == nil || detail["arcades"] == nil {
		t.Fatal("applicant details missing")
	}
	body := fmt.Sprintf(`{"id":%q,"status":"approved","reason":"Verified edits"}`, request.Id)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/supporter/request", body, map[string]string{"Authorization": "Bearer " + supporterToken}), http.StatusForbidden)
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/supporter/request", body, headers), http.StatusOK)
	saved := mustFindRecord(t, app, "user", applicant.Id)
	if !slices.Contains(saved.GetStringSlice("tags"), "founding_supporter") || !slices.Contains(saved.GetStringSlice("tags"), "arcade_owner") {
		t.Fatal("approval did not preserve tags and grant founding supporter")
	}
	if mustFindRecord(t, app, "supporter_request", request.Id).GetString("status") != "approved" {
		t.Fatal("approval not persisted")
	}
	assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/supporter/request", body, headers), http.StatusConflict)
}

func TestSupporterRejectionAndAtomicApproval(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		tags         []string
		expected     int
	}{
		{"reject", "rejected", nil, http.StatusOK},
		{"existing tag", "approved", []string{"founding_supporter"}, http.StatusOK},
		{"tag capacity rolls back", "approved", []string{"developer", "moderator", "arcade_owner"}, http.StatusBadGateway},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newArcadeTestApp(t)
			_, applicant := createAuthUserWithTags(t, app, tc.tags)
			token, _ := createAuthUserWithTags(t, app, []string{"developer"})
			coll, _ := app.FindCollectionByNameOrId("supporter_request")
			request := core.NewRecord(coll)
			request.Set("user", applicant.Id)
			request.Set("createdBy", applicant.Id)
			request.Set("status", "pending")
			if err := app.Save(request); err != nil {
				t.Fatal(err)
			}
			body := fmt.Sprintf(`{"id":%q,"status":%q,"reason":"Review"}`, request.Id, tc.status)
			assertContractStatus(t, executeJSONRequest(t, app, http.MethodPut, "/moderation/supporter/request", body, map[string]string{"Authorization": "Bearer " + token}), tc.expected)
			saved := mustFindRecord(t, app, "user", applicant.Id)
			if tc.status == "rejected" && slices.Contains(saved.GetStringSlice("tags"), "founding_supporter") {
				t.Fatal("rejection granted a role")
			}
			if tc.expected != http.StatusOK && mustFindRecord(t, app, "supporter_request", request.Id).GetString("status") != "pending" {
				t.Fatal("failed tag write still approved the request")
			}
			if tc.name == "existing tag" && len(saved.GetStringSlice("tags")) != 1 {
				t.Fatal("duplicate role")
			}
		})
	}
}
