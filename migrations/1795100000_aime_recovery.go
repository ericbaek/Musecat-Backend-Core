package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(usernotification.EnsureAimeSchema, func(core.App) error { return fmt.Errorf("Aime recovery is forward-only") })
}
