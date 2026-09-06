package user

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	pbtypes "github.com/pocketbase/pocketbase/tools/types"
)

const (
	kstLocationName = "Asia/Seoul"
)

var attendanceNow = func() time.Time {
	return time.Now().UTC()
}

type UserLevelState struct {
	Exp   int
	Level int
}

type ExpFeedback struct {
	PreviousExp                 int `json:"previous_exp"`
	NewExp                      int `json:"new_exp"`
	DiffExp                     int `json:"diff_exp"`
	PreviousPercentToNextLevel  int `json:"previous_percent_to_next_level"`
	NewPercentToNextLevel       int `json:"new_percent_to_next_level"`
	PreviousLevel               int `json:"previous_level"`
	NewLevel                    int `json:"new_level"`
	LevelDiff                   int `json:"level_diff"`
	RemainingExpToNextLevel     int `json:"remaining_exp_to_next_level"`
	RemainingPercentToNextLevel int `json:"remaining_percent_to_next_level"`
}

type ArcadePublicExpPreview struct {
	CurrentExp    int         `json:"current_exp"`
	PublicExp     int         `json:"public_exp"`
	BackfillExp   int         `json:"backfill_exp"`
	EstimatedExp  int         `json:"estimated_exp"`
	EstimatedGain int         `json:"estimated_gain"`
	XPFeedback    ExpFeedback `json:"xp_feedback"`
}

func SetAttendanceNowForTest(nowFn func() time.Time) func() {
	prev := attendanceNow
	attendanceNow = nowFn
	return func() {
		attendanceNow = prev
	}
}

func LevelFromExp(exp int) int {
	if exp < 4 {
		return 0
	}

	lo := 0
	hi := 1
	for LevelBaseExp(hi) <= exp {
		hi *= 2
	}

	for lo < hi {
		mid := (lo + hi + 1) / 2
		if LevelBaseExp(mid) <= exp {
			lo = mid
			continue
		}
		hi = mid - 1
	}

	return lo
}

func NextLevelExp(level int) int {
	if level < 0 {
		return 0
	}
	return LevelBaseExp(level + 1)
}

func LevelBaseExp(level int) int {
	if level <= 0 {
		return 0
	}
	return int(math.Round(float64((6*level*level)+((110)*level)) / 29.0))
}

func BuildExpFeedback(previousExp, newExp int) ExpFeedback {
	previousLevel := LevelFromExp(previousExp)
	newLevel := LevelFromExp(newExp)
	nextThreshold := NextLevelExp(newLevel)
	remainingExp := nextThreshold - newExp
	if remainingExp < 0 {
		remainingExp = 0
	}
	span := nextThreshold - LevelBaseExp(newLevel)
	remainingPercent := 0
	if span > 0 {
		remainingPercent = int(math.Round(float64(remainingExp) * 100 / float64(span)))
	}
	previousRemainingExp := NextLevelExp(previousLevel) - previousExp
	if previousRemainingExp < 0 {
		previousRemainingExp = 0
	}
	previousSpan := NextLevelExp(previousLevel) - LevelBaseExp(previousLevel)
	previousRemainingPercent := 0
	if previousSpan > 0 {
		previousRemainingPercent = int(math.Round(float64(previousRemainingExp) * 100 / float64(previousSpan)))
	}

	return ExpFeedback{
		PreviousExp:                 previousExp,
		NewExp:                      newExp,
		DiffExp:                     newExp - previousExp,
		PreviousPercentToNextLevel:  100 - previousRemainingPercent,
		NewPercentToNextLevel:       100 - remainingPercent,
		PreviousLevel:               previousLevel,
		NewLevel:                    newLevel,
		LevelDiff:                   newLevel - previousLevel,
		RemainingExpToNextLevel:     remainingExp,
		RemainingPercentToNextLevel: remainingPercent,
	}
}

func CheckInKind(day string) string {
	return "xp:attendance:service:" + strings.TrimSpace(day)
}

func ArcadePublicKind(arcadeID string) string {
	return "xp:arcade-public:" + strings.TrimSpace(arcadeID)
}

func ArcadeEditKind(arcadeID, part string) string {
	return "xp:arcade-edit:" + strings.TrimSpace(part) + ":" + strings.TrimSpace(arcadeID)
}

