package geo

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OfflineResult is the result of the bundled boundary lookup. CitySourceID is
// a GeoNames source_id and is deliberately kept separate from PocketBase ids.
type OfflineResult struct {
	Country      string
	Timezone     string
	CitySourceID string
	CityStatus   string // resolved, unmapped, ambiguous
}

type boundaryFeature struct {
	Country      string
	Timezone     string
	CityID       string
	Geometry     geoJSONGeometry
	Polygon      [][][]float64
	MultiPolygon [][][][]float64
	MinLat       float64
	MaxLat       float64
	MinLon       float64
	MaxLon       float64
	hasBounds    bool
}

type geoJSONGeometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

type geoJSONFile struct {
	Type     string `json:"type"`
	Features []struct {
		Type       string                 `json:"type"`
		Properties map[string]interface{} `json:"properties"`
		Geometry   geoJSONGeometry        `json:"geometry"`
	} `json:"features"`
}

// OfflineResolver contains immutable in-memory boundary data. It is safe for
// concurrent reads after construction.
type OfflineResolver struct {
	countries []boundaryFeature
	timezones []boundaryFeature
	cities    []boundaryFeature
}

// The country and timezone boundaries are vendored so production lookups do
// not depend on a geocoding provider or an internet connection. The files are
// gzip-compressed because the timezone boundary collection is large.
//
//go:embed data/countries.geojson.gz data/timezones.geojson.gz
var embeddedGeoData embed.FS

var (
	offlineMu       sync.RWMutex
	offlineResolver *OfflineResolver
	offlineRequired bool
)

// SetOfflineResolver installs the process-wide resolver used by the existing
// LookupCountryAndTimezone API. Passing nil disables the internal resolver.
func SetOfflineResolver(resolver *OfflineResolver, required bool) {
	offlineMu.Lock()
	offlineResolver = resolver
	offlineRequired = required
	offlineMu.Unlock()
}

func currentOfflineResolver() (*OfflineResolver, bool, bool) {
	offlineMu.RLock()
	r, required := offlineResolver, offlineRequired
	offlineMu.RUnlock()
	return r, required, r != nil
}

// LoadEmbeddedResolver loads the versioned boundary bundles shipped with the
// binary. City polygons are intentionally not embedded: city labels come from
// the local GeoNames passport_city catalog and are selected by the caller's
// deterministic nearest-city policy.
func LoadEmbeddedResolver() (*OfflineResolver, error) {
	res := &OfflineResolver{}
	for _, spec := range []struct {
		name string
		dst  *[]boundaryFeature
	}{
		{"data/countries.geojson.gz", &res.countries},
		{"data/timezones.geojson.gz", &res.timezones},
	} {
		raw, err := embeddedGeoData.ReadFile(spec.name)
		if err != nil {
			return nil, fmt.Errorf("read embedded offline geo bundle %s: %w", spec.name, err)
		}
		reader, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, fmt.Errorf("open embedded offline geo bundle %s: %w", spec.name, err)
		}
		err = decodeBoundaryFile(reader, spec.dst)
		closeErr := reader.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close embedded offline geo bundle %s: %w", spec.name, closeErr)
		}
	}
	return res, nil
}

// LoadOfflineResolver loads the deployment-owned GeoJSON bundles from dir.
// Required files are countries.geojson and timezones.geojson. The optional
// city_boundaries.geojson uses a city_source_id property and is only used when
// a unique polygon contains the coordinate.
func LoadOfflineResolver(dir string) (*OfflineResolver, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("offline geo data directory is empty")
	}
	res := &OfflineResolver{}
	for _, spec := range []struct {
		name string
		dst  *[]boundaryFeature
	}{
		{"countries.geojson", &res.countries},
		{"timezones.geojson", &res.timezones},
	} {
		if err := loadBoundaryFile(filepath.Join(dir, spec.name), spec.dst); err != nil {
			return nil, err
		}
	}
	cityPath := filepath.Join(dir, "city_boundaries.geojson")
	if _, err := os.Stat(cityPath); err == nil {
		if err := loadBoundaryFile(cityPath, &res.cities); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return res, nil
}

func loadBoundaryFile(path string, dst *[]boundaryFeature) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open offline geo bundle %s: %w", path, err)
	}
	defer f.Close()
	return decodeBoundaryFile(f, dst)
}

func decodeBoundaryFile(r io.Reader, dst *[]boundaryFeature) error {
	var file geoJSONFile
	if err := json.NewDecoder(r).Decode(&file); err != nil {
		return fmt.Errorf("decode offline geo bundle: %w", err)
	}
	if file.Type != "FeatureCollection" {
		return errors.New("offline geo bundle must be a FeatureCollection")
	}
	for i, feature := range file.Features {
		if feature.Geometry.Type != "Polygon" && feature.Geometry.Type != "MultiPolygon" {
			return fmt.Errorf("unsupported boundary geometry at feature %d", i)
		}
		country := propertyString(feature.Properties, "country", "country_code", "ISO_A2", "ISO3166-1-Alpha-2")
		timezone := propertyString(feature.Properties, "timezone", "tzid")
		city := propertyString(feature.Properties, "city_source_id", "geonames_id")
		if country == "" && timezone == "" && city == "" {
			return fmt.Errorf("boundary feature %d has no supported identity property", i)
		}
		if feature.Geometry.Coordinates == nil {
			return fmt.Errorf("boundary feature %d has no coordinates", i)
		}
		parsed := boundaryFeature{Country: strings.ToUpper(country), Timezone: timezone, CityID: city, Geometry: geoJSONGeometry{Type: feature.Geometry.Type}}
		switch feature.Geometry.Type {
		case "Polygon":
			if err := json.Unmarshal(feature.Geometry.Coordinates, &parsed.Polygon); err != nil {
				return fmt.Errorf("decode polygon coordinates at feature %d: %w", i, err)
			}
		case "MultiPolygon":
			if err := json.Unmarshal(feature.Geometry.Coordinates, &parsed.MultiPolygon); err != nil {
				return fmt.Errorf("decode multipolygon coordinates at feature %d: %w", i, err)
			}
		}
		if err := parsed.setBounds(); err != nil {
			return fmt.Errorf("invalid boundary coordinates at feature %d: %w", i, err)
		}
		*dst = append(*dst, parsed)
	}
	if len(*dst) == 0 {
		return errors.New("offline geo bundle is empty")
	}
	return nil
}

