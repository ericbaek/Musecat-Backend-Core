package user

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// UpdateGamePreferences replaces the authenticated user's preferred series.
func UpdateGamePreferences(re *core.RequestEvent) error {
	if re.Auth == nil {
		return re.JSON(http.StatusUnauthorized, map[string]any{"error": "authentication required"})
	}
	var body struct {
		Series       json.RawMessage `json:"series"`
		SeriesPublic *bool           `json:"series_public"`
		Warp         json.RawMessage `json:"warp"`
	}
	if !strings.HasPrefix(strings.ToLower(re.Request.Header.Get("Content-Type")), "application/json") || json.NewDecoder(re.Request.Body).Decode(&body) != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body"})
	}
	var series []string
	if len(body.Series) == 0 || json.Unmarshal(body.Series, &series) != nil || series == nil || body.SeriesPublic == nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "series array and series_public boolean are required"})
	}
	var warp bool
	if len(body.Warp) > 0 && (string(body.Warp) == "null" || json.Unmarshal(body.Warp, &warp) != nil) {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "warp must be a boolean"})
	}
	seen := map[string]bool{}
	normalized := make([]string, 0, len(series))
	for _, id := range series {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if _, err := re.App.FindRecordById(CollectionGameSeries, id); err != nil {
			return re.JSON(http.StatusBadRequest, map[string]any{"error": "unknown game series", "details": id})
		}
		seen[id] = true
		normalized = append(normalized, id)
	}
	rec, err := re.App.FindRecordById(CollectionUserInfo, re.Auth.Id)
	if err != nil {
		return re.JSON(http.StatusConflict, map[string]any{"error": "user_info is required"})
	}
	rec.Set("series", normalized)
	rec.Set("series_public", *body.SeriesPublic)
	if len(body.Warp) > 0 {
		rec.Set("warp", warp)
	}
	if err := re.App.Save(rec); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update game preferences"})
	}
	return re.JSON(http.StatusOK, map[string]any{"success": true, "series": normalized, "series_public": rec.GetBool("series_public"), "warp": rec.GetBool("warp")})
}
