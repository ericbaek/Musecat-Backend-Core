package gtk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

// UpdateArcadeGTKBody represents the request body for updating arcade GTK.
type UpdateArcadeGTKBody struct {
	Arcade string         `json:"arcade"`
	GTK    []GTKAtomInput `json:"gtk"`
}

type ParkingGeoInput struct {
	Lat *float64 `json:"lat,omitempty"`
	Lon *float64 `json:"lon,omitempty"`
}

func (g *ParkingGeoInput) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*g = ParkingGeoInput{}
		return nil
	}

	type alias ParkingGeoInput
	var decoded alias
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*g = ParkingGeoInput(decoded)
	return nil
}

type ParkingMetaInput struct {
	Geo          *ParkingGeoInput `json:"geo,omitempty"`
	Availability *string          `json:"availability,omitempty"`
	Options      []string         `json:"options,omitempty"`
	EVCharging   *bool            `json:"ev_charging,omitempty"`
	GovParking   *bool            `json:"gov_parking,omitempty"`
}

func (m *ParkingMetaInput) UnmarshalJSON(data []byte) error {
	if strings.TrimSpace(string(data)) == "null" {
		*m = ParkingMetaInput{}
		return nil
	}

	type alias ParkingMetaInput
	var decoded alias
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	*m = ParkingMetaInput(decoded)
	return nil
}

// GTKAtomInput represents a single GTK atom to be saved under a GTK molecule.
type GTKAtomInput struct {
	Type string            `json:"type"`
	Bool *bool             `json:"bool"` // pointer to detect presence; required
	Note string            `json:"note,omitempty"`
	Meta *ParkingMetaInput `json:"meta,omitempty"`
}

var allowedGTKTypes = map[string]struct{}{
	"Basket":          {},
	"FreeGloves":      {},
	"FreeSlipper":     {},
	"FreeWater":       {},
	"FreeDrumStick":   {},
	"FreeBachi":       {},
	"FreeWifi":        {},
	"Locker":          {},
	"Toilet":          {},
	"AC":              {},
	"Fan":             {},
	"Refrigerator":    {},
	"PowerOutlet":     {},
	"SellGloves":      {},
	"SellDrumStick":   {},
	"SellBachi":       {},
	"SellSlipper":     {},
	"SellDrink":       {},
	"SellAmusementIC": {},
	"SellAMPASS":      {},
	"Parking":         {},
	"SmokingRoom":     {},
}

var allowedParkingAvailability = map[string]struct{}{
	"always_plenty":      {},
	"somewhat_plenty":    {},
	"somewhat_difficult": {},
	"difficult":          {},
}

var allowedParkingOptions = map[string]struct{}{
	"free_parking_lot":    {},
	"paid_parking_lot":    {},
	"free_street_parking": {},
	"paid_street_parking": {},
}

func parseUpdateGTKBody(re *core.RequestEvent) (UpdateArcadeGTKBody, error) {
	var body UpdateArcadeGTKBody
	err := json.NewDecoder(re.Request.Body).Decode(&body)
	return body, err
}

func validateUpdateGTKBody(body UpdateArcadeGTKBody) error {
	if body.Arcade == "" {
		return fmt.Errorf("arcade is required")
	}
	for i, a := range body.GTK {
		if a.Type == "" {
			return fmt.Errorf("gtk[%d].type is required", i)
		}
		if _, ok := allowedGTKTypes[a.Type]; !ok {
			return fmt.Errorf("gtk[%d].type '%s' is not allowed", i, a.Type)
		}
		if a.Bool == nil {
			return fmt.Errorf("gtk[%d].bool is required", i)
		}
		if a.Type == "Parking" {
			if err := validateParkingMeta(a.Meta); err != nil {
				return fmt.Errorf("gtk[%d].meta: %w", i, err)
			}
		} else if a.Meta != nil {
			return fmt.Errorf("gtk[%d].meta is only allowed for Parking", i)
		}
		// note is optional
	}
	return nil
}

func validateParkingMeta(meta *ParkingMetaInput) error {
	if meta == nil {
		return nil
	}
	if meta.Geo != nil {
		if meta.Geo.Lat == nil {
			return fmt.Errorf("geo.lat is required when geo is provided")
		}
		if meta.Geo.Lon == nil {
			return fmt.Errorf("geo.lon is required when geo is provided")
		}
	}
	if meta.Availability != nil {
		availability := strings.TrimSpace(*meta.Availability)
		if availability == "" {
			return fmt.Errorf("availability cannot be empty")
		}
		if _, ok := allowedParkingAvailability[availability]; !ok {
			return fmt.Errorf("availability '%s' is not allowed", availability)
		}
	}
	for i, option := range meta.Options {
		normalized := strings.TrimSpace(option)
		if normalized == "" {
			return fmt.Errorf("options[%d] cannot be empty", i)
		}
		if _, ok := allowedParkingOptions[normalized]; !ok {
			return fmt.Errorf("options[%d] '%s' is not allowed", i, normalized)
		}
	}
	return nil
}

func normalizeParkingMeta(meta *ParkingMetaInput) map[string]any {
	if meta == nil {
		return nil
	}

	out := map[string]any{}
	if meta.Geo != nil && meta.Geo.Lat != nil && meta.Geo.Lon != nil {
		out["geo"] = map[string]any{
			"lat": *meta.Geo.Lat,
			"lon": *meta.Geo.Lon,
		}
	}
	if meta.Availability != nil {
		availability := strings.TrimSpace(*meta.Availability)
		if availability != "" {
			out["availability"] = availability
		}
	}
	if meta.Options != nil {
		options := make([]string, 0, len(meta.Options))
		for _, option := range meta.Options {
			normalized := strings.TrimSpace(option)
			if normalized == "" {
				continue
			}
			options = append(options, normalized)
		}
		out["options"] = options
	}
	if meta.EVCharging != nil {
		out["ev_charging"] = *meta.EVCharging
	}
	if meta.GovParking != nil {
		out["gov_parking"] = *meta.GovParking
	}
	return out
}

