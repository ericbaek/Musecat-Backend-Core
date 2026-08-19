package campaign

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	arcadegame "github.com/ericbaek/musecat-backend-core/handlers/arcade/game"
	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
	arcadequery "github.com/ericbaek/musecat-backend-core/handlers/arcade/query"
	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const (
	statusDraft    = "draft"
	statusActive   = "active"
	statusEnded    = "ended"
	resultStillOld = "still_old"
	resultUpdated  = "updated"
)

type campaignMutationBody struct {
	ID           string   `json:"id,omitempty"`
	FromVersion  string   `json:"from_version"`
	ToVersion    string   `json:"to_version"`
	CabinetScope string   `json:"cabinet_scope"`
	Cabinets     []string `json:"cabinets"`
	CountryScope string   `json:"country_scope"`
	Countries    []string `json:"countries"`
	RewardExp    int      `json:"reward_exp"`
	Status       string   `json:"status"`
	StartAt      string   `json:"start_at"`
	EndAt        string   `json:"end_at"`
}

type campaignCheckBody struct {
	Campaign string `json:"campaign"`
	Arcade   string `json:"arcade"`
	GameID   string `json:"game_id"`
	Result   string `json:"result"`
}

type campaignEndBody struct {
	ID string `json:"id"`
}

type campaignConfig struct {
	ID           string
	FromVersion  string
	ToVersion    string
	CabinetScope string
	Cabinets     []string
	CountryScope string
	Countries    []string
	RewardExp    int
	Status       string
	StartAt      time.Time
	EndAt        time.Time
}

type candidateRow struct {
	ArcadeID       string
	Country        string
	Name           string
	Address        string
	ArcadeLocation string
	StateID        string
	GameID         string
	VersionID      string
	CabinetID      string
	GameLocation   string
	Quantity       int
}

func ListCampaigns(re *core.RequestEvent) error {
	now := time.Now().UTC()
	records, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeCampaign,
		"status={:status} && start_at <= {:now} && end_at >= {:now}",
		"-start_at",
		20,
		0,
		dbx.Params{"status": statusActive, "now": now},
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load campaigns", "details": err.Error()})
	}

	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		config, err := loadCampaignConfig(record)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to decode campaign", "details": err.Error()})
		}
		rows, err := loadCandidateRows(re.App, config)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to count campaign targets", "details": err.Error()})
		}
		item, err := buildCampaignSummary(re.App, config, len(rows))
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to build campaign", "details": err.Error()})
		}
		items = append(items, item)
	}
	return re.JSON(http.StatusOK, map[string]any{"items": items})
}

func GetCampaign(re *core.RequestEvent) error {
	id := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	if id == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "campaign is required"})
	}
	record, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeCampaign, id)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "campaign not found"})
	}
	config, err := loadCampaignConfig(record)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to decode campaign", "details": err.Error()})
	}
	if !campaignIsActive(config, time.Now().UTC()) {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "campaign not found"})
	}
	rows, err := loadCandidateRows(re.App, config)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load campaign targets", "details": err.Error()})
	}
	items, err := buildCampaignItems(re.App, rows)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to build campaign targets", "details": err.Error()})
	}
	summary, err := buildCampaignSummary(re.App, config, len(items))
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to build campaign", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, map[string]any{"campaign": summary, "items": items})
}

// ListArcadeCampaigns returns active campaign prompts for the public/open
// arcade identified by id. Keeping this as a dedicated aggregate endpoint lets
// the arcade detail render its prompts without exposing raw campaign records.
func ListArcadeCampaigns(re *core.RequestEvent) error {
	arcadeID := strings.TrimSpace(re.Request.URL.Query().Get("id"))
	if arcadeID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "arcade id is required"})
	}
	now := time.Now().UTC()
	records, err := re.App.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeCampaign,
		"status={:status} && start_at <= {:now} && end_at >= {:now}",
		"-start_at",
		20,
		0,
		dbx.Params{"status": statusActive, "now": now},
	)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load arcade campaigns", "details": err.Error()})
	}

	items := make([]map[string]any, 0)
	for _, record := range records {
		config, err := loadCampaignConfig(record)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to decode campaign", "details": err.Error()})
		}
		rows, err := loadCandidateRowsForArcade(re.App, config, arcadeID)
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load campaign targets", "details": err.Error()})
		}
		summary, err := buildCampaignSummary(re.App, config, len(rows))
		if err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to build campaign", "details": err.Error()})
		}
		for _, row := range rows {
			targets, err := buildCampaignItems(re.App, []candidateRow{row})
			if err != nil {
				return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to build campaign target", "details": err.Error()})
			}
			items = append(items, map[string]any{"campaign": summary, "target": targets[0]})
		}
	}
	return re.JSON(http.StatusOK, map[string]any{"items": items})
}

