package connections

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"time"
)

// PurgeExpiredRequests removes secrets from expired attempts, including interrupted exchanges.
func PurgeExpiredRequests(app core.App, now time.Time) error {
	return app.RunInTransaction(func(tx core.App) error {
		_, err := tx.DB().NewQuery("DELETE FROM external_connection_request WHERE expires <= {:now}").Bind(dbx.Params{"now": now.UTC().Format("2006-01-02 15:04:05.000Z")}).Execute()
		return err
	})
}

// RegisterRequestCleanupCron is called by executables, never by coreapp.Configure.
func RegisterRequestCleanupCron(app core.App) {
	app.Cron().MustAdd("external_connection_request_cleanup", "*/5 * * * *", func() {
		if err := PurgeExpiredRequests(app, time.Now()); err != nil {
			app.Logger().Error("Failed to purge expired external connection requests")
		}
	})
}
