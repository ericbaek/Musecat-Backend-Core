package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/geo"
)

// GeoLookupHandler handles GET /geo?lat=..&lon=..
func GeoLookupHandler(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	latStr := q.Get("lat")
	lonStr := q.Get("lon")

	lat, err1 := strconv.ParseFloat(latStr, 64)
	lon, err2 := strconv.ParseFloat(lonStr, 64)
	if latStr == "" || lonStr == "" || err1 != nil || err2 != nil {
		return re.JSON(http.StatusBadRequest, map[string]string{"error": "invalid or missing lat/lon"})
	}

	res, err := geo.LookupCountryAndTimezone(re.Request.Context(), lat, lon)
	if err != nil {
		re.App.Logger().Error("geo lookup failed", "error", err)
		return re.JSON(http.StatusBadGateway, map[string]string{"error": "country and timezone lookup failed"})
	}
	return re.JSON(http.StatusOK, res)
}

// GeocodeHandler handles GET /geocode?query=...&region=...
func GeocodeHandler(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	query := strings.TrimSpace(q.Get("query"))
	if query == "" {
		return re.JSON(http.StatusBadRequest, map[string]string{"error": "missing query"})
	}

	res, err := geo.ForwardGeocode(re.Request.Context(), query, q.Get("region"), q.Get("mode"))
	if err != nil {
		re.App.Logger().Error("forward geocode failed", "error", err)
		return re.JSON(http.StatusBadGateway, map[string]string{"error": "geocoding failed"})
	}
	return re.JSON(http.StatusOK, res)
}

// ReverseGeocodeHandler handles GET /reverse_geocode?lat=...&lon=...&region=...
func ReverseGeocodeHandler(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	latStr := strings.TrimSpace(q.Get("lat"))
	lonStr := strings.TrimSpace(q.Get("lon"))

	lat, err1 := strconv.ParseFloat(latStr, 64)
	lon, err2 := strconv.ParseFloat(lonStr, 64)
	if latStr == "" || lonStr == "" || err1 != nil || err2 != nil {
		return re.JSON(http.StatusBadRequest, map[string]string{"error": "invalid or missing lat/lon"})
	}

	res, err := geo.ReverseGeocode(re.Request.Context(), lat, lon, q.Get("region"), q.Get("mode"))
	if err != nil {
		if err.Error() == "invalid coordinates" {
			return re.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		}
		re.App.Logger().Error("reverse geocode failed", "error", err)
		return re.JSON(http.StatusBadGateway, map[string]string{"error": "reverse geocoding failed"})
	}
	return re.JSON(http.StatusOK, res)
}
