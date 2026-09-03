package arcade_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestChangelogPhotoAssets_BothTimelinesRespectCurrentAccessAndArcadeOwnership(t *testing.T) {
	app := newArcadeTestApp(t)
	ownerToken, owner := createAuthUserWithTags(t, app, nil)
	contributorToken, _ := createAuthUserWithTags(t, app, nil)
	supporterToken, _ := createAuthUserWithTags(t, app, []string{"supporter"})
	moderatorToken, _ := createAuthUserWithTags(t, app, []string{"moderator"})
	arcadeID, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Photo history", Location: location{Lat: 37.5, Lon: 127}})
	foreignArcade, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Other arcade", Location: location{Lat: 37.5, Lon: 127}})
	published := seedPhotoAtom(t, app, arcadeID, owner.Id, true)
	pending := seedPhotoAtom(t, app, arcadeID, owner.Id, false)
	foreign := seedPhotoAtom(t, app, foreignArcade, owner.Id, true)
	coll, err := app.FindCollectionByNameOrId("arcade_changelog")
	if err != nil {
		t.Fatal(err)
	}
	change := core.NewRecord(coll)
	change.Set("arcade", arcadeID)
	change.Set("by", owner.Id)
	change.Set("changed", "photo")
	entries := []map[string]any{}
	for _, id := range []string{published, pending, foreign, "missingphoto123"} {
		entries = append(entries, map[string]any{"change_type": "deleted", "prev_id": id, "photo": id})
	}
	change.Set("log", map[string]any{"type": "photo_diff", "version": 1, "items": entries})
	if err := app.Save(change); err != nil {
		t.Fatal(err)
	}
	original, _ := json.Marshal(change.Get("log"))
	arcade, err := app.FindRecordById("arcade", arcadeID)
	if err != nil {
		t.Fatal(err)
	}

	for _, state := range []struct {
		name           string
		public, closed bool
	}{
		{"public", true, false}, {"closed", true, true}, {"private", false, false},
	} {
		arcade.Set("public", state.public)
		arcade.Set("closed", state.closed)
		if err := app.Save(arcade); err != nil {
			t.Fatal(err)
		}
		for _, actor := range []struct {
			name, token   string
			privateAccess bool
		}{
			{"anonymous", "", false}, {"owner", ownerToken, true},
			{"contributor", contributorToken, false}, {"supporter", supporterToken, false},
			{"moderator", moderatorToken, true},
		} {
			for _, scope := range []string{"arcade", "user"} {
				t.Run(state.name+"/"+actor.name+"/"+scope, func(t *testing.T) {
					url := "/arcade/changelog?arcade=" + arcadeID
					if scope == "user" {
						url = "/user/changelog?user=" + owner.Id
					}
					headers := map[string]string{}
					if actor.token != "" {
						headers["Authorization"] = "Bearer " + actor.token
					}
					res := executeJSONRequest(t, app, http.MethodGet, url, "", headers)
					if !state.public && !actor.privateAccess && scope == "arcade" {
						defer res.Body.Close()
						if res.StatusCode != http.StatusNotFound {
							t.Fatalf("private history leaked: %d", res.StatusCode)
						}
						return
					}
					if res.StatusCode != http.StatusOK {
						t.Fatalf("status %d", res.StatusCode)
					}
					payload := decodeJSONMap(t, res)
					items := payload["items"].([]any)
					if !state.public && !actor.privateAccess {
						if len(items) != 0 {
							t.Fatalf("private user history leaked: %#v", items)
						}
						return
					}
					if len(items) != 1 {
						t.Fatalf("expected one row, got %#v", items)
					}
					assets := items[0].(map[string]any)["photo_assets"].([]any)
					want := map[string]bool{published: true}
					if actor.token != "" {
						want[pending] = true
					}
					if len(assets) != len(want) {
						t.Fatalf("wrong assets: %#v", assets)
					}
					for _, value := range assets {
						asset := value.(map[string]any)
						id := asset["id"].(string)
						if !want[id] || asset["file_url"] != "/arcade/photo/file?id="+id {
							t.Fatalf("foreign/missing asset leaked: %#v", asset)
						}
						file := executeJSONRequest(t, app, http.MethodGet, asset["file_url"].(string), "", headers)
						file.Body.Close()
						if file.StatusCode != http.StatusOK {
							t.Fatalf("advertised photo not readable: %d", file.StatusCode)
						}
					}
				})
			}
		}
	}
	stored, err := app.FindRecordById("arcade_changelog", change.Id)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(stored.Get("log"))
	if string(original) != string(after) {
		t.Fatal("read enrichment changed immutable log")
	}
}

func TestChangelogMemoSnapshots_DistinguishAbsentMissingAndForeignRevisions(t *testing.T) {
	app := newArcadeTestApp(t)
	token, owner := createAuthUserWithTags(t, app, nil)
	arcadeID, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Memo history", Location: location{Lat: 37.5, Lon: 127}})
	otherID, _ := seedPublicArcade(t, app, owner.Id, arcadeSeed{Name: "Other memo", Location: location{Lat: 37.5, Lon: 127}})
	saveMemo := func(id, document string) string {
		body := `{"arcade":"` + id + `","document":` + document + `}`
		res := executeJSONRequest(t, app, http.MethodPut, "/arcade/memo", body, map[string]string{"Authorization": "Bearer " + token})
		if res.StatusCode != http.StatusOK {
			t.Fatalf("memo setup status: %d", res.StatusCode)
		}
		return decodeJSONMap(t, res)["memo"].(map[string]any)["id"].(string)
	}
	available := saveMemo(arcadeID, `{"type":"doc","content":[]}`)
	foreign := saveMemo(otherID, `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"foreign-secret"}]}]}`)
	collection, err := app.FindCollectionByNameOrId("arcade_changelog")
	if err != nil {
		t.Fatal(err)
	}
	for _, before := range []string{"", "missingmemo12345", foreign} {
		row := core.NewRecord(collection)
		row.Set("arcade", arcadeID)
		row.Set("changed", "memo")
		row.Set("by", owner.Id)
		row.Set("from", before)
		row.Set("to", available)
		if err := app.Save(row); err != nil {
			t.Fatal(err)
		}
	}
	for _, url := range []string{"/arcade/changelog?arcade=" + arcadeID, "/user/changelog?user=" + owner.Id} {
		res := executeJSONRequest(t, app, http.MethodGet, url, "", nil)
		payload := decodeJSONMap(t, res)
		for _, value := range payload["items"].([]any) {
			row := value.(map[string]any)
			if row["arcade"] != arcadeID {
				continue
			}
			memo := row["memo"].(map[string]any)
			want := "unavailable"
			if row["from"] == "" || row["from"] == nil {
				want = "absent"
			}
			if memo["before_status"] != want || memo["after_status"] != "available" {
				t.Fatalf("wrong snapshot status: %#v", memo)
			}
			encoded, _ := json.Marshal(memo)
			if strings.Contains(string(encoded), "foreign-secret") {
				t.Fatal("foreign memo leaked")
			}
		}
	}
}
