package user

import (
	"encoding/json"
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

type updateCountriesBody struct {
	Countries json.RawMessage `json:"countries"`
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
	if len(countries) > 1 && !hasCountrySelectionAccess(re.App, re.Auth) {
		return re.JSON(http.StatusForbidden, map[string]any{"error": "multiple countries require level 15"})
	}

	rec, err := re.App.FindRecordById(CollectionUserInfo, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusConflict, map[string]any{"error": "user_info is required"})
	}
	rec.Set("countries", countries)
	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update profile countries"})
	}

	primary := ""
	if len(countries) > 0 {
		primary = countries[0]
	}
	return re.JSON(http.StatusOK, map[string]any{"countries": countries, "primary_country": primary})
}
