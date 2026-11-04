package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(delegation.EnsureOIDCSchema, func(core.App) error { return fmt.Errorf("OIDC schema is forward-only") })
}
