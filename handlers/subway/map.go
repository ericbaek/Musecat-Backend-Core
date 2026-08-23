package subway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gabriel-vasile/mimetype"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	collectionSubwayMap = "subwayMap"
	maxMapFileSize      = 5 << 20
	maxChangelogSize    = 2_000_000
)

var subwayMapFileFields = map[string]string{
	"lightSVG": "image/svg+xml",
	"darkSVG":  "image/svg+xml",
	"image":    "image/*",
	"file":     "application/pdf",
}

type mapFilePayload struct {
	Name    string `json:"name"`
	FileURL string `json:"file_url"`
}

type mapPayload struct {
	ID          string          `json:"id"`
	Region      string          `json:"region"`
	ReleaseDate string          `json:"release_date"`
	Changelog   []string        `json:"changelog"`
	LightSVG    *mapFilePayload `json:"light_svg"`
	DarkSVG     *mapFilePayload `json:"dark_svg"`
	Image       *mapFilePayload `json:"image"`
	Document    *mapFilePayload `json:"document"`
	Created     string          `json:"created"`
	Updated     string          `json:"updated"`
}

type mapMutationInput struct {
	ID             string
	Region         string
	HasRegion      bool
	ReleaseDate    time.Time
	HasReleaseDate bool
	Changelog      []string
	HasChangelog   bool
	Files          map[string]*filesystem.File
}

// GetMap returns the newest subway map version for a supported region.
func GetMap(re *core.RequestEvent) error {
	region := strings.TrimSpace(re.Request.URL.Query().Get("region"))
	if !isSupportedRegion(region) {
		return validationError(re, "region must be one of center or busan")
	}

	records, err := re.App.FindRecordsByFilter(
		collectionSubwayMap,
		"region = {:region}",
		"-releaseDate,-created",
		1,
		0,
		dbx.Params{"region": region},
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load subway map",
			"details": err.Error(),
		})
	}
	if len(records) == 0 {
		return re.NotFoundError("subway map not found", nil)
	}

	return re.JSON(http.StatusOK, buildMapPayload(records[0]))
}

// CreateMap creates a new version without replacing older regional versions.
func CreateMap(re *core.RequestEvent) error {
	input, err := parseMapMutationInput(re)
	if err != nil {
		return validationError(re, err.Error())
	}
	if !input.HasRegion || !isSupportedRegion(input.Region) {
		return validationError(re, "region must be one of center or busan")
	}
	if !input.HasReleaseDate {
		return validationError(re, "releaseDate is required and must use YYYY-MM-DD")
	}
	if !hasDisplayAsset(nil, input.Files) {
		return validationError(re, "at least one display asset is required")
	}

	collection, err := re.App.FindCollectionByNameOrId(collectionSubwayMap)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load subway map collection",
			"details": err.Error(),
		})
	}

	record := core.NewRecord(collection)
	applyMapMutation(record, input)
	if err := re.App.Save(record); err != nil {
		return validationError(re, "failed to save subway map: "+err.Error())
	}

	return re.JSON(http.StatusCreated, buildMapPayload(record))
}

// UpdateMap updates the explicitly selected version. Omitted files and fields
// retain their current values.
func UpdateMap(re *core.RequestEvent) error {
	input, err := parseMapMutationInput(re)
	if err != nil {
		return validationError(re, err.Error())
	}
	if input.ID == "" {
		return validationError(re, "id is required")
	}
	if input.HasRegion && !isSupportedRegion(input.Region) {
		return validationError(re, "region must be one of center or busan")
	}

	record, err := re.App.FindRecordById(collectionSubwayMap, input.ID)
	if err != nil {
		return re.NotFoundError("subway map not found", nil)
	}
	if !hasDisplayAsset(record, input.Files) {
		return validationError(re, "at least one display asset is required")
	}

	applyMapMutation(record, input)
	if err := re.App.Save(record); err != nil {
		return validationError(re, "failed to save subway map: "+err.Error())
	}

	return re.JSON(http.StatusOK, buildMapPayload(record))
}

