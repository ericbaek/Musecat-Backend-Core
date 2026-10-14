package delegation

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"time"
)

func CleanupExpired(app core.App) error {
	return app.RunInTransaction(func(tx core.App) error {
		now := time.Now().UTC()
		for _, v := range []struct {
			table, filter, sort string
			params              dbx.Params
		}{
			{Requests, "expires < {:now}", "expires", dbx.Params{"now": now.Format("2006-01-02 15:04:05.000Z")}},
			{Codes, "(consumed=false && expires < {:now}) || (consumed=true && expires < {:old})", "expires", dbx.Params{"now": now.Format("2006-01-02 15:04:05.000Z"), "old": now.Add(-30 * 24 * time.Hour).Format("2006-01-02 15:04:05.000Z")}},
			{Tokens, "refresh_expires < {:now}", "refresh_expires", dbx.Params{"now": now.Format("2006-01-02 15:04:05.000Z")}},
		} {
			rows, err := tx.FindRecordsByFilter(v.table, v.filter, v.sort, 1000, 0, v.params)
			if err != nil {
				return err
			}
			for _, row := range rows {
				if err = tx.Delete(row); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
func RegisterCleanupCron(app *pocketbase.PocketBase) {
	app.Cron().MustAdd("oauth-delegation-cleanup", "*/5 * * * *", func() {
		if e := CleanupExpired(app); e != nil {
			app.Logger().Error("OAuth delegation cleanup failed", "error", e)
		}
	})
}
