package campaign

import (
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
)

const (
	photoCampaignStaleAfter  = 6 // months
	defaultPhotoCampaignPage = 50
	maxPhotoCampaignPage     = 100
)

// PhotoCampaignStatus is the server-side source of truth for photo campaign
// eligibility. Only the current arcade.photo molecule is considered; photos
// removed from the gallery remain public but do not keep the arcade fresh.
type PhotoCampaignStatus struct {
	Status            string
	LastPublicPhoto   time.Time
	RepresentativeURL string
}

type photoCampaignRow struct {
	arcade   map[string]any
	status   PhotoCampaignStatus
	location *arcadeinternal.Location
}

// GetPhotoCampaignStatus returns the current photo status for one arcade. A
// missing or stale status is eligible only for a public/open arcade.
func GetPhotoCampaignStatus(app core.App, arcadeID string, now time.Time) (PhotoCampaignStatus, error) {
	arcadeID = strings.TrimSpace(arcadeID)
	if arcadeID == "" {
		return PhotoCampaignStatus{}, fmt.Errorf("arcade id is required")
	}
	arcade, err := app.FindRecordById(arcadeinternal.CollectionArcade, arcadeID)
	if err != nil {
		return PhotoCampaignStatus{}, err
	}
	status := PhotoCampaignStatus{Status: "not_eligible"}
	if !arcade.GetBool("public") || arcade.GetBool("closed") {
		return status, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	photoID := strings.TrimSpace(arcade.GetString("photo"))
	if photoID == "" {
		status.Status = "missing"
		return status, nil
	}
	molecule, err := app.FindRecordById(arcadeinternal.CollectionArcadePhoto, photoID)
	if err != nil || molecule.GetString("arcade") != arcadeID {
		status.Status = "missing"
		return status, nil
	}

	for _, rawAtomID := range molecule.GetStringSlice("photos") {
		atomID := strings.TrimSpace(rawAtomID)
		if atomID == "" {
			continue
		}
		atom, err := app.FindRecordById(arcadeinternal.CollectionArcadePhotoAtoms, atomID)
		if err != nil || atom.GetString("arcade") != arcadeID || !atom.GetBool("public") || strings.TrimSpace(atom.GetString("photo")) == "" {
			continue
		}
		publishedAt := photoAtomTimestamp(atom)
		if publishedAt.After(status.LastPublicPhoto) {
			status.LastPublicPhoto = publishedAt
		}
		if status.RepresentativeURL == "" {
			status.RepresentativeURL = "/arcade/photo/file?id=" + url.QueryEscape(atom.Id)
		}
	}

	if status.LastPublicPhoto.IsZero() {
		status.Status = "missing"
		return status, nil
	}
	if !status.LastPublicPhoto.After(now.AddDate(0, -photoCampaignStaleAfter, 0)) {
		status.Status = "stale"
		return status, nil
	}
	status.Status = "current"
	return status, nil
}

// IsPhotoCampaignTarget reports whether an arcade is in the automatic photo
// campaign. It deliberately uses the same current-molecule calculation as the
// public campaign list and the photo publication XP transaction.
func IsPhotoCampaignTarget(app core.App, arcadeID string, now time.Time) (bool, error) {
	status, err := GetPhotoCampaignStatus(app, arcadeID, now)
	if err != nil {
		return false, err
	}
	return status.Status == "missing" || status.Status == "stale", nil
}

// ListPhotoCampaign handles GET /campaign/photo. It is intentionally a
// computed public endpoint rather than an arcade_campaign record: target
// membership follows the current public photo molecule in real time.
func ListPhotoCampaign(re *core.RequestEvent) error {
	page, perPage, err := parsePhotoCampaignPagination(re.Request.URL.Query().Get("page"), re.Request.URL.Query().Get("per_page"))
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	lat, lon, hasLocation, err := parsePhotoCampaignLocation(re.Request.URL.Query())
	if err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": err.Error()})
	}
	now := time.Now().UTC()
	candidates, err := arcadequery.GetArcadeCandidates(re.App)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load arcades", "details": err.Error()})
	}

	rows := make([]photoCampaignRow, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.Closed {
			continue
		}
		status, err := GetPhotoCampaignStatus(re.App, candidate.ID, now)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load photo campaign", "details": err.Error()})
		}
		if status.Status != "missing" && status.Status != "stale" {
			continue
		}
		rows = append(rows, photoCampaignRow{arcade: candidate.Summary(true, true), status: status, location: candidate.Location})
	}

	// Missing-photo venues have no timestamp and are shown first. Among stale
	// venues, the oldest public photo is the highest-priority target.
	sortPhotoCampaignRows(rows, lat, lon, hasLocation)
	total := len(rows)
	lastPage := 0
	if total > 0 {
		lastPage = (total + perPage - 1) / perPage
	}
	start := (page - 1) * perPage
	if start > total {
		start = total
	}
	end := start + perPage
	if end > total {
		end = total
	}
	items := make([]map[string]any, 0, end-start)
	for _, row := range rows[start:end] {
		item := map[string]any{
			"arcade":       row.arcade,
			"photo_status": row.status.Status,
			"photo_url":    row.status.RepresentativeURL,
		}
		if row.status.LastPublicPhoto.IsZero() {
			item["last_public_photo_at"] = nil
		} else {
			item["last_public_photo_at"] = row.status.LastPublicPhoto.Format(time.RFC3339Nano)
		}
		items = append(items, item)
	}
	re.Response.Header().Set("Cache-Control", "public, max-age=60")
	return re.JSON(http.StatusOK, map[string]any{
		"page":      page,
		"per_page":  perPage,
		"last_page": lastPage,
		"total":     total,
		"items":     items,
	})
}

