package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

type gameSeriesVersionMutationBody struct {
	ID           string          `json:"id"`
	Series       string          `json:"series"`
	ReleasedOn   string          `json:"released_on"`
	En           string          `json:"en"`
	Kr           string          `json:"kr"`
	Jp           string          `json:"jp"`
	PriceDefault json.RawMessage `json:"price_default"`
}

func parseGameSeriesVersionMutationBody(re *core.RequestEvent) (gameSeriesVersionMutationBody, error) {
	var body gameSeriesVersionMutationBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return body, err
	}

	body.ID = strings.TrimSpace(body.ID)
	body.Series = strings.TrimSpace(body.Series)
	body.ReleasedOn = strings.TrimSpace(body.ReleasedOn)
	body.En = strings.TrimSpace(body.En)
	body.Kr = strings.TrimSpace(body.Kr)
	body.Jp = strings.TrimSpace(body.Jp)
	body.PriceDefault = json.RawMessage(strings.TrimSpace(string(body.PriceDefault)))

	return body, nil
}

func validateGameSeriesVersionMutationBody(body gameSeriesVersionMutationBody, requireID bool) error {
	if requireID {
		if body.ID == "" {
			return fmt.Errorf("id is required")
		}
		if len(body.ID) != 15 {
			return fmt.Errorf("id must be a valid record id")
		}
	}

	if body.Series == "" {
		return fmt.Errorf("series is required")
	}
	if len(body.Series) != 15 {
		return fmt.Errorf("series must be a valid record id")
	}
	if body.ReleasedOn == "" {
		return fmt.Errorf("released_on is required")
	}
	if body.En == "" {
		return fmt.Errorf("en is required")
	}
	if body.Kr == "" {
		return fmt.Errorf("kr is required")
	}
	if body.Jp == "" {
		return fmt.Errorf("jp is required")
	}
	if len(body.PriceDefault) == 0 {
		return fmt.Errorf("price_default is required")
	}
	if strings.TrimSpace(string(body.PriceDefault)) == "" {
		return fmt.Errorf("price_default cannot be empty")
	}
	if err := validateGameSeriesVersionPriceDefault(body.PriceDefault); err != nil {
		return err
	}

	return nil
}

func applyGameSeriesVersionMutation(body gameSeriesVersionMutationBody, rec *core.Record) error {
	rec.Set("series", body.Series)
	rec.Set("released_on", body.ReleasedOn)
	rec.Set("en", body.En)
	rec.Set("kr", body.Kr)
	rec.Set("jp", body.Jp)

	var parsed any
	if err := json.Unmarshal(body.PriceDefault, &parsed); err != nil {
		return fmt.Errorf("price_default must be valid JSON")
	}
	rec.Set("price_default", parsed)

	return nil
}

type gameSeriesVersionPriceDefault struct {
	Global    *gameSeriesVersionPriceDefaultEntry            `json:"global"`
	Countries map[string]*gameSeriesVersionPriceDefaultEntry `json:"countries"`
}

type gameSeriesVersionPriceDefaultEntry struct {
	Modes []gameSeriesVersionPriceDefaultMode `json:"modes"`
}

type gameSeriesVersionPriceDefaultMode struct {
	ModeKey   string `json:"mode_key"`
	Label     string `json:"label"`
	Represent *bool  `json:"represent"`
}

func validateGameSeriesVersionPriceDefault(raw json.RawMessage) error {
	var payload gameSeriesVersionPriceDefault
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("price_default must match the expected structure")
	}

	if payload.Global == nil {
		return fmt.Errorf("price_default.global is required")
	}
	if len(payload.Global.Modes) == 0 {
		return fmt.Errorf("price_default.global.modes must have at least 1 item")
	}
	if err := validateGameSeriesVersionPriceDefaultEntry("price_default.global", payload.Global); err != nil {
		return err
	}

	for country, entry := range payload.Countries {
		country = strings.TrimSpace(country)
		if country == "" {
			return fmt.Errorf("price_default.countries keys must be non-empty")
		}
		if entry == nil {
			return fmt.Errorf("price_default.countries.%s is required", country)
		}
		if err := validateGameSeriesVersionPriceDefaultEntry("price_default.countries."+country, entry); err != nil {
			return err
		}
	}

	return nil
}

func validateGameSeriesVersionPriceDefaultEntry(path string, entry *gameSeriesVersionPriceDefaultEntry) error {
	if entry == nil {
		return fmt.Errorf("%s is required", path)
	}
	if len(entry.Modes) == 0 {
		return fmt.Errorf("%s.modes must have at least 1 item", path)
	}

	representCount := 0
	for i, mode := range entry.Modes {
		itemPath := fmt.Sprintf("%s.modes[%d]", path, i)
		if strings.TrimSpace(mode.ModeKey) == "" {
			return fmt.Errorf("%s.mode_key is required", itemPath)
		}
		if mode.Represent == nil {
			return fmt.Errorf("%s.represent is required", itemPath)
		}
		if *mode.Represent {
			representCount++
		}
	}

	if representCount > 2 {
		return fmt.Errorf("%s.modes can have at most 2 represent=true items", path)
	}

	return nil
}

func CreateGameSeriesVersion(re *core.RequestEvent) error {
	body, err := parseGameSeriesVersionMutationBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateGameSeriesVersionMutationBody(body, false); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	coll, err := re.App.FindCollectionByNameOrId(arcadeinternal.CollectionGameSeriesVersion)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create game_series_version",
			"details": fmt.Sprintf("failed to find game_series_version collection: %v", err),
		})
	}

	rec := core.NewRecord(coll)
	if err := applyGameSeriesVersionMutation(body, rec); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to create game_series_version",
			"details": fmt.Sprintf("failed to save game_series_version record: %v", err),
		})
	}

	bundle, err := BuildGameSeriesBundle(re.App, rec.Id)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load game_series_version",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, bundle)
}

func UpdateGameSeriesVersion(re *core.RequestEvent) error {
	body, err := parseGameSeriesVersionMutationBody(re)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "invalid JSON body",
			"details": err.Error(),
		})
	}
	if err := validateGameSeriesVersionMutationBody(body, true); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	rec, err := re.App.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, body.ID)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{
			"error":   "game_series_version not found",
			"details": err.Error(),
		})
	}

	if err := applyGameSeriesVersionMutation(body, rec); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error":   "validation failed",
			"details": err.Error(),
		})
	}

	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to update game_series_version",
			"details": fmt.Sprintf("failed to save game_series_version record: %v", err),
		})
	}

	bundle, err := BuildGameSeriesBundle(re.App, rec.Id)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load game_series_version",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, bundle)
}
