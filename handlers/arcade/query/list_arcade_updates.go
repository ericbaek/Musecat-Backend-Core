package query

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

var arcadeUpdatePartOrder = []string{"basic", "hour", "sns", "gtk", "game", "bulk_game_version", "photo", "flag", "flag_reaction", "visit"}

type arcadeUpdateBlockRow struct {
	ArcadeID       string
	Changed        string
	ChangedBy      string
	Created        string
	BlockStartedAt string
	BlockEndedAt   string
}

// ListArcadeUpdates handles GET /arcades/updates.
func ListArcadeUpdates(re *core.RequestEvent) error {
	q := re.Request.URL.Query()

	limit := 5
	if rawLimit := strings.TrimSpace(q.Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error": "invalid 'limit' value; expected positive integer",
			})
		}
		if parsed > 50 {
			return re.JSON(http.StatusBadRequest, map[string]any{
				"error": "invalid 'limit' value; maximum is 50",
			})
		}
		limit = parsed
	}

	country := strings.TrimSpace(q.Get("country"))
	filterParts := []string{
		"a.public = 1",
		"a.closed = 0",
	}
	params := dbx.Params{
		"limit": limit,
	}
	if country != "" {
		filterParts = append(filterParts, "a.country = {:country}")
		params["country"] = country
	}

	sql := fmt.Sprintf(`
WITH filtered AS (
	SELECT
		c.arcade,
		c.changed,
		c."by" AS changed_by,
		c.created,
		c.id
	FROM arcade_changelog c
	INNER JOIN arcade a ON a.id = c.arcade
	WHERE %s
	UNION ALL
	SELECT
		f.arcade,
		'flag' AS changed,
		f."createdBy" AS changed_by,
		f.created,
		f.id
	FROM arcade_flag f
	INNER JOIN arcade a ON a.id = f.arcade
	WHERE %s
	UNION ALL
	SELECT
		f.arcade,
		'flag_reaction' AS changed,
		r."createdBy" AS changed_by,
		r.created,
		r.id
	FROM arcade_flag_reaction r
	INNER JOIN arcade_flag f ON f.id = r.flag
	INNER JOIN arcade a ON a.id = f.arcade
	WHERE %s
	UNION ALL
	SELECT
		v.arcade,
		'visit' AS changed,
		v.user AS changed_by,
		v.visited_at AS created,
		v.id
	FROM arcade_visit v
	INNER JOIN arcade a ON a.id = v.arcade
	INNER JOIN user_info ui ON ui.id = v.user
	WHERE %s
),
ordered AS (
	SELECT
		arcade,
		changed,
		changed_by,
		created,
		id,
		LAG(arcade) OVER (ORDER BY created DESC, id DESC) AS prev_arcade
	FROM filtered
),
blocks AS (
	SELECT
		arcade,
		changed,
		changed_by,
		created,
		id,
		SUM(
			CASE
				WHEN prev_arcade IS NULL OR prev_arcade <> arcade THEN 1
				ELSE 0
			END
		) OVER (ORDER BY created DESC, id DESC) AS block_id
	FROM ordered
),
block_ranges AS (
	SELECT
		block_id,
		arcade,
		MAX(created) AS block_started_at,
		MIN(created) AS block_ended_at
	FROM blocks
	GROUP BY block_id, arcade
),
latest_block_per_arcade AS (
	SELECT
		block_id,
		arcade,
		block_started_at,
		block_ended_at,
		ROW_NUMBER() OVER (
			PARTITION BY arcade
			ORDER BY block_started_at DESC, block_id ASC
		) AS arcade_rank
	FROM block_ranges
),
selected_blocks AS (
	SELECT
		block_id,
		arcade,
		block_started_at,
		block_ended_at
	FROM latest_block_per_arcade
	WHERE arcade_rank = 1
	ORDER BY block_started_at DESC, block_id ASC
	LIMIT {:limit}
)
SELECT
	b.arcade,
	b.changed,
	b.changed_by,
	b.created,
	s.block_started_at,
	s.block_ended_at
FROM blocks b
INNER JOIN selected_blocks s ON s.block_id = b.block_id
ORDER BY s.block_started_at DESC, s.block_id ASC, b.created DESC, b.id DESC
	`, strings.Join(filterParts, " AND "), strings.Join(filterParts, " AND "), strings.Join(filterParts, " AND "), strings.Join(filterParts, " AND ")+" AND ui.visit_visibility IN ('summary', 'full')")

	rows, err := re.App.DB().NewQuery(sql).Bind(params).Rows()
	if err != nil {
		return re.JSON(http.StatusBadGateway, map[string]any{
			"error":   "failed to load arcade updates",
			"details": err.Error(),
		})
	}
	defer rows.Close()

	type entryState struct {
		payload map[string]any
		partSet map[string]struct{}
	}

	entries := make([]*entryState, 0, limit)
	entryByArcade := map[string]*entryState{}

	for rows.Next() {
		raw := dbx.NullStringMap{}
		if err := rows.ScanMap(raw); err != nil {
			return re.JSON(http.StatusBadGateway, map[string]any{
				"error":   "failed to decode arcade updates",
				"details": err.Error(),
			})
		}

		row := arcadeUpdateBlockRow{
			ArcadeID:       nullStringMapValue(raw, "arcade"),
			Changed:        nullStringMapValue(raw, "changed"),
			ChangedBy:      nullStringMapValue(raw, "changed_by"),
			Created:        nullStringMapValue(raw, "created"),
			BlockStartedAt: nullStringMapValue(raw, "block_started_at"),
			BlockEndedAt:   nullStringMapValue(raw, "block_ended_at"),
		}
		if row.ArcadeID == "" {
			continue
		}

		entry, ok := entryByArcade[row.ArcadeID]
		if !ok {
			arcadeRec, err := re.App.FindRecordById("arcade", row.ArcadeID)
			if err != nil {
				return re.JSON(http.StatusBadGateway, map[string]any{
					"error":   "failed to load arcade summary",
					"details": err.Error(),
				})
			}

			payload := buildArcadeSummary(re.App, arcadeRec)
			if payload == nil {
				continue
			}
			payload["updated"] = arcadeRec.Get("updated")
			payload["block_started_at"] = row.BlockStartedAt
			payload["block_ended_at"] = row.BlockEndedAt
			payload["changes"] = []map[string]any{}

			entry = &entryState{
				payload: payload,
				partSet: map[string]struct{}{},
			}
			entryByArcade[row.ArcadeID] = entry
			entries = append(entries, entry)
		}

		if row.Changed != "" {
			entry.partSet[row.Changed] = struct{}{}
		}
		entry.payload["changes"] = append(entry.payload["changes"].([]map[string]any), map[string]any{
			"part":    row.Changed,
			"by":      row.ChangedBy,
			"created": row.Created,
		})
	}

	out := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		entry.payload["updated_parts"] = orderedArcadeUpdateParts(entry.partSet)
		out = append(out, entry.payload)
	}

	return re.JSON(http.StatusOK, out)
}

func nullStringMapValue(raw dbx.NullStringMap, key string) string {
	value, ok := raw[key]
	if !ok || !value.Valid {
		return ""
	}
	return value.String
}

func orderedArcadeUpdateParts(partSet map[string]struct{}) []string {
	out := make([]string, 0, len(partSet))
	for _, part := range arcadeUpdatePartOrder {
		if _, ok := partSet[part]; ok {
			out = append(out, part)
		}
	}
	return out
}
