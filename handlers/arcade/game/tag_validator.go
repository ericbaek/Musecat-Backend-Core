package game

import (
	"fmt"
	"strings"
)

type TagCategory string

const (
	TagCategoryCabinetType     TagCategory = "케비넷 타입"
	TagCategoryRotationVersion TagCategory = "로테이션 버전"
	TagCategoryMonitor         TagCategory = "모니터"
	TagCategorySwitch          TagCategory = "스위치"
	TagCategorySound           TagCategory = "사운드"
	TagCategoryBroadcast       TagCategory = "녹화방송"
	TagCategoryEtc             TagCategory = "기타"
)

var tagCategoryEnum = map[TagCategory]struct{}{
	TagCategoryCabinetType:     {},
	TagCategoryRotationVersion: {},
	TagCategoryMonitor:         {},
	TagCategorySwitch:          {},
	TagCategorySound:           {},
	TagCategoryBroadcast:       {},
	TagCategoryEtc:             {},
}

type PriceAccept string

const (
	PriceAcceptCreditCard PriceAccept = "Credit Card"
	PriceAcceptPrepaid    PriceAccept = "Prepaid Card"
	PriceAcceptCash       PriceAccept = "Cash"
)

var priceAcceptEnum = map[PriceAccept]struct{}{
	PriceAcceptCreditCard: {},
	PriceAcceptPrepaid:    {},
	PriceAcceptCash:       {},
}

type PriceCurrency string

const (
	PriceCurrencyKRW PriceCurrency = "KRW"
	PriceCurrencyJPY PriceCurrency = "JPY"
	PriceCurrencyUSD PriceCurrency = "USD"
)

var priceCurrencyEnum = map[PriceCurrency]struct{}{
	PriceCurrencyKRW: {},
	PriceCurrencyJPY: {},
	PriceCurrencyUSD: {},
}

type PriceType string

const (
	PriceTypeGamemode PriceType = "gamemode"
	PriceTypeCredit   PriceType = "credit"
	PriceTypeSong     PriceType = "song"
	PriceTypeTime     PriceType = "time"
	PriceTypeFree     PriceType = "free"
	PriceTypeCustom   PriceType = "custom"
)

var priceTypeEnum = map[PriceType]struct{}{
	PriceTypeGamemode: {},
	PriceTypeCredit:   {},
	PriceTypeSong:     {},
	PriceTypeTime:     {},
	PriceTypeFree:     {},
	PriceTypeCustom:   {},
}

type TagItem struct {
	Category TagCategory `json:"category"`
	Note     string      `json:"note"`
}

func ValidatePriceAccept(accepts []string) error {
	for i := range accepts {
		value := PriceAccept(strings.TrimSpace(accepts[i]))
		if _, ok := priceAcceptEnum[value]; !ok {
			return fmt.Errorf("price.accept[%d] must be one of enum values", i)
		}
	}
	return nil
}

func ValidatePriceType(priceType string) error {
	value := PriceType(strings.TrimSpace(priceType))
	if _, ok := priceTypeEnum[value]; !ok {
		return fmt.Errorf("price.type must be one of gamemode, credit, song, time, free, custom")
	}
	return nil
}

func ValidateTagItems(tags []TagItem) error {
	for i := range tags {
		cat := TagCategory(strings.TrimSpace(string(tags[i].Category)))
		if _, ok := tagCategoryEnum[cat]; !ok {
			return fmt.Errorf("tag[%d].category must be one of enum values", i)
		}

		note := strings.TrimSpace(tags[i].Note)
		if note == "" {
			return fmt.Errorf("tag[%d].note is required", i)
		}
	}
	return nil
}
