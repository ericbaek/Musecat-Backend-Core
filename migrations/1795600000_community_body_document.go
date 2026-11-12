package migrations

import (
	"github.com/ericbaek/musecat-backend-core/service/communitymention"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() { m.Register(communitymention.EnsureSchema, func(core.App) error { return nil }) }
