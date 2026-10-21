package migrations

import (
	"github.com/ericbaek/musecat-backend-core/service/arcadereport"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(arcadereport.EnsureSettingsCapacitySchema, func(core.App) error { return nil })
}
