package user_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

func TestUpdateProfileCountries(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, userRec := createAuthUser(t, app, true)
	headers := map[string]string{"Authorization": "Bearer " + token}

	res := doUserRequest(t, app, http.MethodPut, "/user/countries", headers, `{"countries":[" kr "]}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("single country status=%d", res.StatusCode)
	}
	payload := decodeJSON(t, res)
	if payload["primary_country"] != "KR" || !containsString(payload["countries"], "KR") {
		t.Fatalf("unexpected single-country payload: %#v", payload)
	}

	res = doUserRequest(t, app, http.MethodPut, "/user/countries", headers, `{"countries":["KR","JP"]}`)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("ordinary multi-country status=%d", res.StatusCode)
	}

	setUserLevelForCountriesTest(t, app, userRec.Id, userhandler.LevelBaseExp(15))
	res = doUserRequest(t, app, http.MethodPut, "/user/countries", headers, `{"countries":["KR","JP","US"]}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("level-15 multi-country status=%d", res.StatusCode)
	}
	payload = decodeJSON(t, res)
	if countries, ok := payload["countries"].([]any); !ok || len(countries) != 3 || countries[0] != "KR" || countries[2] != "US" {
		t.Fatalf("unexpected level-15 countries: %#v", payload)
	}

	for _, body := range []string{
		`{"countries":["KR","kr"]}`,
		`{"countries":["ZZ"]}`,
		`{"countries":["KR","JP","US","AU"]}`,
		`{"countries":null}`,
	} {
		res = doUserRequest(t, app, http.MethodPut, "/user/countries", headers, body)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %s status=%d, want 400", body, res.StatusCode)
		}
	}
}

func TestProfileCountriesRestrictPublicAfterAccessLoss(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, userRec := createAuthUser(t, app, true)
	setUserLevelForCountriesTest(t, app, userRec.Id, userhandler.LevelBaseExp(15))
	info := ensureUserInfo(t, app, userRec.Id)
	info.Set("countries", []string{"KR", "JP", "US"})
	if err := app.Save(info); err != nil {
		t.Fatal(err)
	}

	setUserLevelForCountriesTest(t, app, userRec.Id, 0)

	public := doUserRequest(t, app, http.MethodGet, "/user?id="+userRec.Id, nil, "")
	if public.StatusCode != http.StatusOK {
		t.Fatalf("public profile status=%d", public.StatusCode)
	}
	var publicPayload map[string]any
	if err := json.NewDecoder(public.Body).Decode(&publicPayload); err != nil {
		t.Fatal(err)
	}
	if publicPayload["primary_country"] != "KR" || !containsOnlyCountry(publicPayload["countries"], "KR") {
		t.Fatalf("unexpected public countries: %#v", publicPayload)
	}

	self := doUserRequest(t, app, http.MethodGet, "/user/me", map[string]string{"Authorization": "Bearer " + token}, "")
	if self.StatusCode != http.StatusOK {
		t.Fatalf("self profile status=%d", self.StatusCode)
	}
	var selfPayload map[string]any
	if err := json.NewDecoder(self.Body).Decode(&selfPayload); err != nil {
		t.Fatal(err)
	}
	if countries, ok := selfPayload["countries"].([]any); !ok || len(countries) != 3 {
		t.Fatalf("self profile must retain saved countries: %#v", selfPayload)
	}
}

func setUserLevelForCountriesTest(t *testing.T, app *tests.TestApp, userID string, exp int) {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId("user_level")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := app.FindRecordById("user_level", userID)
	if err != nil {
		rec = core.NewRecord(coll)
		rec.Set("id", userID)
		rec.Set("user", userID)
	}
	rec.Set("exp", exp)
	if err := app.Save(rec); err != nil {
		t.Fatal(err)
	}
}

func containsOnlyCountry(value any, country string) bool {
	items, ok := value.([]any)
	return ok && len(items) == 1 && items[0] == country
}
