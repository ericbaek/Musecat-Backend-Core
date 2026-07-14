package sns

import (
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	arcadeinternal "github.com/ericbaek/musecat-backend-core/handlers/arcade/internal"
)

type snsAtomState struct {
	AtomID string
	Type   string
	Link   string
	Name   string
}

type snsDiffLogItem struct {
	AtomID     string                      `json:"atom_id,omitempty"`
	PrevID     string                      `json:"prev_id,omitempty"`
	SType      string                      `json:"sns_type"`
	Link       string                      `json:"link"`
	Name       string                      `json:"name,omitempty"`
	ChangeType string                      `json:"change_type"`
	Bullets    []arcadeinternal.I18nBullet `json:"bullets"`
	Diff       []map[string]any            `json:"diff,omitempty"`
}

func normalizeSNSInput(a SNSAtomInput) SNSAtomInput {
	a.Type = strings.TrimSpace(a.Type)
	a.Link = strings.TrimSpace(a.Link)
	a.Name = strings.TrimSpace(a.Name)
	return a
}

func loadCurrentSNSAtoms(app core.App, arcadeID, moleculeID string) ([]snsAtomState, error) {
	if strings.TrimSpace(moleculeID) == "" {
		return []snsAtomState{}, nil
	}

	rec, err := app.FindRecordById(arcadeinternal.CollectionArcadeSNS, moleculeID)
	if err != nil {
		return nil, fmt.Errorf("failed to load previous arcade_sns: %w", err)
	}
	if rec.GetString("arcade") != arcadeID {
		return nil, fmt.Errorf("previous arcade_sns does not belong to arcade")
	}

	atoms, err := app.FindRecordsByFilter(
		arcadeinternal.CollectionArcadeSNSAtoms,
		"molecule={:id}",
		"+created",
		0,
		0,
		map[string]any{"id": moleculeID},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load previous sns atoms: %w", err)
	}

	out := make([]snsAtomState, 0, len(atoms))
	for _, atom := range atoms {
		snsType := strings.TrimSpace(atom.GetString("type"))
		out = append(out, snsAtomState{
			AtomID: atom.Id,
			Type:   snsType,
			Link:   arcadeinternal.ResolveSNSLinkForOutput(snsType, atom.GetString("link"), atom.GetString("phone")),
			Name:   strings.TrimSpace(atom.GetString("name")),
		})
	}
	return out, nil
}

func buildSNSDiffLogItem(next snsAtomState, prev *snsAtomState) snsDiffLogItem {
	item := snsDiffLogItem{
		AtomID:  next.AtomID,
		SType:   next.Type,
		Link:    next.Link,
		Name:    next.Name,
		Bullets: []arcadeinternal.I18nBullet{},
	}

	if prev == nil {
		item.ChangeType = "added"
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.sns.added", map[string]any{
			"type": next.Type,
			"link": next.Link,
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "link", nil, next.Link)
		if next.Name != "" {
			item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "name", nil, next.Name)
		}
		return item
	}

	item.PrevID = prev.AtomID
	item.ChangeType = "updated"
	if prev.Link != next.Link {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.sns.link.changed", map[string]any{
			"from": prev.Link,
			"to":   next.Link,
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "link", prev.Link, next.Link)
	}
	if prev.Name != next.Name {
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.sns.name.changed", map[string]any{
			"from": arcadeinternal.DisplayDiffText(prev.Name),
			"to":   arcadeinternal.DisplayDiffText(next.Name),
		}))
		item.Diff = arcadeinternal.AppendDiffEntry(item.Diff, "name", prev.Name, next.Name)
	}
	if len(item.Diff) == 0 {
		item.ChangeType = "unchanged"
		item.Bullets = append(item.Bullets, arcadeinternal.BuildI18nBullet("arcade.changelog.sns.no_changes", nil))
		item.Diff = nil
	}
	return item
}

func buildDeletedSNSDiffLogItem(prev snsAtomState) snsDiffLogItem {
	return snsDiffLogItem{
		PrevID:     prev.AtomID,
		SType:      prev.Type,
		Link:       prev.Link,
		Name:       prev.Name,
		ChangeType: "deleted",
		Bullets: []arcadeinternal.I18nBullet{
			arcadeinternal.BuildI18nBullet("arcade.changelog.sns.deleted", map[string]any{
				"type": prev.Type,
				"link": prev.Link,
			}),
		},
		Diff: []map[string]any{
			{
				"field": "deleted",
				"from": map[string]any{
					"type": prev.Type,
					"link": prev.Link,
					"name": prev.Name,
				},
				"to": nil,
			},
		},
	}
}

// matchSNSAtoms prefers stable logical identity first and only falls back to a
// type-only match when there is a single unambiguous candidate left.
func matchSNSAtoms(prevAtoms, nextAtoms []snsAtomState) map[int]int {
	matches := map[int]int{}
	prevUsed := make([]bool, len(prevAtoms))
	nextUsed := make([]bool, len(nextAtoms))

	match := func(predicate func(prev snsAtomState, next snsAtomState) bool) {
		for nextIdx, nextAtom := range nextAtoms {
			if nextUsed[nextIdx] {
				continue
			}
			for prevIdx, prevAtom := range prevAtoms {
				if prevUsed[prevIdx] {
					continue
				}
				if !predicate(prevAtom, nextAtom) {
					continue
				}
				prevUsed[prevIdx] = true
				nextUsed[nextIdx] = true
				matches[nextIdx] = prevIdx
				break
			}
		}
	}

	match(func(prev snsAtomState, next snsAtomState) bool {
		return prev.Type == next.Type && prev.Link == next.Link
	})
	match(func(prev snsAtomState, next snsAtomState) bool {
		return prev.Type == next.Type && prev.Name == next.Name
	})

	type remainingPair struct {
		prevIdx int
		nextIdx int
	}
	typePairs := map[string]remainingPair{}
	typeCounts := map[string]struct {
		prev []int
		next []int
	}{}
	for prevIdx, prevAtom := range prevAtoms {
		if prevUsed[prevIdx] {
			continue
		}
		entry := typeCounts[prevAtom.Type]
		entry.prev = append(entry.prev, prevIdx)
		typeCounts[prevAtom.Type] = entry
	}
	for nextIdx, nextAtom := range nextAtoms {
		if nextUsed[nextIdx] {
			continue
		}
		entry := typeCounts[nextAtom.Type]
		entry.next = append(entry.next, nextIdx)
		typeCounts[nextAtom.Type] = entry
	}
	for atomType, entry := range typeCounts {
		if len(entry.prev) == 1 && len(entry.next) == 1 {
			typePairs[atomType] = remainingPair{prevIdx: entry.prev[0], nextIdx: entry.next[0]}
		}
	}
	for _, pair := range typePairs {
		prevUsed[pair.prevIdx] = true
		nextUsed[pair.nextIdx] = true
		matches[pair.nextIdx] = pair.prevIdx
	}

	return matches
}
