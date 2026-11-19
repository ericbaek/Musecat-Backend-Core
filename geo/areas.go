package geo

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Area classification statuses stored on arcades.
const (
	AreaResolved  = "resolved"  // inside exactly one unit
	AreaNear      = "near"      // outside every unit but within AreaNearMeters of one (coast, reclaimed land)
	AreaEstimated = "estimated" // country without city boundaries; city is the nearest seat
	AreaAmbiguous = "ambiguous" // inside units of different cities (overlapping sources)
	AreaUnmapped  = "unmapped"  // nothing within reach; needs review
)

// AreaNearMeters is how far outside the simplified coastline an arcade may lie
// and still take the nearest unit. Reclaimed land (Caofeidian, PIK) needs ~5km.
const AreaNearMeters = 5000

// seatReachMeters bounds the nearest-seat fallback for countries without city
// boundaries so a remote arcade is not credited to a far capital.
const seatReachMeters = 60000

// AreaCity is one Passport city. Boundary cities are unions of units; seat
// cities (Estimated) stand in for countries without city boundaries.
type AreaCity struct {
	Key        string            `json:"-"`
	Country    string            `json:"country"`
	Admin1     string            `json:"admin1"`
	Names      map[string]string `json:"names"`
	Lat        float64           `json:"lat"`
	Lon        float64           `json:"lon"`
	GeoNamesID string            `json:"geonames_id"`
	Estimated  bool              `json:"estimated"`
}

// Name returns the display name for locale, falling back to English and then
// the local name from the boundary source.
func (c AreaCity) Name(locale string) string {
	for _, key := range []string{strings.ToLower(locale), "en", "local"} {
		if name := strings.TrimSpace(c.Names[key]); name != "" {
			return name
		}
	}
	return ""
}

// AreaResult is the classification of one coordinate.
type AreaResult struct {
	Admin1         string
	Admin2         string
	Admin2Name     string
	City           string
	Status         string
	DistanceMeters float64
}

// AreaAdmin1 is one first-level area with display names and the aliases an
// address may use for it.
type AreaAdmin1 struct {
	Code    string            `json:"-"`
	Country string            `json:"country"`
	Names   map[string]string `json:"names"`
	Aliases []string          `json:"aliases"`
}

// Name returns the display name for locale, falling back to English and then
// the local name.
func (a AreaAdmin1) Name(locale string) string {
	return AreaCity{Names: a.Names}.Name(locale)
}

// flat float32 rings keep memory at eight bytes per vertex; the source data
// is rounded to 1e-5 degrees (about 1 m), well inside float32 precision.
type areaPolygon struct {
	rings                          [][]float32 // lon,lat pairs; rings[0] is the exterior
	minLon, minLat, maxLon, maxLat float64
}

type areaUnit struct {
	country, admin1, admin2, name, city string
	polygons                            []areaPolygon
	minLon, minLat, maxLon, maxLat      float64
}

const areaGridDegrees = 0.5

type areaGridKey struct{ x, y int32 }

type areaAlias struct {
	text   string // lower-cased
	admin1 string
	latin  bool // matched on word boundaries
}

type areaCountry struct {
	units   []areaUnit
	grid    map[areaGridKey][]int32
	seats   []string
	aliases []areaAlias
}

// Areas is the immutable, embedded admin-area catalog. It is safe for
// concurrent use.
type Areas struct {
	version   string
	whole     map[string]string // country → its single city key
	countries map[string]*areaCountry
	cities    map[string]AreaCity
	admin1    map[string]AreaAdmin1
	renames   map[string]string
}

//go:embed data/admin_areas.geojson.gz
var embeddedAdminAreas []byte

var (
	areasOnce sync.Once
	areas     *Areas
	areasErr  error
)

// EmbeddedAreas loads the admin-area catalog shipped with the binary once.
func EmbeddedAreas() (*Areas, error) {
	areasOnce.Do(func() {
		var reader *gzip.Reader
		reader, areasErr = gzip.NewReader(bytes.NewReader(embeddedAdminAreas))
		if areasErr != nil {
			return
		}
		defer reader.Close()
		areas, areasErr = DecodeAreas(reader)
	})
	return areas, areasErr
}

type areaFile struct {
	Type         string                `json:"type"`
	Version      string                `json:"version"`
	WholeCountry []string              `json:"whole_country"`
	Renames      map[string]string     `json:"renames"`
	Admin1       map[string]AreaAdmin1 `json:"admin1"`
	Cities       map[string]AreaCity   `json:"cities"`
	Features     []struct {
		Properties struct {
			Country string `json:"country"`
			Admin1  string `json:"admin1"`
			Admin2  string `json:"admin2"`
			Name    string `json:"name"`
			City    string `json:"city"`
		} `json:"properties"`
		Geometry geoJSONGeometry `json:"geometry"`
	} `json:"features"`
}

