package migrations

import (
	"github.com/ericbaek/musecat-backend-core/service/announcement"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() { m.Register(announcement.EnsureSchema, func(core.App) error { return nil }) }