func CreateCampaign(re *core.RequestEvent) error {
	var body campaignMutationBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	if err := normalizeAndValidateCampaignBody(re.App, &body, false); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": err.Error()})
	}

	record, err := re.App.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeCampaign)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to load campaign collection", "details": err.Error()})
	}
	campaign := core.NewRecord(record)
	applyCampaignBody(campaign, body, re.Auth.Id)
	if err := re.App.Save(campaign); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to create campaign", "details": err.Error()})
	}
	config, err := loadCampaignConfig(campaign)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to read created campaign", "details": err.Error()})
	}
	summary, err := buildCampaignSummary(re.App, config, 0)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to build created campaign", "details": err.Error()})
	}
	return re.JSON(http.StatusCreated, map[string]any{"campaign": summary})
}

func UpdateCampaign(re *core.RequestEvent) error {
	var body campaignMutationBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	body.ID = strings.TrimSpace(body.ID)
	if body.ID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "id is required"})
	}
	if err := normalizeAndValidateCampaignBody(re.App, &body, true); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "validation failed", "details": err.Error()})
	}
	campaign, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeCampaign, body.ID)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "campaign not found"})
	}
	applyCampaignBody(campaign, body, campaign.GetString("created_by"))
	if err := re.App.Save(campaign); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to update campaign", "details": err.Error()})
	}
	config, err := loadCampaignConfig(campaign)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to read updated campaign", "details": err.Error()})
	}
	summary, err := buildCampaignSummary(re.App, config, 0)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to build updated campaign", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, map[string]any{"campaign": summary})
}

func EndCampaign(re *core.RequestEvent) error {
	var body campaignEndBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	body.ID = strings.TrimSpace(body.ID)
	if body.ID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "id is required"})
	}
	campaign, err := re.App.FindRecordById(arcadeinternal.CollectionArcadeCampaign, body.ID)
	if err != nil {
		return re.JSON(http.StatusNotFound, map[string]any{"error": "campaign not found"})
	}
	campaign.Set("status", statusEnded)
	if err := re.App.Save(campaign); err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to end campaign", "details": err.Error()})
	}
	config, err := loadCampaignConfig(campaign)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to read ended campaign", "details": err.Error()})
	}
	summary, err := buildCampaignSummary(re.App, config, 0)
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "failed to build ended campaign", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, map[string]any{"campaign": summary})
}

