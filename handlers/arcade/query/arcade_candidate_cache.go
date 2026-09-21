package query

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const arcadeCandidateCacheTTL = 30 * time.Minute
const arcadeCandidateCacheKey = "arcade_candidate_snapshot"

var arcadeCandidateCacheHookCollections = []string{
	arcadeinternal.CollectionArcade,
	arcadeinternal.CollectionArcadeBasic,
	arcadeinternal.CollectionArcadeGameEntry,
	arcadeinternal.CollectionArcadeGameRevisionBatch,
	arcadeinternal.CollectionArcadeGameRevision,
	arcadeinternal.CollectionGameSeriesVersion,
	arcadeinternal.CollectionGameCabinet,
	arcadeinternal.CollectionGameSeriesVersionCabinet,
}

type ArcadeCandidate struct {
	ID                string
	Country           string
	Closed            bool
	Name              string
	Address           string
	GameID            string
	Nicknames         []string
	GameSeries        []string
	GameInstallations []ArcadeGameInstallation
	Location          *arcadeinternal.Location
	NameNorm          string
	AddressNorm       string
	AddressAliasNorm  string
	NicknameNorms     []string
}

type ArcadeGameInstallation struct {
	SeriesID  string
	CabinetID string
	Quantity  int
}

func (c ArcadeCandidate) Summary(includeLocation bool, includeGameSeries bool) map[string]any {
	item := map[string]any{
		"id":       c.ID,
		"country":  c.Country,
		"name":     c.Name,
		"address":  c.Address,
		"nickname": cloneStringSliceOrEmpty(c.Nicknames),
		"closed":   c.Closed,
	}
	if includeLocation && c.Location != nil {
		item["location"] = map[string]any{
			"lat": c.Location.Lat,
			"lon": c.Location.Lon,
		}
	}
	if includeGameSeries {
		item["game_series"] = cloneStringSliceOrEmpty(c.GameSeries)
	}
	if c.GameID != "" {
		item["game"] = c.GameID
	}
	return item
}

type arcadeCandidateSnapshot struct {
	builtAt    time.Time
	candidates []ArcadeCandidate
}

var arcadeCandidateSnapshotCache sync.Map

func RegisterCandidateSnapshotHooks(app core.App) {
	if app == nil {
		return
	}

	for _, collectionName := range arcadeCandidateCacheHookCollections {
		collectionName := collectionName
		bindInvalidateHook(app, collectionName)
	}
}

func bindInvalidateHook(app core.App, collectionName string) {
	handlerID := "__arcadeCandidateSnapshotInvalidate_" + collectionName + "__"
	app.OnRecordAfterCreateSuccess(collectionName).Bind(&hook.Handler[*core.RecordEvent]{
		Id: handlerID + "create",
		Func: func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			InvalidateArcadeCandidateSnapshots(e.App)
			return nil
		},
	})
	app.OnRecordAfterUpdateSuccess(collectionName).Bind(&hook.Handler[*core.RecordEvent]{
		Id: handlerID + "update",
		Func: func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			InvalidateArcadeCandidateSnapshots(e.App)
			return nil
		},
	})
	app.OnRecordAfterDeleteSuccess(collectionName).Bind(&hook.Handler[*core.RecordEvent]{
		Id: handlerID + "delete",
		Func: func(e *core.RecordEvent) error {
			if err := e.Next(); err != nil {
				return err
			}
			InvalidateArcadeCandidateSnapshots(e.App)
			return nil
		},
	})
}

func InvalidateArcadeCandidateSnapshots(app core.App) {
	arcadeCandidateSnapshotCache.Delete(appCacheKey(app))
}

func GetArcadeCandidates(app core.App) ([]ArcadeCandidate, error) {
	if app == nil {
		return nil, fmt.Errorf("app is required")
	}

	now := time.Now().UTC()
	key := appCacheKey(app)
	if cached, ok := arcadeCandidateSnapshotCache.Load(key); ok {
		entry := cached.(*arcadeCandidateSnapshot)
		if now.Sub(entry.builtAt) <= arcadeCandidateCacheTTL {
			return cloneArcadeCandidates(entry.candidates), nil
		}
	}

	candidates, err := BuildArcadeCandidates(app)
	if err != nil {
		return nil, err
	}

	arcadeCandidateSnapshotCache.Store(key, &arcadeCandidateSnapshot{
		builtAt:    now,
		candidates: cloneArcadeCandidates(candidates),
	})

	return cloneArcadeCandidates(candidates), nil
}