func (f *boundaryFeature) setBounds() error {
	minLat, maxLat := 90.0, -90.0
	minLon, maxLon := 180.0, -180.0
	visit := func(ring [][]float64) {
		for _, point := range ring {
			if len(point) < 2 {
				continue
			}
			if point[1] < minLat {
				minLat = point[1]
			}
			if point[1] > maxLat {
				maxLat = point[1]
			}
			if point[0] < minLon {
				minLon = point[0]
			}
			if point[0] > maxLon {
				maxLon = point[0]
			}
		}
	}
	for _, ring := range f.Polygon {
		visit(ring)
	}
	for _, polygon := range f.MultiPolygon {
		for _, ring := range polygon {
			visit(ring)
		}
	}
	if minLat > maxLat || minLon > maxLon {
		return errors.New("boundary has no valid points")
	}
	f.MinLat, f.MaxLat, f.MinLon, f.MaxLon, f.hasBounds = minLat, maxLat, minLon, maxLon, true
	return nil
}

func propertyString(properties map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := properties[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		if value, ok := properties[key].(float64); ok && value > 0 {
			return fmt.Sprintf("%.0f", value)
		}
	}
	return ""
}

// Resolve performs country, timezone and optional city point-in-polygon
// lookups without network access. A city is unresolved when zero or multiple
// mapped city polygons contain the point.
func (r *OfflineResolver) Resolve(lat, lon float64) (OfflineResult, error) {
	if r == nil {
		return OfflineResult{}, errors.New("offline geo resolver is not configured")
	}
	country := []boundaryFeature{}
	for _, feature := range r.countries {
		if feature.contains(lat, lon) {
			country = append(country, feature)
		}
	}
	if len(country) != 1 {
		return OfflineResult{}, fmt.Errorf("offline country boundary matched %d features", len(country))
	}
	timezone := []boundaryFeature{}
	for _, feature := range r.timezones {
		if feature.contains(lat, lon) {
			timezone = append(timezone, feature)
		}
	}
	if len(timezone) != 1 || strings.TrimSpace(timezone[0].Timezone) == "" {
		return OfflineResult{}, fmt.Errorf("offline timezone boundary matched %d features", len(timezone))
	}
	if _, err := time.LoadLocation(timezone[0].Timezone); err != nil {
		return OfflineResult{}, fmt.Errorf("offline timezone is invalid: %w", err)
	}
	result := OfflineResult{Country: country[0].Country, Timezone: timezone[0].Timezone, CityStatus: "unmapped"}
	if len(r.cities) == 0 {
		return result, nil
	}
	for _, feature := range r.cities {
		if feature.Country != "" && feature.Country != result.Country {
			continue
		}
		if feature.CityID != "" && feature.contains(lat, lon) {
			if result.CitySourceID != "" {
				result.CitySourceID = ""
				result.CityStatus = "ambiguous"
				continue
			}
			result.CitySourceID = feature.CityID
			result.CityStatus = "resolved"
		}
	}
	return result, nil
}

func (f boundaryFeature) contains(lat, lon float64) bool {
	if f.hasBounds && (lat < f.MinLat || lat > f.MaxLat || lon < f.MinLon || lon > f.MaxLon) {
		return false
	}
	if len(f.Polygon) > 0 {
		return pointInPolygonRings(f.Polygon, lat, lon)
	}
	if len(f.MultiPolygon) > 0 {
		for _, polygon := range f.MultiPolygon {
			if pointInPolygonRings(polygon, lat, lon) {
				return true
			}
		}
		return false
	}
	return pointInGeometry(f.Geometry, lat, lon)
}

func pointInGeometry(geometry geoJSONGeometry, lat, lon float64) bool {
	if geometry.Type == "Polygon" {
		var rings [][][]float64
		if json.Unmarshal(geometry.Coordinates, &rings) != nil {
			return false
		}
		return pointInPolygonRings(rings, lat, lon)
	}
	var polygons [][][][]float64
	if json.Unmarshal(geometry.Coordinates, &polygons) != nil {
		return false
	}
	for _, polygon := range polygons {
		if pointInPolygonRings(polygon, lat, lon) {
			return true
		}
	}
	return false
}

func pointInPolygonRings(rings [][][]float64, lat, lon float64) bool {
	if len(rings) == 0 || !pointInRing(rings[0], lat, lon) {
		return false
	}
	for _, hole := range rings[1:] {
		if pointInRing(hole, lat, lon) {
			return false
		}
	}
	return true
}

func pointInRing(ring [][]float64, lat, lon float64) bool {
	inside := false
	for i, j := 0, len(ring)-1; i < len(ring); j, i = i, i+1 {
		if len(ring[i]) < 2 || len(ring[j]) < 2 {
			continue
		}
		xi, yi := ring[i][0], ring[i][1]
		xj, yj := ring[j][0], ring[j][1]
		intersects := (yi > lat) != (yj > lat) && lon < (xj-xi)*(lat-yi)/(yj-yi)+xi
		if intersects {
			inside = !inside
		}
	}
	return inside
}
