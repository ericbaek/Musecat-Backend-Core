package user

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

type PassportCity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Country     string `json:"country"`
	Admin1      string `json:"admin1"`
	Location    any    `json:"location"`
	ArcadeCount int    `json:"arcade_count"`
	VisitCount  int    `json:"visit_count"`
}
type PassportCountry struct {
	Country     string `json:"country"`
	ArcadeCount int    `json:"arcade_count"`
	VisitCount  int    `json:"visit_count"`
}
type PassportStamp struct {
	Arcade          string   `json:"arcade"`
	Name            string   `json:"name"`
	Country         string   `json:"country"`
	CityID          string   `json:"city_id"`
	CityName        string   `json:"city_name"`
	Closed          bool     `json:"closed"`
	PhotoURL        string   `json:"photo_url,omitempty"`
	FirstVisitDay   string   `json:"first_visit_day,omitempty"`
	LastVisitDay    string   `json:"last_visit_day,omitempty"`
	VisitDates      []string `json:"visit_dates,omitempty"`
	VisitCount      int      `json:"visit_count"`
	TotalVisitCount int      `json:"total_visit_count"`
	lastAt          string
	firstAt         string
	photoID         string
	days            []string
}
type PassportMonth struct {
	Month      string `json:"month"`
	Visits     int    `json:"visits"`
	NewArcades int    `json:"new_arcades"`
}
type PassportDay struct {
	Weekday int `json:"weekday"` // ISO Monday=1
	Visits  int `json:"visits"`
}
type Passport struct {
	Visibility           string            `json:"visibility"`
	CityCatalogAvailable bool              `json:"city_catalog_available"`
	Year                 string            `json:"year"`
	AvailableYears       []int             `json:"available_years"`
	TotalVisits          int               `json:"total_visits"`
	DistinctArcades      int               `json:"distinct_arcades"`
	DistinctCities       int               `json:"distinct_cities"`
	DistinctCountries    int               `json:"distinct_countries"`
	NewArcades           int               `json:"new_arcades"`
	VisitDays            int               `json:"visit_days"`
	MaxArcadesInDay      int               `json:"max_arcades_in_day"`
	TotalDistanceMeters  float64           `json:"total_distance_meters"`
	UnclassifiedArcades  int               `json:"unclassified_arcades"`
	Countries            []PassportCountry `json:"countries"`
	Cities               []PassportCity    `json:"cities"`
	Months               []PassportMonth   `json:"months"`
	Weekdays             []PassportDay     `json:"weekdays"`
	TopArcades           []PassportStamp   `json:"top_arcades"`
	Stamps               []PassportStamp   `json:"-"`
}

type passportRow struct {
	arcade, name, country, photo, day, at, cityID, cityName, admin1 string
	closed                                                          bool
	location, cityLocation                                          sql.NullString
}

