package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/text/language"
)

// The strict reviewer route guard protects all moderation operations.
func ListModerationRequests(re *core.RequestEvent) error {
	records, err := re.App.FindRecordsByFilter(arcadeinternal.CollectionArcadeRequestAdmin, "", "-created", 0, 0)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to list requests"})
	}
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		items = append(items, editReportPayload(record))
	}
	return re.JSON(http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func GetArcadeStatus(re *core.RequestEvent) error {
	arcade, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, strings.TrimSpace(re.Request.URL.Query().Get("arcade")))
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}
	return re.JSON(http.StatusOK, arcadeStatusPayload(arcade))
}

func UpdateArcadeStatus(re *core.RequestEvent) error {
	var body struct {
		Arcade   string `json:"arcade"`
		Public   *bool  `json:"public"`
		Closed   *bool  `json:"closed"`
		Country  string `json:"country"`
		Timezone string `json:"timezone"`
	}
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil || len(strings.TrimSpace(body.Arcade)) != 15 || body.Public == nil || body.Closed == nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade, public and closed are required"})
	}
	body.Arcade = strings.TrimSpace(body.Arcade)
	body.Country = strings.ToUpper(strings.TrimSpace(body.Country))
	body.Timezone = strings.TrimSpace(body.Timezone)
	region, countryErr := language.ParseRegion(body.Country)
	if len(body.Country) != 2 || countryErr != nil || !region.IsCountry() || region.String() != body.Country {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "country must be an ISO 3166-1 alpha-2 code"})
	}
	if _, err := time.LoadLocation(body.Timezone); err != nil || body.Timezone == "" || body.Timezone == "Local" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "timezone must be a valid IANA timezone"})
	}
	if _, err := re.App.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade); err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "arcade not found"})
	}
	var arcade *core.Record
	err := re.App.RunInTransaction(func(txApp core.App) error {
		var err error
		arcade, err = txApp.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil {
			return err
		}
		arcade.Set("public", *body.Public)
		arcade.Set("closed", *body.Closed)
		arcade.Set("country", body.Country)
		arcade.Set("timezone", body.Timezone)
		return txApp.Save(arcade)
	})
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update arcade status"})
	}
	return re.JSON(http.StatusOK, arcadeStatusPayload(arcade))
}

func arcadeStatusPayload(arcade *core.Record) map[string]any {
	return map[string]any{"arcade": arcade.Id, "public": arcade.GetBool("public"), "closed": arcade.GetBool("closed"), "country": arcade.GetString("country"), "timezone": arcade.GetString("timezone")}
}
