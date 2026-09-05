package passportcities

import (
	"math"
	"strings"
	"unicode"
)

// AddressCandidates generates review candidates, never approved assignments.
// A named city/section alias must occur in the address. Coordinates only reject
// distant homonyms; proximity alone cannot generate a match. The 50 km check is
// a sanity bound, not an administrative boundary or a nearest-city classifier.
func AddressCandidates(cities []City, country, address string, lat, lon float64) []City {
	out := []City{}
	if address == "" || country == "" || !(lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180) || (lat == 0 && lon == 0) {
		return out
	}
	address = strings.ToLower(address)
	for _, c := range cities {
		if c.Country != country || distanceKM(lat, lon, c.Lat, c.Lon) > 50 {
			continue
		}
		for _, name := range append([]string{c.Name}, c.Aliases...) {
			name = strings.ToLower(strings.TrimSpace(name))
			if len([]rune(name)) < 2 {
				continue
			}
			if addressContains(address, name) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// Nearest returns the closest imported city in the requested country. The
// catalog is already curated to populated places, so this is the deterministic
// fallback used by the automatic backfill and request-time assignment. A
// country match is required; within that country an assignment is always made
// when at least one city exists because an unclassified venue is more harmful
// to Passport coverage than a less precise city label.
func Nearest(cities []City, country string, lat, lon float64) (City, bool) {
	if country == "" || !(lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180) || (lat == 0 && lon == 0) {
		return City{}, false
	}
	country = strings.ToUpper(strings.TrimSpace(country))
	var best City
	bestDistance := math.Inf(1)
	for _, city := range cities {
		if strings.ToUpper(strings.TrimSpace(city.Country)) != country || !(city.Lat >= -90 && city.Lat <= 90 && city.Lon >= -180 && city.Lon <= 180) || (city.Lat == 0 && city.Lon == 0) {
			continue
		}
		distance := distanceKM(lat, lon, city.Lat, city.Lon)
		if distance < bestDistance || (distance == bestDistance && city.SourceID < best.SourceID) {
			best, bestDistance = city, distance
		}
	}
	return best, best.SourceID != ""
}

func addressContains(address, name string) bool {
	for start := 0; start < len(address); {
		at := strings.Index(address[start:], name)
		if at < 0 {
			return false
		}
		at += start
		before := []rune(address[:at])
		after := []rune(address[at+len(name):])
		// Require a full locality name, not a street substring (e.g. 한양대학로).
		word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
		starts := len(before) == 0 || !word(before[len(before)-1])
		ends := len(after) == 0 || !word(after[0])
		for _, suffix := range []string{"특별자치시", "특별시", "광역시", "시", "군", "구", "都", "府", "県", "市", "区"} {
			if strings.HasPrefix(string(after), suffix) {
				ends = true
				break
			}
		}
		if starts && ends {
			return true
		}
		start = at + len(name)
	}
	return false
}

func distanceKM(lat1, lon1, lat2, lon2 float64) float64 {
	const rad = math.Pi / 180
	a := math.Pow(math.Sin((lat2-lat1)*rad/2), 2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Pow(math.Sin((lon2-lon1)*rad/2), 2)
	return 6371 * 2 * math.Asin(math.Sqrt(math.Min(1, a)))
}
