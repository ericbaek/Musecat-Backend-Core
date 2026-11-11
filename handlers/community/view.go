package community

import (
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"net/http"
	"regexp"
	"time"
)

var viewSessionPattern = regexp.MustCompile(`^[a-zA-Z0-9-]{1,64}$`)

// ViewPost leases only the authenticated account's current visible detail view.
func ViewPost(re *core.RequestEvent) error {
	id := re.Request.URL.Query().Get("id")
	if err := activePost(re.App, id); err != nil {
		return err
	}
	var body struct {
		Session string `json:"session"`
	}
	if err := re.BindBody(&body); err != nil || !viewSessionPattern.MatchString(body.Session) {
		return apis.NewBadRequestError("Invalid view session", nil)
	}
	now := time.Now().UTC()
	err := re.App.RunInTransaction(func(tx core.App) error {
		rows, err := tx.FindRecordsByFilter(usernotification.CommunityViews, "user={:user} && post={:post} && session={:session}", "", 1, 0, dbx.Params{"user": re.Auth.Id, "post": id, "session": body.Session})
		if err != nil {
			return err
		}
		if re.Request.Method == http.MethodDelete {
			if len(rows) > 0 {
				return tx.Delete(rows[0])
			}
			return nil
		}
		var row *core.Record
		if len(rows) > 0 {
			row = rows[0]
		} else {
			col, err := tx.FindCollectionByNameOrId(usernotification.CommunityViews)
			if err != nil {
				return err
			}
			row = core.NewRecord(col)
		}
		row.Set("user", re.Auth.Id)
		row.Set("post", id)
		row.Set("session", body.Session)
		row.Set("expires", now.Add(45*time.Second))
		if err := tx.Save(row); err != nil {
			return err
		}
		// Acknowledge the complete history present at entry, including groups over 100 events.
		if _, err := tx.DB().NewQuery("UPDATE user_notification SET read=true WHERE user={:user} AND community_post={:post} AND read=false AND created<={:now}").Bind(dbx.Params{"user": re.Auth.Id, "post": id, "now": now.Format("2006-01-02 15:04:05.000Z")}).Execute(); err != nil {
			return err
		}
		expired, err := tx.FindRecordsByFilter(usernotification.CommunityViews, "expires<={:now}", "expires", 100, 0, dbx.Params{"now": now})
		if err != nil {
			return err
		}
		for _, old := range expired {
			if err := tx.Delete(old); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	re.Response.Header().Set("Cache-Control", "private, no-store")
	return re.JSON(http.StatusOK, map[string]bool{"ok": true})
}
