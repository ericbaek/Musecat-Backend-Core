package arcadeinternal

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"

	pbtypes "github.com/pocketbase/pocketbase/tools/types"
)

type Location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

// ValidateRequiredLocation ensures the pointer is non-nil and coordinates are valid.
func ValidateRequiredLocation(loc *Location) error {
	if loc == nil {
		return fmt.Errorf("location is required")
	}
	return ValidateLocationCoords(loc.Lat, loc.Lon)
}

// ValidateLocationCoords enforces the same latitude/longitude rules for create and update flows.
func ValidateLocationCoords(lat, lon float64) error {
	if math.IsNaN(lat) || lat < -90 || lat > 90 || lat == 0 {
		return fmt.Errorf("location.lat out of range %f", lat)
	}
	if math.IsNaN(lon) || lon < -180 || lon > 180 || lon == 0 {
		return fmt.Errorf("location.lon out of rang %f", lon)
	}
	return nil
}

func ReadLocation(v any) (float64, float64, bool) {
	// Support PocketBase types.GeoPoint and common map shapes
	// Direct type assertions
	switch t := v.(type) {
	case pbtypes.GeoPoint:
		return t.Lat, t.Lon, true
	case *pbtypes.GeoPoint:
		if t == nil {
			return 0, 0, false
		}
		return t.Lat, t.Lon, true
	case map[string]any:
		if _, ok := t["latitude"]; ok {
			return toFloat(t["latitude"]), toFloat(t["longitude"]), true
		}
		if _, ok := t["lng"]; ok {
			return toFloat(t["lat"]), toFloat(t["lng"]), true
		}
		if _, ok := t["lon"]; ok {
			return toFloat(t["lat"]), toFloat(t["lon"]), true
		}
		// fallback (in case lat only)
		return toFloat(t["lat"]), toFloat(t["lon"]), true
	}

	// Try reflection in case it is a types.GeoPoint coming as an alias or different pkg path
	rv := reflect.ValueOf(v)
	if rv.IsValid() {
		if rv.Kind() == reflect.Ptr {
			if rv.IsNil() {
				return 0, 0, false
			}
			rv = rv.Elem()
		}
		if rv.Kind() == reflect.Struct {
			latF := rv.FieldByName("Lat")
			lonF := rv.FieldByName("Lon")
			if latF.IsValid() && lonF.IsValid() && latF.CanInterface() && lonF.CanInterface() {
				lat := toFloat(latF.Interface())
				lon := toFloat(lonF.Interface())
				return lat, lon, true
			}
		}
	}
	return 0, 0, false
}

func toFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case float32:
		return float64(t)
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case int32:
		return float64(t)
	case uint:
		return float64(t)
	case uint64:
		return float64(t)
	case uint32:
		return float64(t)
	case json.Number:
		f, _ := t.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	default:
		return 0
	}
}

// DistanceKm returns the haversine distance in kilometers between two coordinates.
func DistanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	toRad := func(deg float64) float64 { return deg * math.Pi / 180 }

	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)

	lat1Rad := toRad(lat1)
	lat2Rad := toRad(lat2)

	a := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1Rad)*math.Cos(lat2Rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusKm * c
}
