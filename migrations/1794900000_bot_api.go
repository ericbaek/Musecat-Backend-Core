package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/botaccess"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(botaccess.EnsureSchema, func(core.App) error { return fmt.Errorf("bot attribution is forward-only") })
}
