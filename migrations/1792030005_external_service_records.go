package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(connections.EnsureRecordSchema, func(app core.App) error { return fmt.Errorf("external service record schema is forward-only") })
}
