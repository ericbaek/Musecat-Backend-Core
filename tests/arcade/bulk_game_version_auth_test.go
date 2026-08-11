package arcade_test

import (
	"net/http"
	"testing"
)

func TestBulkUpdateArcadeGameVersionRequiresDeveloperOrModerator(t *testing.T) {
	app := newArcadeTestApp(t)

	for _, role := range []string{"developer", "moderator"} {
		t.Run(role+"_allowed", func(t *testing.T) {
			token, _ := createAuthUserWithTags(t, app, []string{role})
			res := executeJSONRequest(t, app, http.MethodPost, "/arcade/game/bulk_version", `{}`, map[string]string{
				"Authorization": "Bearer " + token,
			})
			defer res.Body.Close()
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("expected %s to pass authorization and fail validation with 400, got %d", role, res.StatusCode)
			}
		})
	}

	for _, role := range []string{"supporter", "founding_supporter"} {
		t.Run(role, func(t *testing.T) {
			token, _ := createAuthUserWithTags(t, app, []string{role})
			res := executeJSONRequest(t, app, http.MethodPost, "/arcade/game/bulk_version", `{}`, map[string]string{
				"Authorization": "Bearer " + token,
			})
			defer res.Body.Close()
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("expected %s to receive 403, got %d", role, res.StatusCode)
			}
		})
	}
}
