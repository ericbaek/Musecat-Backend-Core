package version

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

type PriceDefaultMode struct {
	ModeKey   string   `json:"mode_key"`
	Label     *string  `json:"label,omitempty"`
	Amount    *float64 `json:"amount,omitempty"`
	Represent *bool    `json:"represent"`
}

type PriceDefaultEntry struct {
	Modes []PriceDefaultMode `json:"modes"`
}

type GameSeriesVersionPriceDefault struct {
	Global    *PriceDefaultEntry            `json:"global,omitempty"`
	Countries map[string]*PriceDefaultEntry `json:"countries,omitempty"`
}

func RegisterHooks(app core.App) {
	app.OnRecordValidate(arcadeinternal.CollectionGameSeriesVersion).BindFunc(func(e *core.RecordEvent) error {
		if err := ValidatePriceDefaultValue(e.Record.Get("price_default")); err != nil {
			return validation.Errors{
				"price_default": err,
			}
		}

		return e.Next()
	})
}

func ValidatePriceDefaultValue(raw any) error {
	if raw == nil {
		return nil
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return fmt.Errorf("must be a valid JSON object")
	}

	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}

	var payload GameSeriesVersionPriceDefault
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return fmt.Errorf("must match the expected price_default shape")
	}

	if payload.Global != nil {
		if err := validatePriceDefaultEntry("global", payload.Global); err != nil {
			return err
		}
	}

	for country, entry := range payload.Countries {
		country = strings.TrimSpace(country)
		if country == "" {
			return fmt.Errorf("countries keys must be non-empty")
		}
		if entry == nil {
			return fmt.Errorf("countries.%s is required", country)
		}
		if err := validatePriceDefaultEntry("countries."+country, entry); err != nil {
			return err
		}
	}

	return nil
}

func validatePriceDefaultEntry(path string, entry *PriceDefaultEntry) error {
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
		if mode.Amount != nil && *mode.Amount <= 0 {
			return fmt.Errorf("%s.amount must be > 0 or null", itemPath)
		}
	}

	if representCount > 2 {
		return fmt.Errorf("%s.modes can have at most 2 represent=true items", path)
	}

	return nil
}
