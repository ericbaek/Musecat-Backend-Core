package migrations

import (
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	// Core defines the empty-bootstrap inbox contract only. Existing-account
	// notice backfill belongs to Full's independently guarded migration.
	m.Register(usernotification.EnsureSchema, func(core.App) error { return nil })
}
