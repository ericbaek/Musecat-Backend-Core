package query

import (
	"fmt"
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

// exportGameSeriesVersion maps a game_series_version record to a plain object using exact schema fields.
func exportGameSeriesVersion(rec *core.Record) map[string]any {
	if rec == nil {
		return nil
	}
	return map[string]any{
		"id":            rec.Id,
		"series":        rec.Get("series"),      // relation id
		"released_on":   rec.Get("released_on"), // date
		"en":            rec.GetString("en"),
		"kr":            rec.GetString("kr"),
		"jp":            rec.GetString("jp"),
		"price_default": rec.Get("price_default"),
		// "hide_at":     rec.Get("hide_at"), // json passthrough
		// "created":     rec.Get("created"),
		// "updated":     rec.Get("updated"),
	}
}

// exportGameSeries maps a game_series record to a plain object using exact schema fields.
func exportGameSeries(rec *core.Record) map[string]any {
	if rec == nil {
		return nil
	}
	return map[string]any{
		"id":           rec.Id,
		"seriesNumber": rec.Get("seriesNumber"), // number
		"en":           rec.GetString("en"),
		"kr":           rec.GetString("kr"),
		"jp":           rec.GetString("jp"),
		"en_short":     rec.GetString("en_short"),
		"kr_short":     rec.GetString("kr_short"),
		"jp_short":     rec.GetString("jp_short"),
		"manufacturer": rec.Get("manufacturer"), // relation id
		// "hide_at":       rec.Get("hide_at"),      // json passthrough
		// "created":       rec.Get("created"),
		// "updated":       rec.Get("updated"),
	}
}

// BuildGameSeriesBundle returns a map with { version, series } for a game_series_version id.
func BuildGameSeriesBundle(app core.App, versionId string) (map[string]any, error) {
	if versionId == "" {
		return nil, fmt.Errorf("empty versionId")
	}
	versionRec, err := app.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, versionId)
	if err != nil {
		return nil, err
	}
	seriesId, _ := arcadeinternal.AsString(versionRec.Get("series"))
	var seriesRec *core.Record
	if seriesId != "" {
		if rec, err := app.FindRecordById(arcadeinternal.CollectionGameSeries, seriesId); err == nil {
			seriesRec = rec
		}
	}
	return map[string]any{
		"version": exportGameSeriesVersion(versionRec),
		"series":  exportGameSeries(seriesRec),
	}, nil
}

// GetGameSeriesVersion handles GET /game_series_version?id=...
// Returns the version record plus the related series record (via version.series relation).
func GetGameSeriesVersion(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	id := q.Get("id")
	if id == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{
			"error": "missing required query param 'id'",
		})
	}
	bundle, err := BuildGameSeriesBundle(re.App, id)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{
			"error":   "game_series_version not found",
			"details": err.Error(),
		})
	}
	return re.JSON(http.StatusOK, bundle)
}
