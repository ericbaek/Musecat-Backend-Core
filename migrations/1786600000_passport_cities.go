package migrations

import (
	"github.com/ericbaek/musecat-backend-core/passportcities"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() { m.Register(passportcities.EnsureSchema, func(app core.App) error { return nil }) }