// LoadPassport is the single aggregation source for private details and public summaries.
// It never queries a geo provider and only includes currently public arcades.
func LoadPassport(app core.App, userID, year string) (Passport, error) {
	out := Passport{Visibility: "owner", Year: year, AvailableYears: []int{}, Countries: []PassportCountry{}, Cities: []PassportCity{}, Months: []PassportMonth{}, Weekdays: []PassportDay{}, TopArcades: []PassportStamp{}, Stamps: []PassportStamp{}}
	rows, err := app.DB().NewQuery(`SELECT v.arcade, COALESCE(b.name,''), a.country, a.photo, a.closed,
 v.visit_day, v.visited_at, b.location, COALESCE(c.id,''), COALESCE(c.name,''), COALESCE(c.admin1,''), c.location
 FROM arcade_visit v JOIN arcade a ON a.id=v.arcade LEFT JOIN arcade_basic b ON b.id=a.basic
 LEFT JOIN passport_city c ON c.id=b.city_id AND c.country=a.country
 WHERE v.user={:user} AND a.public=true ORDER BY v.visited_at ASC, v.id ASC`).Bind(dbx.Params{"user": userID}).Rows()
	if err != nil {
		return out, err
	}
	records := []passportRow{}
	for rows.Next() {
		var r passportRow
		if err = rows.Scan(&r.arcade, &r.name, &r.country, &r.photo, &r.closed, &r.day, &r.at, &r.location, &r.cityID, &r.cityName, &r.admin1, &r.cityLocation); err != nil {
			rows.Close()
			return out, err
		}
		records = append(records, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	if err := app.DB().NewQuery("SELECT EXISTS(SELECT 1 FROM passport_city)").Row(&out.CityCatalogAvailable); err != nil {
		return out, err
	}
	stamps := map[string]*PassportStamp{}
	cities := map[string]*PassportCity{}
	countries := map[string]*PassportCountry{}
	months := map[string]*PassportMonth{}
	years := map[int]bool{}
	days := map[string]map[string]bool{}
	weekdays := [7]int{}
	var prevLat, prevLon float64
	prevValid := false
	for _, r := range records {
		day, e := time.Parse("2006-01-02", r.day)
		if e != nil {
			return out, fmt.Errorf("invalid stored visit day")
		}
		years[day.Year()] = true
		stamp, exists := stamps[r.arcade]
		if !exists {
			stamp = &PassportStamp{Arcade: r.arcade, Name: r.name, Country: r.country, CityID: r.cityID, CityName: r.cityName, Closed: r.closed, FirstVisitDay: r.day, firstAt: r.at, photoID: r.photo, days: []string{}}
			stamps[r.arcade] = stamp
		}
		stamp.TotalVisitCount++
		if r.day < stamp.FirstVisitDay {
			stamp.FirstVisitDay = r.day
		}
		selected := year == "all" || strconv.Itoa(day.Year()) == year
		lat, lon, valid := readVisitLocation(r.location.String)
		valid = valid && r.location.Valid
		if selected && valid && prevValid {
			out.TotalDistanceMeters += visitDistanceMeters(prevLat, prevLon, lat, lon)
		}
		prevLat, prevLon, prevValid = lat, lon, valid && selected
		if !selected {
			continue
		}
		firstInPeriod := stamp.VisitCount == 0
		stamp.VisitCount++
		stamp.lastAt = r.at
		if r.day > stamp.LastVisitDay {
			stamp.LastVisitDay = r.day
		}
		stamp.days = append(stamp.days, r.day)
		out.TotalVisits++
		if days[r.day] == nil {
			days[r.day] = map[string]bool{}
		}
		days[r.day][r.arcade] = true
		weekdays[(int(day.Weekday())+6)%7]++
		month := day.Format("2006-01")
		if months[month] == nil {
			months[month] = &PassportMonth{Month: month}
		}
		months[month].Visits++
		if !exists {
			out.NewArcades++
			months[month].NewArcades++
		}
		if r.country != "" {
			if countries[r.country] == nil {
				countries[r.country] = &PassportCountry{Country: r.country}
			}
			countries[r.country].VisitCount++
			if firstInPeriod {
				countries[r.country].ArcadeCount++
			}
		}
		if r.cityID != "" {
			if cities[r.cityID] == nil {
				var loc any
				if a, b, ok := readVisitLocation(r.cityLocation.String); ok {
					loc = map[string]float64{"lat": a, "lon": b}
				}
				cities[r.cityID] = &PassportCity{ID: r.cityID, Name: r.cityName, Country: r.country, Admin1: r.admin1, Location: loc}
			}
			cities[r.cityID].VisitCount++
			if firstInPeriod {
				cities[r.cityID].ArcadeCount++
			}
		} else if firstInPeriod {
			out.UnclassifiedArcades++
		}
	}
	for y := range years {
		out.AvailableYears = append(out.AvailableYears, y)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(out.AvailableYears)))
	for _, stamp := range stamps {
		if stamp.VisitCount > 0 {
			stamp.PhotoURL = visitArcadePhotoURL(app, stamp.Arcade, stamp.photoID)
			sort.Sort(sort.Reverse(sort.StringSlice(stamp.days)))
			out.Stamps = append(out.Stamps, *stamp)
		}
	}
	sortPassportStamps(out.Stamps, "visits")
	for i := 0; i < len(out.Stamps) && i < 5; i++ {
		out.TopArcades = append(out.TopArcades, out.Stamps[i])
	}
	for _, c := range cities {
		out.Cities = append(out.Cities, *c)
	}
	sort.Slice(out.Cities, func(i, j int) bool {
		a, b := out.Cities[i], out.Cities[j]
		if a.ArcadeCount != b.ArcadeCount {
			return a.ArcadeCount > b.ArcadeCount
		}
		return a.ID < b.ID
	})
	for _, c := range countries {
		out.Countries = append(out.Countries, *c)
	}
	sort.Slice(out.Countries, func(i, j int) bool {
		a, b := out.Countries[i], out.Countries[j]
		if a.ArcadeCount != b.ArcadeCount {
			return a.ArcadeCount > b.ArcadeCount
		}
		return a.Country < b.Country
	})
	// Keep empty months in the selected year (or every recorded year).
	monthYears := out.AvailableYears
	if year != "all" {
		y, _ := strconv.Atoi(year)
		monthYears = []int{y}
	}
	for _, y := range monthYears {
		for m := 1; m <= 12; m++ {
			key := fmt.Sprintf("%04d-%02d", y, m)
			if months[key] == nil {
				months[key] = &PassportMonth{Month: key}
			}
		}
	}
	for _, m := range months {
		out.Months = append(out.Months, *m)
	}
	sort.Slice(out.Months, func(i, j int) bool { return out.Months[i].Month < out.Months[j].Month })
	for i, v := range weekdays {
		out.Weekdays = append(out.Weekdays, PassportDay{Weekday: i + 1, Visits: v})
	}
	out.DistinctArcades = len(out.Stamps)
	out.DistinctCities = len(cities)
	out.DistinctCountries = len(countries)
	out.VisitDays = len(days)
	for _, d := range days {
		if len(d) > out.MaxArcadesInDay {
			out.MaxArcadesInDay = len(d)
		}
	}
	return out, nil
}

func sortPassportStamps(items []PassportStamp, order string) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		if order == "first" && a.firstAt != b.firstAt {
			return a.firstAt < b.firstAt
		}
		if order == "visits" && a.VisitCount != b.VisitCount {
			return a.VisitCount > b.VisitCount
		}
		if a.lastAt != b.lastAt {
			return a.lastAt > b.lastAt
		}
		return a.Arcade < b.Arcade
	})
}
func passportYear(re *core.RequestEvent) (string, error) {
	year := re.Request.URL.Query().Get("year")
	if year == "" || year == "all" {
		return "all", nil
	}
	y, err := strconv.Atoi(year)
	if err != nil || len(year) != 4 || y < 1 || strconv.Itoa(y) != year {
		return "", fmt.Errorf("year must be all or YYYY")
	}
	return year, nil
}

