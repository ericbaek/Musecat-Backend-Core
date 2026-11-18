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

// TrustedReader reports whether a deployment vouches for a request as a
// server-side reader, such as the web server rendering public pages with the
// sitemap secret. It never authenticates a user.
type TrustedReader func(*core.RequestEvent) bool

const trustedReaderKey = "musecat.trustedReader"

func RegisterTrustedReader(app core.App, reader TrustedReader) {
	app.Store().Set(trustedReaderKey, reader)
}

// IsTrustedReader is false when no deployment registered a reader, so
// standalone Core treats every unauthenticated request as a guest.
func IsTrustedReader(re *core.RequestEvent) bool {
	reader, _ := re.App.Store().Get(trustedReaderKey).(TrustedReader)
	return reader != nil && reader(re)
}
