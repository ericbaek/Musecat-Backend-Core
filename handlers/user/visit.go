package user

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/pocketbase/pocketbase/tools/types"
)

const (
	visitRadiusMeters      = 100.0
	maxVisitAccuracyMeters = 100.0
	firstVisitExp          = 6
	revisitExp             = 3
)

var visitNow = func() time.Time { return time.Now().UTC() }

type visitRequest struct {
	Arcade   string  `json:"arcade"`
	Lat      float64 `json:"lat"`
	Lon      float64 `json:"lon"`
	Accuracy float64 `json:"accuracy"`
}
type VisitStats struct {
	TotalVisits     int                `json:"total_visits"`
	DistinctArcades int                `json:"distinct_arcades"`
	LastVisitedAt   string             `json:"last_visited_at,omitempty"`
	Arcades         []ArcadeVisitCount `json:"arcades"`
}
type ArcadeVisitCount struct {
	Arcade     string `json:"arcade"`
	VisitCount int    `json:"visit_count"`
}
type VisitSummary struct {
	ID             string  `json:"id"`
	Arcade         string  `json:"arcade"`
	VisitDay       string  `json:"visit_day"`
	VisitedAt      string  `json:"visited_at"`
	DistanceMeters float64 `json:"distance_meters"`
	AccuracyMeters float64 `json:"accuracy_meters"`
	GainedExp      int     `json:"gained_exp"`
}

func SetVisitNowForTest(nowFn func() time.Time) func() {
	prev := visitNow
	visitNow = nowFn
	return func() { visitNow = prev }
}

func VisitArcade(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	}
	var in visitRequest
	if err := json.NewDecoder(re.Request.Body).Decode(&in); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
	}
	in.Arcade = strings.TrimSpace(in.Arcade)
	if in.Arcade == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}
	if !validVisitCoords(in.Lat, in.Lon) || math.IsNaN(in.Accuracy) || in.Accuracy < 0 || in.Accuracy > maxVisitAccuracyMeters {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid visit location or accuracy"})
	}
	arcade, err := re.App.FindRecordById("arcade", in.Arcade)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}
	if !arcade.GetBool("public") || arcade.GetBool("closed") {
		return re.JSON(http.StatusForbidden, map[string]any{"error": "arcade is not eligible for visits"})
	}
	basicID := strings.TrimSpace(arcade.GetString("basic"))
	if basicID == "" {
		return re.JSON(http.StatusConflict, map[string]any{"error": "arcade location unavailable"})
	}
	basic, err := re.App.FindRecordById("arcade_basic", basicID)
	if err != nil {
		return re.JSON(http.StatusConflict, map[string]any{"error": "arcade location unavailable"})
	}
	lat, lon, ok := readVisitLocation(basic.Get("location"))
	if !ok {
		return re.JSON(http.StatusConflict, map[string]any{"error": "arcade location unavailable"})
	}
	distance := visitDistanceMeters(in.Lat, in.Lon, lat, lon)
	if distance > visitRadiusMeters {
		return re.JSON(http.StatusForbidden, map[string]any{"error": "outside visit radius", "distance_meters": distance})
	}
	loc, err := time.LoadLocation(strings.TrimSpace(arcade.GetString("timezone")))
	if err != nil {
		return re.JSON(http.StatusConflict, map[string]any{"error": "arcade timezone unavailable"})
	}
	visitDay := visitNow().In(loc).Format("2006-01-02")
	baseExp, err := LoadCurrentExp(re.App, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load current exp"})
	}
	var out VisitSummary
	var exp int
	var granted bool
	err = re.App.RunInTransaction(func(tx core.App) error {
		existing, err := tx.FindRecordsByFilter(CollectionArcadeVisit, "user={:user} && arcade={:arcade} && visit_day={:day}", "", 1, 0, dbx.Params{"user": re.Auth.Id, "arcade": in.Arcade, "day": visitDay})
		if err != nil {
			return err
		}
		if len(existing) > 0 {
			out = visitSummary(existing[0])
			exp, err = LoadCurrentExp(tx, re.Auth.Id)
			return err
		}
		prior, err := tx.FindRecordsByFilter(CollectionArcadeVisit, "user={:user} && arcade={:arcade}", "", 1, 0, dbx.Params{"user": re.Auth.Id, "arcade": in.Arcade})
		if err != nil {
			return err
		}
		coll, err := tx.FindCollectionByNameOrId(CollectionArcadeVisit)
		if err != nil {
			return err
		}
		rec := core.NewRecord(coll)
		rec.Set("user", re.Auth.Id)
		rec.Set("arcade", in.Arcade)
		rec.Set("visit_day", visitDay)
		rec.Set("visited_at", visitNow().UTC().Format(time.RFC3339Nano))
		rec.Set("distance_meters", distance)
		rec.Set("accuracy_meters", in.Accuracy)
		gain := revisitExp
		if len(prior) == 0 {
			gain = firstVisitExp
		}
		rec.Set("gained_exp", gain)
		if err := tx.Save(rec); err != nil {
			return err
		}
		exp, granted, err = AwardExpTx(tx, re.Auth.Id, ArcadeVisitKind(rec.Id), gain, baseExp)
		if err != nil {
			return err
		}
		if !granted {
			return fmt.Errorf("visit xp was not granted")
		}
		out = visitSummary(rec)
		return nil
	})
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "visit verification failed", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, map[string]any{"visited": granted, "already_visited": !granted, "visit": out, "gained_exp": func() int {
		if granted {
			return out.GainedExp
		}
		return 0
	}(), "exp": exp, "level": LevelFromExp(exp), "xp_feedback": BuildExpFeedback(baseExp, exp)})
}

