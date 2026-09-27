package coreapp

import (
	"crypto/subtle"
	"net"
	"net/netip"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

// clientIPForwarding accepts IP attribution only from the authenticated web BFF.
// Native/direct callers continue to use PocketBase's normal proxy configuration.
func clientIPForwarding(secret string) *hook.Handler[*core.RequestEvent] {
	return &hook.Handler[*core.RequestEvent]{
		Id:       "musecatClientIP",
		Priority: apis.DefaultRateLimitMiddlewarePriority - 1,
		Func: func(e *core.RequestEvent) error {
			proof := e.Request.Header.Get("X-Musecat-Forward-Secret")
			ip, err := netip.ParseAddr(e.Request.Header.Get("X-Musecat-Client-IP"))
			// Never let these internal headers act as unsigned trusted proxy headers.
			e.Request.Header.Del("X-Musecat-Forward-Secret")
			e.Request.Header.Del("X-Musecat-Client-IP")
			if len(secret) < 32 || subtle.ConstantTimeCompare([]byte(proof), []byte(secret)) != 1 || err != nil || ip.Zone() != "" {
				return e.Next()
			}
			address := ip.Unmap().String()
			e.Request.RemoteAddr = net.JoinHostPort(address, "0")
			for _, header := range e.App.Settings().TrustedProxy.Headers {
				if !strings.EqualFold(header, "X-Musecat-Forward-Secret") {
					e.Request.Header.Set(header, address)
				}
			}
			return e.Next()
		},
	}
}
