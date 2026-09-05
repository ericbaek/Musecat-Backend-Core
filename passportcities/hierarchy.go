package passportcities

import (
	"bufio"
	"fmt"
	"io"
	"sort"
	"strings"
)

// ReadCatalog attaches populated-place sections to their explicitly recorded
// GeoNames parent city. Sections never become independent city stamps. Missing
// or ambiguous parentage stays unresolved; coordinates do not choose a parent.
// Both inputs are official uncompressed GeoNames extracts.
func ReadCatalog(places, hierarchy io.Reader, accept func(City) error) error {
	if hierarchy == nil {
		return ReadGeoNames(places, accept)
	}
	cities := map[string]City{}
	order := []string{}
	if err := readGeoNames(places, true, func(c City) error {
		cities[c.SourceID] = c
		order = append(order, c.SourceID)
		return nil
	}); err != nil {
		return err
	}
	parents := map[string][]string{}
	scanner := bufio.NewScanner(hierarchy)
	for line := 1; scanner.Scan(); line++ {
		f := strings.Split(scanner.Text(), "\t")
		if len(f) < 2 || f[0] == "" || f[1] == "" {
			return fmt.Errorf("invalid GeoNames hierarchy at line %d", line)
		}
		child, ok := cities[f[1]]
		parent, parentOK := cities[f[0]]
		if ok && parentOK && child.FeatureCode == "PPLX" && child.Country == parent.Country && child.Admin1 == parent.Admin1 {
			parents[f[1]] = append(parents[f[1]], f[0])
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	var roots func(string, map[string]bool, map[string]bool)
	roots = func(id string, seen, found map[string]bool) {
		if seen[id] {
			return
		}
		seen[id] = true
		if cities[id].FeatureCode != "PPLX" {
			found[id] = true
			return
		}
		for _, parent := range parents[id] {
			roots(parent, seen, found)
		}
	}
	for _, id := range order {
		child := cities[id]
		if child.FeatureCode != "PPLX" {
			continue
		}
		found := map[string]bool{}
		roots(id, map[string]bool{}, found)
		if len(found) != 1 {
			continue
		}
		for parentID := range found {
			parent := cities[parentID]
			parent.Aliases = append(parent.Aliases, child.Name)
			parent.Aliases = append(parent.Aliases, child.Aliases...)
			cities[parentID] = parent
		}
	}
	for _, id := range order {
		c := cities[id]
		if c.FeatureCode == "PPLX" {
			continue
		}
		unique := map[string]bool{}
		for _, alias := range c.Aliases {
			if strings.TrimSpace(alias) != "" {
				unique[alias] = true
			}
		}
		c.Aliases = make([]string, 0, len(unique))
		for alias := range unique {
			c.Aliases = append(c.Aliases, alias)
		}
		sort.Strings(c.Aliases)
		if err := accept(c); err != nil {
			return err
		}
	}
	return nil
}
