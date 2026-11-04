package user_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func TestOwnerVisitTimelineUsesActualChronologyAndCurrentVenueLocations(t *testing.T) {
	app := nativeReadApp(t, true)
	web, owner := createAuthUser(t, app, true)
	_, other := createAuthUser(t, app, true)
	native := nativeLoginIssue(t, app, web).AccessToken
	first := seedVisitArcade(t, app, "Asia/Seoul")
	second := seedVisitArcade(t, app, "America/Los_Angeles")
	missing := seedVisitArcade(t, app, "America/Los_Angeles")
	hidden := seedVisitArcade(t, app, "Asia/Seoul")
	setVisitArcadeProfileDetails(t, app, first, "First arcade", "KR", 37.5, 127)
	setVisitArcadeProfileDetails(t, app, second, "Second arcade", "US", 34, -118)
	setVisitArcadeProfileDetails(t, app, missing, "Missing location", "US", 0, 0)
	second.Set("closed", true)
	hidden.Set("public", false)
	for _, r := range []*core.Record{second, hidden} {
		if err := app.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	collection, _ := app.FindCollectionByNameOrId("arcade_visit")
	// Insert out of order. Equal timestamps must use the actual record-id tie
	// breaker; local dates across countries deliberately differ from UTC order.
	for _, v := range []struct{ id, arcade, user, day, at string }{
		{"ccccccccccccccc", first.Id, owner.Id, "2026-07-02", "2026-07-01 15:00:00.000Z"},
		{"ddddddddddddddd", first.Id, owner.Id, "2026-07-03", "2026-07-02 15:00:00.000Z"},
		{"bbbbbbbbbbbbbbb", second.Id, owner.Id, "2026-07-01", "2026-07-01 16:00:00.000Z"},
		{"aaaaaaaaaaaaaaa", missing.Id, owner.Id, "2026-07-01", "2026-07-01 16:00:00.000Z"},
		{"eeeeeeeeeeeeeee", hidden.Id, owner.Id, "2026-07-02", "2026-07-02 10:00:00.000Z"},
		{"fffffffffffffff", first.Id, other.Id, "2026-07-01", "2026-07-01 10:00:00.000Z"},
	} {
		r := core.NewRecord(collection)
		r.Id = v.id
		r.Set("arcade", v.arcade)
		r.Set("user", v.user)
		r.Set("visit_day", v.day)
		r.Set("visited_at", v.at)
		if err := app.Save(r); err != nil {
			t.Fatal(err)
		}
	}
	// An unrelated target parameter cannot turn this owner operation into a
	// foreign-account timeline, and private profile visibility cannot hide self.
	info, _ := app.FindRecordById("user_info", owner.Id)
	info.Set("visit_visibility", "private")
	info.Set("visit_timeline_hidden", true)
	if err := app.Save(info); err != nil {
		t.Fatal(err)
	}
	actual := canonicalReadJSON(t, app, "/user/visits?user="+other.Id, native)
	expected := canonicalReadJSON(t, app, "/user/visits", web)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("native owner response differs from canonical web")
	}
	timeline := actual["timeline"].([]any)
	if len(timeline) != 4 {
		t.Fatal("timeline collapsed revisits or exposed hidden/foreign records")
	}
	for i, arcade := range []string{first.Id, missing.Id, second.Id, first.Id} {
		item := timeline[i].(map[string]any)
		if item["arcade"] != arcade || item["id"] != arcade+":"+item["visit_day"].(string) {
			t.Fatal("timeline lost server order or stable arcade/day identity")
		}
		if len(item) != 5 {
			t.Fatal("timeline exposed non-contract fields")
		}
	}
	if timeline[1].(map[string]any)["location"] != nil {
		t.Fatal("missing location must remain an explicit route gap")
	}
	location := timeline[0].(map[string]any)["location"].(map[string]any)
	if location["lat"] != 37.5 || location["lon"] != float64(127) {
		t.Fatal("timeline did not use registered venue coordinates")
	}
	for _, token := range []string{web, native} {
		r := nativeReadRequest(t, app, "GET", "/user/visits", token, nil, 200)
		if r.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("owner timeline may not be cached")
		}
	}
	exportCanonicalReadFixture(t, "visit-timeline", actual)
	// Current venue coordinates, rather than verification GPS or a copied
	// historical point, remain authoritative. Malformed legacy coordinates fail
	// closed without dropping the event or connecting its neighbours.
	if _, err := app.DB().NewQuery(`UPDATE arcade_basic SET location='{"lat":95,"lon":127}' WHERE id={:id}`).Bind(dbx.Params{"id": first.GetString("basic")}).Execute(); err != nil {
		t.Fatal(err)
	}
	invalid := canonicalReadJSON(t, app, "/user/visits", native)["timeline"].([]any)
	if len(invalid) != 4 || invalid[0].(map[string]any)["location"] != nil || invalid[3].(map[string]any)["location"] != nil {
		t.Fatal("invalid coordinates must preserve null timeline stops")
	}
	if _, err := app.DB().NewQuery(`UPDATE arcade_basic SET location='{"lat":"NaN","lon":127}' WHERE id={:id}`).Bind(dbx.Params{"id": first.GetString("basic")}).Execute(); err != nil {
		t.Fatal(err)
	}
	nonfinite := canonicalReadJSON(t, app, "/user/visits", native)["timeline"].([]any)
	if len(nonfinite) != 4 || nonfinite[0].(map[string]any)["location"] != nil {
		t.Fatal("nonfinite coordinate prevented a safe null timeline event")
	}
	for _, path := range []string{"/user?id=" + owner.Id, "/user/passport", "/user/passport/stamps"} {
		payload := canonicalReadJSON(t, app, path, web)
		raw, _ := json.Marshal(payload)
		if strings.Contains(string(raw), `"timeline"`) {
			t.Fatal("timeline leaked into another profile/passport contract")
		}
	}
	nativeReadRequest(t, app, "GET", "/user/visits", "", nil, 401)
	seedUserBanForUserTest(t, app, owner.Id, hashNormalizedEmailForUserTest(owner.Email()), "timeline_fixture", time.Now().Add(time.Hour))
	nativeReadRequest(t, app, "GET", "/user/visits", native, nil, 403)
}

func TestOwnerVisitTimelineEmptyIsSupportedAndPrivate(t *testing.T) {
	app := nativeReadApp(t, true)
	web, _ := createAuthUser(t, app, true)
	result := canonicalReadJSON(t, app, "/user/visits", web)
	timeline, ok := result["timeline"].([]any)
	if !ok || len(timeline) != 0 {
		t.Fatal("empty owner timeline must be an array, not missing or null")
	}
}