func GetMyVisits(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	}
	visits, err := ListVisits(re.App, re.Auth.Id, 0, 0)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load visits"})
	}
	stats, err := LoadVisitStats(re.App, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load visit stats"})
	}
	return re.JSON(http.StatusOK, map[string]any{"visits": visits, "stats": stats})
}
func GetArcadeVisitStats(re *core.RequestEvent) error {
	id := strings.TrimSpace(re.Request.URL.Query().Get("arcade"))
	if id == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade is required"})
	}
	a, err := re.App.FindRecordById("arcade", id)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}
	if !a.GetBool("public") || a.GetBool("closed") {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}
	stats, err := LoadArcadeVisitStats(re.App, id)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load arcade visit stats"})
	}
	return re.JSON(http.StatusOK, stats)
}
func UpdateVisitVisibility(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	}
	var body struct {
		Visibility string `json:"visit_visibility"`
	}
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
	}
	v := visitVisibility(body.Visibility)
	if strings.TrimSpace(body.Visibility) != v {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid visit_visibility"})
	}
	rec, err := re.App.FindRecordById(CollectionUserInfo, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusConflict, map[string]any{"error": "user_info is required"})
	}
	rec.Set("visit_visibility", v)
	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update visit visibility"})
	}
	return re.JSON(http.StatusOK, map[string]any{"visit_visibility": v})
}
func visitVisibility(v string) string {
	switch strings.TrimSpace(v) {
	case "private", "summary", "full":
		return strings.TrimSpace(v)
	default:
		return "summary"
	}
}
func visitSummary(r *core.Record) VisitSummary {
	return VisitSummary{ID: r.Id, Arcade: r.GetString("arcade"), VisitDay: r.GetString("visit_day"), VisitedAt: r.GetString("visited_at"), DistanceMeters: r.GetFloat("distance_meters"), AccuracyMeters: r.GetFloat("accuracy_meters"), GainedExp: r.GetInt("gained_exp")}
}
func ListVisits(app core.App, userID string, limit, offset int) ([]VisitSummary, error) {
	recs, err := app.FindRecordsByFilter(CollectionArcadeVisit, "user={:user}", "-visited_at", limit, offset, dbx.Params{"user": userID})
	if err != nil {
		return nil, err
	}
	out := make([]VisitSummary, 0, len(recs))
	for _, r := range recs {
		out = append(out, visitSummary(r))
	}
	return out, nil
}
func LoadVisitStats(app core.App, userID string) (VisitStats, error) {
	s := VisitStats{Arcades: []ArcadeVisitCount{}}
	err := app.DB().NewQuery("SELECT COUNT(*), COUNT(DISTINCT arcade), COALESCE(MAX(visited_at), '') FROM arcade_visit WHERE user={:user}").Bind(dbx.Params{"user": userID}).Row(&s.TotalVisits, &s.DistinctArcades, &s.LastVisitedAt)
	if err != nil {
		return s, err
	}
	rows, err := app.DB().NewQuery(`
SELECT arcade, COUNT(*)
FROM arcade_visit
WHERE user = {:user}
GROUP BY arcade
ORDER BY COUNT(*) DESC, arcade ASC
`).Bind(dbx.Params{"user": userID}).Rows()
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ArcadeVisitCount
		if err := rows.Scan(&item.Arcade, &item.VisitCount); err != nil {
			return s, err
		}
		s.Arcades = append(s.Arcades, item)
	}
	return s, rows.Err()
}
func LoadArcadeVisitStats(app core.App, arcadeID string) (map[string]any, error) {
	var total, users int
	err := app.DB().NewQuery("SELECT COUNT(*), COUNT(DISTINCT user) FROM arcade_visit WHERE arcade={:arcade}").Bind(dbx.Params{"arcade": arcadeID}).Row(&total, &users)
	return map[string]any{"arcade": arcadeID, "total_visits": total, "distinct_visitors": users}, err
}
func parseVisitPagination(raw string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.Atoi(raw)
}

func validVisitCoords(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180 && lat != 0 && lon != 0
}
func readVisitLocation(v any) (float64, float64, bool) {
	switch p := v.(type) {
	case pbtypes.GeoPoint:
		return p.Lat, p.Lon, true
	case *pbtypes.GeoPoint:
		if p != nil {
			return p.Lat, p.Lon, true
		}
	case map[string]any:
		lat, ok1 := visitFloat(p["lat"])
		if !ok1 {
			lat, ok1 = visitFloat(p["latitude"])
		}
		lon, ok2 := visitFloat(p["lon"])
		if !ok2 {
			lon, ok2 = visitFloat(p["longitude"])
		}
		return lat, lon, ok1 && ok2
	case string:
		var point map[string]any
		if json.Unmarshal([]byte(p), &point) == nil {
			return readVisitLocation(point)
		}
	}
	return 0, 0, false
}

func visitFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case json.Number:
		f, e := n.Float64()
		return f, e == nil
	case string:
		f, e := strconv.ParseFloat(n, 64)
		return f, e == nil
	default:
		return 0, false
	}
}
func visitDistanceMeters(lat1, lon1, lat2, lon2 float64) float64 {
	toRad := func(v float64) float64 { return v * math.Pi / 180 }
	dLat, dLon := toRad(lat2-lat1), toRad(lon2-lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 6371000 * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}
