package user

import (
	"encoding/json"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

type ProfileSeries struct {
	ID           string `json:"id"`
	SeriesNumber any    `json:"seriesNumber"`
	En           string `json:"en"`
	Kr           string `json:"kr"`
	Jp           string `json:"jp"`
	EnShort      string `json:"en_short"`
	KrShort      string `json:"kr_short"`
	JpShort      string `json:"jp_short"`
	Manufacturer any    `json:"manufacturer"`
}

type ProfileSNSItem struct {
	Type string `json:"type"`
	Link string `json:"link"`
	Name string `json:"name,omitempty"`
}

type ProfileSNS struct {
	Items []ProfileSNSItem `json:"items"`
}

type Profile struct {
	ID              string          `json:"id"`
	Created         string          `json:"created"`
	Username        string          `json:"username"`
	Nickname        string          `json:"nickname"`
	Level           int             `json:"level"`
	Bio             string          `json:"bio"`
	Avatar          string          `json:"avatar"`
	Background      string          `json:"background"`
	Tag             []string        `json:"tag"`
	SNS             ProfileSNS      `json:"sns"`
	Withdrawn       bool            `json:"withdrawn"`
	SeriesPublic    bool            `json:"series_public"`
	Warp            *bool           `json:"warp,omitempty"`
	Series          []ProfileSeries `json:"series,omitempty"`
	VisitVisibility string          `json:"visit_visibility,omitempty"`
	VisitStats      *VisitStats     `json:"visit_stats,omitempty"`
	Visits          []VisitSummary  `json:"visits,omitempty"`
}

func FetchMergedProfile(app core.App, userID string) (*Profile, error) {
	return fetchMergedProfile(app, userID, false)
}

func fetchMergedProfile(app core.App, userID string, includePrivateSeries bool) (*Profile, error) {
	userRec, err := app.FindRecordById(CollectionUser, userID)
	if err != nil {
		return nil, err
	}

	return mergeProfileFromRecords(app, userRec, findUserInfoRecord(app, userID), includePrivateSeries), nil
}

func FetchMergedProfileByUsername(app core.App, username string) (*Profile, error) {
	userRec, err := app.FindFirstRecordByFilter(
		CollectionUser,
		"username = {:username}",
		map[string]any{"username": strings.TrimSpace(username)},
	)
	if err != nil {
		return nil, err
	}

	return mergeProfileFromRecords(app, userRec, findUserInfoRecord(app, userRec.Id), false), nil
}

func BuildProfileFromAuth(app core.App, auth *core.Record) (*Profile, error) {
	if auth == nil {
		return nil, nil
	}

	return fetchMergedProfile(app, auth.Id, true)
}

func mergeProfileFromRecords(app core.App, userRec *core.Record, userInfoRec *core.Record, includePrivateSeries bool) *Profile {
	if userRec == nil {
		return nil
	}

	out := &Profile{
		ID:        userRec.Id,
		Created:   userRec.GetString("created"),
		Tag:       []string{},
		SNS:       ProfileSNS{Items: []ProfileSNSItem{}},
		Withdrawn: userRec.GetBool("withdrawn"),
	}
	if exp, err := LoadCurrentExp(app, userRec.Id); err == nil {
		out.Level = LevelFromExp(exp)
	}
	if includePrivateSeries {
		warp := false
		if userInfoRec != nil {
			warp = userInfoRec.GetBool("warp")
		}
		out.Warp = &warp
	}

	if out.Withdrawn {
		out.Username = WithdrawnDisplayName()
		out.Nickname = WithdrawnDisplayName()
		out.Bio = ""
		out.Avatar = ""
		out.Background = ""
		out.Tag = []string{}
		return out
	}

	username := strings.TrimSpace(userRec.GetString("username"))
	nickname := ""
	bio := ""
	avatar := ""
	background := ""
	tag := parseUserTag(userRec)

	if userInfoRec != nil {
		nickname = strings.TrimSpace(userInfoRec.GetString("nickname"))
		bio = strings.TrimSpace(userInfoRec.GetString("bio"))
		avatar = firstFileFilename(userInfoRec, "avatar")
		background = firstFileFilename(userInfoRec, "background")
		out.SNS = parseProfileSNS(userInfoRec)
		out.SeriesPublic = userInfoRec.GetBool("series_public")
		out.VisitVisibility = visitVisibility(userInfoRec.GetString("visit_visibility"))
	}

	out.Username = username
	out.Nickname = firstNonEmpty(nickname, username)
	out.Bio = bio
	out.Avatar = avatar
	out.Background = background
	out.Tag = tag
	if userInfoRec != nil && (userInfoRec.GetBool("series_public") || includePrivateSeries) {
		out.Series = loadProfileSeries(app, userInfoRec)
	}
	if !out.Withdrawn {
		if stats, err := LoadVisitStats(app, userRec.Id); err == nil && (includePrivateSeries || out.VisitVisibility != "private") {
			out.VisitStats = &stats
		}
		if includePrivateSeries || out.VisitVisibility == "full" {
			if visits, err := ListVisits(app, userRec.Id, 0, 0); err == nil {
				out.Visits = visits
			}
		}
	}

	return out
}

func parseProfileSNS(userInfoRec *core.Record) ProfileSNS {
	if userInfoRec == nil {
		return ProfileSNS{Items: []ProfileSNSItem{}}
	}

	raw := strings.TrimSpace(userInfoRec.GetString("sns"))
	if raw == "" {
		return ProfileSNS{Items: []ProfileSNSItem{}}
	}

	var parsed struct {
		Items []ProfileSNSItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return ProfileSNS{Items: []ProfileSNSItem{}}
	}

	items := make([]ProfileSNSItem, 0, len(parsed.Items))
	for _, item := range parsed.Items {
		snsType := strings.TrimSpace(item.Type)
		link := strings.TrimSpace(item.Link)
		if snsType == "" || link == "" {
			continue
		}

		next := ProfileSNSItem{
			Type: snsType,
			Link: link,
		}
		if name := strings.TrimSpace(item.Name); name != "" {
			next.Name = name
		}
		items = append(items, next)
	}

	return ProfileSNS{Items: items}
}

func findUserInfoRecord(app core.App, userID string) *core.Record {
	rec, err := app.FindRecordById(CollectionUserInfo, userID)
	if err != nil {
		return nil
	}

	return rec
}

func firstFileFilename(rec *core.Record, field string) string {
	if rec == nil {
		return ""
	}

	files := rec.GetStringSlice(field)
	for _, f := range files {
		if trimmed := strings.TrimSpace(f); trimmed != "" {
			return trimmed
		}
	}

	raw := strings.TrimSpace(rec.GetString(field))
	if raw == "" {
		return ""
	}

	parts := strings.Split(raw, ",")
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

func parseUserTag(userRec *core.Record) []string {
	if userRec == nil {
		return []string{}
	}

	tags := trimmedStringSlice(userRec.GetStringSlice("tag"))
	if len(tags) > 0 {
		return tags
	}

	// Backward/forward compatibility for potential schema name drift.
	tags = trimmedStringSlice(userRec.GetStringSlice("tags"))
	if len(tags) > 0 {
		return tags
	}

	tags = parseCSVList(userRec.GetString("tag"))
	if len(tags) > 0 {
		return tags
	}

	return parseCSVList(userRec.GetString("tags"))
}

func trimmedStringSlice(values []string) []string {
	if len(values) == 0 {
		return []string{}
	}

	out := make([]string, 0, len(values))
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	if len(out) == 0 {
		return []string{}
	}

	return out
}

func parseCSVList(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []string{}
	}

	parts := strings.Split(trimmed, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if value := strings.TrimSpace(p); value != "" {
			out = append(out, value)
		}
	}

	if len(out) == 0 {
		return []string{}
	}

	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}

	return ""
}