func parsePhotoCampaignPagination(rawPage, rawPerPage string) (int, int, error) {
	page, perPage := 1, defaultPhotoCampaignPage
	if strings.TrimSpace(rawPage) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(rawPage))
		if err != nil || parsed < 1 {
			return 0, 0, fmt.Errorf("page must be a positive integer")
		}
		page = parsed
	}
	if strings.TrimSpace(rawPerPage) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(rawPerPage))
		if err != nil || parsed < 1 || parsed > maxPhotoCampaignPage {
			return 0, 0, fmt.Errorf("per_page must be between 1 and %d", maxPhotoCampaignPage)
		}
		perPage = parsed
	}
	return page, perPage, nil
}

func parsePhotoCampaignLocation(query url.Values) (float64, float64, bool, error) {
	latRaw, lonRaw := strings.TrimSpace(query.Get("lat")), strings.TrimSpace(query.Get("lon"))
	if latRaw == "" && lonRaw == "" {
		return 0, 0, false, nil
	}
	if latRaw == "" || lonRaw == "" {
		return 0, 0, false, fmt.Errorf("lat and lon query params must be provided together")
	}
	lat, err := strconv.ParseFloat(latRaw, 64)
	if err != nil || math.IsInf(lat, 0) {
		return 0, 0, false, fmt.Errorf("invalid lat")
	}
	lon, err := strconv.ParseFloat(lonRaw, 64)
	if err != nil || math.IsInf(lon, 0) {
		return 0, 0, false, fmt.Errorf("invalid lon")
	}
	if err := arcadeinternal.ValidateLocationCoords(lat, lon); err != nil {
		return 0, 0, false, err
	}
	return lat, lon, true, nil
}

func sortPhotoCampaignRows(rows []photoCampaignRow, lat, lon float64, hasLocation bool) {
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if hasLocation {
			leftDistance, rightDistance := math.Inf(1), math.Inf(1)
			if left.location != nil {
				leftDistance = arcadeinternal.DistanceKm(lat, lon, left.location.Lat, left.location.Lon)
			}
			if right.location != nil {
				rightDistance = arcadeinternal.DistanceKm(lat, lon, right.location.Lat, right.location.Lon)
			}
			if leftDistance != rightDistance {
				return leftDistance < rightDistance
			}
		}
		leftMissing := left.status.Status == "missing"
		rightMissing := right.status.Status == "missing"
		if leftMissing != rightMissing {
			return leftMissing
		}
		if !left.status.LastPublicPhoto.Equal(right.status.LastPublicPhoto) {
			if left.status.LastPublicPhoto.IsZero() {
				return true
			}
			if right.status.LastPublicPhoto.IsZero() {
				return false
			}
			return left.status.LastPublicPhoto.Before(right.status.LastPublicPhoto)
		}
		leftID, _ := left.arcade["id"].(string)
		rightID, _ := right.arcade["id"].(string)
		return leftID < rightID
	})
}

func photoAtomTimestamp(atom *core.Record) time.Time {
	if atom == nil {
		return time.Time{}
	}
	// Core's fresh bootstrap currently stores atom creation time only. Prefer
	// an updated value when a deployment supplies it; published atoms are
	// immutable, so this fallback remains stable and migration-free.
	if updated := atom.GetDateTime("updated").Time().UTC(); !updated.IsZero() {
		return updated
	}
	return atom.GetDateTime("created").Time().UTC()
}
