package arcadeinternal_test

import (
	"testing"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

func TestRedactGuestPrice(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		if got := arcadeinternal.RedactGuestPrice(nil); got != nil {
			t.Fatalf("expected nil, got %#v", got)
		}
	})

	t.Run("filters non-representative prices and strips accept", func(t *testing.T) {
		raw := map[string]any{
			"currency": "KRW",
			"type":     "credit",
			"accept":   []string{"Cash", "Credit Card"},
			"list": []map[string]any{
				{
					"title":     "Standard Play",
					"value":     1000,
					"mode_key":  "standard",
					"represent": true,
				},
				{
					"title":     "Premium Play",
					"value":     1500,
					"mode_key":  "premium",
					"represent": false,
				},
			},
		}

		redacted, ok := arcadeinternal.RedactGuestPrice(raw).(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any, got %T", redacted)
		}

		if redacted["currency"] != "KRW" {
			t.Errorf("expected currency KRW, got %v", redacted["currency"])
		}
		if redacted["type"] != "credit" {
			t.Errorf("expected type credit, got %v", redacted["type"])
		}
		if redacted["accept"] != nil {
			t.Errorf("expected accept to be nil, got %v", redacted["accept"])
		}
		if redacted["hasHiddenPrices"] != true {
			t.Errorf("expected hasHiddenPrices to be true, got %v", redacted["hasHiddenPrices"])
		}

		list, ok := redacted["list"].([]map[string]any)
		if !ok || len(list) != 1 {
			t.Fatalf("expected 1 item in list, got %d", len(list))
		}
		if list[0]["title"] != "Standard Play" || list[0]["value"] != float64(1000) || list[0]["represent"] != true {
			t.Errorf("unexpected list item: %#v", list[0])
		}
	})

	t.Run("all representative prices hasHiddenPrices is false", func(t *testing.T) {
		raw := map[string]any{
			"currency": "JPY",
			"type":     "song",
			"list": []map[string]any{
				{
					"title":     "1 Tune",
					"value":     100,
					"represent": true,
				},
			},
		}

		redacted, ok := arcadeinternal.RedactGuestPrice(raw).(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any, got %T", redacted)
		}
		if redacted["hasHiddenPrices"] != false {
			t.Errorf("expected hasHiddenPrices to be false, got %v", redacted["hasHiddenPrices"])
		}
	})

	t.Run("legacy prices without represent flag fallback to first item", func(t *testing.T) {
		raw := map[string]any{
			"list": []map[string]any{
				{"title": "First", "value": 500},
				{"title": "Second", "value": 1000},
			},
		}

		redacted, ok := arcadeinternal.RedactGuestPrice(raw).(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any, got %T", redacted)
		}
		if _, exists := redacted["currency"]; exists {
			t.Errorf("expected missing currency to be omitted")
		}
		if _, exists := redacted["type"]; exists {
			t.Errorf("expected missing type to be omitted")
		}
		if redacted["hasHiddenPrices"] != true {
			t.Errorf("expected hasHiddenPrices to be true")
		}
		list, ok := redacted["list"].([]map[string]any)
		if !ok || len(list) != 1 {
			t.Fatalf("expected 1 fallback item, got %d", len(list))
		}
		if list[0]["title"] != "First" || list[0]["value"] != float64(500) || list[0]["represent"] != true {
			t.Errorf("unexpected fallback item: %#v", list[0])
		}
	})
}