// DecodeAreas parses an admin-area FeatureCollection built by
// geo/tools/areas/build.mjs.
func DecodeAreas(r io.Reader) (*Areas, error) {
	var file areaFile
	if err := json.NewDecoder(r).Decode(&file); err != nil {
		return nil, fmt.Errorf("decode admin areas: %w", err)
	}
	if file.Type != "FeatureCollection" || len(file.Features) == 0 {
		return nil, errors.New("admin areas must be a non-empty FeatureCollection")
	}
	if file.Version == "" {
		return nil, errors.New("admin areas have no version")
	}
	out := &Areas{version: file.Version, whole: map[string]string{}, countries: map[string]*areaCountry{}, cities: map[string]AreaCity{}, admin1: map[string]AreaAdmin1{}, renames: map[string]string{}}
	whole := map[string]bool{}
	for _, country := range file.WholeCountry {
		whole[strings.ToUpper(country)] = true
	}
	for code, admin1 := range file.Admin1 {
		admin1.Code = code
		out.admin1[code] = admin1
		c := out.country(admin1.Country)
		for _, alias := range admin1.Aliases {
			text := strings.ToLower(strings.TrimSpace(alias))
			if text != "" {
				c.aliases = append(c.aliases, areaAlias{text: text, admin1: code, latin: isLatin(text)})
			}
		}
	}
	for key, city := range file.Cities {
		city.Key = key
		out.cities[key] = city
		if city.Estimated {
			c := out.country(city.Country)
			c.seats = append(c.seats, key)
		}
	}
	for i, feature := range file.Features {
		p := feature.Properties
		if p.Country == "" || p.Admin1 == "" {
			return nil, fmt.Errorf("admin area %d has no country or admin1", i)
		}
		if p.City != "" {
			if _, ok := out.cities[p.City]; !ok {
				return nil, fmt.Errorf("admin area %d names unknown city %q", i, p.City)
			}
		}
		if whole[p.Country] {
			out.whole[p.Country] = p.City
		}
		unit := areaUnit{country: p.Country, admin1: p.Admin1, admin2: p.Admin2, name: p.Name, city: p.City}
		polygons, err := decodeAreaPolygons(feature.Geometry)
		if err != nil {
			return nil, fmt.Errorf("admin area %d: %w", i, err)
		}
		unit.polygons = polygons
		unit.minLon, unit.minLat, unit.maxLon, unit.maxLat = math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
		for _, polygon := range polygons {
			unit.minLon, unit.minLat = math.Min(unit.minLon, polygon.minLon), math.Min(unit.minLat, polygon.minLat)
			unit.maxLon, unit.maxLat = math.Max(unit.maxLon, polygon.maxLon), math.Max(unit.maxLat, polygon.maxLat)
		}
		c := out.country(p.Country)
		index := int32(len(c.units))
		c.units = append(c.units, unit)
		for x := gridCell(unit.minLon); x <= gridCell(unit.maxLon); x++ {
			for y := gridCell(unit.minLat); y <= gridCell(unit.maxLat); y++ {
				key := areaGridKey{x, y}
				c.grid[key] = append(c.grid[key], index)
			}
		}
	}
	for country := range whole {
		if out.whole[country] == "" {
			return nil, fmt.Errorf("whole-country %s has no city", country)
		}
	}
	for from, to := range file.Renames {
		if _, ok := out.cities[to]; !ok {
			return nil, fmt.Errorf("rename %q → unknown city %q", from, to)
		}
		out.renames[from] = to
	}
	for _, c := range out.countries {
		sort.Strings(c.seats)
		// Longest first, so 東京都 wins over 京都 at the same position.
		sort.SliceStable(c.aliases, func(i, j int) bool { return len(c.aliases[i].text) > len(c.aliases[j].text) })
	}
	return out, nil
}

func isLatin(text string) bool {
	for _, r := range text {
		if r > unicode.MaxLatin1 && !unicode.Is(unicode.Latin, r) {
			return false
		}
	}
	return true
}

func (a *Areas) country(code string) *areaCountry {
	c := a.countries[code]
	if c == nil {
		c = &areaCountry{grid: map[areaGridKey][]int32{}}
		a.countries[code] = c
	}
	return c
}

func gridCell(degrees float64) int32 { return int32(math.Floor(degrees / areaGridDegrees)) }