func CheckCampaign(re *core.RequestEvent) error {
	var body campaignCheckBody
	if err := json.NewDecoder(re.Request.Body).Decode(&body); err != nil {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "invalid JSON body", "details": err.Error()})
	}
	body.Campaign = strings.TrimSpace(body.Campaign)
	body.Arcade = strings.TrimSpace(body.Arcade)
	body.GameID = strings.TrimSpace(body.GameID)
	body.Result = strings.TrimSpace(body.Result)
	if body.Campaign == "" || body.Arcade == "" || body.GameID == "" {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "campaign, arcade, and game_id are required"})
	}
	if body.Result != resultStillOld && body.Result != resultUpdated {
		return re.JSON(http.StatusBadRequest, map[string]any{"error": "result must be still_old or updated"})
	}

	var response map[string]any
	err := re.App.RunInTransaction(func(tx core.App) error {
		campaign, err := tx.FindRecordById(arcadeinternal.CollectionArcadeCampaign, body.Campaign)
		if err != nil {
			return httpError{status: http.StatusNotFound, message: "campaign not found"}
		}
		config, err := loadCampaignConfig(campaign)
		if err != nil {
			return err
		}
		if !campaignIsActive(config, time.Now().UTC()) {
			return httpError{status: http.StatusConflict, message: "campaign is not active"}
		}
		arcade, err := tx.FindRecordById(arcadeinternal.CollectionArcade, body.Arcade)
		if err != nil || !arcade.GetBool("public") || arcade.GetBool("closed") {
			return httpError{status: http.StatusNotFound, message: "arcade not found"}
		}
		if !countryMatches(config, arcade.GetString("country")) {
			return httpError{status: http.StatusConflict, message: "arcade is outside campaign country scope"}
		}
		if err := requireSameDayVisit(tx, re.Auth.Id, arcade); err != nil {
			return httpError{status: http.StatusForbidden, message: err.Error()}
		}

		stateID := strings.TrimSpace(arcade.GetString("game_v2"))
		revision, err := tx.FindFirstRecordByFilter(
			arcadeinternal.CollectionArcadeGameRevision,
			"batch={:batch} && entry={:entry}",
			dbx.Params{"batch": stateID, "entry": body.GameID},
		)
		if err != nil {
			return httpError{status: http.StatusConflict, message: "game is no longer a campaign target"}
		}
		if err := validateRevisionTarget(tx, config, revision); err != nil {
			return httpError{status: http.StatusConflict, message: err.Error()}
		}

		baseExp, err := userhandler.LoadCurrentExp(tx, re.Auth.Id)
		if err != nil {
			return err
		}
		check, checkErr := findCampaignCheck(tx, config.ID, re.Auth.Id, body.GameID)
		if checkErr != nil {
			return checkErr
		}
		if body.Result == resultStillOld {
			if revision.GetString("version") != config.FromVersion {
				return httpError{status: http.StatusConflict, message: "game is already updated"}
			}
			if check != nil && check.GetString("result") == resultUpdated {
				return httpError{status: http.StatusConflict, message: "campaign check is already completed"}
			}
			if err := saveCampaignCheck(tx, check, config.ID, body.Arcade, body.GameID, re.Auth.Id, resultStillOld, stateID); err != nil {
				return err
			}
			response = campaignCheckResponse(baseExp, baseExp, false, resultStillOld, stateID, nil)
			return nil
		}

		currentStateID := stateID
		if revision.GetString("version") == config.FromVersion {
			update, err := arcadegame.BuildUpdateBodyFromCurrentState(tx, body.Arcade)
			if err != nil {
				return err
			}
			found := false
			for i := range update.Games {
				if update.Games[i].ID != body.GameID {
					continue
				}
				update.Games[i].Game = config.ToVersion
				found = true
				break
			}
			if !found {
				return httpError{status: http.StatusConflict, message: "game is no longer a campaign target"}
			}
			currentStateID, err = arcadegame.UpdateArcadeGameTxFromExistingAtoms(tx, update, re.Auth.Id, "campaign_update")
			if err != nil {
				return err
			}
		}

		currentExp := baseExp
		granted := false
		if check == nil || check.GetString("result") != resultUpdated {
			currentExp, granted, err = userhandler.AwardExpTx(tx, re.Auth.Id, campaignXPKind(config.ID, body.GameID), config.RewardExp, baseExp)
			if err != nil {
				return err
			}
		}
		if err := saveCampaignCheck(tx, check, config.ID, body.Arcade, body.GameID, re.Auth.Id, resultUpdated, currentStateID); err != nil {
			return err
		}
		response = campaignCheckResponse(baseExp, currentExp, granted, resultUpdated, currentStateID, map[string]any{"arcade": body.Arcade, "game_id": body.GameID})
		return nil
	})
	if err != nil {
		if apiErr, ok := err.(httpError); ok {
			return re.JSON(apiErr.status, map[string]any{"error": apiErr.message})
		}
		return re.JSON(http.StatusBadGateway, map[string]any{"error": "campaign check failed", "details": err.Error()})
	}
	return re.JSON(http.StatusOK, response)
}

type httpError struct {
	status  int
	message string
}

func (e httpError) Error() string { return e.message }

