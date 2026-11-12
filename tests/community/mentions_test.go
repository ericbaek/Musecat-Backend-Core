package community_test

import (
	"encoding/json"
	"fmt"
	"github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	"github.com/ericbaek/musecat-backend-core/service/communitymention"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestPostMentionsAtomicAndIdempotent(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, author := createCommunityUser(t, app)
	_, target := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	label := target.GetString("username")
	body := "🎉 @" + label
	mentions := []communitymention.Mention{{Kind: "user", ID: target.Id, Label: label, Start: 3, End: 4 + len(label)}}
	raw, _ := json.Marshal(mentions)
	response := achievementRequest(t, app, "POST", "", token, map[string]string{"flair": "achievement", "game_series": series.Id, "body": body, "mentions": string(raw)}, 1)
	if response.StatusCode != 201 {
		t.Fatal(response.StatusCode, decodeBody(t, response))
	}
	post := decodeBody(t, response)["id"].(string)
	count := func() int64 {
		n, err := app.CountRecords(usernotification.Inbox, dbx.HashExp{"kind": usernotification.CommunityMention, "community_post": post})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatal("missing mention notification")
	}
	payload := fmt.Sprintf(`{"body":%q,"flair":"achievement","game_series":%q,"mentions":%s}`, body, series.Id, raw)
	for range 2 {
		res := communityRequest(t, app, "PUT", "/community/post?id="+post, payload, token)
		if res.StatusCode != 200 {
			t.Fatal(res.StatusCode, decodeBody(t, res))
		}
	}
	if count() != 1 {
		t.Fatal("edit duplicated notification")
	}
	rows, _ := app.FindRecordsByFilter(usernotification.Inbox, "kind='community_mention'", "", 0, 0)
	if !usernotification.Visible(app, target, rows[0]) || usernotification.Visible(app, author, rows[0]) {
		t.Fatal("incorrect notification visibility")
	}
	res := communityRequest(t, app, "PUT", "/community/post?id="+post, fmt.Sprintf(`{"body":"removed","flair":"achievement","game_series":%q}`, series.Id), token)
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	if usernotification.Visible(app, target, rows[0]) {
		t.Fatal("removed mention still visible")
	}
	res = communityRequest(t, app, "PUT", "/community/post?id="+post, payload, token)
	if res.StatusCode != 200 || count() != 1 {
		t.Fatal("readding mention duplicated event")
	}
	bad := strings.Replace(payload, label, "forged", 1)
	res = communityRequest(t, app, "PUT", "/community/post?id="+post, bad, token)
	if res.StatusCode != 400 {
		t.Fatal("forged mention accepted", res.StatusCode)
	}
}

