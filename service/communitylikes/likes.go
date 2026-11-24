// Package communitylikes owns read-only aggregates over community post likes
// that more than one handler package exposes.
package communitylikes

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// ReceivedAchievementLikeCount returns how many likes other users have given
// to the author's active achievement posts, over all time. Likes on removed
// posts and the author's own likes are excluded so the total matches what the
// public feed shows for that author.
func ReceivedAchievementLikeCount(app core.App, authorID string) (int, error) {
	if authorID == "" {
		return 0, nil
	}
	var row struct {
		Count int `db:"count"`
	}
	err := app.DB().NewQuery(`
		SELECT COUNT(*) AS count
		FROM community_post_like AS l
		JOIN community_post AS p ON p.id = l.post
		WHERE p.author = {:author}
		  AND p.status = 'active'
		  AND p.flair = 'achievement'
		  AND l.author != {:author}`).
		Bind(dbx.Params{"author": authorID}).
		One(&row)
	return row.Count, err
}
