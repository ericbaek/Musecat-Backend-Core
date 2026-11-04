package delegation

import (
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
)

const Usage = "oauth_client_usage"

// Aggregate counters contain no user identifiers, URLs, codes, or credentials.
func EnsureUsageSchema(app core.App) error {
	c := core.NewBaseCollection(Usage)
	c.Fields.Add(&core.TextField{Name: "client", Required: true, Max: 128}, &core.TextField{Name: "day", Required: true, Max: 10}, &core.SelectField{Name: "kind", Required: true, MaxSelect: 1, Values: []string{"api", "code", "refresh"}}, &core.NumberField{Name: "requests", OnlyInt: true}, &core.NumberField{Name: "errors", OnlyInt: true})
	c.AddIndex("idx_oauth_usage_day", true, "client, day, kind", "")
	return ensureCollection(app, c)
}

func (m *Manager) AccessClient(app core.App, raw string) string {
	if m == nil || len(raw) > 512 {
		return ""
	}
	token, err := find(app, Tokens, "access_hash={:hash}", dbx.Params{"hash": hash(raw)})
	if err != nil {
		return ""
	}
	grant, err := app.FindRecordById(Grants, token.GetString("grant"))
	if err != nil {
		return ""
	}
	return grant.GetString("client")
}

func (m *Manager) RecordUsage(app core.App, client, kind string, status int) {
	if m == nil || client == "" {
		return
	}
	failed := 0
	if status >= 400 {
		failed = 1
	}
	_, err := app.DB().NewQuery("INSERT INTO oauth_client_usage (id,client,day,kind,requests,errors) VALUES ({:id},{:client},{:day},{:kind},1,{:errors}) ON CONFLICT(client,day,kind) DO UPDATE SET requests=requests+1,errors=errors+excluded.errors").Bind(dbx.Params{"id": security.RandomString(15), "client": client, "day": time.Now().UTC().Format("2006-01-02"), "kind": kind, "errors": failed}).Execute()
	if err != nil {
		app.Logger().Warn("OAuth usage counter unavailable")
	}
}
