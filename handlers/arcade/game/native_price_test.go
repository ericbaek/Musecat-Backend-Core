package game

import (
	"encoding/json"
	"testing"
)

func TestNativeGamePriceSupportsCurrentPaseliContract(t *testing.T) {
	price, err := parseNativeGamePrice(json.RawMessage(`{"currency":"JPY","type":"credit","list":[{"title":"1","value":100,"paseli_value":95}],"accept":["Cash","PASELI"]}`))
	if err != nil || len(price.List) != 1 || price.List[0].PaseliValue == nil || *price.List[0].PaseliValue != 95 {
		t.Fatalf("native decoder lost current web PASELI price: %+v %v", price, err)
	}
	for _, raw := range []string{
		`{"currency":"KRW","type":"credit","list":[{"title":"1","value":100,"paseli_value":95}],"accept":["PASELI"]}`,
		`{"currency":"JPY","type":"credit","list":[{"title":"1","value":100,"paseli_value":95}],"accept":["Cash"]}`,
		`{"currency":"JPY","type":"credit","list":[{"title":"1","value":100,"paseli_value":0}],"accept":["PASELI"]}`,
	} {
		if _, err := parseNativeGamePrice(json.RawMessage(raw)); err == nil {
			t.Fatalf("native decoder bypassed web PASELI policy: %s", raw)
		}
	}
}