func TestArcadeMentionRequiresPublicCanonicalTarget(t *testing.T) {
	app := newCommunityTestApp(t)
	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		se.Router.GET("/arcades/updates", query.ListArcadeUpdates)
		se.Router.GET("/arcades/updates/history", query.ListArcadeUpdateHistory)
		return se.Next()
	})
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	arcadeCol, _ := app.FindCollectionByNameOrId("arcade")
	arcade := core.NewRecord(arcadeCol)
	arcade.Set("country", "KR")
	arcade.Set("timezone", "Asia/Seoul")
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	basicCol, _ := app.FindCollectionByNameOrId("arcade_basic")
	basic := core.NewRecord(basicCol)
	basic.Set("arcade", arcade.Id)
	basic.Set("name", "Beta Arcade")
	if err := app.Save(basic); err != nil {
		t.Fatal(err)
	}
	arcade.Set("basic", basic.Id)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	mentions, _ := json.Marshal([]communitymention.Mention{{Kind: "arcade", ID: arcade.Id, Label: "Beta Arcade", Start: 0, End: 12}, {Kind: "arcade", ID: arcade.Id, Label: "Beta Arcade", Start: 13, End: 25}})
	fields := map[string]string{"flair": "achievement", "game_series": series.Id, "body": "@Beta Arcade @Beta Arcade", "mentions": string(mentions)}
	res := achievementRequest(t, app, "POST", "", token, fields, 1)
	if res.StatusCode != 400 {
		t.Fatal("private arcade accepted", res.StatusCode)
	}
	arcade.Set("public", true)
	if err := app.Save(arcade); err != nil {
		t.Fatal(err)
	}
	res = achievementRequest(t, app, "POST", "", token, fields, 1)
	if res.StatusCode != 201 {
		t.Fatal(res.StatusCode, decodeBody(t, res))
	}
	if n, _ := app.CountRecords(usernotification.Inbox); n != 0 {
		t.Fatal("arcade mention generated user alert")
	}
	id := decodeBody(t, res)["id"].(string)
	post, err := app.FindRecordById("community_post", id)
	if err != nil || !reflect.DeepEqual(post.GetStringSlice("arcades"), []string{arcade.Id}) {
		t.Fatal("arcade relation missing or duplicated", err)
	}
	created := post.GetString("created")
	assertActivity := func(want bool) {
		t.Helper()
		for _, path := range []string{"/arcades/updates", "/arcades/updates/history"} {
			response := communityRequest(t, app, http.MethodGet, path, "", token)
			if response.StatusCode != 200 {
				t.Fatal(response.StatusCode, decodeBody(t, response))
			}
			var payload any
			if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			rows, ok := payload.([]any)
			if !ok {
				rows = payload.(map[string]any)["items"].([]any)
			}
			if !want {
				if len(rows) != 0 {
					t.Fatal("ineligible achievement leaked", payload)
				}
				continue
			}
			if len(rows) != 1 {
				t.Fatal("missing or duplicated arcade activity", payload)
			}
			row := rows[0].(map[string]any)
			if row["id"] != arcade.Id || !reflect.DeepEqual(row["updated_parts"], []any{"achievement"}) {
				t.Fatal(payload)
			}
			changes := row["changes"].([]any)
			if len(changes) != 1 || changes[0].(map[string]any)["created"] != created {
				t.Fatal("achievement timestamp or deduplication changed", payload)
			}
		}
	}
	assertActivity(true)
	for _, field := range []string{"closed", "public"} {
		arcade.Set(field, field == "closed")
		if err := app.Save(arcade); err != nil {
			t.Fatal(err)
		}
		assertActivity(false)
		arcade.Set(field, field == "public")
		if err := app.Save(arcade); err != nil {
			t.Fatal(err)
		}
	}
	assertActivity(true)
	update := func(body string, refs string, flair string) {
		t.Helper()
		response := communityRequest(t, app, "PUT", "/community/post?id="+id, fmt.Sprintf(`{"body":%q,"mentions":%s,"flair":%q,"game_series":%q,"arcades":[%q]}`, body, refs, flair, series.Id, arcade.Id), token)
		if response.StatusCode != 200 {
			t.Fatal(response.StatusCode, decodeBody(t, response))
		}
		response.Body.Close()
	}
	update("removed", "[]", "achievement")
	post, _ = app.FindRecordById("community_post", id)
	if len(post.GetStringSlice("arcades")) != 0 {
		t.Fatal("removed mention retained relation or trusted client relation")
	}
	assertActivity(false)
	update(fields["body"], string(mentions), "tip")
	assertActivity(false)
	update(fields["body"], string(mentions), "achievement")
	assertActivity(true)
	response := communityRequest(t, app, "DELETE", "/community/post?id="+id, "", token)
	if response.StatusCode != 204 {
		t.Fatal(response.StatusCode)
	}
	response.Body.Close()
	assertActivity(false)
}

func TestTiptapDocumentIsCanonicalAndLegacyUpdatesPreserveDocument(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	series := achievementSeries(t, app)
	created := achievementRequest(t, app, "POST", "", token, map[string]string{"flair": "achievement", "game_series": series.Id, "body": "original"}, 1)
	data := decodeBody(t, created)
	if created.StatusCode != 201 || data["body_document"] == nil {
		t.Fatal(data)
	}
	id := data["id"].(string)
	document := `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Rich caption"}]}]}`
	payload := fmt.Sprintf(`{"flair":"achievement","game_series":%q,"body":"spoofed preview","body_document":%s}`, series.Id, document)
	response := communityRequest(t, app, "PUT", "/community/post?id="+id, payload, token)
	got := decodeBody(t, response)
	if response.StatusCode != 200 || got["body"] != "Rich caption" {
		t.Fatal(response.StatusCode, got)
	}
	original, _ := json.Marshal(got["body_document"])
	response = communityRequest(t, app, "PUT", "/community/post?id="+id, fmt.Sprintf(`{"flair":"achievement","game_series":%q,"body":"Rich caption"}`, series.Id), token)
	got = decodeBody(t, response)
	saved, _ := json.Marshal(got["body_document"])
	if response.StatusCode != 200 || string(saved) != string(original) {
		t.Fatal("legacy metadata edit lost marks", got)
	}
	if col, _ := app.FindCollectionByNameOrId("community_post"); col.Fields.GetByName("body") != nil {
		t.Fatal("redundant caption column remains")
	}

}

func TestRichDocumentCannotBypassRequiredBody(t *testing.T) {
	app := newCommunityTestApp(t)
	defer app.Cleanup()
	token, _ := createCommunityUser(t, app)
	response := communityRequest(t, app, "POST", "/community/post", `{"flair":"tip","body":"spoofed","body_document":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"   "}]}]}}`, token)
	if response.StatusCode != 400 {
		t.Fatal("blank rich body accepted", response.StatusCode)
	}
}
