package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/partnerapp"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(partnerapp.EnsureSchema, func(app core.App) error { return fmt.Errorf("partner application schema is forward-only") })
}
