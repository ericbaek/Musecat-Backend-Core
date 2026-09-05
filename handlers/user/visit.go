package user

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
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
	Cities              []PassportCity     `json:"cities"`
	DistinctCities      int                `json:"distinct_cities"`
	UnclassifiedArcades int                `json:"unclassified_arcades"`
	TotalVisits         int                `json:"total_visits"`
	DistinctArcades     int                `json:"distinct_arcades"`
	TotalDistanceMeters float64            `json:"total_distance_meters"`
	Countries           []VisitCountry     `json:"countries"`
	Arcades             []ArcadeVisitCount `json:"arcades"`
}

type VisitCountry struct {
	Country     string `json:"country"`
	ArcadeCount int    `json:"arcade_count"`
}

type ArcadeVisitCount struct {
	Arcade       string   `json:"arcade"`
	Name         string   `json:"name"`
	Country      string   `json:"country"`
	PhotoURL     string   `json:"photo_url,omitempty"`
	VisitCount   int      `json:"visit_count"`
	LastVisitDay string   `json:"last_visit_day"`
	VisitDays    []string `json:"visit_days,omitempty"`

	lastVisitedAt string
}
type VisitSummary struct {
	Arcade   string `json:"arcade"`
	VisitDay string `json:"visit_day"`

	gainedExp int
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
	var firstVisit bool
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
			firstVisit = true
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
			eligible, err := IsExpEligible(tx, re.Auth.Id)
			if err != nil {
				return err
			}
			if eligible {
				return fmt.Errorf("visit xp was not granted")
			}
			rec.Set("gained_exp", 0)
			if err := tx.Save(rec); err != nil {
				return err
			}
			granted = true
		}
		out = visitSummary(rec)
		return nil
	})
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "visit verification failed", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, map[string]any{"first_visit_to_arcade": firstVisit, "visited": granted, "already_visited": !granted, "visit": out, "gained_exp": func() int {
		if granted {
			return out.gainedExp
		}
		return 0
	}(), "exp": exp, "level": LevelFromExp(exp), "xp_feedback": BuildExpFeedback(baseExp, exp)})
}

func GetMyVisits(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	}
	stats, err := LoadVisitStats(re.App, re.Auth.Id, true)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load visit stats"})
	}
	return re.JSON(http.StatusOK, map[string]any{"stats": stats})
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
	return VisitSummary{Arcade: r.GetString("arcade"), VisitDay: r.GetString("visit_day"), gainedExp: r.GetInt("gained_exp")}
}
func LoadVisitStats(app core.App, userID string, includeVisitDays bool) (VisitStats, error) {
	p, err := LoadPassport(app, userID, "all")
	stats := VisitStats{TotalVisits: p.TotalVisits, DistinctArcades: p.DistinctArcades, TotalDistanceMeters: p.TotalDistanceMeters, Cities: p.Cities, DistinctCities: p.DistinctCities, UnclassifiedArcades: p.UnclassifiedArcades, Countries: []VisitCountry{}, Arcades: []ArcadeVisitCount{}}
	if err != nil {
		return stats, err
	}
	for _, c := range p.Countries {
		stats.Countries = append(stats.Countries, VisitCountry{Country: c.Country, ArcadeCount: c.ArcadeCount})
	}
	for _, s := range p.Stamps {
		item := ArcadeVisitCount{Arcade: s.Arcade, Name: s.Name, Country: s.Country, PhotoURL: s.PhotoURL, VisitCount: s.VisitCount, LastVisitDay: s.LastVisitDay}
		if includeVisitDays {
			item.VisitDays = s.days
		}
		stats.Arcades = append(stats.Arcades, item)
	}
	return stats, nil
}

func visitArcadePhotoURL(app core.App, arcadeID, photoMoleculeID string) string {
	if strings.TrimSpace(arcadeID) == "" || strings.TrimSpace(photoMoleculeID) == "" {
		return ""
	}
	photoMolecule, err := app.FindRecordById("arcade_photo", photoMoleculeID)
	if err != nil || photoMolecule.GetString("arcade") != arcadeID {
		return ""
	}
	for _, rawAtomID := range photoMolecule.GetStringSlice("photos") {
		atomID := strings.TrimSpace(rawAtomID)
		if atomID == "" {
			continue
		}
		atom, err := app.FindRecordById("arcade_photo_atoms", atomID)
		if err != nil || atom.GetString("arcade") != arcadeID || !atom.GetBool("public") || strings.TrimSpace(atom.GetString("photo")) == "" {
			continue
		}
		return "/arcade/photo/file?id=" + url.QueryEscape(atom.Id)
	}
	return ""
}

func LoadArcadeVisitStats(app core.App, arcadeID string) (map[string]any, error) {
	var total, users int
	err := app.DB().NewQuery("SELECT COUNT(*), COUNT(DISTINCT user) FROM arcade_visit WHERE arcade={:arcade}").Bind(dbx.Params{"arcade": arcadeID}).Row(&total, &users)
	return map[string]any{"arcade": arcadeID, "total_visits": total, "distinct_visitors": users}, err
}
func slicesReverse(values []string) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
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