func normalizeAndValidateCampaignBody(app core.App, body *campaignMutationBody, requireID bool) error {
	body.FromVersion = strings.TrimSpace(body.FromVersion)
	body.ToVersion = strings.TrimSpace(body.ToVersion)
	body.CabinetScope = strings.TrimSpace(body.CabinetScope)
	body.CountryScope = strings.TrimSpace(body.CountryScope)
	body.Status = strings.TrimSpace(body.Status)
	if body.Status == "" {
		body.Status = statusActive
	}
	if body.CabinetScope == "" {
		body.CabinetScope = "all"
	}
	if body.CountryScope == "" {
		body.CountryScope = "all"
	}
	if body.RewardExp <= 0 {
		return fmt.Errorf("reward_exp must be positive")
	}
	if requireID && strings.TrimSpace(body.ID) == "" {
		return fmt.Errorf("id is required")
	}
	if body.FromVersion == "" || body.ToVersion == "" || body.FromVersion == body.ToVersion {
		return fmt.Errorf("from_version and to_version must be different")
	}
	if body.CabinetScope != "all" && body.CabinetScope != "include" {
		return fmt.Errorf("cabinet_scope must be all or include")
	}
	if body.CabinetScope == "include" && len(body.Cabinets) == 0 {
		return fmt.Errorf("cabinets is required for include cabinet scope")
	}
	if body.CountryScope != "all" && body.CountryScope != "include" && body.CountryScope != "exclude" {
		return fmt.Errorf("country_scope must be all, include, or exclude")
	}
	if body.CountryScope != "all" && len(body.Countries) == 0 {
		return fmt.Errorf("countries is required for country scope")
	}
	if body.Status != statusDraft && body.Status != statusActive && body.Status != statusEnded {
		return fmt.Errorf("status must be draft, active, or ended")
	}
	startAt, err := parseCampaignTime(body.StartAt)
	if err != nil {
		return fmt.Errorf("start_at is invalid: %w", err)
	}
	endAt, err := parseCampaignTime(body.EndAt)
	if err != nil {
		return fmt.Errorf("end_at is invalid: %w", err)
	}
	if !endAt.After(startAt) {
		return fmt.Errorf("end_at must be after start_at")
	}
	body.StartAt = startAt.UTC().Format(time.RFC3339)
	body.EndAt = endAt.UTC().Format(time.RFC3339)
	body.Cabinets = normalizeIDs(body.Cabinets)
	body.Countries = normalizeCountries(body.Countries)
	if body.CountryScope != "all" {
		for _, country := range body.Countries {
			if len(country) != 2 {
				return fmt.Errorf("countries must contain ISO alpha-2 codes")
			}
		}
	}
	from, err := app.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, body.FromVersion)
	if err != nil {
		return fmt.Errorf("from_version not found")
	}
	to, err := app.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, body.ToVersion)
	if err != nil {
		return fmt.Errorf("to_version not found")
	}
	if strings.TrimSpace(from.GetString("series")) == "" || from.GetString("series") != to.GetString("series") {
		return fmt.Errorf("from_version and to_version must belong to the same series")
	}
	for _, cabinetID := range body.Cabinets {
		if _, err := app.FindRecordById(arcadeinternal.CollectionGameCabinet, cabinetID); err != nil {
			return fmt.Errorf("cabinet %s not found", cabinetID)
		}
		for _, versionID := range []string{body.FromVersion, body.ToVersion} {
			if _, err := app.FindFirstRecordByFilter(arcadeinternal.CollectionGameSeriesVersionCabinet, "version={:version} && cabinet={:cabinet}", dbx.Params{"version": versionID, "cabinet": cabinetID}); err != nil {
				return fmt.Errorf("cabinet %s is not compatible with version %s", cabinetID, versionID)
			}
		}
	}
	return nil
}

func applyCampaignBody(record *core.Record, body campaignMutationBody, createdBy string) {
	record.Set("from_version", body.FromVersion)
	record.Set("to_version", body.ToVersion)
	record.Set("cabinet_scope", body.CabinetScope)
	record.Set("cabinets", body.Cabinets)
	record.Set("country_scope", body.CountryScope)
	record.Set("countries", body.Countries)
	record.Set("reward_exp", body.RewardExp)
	record.Set("status", body.Status)
	record.Set("start_at", body.StartAt)
	record.Set("end_at", body.EndAt)
	if strings.TrimSpace(createdBy) != "" {
		record.Set("created_by", strings.TrimSpace(createdBy))
	}
}