// DeleteMap hard-deletes one version so the previous regional version becomes
// current on the next GET request.
func DeleteMap(re *core.RequestEvent) error {
	id := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	if id == "" {
		return validationError(re, "id is required")
	}

	record, err := re.App.FindRecordById(collectionSubwayMap, id)
	if err != nil {
		return re.NotFoundError("subway map not found", nil)
	}
	if err := re.App.Delete(record); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to delete subway map",
			"details": err.Error(),
		})
	}

	return re.JSON(http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// DownloadMapFile streams only the four documented subway map file fields.
func DownloadMapFile(re *core.RequestEvent) error {
	id := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	if id == "" {
		return validationError(re, "id is required")
	}
	field := strings.TrimSpace(re.Request.URL.Query().Get("field"))
	if _, ok := subwayMapFileFields[field]; !ok {
		return validationError(re, "field must be one of lightSVG, darkSVG, image, or file")
	}

	record, err := re.App.FindRecordById(collectionSubwayMap, id)
	if err != nil {
		return re.NotFoundError("subway map not found", nil)
	}
	filename := record.GetString(field)
	if filename == "" {
		return re.NotFoundError("subway map file not found", nil)
	}

	fsys, err := re.App.NewFilesystem()
	if err != nil {
		return re.InternalServerError("failed to load subway map file", err)
	}
	defer fsys.Close()
	if err := fsys.Serve(re.Response, re.Request, record.BaseFilesPath()+"/"+filename, filename); err != nil {
		return re.NotFoundError("subway map file not found", err)
	}
	return nil
}

func parseMapMutationInput(re *core.RequestEvent) (mapMutationInput, error) {
	contentType := strings.ToLower(strings.TrimSpace(re.Request.Header.Get("Content-Type")))
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		return mapMutationInput{}, errors.New("multipart/form-data body is required")
	}
	if err := re.Request.ParseMultipartForm(24 << 20); err != nil {
		return mapMutationInput{}, fmt.Errorf("invalid multipart body: %w", err)
	}

	input := mapMutationInput{
		ID:    strings.TrimSpace(re.Request.FormValue("id")),
		Files: make(map[string]*filesystem.File, len(subwayMapFileFields)),
	}
	if values, ok := re.Request.MultipartForm.Value["region"]; ok {
		if len(values) != 1 {
			return mapMutationInput{}, errors.New("region must have exactly one value")
		}
		input.HasRegion = true
		input.Region = strings.TrimSpace(values[0])
	}
	if values, ok := re.Request.MultipartForm.Value["releaseDate"]; ok {
		if len(values) != 1 {
			return mapMutationInput{}, errors.New("releaseDate must have exactly one value")
		}
		parsed, err := time.Parse("2006-01-02", strings.TrimSpace(values[0]))
		if err != nil {
			return mapMutationInput{}, errors.New("releaseDate is required and must use YYYY-MM-DD")
		}
		input.HasReleaseDate = true
		input.ReleaseDate = parsed.UTC()
	}
	if values, ok := re.Request.MultipartForm.Value["changelog"]; ok {
		if len(values) != 1 {
			return mapMutationInput{}, errors.New("changelog must have exactly one value")
		}
		raw := []byte(strings.TrimSpace(values[0]))
		if len(raw) > maxChangelogSize {
			return mapMutationInput{}, fmt.Errorf("changelog must be at most %d bytes", maxChangelogSize)
		}
		if len(raw) == 0 || string(raw) == "null" {
			return mapMutationInput{}, errors.New("changelog must be a JSON string array")
		}
		if err := json.Unmarshal(raw, &input.Changelog); err != nil || input.Changelog == nil {
			return mapMutationInput{}, errors.New("changelog must be a JSON string array")
		}
		input.HasChangelog = true
	}

	for field, expectedMIME := range subwayMapFileFields {
		files, err := re.FindUploadedFiles(field)
		if err != nil {
			if errors.Is(err, http.ErrMissingFile) {
				continue
			}
			return mapMutationInput{}, fmt.Errorf("invalid %s file: %w", field, err)
		}
		if len(files) != 1 {
			return mapMutationInput{}, fmt.Errorf("%s must have at most one file", field)
		}
		if err := validateMapFile(field, expectedMIME, files[0]); err != nil {
			return mapMutationInput{}, err
		}
		input.Files[field] = files[0]
	}

	return input, nil
}

