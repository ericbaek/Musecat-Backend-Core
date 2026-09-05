package user_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
	"github.com/pocketbase/pocketbase/core"
)

func TestPassportPeriodsCitiesAndVisibility(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, u := createAuthUser(t, app, true)
	a := seedVisitArcade(t, app, "Asia/Seoul")
	b := seedVisitArcade(t, app, "Asia/Seoul")
	c, _ := app.FindCollectionByNameOrId("passport_city")
	city := core.NewRecord(c)
	city.Set("name", "Seoul")
	city.Set("country", "KR")
	city.Set("admin1", "11")
	city.Set("source_id", "1835848")
	if err := app.Save(city); err != nil {
		t.Fatal(err)
	}
	basic, _ := app.FindRecordById("arcade_basic", a.GetString("basic"))
	basic.Set("city_id", city.Id)
	if err := app.Save(basic); err != nil {
		t.Fatal(err)
	}
	visits, _ := app.FindCollectionByNameOrId("arcade_visit")
	for _, v := range []struct{ arcade, day, at string }{{a.Id, "2025-12-31", "2025-12-31 10:00:00.000Z"}, {a.Id, "2026-01-01", "2025-12-31 16:00:00.000Z"}, {b.Id, "2026-01-01", "2025-12-31 17:00:00.000Z"}, {b.Id, "2026-02-01", "2026-02-01 10:00:00.000Z"}} {
		r := core.NewRecord(visits)
		r.Set("user", u.Id)
		r.Set("arcade", v.arcade)
		r.Set("visit_day", v.day)
		r.Set("visited_at", v.at)
		if err := app.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	p, err := userhandler.LoadPassport(app, u.Id, "2026")
	if err != nil {
		t.Fatal(err)
	}
	if p.TotalVisits != 3 || p.NewArcades != 1 || p.DistinctCities != 1 || p.DistinctArcades != 2 || p.VisitDays != 2 || p.MaxArcadesInDay != 2 || p.UnclassifiedArcades != 1 || len(p.Months) != 12 {
		t.Fatalf("unexpected passport: %+v", p)
	}
	if p.Months[0].NewArcades != 1 || p.Months[0].Visits != 2 {
		t.Fatalf("months: %+v", p.Months)
	}
	headers := map[string]string{"Authorization": "Bearer " + token}
	res := doUserRequest(t, app, http.MethodGet, "/user/passport/stamps?year=2026&per_page=1&sort=visits", headers, "")
	var page struct {
		Total    int                         `json:"total"`
		LastPage int                         `json:"last_page"`
		Items    []userhandler.PassportStamp `json:"items"`
	}
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	json.NewDecoder(res.Body).Decode(&page)
	if page.Total != 2 || page.LastPage != 2 || len(page.Items) != 1 || page.Items[0].Arcade != b.Id {
		t.Fatalf("pagination: %+v", page)
	}
	for _, url := range []string{"/user/passport?year=oops", "/user/passport/stamps?sort=bad", "/user/passport/stamps?page=-1", "/user/passport/stamps?per_page=101"} {
		r := doUserRequest(t, app, http.MethodGet, url, headers, "")
		if r.StatusCode != 400 {
			t.Errorf("%s: %d", url, r.StatusCode)
		}
	}
	for _, url := range []string{"/user/passport", "/user/passport/stamps"} {
		r := doUserRequest(t, app, http.MethodGet, url, nil, "")
		if r.StatusCode != 401 {
			t.Errorf("unauth %s: %d", url, r.StatusCode)
		}
	}
	summary, err := userhandler.LoadVisitStats(app, u.Id, false)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(summary)
	for _, s := range []string{"first_visit_day", "visit_days", "visited_at", "accuracy_meters", "gained_exp"} {
		if strings.Contains(string(raw), s) {
			t.Errorf("summary leaked %s", s)
		}
	}
	a.Set("closed", true)
	if err := app.Save(a); err != nil {
		t.Fatal(err)
	}
	p, err = userhandler.LoadPassport(app, u.Id, "all")
	if err != nil || p.DistinctArcades != 2 {
		t.Fatalf("closed: %+v %v", p, err)
	}
	a.Set("public", false)
	if err := app.Save(a); err != nil {
		t.Fatal(err)
	}
	p, err = userhandler.LoadPassport(app, u.Id, "all")
	if err != nil || p.DistinctArcades != 1 || p.DistinctCities != 0 || p.UnclassifiedArcades != 1 {
		t.Fatalf("private: %+v %v", p, err)
	}
}

func TestPublicPassportSummaryRedactsEveryDateAndPrivateIsNotFound(t *testing.T) {
	app := newUserFetchTestApp(t)
	token, u := createAuthUser(t, app, true)
	a := seedVisitArcade(t, app, "Asia/Seoul")
	c, _ := app.FindCollectionByNameOrId("arcade_visit")
	v := core.NewRecord(c)
	v.Set("user", u.Id)
	v.Set("arcade", a.Id)
	v.Set("visit_day", "2026-01-02")
	v.Set("visited_at", "2026-01-02 10:00:00.000Z")
	if err := app.Save(v); err != nil {
		t.Fatal(err)
	}
	info, _ := app.FindRecordById("user_info", u.Id)
	info.Set("visit_visibility", "summary")
	if err := app.Save(info); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/user/passport?user=", "/user/passport/stamps?user="} {
		res := doUserRequest(t, app, http.MethodGet, path+u.Id, nil, "")
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		if res.StatusCode != 200 {
			t.Fatalf("public status %d %#v", res.StatusCode, out)
		}
		raw, _ := json.Marshal(out)
		for _, field := range []string{"first_visit_day", "last_visit_day", "visit_dates", "visited_at", "accuracy_meters"} {
			if strings.Contains(string(raw), field) {
				t.Fatalf("public summary leaked %s: %s", field, raw)
			}
		}
	}
	res := doUserRequest(t, app, http.MethodGet, "/user/passport/stamps?user="+u.Id, map[string]string{"Authorization": "Bearer " + token}, "")
	var owner map[string]any
	json.NewDecoder(res.Body).Decode(&owner)
	raw, _ := json.Marshal(owner)
	if !strings.Contains(string(raw), "first_visit_day") {
		t.Fatal("owner dates removed")
	}
	info.Set("visit_visibility", "full")
	if err := app.Save(info); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/user/passport?user=", "/user/passport/stamps?user="} {
		res := doUserRequest(t, app, http.MethodGet, path+u.Id, nil, "")
		var full map[string]any
		json.NewDecoder(res.Body).Decode(&full)
		raw, _ := json.Marshal(full)
		if res.StatusCode != 200 || !strings.Contains(string(raw), `"visit_dates":["2026-01-02"]`) {
			t.Fatalf("full dates missing: %d %s", res.StatusCode, raw)
		}
		for _, key := range []string{"visited_at", "accuracy_meters", "latitude", "longitude"} {
			if strings.Contains(string(raw), key) {
				t.Fatalf("full leaked %s", key)
			}
		}
	}
	info.Set("visit_visibility", "private")
	if err := app.Save(info); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/user/passport?user=", "/user/passport/stamps?user="} {
		res := doUserRequest(t, app, http.MethodGet, path+u.Id, nil, "")
		if res.StatusCode != 404 {
			t.Fatalf("private status %d", res.StatusCode)
		}
	}
}
