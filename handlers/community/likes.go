package community

import (
	"net/http"
	"strconv"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

const postLikesPageSize = 30

type postLikeRow struct {
	Seq    int64  `db:"seq"`
	Author string `db:"author"`
}

// ListPostLikes handles GET /community/post/likes?id=<post>&cursor=<cursor>.
// Only the post's author may see who liked it; everyone else keeps seeing the
// aggregate count on the post itself. The author's own like is not listed.
//
// Like rows carry no timestamp, so the newest-first order and the opaque
// cursor use SQLite's insertion rowid rather than a schema change.
func ListPostLikes(re *core.RequestEvent) error {
	q := re.Request.URL.Query()
	id := q.Get("id")
	post, err := re.App.FindRecordById(CollectionPost, id)
	if err != nil || post.GetString("status") != "active" {
		return apis.NewNotFoundError("Community post not found", nil)
	}
	if post.GetString("author") != re.Auth.Id {
		return apis.NewForbiddenError("Only the author can see who liked this post", nil)
	}
	before := int64(-1)
	if raw := q.Get("cursor"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 36, 64)
		if len(raw) > 16 || err != nil || parsed <= 0 {
			return apis.NewBadRequestError("Invalid cursor", nil)
		}
		before = parsed
	}
	params := dbx.Params{"post": id, "self": re.Auth.Id, "before": before, "limit": postLikesPageSize + 1}
	var rows []postLikeRow
	if err := re.App.DB().NewQuery(`
		SELECT rowid AS seq, author FROM community_post_like
		WHERE post = {:post} AND author != {:self} AND ({:before} < 0 OR rowid < {:before})
		ORDER BY rowid DESC LIMIT {:limit}`).Bind(params).All(&rows); err != nil {
		return err
	}
	next := ""
	if len(rows) > postLikesPageSize {
		rows = rows[:postLikesPageSize]
		next = strconv.FormatInt(rows[postLikesPageSize-1].Seq, 36)
	}
	records := make([]*core.Record, 0, len(rows))
	for _, row := range rows {
		record := core.NewRecord(core.NewBaseCollection("community_post_like"))
		record.Set("author", row.Author)
		records = append(records, record)
	}
	authors, err := authorSummaries(re.App, records)
	if err != nil {
		return err
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		summary := authors[row.Author]
		items = append(items, map[string]any{
			"user":       row.Author,
			"name":       summary.Name,
			"avatar_url": summary.Avatar,
		})
	}
	privateSocialResponse(re)
	return re.JSON(http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}
