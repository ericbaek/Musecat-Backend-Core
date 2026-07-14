package arcade_test

import (
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/tests"
)

func TestSearch_ReturnsUsersAndArcadesAcrossSupportedFields(t *testing.T) {
	app := newArcadeTestApp(t)

	_, userByUsername := createAuthUser(t, app)
	userByUsername.Set("username", "searchhero")
	if err := app.Save(userByUsername); err != nil {
		t.Fatalf("failed to save username user: %v", err)
	}
	userByUsernameInfo := ensureUserInfo(t, app, userByUsername.Id)
	userByUsernameInfo.Set("nickname", "Alpha Nick")
	if err := app.Save(userByUsernameInfo); err != nil {
		t.Fatalf("failed to save username user info: %v", err)
	}

	_, userByNickname := createAuthUser(t, app)
	userByNickname.Set("username", "plainuser")
	if err := app.Save(userByNickname); err != nil {
		t.Fatalf("failed to save nickname user: %v", err)
	}
	userByNicknameInfo := ensureUserInfo(t, app, userByNickname.Id)
	userByNicknameInfo.Set("nickname", "Captain Search")
	if err := app.Save(userByNicknameInfo); err != nil {
		t.Fatalf("failed to save nickname user info: %v", err)
	}

	arcadeByName, _ := seedArcade(t, app, userByUsername.Id, arcadeSeed{
		Name:     "Search Palace",
		Address:  "101 Exact Road",
		Nickname: []string{"First Spot"},
	})
	setArcadeVisibility(t, app, arcadeByName, true, false)

	arcadeByNickname, _ := seedArcade(t, app, userByUsername.Id, arcadeSeed{
		Name:     "Ordinary Place",
		Address:  "202 Other Road",
		Nickname: []string{"Search Crew", "Backup"},
	})
	setArcadeVisibility(t, app, arcadeByNickname, true, false)

	arcadeByAddress, _ := seedArcade(t, app, userByUsername.Id, arcadeSeed{
		Name:     "Address Match",
		Address:  "333 Search Street",
		Nickname: []string{"Address Crew"},
	})
	setArcadeVisibility(t, app, arcadeByAddress, true, true)

	resByUsername := executeJSONRequest(t, app, http.MethodGet, "/search?q=searchhero", "", nil)
	assertSearchStatus(t, resByUsername, http.StatusOK)
	payloadByUsername := decodeSearchPayload(t, resByUsername)
	usersByUsername := decodeUsers(t, payloadByUsername)
	if len(usersByUsername) != 1 {
		t.Fatalf("expected 1 user match by username, got %d", len(usersByUsername))
	}
	if got := usersByUsername[0]["username"]; got != "searchhero" {
		t.Fatalf("expected username searchhero, got %v", got)
	}
	if got := usersByUsername[0]["nickname"]; got != "Alpha Nick" {
		t.Fatalf("expected nickname Alpha Nick, got %v", got)
	}
	if got := usersByUsername[0]["avatar"]; got != "" {
		t.Fatalf("expected empty avatar, got %v", got)
	}

	resByNickname := executeJSONRequest(t, app, http.MethodGet, "/search?q=captain", "", nil)
	assertSearchStatus(t, resByNickname, http.StatusOK)
	payloadByNickname := decodeSearchPayload(t, resByNickname)
	usersByNickname := decodeUsers(t, payloadByNickname)
	if len(usersByNickname) != 1 {
		t.Fatalf("expected 1 user match by nickname, got %d", len(usersByNickname))
	}
	if got := usersByNickname[0]["username"]; got != "plainuser" {
		t.Fatalf("expected username plainuser, got %v", got)
	}

	resByName := executeJSONRequest(t, app, http.MethodGet, "/search?q=palace", "", nil)
	assertSearchStatus(t, resByName, http.StatusOK)
	payloadByName := decodeSearchPayload(t, resByName)
	arcadesByName := decodeArcades(t, payloadByName)
	if len(arcadesByName) != 1 {
		t.Fatalf("expected 1 arcade match by name, got %d", len(arcadesByName))
	}
	assertArcadeShape(t, arcadesByName[0], arcadeByName, "Search Palace", "101 Exact Road", []string{"First Spot"}, false)

	resByArcadeNickname := executeJSONRequest(t, app, http.MethodGet, "/search?q=search%20crew", "", nil)
	assertSearchStatus(t, resByArcadeNickname, http.StatusOK)
	payloadByArcadeNickname := decodeSearchPayload(t, resByArcadeNickname)
	arcadesByNickname := decodeArcades(t, payloadByArcadeNickname)
	if len(arcadesByNickname) != 1 {
		t.Fatalf("expected 1 arcade match by nickname, got %d", len(arcadesByNickname))
	}
	assertArcadeShape(t, arcadesByNickname[0], arcadeByNickname, "Ordinary Place", "202 Other Road", []string{"Search Crew", "Backup"}, false)

	resByAddress := executeJSONRequest(t, app, http.MethodGet, "/search?q=search%20street", "", nil)
	assertSearchStatus(t, resByAddress, http.StatusOK)
	payloadByAddress := decodeSearchPayload(t, resByAddress)
	arcadesByAddress := decodeArcades(t, payloadByAddress)
	if len(arcadesByAddress) != 1 {
		t.Fatalf("expected 1 arcade match by address, got %d", len(arcadesByAddress))
	}
	assertArcadeShape(t, arcadesByAddress[0], arcadeByAddress, "Address Match", "333 Search Street", []string{"Address Crew"}, true)
}

