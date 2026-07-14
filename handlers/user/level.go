package user

import (
	"fmt"
	"math"
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

func ArcadeEditGrantKind(arcadeID, part string, at time.Time) string {
	return ArcadeEditKind(arcadeID, part) + ":" + strconv.FormatInt(at.UTC().UnixNano(), 10)
}

func ArcadePhotoSubmissionKind(arcadeID string) string {
	return "xp:arcade-photo-submission:" + strings.TrimSpace(arcadeID)
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
ORDER BY created DESC, id DESC
LIMIT 1
`).Bind(dbx.Params{"user": userID, "kind": prefix}).Rows()
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

func GrantArcadePublicBackfillTx(txApp core.App, userID, arcadeID string, baseExp int) (int, error) {
	userID = strings.TrimSpace(userID)
	arcadeID = strings.TrimSpace(arcadeID)
	if userID == "" {
		return 0, fmt.Errorf("user id is required")
	}
	if arcadeID == "" {
		return 0, fmt.Errorf("arcade id is required")
	}

	currentExp := baseExp
	type rowInfo struct {
		ID      string
		Created string
	}

	changeRows, err := txApp.DB().NewQuery(`
SELECT c.id AS id, c.changed AS changed, COALESCE(c.created, '') AS created
FROM arcade_changelog c
INNER JOIN arcade a ON a.id = c.arcade
WHERE c."by" = {:user}
  AND c.arcade = {:arcade}
  AND a.public = 1
  AND c.changed IN ('basic', 'game', 'hour', 'sns', 'gtk', 'photo')
ORDER BY c.created ASC, c.id ASC
`).Bind(dbx.Params{"user": userID, "arcade": arcadeID}).Rows()
	if err != nil {
		return 0, fmt.Errorf("query arcade backfill changelog failed: %w", err)
	}
	defer changeRows.Close()

	seenChange := map[string]struct{}{}
	for changeRows.Next() {
		var row rowInfo
		var changed string
		if err := changeRows.Scan(&row.ID, &changed, &row.Created); err != nil {
			return 0, fmt.Errorf("scan arcade backfill changelog failed: %w", err)
		}
		changed = strings.TrimSpace(changed)
		if changed == "" {
			continue
		}
		if _, ok := seenChange[changed]; ok {
			continue
		}
		seenChange[changed] = struct{}{}
		if _, granted, err := AwardExpTx(txApp, userID, ArcadeEditKind(arcadeID, changed), 3, currentExp); err != nil {
			return 0, err
		} else if granted {
			currentExp += 3
		}
	}
	if err := changeRows.Err(); err != nil {
		return 0, fmt.Errorf("iterate arcade backfill changelog failed: %w", err)
	}

	flagRows, err := txApp.DB().NewQuery(`
SELECT f.id AS id, COALESCE(f.created, '') AS created
FROM arcade_flag f
INNER JOIN arcade a ON a.id = f.arcade
WHERE f.createdBy = {:user}
  AND f.arcade = {:arcade}
  AND a.public = 1
ORDER BY f.created ASC, f.id ASC
`).Bind(dbx.Params{"user": userID, "arcade": arcadeID}).Rows()
	if err != nil {
		return 0, fmt.Errorf("query arcade backfill flags failed: %w", err)
	}
	defer flagRows.Close()

	for flagRows.Next() {
		var row rowInfo
		if err := flagRows.Scan(&row.ID, &row.Created); err != nil {
			return 0, fmt.Errorf("scan arcade backfill flag failed: %w", err)
		}
		if _, granted, err := AwardExpTx(txApp, userID, FlagKind(row.ID), 5, currentExp); err != nil {
			return 0, err
		} else if granted {
			currentExp += 5
		}
	}
	if err := flagRows.Err(); err != nil {
		return 0, fmt.Errorf("iterate arcade backfill flags failed: %w", err)
	}

	reactionRows, err := txApp.DB().NewQuery(`
SELECT r.id AS id, COALESCE(r.created, '') AS created
FROM arcade_flag_reaction r
INNER JOIN arcade_flag f ON f.id = r.flag
INNER JOIN arcade a ON a.id = f.arcade
WHERE r.createdBy = {:user}
  AND f.arcade = {:arcade}
  AND a.public = 1
ORDER BY r.created ASC, r.id ASC
`).Bind(dbx.Params{"user": userID, "arcade": arcadeID}).Rows()
	if err != nil {
		return 0, fmt.Errorf("query arcade backfill reactions failed: %w", err)
	}
	defer reactionRows.Close()

	for reactionRows.Next() {
		var row rowInfo
		if err := reactionRows.Scan(&row.ID, &row.Created); err != nil {
			return 0, fmt.Errorf("scan arcade backfill reaction failed: %w", err)
		}
		if _, granted, err := AwardExpTx(txApp, userID, FlagReactionKind(row.ID), 3, currentExp); err != nil {
			return 0, err
		} else if granted {
			currentExp += 3
		}
	}
	if err := reactionRows.Err(); err != nil {
		return 0, fmt.Errorf("iterate arcade backfill reactions failed: %w", err)
	}

	photoRows, err := txApp.DB().NewQuery(`
SELECT p.arcade AS arcade_id
FROM arcade_photo_atoms p
INNER JOIN arcade a ON a.id = p.arcade
WHERE p.createdBy = {:user}
  AND p.arcade = {:arcade}
  AND p.public = 1
  AND a.public = 1
ORDER BY p.created ASC, p.id ASC
LIMIT 1
`).Bind(dbx.Params{"user": userID, "arcade": arcadeID}).Rows()
	if err != nil {
		return 0, fmt.Errorf("query arcade backfill photo submission failed: %w", err)
	}
	defer photoRows.Close()

	if photoRows.Next() {
		var rowArcade string
		if err := photoRows.Scan(&rowArcade); err != nil {
			return 0, fmt.Errorf("scan arcade backfill photo submission failed: %w", err)
		}
		if _, granted, err := AwardExpTx(txApp, userID, ArcadePhotoSubmissionKind(arcadeID), 5, currentExp); err != nil {
			return 0, err
		} else if granted {
			currentExp += 5
		}
	}
	if err := photoRows.Err(); err != nil {
		return 0, fmt.Errorf("iterate arcade backfill photo submission failed: %w", err)
	}

	return currentExp, nil
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
