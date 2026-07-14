package arcadeinternal

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	userhandler "github.com/ericbaek/musecat-backend-core/handlers/user"
)

const SupporterScoreThreshold = 300

type SupporterLedgerEntry struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	Source      string         `json:"source"`
	Action      string         `json:"action"`
	Exp         int            `json:"exp"`
	PreviousExp int            `json:"previous_exp"`
	NewExp      int            `json:"new_exp"`
	Created     string         `json:"created"`
	ArcadeID    string         `json:"arcade_id,omitempty"`
	ArcadeName  string         `json:"arcade_name,omitempty"`
	TargetID    string         `json:"target_id,omitempty"`
	TargetName  string         `json:"target_name,omitempty"`
	Detail      map[string]any `json:"detail,omitempty"`
}

type SupporterLatestRequest struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	ExpTotal       int    `json:"exp_total"`
	Qualified      bool   `json:"qualified"`
	Created        string `json:"created"`
	DecisionReason string `json:"decision_reason,omitempty"`
}

type SupporterScoreResponse struct {
	TotalExp      int                     `json:"total_exp"`
	AttendanceExp int                     `json:"attendance_exp"`
	Qualified     bool                    `json:"qualified"`
	Threshold     int                     `json:"threshold"`
	CanRequest    bool                    `json:"can_request"`
	Entries       []SupporterLedgerEntry  `json:"entries"`
	LatestRequest *SupporterLatestRequest `json:"latest_request,omitempty"`
}

type supporterLogRow struct {
	ID          string
	Kind        string
	PreviousExp int
	NewExp      int
	DiffExp     int
	Created     string
}

func BuildSupporterScore(app core.App, userID string) (*SupporterScoreResponse, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("user id is required")
	}

	totalExp, err := userhandler.LoadCurrentExp(app, userID)
	if err != nil {
		return nil, err
	}

	rows, err := app.DB().NewQuery(`
SELECT
	id,
	kind,
	COALESCE(previous_exp, 0) AS previous_exp,
	COALESCE(new_exp, 0) AS new_exp,
	COALESCE(diff_exp, 0) AS diff_exp,
	COALESCE(created, '') AS created
FROM user_level_log
WHERE "user" = {:user}
ORDER BY created DESC, id DESC
`).Bind(dbx.Params{"user": userID}).Rows()
	if err != nil {
		return nil, fmt.Errorf("query supporter ledger failed: %w", err)
	}
	defer rows.Close()

	logRows := make([]supporterLogRow, 0)
	arcadeIDs := map[string]struct{}{}
	flagIDs := map[string]struct{}{}
	reactionIDs := map[string]struct{}{}

	for rows.Next() {
		var row supporterLogRow
		if err := rows.Scan(&row.ID, &row.Kind, &row.PreviousExp, &row.NewExp, &row.DiffExp, &row.Created); err != nil {
			return nil, fmt.Errorf("scan supporter ledger failed: %w", err)
		}
		row.Kind = strings.TrimSpace(row.Kind)
		logRows = append(logRows, row)

		source, action, arcadeID, targetID, _ := parseSupporterLedgerKind(row.Kind)
		switch source {
		case "arcade":
			if arcadeID != "" {
				arcadeIDs[arcadeID] = struct{}{}
			}
		case "flag":
			if targetID != "" {
				flagIDs[targetID] = struct{}{}
			}
		case "flag_reaction":
			if targetID != "" {
				reactionIDs[targetID] = struct{}{}
			}
		}
		if action == "photo_submission" && arcadeID != "" {
			arcadeIDs[arcadeID] = struct{}{}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate supporter ledger failed: %w", err)
	}

	arcadeNameByID, err := loadArcadeNames(app, keysOfSet(arcadeIDs))
	if err != nil {
		return nil, err
	}
	flagArcadeByID, err := loadFlagArcadeRefs(app, keysOfSet(flagIDs))
	if err != nil {
		return nil, err
	}
	reactionArcadeByID, err := loadReactionArcadeRefs(app, keysOfSet(reactionIDs))
	if err != nil {
		return nil, err
	}

	entries := make([]SupporterLedgerEntry, 0, len(logRows))
	attendanceExp := 0
	for _, row := range logRows {
		source, action, arcadeID, targetID, detail := parseSupporterLedgerKind(row.Kind)
		entry := SupporterLedgerEntry{
			ID:          row.ID,
			Kind:        row.Kind,
			Source:      source,
			Action:      action,
			Exp:         row.DiffExp,
			PreviousExp: row.PreviousExp,
			NewExp:      row.NewExp,
			Created:     row.Created,
			Detail:      detail,
		}

		switch source {
		case "arcade":
			entry.ArcadeID = arcadeID
			entry.ArcadeName = arcadeNameByID[arcadeID]
			entry.TargetID = arcadeID
			entry.TargetName = entry.ArcadeName
		case "flag":
			entry.TargetID = targetID
			if ref, ok := flagArcadeByID[targetID]; ok {
				entry.ArcadeID = ref.ArcadeID
				entry.ArcadeName = ref.ArcadeName
			}
		case "flag_reaction":
			entry.TargetID = targetID
			if ref, ok := reactionArcadeByID[targetID]; ok {
				entry.ArcadeID = ref.ArcadeID
				entry.ArcadeName = ref.ArcadeName
			}
		}

		if source == "attendance" {
			attendanceExp += row.DiffExp
		}
		entries = append(entries, entry)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Created == entries[j].Created {
			return entries[i].ID > entries[j].ID
		}
		return entries[i].Created > entries[j].Created
	})

	out := &SupporterScoreResponse{
		TotalExp:      totalExp,
		AttendanceExp: attendanceExp,
		Threshold:     SupporterScoreThreshold,
		Qualified:     totalExp >= SupporterScoreThreshold,
		Entries:       entries,
	}

	latestRequest, err := loadLatestSupporterRequest(app, userID)
	if err != nil {
		return nil, err
	}
	if latestRequest != nil {
		out.LatestRequest = latestRequest
	}
	out.CanRequest = out.Qualified && (latestRequest == nil || (latestRequest.Status != "pending" && latestRequest.Status != "approved"))

	return out, nil
}

