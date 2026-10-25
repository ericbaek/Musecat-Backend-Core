package migrations

import (
	"github.com/ericbaek/musecat-backend-core/service/supportfeedback"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() { m.Register(supportfeedback.EnsureReplyPhotosSchema, func(core.App) error { return nil }) }
