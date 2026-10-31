package migrations

import (
	"fmt"
	"github.com/ericbaek/musecat-backend-core/service/messaging"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		if err := messaging.EnsureUnifiedSchema(app); err != nil {
			return err
		}
		return messaging.RetireEmptyFeedbackSchema(app)
	}, func(core.App) error { return fmt.Errorf("unified messaging is forward-only") })
}
