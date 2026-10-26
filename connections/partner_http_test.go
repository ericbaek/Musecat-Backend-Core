package connections

import (
	"net/http"
	"net/netip"
	"testing"
)

func TestPartnerClientBlocksPrivateAndSpecialAddresses(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "::1", "10.0.0.1", "172.16.1.2", "192.168.1.2", "169.254.169.254", "100.100.100.200", "0.0.0.0", "::ffff:127.0.0.1", "fd00::1", "2001:db8::1", "2002:7f00:1::1"} {
		if publicPartnerIP(netip.MustParseAddr(raw)) {
			t.Fatal("unsafe address allowed", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		if !publicPartnerIP(netip.MustParseAddr(raw)) {
			t.Fatal("public address rejected", raw)
		}
	}

	client := NewPartnerHTTPClient(nil)
	for _, target := range []string{"https://127.0.0.1/", "https://[::1]/", "https://169.254.169.254/"} {
		if _, err := client.Get(target); err == nil {
			t.Fatal("unsafe target allowed", target)
		}
	}
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Fatal("environment proxy bypasses pinned dial")
	}
}
