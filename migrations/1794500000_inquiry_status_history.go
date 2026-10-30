package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(messaging.EnsureStatusHistorySchema, func(core.App) error { return fmt.Errorf("inquiry status history is forward-only") })
}
