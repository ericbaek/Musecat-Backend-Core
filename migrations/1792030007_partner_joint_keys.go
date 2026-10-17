package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/partnerapp"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(partnerapp.EnsureDirectionBColumn, func(app core.App) error { return fmt.Errorf("partner joint key schema is forward-only") })
}
