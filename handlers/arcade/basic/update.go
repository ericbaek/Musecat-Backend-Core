package basic

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/pocketbase/pocketbase/tools/types"

	"github.com/ericbaek/musecat-backend-core/geo"
	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

type UpdateArcadeBasicBody struct {
	Arcade     string                   `json:"arcade"`
	Name       *string                  `json:"name,omitempty"`
	Location   *arcadeinternal.Location `json:"location,omitempty"`
	Address    *string                  `json:"address,omitempty"`
	Direction  *string                  `json:"direction,omitempty"`
	Nickname   *[]string                `json:"nickname,omitempty"`
	SubwayLine *[]string                `json:"subway_line,omitempty"`
}

func parseUpdateBasicBody(re *core.RequestEvent) (UpdateArcadeBasicBody, error) {
	var body UpdateArcadeBasicBody
	err := json.NewDecoder(re.Request.Body).Decode(&body)
	return body, err
}

// BasicFields captures the relevant snapshot of arcade_basic fields we care about.
type BasicFields struct {
	Name        string
	Address     string
	Direction   string
	Nickname    []string
	SubwayLine  []string
	Lat         float64
	Lon         float64
	RawLocation any
}

func validateUpdateBasicBody(body UpdateArcadeBasicBody) error {
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	if body.Location != nil {
		if err := arcadeinternal.ValidateLocationCoords(body.Location.Lat, body.Location.Lon); err != nil {
			return err
		}
	}
	return nil
}

// getCurrentBasic loads the arcade and its current basic record and returns
// their values as BasicFields.
func getCurrentBasic(app core.App, arcadeID string) (BasicFields, error) {
	// load arcade
	arcade, err := app.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil {
		return BasicFields{}, fmt.Errorf("arcade not found: %w", err)
	}

	// read basic relation id (string or single-item slice)
	basicID, _ := arcadeinternal.AsString(arcade.Get("basic"))
	if basicID == "" {
		return BasicFields{}, fmt.Errorf("arcade.basic is empty")
	}

	current, err := app.FindRecordById(arcadeinternal.CollectionArcadeBasic, basicID)
	if err != nil {
		return BasicFields{}, fmt.Errorf("arcade_basic not found: %w", err)
	}

	// snapshot current values
	cur := BasicFields{}
	cur.Name, _ = arcadeinternal.AsString(current.Get("name"))
	cur.Address, _ = arcadeinternal.AsString(current.Get("address"))
	cur.Direction, _ = arcadeinternal.AsString(current.Get("direction"))
	// nickname: robust parse from various PocketBase representations
	// rawNick := current.Get("nickname")
	// cur.Nickname = toStringSlice(rawNick)
	cur.Nickname = current.GetStringSlice("nickname")
	cur.SubwayLine = current.GetStringSlice("subway_line")

	// log.Printf("[update_basic] nickname raw =%#v parsed=%#v", rawNick, cur.Nickname)
	cur.Lat, cur.Lon, _ = arcadeinternal.ReadLocation(current.Get("location"))
	cur.RawLocation = current.Get("location")
	// log.Printf("[update_basic] current RawLocation: %#v | parsed: %f, %f", cur.RawLocation, cur.Lat, cur.Lon)

	return cur, nil
}

func mergeBasicFields(cur BasicFields, body UpdateArcadeBasicBody) BasicFields {
	out := cur
	if body.Name != nil {
		out.Name = *body.Name
	}
	if body.Address != nil {
		out.Address = *body.Address
	}
	if body.Direction != nil {
		out.Direction = *body.Direction
	}
	if body.Nickname != nil {
		out.Nickname = *body.Nickname
	}
	if body.SubwayLine != nil {
		out.SubwayLine = *body.SubwayLine
	}
	if body.Location != nil {
		out.Lat, out.Lon = body.Location.Lat, body.Location.Lon
	}
	return out
}

func computeChangedFields(cur, merged BasicFields, body UpdateArcadeBasicBody) []string {
	changed := []string{}
	if body.Name != nil && merged.Name != cur.Name {
		changed = append(changed, "name")
	}
	if body.Address != nil && merged.Address != cur.Address {
		changed = append(changed, "address")
	}
	if body.Direction != nil && merged.Direction != cur.Direction {
		changed = append(changed, "direction")
	}
	if body.Nickname != nil && !equalStringSlices(merged.Nickname, cur.Nickname) {
		log.Printf("[update_basic] merged : %#v | current_parsed: %#v", merged.Nickname, cur.Nickname)
		changed = append(changed, "nickname")
	}
	if body.SubwayLine != nil && !equalStringSlices(merged.SubwayLine, cur.SubwayLine) {
		changed = append(changed, "subway_line")
	}
	// location: only consider when provided; compare with tolerance
	if body.Location != nil {
		if !floatsEqual(merged.Lat, cur.Lat) || !floatsEqual(merged.Lon, cur.Lon) {
			changed = append(changed, "location")
		}
	}
	return changed
}

