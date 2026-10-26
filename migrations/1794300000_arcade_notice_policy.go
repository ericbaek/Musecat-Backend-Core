package migrations

import (
	"github.com/ericbaek/musecat-backend-core/handlers/arcade/notice"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	// Core only boots fresh schemas; Full independently migrates existing records.
	m.Register(notice.EnsurePolicySchema, func(core.App) error { return nil })
}
