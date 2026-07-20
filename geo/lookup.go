package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// Result represents the lookup result for a coordinate.
type Result struct {
	Country  string `json:"country"`
	Timezone string `json:"timezone"`
}

var (
	httpClientMu sync.RWMutex
	httpClient   = &http.Client{Timeout: 6 * time.Second}

	lookupCache = struct {
		sync.Mutex
		entries map[string]lookupCacheEntry
	}{entries: map[string]lookupCacheEntry{}}
	lookupGroup singleflight.Group
)

const (
	lookupCacheTTL     = 24 * time.Hour
	lookupCacheMaxSize = 4096
)

type lookupCacheEntry struct {
	result    Result
	expiresAt time.Time
}

// SetHTTPClient overrides the package HTTP client and returns a restore function.
func SetHTTPClient(client *http.Client) func() {
	httpClientMu.Lock()
	prev := httpClient
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	httpClient = client
	httpClientMu.Unlock()
	clearLookupCache()

	return func() {
		httpClientMu.Lock()
		httpClient = prev
		httpClientMu.Unlock()
		clearLookupCache()
	}
}

func currentHTTPClient() *http.Client {
	httpClientMu.RLock()
	client := httpClient
	httpClientMu.RUnlock()
	return client
}

func clearLookupCache() {
	lookupCache.Lock()
	lookupCache.entries = map[string]lookupCacheEntry{}
	lookupCache.Unlock()
}

// LookupCountryAndTimezone returns the ISO 3166-1 alpha-2 country code and IANA timezone name for the given coordinates.
//
// It uses public HTTP APIs without requiring API keys:
// - Country ISO: BigDataCloud reverse-geocode-client (fallback: OpenStreetMap Nominatim)
// - Timezone: timeapi.io by coordinates (fallback: open-meteo forecast)
func LookupCountryAndTimezone(ctx context.Context, lat, lon float64) (Result, error) {
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return Result{}, errors.New("invalid coordinates")
	}
	key := lookupCacheKey(lat, lon)
	if result, ok := loadLookupCache(key); ok {
		return result, nil
	}

	result, err, _ := lookupGroup.Do(key, func() (any, error) {
		if cached, ok := loadLookupCache(key); ok {
			return cached, nil
		}

		result, err := lookupCountryAndTimezoneUncached(ctx, lat, lon)
		if err != nil {
			return Result{}, err
		}
		storeLookupCache(key, result)
		return result, nil
	})
	if err != nil {
		return Result{}, err
	}
	return result.(Result), nil
}

func lookupCountryAndTimezoneUncached(ctx context.Context, lat, lon float64) (Result, error) {

	// Create a child context with timeout to bound the overall operation.
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	iso, err := lookupCountryISO(cctx, lat, lon)
	if err != nil {
		return Result{}, fmt.Errorf("country lookup failed: %w", err)
	}

	tz, err := lookupTimezone(cctx, lat, lon)
	if err != nil {
		return Result{}, fmt.Errorf("timezone lookup failed: %w", err)
	}

	if _, err := time.LoadLocation(tz); err != nil {
		return Result{}, fmt.Errorf("invalid timezone result: %w", err)
	}

	iso, tz = normalizeCountryAndTimezone(iso, tz)
	if len(iso) != 2 {
		return Result{}, errors.New("invalid country result")
	}

	return Result{Country: iso, Timezone: tz}, nil
}

func lookupCacheKey(lat, lon float64) string {
	// Four decimal places is about 11m latitude precision. It keeps cache growth
	// bounded while not sharing values across materially different facilities.
	lat = math.Round(lat*1e4) / 1e4
	lon = math.Round(lon*1e4) / 1e4
	return strconv.FormatFloat(lat, 'f', 4, 64) + "," + strconv.FormatFloat(lon, 'f', 4, 64)
}