func loadCampaignConfig(record *core.Record) (campaignConfig, error) {
	if record == nil {
		return campaignConfig{}, fmt.Errorf("campaign record is required")
	}
	startAt := record.GetDateTime("start_at").Time().UTC()
	endAt := record.GetDateTime("end_at").Time().UTC()
	if startAt.IsZero() || endAt.IsZero() {
		return campaignConfig{}, fmt.Errorf("campaign dates are invalid")
	}
	return campaignConfig{
		ID:          record.Id,
		FromVersion: record.GetString("from_version"), ToVersion: record.GetString("to_version"),
		CabinetScope: record.GetString("cabinet_scope"), Cabinets: normalizeIDs(record.GetStringSlice("cabinets")),
		CountryScope: record.GetString("country_scope"), Countries: normalizeCountries(readStringArray(record.Get("countries"))),
		RewardExp: record.GetInt("reward_exp"), Status: record.GetString("status"), StartAt: startAt, EndAt: endAt,
	}, nil
}

func buildCampaignSummary(app core.App, config campaignConfig, targetCount int) (map[string]any, error) {
	from, err := arcadequery.BuildGameSeriesBundle(app, config.FromVersion)
	if err != nil {
		return nil, err
	}
	to, err := arcadequery.BuildGameSeriesBundle(app, config.ToVersion)
	if err != nil {
		return nil, err
	}
	cabinets := make([]map[string]any, 0, len(config.Cabinets))
	for _, id := range config.Cabinets {
		record, err := app.FindRecordById(arcadeinternal.CollectionGameCabinet, id)
		if err != nil {
			return nil, err
		}
		cabinets = append(cabinets, cabinetObject(record))
	}
	return map[string]any{
		"id":           config.ID,
		"from_version": from, "to_version": to,
		"cabinet_scope": config.CabinetScope, "cabinets": cabinets,
		"country_scope": config.CountryScope, "countries": config.Countries,
		"reward_exp": config.RewardExp, "status": config.Status,
		"start_at": config.StartAt.Format(time.RFC3339), "end_at": config.EndAt.Format(time.RFC3339),
		"target_count": targetCount,
	}, nil
}

func buildCampaignItems(app core.App, rows []candidateRow) ([]map[string]any, error) {
	items := make([]map[string]any, 0, len(rows))
	versionCache := map[string]map[string]any{}
	cabinetCache := map[string]map[string]any{}
	for _, row := range rows {
		version, ok := versionCache[row.VersionID]
		if !ok {
			bundle, err := arcadequery.BuildGameSeriesBundle(app, row.VersionID)
			if err != nil {
				return nil, err
			}
			version = bundle
			versionCache[row.VersionID] = bundle
		}
		var cabinet any
		if row.CabinetID != "" {
			obj, ok := cabinetCache[row.CabinetID]
			if !ok {
				record, err := app.FindRecordById(arcadeinternal.CollectionGameCabinet, row.CabinetID)
				if err != nil {
					return nil, err
				}
				obj = cabinetObject(record)
				cabinetCache[row.CabinetID] = obj
			}
			cabinet = obj
		}
		items = append(items, map[string]any{
			"arcade": map[string]any{
				"id": row.ArcadeID, "country": row.Country, "name": row.Name, "address": row.Address,
				"location": decodeJSONValue(row.ArcadeLocation),
			},
			"game": map[string]any{
				"id": row.GameID, "state_id": row.StateID, "version": version["version"], "series": version["series"],
				"cabinet": cabinet, "location": row.GameLocation, "quantity": row.Quantity,
			},
		})
	}
	return items, nil
}

func loadCandidateRows(app core.App, config campaignConfig) ([]candidateRow, error) {
	return loadCandidateRowsForArcade(app, config, "")
}

