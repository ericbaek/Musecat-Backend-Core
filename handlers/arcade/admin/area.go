package admin

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"github.com/ericbaek/musecat-backend-core/arcadearea"
	"github.com/ericbaek/musecat-backend-core/geo"
	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

// reviewStatuses are the area statuses an administrator should look at.
var reviewStatuses = []string{geo.AreaUnmapped, geo.AreaAmbiguous, geo.AreaNear, geo.AreaEstimated}

var admin1Pattern = regexp.MustCompile(`^[A-Z]{2}(-[A-Za-z0-9-]{1,12})?$`)

// ListArcadeAreas handles GET /admin/arcade/areas: arcades whose derived area
// needs review, every visibility included. `status` narrows to a comma list
// of review statuses; `country` to one country.
func ListArcadeAreas(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	statuses := []string{}
	for _, value := range strings.Split(q.Get("status"), ",") {
		if value = strings.TrimSpace(value); value != "" {
			if !contains(reviewStatuses, value) {
				return re.JSON(http.StatusBadRequest, map[string]any{"error": "status must be one of " + strings.Join(reviewStatuses, ",")})
			}
			statuses = append(statuses, value)
		}
	}
	if len(statuses) == 0 {
		statuses = reviewStatuses
	}
	params := map[string]any{}
	clauses := []string{}
	for i, status := range statuses {
		key := "s" + string(rune('0'+i))
		params[key] = status
		clauses = append(clauses, arcadearea.FieldStatus+"={:"+key+"}")
	}
	filter := "(" + strings.Join(clauses, " || ") + ")"
	if country := strings.ToUpper(strings.TrimSpace(q.Get("country"))); country != "" {
		filter += " && country={:country}"
		params["country"] = country
	}
	records, err := re.App.FindRecordsByFilter(arcadeinternal.CollectionArcade, filter, "country,id", 500, 0, params)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to list arcade areas"})
	}
	areas, err := geo.EmbeddedAreas()
	if err != nil {
		return re.JSON(http.StatusServiceUnavailable, map[string]any{"error": "admin areas unavailable"})
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		item := arcadeAreaPayload(areas, record)
		if basic, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeBasic, record.GetString("basic")); err == nil {
			point := basic.GetGeoPoint("location")
			item["name"] = basic.GetString("name")
			item["address"] = basic.GetString("address")
			item["location"] = map[string]float64{"lat": point.Lat, "lon": point.Lon}
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return statusRank(items[i]["area_status"]) < statusRank(items[j]["area_status"])
	})
	return re.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

// UpdateArcadeArea handles PUT /admin/arcade/area. With `manual: true` it
// pins `admin1` and an optional catalog `city` of the arcade's country; with
// `manual: false` it clears the override and recomputes from the location.
// Area values are derived, so no changelog row is written.
func UpdateArcadeArea(re *core.RequestEvent) error {
	var body struct {
		Arcade string `json:"arcade"`
		Manual *bool  `json:"manual"`
		Admin1 string `json:"admin1"`
		City   string `json:"city"`
	}
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil || len(strings.TrimSpace(body.Arcade)) != 15 || body.Manual == nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade and manual are required"})
	}
	areas, err := geo.EmbeddedAreas()
	if err != nil {
		return re.JSON(http.StatusServiceUnavailable, map[string]any{"error": "admin areas unavailable"})
	}
	body.Admin1 = strings.TrimSpace(body.Admin1)
	body.City = strings.TrimSpace(body.City)
	var arcade *core.Record
	var invalid string
	err = re.App.RunInTransaction(func(txApp core.App) error {
		var err error
		arcade, err = txApp.FindRecordById(arcadeinternal.CollectionArcade, strings.TrimSpace(body.Arcade))
		if err != nil {
			return err
		}
		if !*body.Manual {
			arcade.Set(arcadearea.FieldManual, false)
			return txApp.Save(arcade)
		}
		country := strings.ToUpper(arcade.GetString("country"))
		if body.City != "" {
			city, ok := areas.City(body.City)
			if !ok || city.Country != country {
				invalid = "city must be a catalog city in the arcade country"
				return nil
			}
			if body.Admin1 == "" {
				body.Admin1 = city.Admin1
			}
		}
		if !admin1Pattern.MatchString(body.Admin1) || !strings.HasPrefix(body.Admin1, country) {
			invalid = "admin1 must be an admin-1 code of the arcade country"
			return nil
		}
		arcadearea.Apply(arcade, arcadearea.Assignment{Admin1: body.Admin1, City: body.City, Status: geo.AreaResolved}, time.Now())
		arcade.Set(arcadearea.FieldManual, true)
		return txApp.Save(arcade)
	})
	if invalid != "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": invalid})
	}
	if arcade == nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update arcade area"})
	}
	return re.JSON(http.StatusOK, arcadeAreaPayload(areas, arcade))
}

func arcadeAreaPayload(areas *geo.Areas, arcade *core.Record) map[string]any {
	a := arcadearea.FromRecord(arcade)
	item := map[string]any{
		"arcade":      arcade.Id,
		"country":     arcade.GetString("country"),
		"public":      arcade.GetBool("public"),
		"admin1":      a.Admin1,
		"admin2":      a.Admin2,
		"admin2_name": a.Admin2Name,
		"city":        a.City,
		"area_status": a.Status,
		"area_manual": arcade.GetBool(arcadearea.FieldManual),
	}
	if city, ok := areas.City(a.City); ok {
		item["city_names"] = city.Names
	}
	return item
}

func statusRank(value any) int {
	for i, status := range reviewStatuses {
		if value == status {
			return i
		}
	}
	return len(reviewStatuses)
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