func validateMapFile(field, expectedMIME string, file *filesystem.File) error {
	if file.Size <= 0 {
		return fmt.Errorf("%s must not be empty", field)
	}
	if file.Size > maxMapFileSize {
		return fmt.Errorf("%s must be at most %d bytes", field, maxMapFileSize)
	}

	reader, err := file.Reader.Open()
	if err != nil {
		return fmt.Errorf("failed to inspect %s: %w", field, err)
	}
	defer reader.Close()
	detected, err := mimetype.DetectReader(reader)
	if err != nil {
		return fmt.Errorf("failed to inspect %s: %w", field, err)
	}

	if expectedMIME == "image/*" {
		if strings.HasPrefix(detected.String(), "image/") {
			return nil
		}
	} else if detected.Is(expectedMIME) {
		return nil
	}
	return fmt.Errorf("%s must use %s, detected %s", field, expectedMIME, detected.String())
}

func applyMapMutation(record *core.Record, input mapMutationInput) {
	if input.HasRegion {
		record.Set("region", input.Region)
	}
	if input.HasReleaseDate {
		record.Set("releaseDate", input.ReleaseDate)
	}
	if input.HasChangelog {
		raw, _ := json.Marshal(input.Changelog)
		record.Set("changelog", types.JSONRaw(raw))
	}
	for field, file := range input.Files {
		record.Set(field, file)
	}
}

func buildMapPayload(record *core.Record) mapPayload {
	return mapPayload{
		ID:          record.Id,
		Region:      strings.TrimSpace(record.GetString("region")),
		ReleaseDate: formatReleaseDate(record),
		Changelog:   recordChangelog(record),
		LightSVG:    buildMapFilePayload(record, "lightSVG"),
		DarkSVG:     buildMapFilePayload(record, "darkSVG"),
		Image:       buildMapFilePayload(record, "image"),
		Document:    buildMapFilePayload(record, "file"),
		Created:     formatRecordDateTime(record, "created"),
		Updated:     formatRecordDateTime(record, "updated"),
	}
}

func buildMapFilePayload(record *core.Record, field string) *mapFilePayload {
	filename := record.GetString(field)
	if filename == "" {
		return nil
	}
	query := url.Values{}
	query.Set("id", record.Id)
	query.Set("field", field)
	return &mapFilePayload{
		Name:    filename,
		FileURL: "/subway/map/file?" + query.Encode(),
	}
}

func recordChangelog(record *core.Record) []string {
	result := []string{}
	raw, err := json.Marshal(record.Get("changelog"))
	if err != nil || string(raw) == "null" {
		return result
	}
	if err := json.Unmarshal(raw, &result); err != nil || result == nil {
		return []string{}
	}
	return result
}

func formatReleaseDate(record *core.Record) string {
	value := record.GetDateTime("releaseDate")
	if value.IsZero() {
		return ""
	}
	return value.Time().UTC().Format("2006-01-02")
}

func formatRecordDateTime(record *core.Record, field string) string {
	value := record.GetDateTime(field)
	if value.IsZero() {
		return ""
	}
	return value.Time().UTC().Format(time.RFC3339Nano)
}

func isSupportedRegion(region string) bool {
	return region == "center" || region == "busan"
}

func hasDisplayAsset(record *core.Record, files map[string]*filesystem.File) bool {
	for _, field := range []string{"lightSVG", "darkSVG", "image"} {
		if files[field] != nil || (record != nil && record.GetString(field) != "") {
			return true
		}
	}
	return false
}

func validationError(re *core.RequestEvent, details string) error {
	return re.JSON(http.StatusBadRequest, map[string]any{
		"error":   "validation failed",
		"details": details,
	})
}