func createNewBasic(app core.App, arcadeID string, merged BasicFields, createdBy string, body UpdateArcadeBasicBody, cur BasicFields) (string, error) {
	basicColl, err := app.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeBasic)
	if err != nil {
		return "", fmt.Errorf("failed to find arcade_basic: %w", err)
	}
	rec := core.NewRecord(basicColl)
	rec.Set("arcade", arcadeID)
	rec.Set("name", merged.Name)
	rec.Set("address", merged.Address)
	rec.Set("direction", merged.Direction)
	// nickname: keep existing if not provided
	if body.Nickname != nil {
		rec.Set("nickname", merged.Nickname)
	} else {
		rec.Set("nickname", cur.Nickname)
	}
	rec.Set("subway_line", merged.SubwayLine)
	// location: keep existing if not provided
	if body.Location != nil {
		rec.Set("location", pbtypes.GeoPoint{Lat: merged.Lat, Lon: merged.Lon})
	} else {
		rec.Set("location", cur.RawLocation)
	}
	rec.Set("createdBy", createdBy)
	if err := app.Save(rec); err != nil {
		return "", fmt.Errorf("failed to create new arcade_basic: %w", err)
	}
	return rec.Id, nil
}

func UpdateArcadeBasic(re *core.RequestEvent) error {
	// 1) parse
	body, err := parseUpdateBasicBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}

	// 2) validate basic constraints
	if err := validateUpdateBasicBody(body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	// 3) load current state
	cur, err := getCurrentBasic(re.App, body.Arcade)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "load failed",
			"details": err.Error(),
		})
	}

	// 4) merge and diff
	merged := mergeBasicFields(cur, body)
	changed := computeChangedFields(cur, merged, body)
	if len(changed) == 0 {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "at least one changed field is required",
		})
	}

	var geoResult *geo.Result
	if body.Location != nil {
		res, err := geo.LookupCountryAndTimezone(re.Request.Context(), merged.Lat, merged.Lon)
		if err != nil {
			return re.JSON(http.StatusServiceUnavailable, map[string]any{
				"error":   "geo lookup failed",
				"details": err.Error(),
			})
		}
		geoResult = &res
	}

	var newBasicID string
	var xpFeedback userhandler.ExpFeedback
	if err := re.App.RunInTransaction(func(txApp core.App) error {
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		baseExp, err := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp
		if geoResult != nil {
			if _, err := arcadeinternal.SyncArcadeCountryByGeoResult(txApp, body.Arcade, *geoResult); err != nil {
				return err
			}
		}

		newBasicID, createErr := createNewBasic(txApp, body.Arcade, merged, re.Auth.Id, body, cur)
		if createErr != nil {
			return createErr
		}

		basicChangeLog := arcadeinternal.BuildChangelogEnvelope("basic", []basicDiffLogItem{
			buildBasicDiffLogItem(&cur, merged),
		})
		if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(
			txApp,
			body.Arcade,
			map[string]any{"basic": newBasicID},
			map[string]any{"basic": basicChangeLog},
			re.Auth.Id,
		); err != nil {
			return err
		}

		if arcadeRec.GetBool("public") {
			nextExp, _, err := userhandler.AwardArcadeEditExpTx(txApp, re.Auth.Id, body.Arcade, "basic", 3, baseExp, time.Now().UTC())
			if err != nil {
				return err
			}
			currentExp = nextExp
		}
		xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
		return nil
	}); err != nil {
		if errors.Is(err, arcadeinternal.ErrArcadeCountryConflict) {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error": err.Error(),
			})
		}
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to update arcade.basic",
			"details": err.Error(),
		})
	}

	// 7) success
	return re.JSON(http.StatusOK, map[string]any{
		"arcade":      body.Arcade,
		"basic":       newBasicID,
		"changed":     changed,
		"xp_feedback": xpFeedback,
	})
}

// equalStringSlices treats nil and empty slices as equal, trims spaces, and
// compares elements in order.
func equalStringSlices(a, b []string) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

// floatsEqual compares two floats with a small relative/absolute tolerance.
func floatsEqual(a, b float64) bool {
	if a == b {
		return true
	}
	const eps = 1e-6
	diff := math.Abs(a - b)
	if diff <= eps {
		return true
	}
	// relative tolerance for larger magnitudes
	return diff <= eps*math.Max(1, math.Max(math.Abs(a), math.Abs(b)))
}
