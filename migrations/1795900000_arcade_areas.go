package migrations

import (
	"github.com/ericbaek/musecat-backend-core/arcadearea"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() { m.Register(arcadearea.EnsureSchema, func(app core.App) error { return nil }) }
