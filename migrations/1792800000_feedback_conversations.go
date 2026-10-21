package migrations

import (
	"github.com/ericbaek/musecat-backend-core/service/usernotification"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() { m.Register(usernotification.EnsureFeedbackSchema, func(core.App) error { return nil }) }