// passportAudience resolves identity before aggregation. An explicit user permits public
// reads; omitted user always means the authenticated owner, never a public fallback.
func passportAudience(re *core.RequestEvent) (string, string, int, error) {
	id := strings.TrimSpace(re.Request.URL.Query().Get("user"))
	if id == "" {
		if re.Auth == nil {
			return "", "", 401, errors.New("authentication required")
		}
		id = re.Auth.Id
	}
	if re.Auth != nil && re.Auth.Id == id {
		message, code, err := checkArcadeWriteRestriction(re.App, re.Auth, userBanNow())
		if err != nil {
			return "", "", 502, errors.New("failed to verify account")
		}
		if code != "" {
			return "", "", 403, errors.New(message)
		}
		return id, "owner", 0, nil
	}
	user, err := re.App.FindRecordById("user", id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", 404, errors.New("passport not found")
	}
	if err != nil {
		return "", "", 502, errors.New("failed to load passport")
	}
	if user.GetBool("withdrawn") {
		return "", "", 404, errors.New("passport not found")
	}
	info, err := re.App.FindRecordById(CollectionUserInfo, id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", 404, errors.New("passport not found")
	}
	if err != nil {
		return "", "", 502, errors.New("failed to load passport")
	}
	visibility := visitVisibility(info.GetString("visit_visibility"))
	if visibility == "private" {
		return "", "", 404, errors.New("passport not found")
	}
	return id, visibility, 0, nil
}
func projectPassportStamp(stamp PassportStamp, visibility string) PassportStamp {
	if visibility == "summary" {
		stamp.FirstVisitDay = ""
		stamp.LastVisitDay = ""
		stamp.VisitDates = nil
	} else {
		stamp.VisitDates = append([]string(nil), stamp.days...)
	}
	return stamp
}
func GetMyPassport(re *core.RequestEvent) error {
	id, visibility, status, err := passportAudience(re)
	if err != nil {
		return re.JSON(status, map[string]any{"error": err.Error()})
	}
	year, err := passportYear(re)
	if err != nil {
		return re.JSON(400, map[string]any{"error": err.Error()})
	}
	out, err := LoadPassport(re.App, id, year)
	if err != nil {
		return re.JSON(502, map[string]any{"error": "failed to load passport"})
	}
	out.Visibility = visibility
	for i, s := range out.TopArcades {
		out.TopArcades[i] = projectPassportStamp(s, visibility)
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(200, out)
}
func GetMyPassportStamps(re *core.RequestEvent) error {
	id, visibility, status, err := passportAudience(re)
	if err != nil {
		return re.JSON(status, map[string]any{"error": err.Error()})
	}
	year, err := passportYear(re)
	if err != nil {
		return re.JSON(400, map[string]any{"error": err.Error()})
	}
	q := re.Request.URL.Query()
	order := q.Get("sort")
	if order == "" {
		order = "recent"
	}
	if order != "recent" && order != "first" && order != "visits" {
		return re.JSON(400, map[string]any{"error": "invalid sort"})
	}
	page, per := 1, 24
	for key, dest := range map[string]*int{"page": &page, "per_page": &per} {
		if q.Get(key) != "" {
			v, e := strconv.Atoi(q.Get(key))
			if e != nil || v < 1 || v > 1000000 {
				return re.JSON(400, map[string]any{"error": "invalid pagination"})
			}
			*dest = v
		}
	}
	if per > 100 {
		return re.JSON(400, map[string]any{"error": "per_page must be at most 100"})
	}
	country := q.Get("country")
	if country != "" && (len(country) != 2 || strings.Trim(country, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "") {
		return re.JSON(400, map[string]any{"error": "invalid country"})
	}
	out, err := LoadPassport(re.App, id, year)
	if err != nil {
		return re.JSON(502, map[string]any{"error": "failed to load stamps"})
	}
	items := []PassportStamp{}
	for _, s := range out.Stamps {
		if (country == "" || country == s.Country) && (q.Get("city") == "" || q.Get("city") == s.CityID) {
			items = append(items, s)
		}
	}
	sortPassportStamps(items, order)
	total := len(items)
	last := (total + per - 1) / per
	if last < 1 {
		last = 1
	}
	start := min((page-1)*per, total)
	end := min(start+per, total)
	re.Response.Header().Set("Cache-Control", "private, no-store")
	for i := start; i < end; i++ {
		items[i] = projectPassportStamp(items[i], visibility)
	}
	return re.JSON(200, map[string]any{"visibility": visibility, "page": page, "per_page": per, "last_page": last, "total": total, "items": items[start:end]})
}