type arcadeRef struct {
	ArcadeID   string
	ArcadeName string
}

func parseSupporterLedgerKind(kind string) (source, action, arcadeID, targetID string, detail map[string]any) {
	kind = strings.TrimSpace(kind)
	detail = map[string]any{}
	switch {
	case strings.HasPrefix(kind, "xp:attendance:service:"):
		source = "attendance"
		action = "check_in"
		detail["day"] = strings.TrimPrefix(kind, "xp:attendance:service:")
	case strings.HasPrefix(kind, "xp:arcade-public:"):
		source = "arcade"
		action = "public"
		arcadeID = strings.TrimPrefix(kind, "xp:arcade-public:")
	case strings.HasPrefix(kind, "xp:arcade-edit:"):
		source = "arcade"
		rest := strings.TrimPrefix(kind, "xp:arcade-edit:")
		parts := strings.SplitN(rest, ":", 3)
		if len(parts) > 0 {
			action = parts[0]
		}
		if len(parts) > 1 {
			arcadeID = parts[1]
		}
		if len(parts) > 2 && parts[2] != "" {
			detail["grant_key"] = parts[2]
		}
	case strings.HasPrefix(kind, "xp:arcade-photo-submission:"):
		source = "arcade"
		action = "photo_submission"
		arcadeID = strings.TrimPrefix(kind, "xp:arcade-photo-submission:")
	case strings.HasPrefix(kind, "xp:arcade-visit:"):
		source = "visit"
		action = "verified_visit"
		targetID = strings.TrimPrefix(kind, "xp:arcade-visit:")
	case strings.HasPrefix(kind, "xp:flag-reaction:"):
		source = "flag_reaction"
		action = "reaction"
		targetID = strings.TrimPrefix(kind, "xp:flag-reaction:")
	case strings.HasPrefix(kind, "xp:flag:"):
		source = "flag"
		action = "flag"
		targetID = strings.TrimPrefix(kind, "xp:flag:")
	default:
		source = "other"
		action = "other"
	}

	return source, action, strings.TrimSpace(arcadeID), strings.TrimSpace(targetID), detail
}

func loadArcadeNames(app core.App, arcadeIDs []string) (map[string]string, error) {
	arcadeIDs = uniqueStrings(arcadeIDs)
	if len(arcadeIDs) == 0 {
		return map[string]string{}, nil
	}

	params := dbx.Params{}
	placeholders := make([]string, 0, len(arcadeIDs))
	for i, arcadeID := range arcadeIDs {
		key := fmt.Sprintf("arcade_%d", i)
		params[key] = arcadeID
		placeholders = append(placeholders, "{:"+key+"}")
	}

	rows, err := app.DB().NewQuery(fmt.Sprintf(`
SELECT a.id AS arcade_id, COALESCE(b.name, '') AS arcade_name
FROM arcade a
LEFT JOIN arcade_basic b ON b.id = a.basic
WHERE a.id IN (%s)
`, strings.Join(placeholders, ","))).Bind(params).Rows()
	if err != nil {
		return nil, fmt.Errorf("query arcade names failed: %w", err)
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var arcadeID, arcadeName string
		if err := rows.Scan(&arcadeID, &arcadeName); err != nil {
			return nil, fmt.Errorf("scan arcade name failed: %w", err)
		}
		out[strings.TrimSpace(arcadeID)] = strings.TrimSpace(arcadeName)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate arcade names failed: %w", err)
	}
	return out, nil
}

