package gtk

// ExpandedGTKItem is the frontend-friendly GTK atom shape used by GET /arcade
// and by GTK mutation responses after a successful update.
type ExpandedGTKItem struct {
	Type string `json:"type"`
	Bool bool   `json:"bool"`
	Note string `json:"note,omitempty"`
	Meta any    `json:"meta,omitempty"`
}

// ExpandedGTKValue keeps the GTK molecule id together with the rendered items.
type ExpandedGTKValue struct {
	ID    string            `json:"id"`
	Items []ExpandedGTKItem `json:"items"`
}

// BuildExpandedGTKValue normalizes the response shape for any GTK molecule.
func BuildExpandedGTKValue(id string, items []ExpandedGTKItem) ExpandedGTKValue {
	if items == nil {
		items = []ExpandedGTKItem{}
	}
	return ExpandedGTKValue{
		ID:    id,
		Items: items,
	}
}
