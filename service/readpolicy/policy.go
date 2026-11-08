// Package readpolicy exposes a deployment-owned check before arcade detail is returned.
package readpolicy

import "github.com/pocketbase/pocketbase/core"

type Policy func(*core.RequestEvent, []string) error

const storeKey = "musecat.publicReadPolicy"

func Register(app core.App, policy Policy) { app.Store().Set(storeKey, policy) }

// Check receives only the arcade IDs actually exposed by this response. A nil
// policy preserves standalone Core behavior. Rejections must precede JSON writes.
func Check(re *core.RequestEvent, ids ...string) error {
	policy, _ := re.App.Store().Get(storeKey).(Policy)
	if policy == nil || len(ids) == 0 {
		return nil
	}
	return policy(re, ids)
}