func loadLookupCache(key string) (Result, bool) {
	now := time.Now().UTC()
	lookupCache.Lock()
	entry, ok := lookupCache.entries[key]
	if ok && now.Before(entry.expiresAt) {
		lookupCache.Unlock()
		return entry.result, true
	}
	if ok {
		delete(lookupCache.entries, key)
	}
	lookupCache.Unlock()
	return Result{}, false
}

func storeLookupCache(key string, result Result) {
	now := time.Now().UTC()
	lookupCache.Lock()
	for cacheKey, entry := range lookupCache.entries {
		if !now.Before(entry.expiresAt) {
			delete(lookupCache.entries, cacheKey)
		}
	}
	if len(lookupCache.entries) >= lookupCacheMaxSize {
		var oldestKey string
		var oldestExpiry time.Time
		for cacheKey, entry := range lookupCache.entries {
			if oldestKey == "" || entry.expiresAt.Before(oldestExpiry) {
				oldestKey = cacheKey
				oldestExpiry = entry.expiresAt
			}
		}
		if oldestKey != "" {
			delete(lookupCache.entries, oldestKey)
		}
	}
	lookupCache.entries[key] = lookupCacheEntry{result: result, expiresAt: now.Add(lookupCacheTTL)}
	lookupCache.Unlock()
}

// LookupTimezone returns the IANA timezone name for the given coordinates.
func LookupTimezone(ctx context.Context, lat, lon float64) (string, error) {
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return "", errors.New("invalid coordinates")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	tz, err := lookupTimezone(cctx, lat, lon)
	if err != nil {
		return "", fmt.Errorf("timezone lookup failed: %w", err)
	}

	return tz, nil
}

// lookupCountryISO queries BigDataCloud reverse-geocode-client for the countryCode.
func lookupCountryISO(ctx context.Context, lat, lon float64) (string, error) {
	iso, err := lookupCountryISOFromBigDataCloud(ctx, lat, lon)
	if err == nil {
		return iso, nil
	}

	fallbackISO, fallbackErr := lookupCountryISOFromNominatim(ctx, lat, lon)
	if fallbackErr == nil {
		return fallbackISO, nil
	}

	return "", fmt.Errorf("bigdatacloud failed (%v); nominatim failed (%v)", err, fallbackErr)
}

func lookupCountryISOFromBigDataCloud(ctx context.Context, lat, lon float64) (string, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "api.bigdatacloud.net",
		Path:   "/data/reverse-geocode-client",
	}
	q := u.Query()
	q.Set("latitude", fmt.Sprintf("%f", lat))
	q.Set("longitude", fmt.Sprintf("%f", lon))
	q.Set("localityLanguage", "en")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "myapp-geo/1.0")

	resp, err := currentHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("reverse-geocode-client http %d", resp.StatusCode)
	}

	var data struct {
		CountryCode string `json:"countryCode"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	iso := strings.ToUpper(strings.TrimSpace(data.CountryCode))
	if iso == "" {
		return "", errors.New("countryCode not found")
	}
	return iso, nil
}

func lookupCountryISOFromNominatim(ctx context.Context, lat, lon float64) (string, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "nominatim.openstreetmap.org",
		Path:   "/reverse",
	}
	q := u.Query()
	q.Set("lat", fmt.Sprintf("%f", lat))
	q.Set("lon", fmt.Sprintf("%f", lon))
	q.Set("format", "jsonv2")
	q.Set("zoom", "3")
	q.Set("addressdetails", "1")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "myapp-geo/1.0")

	resp, err := currentHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("nominatim http %d", resp.StatusCode)
	}

	var data struct {
		Address struct {
			CountryCode string `json:"country_code"`
		} `json:"address"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	iso := strings.ToUpper(strings.TrimSpace(data.Address.CountryCode))
	if iso == "" {
		if strings.TrimSpace(data.Error) != "" {
			return "", fmt.Errorf("country not found: %s", data.Error)
		}
		return "", errors.New("country not found")
	}
	return iso, nil
}

