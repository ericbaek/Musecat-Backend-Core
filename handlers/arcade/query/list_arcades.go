package query

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

// ListArcades handles GET /arcades
// Optional query params:
//   - public=true|false
//   - closed=true|false
//   - country=KR (or any code used in arcade.country)
//
// Returns a list of arcades with: country, name, location, address, and owned gameSeries ids.
func ListArcades(re *core.RequestEvent) error {
	q := re.Request.URL.Query()

	// Build dynamic filter
	filterParts := []string{}
	params := dbx.Params{}

	if pv := strings.TrimSpace(q.Get("public")); pv != "" {
		b, err := strconv.ParseBool(pv)
		if err != nil {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error":   "invalid 'public' value; expected true/false",
				"details": err.Error(),
			})
		}
		if b {
			filterParts = append(filterParts, "public=true")
		} else {
			filterParts = append(filterParts, "public=false")
		}
	}
	if cv := strings.TrimSpace(q.Get("closed")); cv != "" {
		b, err := strconv.ParseBool(cv)
		if err != nil {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error":   "invalid 'closed' value; expected true/false",
				"details": err.Error(),
			})
		}
		if b {
			filterParts = append(filterParts, "closed=true")
		} else {
			filterParts = append(filterParts, "closed=false")
		}
	}
	if c := strings.TrimSpace(q.Get("country")); c != "" {
		filterParts = append(filterParts, "country={:country}")
		params["country"] = c
	}

	filter := strings.Join(filterParts, " && ")

	// Load arcade records with filter
	var arcades []*core.Record
	var err error
	if len(params) > 0 || filter != "" {
		arcades, err = re.App.FindRecordsByFilter(arcadeinternal.CollectionArcade, filter, "", 0, 0, params)
	} else {
		arcades, err = re.App.FindRecordsByFilter(arcadeinternal.CollectionArcade, "public=true && closed=false", "", 0, 0)
	}
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load arcades",
			"details": err.Error(),
		})
	}

	out := make([]map[string]any, 0, len(arcades))

	for _, a := range arcades {
		if item := buildArcadeSummary(re.App, a); item != nil {
			out = append(out, item)
		}
	}

	return re.JSON(http.StatusOK, out)
}
