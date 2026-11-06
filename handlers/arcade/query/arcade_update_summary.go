package query

import (
	"context"
	"strconv"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/sync/singleflight"
)

var arcadeUpdateSummaryLoads singleflight.Group

// Share only an in-flight public summary query. Nothing is cached across loads,
// so visibility changes and visit timeline opt-outs remain immediately visible.
func loadSharedArcadeUpdateSummary(ctx context.Context, app core.App, sql string) ([]arcadeUpdateBlockRow, error) {
	result := arcadeUpdateSummaryLoads.DoChan(appCacheKey(app), func() (any, error) {
		// One disconnected caller must not cancel the query for other waiting callers.
		queryContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 12*time.Second)
		defer cancel()
		rows, _, err := loadArcadeUpdateRows(queryContext, app, sql, nil)
		return rows, err
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.([]arcadeUpdateBlockRow), nil
	}
}

func loadArcadeUpdateRows(ctx context.Context, app core.App, sql string, params dbx.Params) ([]arcadeUpdateBlockRow, int64, error) {
	rows, err := app.DB().NewQuery(sql).Bind(params).WithContext(ctx).Rows()
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []arcadeUpdateBlockRow
	var total int64
	for rows.Next() {
		raw := dbx.NullStringMap{}
		if err := rows.ScanMap(raw); err != nil {
			return nil, 0, err
		}
		total, err = strconv.ParseInt(nullStringMapValue(raw, "total_blocks"), 10, 64)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, arcadeUpdateBlockRow{
			BlockID: nullStringMapValue(raw, "block_id"), ArcadeID: nullStringMapValue(raw, "arcade"),
			Changed: nullStringMapValue(raw, "changed"), ChangedBy: nullStringMapValue(raw, "changed_by"),
			Created: nullStringMapValue(raw, "created"), BulkID: nullStringMapValue(raw, "bulk_id"),
			BlockStartedAt: nullStringMapValue(raw, "block_started_at"), BlockEndedAt: nullStringMapValue(raw, "block_ended_at"),
		})
	}
	return out, total, rows.Err()
}

// The public endpoint only needs each arcade's newest block. Select those six
// starts first, then find their older boundary; history keeps its full grouping.
func arcadeUpdateSummarySQL(filteredSQL string) string {
	return filteredSQL + `,
ranked_events AS (
 SELECT f.*, ROW_NUMBER() OVER (PARTITION BY arcade ORDER BY created DESC, id DESC) AS event_rank
 FROM filtered f
),
summary_candidates AS (
 SELECT l.*, a.country, ROW_NUMBER() OVER (ORDER BY l.created DESC, l.id DESC) AS summary_rank
 FROM ranked_events l JOIN arcade a ON a.id = l.arcade WHERE event_rank = 1
),
selected_blocks AS MATERIALIZED (
 SELECT s.*, (
  SELECT json_array(f.created, f.id) FROM filtered f
  WHERE f.arcade <> s.arcade AND (f.created, f.id) < (s.created, s.id)
  ORDER BY f.created DESC, f.id DESC LIMIT 1
 ) AS preceding_other
 FROM summary_candidates s
 WHERE summary_rank <= 6 OR (
  country <> 'KR' AND summary_rank = (SELECT MIN(summary_rank) FROM summary_candidates WHERE country <> 'KR')
 )
)
SELECT f.arcade, f.changed, f.changed_by, f.created, f.bulk_id,
 s.created AS block_started_at,
 MIN(f.created) OVER (PARTITION BY s.summary_rank) AS block_ended_at,
 s.summary_rank AS block_id, 0 AS total_blocks
FROM selected_blocks s JOIN filtered f ON f.arcade = s.arcade
WHERE (f.created, f.id) <= (s.created, s.id)
 AND (s.preceding_other IS NULL OR (f.created, f.id) > (
  json_extract(s.preceding_other, '$[0]'), json_extract(s.preceding_other, '$[1]')
 ))
ORDER BY s.created DESC, s.summary_rank ASC, f.created DESC, f.id DESC
`
}