func ArcadePublicBackfillKind(arcadeID, part string) string {
	// Keep the one-time backfill key stable. AwardArcadeEditExpTx explicitly
	// excludes this key from its weekly cooldown query below.
	return ArcadeEditKind(arcadeID, part)
}

func ArcadeEditGrantKind(arcadeID, part string, at time.Time) string {
	return ArcadeEditKind(arcadeID, part) + ":" + strconv.FormatInt(at.UTC().UnixNano(), 10)
}

func ArcadeGameEditGrantKind(arcadeID string, at time.Time, entryIDs []string) string {
	ids := uniqueGameEntryIDs(entryIDs)
	return ArcadeEditKind(arcadeID, "game") + ":" + strconv.FormatInt(at.UTC().UnixNano(), 10) + ":" + strings.Join(ids, ",")
}

func uniqueGameEntryIDs(entryIDs []string) []string {
	seen := make(map[string]struct{}, len(entryIDs))
	ids := make([]string, 0, len(entryIDs))
	for _, entryID := range entryIDs {
		entryID = strings.TrimSpace(entryID)
		if entryID == "" {
			continue
		}
		if _, ok := seen[entryID]; ok {
			continue
		}
		seen[entryID] = struct{}{}
		ids = append(ids, entryID)
	}
	sort.Strings(ids)
	return ids
}

func gameEntryIDsFromGrantKind(kind, arcadeID string) []string {
	prefix := ArcadeEditKind(arcadeID, "game") + ":"
	if !strings.HasPrefix(kind, prefix) {
		return nil
	}
	parts := strings.SplitN(strings.TrimPrefix(kind, prefix), ":", 2)
	if len(parts) != 2 {
		return nil
	}
	return uniqueGameEntryIDs(strings.Split(parts[1], ","))
}

// ArcadeGameEditExp returns the target XP for the number of distinct durable
// game entries counted in the rolling window. A single entry may contain many
// field changes, but it still counts as one entry.
func ArcadeGameEditExp(changedEntries int) int {
	if changedEntries <= 0 {
		return 0
	}
	if exp := 2 * changedEntries; exp < 10 {
		return exp
	}
	return 10
}

func ArcadePhotoSubmissionKind(arcadeID string) string {
	return "xp:arcade-photo-submission:" + strings.TrimSpace(arcadeID)
}

// ArcadePhotoGrantKind identifies the one-time publication of a photo atom.
// Photo atoms are immutable once published, so the atom id is a stable
// idempotency key even when a later photo molecule reorders or removes it.
func ArcadePhotoGrantKind(arcadeID, atomID string) string {
	return "xp:arcade-photo:" + strings.TrimSpace(arcadeID) + ":" + strings.TrimSpace(atomID)
}

func ArcadeVisitKind(visitID string) string {
	return "xp:arcade-visit:" + strings.TrimSpace(visitID)
}

func FlagKind(flagID string) string {
	return "xp:flag:" + strings.TrimSpace(flagID)
}

func FlagReactionKind(reactionID string) string {
	return "xp:flag-reaction:" + strings.TrimSpace(reactionID)
}

func KSTDay(now time.Time) string {
	loc, err := time.LoadLocation(kstLocationName)
	if err != nil {
		loc = time.FixedZone("KST", 9*60*60)
	}
	return now.In(loc).Format("2006-01-02")
}

func LoadCurrentExp(app core.App, userID string) (int, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return 0, fmt.Errorf("user id is required")
	}

	levelState, err := LoadUserLevelState(app, userID)
	if err != nil {
		if !isNotFoundError(err) {
			return 0, err
		}
	} else {
		return levelState.Exp, nil
	}

	return 0, nil
}

// IsExpEligible reports whether the user has completed the one-time username
// setup required before XP can be awarded.
func IsExpEligible(app core.App, userID string) (bool, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, fmt.Errorf("user id is required")
	}

	userRec, err := app.FindRecordById(CollectionUser, userID)
	if err != nil {
		return false, fmt.Errorf("load user for xp eligibility failed: %w", err)
	}
	return strings.TrimSpace(userRec.GetString("username")) != "", nil
}