func loadFlagArcadeRefs(app core.App, flagIDs []string) (map[string]arcadeRef, error) {
	flagIDs = uniqueStrings(flagIDs)
	if len(flagIDs) == 0 {
		return map[string]arcadeRef{}, nil
	}

	params := dbx.Params{}
	placeholders := make([]string, 0, len(flagIDs))
	for i, flagID := range flagIDs {
		key := fmt.Sprintf("flag_%d", i)
		params[key] = flagID
		placeholders = append(placeholders, "{:"+key+"}")
	}

	rows, err := app.DB().NewQuery(fmt.Sprintf(`
SELECT f.id AS flag_id, f.arcade AS arcade_id, COALESCE(b.name, '') AS arcade_name
FROM arcade_flag f
INNER JOIN arcade a ON a.id = f.arcade
LEFT JOIN arcade_basic b ON b.id = a.basic
WHERE f.id IN (%s)
`, strings.Join(placeholders, ","))).Bind(params).Rows()
	if err != nil {
		return nil, fmt.Errorf("query flag arcade refs failed: %w", err)
	}
	defer rows.Close()

	out := map[string]arcadeRef{}
	for rows.Next() {
		var flagID, arcadeID, arcadeName string
		if err := rows.Scan(&flagID, &arcadeID, &arcadeName); err != nil {
			return nil, fmt.Errorf("scan flag arcade ref failed: %w", err)
		}
		out[strings.TrimSpace(flagID)] = arcadeRef{
			ArcadeID:   strings.TrimSpace(arcadeID),
			ArcadeName: strings.TrimSpace(arcadeName),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate flag arcade refs failed: %w", err)
	}
	return out, nil
}

func loadReactionArcadeRefs(app core.App, reactionIDs []string) (map[string]arcadeRef, error) {
	reactionIDs = uniqueStrings(reactionIDs)
	if len(reactionIDs) == 0 {
		return map[string]arcadeRef{}, nil
	}

	params := dbx.Params{}
	placeholders := make([]string, 0, len(reactionIDs))
	for i, reactionID := range reactionIDs {
		key := fmt.Sprintf("reaction_%d", i)
		params[key] = reactionID
		placeholders = append(placeholders, "{:"+key+"}")
	}

	rows, err := app.DB().NewQuery(fmt.Sprintf(`
SELECT r.id AS reaction_id, f.arcade AS arcade_id, COALESCE(b.name, '') AS arcade_name
FROM arcade_flag_reaction r
INNER JOIN arcade_flag f ON f.id = r.flag
INNER JOIN arcade a ON a.id = f.arcade
LEFT JOIN arcade_basic b ON b.id = a.basic
WHERE r.id IN (%s)
`, strings.Join(placeholders, ","))).Bind(params).Rows()
	if err != nil {
		return nil, fmt.Errorf("query reaction arcade refs failed: %w", err)
	}
	defer rows.Close()

	out := map[string]arcadeRef{}
	for rows.Next() {
		var reactionID, arcadeID, arcadeName string
		if err := rows.Scan(&reactionID, &arcadeID, &arcadeName); err != nil {
			return nil, fmt.Errorf("scan reaction arcade ref failed: %w", err)
		}
		out[strings.TrimSpace(reactionID)] = arcadeRef{
			ArcadeID:   strings.TrimSpace(arcadeID),
			ArcadeName: strings.TrimSpace(arcadeName),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reaction arcade refs failed: %w", err)
	}
	return out, nil
}

func loadLatestSupporterRequest(app core.App, userID string) (*SupporterLatestRequest, error) {
	recs, err := app.FindRecordsByFilter(
		CollectionSupporterRequest,
		"createdBy = {:user}",
		"-created",
		1,
		0,
		dbx.Params{"user": userID},
	)
	if err != nil {
		return nil, fmt.Errorf("query latest supporter request failed: %w", err)
	}
	if len(recs) == 0 {
		return nil, nil
	}

	rec := recs[0]
	out := &SupporterLatestRequest{
		ID:             rec.Id,
		Status:         strings.TrimSpace(rec.GetString("status")),
		ExpTotal:       rec.GetInt("score_total"),
		Qualified:      rec.GetBool("qualified"),
		Created:        strings.TrimSpace(rec.GetString("created")),
		DecisionReason: strings.TrimSpace(rec.GetString("decision_reason")),
	}
	return out, nil
}

func keysOfSet(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		k = strings.TrimSpace(k)
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
