package user

import (
	"encoding/json"
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

type updateCountriesBody struct {
	Countries   json.RawMessage `json:"countries"`
	CountryMode string          `json:"country_mode"`
}

// UpdateCountries replaces the authenticated user's ordered profile-country list.
func UpdateCountries(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	}

	var body updateCountriesBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
	}
	if len(body.Countries) == 0 || string(body.Countries) == "null" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "countries is required"})
	}

	var countries []string
	if err := json.Unmarshal(body.Countries, &countries); err != nil || countries == nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "countries must be an array"})
	}
	countries, err := NormalizeProfileCountries(countries)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	countryMode, err := normalizeProfileCountryMode(body.CountryMode)
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	// Keep previously stored choices intact when an account loses level-15
	// access. The restriction applies when selecting a visible manual list;
	// Auto and Off do not expose the extra saved choices.
	if len(countries) > 1 && countryMode == countryModeManual && !hasCountrySelectionAccess(re.App, re.Auth) {
		return re.JSON(http.StatusForbidden, map[string]any{"error": "multiple countries require level 15"})
	}

	rec, err := re.App.FindRecordById(CollectionUserInfo, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusConflict, map[string]any{"error": "user_info is required"})
	}
	rec.Set("countries", countries)
	rec.Set("country_mode", countryMode)
	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update profile countries"})
	}

	if countryMode == countryModeAuto {
		if _, err := RefreshAutoPrimaryProfileCountry(re.App, re.Auth.Id); err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to resolve profile country"})
		}
		rec, err = re.App.FindRecordById(CollectionUserInfo, re.Auth.Id)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to resolve profile country"})
		}
	}
	resolvedCountries, primary := ResolveStoredProfileCountries(countries, countryMode, AutoPrimaryProfileCountry(rec))
	return re.JSON(http.StatusOK, map[string]any{"countries": resolvedCountries, "country_mode": countryMode, "primary_country": primary})
}
