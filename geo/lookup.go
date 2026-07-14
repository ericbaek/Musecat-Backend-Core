package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Result represents the lookup result for a coordinate.
type Result struct {
	Country  string `json:"country"`
	Timezone string `json:"timezone"`
}

var (
	httpClientMu sync.Mutex
	httpClient   = &http.Client{Timeout: 6 * time.Second}
)

// SetHTTPClient overrides the package HTTP client and returns a restore function.
func SetHTTPClient(client *http.Client) func() {
	httpClientMu.Lock()
	prev := httpClient
	if client == nil {
		client = &http.Client{Timeout: 6 * time.Second}
	}
	httpClient = client
	httpClientMu.Unlock()

	return func() {
		httpClientMu.Lock()
		httpClient = prev
		httpClientMu.Unlock()
	}
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

	iso = overrideCountryForSpecialTimezone(iso, tz)

	return Result{Country: iso, Timezone: tz}, nil
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

	resp, err := httpClient.Do(req)
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

	resp, err := httpClient.Do(req)
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

	resp, err := httpClient.Do(req)
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

	resp, err := httpClient.Do(req)
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