func TestSearch_ExcludesNonPublicArcadesAndDeduplicatesNicknameMatches(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)

	publicArcade, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Double Nick",
		Address:  "101 Shared Ave",
		Nickname: []string{"Shared Search", "Search Shared"},
	})
	setArcadeVisibility(t, app, publicArcade, true, false)

	privateArcade, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Private Search Place",
		Address:  "202 Hidden Ave",
		Nickname: []string{"Hidden Search"},
	})
	setArcadeVisibility(t, app, privateArcade, false, false)

	res := executeJSONRequest(t, app, http.MethodGet, "/search?q=search", "", nil)
	assertSearchStatus(t, res, http.StatusOK)

	payload := decodeSearchPayload(t, res)
	arcades := decodeArcades(t, payload)
	if len(arcades) != 1 {
		t.Fatalf("expected exactly 1 public arcade match, got %d", len(arcades))
	}
	assertArcadeShape(t, arcades[0], publicArcade, "Double Nick", "101 Shared Ave", []string{"Shared Search", "Search Shared"}, false)
}

func TestSearch_RejectsEmptyQuery(t *testing.T) {
	app := newArcadeTestApp(t)

	res := executeJSONRequest(t, app, http.MethodGet, "/search?q=%20%20", "", nil)
	assertSearchStatus(t, res, http.StatusBadRequest)

	payload := decodeSearchPayload(t, res)
	if got := payload["error"]; got != "missing required query param 'q'" {
		t.Fatalf("expected missing q error, got %v", got)
	}
}

func TestSearch_LimitCapAndUserFallbackNickname(t *testing.T) {
	app := newArcadeTestApp(t)
	const token = "zzcaponly"

	for i := 0; i < 12; i++ {
		_, user := createAuthUser(t, app)
		username := token + "user" + strings.Repeat("x", i)
		user.Set("username", username)
		if err := app.Save(user); err != nil {
			t.Fatalf("failed to save capped user %d: %v", i, err)
		}

		info := ensureUserInfo(t, app, user.Id)
		if i == 0 {
			info.Set("nickname", "")
		} else {
			info.Set("nickname", token+" nick "+strings.Repeat("x", i))
		}
		if err := app.Save(info); err != nil {
			t.Fatalf("failed to save capped user info %d: %v", i, err)
		}
	}

	for i := 0; i < 12; i++ {
		arcadeID, _ := seedArcade(t, app, "", arcadeSeed{
			Name:     token + " Search " + strings.Repeat("x", i),
			Address:  token + " Address " + strings.Repeat("x", i),
			Nickname: []string{token + " Nick " + strings.Repeat("x", i)},
		})
		setArcadeVisibility(t, app, arcadeID, true, i%2 == 0)
	}

	res := executeJSONRequest(t, app, http.MethodGet, "/search?q="+token+"&limit=99", "", nil)
	assertSearchStatus(t, res, http.StatusOK)

	payload := decodeSearchPayload(t, res)
	users := decodeUsers(t, payload)
	arcades := decodeArcades(t, payload)

	if len(users) != 12 {
		t.Fatalf("expected all 12 user matches under raised cap, got %d", len(users))
	}
	if len(arcades) != 12 {
		t.Fatalf("expected all 12 arcade matches under raised cap, got %d", len(arcades))
	}
	if got := users[0]["username"]; got != token+"user" {
		t.Fatalf("expected exact username to rank first, got %v", got)
	}
	if got := users[0]["nickname"]; got != token+"user" {
		t.Fatalf("expected empty nickname to fall back to username, got %v", got)
	}

	resCapped := executeJSONRequest(t, app, http.MethodGet, "/search?q="+token+"&limit=999", "", nil)
	assertSearchStatus(t, resCapped, http.StatusOK)

	payloadCapped := decodeSearchPayload(t, resCapped)
	usersCapped := decodeUsers(t, payloadCapped)
	arcadesCapped := decodeArcades(t, payloadCapped)
	if len(usersCapped) != 12 {
		t.Fatalf("expected raised cap not to trim 12 users, got %d", len(usersCapped))
	}
	if len(arcadesCapped) != 12 {
		t.Fatalf("expected raised cap not to trim 12 arcades, got %d", len(arcadesCapped))
	}
}