func HasLevelLogKind(app core.App, userID, kind string) (bool, error) {
	userID = strings.TrimSpace(userID)
	kind = strings.TrimSpace(kind)
	if userID == "" {
		return false, fmt.Errorf("user id is required")
	}
	if kind == "" {
		return false, fmt.Errorf("kind is required")
	}

	recs, err := app.FindRecordsByFilter(
		CollectionUserLevelLog,
		"user = {:user} && kind = {:kind}",
		"",
		1,
		0,
		dbx.Params{"user": userID, "kind": kind},
	)
	if err != nil {
		return false, fmt.Errorf("query user level log kind failed: %w", err)
	}
	return len(recs) > 0, nil
}

func LoadUserLevelState(app core.App, userID string) (UserLevelState, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return UserLevelState{}, fmt.Errorf("user id is required")
	}

	rec, err := app.FindRecordById(CollectionUserLevel, userID)
	if err != nil {
		return UserLevelState{}, err
	}

	exp := rec.GetInt("exp")
	return UserLevelState{
		Exp:   exp,
		Level: LevelFromExp(exp),
	}, nil
}

func AwardExpTx(txApp core.App, userID, kind string, diff int, baseExp int) (int, bool, error) {
	userID = strings.TrimSpace(userID)
	kind = strings.TrimSpace(kind)
	if userID == "" {
		return 0, false, fmt.Errorf("user id is required")
	}
	if kind == "" {
		return 0, false, fmt.Errorf("kind is required")
	}
	if diff == 0 {
		return baseExp, false, nil
	}

	eligible, err := IsExpEligible(txApp, userID)
	if err != nil {
		return 0, false, err
	}
	if !eligible {
		return baseExp, false, nil
	}

	currentExp, err := ensureUserLevelBaseTx(txApp, userID, baseExp)
	if err != nil {
		return 0, false, err
	}

	existing, err := txApp.FindRecordsByFilter(
		CollectionUserLevelLog,
		"user = {:user} && kind = {:kind}",
		"-created",
		1,
		0,
		dbx.Params{"user": userID, "kind": kind},
	)
	if err != nil {
		return 0, false, fmt.Errorf("query user level log failed: %w", err)
	}
	if len(existing) > 0 {
		return currentExp, false, nil
	}

	nextExp := currentExp + diff
	if err := updateUserLevelExpTx(txApp, userID, nextExp); err != nil {
		return 0, false, err
	}
	if err := createUserLevelLogTx(txApp, userID, kind, currentExp, nextExp, diff); err != nil {
		return 0, false, err
	}

	return nextExp, true, nil
}

