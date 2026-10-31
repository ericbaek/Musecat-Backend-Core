package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(messaging.EnsureBotSchema, func(core.App) error { return fmt.Errorf("inquiry automation is forward-only") })
}