func decodeAreaPolygons(geometry geoJSONGeometry) ([]areaPolygon, error) {
	var nested [][][][]float64
	switch geometry.Type {
	case "Polygon":
		var rings [][][]float64
		if err := json.Unmarshal(geometry.Coordinates, &rings); err != nil {
			return nil, err
		}
		nested = [][][][]float64{rings}
	case "MultiPolygon":
		if err := json.Unmarshal(geometry.Coordinates, &nested); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported geometry %q", geometry.Type)
	}
	out := make([]areaPolygon, 0, len(nested))
	for _, rings := range nested {
		if len(rings) == 0 || len(rings[0]) < 3 {
			continue
		}
		polygon := areaPolygon{rings: make([][]float32, len(rings)), minLon: math.Inf(1), minLat: math.Inf(1), maxLon: math.Inf(-1), maxLat: math.Inf(-1)}
		for r, ring := range rings {
			flat := make([]float32, 0, len(ring)*2)
			for _, point := range ring {
				if len(point) < 2 {
					return nil, errors.New("short coordinate")
				}
				flat = append(flat, float32(point[0]), float32(point[1]))
				if r == 0 {
					x, y := float64(float32(point[0])), float64(float32(point[1]))
					polygon.minLon, polygon.maxLon = math.Min(polygon.minLon, x), math.Max(polygon.maxLon, x)
					polygon.minLat, polygon.maxLat = math.Min(polygon.minLat, y), math.Max(polygon.maxLat, y)
				}
			}
			polygon.rings[r] = flat
		}
		out = append(out, polygon)
	}
	if len(out) == 0 {
		return nil, errors.New("no polygons")
	}
	return out, nil
}

// Version identifies the dataset; arcades classified with another version are
// recomputed.
func (a *Areas) Version() string { return a.version }

// City returns the catalog entry for a stored city key.
func (a *Areas) City(key string) (AreaCity, bool) {
	city, ok := a.cities[key]
	return city, ok
}

// CurrentCityKey maps a stored key through dataset renames. It returns the
// key unchanged when it is current, and false when it is neither current nor
// renamed (retired).
func (a *Areas) CurrentCityKey(key string) (string, bool) {
	for i := 0; i < 8; i++ {
		if _, ok := a.cities[key]; ok {
			return key, true
		}
		next, ok := a.renames[key]
		if !ok {
			return key, false
		}
		key = next
	}
	return key, false
}

// Admin1 returns the catalog entry for an admin-1 code.
func (a *Areas) Admin1(code string) (AreaAdmin1, bool) {
	admin1, ok := a.admin1[code]
	return admin1, ok
}

// AddressAdmin1 lists the admin-1 codes of the arcade's country that an
// address names, using the catalog aliases. Overlapping matches keep the
// longest (東京都 over 京都); Latin aliases match whole words only.
func (a *Areas) AddressAdmin1(country, address string) []string {
	c := a.countries[strings.ToUpper(strings.TrimSpace(country))]
	text := strings.ToLower(address)
	if c == nil || strings.TrimSpace(text) == "" {
		return nil
	}
	taken := make([]bool, len(text))
	var out []string
	seen := map[string]bool{}
	for _, alias := range c.aliases {
		for from := 0; from < len(text); {
			index := strings.Index(text[from:], alias.text)
			if index < 0 {
				break
			}
			start, end := from+index, from+index+len(alias.text)
			from = start + 1
			if alias.latin && !(wordBoundary(text, start, true) && wordBoundary(text, end, false)) {
				continue
			}
			free := true
			for i := start; i < end; i++ {
				if taken[i] {
					free = false
					break
				}
			}
			if !free {
				continue
			}
			for i := start; i < end; i++ {
				taken[i] = true
			}
			if !seen[alias.admin1] {
				seen[alias.admin1] = true
				out = append(out, alias.admin1)
			}
		}
	}
	sort.Strings(out)
	return out
}

// AddressMismatch reports whether an address names admin-1 areas of the
// country but not the one the coordinate falls in, a hint that the marker or
// the address is wrong.
func (a *Areas) AddressMismatch(country, admin1, address string) bool {
	if admin1 == "" {
		return false
	}
	named := a.AddressAdmin1(country, address)
	if len(named) == 0 {
		return false
	}
	for _, code := range named {
		if code == admin1 {
			return false
		}
	}
	return true
}

