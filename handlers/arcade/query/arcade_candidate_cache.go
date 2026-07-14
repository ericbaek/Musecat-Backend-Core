package query

import (
	"fmt"
	"github.com/pocketbase/dbx"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

const arcadeCandidateCacheTTL = 30 * time.Minute
const arcadeCandidateCacheKey = "arcade_candidate_snapshot"

var arcadeCandidateCacheHookCollections = []string{
	arcadeinternal.CollectionArcade,
	arcadeinternal.CollectionArcadeBasic,
	arcadeinternal.CollectionArcadeGame,
	arcadeinternal.CollectionArcadeGameAtoms,
	arcadeinternal.CollectionGameSeriesVersion,
}

type ArcadeCandidate struct {
	ID               string
	Country          string
	Closed           bool
	Name             string
	Address          string
	GameID           string
	Nicknames        []string
	GameSeries       []string
	Location         *arcadeinternal.Location
	NameNorm         string
	AddressNorm      string
	AddressAliasNorm string
	NicknameNorms    []string
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

	basics, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcadeBasic, "", "", 0, 0)
	if err != nil {
		return nil, err
	}
	games, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcadeGame, "", "", 0, 0)
	if err != nil {
		return nil, err
	}
	atoms, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcadeGameAtoms, "", "", 0, 0)
	if err != nil {
		return nil, err
	}
	versions, err := app.FindRecordsByFilter(arcadeinternal.CollectionGameSeriesVersion, "", "", 0, 0)
	if err != nil {
		return nil, err
	}

	basicByArcadeID := make(map[string]*core.Record, len(basics))
	for _, basicRec := range basics {
		arcadeID := strings.TrimSpace(basicRec.GetString("arcade"))
		if arcadeID == "" {
			continue
		}
		basicByArcadeID[arcadeID] = basicRec
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

	seriesSetByMoleculeID := map[string]map[string]struct{}{}
	for _, atomRec := range atoms {
		moleculeID := strings.TrimSpace(atomRec.GetString("molecule"))
		versionID := strings.TrimSpace(atomRec.GetString("game"))
		if moleculeID == "" || versionID == "" {
			continue
		}
		seriesID := seriesByVersionID[versionID]
		if seriesID == "" {
			continue
		}
		seriesSet := seriesSetByMoleculeID[moleculeID]
		if seriesSet == nil {
			seriesSet = map[string]struct{}{}
			seriesSetByMoleculeID[moleculeID] = seriesSet
		}
		seriesSet[seriesID] = struct{}{}
	}

	gameSeriesByMoleculeID := make(map[string][]string, len(games))
	for _, gameRec := range games {
		moleculeID := strings.TrimSpace(gameRec.Id)
		seriesSet := seriesSetByMoleculeID[moleculeID]
		if len(seriesSet) == 0 {
			continue
		}
		series := make([]string, 0, len(seriesSet))
		for sid := range seriesSet {
			series = append(series, sid)
		}
		sort.Strings(series)
		gameSeriesByMoleculeID[moleculeID] = series
	}

	candidates := make([]ArcadeCandidate, 0, len(arcades))
	for _, arcadeRec := range arcades {
		basicRec := basicByArcadeID[arcadeRec.Id]
		gameSeries := gameSeriesByMoleculeID[strings.TrimSpace(arcadeRec.GetString("game"))]
		candidate, ok := buildArcadeCandidateFromRecords(arcadeRec, basicRec, gameSeries)
		if !ok {
			continue
		}
		candidates = append(candidates, candidate)
	}

	return candidates, nil
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

	gameSeries := loadArcadeGameSeries(app, arcadeRec.GetString("game"))
	return buildArcadeCandidateFromRecords(arcadeRec, basicRec, gameSeries)
}

func buildArcadeCandidateFromRecords(arcadeRec, basicRec *core.Record, gameSeries []string) (ArcadeCandidate, bool) {
	if arcadeRec == nil || basicRec == nil {
		return ArcadeCandidate{}, false
	}

	candidate := ArcadeCandidate{
		ID:      arcadeRec.Id,
		Country: strings.TrimSpace(arcadeRec.GetString("country")),
		Closed:  arcadeRec.GetBool("closed"),
		GameID:  strings.TrimSpace(arcadeRec.GetString("game")),
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

	candidate.GameSeries = cloneStringSliceOrEmpty(gameSeries)
	sort.Strings(candidate.GameSeries)
	return candidate, true
}

func loadArcadeGameSeries(app core.App, gameID string) []string {
	gameID = strings.TrimSpace(gameID)
	if app == nil || gameID == "" {
		return nil
	}

	atoms, err := app.FindRecordsByFilter(arcadeinternal.CollectionArcadeGameAtoms, "molecule={:id}", "", 0, 0, dbx.Params{"id": gameID})
	if err != nil {
		return nil
	}

	seriesSet := map[string]struct{}{}
	for _, atom := range atoms {
		versionID := strings.TrimSpace(atom.GetString("game"))
		if versionID == "" {
			continue
		}
		verRec, err := app.FindRecordById(arcadeinternal.CollectionGameSeriesVersion, versionID)
		if err != nil || verRec == nil {
			continue
		}
		if sid, ok := arcadeinternal.AsString(verRec.Get("series")); ok && strings.TrimSpace(sid) != "" {
			seriesSet[strings.TrimSpace(sid)] = struct{}{}
		}
	}

	if len(seriesSet) == 0 {
		return nil
	}

	series := make([]string, 0, len(seriesSet))
	for id := range seriesSet {
		series = append(series, id)
	}
	return series
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
		out[i].NicknameNorms = cloneStringSliceOrEmpty(candidate.NicknameNorms)
	}
	return out
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