// lookupTimezone queries timeapi.io for IANA timezone by coordinates.
func lookupTimezone(ctx context.Context, lat, lon float64) (string, error) {
	tz, err := lookupTimezoneFromTimeAPI(ctx, lat, lon)
	if err == nil {
		return tz, nil
	}

	fallbackTZ, fallbackErr := lookupTimezoneFromOpenMeteo(ctx, lat, lon)
	if fallbackErr == nil {
		return fallbackTZ, nil
	}

	return "", fmt.Errorf("timeapi.io failed (%v); open-meteo failed (%v)", err, fallbackErr)
}

// normalizeCountryAndTimezone keeps timezone storage stable for countries with
// a single canonical IANA timezone. Coordinate providers sometimes return a
// neighboring timezone that currently has the same UTC offset.
func normalizeCountryAndTimezone(country, timezone string) (string, string) {
	country = overrideCountryForSpecialTimezone(country, timezone)
	if canonical, ok := canonicalTimezoneByCountry[country]; ok {
		return country, canonical
	}
	return country, canonicalTimezoneAlias(timezone)
}

var canonicalTimezoneByCountry = map[string]string{
	"BN": "Asia/Brunei",
	"HK": "Asia/Hong_Kong",
	"JP": "Asia/Tokyo",
	"KH": "Asia/Phnom_Penh",
	"KR": "Asia/Seoul",
	"LA": "Asia/Vientiane",
	"MO": "Asia/Macau",
	"MY": "Asia/Kuala_Lumpur",
	"PH": "Asia/Manila",
	"SG": "Asia/Singapore",
	"TH": "Asia/Bangkok",
	"TW": "Asia/Taipei",
	"VN": "Asia/Ho_Chi_Minh",
}

func canonicalTimezoneAlias(timezone string) string {
	switch strings.TrimSpace(timezone) {
	case "Asia/Macao":
		return "Asia/Macau"
	case "Asia/Calcutta":
		return "Asia/Kolkata"
	default:
		return strings.TrimSpace(timezone)
	}
}

func overrideCountryForSpecialTimezone(country, timezone string) string {
	switch {
	case strings.EqualFold(strings.TrimSpace(timezone), "Asia/Hong_Kong"):
		return "HK"
	case strings.EqualFold(strings.TrimSpace(timezone), "Asia/Macau"),
		strings.EqualFold(strings.TrimSpace(timezone), "Asia/Macao"):
		return "MO"
	default:
		return strings.ToUpper(strings.TrimSpace(country))
	}
}

func lookupTimezoneFromTimeAPI(ctx context.Context, lat, lon float64) (string, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "timeapi.io",
		Path:   "/api/Time/current/coordinate",
	}
	q := u.Query()
	q.Set("latitude", fmt.Sprintf("%f", lat))
	q.Set("longitude", fmt.Sprintf("%f", lon))
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "myapp-geo/1.0")

	resp, err := currentHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("timeapi.io http %d", resp.StatusCode)
	}

	// Known schema: includes timeZone field (e.g., "America/Los_Angeles").
	var data struct {
		TimeZone string `json:"timeZone"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if data.TimeZone == "" {
		return "", errors.New("timezone not found")
	}
	return data.TimeZone, nil
}

func lookupTimezoneFromOpenMeteo(ctx context.Context, lat, lon float64) (string, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "api.open-meteo.com",
		Path:   "/v1/forecast",
	}
	q := u.Query()
	q.Set("latitude", fmt.Sprintf("%f", lat))
	q.Set("longitude", fmt.Sprintf("%f", lon))
	q.Set("current", "temperature_2m")
	q.Set("timezone", "auto")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "myapp-geo/1.0")

	resp, err := currentHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("open-meteo http %d", resp.StatusCode)
	}

	var data struct {
		Timezone string `json:"timezone"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if strings.TrimSpace(data.Timezone) == "" {
		return "", errors.New("timezone not found")
	}
	return data.Timezone, nil
}