func AwardArcadeEditExpTx(txApp core.App, userID, arcadeID, part string, diff int, baseExp int, now time.Time) (int, bool, error) {
	userID = strings.TrimSpace(userID)
	arcadeID = strings.TrimSpace(arcadeID)
	part = strings.TrimSpace(part)
	if userID == "" {
		return 0, false, fmt.Errorf("user id is required")
	}
	if arcadeID == "" {
		return 0, false, fmt.Errorf("arcade id is required")
	}
	if part == "" {
		return 0, false, fmt.Errorf("part is required")
	}
	if diff == 0 {
		return baseExp, false, nil
	}

	eligible, err := IsExpEligible(txApp, userID)
	if err != nil {
		return 0, false, err
	}
	if !eligible {
		return baseExp, false, nil
	}

	currentExp, err := ensureUserLevelBaseTx(txApp, userID, baseExp)
	if err != nil {
		return 0, false, err
	}

	cutoff := now.UTC().Add(-7 * 24 * time.Hour)
	prefix := ArcadeEditKind(arcadeID, part) + "%"
	rows, err := txApp.DB().NewQuery(`
SELECT COALESCE(created, '') AS created
FROM user_level_log
WHERE "user" = {:user}
  AND kind LIKE {:kind}
  AND kind != {:backfill_kind}
ORDER BY created DESC, id DESC
LIMIT 1
`).Bind(dbx.Params{
		"user":          userID,
		"kind":          prefix,
		"backfill_kind": ArcadePublicBackfillKind(arcadeID, part),
	}).Rows()
	if err != nil {
		return 0, false, fmt.Errorf("query arcade edit cooldown failed: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		var created string
		if err := rows.Scan(&created); err != nil {
			return 0, false, fmt.Errorf("scan arcade edit cooldown failed: %w", err)
		}
		lastCreated, err := parseLevelLogCreated(created)
		if err == nil && lastCreated.After(cutoff) {
			return currentExp, false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("iterate arcade edit cooldown failed: %w", err)
	}

	kind := ArcadeEditGrantKind(arcadeID, part, now)
	return AwardExpTx(txApp, userID, kind, diff, baseExp)
}

// AwardArcadeGameEditExpTx awards only the incremental XP represented by new
// durable game entries touched during the rolling seven-day window. The entry
// ids are encoded in the grant kind because user_level_log has no metadata
// column and private draft changelogs must not be mistaken for public edit XP.
func AwardArcadeGameEditExpTx(txApp core.App, userID, arcadeID string, entryIDs []string, baseExp int, now time.Time) (int, bool, error) {
	userID = strings.TrimSpace(userID)
	arcadeID = strings.TrimSpace(arcadeID)
	entryIDs = uniqueGameEntryIDs(entryIDs)
	if userID == "" {
		return 0, false, fmt.Errorf("user id is required")
	}
	if arcadeID == "" {
		return 0, false, fmt.Errorf("arcade id is required")
	}
	if len(entryIDs) == 0 {
		return baseExp, false, nil
	}

	eligible, err := IsExpEligible(txApp, userID)
	if err != nil {
		return 0, false, err
	}
	if !eligible {
		return baseExp, false, nil
	}

	currentExp, err := ensureUserLevelBaseTx(txApp, userID, baseExp)
	if err != nil {
		return 0, false, err
	}

	cutoff := now.UTC().Add(-7 * 24 * time.Hour)
	prefix := ArcadeEditKind(arcadeID, "game") + ":%"
	rows, err := txApp.DB().NewQuery(`
SELECT kind, COALESCE(diff_exp, 0) AS diff_exp, COALESCE(created, '') AS created
FROM user_level_log
WHERE "user" = {:user}
  AND kind LIKE {:kind}
ORDER BY created ASC, id ASC
`).Bind(dbx.Params{"user": userID, "kind": prefix}).Rows()
	if err != nil {
		return 0, false, fmt.Errorf("query arcade game edit window failed: %w", err)
	}
	defer rows.Close()

	seenEntries := make(map[string]struct{}, len(entryIDs))
	for _, entryID := range entryIDs {
		seenEntries[entryID] = struct{}{}
	}
	awardedExp := 0
	for rows.Next() {
		var kind, created string
		var diffExp int
		if err := rows.Scan(&kind, &diffExp, &created); err != nil {
			return 0, false, fmt.Errorf("scan arcade game edit window failed: %w", err)
		}
		lastCreated, parseErr := parseLevelLogCreated(created)
		if parseErr != nil || !lastCreated.After(cutoff) {
			continue
		}
		priorEntries := gameEntryIDsFromGrantKind(kind, arcadeID)
		if len(priorEntries) == 0 {
			// Ignore legacy fixed-cooldown game grants that predate entry-aware
			// grants. They cannot identify which entries were already counted.
			continue
		}
		for _, entryID := range priorEntries {
			seenEntries[entryID] = struct{}{}
		}
		if diffExp > 0 {
			awardedExp += diffExp
		}
	}
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("iterate arcade game edit window failed: %w", err)
	}

	targetExp := ArcadeGameEditExp(len(seenEntries))
	diffExp := targetExp - awardedExp
	if diffExp <= 0 {
		return currentExp, false, nil
	}

	kind := ArcadeGameEditGrantKind(arcadeID, now, entryIDs)
	return AwardExpTx(txApp, userID, kind, diffExp, baseExp)
}

// AwardArcadePhotoExpTx awards two XP for each atom that is being published
// for the first time. The rolling seven-day cap is scoped to one user and
// arcade: campaign targets have a 10 XP cap and other public arcades have a
// 4 XP cap. Callers must pass only atoms whose public flag was false before
// this transaction; AwardExpTx provides the final per-atom idempotency guard.
func AwardArcadePhotoExpTx(txApp core.App, userID, arcadeID string, atomIDs []string, campaignTarget bool, baseExp int, now time.Time) (int, bool, error) {
	userID = strings.TrimSpace(userID)
	arcadeID = strings.TrimSpace(arcadeID)
	atomIDs = uniqueGameEntryIDs(atomIDs)
	if userID == "" {
		return 0, false, fmt.Errorf("user id is required")
	}
	if arcadeID == "" {
		return 0, false, fmt.Errorf("arcade id is required")
	}
	if len(atomIDs) == 0 {
		return baseExp, false, nil
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}

	eligible, err := IsExpEligible(txApp, userID)
	if err != nil {
		return 0, false, err
	}
	if !eligible {
		return baseExp, false, nil
	}

	currentExp, err := ensureUserLevelBaseTx(txApp, userID, baseExp)
	if err != nil {
		return 0, false, err
	}

	capExp := 4
	if campaignTarget {
		capExp = 10
	}
	cutoff := now.Add(-7 * 24 * time.Hour)
	prefix := "xp:arcade-photo:" + arcadeID + ":%"
	rows, err := txApp.DB().NewQuery(`
SELECT COALESCE(diff_exp, 0) AS diff_exp, COALESCE(created, '') AS created
FROM user_level_log
WHERE "user" = {:user} AND kind LIKE {:kind}
ORDER BY created ASC, id ASC
`).Bind(dbx.Params{"user": userID, "kind": prefix}).Rows()
	if err != nil {
		return 0, false, fmt.Errorf("query arcade photo edit window failed: %w", err)
	}
	defer rows.Close()
	awardedExp := 0
	for rows.Next() {
		var diffExp int
		var created string
		if err := rows.Scan(&diffExp, &created); err != nil {
			return 0, false, fmt.Errorf("scan arcade photo edit window failed: %w", err)
		}
		lastCreated, parseErr := parseLevelLogCreated(created)
		if parseErr != nil || !lastCreated.After(cutoff) || diffExp <= 0 {
			continue
		}
		awardedExp += diffExp
	}
	if err := rows.Err(); err != nil {
		return 0, false, fmt.Errorf("iterate arcade photo edit window failed: %w", err)
	}

	remaining := capExp - awardedExp
	if remaining <= 0 {
		return currentExp, false, nil
	}
	granted := false
	for _, atomID := range atomIDs {
		if remaining <= 0 {
			break
		}
		grant := 2
		if grant > remaining {
			grant = remaining
		}
		nextExp, didGrant, err := AwardExpTx(txApp, userID, ArcadePhotoGrantKind(arcadeID, atomID), grant, currentExp)
		if err != nil {
			return 0, false, err
		}
		if didGrant {
			currentExp = nextExp
			remaining -= grant
			granted = true
		}
	}
	return currentExp, granted, nil
}

type arcadePublicBackfillGrant struct {
	kind string
	diff int
}

func PreviewArcadePublicExp(app core.App, userID, arcadeID string) (ArcadePublicExpPreview, error) {
	userID = strings.TrimSpace(userID)
	arcadeID = strings.TrimSpace(arcadeID)
	if userID == "" {
		return ArcadePublicExpPreview{}, fmt.Errorf("user id is required")
	}
	if arcadeID == "" {
		return ArcadePublicExpPreview{}, fmt.Errorf("arcade id is required")
	}

	baseExp, err := LoadCurrentExp(app, userID)
	if err != nil {
		return ArcadePublicExpPreview{}, fmt.Errorf("failed to load current exp: %w", err)
	}
	eligible, err := IsExpEligible(app, userID)
	if err != nil {
		return ArcadePublicExpPreview{}, err
	}
	if !eligible {
		return ArcadePublicExpPreview{
			CurrentExp:   baseExp,
			EstimatedExp: baseExp,
			XPFeedback:   BuildExpFeedback(baseExp, baseExp),
		}, nil
	}

	currentExp := baseExp
	publicExp := 0
	if awarded, err := HasLevelLogKind(app, userID, ArcadePublicKind(arcadeID)); err != nil {
		return ArcadePublicExpPreview{}, err
	} else if !awarded {
		publicExp = 5
		currentExp += publicExp
	}

	grants, err := collectArcadePublicBackfillGrants(app, userID, arcadeID, false)
	if err != nil {
		return ArcadePublicExpPreview{}, err
	}
	backfillExp := 0
	for _, grant := range grants {
		awarded, err := HasLevelLogKind(app, userID, grant.kind)
		if err != nil {
			return ArcadePublicExpPreview{}, err
		}
		if awarded {
			continue
		}
		backfillExp += grant.diff
		currentExp += grant.diff
	}

	return ArcadePublicExpPreview{
		CurrentExp:    baseExp,
		PublicExp:     publicExp,
		BackfillExp:   backfillExp,
		EstimatedExp:  currentExp,
		EstimatedGain: currentExp - baseExp,
		XPFeedback:    BuildExpFeedback(baseExp, currentExp),
	}, nil
}

func GrantArcadePublicBackfillTx(txApp core.App, userID, arcadeID string, baseExp int) (int, error) {
	userID = strings.TrimSpace(userID)
	arcadeID = strings.TrimSpace(arcadeID)
	if userID == "" {
		return 0, fmt.Errorf("user id is required")
	}
	if arcadeID == "" {
		return 0, fmt.Errorf("arcade id is required")
	}
	eligible, err := IsExpEligible(txApp, userID)
	if err != nil {
		return 0, err
	}
	if !eligible {
		return baseExp, nil
	}

	currentExp := baseExp
	grants, err := collectArcadePublicBackfillGrants(txApp, userID, arcadeID, true)
	if err != nil {
		return 0, err
	}
	for _, grant := range grants {
		nextExp, granted, err := AwardExpTx(txApp, userID, grant.kind, grant.diff, currentExp)
		if err != nil {
			return 0, err
		}
		if granted {
			currentExp = nextExp
		}
	}

	return currentExp, nil
}

func collectArcadePublicBackfillGrants(app core.App, userID, arcadeID string, publicOnly bool) ([]arcadePublicBackfillGrant, error) {
	publicFilter := ""
	if publicOnly {
		publicFilter = " AND a.public = 1"
	}

	grants := make([]arcadePublicBackfillGrant, 0)
	changeRows, err := app.DB().NewQuery(`
SELECT c.id AS id, c.changed AS changed, COALESCE(c.created, '') AS created
FROM arcade_changelog c
INNER JOIN arcade a ON a.id = c.arcade
WHERE c."by" = {:user}
  AND c.arcade = {:arcade}
  AND c.changed IN ('basic', 'game', 'hour', 'sns', 'gtk', 'photo')
` + publicFilter + `
ORDER BY c.created ASC, c.id ASC
`).Bind(dbx.Params{"user": userID, "arcade": arcadeID}).Rows()
	if err != nil {
		return nil, fmt.Errorf("query arcade backfill changelog failed: %w", err)
	}
	defer changeRows.Close()

	seenChange := map[string]struct{}{}
	for changeRows.Next() {
		var rowID, created string
		var changed string
		if err := changeRows.Scan(&rowID, &changed, &created); err != nil {
			return nil, fmt.Errorf("scan arcade backfill changelog failed: %w", err)
		}
		changed = strings.TrimSpace(changed)
		if changed == "" {
			continue
		}
		if _, ok := seenChange[changed]; ok {
			continue
		}
		seenChange[changed] = struct{}{}
		// Public-conversion backfill follows the current edit policy. Photo
		// history is handled by per-atom publication grants below (and is not a
		// blanket one-time grant).
		if changed != "photo" {
			grants = append(grants, arcadePublicBackfillGrant{kind: ArcadePublicBackfillKind(arcadeID, changed), diff: 2})
		}
	}
	if err := changeRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate arcade backfill changelog failed: %w", err)
	}

	flagRows, err := app.DB().NewQuery(`
SELECT f.id AS id, COALESCE(f.created, '') AS created
FROM arcade_flag f
INNER JOIN arcade a ON a.id = f.arcade
WHERE f.createdBy = {:user}
  AND f.arcade = {:arcade}
` + publicFilter + `
ORDER BY f.created ASC, f.id ASC
`).Bind(dbx.Params{"user": userID, "arcade": arcadeID}).Rows()
	if err != nil {
		return nil, fmt.Errorf("query arcade backfill flags failed: %w", err)
	}
	defer flagRows.Close()

	for flagRows.Next() {
		var rowID, created string
		if err := flagRows.Scan(&rowID, &created); err != nil {
			return nil, fmt.Errorf("scan arcade backfill flag failed: %w", err)
		}
		grants = append(grants, arcadePublicBackfillGrant{kind: FlagKind(rowID), diff: 5})
	}
	if err := flagRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate arcade backfill flags failed: %w", err)
	}

	reactionRows, err := app.DB().NewQuery(`
SELECT r.id AS id, r.reaction AS reaction, COALESCE(r.created, '') AS created
FROM arcade_flag_reaction r
INNER JOIN arcade_flag f ON f.id = r.flag
INNER JOIN arcade a ON a.id = f.arcade
WHERE r.createdBy = {:user}
  AND f.arcade = {:arcade}
` + publicFilter + `
ORDER BY r.created ASC, r.id ASC
`).Bind(dbx.Params{"user": userID, "arcade": arcadeID}).Rows()
	if err != nil {
		return nil, fmt.Errorf("query arcade backfill reactions failed: %w", err)
	}
	defer reactionRows.Close()

	for reactionRows.Next() {
		var rowID, reaction, created string
		if err := reactionRows.Scan(&rowID, &reaction, &created); err != nil {
			return nil, fmt.Errorf("scan arcade backfill reaction failed: %w", err)
		}
		diff := 0
		switch strings.TrimSpace(reaction) {
		case "issue_persist":
			diff = 2
		case "fixed":
			diff = 3
		}
		if diff > 0 {
			grants = append(grants, arcadePublicBackfillGrant{kind: FlagReactionKind(rowID), diff: diff})
		}
	}
	if err := reactionRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate arcade backfill reactions failed: %w", err)
	}

	return grants, nil
}

