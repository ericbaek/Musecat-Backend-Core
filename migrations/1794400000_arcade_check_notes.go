package migrations

import (
	"github.com/ericbaek/musecat-backend-core/service/arcadechecknote"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() { m.Register(arcadechecknote.EnsureSchema, func(core.App) error { return nil }) }
