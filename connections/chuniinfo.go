package connections

import (
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"
)

const chuniInfoMe = "https://chuni.info/api/v1/oauth2/@me"

// IsChuniInfo identifies the fixed upstream contract, never a caller-chosen parser.
// A different endpoint keeps the generic provider's strict subject/issuer contract.
func (p Provider) IsChuniInfo() bool {
	return p.Issuer == "https://chuni.info" && p.AuthorizationURL == "https://chuni.info/oauth2/authorize" && p.TokenURL == "https://chuni.info/api/v1/oauth2/token" && p.UserInfoURL == chuniInfoMe && p.RecordsURL == chuniInfoMe
}

func (p Provider) RecordEndpoint(service string) string {
	if p.IsChuniInfo() {
		return chuniInfoMe
	}
	return p.RecordsURL + "/" + service
}

// ParseProviderRecords projects native APIs into the same validated public contract.
func ParseProviderRecords(p Provider, data []byte, service, subject string) (RecordEnvelope, error) {
	if !p.IsChuniInfo() {
		return ParseEnvelope(data, service, subject)
	}
	if service != "chunithm" || len(data) > recordMaxBytes {
		return RecordEnvelope{}, errEnvelope
	}
	var body map[string]any
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if dec.Decode(&body) != nil || dec.Decode(new(any)) != io.EOF || body["id"] != subject || subject == "" {
		return RecordEnvelope{}, errEnvelope
	}
	scopes, ok := body["scopes"].([]any)
	if !ok {
		return RecordEnvelope{}, errEnvelope
	}
	allowed := map[string]bool{}
	for _, v := range scopes {
		if s, ok := v.(string); ok {
			allowed[s] = true
		}
	}
	e := RecordEnvelope{Spec: RecordSpec, Service: service, GameSchema: "musecat-game/chunithm/v1", Visibility: "public", UpdatedAt: time.Now().UTC(), Source: RecordSource{Name: "CHUNITHM Info", URL: "https://chuni.info"}, Fields: []RecordField{}, Sections: []RecordSection{}, Selection: []RecordSelection{{Key: "recent_plays", Status: "unsupported"}}}
	label := func(en, ko string) RecordLabel { return RecordLabel{En: en, Ko: ko} }
	object := func(key string) map[string]any {
		if !allowed[key] {
			return nil
		}
		v, _ := body[key].(map[string]any)
		return v
	}
	text := func(m map[string]any, k string) string { s, _ := m[k].(string); return s }
	add := func(m map[string]any, from, key, typ, en, ko string) {
		v, exists := m[from]
		if !exists || v == nil {
			return
		}
		e.Fields = append(e.Fields, RecordField{Key: key, Label: label(en, ko), Type: typ, Value: v})
	}
	profile := object("profile")
	rating := object("rating")
	stats := object("stats")
	if profile != nil {
		name := text(profile, "chuni_name")
		if name == "" {
			name = text(profile, "display_name")
		}
		if name == "" {
			name = text(profile, "username")
		}
		e.Player = &RecordPlayer{Name: name, Title: text(rating, "honor"), Team: text(rating, "team_name")}
	}
	add(rating, "rating_current", "rating", "decimal", "Rating", "레이팅")
	add(rating, "rating_peak", "rating_max", "decimal", "Peak rating", "최고 레이팅")
	add(rating, "over_power", "over_power", "decimal", "OVER POWER", "OVER POWER")
	add(rating, "level", "level", "integer", "Level", "레벨")
	add(rating, "honor", "honor", "string", "Title", "칭호")
	add(rating, "team_name", "team_name", "string", "Team", "팀")
	add(rating, "team_tier", "team_tier", "string", "Team tier", "팀 등급")
	add(stats, "played_chart_count", "played_chart_count", "integer", "Played charts", "플레이한 보면")
	add(stats, "fc_count", "full_combo_count", "integer", "FULL COMBO", "FULL COMBO")
	add(stats, "aj_count", "all_justice_count", "integer", "ALL JUSTICE", "ALL JUSTICE")
	add(stats, "last_play_at", "last_played_at", "datetime", "Last play", "마지막 플레이")
	add(stats, "last_sync_at", "last_synced_at", "datetime", "Last sync", "마지막 동기화")
	if d, ok := stats["rank_distribution"].(map[string]any); ok {
		section := RecordSection{Key: "rank_distribution", Label: label("Rank distribution", "랭크 분포"), Columns: []RecordColumn{{Key: "rank", Label: label("Rank", "랭크"), Type: "rank"}, {Key: "count", Label: label("Charts", "보면 수"), Type: "integer"}}, Rows: [][]any{}}
		keys := make([]string, 0, len(d))
		for k := range d {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			section.Rows = append(section.Rows, []any{k, d[k]})
		}
		e.Sections = append(e.Sections, section)
	}
	// Only documented, recognized row fields are forwarded. Never infer a score or
	// a recent play from a best record or a webhook's new_plays count.
	scores := object("scores")
	if rows, ok := scores["best30"].([]any); ok {
		section := RecordSection{Key: "best30", Label: label("Best 30", "베스트 30"), Columns: []RecordColumn{{Key: "title", Label: label("Song", "곡"), Type: "string"}, {Key: "difficulty", Label: label("Difficulty", "난이도"), Type: "string"}, {Key: "score", Label: label("Score", "점수"), Type: "integer"}, {Key: "rank", Label: label("Rank", "랭크"), Type: "rank"}, {Key: "rating", Label: label("Chart rating", "곡별 레이팅"), Type: "decimal"}}, Rows: [][]any{}}
		for _, v := range rows {
			r, ok := v.(map[string]any)
			if !ok {
				return RecordEnvelope{}, errEnvelope
			}
			title := text(r, "song_title")
			if title == "" {
				title = text(r, "title")
			}
			if title == "" {
				return RecordEnvelope{}, errEnvelope
			}
			section.Rows = append(section.Rows, []any{title, r["difficulty"], r["score"], r["rank"], r["rating"]})
		}
		e.Sections = append(e.Sections, section)
	}
	if !allowed["profile"] {
		e.Selection = append(e.Selection, RecordSelection{Key: "player", Status: "denied"})
	}
	// The source grants data via OAuth scopes and documents no independent public
	// flag. Musecat still requires its owner's explicit publish switch; dev apps
	// remain owner/tester only. No hidden upstream groups are projected.
	raw := map[string]any{}
	b, _ := json.Marshal(e)
	_ = json.Unmarshal(b, &raw)
	raw["sub"] = subject
	b, _ = json.Marshal(raw)
	return ParseEnvelope(b, service, subject)
}
