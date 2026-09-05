package passportcities

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

type City struct {
	SourceID string   `json:"source_id"`
	Name     string   `json:"name"`
	Country  string   `json:"country"`
	Admin1   string   `json:"admin1"`
	Aliases  []string `json:"aliases"`
	Lat      float64  `json:"lat"`
	Lon      float64  `json:"lon"`
}

// ReadGeoNames reads the official tab-separated allCountries extract. Districts,
// abandoned settlements and populated-place sections are not city candidates.
func ReadGeoNames(r io.Reader, accept func(City) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	for line := 1; scanner.Scan(); line++ {
		fields := strings.Split(scanner.Text(), "\t")
		if len(fields) != 19 {
			return fmt.Errorf("GeoNames line %d: expected 19 fields", line)
		}
		code := fields[7]
		if fields[6] != "P" || !(code == "PPL" || code == "PPLC" || code == "PPLA" || code == "PPLA2" || code == "PPLA3" || code == "PPLA4" || code == "PPLA5") {
			continue
		}
		id, e1 := strconv.ParseInt(fields[0], 10, 64)
		lat, e2 := strconv.ParseFloat(fields[4], 64)
		lon, e3 := strconv.ParseFloat(fields[5], 64)
		if e1 != nil || id < 1 || e2 != nil || e3 != nil || !(lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180) || len(fields[8]) != 2 || fields[10] == "" {
			return fmt.Errorf("invalid GeoNames city at line %d", line)
		}
		c := City{SourceID: fields[0], Name: fields[1], Country: fields[8], Admin1: fields[10], Aliases: append([]string{fields[2]}, strings.Split(fields[3], ",")...), Lat: lat, Lon: lon}
		if err := accept(c); err != nil {
			return err
		}
	}
	return scanner.Err()
}
func normalized(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(s))
}

// Match requires explicit country, first administrative subdivision and locality.
// Names are aliases, never identifiers; multiple matches deliberately stay unresolved.
func Match(cities []City, country, admin1, locality string) []City {
	out := []City{}
	if country == "" || admin1 == "" || strings.TrimSpace(locality) == "" {
		return out
	}
	for _, c := range cities {
		if c.Country != country || c.Admin1 != admin1 {
			continue
		}
		names := append([]string{c.Name}, c.Aliases...)
		for _, name := range names {
			if normalized(name) == normalized(locality) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}