func loadCandidateRowsForArcade(app core.App, config campaignConfig, arcadeID string) ([]candidateRow, error) {
	clauses := []string{
		"a.public = 1", "a.closed = 0", "a.game_v2 <> ''",
		"r.version = {:from_version}",
		"(TRIM(COALESCE(r.cabinet, '')) = '' OR EXISTS (SELECT 1 FROM game_series_version_cabinet target_support WHERE target_support.version = {:to_version} AND target_support.cabinet = r.cabinet))",
	}
	params := dbx.Params{"from_version": config.FromVersion, "to_version": config.ToVersion}
	if arcadeID = strings.TrimSpace(arcadeID); arcadeID != "" {
		clauses = append(clauses, "a.id = {:arcade_id}")
		params["arcade_id"] = arcadeID
	}
	if config.CabinetScope == "include" {
		placeholders := make([]string, 0, len(config.Cabinets))
		for i, id := range config.Cabinets {
			key := "cabinet_" + strconv.Itoa(i)
			placeholders = append(placeholders, "{:"+key+"}")
			params[key] = id
		}
		clauses = append(clauses, "r.cabinet IN ("+strings.Join(placeholders, ",")+")")
	}
	if config.CountryScope != "all" {
		placeholders := make([]string, 0, len(config.Countries))
		for i, country := range config.Countries {
			key := "country_" + strconv.Itoa(i)
			placeholders = append(placeholders, "{:"+key+"}")
			params[key] = country
		}
		operator := "IN"
		if config.CountryScope == "exclude" {
			operator = "NOT IN"
		}
		clauses = append(clauses, "UPPER(TRIM(a.country)) "+operator+" ("+strings.Join(placeholders, ",")+")")
	}
	query := `
SELECT a.id AS arcade_id, a.country, a.game_v2 AS state_id,
       b.name, b.address, b.location AS arcade_location,
       r.entry AS game_id, r.version AS version_id, r.cabinet AS cabinet_id,
       r.location AS game_location, r.quantity
FROM arcade a
INNER JOIN arcade_basic b ON b.id = a.basic
INNER JOIN arcade_game_history r ON r.batch = a.game_v2
WHERE ` + strings.Join(clauses, " AND ") + `
ORDER BY UPPER(TRIM(a.country)), b.name, a.id, r.entry`
	rows, err := app.DB().NewQuery(query).Bind(params).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]candidateRow, 0)
	for rows.Next() {
		raw := dbx.NullStringMap{}
		if err := rows.ScanMap(raw); err != nil {
			return nil, err
		}
		result = append(result, candidateRow{
			ArcadeID: nullString(raw, "arcade_id"), Country: nullString(raw, "country"), Name: nullString(raw, "name"), Address: nullString(raw, "address"), ArcadeLocation: nullString(raw, "arcade_location"), StateID: nullString(raw, "state_id"), GameID: nullString(raw, "game_id"), VersionID: nullString(raw, "version_id"), CabinetID: nullString(raw, "cabinet_id"), GameLocation: nullString(raw, "game_location"), Quantity: nullInt(raw, "quantity"),
		})
	}
	return result, rows.Err()
}

func validateRevisionTarget(app core.App, config campaignConfig, revision *core.Record) error {
	if revision == nil {
		return fmt.Errorf("game is no longer a campaign target")
	}
	if revision.GetString("version") != config.FromVersion && revision.GetString("version") != config.ToVersion {
		return fmt.Errorf("game is no longer a campaign target")
	}
	if config.CabinetScope == "include" && !contains(config.Cabinets, strings.TrimSpace(revision.GetString("cabinet"))) {
		return fmt.Errorf("game cabinet is outside campaign scope")
	}
	if revision.GetString("version") == config.FromVersion && strings.TrimSpace(revision.GetString("cabinet")) != "" {
		if _, err := app.FindFirstRecordByFilter(arcadeinternal.CollectionGameSeriesVersionCabinet, "version={:version} && cabinet={:cabinet}", dbx.Params{"version": config.ToVersion, "cabinet": revision.GetString("cabinet")}); err != nil {
			return fmt.Errorf("game cabinet is not compatible with campaign target version")
		}
	}
	return nil
}