// UpdateArcadeGTK creates a new arcade_gtk record (molecule), attaches atoms in arcade_gtk_atoms,
// and updates the arcade.gtk relation to the new molecule id. Uses best-effort rollback on failure.
func UpdateArcadeGTK(re *core.RequestEvent) error {
	body, err := parseUpdateGTKBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}

	if err := validateUpdateGTKBody(body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	var newGTKId string
	var expandedGTKValue ExpandedGTKValue
	var xpFeedback userhandler.ExpFeedback

	if err := re.App.RunInTransaction(func(txApp core.App) error {
		// 1) Verify the arcade exists and capture the previous GTK molecule for diffing.
		arcadeRec, err := txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return fmt.Errorf("arcade not found: %w", err)
		}
		prevAtoms, err := loadCurrentGTKAtoms(txApp, body.Arcade, strings.TrimSpace(arcadeRec.GetString("gtk")))
		if err != nil {
			return err
		}
		baseExp, err := userhandler.LoadCurrentExp(txApp, re.Auth.Id)
		if err != nil {
			return fmt.Errorf("failed to load current exp: %w", err)
		}
		currentExp := baseExp

		// 2) Create the new GTK molecule that will replace arcade.gtk.
		gtkColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeGTK)
		if err != nil {
			return fmt.Errorf("failed to find arcade_gtk: %w", err)
		}
		gtkRec := core.NewRecord(gtkColl)
		gtkRec.Set("arcade", body.Arcade)
		gtkRec.Set("createdBy", re.Auth.Id)
		if err := txApp.Save(gtkRec); err != nil {
			return fmt.Errorf("failed to create arcade_gtk: %w", err)
		}

		newGTKId = gtkRec.Id

		// 3) Create the GTK atoms under that molecule and keep a rendered copy
		//    for the success response.
		atomColl, err := txApp.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeGTKAtoms)
		if err != nil {
			return fmt.Errorf("failed to find arcade_gtk_atoms: %w", err)
		}
		nextAtoms := make([]gtkAtomState, 0, len(body.GTK))
		nextItems := make([]ExpandedGTKItem, 0, len(body.GTK))
		for i, a := range body.GTK {
			atom := core.NewRecord(atomColl)
			atom.Set("molecule", newGTKId)
			atom.Set("type", a.Type)
			atom.Set("bool", *a.Bool)
			if a.Note != "" {
				atom.Set("note", a.Note)
			}
			if a.Type == "Parking" {
				if meta := normalizeParkingMeta(a.Meta); meta != nil {
					atom.Set("meta", meta)
				}
			}
			atom.Set("createdBy", re.Auth.Id)
			if err := txApp.Save(atom); err != nil {
				return fmt.Errorf("failed to create gtk atom %d: %w", i, err)
			}
			nextAtoms = append(nextAtoms, gtkAtomState{
				AtomID: atom.Id,
				Type:   a.Type,
				Bool:   *a.Bool,
				Note:   strings.TrimSpace(a.Note),
				Meta:   normalizeParkingMeta(a.Meta),
			})
			nextItems = append(nextItems, ExpandedGTKItem{
				Type: a.Type,
				Bool: *a.Bool,
				Note: strings.TrimSpace(a.Note),
				Meta: normalizeParkingMeta(a.Meta),
			})
		}

		// 4) Compare old and new atoms so the changelog records added / updated / deleted entries.
		prevQueues := map[string][]gtkAtomState{}
		for _, prevAtom := range prevAtoms {
			prevQueues[prevAtom.Type] = append(prevQueues[prevAtom.Type], prevAtom)
		}
		gtkLogItems := make([]gtkDiffLogItem, 0, len(nextAtoms)+len(prevAtoms))
		for _, nextAtom := range nextAtoms {
			queue := prevQueues[nextAtom.Type]
			if len(queue) == 0 {
				gtkLogItems = append(gtkLogItems, buildGTKDiffLogItem(nextAtom, nil))
				continue
			}
			prevAtom := queue[0]
			prevQueues[nextAtom.Type] = queue[1:]
			gtkLogItems = append(gtkLogItems, buildGTKDiffLogItem(nextAtom, &prevAtom))
		}
		for _, queue := range prevQueues {
			for _, prevAtom := range queue {
				gtkLogItems = append(gtkLogItems, buildDeletedGTKDiffLogItem(prevAtom))
			}
		}

		// 5) Update arcade.gtk via shared helper (atomic + changelog).
		if err := arcadeinternal.UpdateArcadeFieldsTxWithLogs(
			txApp,
			body.Arcade,
			map[string]any{"gtk": newGTKId},
			map[string]any{"gtk": arcadeinternal.BuildChangelogEnvelope("gtk", gtkLogItems)},
			re.Auth.Id,
		); err != nil {
			return fmt.Errorf("failed to update arcade.gtk: %w", err)
		}
		if arcadeRec.GetBool("public") {
			nextExp, _, err := userhandler.AwardArcadeEditExpTx(txApp, re.Auth.Id, body.Arcade, "gtk", 3, baseExp, time.Now().UTC())
			if err != nil {
				return err
			}
			currentExp = nextExp
		}

		expandedGTKValue = BuildExpandedGTKValue(newGTKId, nextItems)
		xpFeedback = userhandler.BuildExpFeedback(baseExp, currentExp)
		return nil
	}); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "transaction failed",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{
		"arcade":      body.Arcade,
		"gtk":         expandedGTKValue,
		"xp_feedback": xpFeedback,
	})
}
