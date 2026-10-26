package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/connections"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(connections.EnsureRecordRetryColumn, func(core.App) error { return fmt.Errorf("external record retry schema is forward-only") })
}