func wordBoundary(text string, at int, before bool) bool {
	var r rune
	if before {
		if at == 0 {
			return true
		}
		r, _ = utf8.DecodeLastRuneInString(text[:at])
	} else {
		if at >= len(text) {
			return true
		}
		r, _ = utf8.DecodeRuneInString(text[at:])
	}
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// Classify places a coordinate within the arcade's country. Only that
// country's units are considered, so border towns and enclaves (Macau inside
// the Zhuhai boundary) follow the stored country instead of the geometry.
func (a *Areas) Classify(country string, lat, lon float64) AreaResult {
	country = strings.ToUpper(strings.TrimSpace(country))
	if math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 || country == "" {
		return AreaResult{Status: AreaUnmapped}
	}
	if key, ok := a.whole[country]; ok {
		return AreaResult{Admin1: country, City: key, Status: AreaResolved}
	}
	c := a.countries[country]
	if c == nil {
		return AreaResult{Status: AreaUnmapped}
	}
	var hits []*areaUnit
	for _, index := range c.grid[areaGridKey{gridCell(lon), gridCell(lat)}] {
		unit := &c.units[index]
		if unit.contains(lat, lon) {
			hits = append(hits, unit)
		}
	}
	result := AreaResult{Status: AreaResolved}
	var unit *areaUnit
	switch {
	case len(hits) == 1:
		unit = hits[0]
	case len(hits) > 1:
		unit = hits[0]
		for _, other := range hits[1:] {
			if other.city != unit.city || other.admin1 != unit.admin1 {
				result.Status = AreaAmbiguous
			}
		}
	default:
		unit, result.DistanceMeters = c.nearest(lat, lon, AreaNearMeters)
		if unit == nil {
			result.Status = AreaUnmapped
			result.DistanceMeters = 0
			return result
		}
		result.Status = AreaNear
	}
	result.Admin1, result.Admin2, result.Admin2Name, result.City = unit.admin1, unit.admin2, unit.name, unit.city
	if result.City == "" && len(c.seats) > 0 {
		if key, ok := a.nearestSeat(c, lat, lon); ok {
			result.City = key
			if result.Status == AreaResolved {
				result.Status = AreaEstimated
			}
		}
	}
	return result
}

func (a *Areas) nearestSeat(c *areaCountry, lat, lon float64) (string, bool) {
	best, bestDistance := "", math.Inf(1)
	for _, key := range c.seats {
		city := a.cities[key]
		if d := haversineMeters(lat, lon, city.Lat, city.Lon); d < bestDistance {
			best, bestDistance = key, d
		}
	}
	return best, best != "" && bestDistance <= seatReachMeters
}

func (u *areaUnit) contains(lat, lon float64) bool {
	if lat < u.minLat || lat > u.maxLat || lon < u.minLon || lon > u.maxLon {
		return false
	}
	for i := range u.polygons {
		p := &u.polygons[i]
		if lat < p.minLat || lat > p.maxLat || lon < p.minLon || lon > p.maxLon || !flatRingContains(p.rings[0], lat, lon) {
			continue
		}
		inHole := false
		for _, hole := range p.rings[1:] {
			if flatRingContains(hole, lat, lon) {
				inHole = true
				break
			}
		}
		if !inHole {
			return true
		}
	}
	return false
}

func flatRingContains(ring []float32, lat, lon float64) bool {
	inside := false
	n := len(ring) / 2
	for i, j := 0, n-1; i < n; j, i = i, i+1 {
		xi, yi := float64(ring[2*i]), float64(ring[2*i+1])
		xj, yj := float64(ring[2*j]), float64(ring[2*j+1])
		if (yi > lat) != (yj > lat) && lon < (xj-xi)*(lat-yi)/(yj-yi)+xi {
			inside = !inside
		}
	}
	return inside
}

// nearest finds the unit whose exterior lies closest within reach, using a
// local equirectangular projection like the country coastline tolerance.
func (c *areaCountry) nearest(lat, lon, reach float64) (*areaUnit, float64) {
	const metersPerDegree = 6371000 * math.Pi / 180
	xScale := metersPerDegree * math.Cos(lat*math.Pi/180)
	padLat := reach / metersPerDegree
	padLon := reach / math.Max(xScale, 1)
	seen := map[int32]bool{}
	var best *areaUnit
	bestDistance := reach
	for x := gridCell(lon - padLon); x <= gridCell(lon+padLon); x++ {
		for y := gridCell(lat - padLat); y <= gridCell(lat+padLat); y++ {
			for _, index := range c.grid[areaGridKey{x, y}] {
				if seen[index] {
					continue
				}
				seen[index] = true
				unit := &c.units[index]
				if lat < unit.minLat-padLat || lat > unit.maxLat+padLat || lon < unit.minLon-padLon || lon > unit.maxLon+padLon {
					continue
				}
				for _, polygon := range unit.polygons {
					ring := polygon.rings[0]
					n := len(ring) / 2
					for i, j := 0, n-1; i < n; j, i = i, i+1 {
						ax, ay := (float64(ring[2*j])-lon)*xScale, (float64(ring[2*j+1])-lat)*metersPerDegree
						bx, by := (float64(ring[2*i])-lon)*xScale, (float64(ring[2*i+1])-lat)*metersPerDegree
						dx, dy := bx-ax, by-ay
						t := 0.0
						if l := dx*dx + dy*dy; l > 0 {
							t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l))
						}
						if d := math.Hypot(ax+t*dx, ay+t*dy); d < bestDistance {
							best, bestDistance = unit, d
						}
					}
				}
			}
		}
	}
	return best, bestDistance
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := (lat2-lat1)*math.Pi/180, (lon2-lon1)*math.Pi/180
	h := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(h)))
}