func requireSameDayVisit(app core.App, userID string, arcade *core.Record) error {
	location, err := time.LoadLocation(strings.TrimSpace(arcade.GetString("timezone")))
	if err != nil {
		return fmt.Errorf("arcade timezone unavailable")
	}
	day := time.Now().UTC().In(location).Format("2006-01-02")
	visits, err := app.FindRecordsByFilter(userhandler.CollectionArcadeVisit, "user={:user} && arcade={:arcade} && visit_day={:day}", "", 1, 0, dbx.Params{"user": userID, "arcade": arcade.Id, "day": day})
	if err != nil {
		return fmt.Errorf("failed to verify arcade visit")
	}
	if len(visits) == 0 {
		return fmt.Errorf("a verified visit is required before campaign check")
	}
	return nil
}

func findCampaignCheck(app core.App, campaignID, userID, gameID string) (*core.Record, error) {
	checks, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcadeCampaignCheck, "campaign={:campaign} && user={:user} && game_id={:game_id}", "", 1, 0, dbx.Params{"campaign": campaignID, "user": userID, "game_id": gameID})
	if err != nil {
		return nil, err
	}
	if len(checks) == 0 {
		return nil, nil
	}
	return checks[0], nil
}

func saveCampaignCheck(app core.App, check *core.Record, campaignID, arcadeID, gameID, userID, result, stateID string) error {
	if check == nil {
		collection, err := app.FindCollectionByNameOrId(arcadeinternal.CollectionArcadeCampaignCheck)
		if err != nil {
			return err
		}
		check = core.NewRecord(collection)
		check.Set("campaign", campaignID)
		check.Set("arcade", arcadeID)
		check.Set("game_id", gameID)
		check.Set("user", userID)
	}
	check.Set("result", result)
	check.Set("state_id", stateID)
	return app.Save(check)
}

func campaignCheckResponse(previousExp, currentExp int, granted bool, result, stateID string, target map[string]any) map[string]any {
	response := map[string]any{
		"result": result, "updated": result == resultUpdated, "gained_exp": 0,
		"exp": currentExp, "level": userhandler.LevelFromExp(currentExp), "state_id": stateID,
		"xp_feedback": userhandler.BuildExpFeedback(previousExp, currentExp),
	}
	if granted {
		response["gained_exp"] = currentExp - previousExp
	}
	if target != nil {
		for key, value := range target {
			response[key] = value
		}
	}
	return response
}

func campaignXPKind(campaignID, gameID string) string {
	return "xp:campaign:" + campaignID + ":" + gameID
}

func campaignIsActive(config campaignConfig, now time.Time) bool {
	return config.Status == statusActive && !now.Before(config.StartAt) && !now.After(config.EndAt)
}

func countryMatches(config campaignConfig, raw string) bool {
	if config.CountryScope == "all" {
		return true
	}
	match := contains(config.Countries, strings.ToUpper(strings.TrimSpace(raw)))
	if config.CountryScope == "exclude" {
		return !match
	}
	return match
}

func parseCampaignTime(raw string) (time.Time, error) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, fmt.Errorf("value is required")
	}
	return time.Parse(time.RFC3339, strings.TrimSpace(raw))
}

func normalizeIDs(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			if _, ok := seen[value]; !ok {
				seen[value] = struct{}{}
				result = append(result, value)
			}
		}
	}
	sort.Strings(result)
	return result
}

func normalizeCountries(values []string) []string {
	result := normalizeIDs(values)
	for i := range result {
		result[i] = strings.ToUpper(result[i])
	}
	sort.Strings(result)
	return result
}

func readStringArray(raw any) []string {
	if raw == nil {
		return []string{}
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return []string{}
	}
	var result []string
	if json.Unmarshal(encoded, &result) == nil {
		return result
	}
	if text, ok := raw.(string); ok {
		_ = json.Unmarshal([]byte(text), &result)
	}
	return result
}

func cabinetObject(record *core.Record) map[string]any {
	return map[string]any{"id": record.Id, "en": record.GetString("en"), "kr": record.GetString("kr"), "jp": record.GetString("jp")}
}

func decodeJSONValue(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var value any
	if json.Unmarshal([]byte(raw), &value) == nil {
		return value
	}
	return nil
}

func nullString(raw dbx.NullStringMap, key string) string {
	value := raw[key]
	if !value.Valid {
		return ""
	}
	return strings.TrimSpace(value.String)
}

func nullInt(raw dbx.NullStringMap, key string) int {
	value := nullString(raw, key)
	parsed, _ := strconv.Atoi(value)
	return parsed
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
