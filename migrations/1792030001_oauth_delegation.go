package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/delegation"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(delegation.EnsureSchema, func(app core.App) error { return fmt.Errorf("OAuth delegation schema is forward-only") })
}