func TestSearch_WithLocationSortsArcadesByDistance(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)
	const token = "zzlocsort"

	nearID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     token,
		Address:  token + " Near Road",
		Location: location{Lat: 37.5665, Lon: 126.9790},
	})
	setArcadeVisibility(t, app, nearID, true, false)

	midID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     token,
		Address:  token + " Mid Road",
		Location: location{Lat: 37.5665, Lon: 127.0200},
	})
	setArcadeVisibility(t, app, midID, true, false)

	farID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     token,
		Address:  token + " Far Road",
		Location: location{Lat: 37.5665, Lon: 127.2000},
	})
	setArcadeVisibility(t, app, farID, true, false)

	res := executeJSONRequest(t, app, http.MethodGet, "/search?q="+token+"&lat=37.5665&lon=126.9780", "", nil)
	assertSearchStatus(t, res, http.StatusOK)

	payload := decodeSearchPayload(t, res)
	arcades := decodeArcades(t, payload)
	if len(arcades) != 3 {
		t.Fatalf("expected 3 arcade matches, got %d", len(arcades))
	}

	wantIDs := []string{nearID, midID, farID}
	wantDistances := []float64{
		haversineKm(37.5665, 126.9780, 37.5665, 126.9790),
		haversineKm(37.5665, 126.9780, 37.5665, 127.0200),
		haversineKm(37.5665, 126.9780, 37.5665, 127.2000),
	}

	lastDistance := -1.0
	for i, arcade := range arcades {
		if got := arcade["id"]; got != wantIDs[i] {
			t.Fatalf("expected arcade[%d] id %q, got %v", i, wantIDs[i], got)
		}
		rawDistance, ok := arcade["distance_km"].(float64)
		if !ok {
			t.Fatalf("expected distance_km on arcade[%d], got %#v", i, arcade["distance_km"])
		}
		if !floatAlmostEq(rawDistance, wantDistances[i]) {
			t.Fatalf("expected distance_km[%d] ~= %f, got %f", i, wantDistances[i], rawDistance)
		}
		if rawDistance < lastDistance {
			t.Fatalf("expected distances to be sorted ascending, got %f before %f", lastDistance, rawDistance)
		}
		lastDistance = rawDistance
	}
}

func TestSearch_MatchesPluralizedArcadeNamesByTokens(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)

	arcadeID, _ := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Timezone @ Eastgardens",
		Address:  "Westfield Eastgardens",
		Nickname: []string{"Timezone East"},
	})
	setArcadeVisibility(t, app, arcadeID, true, false)

	res := executeJSONRequest(t, app, http.MethodGet, "/search?q=timezone%20eastgarden", "", nil)
	assertSearchStatus(t, res, http.StatusOK)

	payload := decodeSearchPayload(t, res)
	arcades := decodeArcades(t, payload)
	found := false
	for _, arcade := range arcades {
		if got := arcade["id"]; got == arcadeID {
			if name := arcade["name"]; name != "Timezone @ Eastgardens" {
				t.Fatalf("expected exact arcade name, got %v", name)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected arcade id %q to appear in results, got %#v", arcadeID, arcades)
	}
}

func TestSearch_RebuildsAfterBasicNameUpdate(t *testing.T) {
	app := newArcadeTestApp(t)
	_, user := createAuthUser(t, app)

	arcadeID, basicID := seedArcade(t, app, user.Id, arcadeSeed{
		Name:     "Cache Old Name",
		Address:  "Cache Road",
		Nickname: []string{"Cache Crew"},
	})
	setArcadeVisibility(t, app, arcadeID, true, false)

	res := executeJSONRequest(t, app, http.MethodGet, "/search?q=cache%20old", "", nil)
	assertSearchStatus(t, res, http.StatusOK)
	payload := decodeSearchPayload(t, res)
	arcades := decodeArcades(t, payload)
	if len(arcades) != 1 {
		t.Fatalf("expected 1 arcade before update, got %d", len(arcades))
	}

	basicRec, err := app.FindRecordById("arcade_basic", basicID)
	if err != nil {
		t.Fatalf("failed to load arcade_basic: %v", err)
	}
	basicRec.Set("name", "Cache New Name")
	if err := app.Save(basicRec); err != nil {
		t.Fatalf("failed to update arcade_basic name: %v", err)
	}

	res = executeJSONRequest(t, app, http.MethodGet, "/search?q=cache%20new", "", nil)
	assertSearchStatus(t, res, http.StatusOK)
	payload = decodeSearchPayload(t, res)
	arcades = decodeArcades(t, payload)
	if len(arcades) != 1 {
		t.Fatalf("expected 1 arcade after update, got %d", len(arcades))
	}
	if got := arcades[0]["id"]; got != arcadeID {
		t.Fatalf("expected updated arcade id %q, got %v", arcadeID, got)
	}
	if got := arcades[0]["name"]; got != "Cache New Name" {
		t.Fatalf("expected updated arcade name, got %v", got)
	}
}

func setArcadeVisibility(tb testing.TB, app *tests.TestApp, arcadeID string, public bool, closed bool) {
	tb.Helper()

	rec, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		tb.Fatalf("failed to load arcade: %v", err)
	}

	rec.Set("public", public)
	rec.Set("closed", closed)
	if err := app.Save(rec); err != nil {
		tb.Fatalf("failed to save arcade visibility: %v", err)
	}
}