func loadProfileSeries(app core.App, userInfoRec *core.Record) []ProfileSeries {
	if app == nil || userInfoRec == nil {
		return nil
	}

	ids := relationIDs(userInfoRec, "series")
	if len(ids) == 0 {
		return []ProfileSeries{}
	}

	out := make([]ProfileSeries, 0, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(id) == "" {
			continue
		}

		rec, err := app.FindRecordById(CollectionGameSeries, id)
		if err != nil || rec == nil {
			continue
		}

		out = append(out, exportProfileSeries(rec))
	}

	if len(out) == 0 {
		return []ProfileSeries{}
	}

	return out
}

func exportProfileSeries(rec *core.Record) ProfileSeries {
	return ProfileSeries{
		ID:           rec.Id,
		SeriesNumber: rec.Get("seriesNumber"),
		En:           rec.GetString("en"),
		Kr:           rec.GetString("kr"),
		Jp:           rec.GetString("jp"),
		EnShort:      rec.GetString("en_short"),
		KrShort:      rec.GetString("kr_short"),
		JpShort:      rec.GetString("jp_short"),
		Manufacturer: rec.Get("manufacturer"),
	}
}

func relationIDs(rec *core.Record, field string) []string {
	if rec == nil {
		return nil
	}

	values := rec.GetStringSlice(field)
	if len(values) > 0 {
		out := make([]string, 0, len(values))
		for _, value := range values {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	}

	raw := strings.TrimSpace(rec.GetString(field))
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}

	return out
}
