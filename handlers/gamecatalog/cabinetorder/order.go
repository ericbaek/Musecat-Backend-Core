package cabinetorder

import "strings"

// Record keeps the oldest release date of a cabinet's active compatible versions.
func Record(releases map[string]string, cabinetID, releasedOn string) {
	releasedOn = strings.TrimSpace(releasedOn)
	if cabinetID == "" || releasedOn == "" {
		return
	}
	if oldest := releases[cabinetID]; oldest == "" || releasedOn < oldest {
		releases[cabinetID] = releasedOn
	}
}

// Less sorts cabinets by their oldest compatible release, newest first.
func Less(releases map[string]string, leftID, leftName, rightID, rightName string) bool {
	leftRelease := releases[leftID]
	rightRelease := releases[rightID]
	if leftRelease != rightRelease {
		if leftRelease == "" {
			return false
		}
		if rightRelease == "" {
			return true
		}
		return leftRelease > rightRelease
	}
	left := strings.ToLower(leftName)
	right := strings.ToLower(rightName)
	if left == right {
		return leftID < rightID
	}
	return left < right
}