func BuildArcadeCandidates(app core.App) ([]ArcadeCandidate, error) {
	arcades, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcade, "public=true", "", 0, 0)
	if err != nil {
		return nil, err
	}

	basicIDs := make([]string, 0, len(arcades))
	stateIDs := make([]string, 0, len(arcades))
	for _, arcade := range arcades {
		basicIDs = append(basicIDs, arcade.GetString("basic"))
		stateIDs = append(stateIDs, arcade.GetString("game_v2"))
	}
	basics, err := findCandidateRecords(app, arcadeinternal.CollectionArcadeBasic, "id", basicIDs)
	if err != nil {
		return nil, err
	}
	revisions, err := findCandidateRecords(app, arcadeinternal.CollectionArcadeGameRevision, "batch", stateIDs)
	if err != nil {
		return nil, err
	}
	versionIDs := make([]string, 0, len(revisions))
	for _, revision := range revisions {
		versionIDs = append(versionIDs, revision.GetString("version"))
	}
	versions, err := findCandidateRecords(app, arcadeinternal.CollectionGameSeriesVersion, "id", versionIDs)
	if err != nil {
		return nil, err
	}

	basicByID := make(map[string]*core.Record, len(basics))
	for _, basicRec := range basics {
		basicByID[basicRec.Id] = basicRec
	}

	seriesByVersionID := make(map[string]string, len(versions))
	for _, versionRec := range versions {
		if sid, ok := arcadeinternal.AsString(versionRec.Get("series")); ok {
			sid = strings.TrimSpace(sid)
			if sid != "" {
				seriesByVersionID[versionRec.Id] = sid
			}
		}
	}

	installationsByStateID := map[string][]ArcadeGameInstallation{}
	for _, revision := range revisions {
		stateID := strings.TrimSpace(revision.GetString("batch"))
		versionID := strings.TrimSpace(revision.GetString("version"))
		if stateID == "" || versionID == "" {
			continue
		}
		seriesID := seriesByVersionID[versionID]
		if seriesID == "" {
			continue
		}
		installationsByStateID[stateID] = append(installationsByStateID[stateID], ArcadeGameInstallation{
			SeriesID:  seriesID,
			CabinetID: strings.TrimSpace(revision.GetString("cabinet")),
			Quantity:  revision.GetInt("quantity"),
		})
	}

	for stateID := range installationsByStateID {
		sortGameInstallations(installationsByStateID[stateID])
	}

	candidates := make([]ArcadeCandidate, 0, len(arcades))
	for _, arcadeRec := range arcades {
		basicRec := basicByID[arcadeRec.GetString("basic")]
		if basicRec == nil || basicRec.GetString("arcade") != arcadeRec.Id {
			continue
		}
		stateID := strings.TrimSpace(arcadeRec.GetString("game_v2"))
		candidate, ok := buildArcadeCandidateFromRecords(arcadeRec, basicRec, installationsByStateID[stateID])
		if !ok {
			continue
		}
		candidates = append(candidates, candidate)
	}

	return candidates, nil
}

// findCandidateRecords reads only selected current records. Batching keeps the
// filter below SQLite's parameter limit on larger public arcade catalogs.
func findCandidateRecords(app core.App, collection, field string, values []string) ([]*core.Record, error) {
	const batchSize = 400
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	if len(unique) == 0 {
		return []*core.Record{}, nil
	}

	records := make([]*core.Record, 0, len(unique))
	for start := 0; start < len(unique); start += batchSize {
		end := min(start+batchSize, len(unique))
		batchValues := make([]any, end-start)
		for i, value := range unique[start:end] {
			batchValues[i] = value
		}
		batch, err := app.FindAllRecords(collection, dbx.In(field, batchValues...))
		if err != nil {
			return nil, err
		}
		records = append(records, batch...)
	}
	return records, nil
}

func buildArcadeCandidate(app core.App, arcadeRec *core.Record) (ArcadeCandidate, bool) {
	if app == nil || arcadeRec == nil {
		return ArcadeCandidate{}, false
	}

	basicID, _ := arcadeinternal.AsString(arcadeRec.Get("basic"))
	if basicID == "" {
		return ArcadeCandidate{}, false
	}

	basicRec, err := app.FindRecordById(arcadeinternal.CollectionArcadeBasic, basicID)
	if err != nil || basicRec == nil {
		return ArcadeCandidate{}, false
	}

	installations := loadArcadeGameInstallations(app, arcadeRec.GetString("game_v2"))
	return buildArcadeCandidateFromRecords(arcadeRec, basicRec, installations)
}

