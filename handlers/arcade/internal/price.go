package arcadeinternal

import (
	"encoding/json"
)

// RedactGuestPrice filters out non-representative price items, removes accept
// payment methods, and marks hasHiddenPrices for unauthenticated/guest callers.
func RedactGuestPrice(raw any) any {
	if raw == nil {
		return nil
	}

	buf, err := json.Marshal(raw)
	if err != nil {
		return raw
	}

	var priceMap map[string]any
	if err := json.Unmarshal(buf, &priceMap); err != nil {
		return raw
	}
	if priceMap == nil {
		return nil
	}

	var rawList []any
	if listVal, ok := priceMap["list"].([]any); ok {
		rawList = listVal
	}

	representativeList := make([]map[string]any, 0)
	for _, item := range rawList {
		if itemMap, ok := item.(map[string]any); ok {
			isRepresent := false
			if rep, ok := itemMap["represent"].(bool); ok && rep {
				isRepresent = true
			}
			if isRepresent {
				repItem := map[string]any{
					"represent": true,
				}
				if title, exists := itemMap["title"]; exists {
					repItem["title"] = title
				}
				if value, exists := itemMap["value"]; exists {
					repItem["value"] = value
				}
				if modeKey, exists := itemMap["mode_key"]; exists {
					repItem["mode_key"] = modeKey
				}
				representativeList = append(representativeList, repItem)
			}
		}
	}

	// Legacy fallback: if no price item was explicitly marked represent == true,
	// treat the first price item as representative.
	if len(representativeList) == 0 && len(rawList) > 0 {
		if firstMap, ok := rawList[0].(map[string]any); ok {
			repItem := map[string]any{
				"represent": true,
			}
			if title, exists := firstMap["title"]; exists {
				repItem["title"] = title
			}
			if value, exists := firstMap["value"]; exists {
				repItem["value"] = value
			}
			if modeKey, exists := firstMap["mode_key"]; exists {
				repItem["mode_key"] = modeKey
			}
			representativeList = append(representativeList, repItem)
		}
	}

	out := map[string]any{
		"list":            representativeList,
		"hasHiddenPrices": len(rawList) > len(representativeList),
		"accept":          nil,
	}
	if cur, ok := priceMap["currency"]; ok {
		out["currency"] = cur
	}
	if tp, ok := priceMap["type"]; ok {
		out["type"] = tp
	}

	return out
}

// RedactGuestGame masks sensitive attributes from an expanded game object for unauthenticated callers.
func RedactGuestGame(game map[string]any) map[string]any {
	if game == nil {
		return nil
	}

	var itemsVal []map[string]any
	if list, ok := game["items"].([]map[string]any); ok {
		itemsVal = list
	} else if anyList, ok := game["items"].([]any); ok {
		itemsVal = make([]map[string]any, 0, len(anyList))
		for _, elem := range anyList {
			if m, ok := elem.(map[string]any); ok {
				itemsVal = append(itemsVal, m)
			}
		}
	}

	redactedItems := make([]map[string]any, 0, len(itemsVal))
	for _, item := range itemsVal {
		clone := make(map[string]any, len(item))
		for k, v := range item {
			clone[k] = v
		}
		clone["tag"] = nil
		clone["location"] = ""
		clone["updated"] = ""
		clone["updated_by"] = ""
		clone["price"] = RedactGuestPrice(item["price"])
		redactedItems = append(redactedItems, clone)
	}

	out := make(map[string]any, len(game))
	for k, v := range game {
		out[k] = v
	}
	out["items"] = redactedItems
	return out
}