func ensureUserLevelBaseTx(txApp core.App, userID string, baseExp int) (int, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return 0, fmt.Errorf("user id is required")
	}

	rec, err := txApp.FindRecordById(CollectionUserLevel, userID)
	if err == nil {
		return rec.GetInt("exp"), nil
	}
	if !isNotFoundError(err) {
		return 0, fmt.Errorf("load user_level failed: %w", err)
	}

	coll, err := txApp.FindCollectionByNameOrId(CollectionUserLevel)
	if err != nil {
		return 0, fmt.Errorf("failed to load user_level collection: %w", err)
	}

	rec = core.NewRecord(coll)
	rec.Set("id", userID)
	rec.Set("user", userID)
	rec.Set("exp", baseExp)
	if err := txApp.Save(rec); err != nil {
		return 0, fmt.Errorf("failed to create user_level: %w", err)
	}

	return baseExp, nil
}

func updateUserLevelExpTx(txApp core.App, userID string, exp int) error {
	rec, err := txApp.FindRecordById(CollectionUserLevel, userID)
	if err != nil {
		return fmt.Errorf("load user_level failed: %w", err)
	}
	rec.Set("exp", exp)
	if err := txApp.Save(rec); err != nil {
		return fmt.Errorf("failed to update user_level: %w", err)
	}
	return nil
}

