package game

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCustomPriceWireAndHistoricalCompatibility(t *testing.T) {
	for _, raw := range []string{
		`{"currency":"IDR","type":"custom","list":["7 Chips / 1 Credit","3.5 Chips on Wednesday"],"accept":[]}`,
		`{"currency":"IDR","type":"custom","list":[],"accept":[]}`,
		`{"currency":"IDR","type":"custom_v2","list":["7 Chips / 1 Credit"],"accept":[]}`,
		`{"currency":"JPY","type":"custom_v1","list":[{"title":"Normal","value":100}],"accept":[]}`,
		`{"currency":"IDR","type":"custom","list":[{"title":"Old tariff","value":28100}],"accept":[]}`,
	} {
		var price Price
		if err := json.Unmarshal([]byte(raw), &price); err != nil {
			t.Fatal(err)
		}
		if err := validatePrice(price); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(price)
		if err != nil {
			t.Fatal(err)
		}
		var before, after any
		json.Unmarshal([]byte(raw), &before)
		json.Unmarshal(encoded, &after)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("wire shape changed: %s", encoded)
		}
	}
}

func TestCustomPriceRejectsMixedItemsAndStructuredStrings(t *testing.T) {
	for _, raw := range []string{
		`{"currency":"IDR","type":"custom","list":["text",{"value":100}],"accept":[]}`,
		`{"currency":"IDR","type":"credit","list":["text"],"accept":[]}`,
		`{"currency":"IDR","type":"custom","list":[null],"accept":[]}`,
		`{"currency":"JPY","type":"custom_v1","list":["text"],"accept":[]}`,
		`{"currency":"JPY","type":"custom_v2","list":[{"value":100}],"accept":[]}`,
	} {
		var price Price
		if err := json.Unmarshal([]byte(raw), &price); err == nil && validatePrice(price) == nil {
			t.Fatalf("accepted invalid price: %s", raw)
		}
	}
}