func buildArcadeCandidateFromRecords(arcadeRec, basicRec *core.Record, installations []ArcadeGameInstallation) (ArcadeCandidate, bool) {
	if arcadeRec == nil || basicRec == nil {
		return ArcadeCandidate{}, false
	}

	candidate := ArcadeCandidate{
		ID:      arcadeRec.Id,
		Country: strings.TrimSpace(arcadeRec.GetString("country")),
		Closed:  arcadeRec.GetBool("closed"),
		GameID:  strings.TrimSpace(arcadeRec.GetString("game_v2")),
	}

	candidate.Name = strings.TrimSpace(basicRec.GetString("name"))
	candidate.Address = strings.TrimSpace(basicRec.GetString("address"))
	candidate.Nicknames = append([]string(nil), basicRec.GetStringSlice("nickname")...)
	candidate.NameNorm = normalizeSearchText(candidate.Name)
	candidate.AddressNorm = normalizeSearchText(candidate.Address)
	candidate.AddressAliasNorm = normalizeAddressQuery(candidate.Address)
	candidate.NicknameNorms = make([]string, 0, len(candidate.Nicknames))
	for _, nickname := range candidate.Nicknames {
		candidate.NicknameNorms = append(candidate.NicknameNorms, normalizeSearchText(nickname))
	}

	if lat, lon, ok := arcadeinternal.ReadLocation(basicRec.Get("location")); ok {
		candidate.Location = &arcadeinternal.Location{Lat: lat, Lon: lon}
	}

	candidate.GameInstallations = cloneGameInstallations(installations)
	sortGameInstallations(candidate.GameInstallations)
	candidate.GameSeries = gameSeriesFromInstallations(candidate.GameInstallations)
	return candidate, true
}

func loadArcadeGameInstallations(app core.App, stateID string) []ArcadeGameInstallation {
	stateID = strings.TrimSpace(stateID)
	if app == nil || stateID == "" {
		return nil
	}

	revisions, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcadeGameRevision, "batch={:id}", "", 0, 0, dbx.Params{"id": stateID})
	if err != nil {
		return nil
	}

	installations := make([]ArcadeGameInstallation, 0, len(revisions))
	for _, revision := range revisions {
		versionID := strings.TrimSpace(revision.GetString("version"))
		if versionID == "" {
			continue
		}
		verRec, err := app.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, versionID)
		if err != nil || verRec == nil {
			continue
		}
		if sid, ok := arcadeinternal.AsString(verRec.Get("series")); ok && strings.TrimSpace(sid) != "" {
			installations = append(installations, ArcadeGameInstallation{
				SeriesID:  strings.TrimSpace(sid),
				CabinetID: strings.TrimSpace(revision.GetString("cabinet")),
				Quantity:  revision.GetInt("quantity"),
			})
		}
	}
	sortGameInstallations(installations)
	return installations
}

func cloneArcadeCandidates(candidates []ArcadeCandidate) []ArcadeCandidate {
	out := make([]ArcadeCandidate, len(candidates))
	for i, candidate := range candidates {
		out[i] = candidate
		if candidate.Location != nil {
			loc := *candidate.Location
			out[i].Location = &loc
		}
		out[i].Nicknames = cloneStringSliceOrEmpty(candidate.Nicknames)
		out[i].GameSeries = cloneStringSliceOrEmpty(candidate.GameSeries)
		out[i].GameInstallations = cloneGameInstallations(candidate.GameInstallations)
		out[i].NicknameNorms = cloneStringSliceOrEmpty(candidate.NicknameNorms)
	}
	return out
}

func cloneGameInstallations(in []ArcadeGameInstallation) []ArcadeGameInstallation {
	if len(in) == 0 {
		return []ArcadeGameInstallation{}
	}
	return append([]ArcadeGameInstallation(nil), in...)
}

func sortGameInstallations(installations []ArcadeGameInstallation) {
	sort.Slice(installations, func(i, j int) bool {
		if installations[i].SeriesID != installations[j].SeriesID {
			return installations[i].SeriesID < installations[j].SeriesID
		}
		return installations[i].CabinetID < installations[j].CabinetID
	})
}

func gameSeriesFromInstallations(installations []ArcadeGameInstallation) []string {
	set := make(map[string]struct{}, len(installations))
	for _, installation := range installations {
		if installation.SeriesID != "" {
			set[installation.SeriesID] = struct{}{}
		}
	}
	series := make([]string, 0, len(set))
	for seriesID := range set {
		series = append(series, seriesID)
	}
	sort.Strings(series)
	return series
}

func cloneStringSliceOrEmpty(in []string) []string {
	if len(in) == 0 {
		return []string{}
	}
	return append([]string(nil), in...)
}

func appCacheKey(app core.App) string {
	if app == nil {
		return arcadeCandidateCacheKey
	}

	if dataDir := strings.TrimSpace(app.DataDir()); dataDir != "" {
		return dataDir
	}

	return arcadeCandidateCacheKey
}