func createUserLevelLogTx(txApp core.App, userID, kind string, previousExp, newExp, diffExp int) error {
	coll, err := txApp.FindCollectionByNameOrId(CollectionUserLevelLog)
	if err != nil {
		return fmt.Errorf("failed to load user_level_log collection: %w", err)
	}

	rec := core.NewRecord(coll)
	rec.Set("user", userID)
	rec.Set("kind", kind)
	rec.Set("previous_exp", previousExp)
	rec.Set("new_exp", newExp)
	rec.Set("diff_exp", diffExp)
	if err := txApp.Save(rec); err != nil {
		return fmt.Errorf("failed to create user_level_log: %w", err)
	}
	return nil
}

func parseLevelLogCreated(created string) (time.Time, error) {
	created = strings.TrimSpace(created)
	if created == "" {
		return time.Time{}, fmt.Errorf("created is required")
	}
	if ts, err := time.Parse(time.RFC3339Nano, created); err == nil {
		return ts, nil
	}
	if ts, err := time.Parse(time.RFC3339, created); err == nil {
		return ts, nil
	}
	return time.Parse(pbtypes.DefaultDateLayout, created)
}

func loadAttendanceExp(app core.App, userID string) (int, error) {
	dayPrefix := "xp:attendance:service:"
	recs, err := app.FindRecordsByFilter(
		CollectionUserLevelLog,
		"user = {:user} && kind ~ {:kind}",
		"",
		0,
		0,
		dbx.Params{"user": strings.TrimSpace(userID), "kind": dayPrefix},
	)
	if err != nil {
		return 0, fmt.Errorf("query attendance exp failed: %w", err)
	}

	total := 0
	for _, rec := range recs {
		kind := strings.TrimSpace(rec.GetString("kind"))
		if !strings.HasPrefix(kind, dayPrefix) {
			continue
		}
		total += rec.GetInt("diff_exp")
	}
	return total, nil
}
