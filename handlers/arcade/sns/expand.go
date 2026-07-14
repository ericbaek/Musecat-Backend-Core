package sns

// ExpandedSNSItem is the frontend-friendly SNS atom shape used by GET /arcade
// and by SNS mutation responses after a successful update.
type ExpandedSNSItem struct {
	Type string `json:"type"`
	Link string `json:"link"`
	Name string `json:"name,omitempty"`
}

// ExpandedSNSValue keeps the SNS molecule id together with the rendered items.
type ExpandedSNSValue struct {
	ID    string            `json:"id"`
	Items []ExpandedSNSItem `json:"items"`
}

// BuildExpandedSNSValue normalizes the response shape for any SNS molecule.
func BuildExpandedSNSValue(id string, items []ExpandedSNSItem) ExpandedSNSValue {
	if items == nil {
		items = []ExpandedSNSItem{}
	}
	return ExpandedSNSValue{
		ID:    id,
		Items: items,
	}
}
