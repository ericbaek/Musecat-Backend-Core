package reporting

import "testing"

func TestPushEndpointAllowlist(t *testing.T) {
	for _, endpoint := range []string{
		"https://jmt17.google.com.evil.test/send", "https://jmt17.google.com:8443/send", "https://user@jmt17.google.com/send", "http://jmt17.google.com/send", "http://fcm.googleapis.com/send/x", "https://127.0.0.1/send", "https://internal.example/send", "https://fcm.googleapis.com.evil.test/send", "https://user@fcm.googleapis.com/send", "https://fcm.googleapis.com:8443/send", "https://fcm.googleapis.com/send#fragment",
	} {
		if PushEndpointAllowed(endpoint) {
			t.Errorf("accepted unsafe endpoint %q", endpoint)
		}
	}
	for _, endpoint := range []string{"https://jmt17.google.com/fcm/send/token", "https://fcm.googleapis.com/fcm/send/token", "https://updates.push.services.mozilla.com/wpush/v2/token", "https://web.push.apple.com/token", "https://wns2.notify.windows.com/token"} {
		if !PushEndpointAllowed(endpoint) {
			t.Errorf("rejected browser push endpoint %q", endpoint)
		}
	}
}
