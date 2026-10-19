package connections_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ericbaek/musecat-backend-core/connections"
)

func chuniInfoProvider() connections.Provider {
	p := provider("chuniinfo")
	p.Issuer = "https://chuni.info"
	p.AuthorizationURL = p.Issuer + "/oauth2/authorize"
	p.TokenURL = p.Issuer + "/api/v1/oauth2/token"
	p.UserInfoURL = p.Issuer + "/api/v1/oauth2/@me"
	p.RecordsURL = p.UserInfoURL
	p.Services = p.Services[:1]
	return p
}
func chuniBody() map[string]any {
	return map[string]any{"id": "42", "scopes": []string{"profile", "rating", "stats", "scores"}, "profile": map[string]any{"display_name": "PLAYER"}, "rating": map[string]any{"rating_current": 16.42, "rating_peak": 16.58, "over_power": 12345.67}, "stats": map[string]any{"played_chart_count": 123, "fc_count": 80, "aj_count": 30, "rank_distribution": map[string]int{"sssp": 30}, "last_play_at": "2026-01-01T10:00:00Z"}, "scores": map[string]any{"best30": []any{map[string]any{"song_title": "Song", "difficulty": "MASTER", "score": 1009876, "rank": "SSS+", "rating": 16.7}}}}
}
func TestChuniInfoProjectionAndScopeBoundary(t *testing.T) {
	p := chuniInfoProvider()
	body := chuniBody()
	data, _ := json.Marshal(body)
	e, err := connections.ParseProviderRecords(p, data, "chunithm", "42")
	if err != nil || e.Player.Name != "PLAYER" || len(e.Fields) != 7 || len(e.Sections) != 2 || e.RecentPlays != nil {
		t.Fatalf("projection: %+v %v", e, err)
	}
	if e.Sections[0].Rows[0][0] != "SSS+" {
		t.Fatal("native rank was not normalized", e.Sections[0])
	}
	if p.RecordEndpoint("chunithm") != p.UserInfoURL {
		t.Fatal("wrong endpoint")
	}
	if _, err = connections.ParseProviderRecords(p, data, "chunithm", "other"); err == nil {
		t.Fatal("cross-subject accepted")
	}
	if _, err = connections.ParseProviderRecords(p, data, "sdvx", "42"); err == nil {
		t.Fatal("wrong game accepted")
	}
	body["scopes"] = []string{"profile"}
	data, _ = json.Marshal(body)
	e, err = connections.ParseProviderRecords(p, data, "chunithm", "42")
	if err != nil || len(e.Fields) != 0 || len(e.Sections) != 0 {
		t.Fatal("unconsented fields leaked", e, err)
	}
	body = chuniBody()
	body["rating"].(map[string]any)["rating_current"] = "broken"
	data, _ = json.Marshal(body)
	if _, err = connections.ParseProviderRecords(p, data, "chunithm", "42"); err == nil {
		t.Fatal("invalid metric accepted")
	}
	p.UserInfoURL = "https://other.example/me"
	if p.IsChuniInfo() {
		t.Fatal("adapter escaped pinned endpoint")
	}
	if _, err = connections.ParseProviderRecords(p, data, "chunithm", "42"); err == nil {
		t.Fatal("generic contract bypass")
	}
}
func TestChuniInfoOAuthAndOwnerRecords(t *testing.T) {
	f := newFixture(t)
	p := chuniInfoProvider()
	if err := f.manager.Reload([]connections.Provider{p}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	f.joint = func(r *http.Request) *http.Response {
		if r.URL.Host != "chuni.info" {
			return nil
		}
		if r.URL.Path == "/api/v1/oauth2/token" {
			_ = r.ParseForm()
			id, secret, ok := r.BasicAuth()
			if !ok || id != "musecat" || secret != "client-secret" {
				t.Error("missing Basic authentication")
			}
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if base64.RawURLEncoding.EncodeToString(sum[:]) != f.challenge {
				t.Error("PKCE not bound")
			}
			return jsonResponse(map[string]any{"access_token": "secret-access", "refresh_token": "secret-refresh", "token_type": "Bearer", "expires_in": 86400, "scope": "profile rating stats scores"})
		}
		if r.URL.Path == "/api/v1/oauth2/@me" {
			if r.Header.Get("Authorization") != "Bearer secret-access" {
				t.Error("missing bearer")
			}
			calls++
			return jsonResponse(chuniBody())
		}
		t.Errorf("unexpected chuni endpoint %s", r.URL.Path)
		return status(404, nil)
	}
	input := f.start(f.token, "chuniinfo", "chunithm")
	input.Issuer = "" // provider's documented OAuth2 callback has no iss
	id := f.complete(input)
	view := f.request(f.token, "GET", "/user/connections/"+id+"/records/chunithm", nil, 200)
	if view["status"] != "ok" || calls != 2 {
		t.Fatal(view, calls)
	}
	payload, _ := json.Marshal(view["payload"])
	if strings.Contains(string(payload), `"id":"42"`) {
		t.Fatal("subject leaked")
	}
	other, _ := f.newUser()
	f.request(other, "GET", "/user/connections/"+id+"/records/chunithm", nil, 404)
}
