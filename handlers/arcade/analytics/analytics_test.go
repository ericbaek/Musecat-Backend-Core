package analytics

import (
	"fmt"
	"testing"
	"time"
)

func TestAnonymousAnalyticsEventRollingLimit(t *testing.T) {
	now := time.Now()
	clientIP := fmt.Sprintf("test-client-%d", now.UnixNano())

	for i := 0; i < maxAnalyticsEventsWindow; i++ {
		if _, allowed := allowAnonymousAnalyticsEvent(nil, clientIP, fmt.Sprintf("arcade-%d", i), now); !allowed {
			t.Fatalf("event %d unexpectedly rate limited", i+1)
		}
	}

	retryAfter, allowed := allowAnonymousAnalyticsEvent(nil, clientIP, "another-arcade", now)
	if allowed {
		t.Fatal("event beyond rolling limit was allowed")
	}
	if retryAfter <= 0 {
		t.Fatalf("retryAfter=%s, want positive duration", retryAfter)
	}
}
