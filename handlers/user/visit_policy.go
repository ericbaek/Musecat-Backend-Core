package user

import (
	"github.com/pocketbase/pocketbase/core"
	"time"
)

// VisitAttempt contains server-validated inputs for a deployment-owned policy.
type VisitAttempt struct {
	User, Arcade string
	Lat, Lon     float64
	At           time.Time
}

type VisitPolicy struct {
	Evaluate func(core.App, VisitAttempt) (string, error)
	Decorate func(*core.Record, VisitAttempt)
	// AggregateCondition is trusted deployment SQL against arcade_visit alias v.
	AggregateCondition string
}

func RegisterVisitPolicy(app core.App, policy VisitPolicy) {
	app.Store().Set("deployment.visitPolicy", policy)
}
func evaluateVisitPolicy(app core.App, attempt VisitAttempt) (string, error) {
	policy, _ := app.Store().Get("deployment.visitPolicy").(VisitPolicy)
	if policy.Evaluate == nil {
		return "", nil
	}
	return policy.Evaluate(app, attempt)
}
func decorateVisitRecord(app core.App, record *core.Record, attempt VisitAttempt) {
	policy, _ := app.Store().Get("deployment.visitPolicy").(VisitPolicy)
	if policy.Decorate != nil {
		policy.Decorate(record, attempt)
	}
}
func VisitAggregateCondition(app core.App) string {
	policy, _ := app.Store().Get("deployment.visitPolicy").(VisitPolicy)
	if policy.AggregateCondition != "" {
		return policy.AggregateCondition
	}
	return "1=1"
}
