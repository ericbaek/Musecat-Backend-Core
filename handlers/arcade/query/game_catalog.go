package query

import (
	"net/http"
	"sort"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

type gameCatalogResponse struct {
	Series   []gameCatalogSeries  `json:"series"`
	Versions []gameCatalogVersion `json:"versions"`
}

type gameCatalogSeries struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SeriesNumber int    `json:"series_number"`
}

type gameCatalogVersion struct {
	ID           string               `json:"id"`
	SeriesID     string               `json:"series_id"`
	Name         string               `json:"name"`
	ReleasedOn   *string              `json:"released_on"`
	PriceDefault any                  `json:"price_default"`
	Cabinets     []gameCatalogCabinet `json:"cabinets"`
}

type gameCatalogCabinet struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	PriceDefault any    `json:"price_default"`
}

// GetGameCatalog returns the feature-neutral game catalog needed to edit
// arcade game installations. A cabinet appears only when it is explicitly
// compatible with its version; an unverified cabinet is represented by null
// only in mutation/read game entries, never as a catalog row.
func GetGameCatalog(re *core.RequestEvent) error {
	locale, ok := catalogLocale(re.Request.URL.Query().Get("locale"))
	if !ok {
		return re.JSON(http.StatusBadRequest, map[string]string{
			"error": "locale must be one of en-US, ko-KR, ja-JP",
		})
	}

	seriesRecords, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionGameSeries,
		"",
		"",
		0,
		0,
		nil,
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]string{
			"error": "failed to load game catalog series",
		})
	}
	seriesByID := make(map[string]gameCatalogSeries, len(seriesRecords))
	for _, record := range seriesRecords {
		seriesByID[record.Id] = gameCatalogSeries{
			ID:           record.Id,
			Name:         catalogSeriesName(record, locale),
			SeriesNumber: record.GetInt("seriesNumber"),
		}
	}

	versionRecords, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionGameSeriesVersion,
		"",
		"",
		0,
		0,
		nil,
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]string{
			"error": "failed to load game catalog versions",
		})
	}
	versionsByID := make(map[string]gameCatalogVersion, len(versionRecords))
	for _, record := range versionRecords {
		seriesID := record.GetString("series")
		if _, exists := seriesByID[seriesID]; !exists {
			continue
		}
		versionsByID[record.Id] = gameCatalogVersion{
			ID:           record.Id,
			SeriesID:     seriesID,
			Name:         catalogName(record, locale),
			ReleasedOn:   catalogNullableString(record.GetString("released_on")),
			PriceDefault: record.Get("price_default"),
			Cabinets:     []gameCatalogCabinet{},
		}
	}

	cabinetRecords, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionGameCabinet,
		"",
		"",
		0,
		0,
		nil,
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]string{
			"error": "failed to load game catalog cabinets",
		})
	}
	cabinetsByID := make(map[string]gameCatalogCabinet, len(cabinetRecords))
	for _, record := range cabinetRecords {
		cabinetsByID[record.Id] = gameCatalogCabinet{
			ID:   record.Id,
			Name: catalogName(record, locale),
		}
	}

	compatibilityRecords, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionGameSeriesVersionCabinet,
		"",
		"",
		0,
		0,
		nil,
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]string{
			"error": "failed to load game catalog compatibility",
		})
	}
	for _, record := range compatibilityRecords {
		version, versionExists := versionsByID[record.GetString("version")]
		cabinet, cabinetExists := cabinetsByID[record.GetString("cabinet")]
		if !versionExists || !cabinetExists {
			continue
		}
		cabinet.PriceDefault = record.Get("price_default")
		version.Cabinets = append(version.Cabinets, cabinet)
		versionsByID[version.ID] = version
	}

	response := gameCatalogResponse{
		Series:   make([]gameCatalogSeries, 0, len(seriesByID)),
		Versions: make([]gameCatalogVersion, 0, len(versionsByID)),
	}
	for _, series := range seriesByID {
		response.Series = append(response.Series, series)
	}
	for _, version := range versionsByID {
		sort.Slice(version.Cabinets, func(i, j int) bool {
			return catalogLess(version.Cabinets[i].Name, version.Cabinets[i].ID, version.Cabinets[j].Name, version.Cabinets[j].ID)
		})
		response.Versions = append(response.Versions, version)
	}
	sort.Slice(response.Series, func(i, j int) bool {
		if response.Series[i].SeriesNumber != response.Series[j].SeriesNumber {
			return response.Series[i].SeriesNumber < response.Series[j].SeriesNumber
		}
		return catalogLess(response.Series[i].Name, response.Series[i].ID, response.Series[j].Name, response.Series[j].ID)
	})
	sort.Slice(response.Versions, func(i, j int) bool {
		leftReleasedOn := ""
		if response.Versions[i].ReleasedOn != nil {
			leftReleasedOn = *response.Versions[i].ReleasedOn
		}
		rightReleasedOn := ""
		if response.Versions[j].ReleasedOn != nil {
			rightReleasedOn = *response.Versions[j].ReleasedOn
		}
		if leftReleasedOn != rightReleasedOn {
			return leftReleasedOn > rightReleasedOn
		}
		return catalogLess(response.Versions[i].Name, response.Versions[i].ID, response.Versions[j].Name, response.Versions[j].ID)
	})

	return re.JSON(http.StatusOK, response)
}

func catalogLocale(value string) (string, bool) {
	switch value {
	case "en-US", "ko-KR", "ja-JP":
		return value, true
	default:
		return "", false
	}
}

func catalogSeriesName(record *core.Record, locale string) string {
	switch locale {
	case "ko-KR":
		return firstCatalogName(record.GetString("kr_short"), record.GetString("kr"), record.GetString("en_short"), record.GetString("en"), record.GetString("jp"))
	case "ja-JP":
		return firstCatalogName(record.GetString("jp_short"), record.GetString("jp"), record.GetString("en_short"), record.GetString("en"), record.GetString("kr"))
	default:
		return firstCatalogName(record.GetString("en_short"), record.GetString("en"), record.GetString("kr"), record.GetString("jp"))
	}
}

func catalogName(record *core.Record, locale string) string {
	switch locale {
	case "ko-KR":
		return firstCatalogName(record.GetString("kr"), record.GetString("en"), record.GetString("jp"))
	case "ja-JP":
		return firstCatalogName(record.GetString("jp"), record.GetString("en"), record.GetString("kr"))
	default:
		return firstCatalogName(record.GetString("en"), record.GetString("kr"), record.GetString("jp"))
	}
}

func firstCatalogName(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "Untitled"
}

func catalogNullableString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func catalogLess(leftName, leftID, rightName, rightID string) bool {
	left := strings.ToLower(leftName)
	right := strings.ToLower(rightName)
	if left == right {
		return leftID < rightID
	}
	return left < right
}
