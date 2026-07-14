package query

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

// buildArcadeSummary returns a lightweight arcade representation with basic info and owned game_series ids.
func buildArcadeSummary(app core.App, a *core.Record) map[string]any {
	candidate, ok := buildArcadeCandidate(app, a)
	if !ok {
		return nil
	}
	return candidate.Summary(true, true)
}

// parseLatLon validates and parses lat/lon from query params.
func parseLatLon(q url.Values) (float64, float64, error) {
	latStr := strings.TrimSpace(q.Get("lat"))
	lonStr := strings.TrimSpace(q.Get("lon"))
	if latStr == "" || lonStr == "" {
		return 0, 0, fmt.Errorf("lat and lon query params are required")
	}

	lat, err := strconv.ParseFloat(latStr, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid lat: %w", err)
	}
	lon, err := strconv.ParseFloat(lonStr, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid lon: %w", err)
	}
	if err := arcadeinternal.ValidateLocationCoords(lat, lon); err != nil {
		return 0, 0, err
	}
	return lat, lon, nil
}