func assertSearchStatus(tb testing.TB, res *http.Response, want int) {
	tb.Helper()
	if res.StatusCode != want {
		tb.Fatalf("expected status %d, got %d", want, res.StatusCode)
	}
}

func decodeSearchPayload(tb testing.TB, res *http.Response) map[string]any {
	tb.Helper()
	defer res.Body.Close()

	var payload map[string]any
	if err := json.NewDecoder(res.Body).Decode(&payload); err != nil {
		tb.Fatalf("failed to decode search payload: %v", err)
	}
	return payload
}

func decodeUsers(tb testing.TB, payload map[string]any) []map[string]any {
	tb.Helper()
	return decodeSearchObjects(tb, payload, "users")
}

func decodeArcades(tb testing.TB, payload map[string]any) []map[string]any {
	tb.Helper()
	return decodeSearchObjects(tb, payload, "arcades")
}

func decodeSearchObjects(tb testing.TB, payload map[string]any, key string) []map[string]any {
	tb.Helper()

	raw, ok := payload[key]
	if !ok {
		tb.Fatalf("expected %q key in payload: %#v", key, payload)
	}

	buf, err := json.Marshal(raw)
	if err != nil {
		tb.Fatalf("failed to marshal %s: %v", key, err)
	}

	var out []map[string]any
	if err := json.Unmarshal(buf, &out); err != nil {
		tb.Fatalf("failed to unmarshal %s: %v", key, err)
	}
	return out
}

func assertArcadeShape(tb testing.TB, payload map[string]any, wantID string, wantName string, wantAddress string, wantNickname []string, wantClosed bool) {
	tb.Helper()

	if got := payload["id"]; got != wantID {
		tb.Fatalf("expected arcade id %q, got %v", wantID, got)
	}
	if got := payload["name"]; got != wantName {
		tb.Fatalf("expected arcade name %q, got %v", wantName, got)
	}
	if got := payload["address"]; got != wantAddress {
		tb.Fatalf("expected arcade address %q, got %v", wantAddress, got)
	}
	if got := payload["closed"]; got != wantClosed {
		tb.Fatalf("expected closed=%v, got %v", wantClosed, got)
	}
	if _, ok := payload["country"]; !ok {
		tb.Fatalf("expected country field in payload: %#v", payload)
	}

	nickRaw, ok := payload["nickname"]
	if !ok {
		tb.Fatalf("expected nickname field in payload: %#v", payload)
	}
	buf, err := json.Marshal(nickRaw)
	if err != nil {
		tb.Fatalf("failed to marshal nickname: %v", err)
	}

	var gotNickname []string
	if err := json.Unmarshal(buf, &gotNickname); err != nil {
		tb.Fatalf("failed to unmarshal nickname slice: %v", err)
	}
	if len(gotNickname) != len(wantNickname) {
		tb.Fatalf("expected %d nicknames, got %d (%#v)", len(wantNickname), len(gotNickname), gotNickname)
	}
	for i := range wantNickname {
		if gotNickname[i] != wantNickname[i] {
			tb.Fatalf("expected nickname[%d]=%q, got %q", i, wantNickname[i], gotNickname[i])
		}
	}
}

func haversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	toRad := func(deg float64) float64 { return deg * math.Pi / 180 }

	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	lat1Rad := toRad(lat1)
	lat2Rad := toRad(lat2)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}
