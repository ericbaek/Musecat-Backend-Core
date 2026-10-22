package game

import (
	"testing"
)

func TestNormalizePriceForStorage(t *testing.T) {
	title := "  Standard Play  "
	modeKey := "  card  "
	val := float32(1000)

	p := Price{
		Currency: "  KRW  ",
		Type:     "  credit  ",
		List: []PriceItem{
			{
				Title:   title,
				ModeKey: &modeKey,
				Value:   &val,
			},
		},
		Accept: []string{"  cash  ", "  ic_card  "},
	}

	normalized := NormalizePriceForStorage(p)

	if normalized.Currency != "KRW" {
		t.Errorf("expected currency KRW, got %q", normalized.Currency)
	}
	if normalized.Type != "credit" {
		t.Errorf("expected type credit, got %q", normalized.Type)
	}
	if len(normalized.List) != 1 {
		t.Fatalf("expected 1 list item, got %d", len(normalized.List))
	}
	if normalized.List[0].Title != "Standard Play" {
		t.Errorf("expected trimmed title, got %q", normalized.List[0].Title)
	}
	if *normalized.List[0].ModeKey != "card" {
		t.Errorf("expected trimmed modeKey, got %q", *normalized.List[0].ModeKey)
	}
	if len(normalized.Accept) != 2 || normalized.Accept[0] != "cash" || normalized.Accept[1] != "ic_card" {
		t.Errorf("expected trimmed accept list, got %v", normalized.Accept)
	}

	// Test nil Accept is initialized to empty slice
	pNilAccept := Price{
		Currency: "KRW",
		Accept:   nil,
	}
	normalizedNil := NormalizePriceForStorage(pNilAccept)
	if normalizedNil.Accept == nil || len(normalizedNil.Accept) != 0 {
		t.Errorf("expected non-nil empty accept slice, got %v", normalizedNil.Accept)
	}
}

func TestNormalizePriceForRead(t *testing.T) {
	p := Price{
		Currency: "  USD  ",
		Type:     "invalid_type",
		List:     nil,
		Accept:   nil,
	}

	normalized := NormalizePriceForRead(p)
	if normalized.Currency != "USD" {
		t.Errorf("expected currency USD, got %q", normalized.Currency)
	}
	if normalized.Type != string(PriceTypeCustom) {
		t.Errorf("expected invalid type to fallback to %q, got %q", PriceTypeCustom, normalized.Type)
	}
	if normalized.List == nil || len(normalized.List) != 0 {
		t.Errorf("expected non-nil empty List, got %v", normalized.List)
	}
	if normalized.Accept == nil || len(normalized.Accept) != 0 {
		t.Errorf("expected non-nil empty Accept, got %v", normalized.Accept)
	}
}

func TestValidatePriceType(t *testing.T) {
	validTypes := []string{
		string(PriceTypeGamemode),
		string(PriceTypeCredit),
		string(PriceTypeSong),
		string(PriceTypeTime),
		string(PriceTypeFree),
		string(PriceTypeCustom),
	}

	for _, vt := range validTypes {
		if err := ValidatePriceType(vt); err != nil {
			t.Errorf("ValidatePriceType(%q) unexpected error: %v", vt, err)
		}
	}

	invalidTypes := []string{"", "random", "unknown", "per_credit"}
	for _, it := range invalidTypes {
		if err := ValidatePriceType(it); err == nil {
			t.Errorf("ValidatePriceType(%q) expected error, got nil", it)
		}
	}
}

func TestPaseliPriceOptionalAndValidated(t *testing.T) {
	base := float32(100)
	p := Price{Currency: "JPY", Type: "gamemode", Accept: []string{"Cash", "PASELI"}, List: []PriceItem{{Title: "Standard", Value: &base}}}
	if err := validatePrice(p); err != nil {
		t.Fatal(err)
	}
	paseli := float32(95)
	p.List[0].PaseliValue = &paseli
	if err := validatePrice(p); err != nil {
		t.Fatal(err)
	}
	normalized := NormalizePriceForStorage(p)
	if normalized.List[0].PaseliValue == nil || *normalized.List[0].PaseliValue != 95 {
		t.Fatal("PASELI amount lost")
	}
	p.List[0].Value = nil
	if err := validatePrice(p); err != nil {
		t.Fatal("PASELI-only price rejected:", err)
	}
	for _, amount := range []float32{0, -1} {
		p.List[0].PaseliValue = &amount
		if validatePrice(p) == nil {
			t.Fatal("invalid PASELI amount accepted")
		}
	}
	p.List[0].PaseliValue = &paseli
	p.Accept = []string{"Cash"}
	if validatePrice(p) == nil {
		t.Fatal("PASELI price without acceptance")
	}
	p.Accept = []string{"PASELI"}
	p.Currency = "KRW"
	if validatePrice(p) == nil {
		t.Fatal("PASELI price with wrong currency")
	}
}

func TestPaseliNumericPriceTypes(t *testing.T) {
	amount := float32(95)
	for _, typ := range []string{"gamemode", "credit", "song", "time", "custom", "custom_v1", "free"} {
		t.Run(typ, func(t *testing.T) {
			p := Price{Currency: "JPY", Type: typ, Accept: []string{"PASELI"}, List: []PriceItem{{Title: "1", PaseliValue: &amount}}}
			err := validatePrice(p)
			if typ == "free" {
				if err == nil {
					t.Fatal("free prices cannot have PASELI amounts")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
