package migrations

import (
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error { return nativeauth.InstallSchema(app) }, func(app core.App) error { return nil })
}
