package migrations

import (
	"github.com/ericbaek/musecat-backend-core/nativeauth"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(nativeauth.InstallWebHandoffSchema, func(core.App) error { return nil })
}
