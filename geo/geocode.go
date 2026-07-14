package geo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	ProviderGoogle = "google"
	ProviderKakao  = "kakao"
	ProviderOSM    = "osm"
)

var errNoResults = errors.New("no results")

// GeocodeCandidate is a normalized forward-geocoding result item.
type GeocodeCandidate struct {
	Address     string  `json:"address"`
	RoadAddress string  `json:"road_address,omitempty"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	PlaceID     string  `json:"place_id,omitempty"`
}

// GeocodeResponse is the normalized forward-geocoding response.
type GeocodeResponse struct {
	Provider string             `json:"provider"`
	Query    string             `json:"query"`
	Results  []GeocodeCandidate `json:"results"`
}

// ReverseGeocodeResponse is a normalized reverse-geocoding result.
type ReverseGeocodeResponse struct {
	Provider    string  `json:"provider"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
	Address     string  `json:"address"`
	RoadAddress string  `json:"road_address,omitempty"`
	DisplayName string  `json:"display_name"`
	PlaceID     string  `json:"place_id,omitempty"`
}

// ForwardGeocode resolves a query to coordinates using a provider selected by region.
// region=kr uses Kakao, all other regions use Google.
func ForwardGeocode(ctx context.Context, query, region, mode string) (GeocodeResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return GeocodeResponse{}, errors.New("query is required")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	providers := preferredForwardProviders(region, mode)
	provider := providers[0]
	results, err := geocodeWithProvider(cctx, provider, query)
	if errors.Is(err, errNoResults) {
		for _, fallbackProvider := range providers[1:] {
			results, err = geocodeWithProvider(cctx, fallbackProvider, query)
			if err == nil {
				provider = fallbackProvider
				break
			}
			if !errors.Is(err, errNoResults) {
				break
			}
		}
	}
	if err != nil {
		if errors.Is(err, errNoResults) {
			results = []GeocodeCandidate{}
		} else {
			return GeocodeResponse{}, err
		}
	}

	return GeocodeResponse{
		Provider: provider,
		Query:    query,
		Results:  results,
	}, nil
}

// ReverseGeocode resolves coordinates to a human-readable address.
// region=kr uses Kakao, all other regions use Google.
func ReverseGeocode(ctx context.Context, lat, lon float64, region, mode string) (ReverseGeocodeResponse, error) {
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return ReverseGeocodeResponse{}, errors.New("invalid coordinates")
	}

	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	providers := preferredReverseProviders(region, mode)
	res, err := reverseGeocodeWithProvider(cctx, providers[0], lat, lon)
	if errors.Is(err, errNoResults) {
		for _, fallbackProvider := range providers[1:] {
			res, err = reverseGeocodeWithProvider(cctx, fallbackProvider, lat, lon)
			if err == nil || !errors.Is(err, errNoResults) {
				break
			}
		}
	}
	if err != nil {
		return ReverseGeocodeResponse{}, err
	}
	return res, nil
}

func preferredForwardProviders(region, mode string) []string {
	if strings.EqualFold(strings.TrimSpace(mode), "free") {
		if strings.EqualFold(strings.TrimSpace(region), "kr") {
			return []string{ProviderKakao, ProviderOSM}
		}
		return []string{ProviderOSM}
	}
	if strings.EqualFold(strings.TrimSpace(region), "kr") {
		return []string{ProviderKakao, ProviderGoogle}
	}
	return []string{ProviderGoogle, ProviderKakao}
}

func preferredReverseProviders(region, mode string) []string {
	if strings.EqualFold(strings.TrimSpace(mode), "free") {
		if strings.EqualFold(strings.TrimSpace(region), "kr") {
			return []string{ProviderKakao, ProviderOSM}
		}
		return []string{ProviderOSM}
	}
	if strings.EqualFold(strings.TrimSpace(region), "kr") {
		return []string{ProviderKakao, ProviderGoogle}
	}
	return []string{ProviderGoogle, ProviderKakao}
}

func geocodeWithProvider(ctx context.Context, provider, query string) ([]GeocodeCandidate, error) {
	switch provider {
	case ProviderKakao:
		return geocodeWithKakao(ctx, query)
	case ProviderOSM:
		return geocodeWithOSM(ctx, query)
	default:
		return geocodeWithGoogle(ctx, query)
	}
}

func reverseGeocodeWithProvider(ctx context.Context, provider string, lat, lon float64) (ReverseGeocodeResponse, error) {
	switch provider {
	case ProviderKakao:
		return reverseGeocodeWithKakao(ctx, lat, lon)
	case ProviderOSM:
		return reverseGeocodeWithOSM(ctx, lat, lon)
	default:
		return reverseGeocodeWithGoogle(ctx, lat, lon)
	}
}

func geocodeWithGoogle(ctx context.Context, query string) ([]GeocodeCandidate, error) {
	key := strings.TrimSpace(os.Getenv("GOOGLE_MAPS_API_KEY"))
	if key == "" {
		return nil, errors.New("GOOGLE_MAPS_API_KEY is not configured")
	}

	u := url.URL{
		Scheme: "https",
		Host:   "maps.googleapis.com",
		Path:   "/maps/api/geocode/json",
	}
	q := u.Query()
	q.Set("address", query)
	q.Set("key", key)
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "myapp-geo/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google geocoding http %d", resp.StatusCode)
	}

	var data struct {
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
		Results      []struct {
			FormattedAddress string `json:"formatted_address"`
			PlaceID          string `json:"place_id"`
			Geometry         struct {
				Location struct {
					Lat float64 `json:"lat"`
					Lng float64 `json:"lng"`
				} `json:"location"`
			} `json:"geometry"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	switch data.Status {
	case "OK":
	case "ZERO_RESULTS":
		return nil, errNoResults
	default:
		if data.ErrorMessage != "" {
			return nil, fmt.Errorf("google geocoding status %s: %s", data.Status, data.ErrorMessage)
		}
		return nil, fmt.Errorf("google geocoding status %s", data.Status)
	}

	out := make([]GeocodeCandidate, 0, len(data.Results))
	for _, item := range data.Results {
		out = append(out, GeocodeCandidate{
			Address: item.FormattedAddress,
			Lat:     item.Geometry.Location.Lat,
			Lon:     item.Geometry.Location.Lng,
			PlaceID: item.PlaceID,
		})
	}
	return out, nil
}

func reverseGeocodeWithGoogle(ctx context.Context, lat, lon float64) (ReverseGeocodeResponse, error) {
	key := strings.TrimSpace(os.Getenv("GOOGLE_MAPS_API_KEY"))
	if key == "" {
		return ReverseGeocodeResponse{}, errors.New("GOOGLE_MAPS_API_KEY is not configured")
	}

	u := url.URL{
		Scheme: "https",
		Host:   "maps.googleapis.com",
		Path:   "/maps/api/geocode/json",
	}
	q := u.Query()
	q.Set("latlng", fmt.Sprintf("%f,%f", lat, lon))
	q.Set("key", key)
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "myapp-geo/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return ReverseGeocodeResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ReverseGeocodeResponse{}, fmt.Errorf("google reverse geocoding http %d", resp.StatusCode)
	}

	var data struct {
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
		Results      []struct {
			FormattedAddress string `json:"formatted_address"`
			PlaceID          string `json:"place_id"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return ReverseGeocodeResponse{}, err
	}

	switch data.Status {
	case "OK":
	case "ZERO_RESULTS":
		return ReverseGeocodeResponse{}, errNoResults
	default:
		if data.ErrorMessage != "" {
			return ReverseGeocodeResponse{}, fmt.Errorf("google reverse geocoding status %s: %s", data.Status, data.ErrorMessage)
		}
		return ReverseGeocodeResponse{}, fmt.Errorf("google reverse geocoding status %s", data.Status)
	}

	if len(data.Results) == 0 {
		return ReverseGeocodeResponse{}, errNoResults
	}

	displayName := strings.TrimSpace(data.Results[0].FormattedAddress)
	return ReverseGeocodeResponse{
		Provider:    ProviderGoogle,
		Lat:         lat,
		Lon:         lon,
		Address:     displayName,
		RoadAddress: displayName,
		DisplayName: displayName,
		PlaceID:     data.Results[0].PlaceID,
	}, nil
}

func geocodeWithKakao(ctx context.Context, query string) ([]GeocodeCandidate, error) {
	key := strings.TrimSpace(os.Getenv("KAKAO_REST_API_KEY"))
	if key == "" {
		return nil, errors.New("KAKAO_REST_API_KEY is not configured")
	}

	u := url.URL{
		Scheme: "https",
		Host:   "dapi.kakao.com",
		Path:   "/v2/local/search/address.json",
	}
	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("Authorization", "KakaoAK "+key)
	req.Header.Set("User-Agent", "myapp-geo/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("kakao geocoding http %d", resp.StatusCode)
	}

	var data struct {
		Documents []struct {
			AddressName string `json:"address_name"`
			X           string `json:"x"`
			Y           string `json:"y"`
			Address     *struct {
				AddressName string `json:"address_name"`
			} `json:"address"`
			RoadAddress *struct {
				AddressName string `json:"address_name"`
			} `json:"road_address"`
		} `json:"documents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	out := make([]GeocodeCandidate, 0, len(data.Documents))
	if len(data.Documents) == 0 {
		return nil, errNoResults
	}
	for _, item := range data.Documents {
		lon, err := strconv.ParseFloat(item.X, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid kakao longitude %q: %w", item.X, err)
		}
		lat, err := strconv.ParseFloat(item.Y, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid kakao latitude %q: %w", item.Y, err)
		}

		address := item.AddressName
		if item.Address != nil && strings.TrimSpace(item.Address.AddressName) != "" {
			address = item.Address.AddressName
		}

		candidate := GeocodeCandidate{
			Address: address,
			Lat:     lat,
			Lon:     lon,
		}
		if item.RoadAddress != nil {
			candidate.RoadAddress = strings.TrimSpace(item.RoadAddress.AddressName)
		}
		out = append(out, candidate)
	}
	return out, nil
}

func geocodeWithOSM(ctx context.Context, query string) ([]GeocodeCandidate, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "nominatim.openstreetmap.org",
		Path:   "/search",
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "jsonv2")
	q.Set("limit", "5")
	q.Set("addressdetails", "1")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "myapp-geo/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osm geocoding http %d", resp.StatusCode)
	}

	var data []struct {
		PlaceID     int64  `json:"place_id"`
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
		Name        string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, errNoResults
	}

	out := make([]GeocodeCandidate, 0, len(data))
	for _, item := range data {
		lat, err := strconv.ParseFloat(item.Lat, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid osm latitude %q: %w", item.Lat, err)
		}
		lon, err := strconv.ParseFloat(item.Lon, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid osm longitude %q: %w", item.Lon, err)
		}

		candidate := GeocodeCandidate{
			Address: item.DisplayName,
			Lat:     lat,
			Lon:     lon,
		}
		if item.PlaceID != 0 {
			candidate.PlaceID = strconv.FormatInt(item.PlaceID, 10)
		}
		if strings.TrimSpace(item.Name) != "" {
			candidate.RoadAddress = strings.TrimSpace(item.Name)
		}
		out = append(out, candidate)
	}
	return out, nil
}

func reverseGeocodeWithKakao(ctx context.Context, lat, lon float64) (ReverseGeocodeResponse, error) {
	key := strings.TrimSpace(os.Getenv("KAKAO_REST_API_KEY"))
	if key == "" {
		return ReverseGeocodeResponse{}, errors.New("KAKAO_REST_API_KEY is not configured")
	}

	u := url.URL{
		Scheme: "https",
		Host:   "dapi.kakao.com",
		Path:   "/v2/local/geo/coord2address.json",
	}
	q := u.Query()
	q.Set("x", fmt.Sprintf("%f", lon))
	q.Set("y", fmt.Sprintf("%f", lat))
	q.Set("input_coord", "WGS84")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("Authorization", "KakaoAK "+key)
	req.Header.Set("User-Agent", "myapp-geo/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return ReverseGeocodeResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ReverseGeocodeResponse{}, fmt.Errorf("kakao reverse geocoding http %d", resp.StatusCode)
	}

	var data struct {
		Documents []struct {
			Address *struct {
				AddressName string `json:"address_name"`
			} `json:"address"`
			RoadAddress *struct {
				AddressName string `json:"address_name"`
			} `json:"road_address"`
		} `json:"documents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return ReverseGeocodeResponse{}, err
	}
	if len(data.Documents) == 0 {
		return ReverseGeocodeResponse{}, errNoResults
	}

	address := ""
	if data.Documents[0].Address != nil {
		address = strings.TrimSpace(data.Documents[0].Address.AddressName)
	}
	roadAddress := ""
	if data.Documents[0].RoadAddress != nil {
		roadAddress = strings.TrimSpace(data.Documents[0].RoadAddress.AddressName)
	}
	displayName := roadAddress
	if displayName == "" {
		displayName = address
	}
	if displayName == "" {
		return ReverseGeocodeResponse{}, errNoResults
	}

	return ReverseGeocodeResponse{
		Provider:    ProviderKakao,
		Lat:         lat,
		Lon:         lon,
		Address:     address,
		RoadAddress: roadAddress,
		DisplayName: displayName,
	}, nil
}

func reverseGeocodeWithOSM(ctx context.Context, lat, lon float64) (ReverseGeocodeResponse, error) {
	u := url.URL{
		Scheme: "https",
		Host:   "nominatim.openstreetmap.org",
		Path:   "/reverse",
	}
	q := u.Query()
	q.Set("lat", fmt.Sprintf("%f", lat))
	q.Set("lon", fmt.Sprintf("%f", lon))
	q.Set("format", "jsonv2")
	q.Set("addressdetails", "1")
	u.RawQuery = q.Encode()

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	req.Header.Set("User-Agent", "myapp-geo/1.0")
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return ReverseGeocodeResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ReverseGeocodeResponse{}, fmt.Errorf("osm reverse geocoding http %d", resp.StatusCode)
	}

	var data struct {
		PlaceID     int64  `json:"place_id"`
		Lat         string `json:"lat"`
		Lon         string `json:"lon"`
		DisplayName string `json:"display_name"`
		Name        string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return ReverseGeocodeResponse{}, err
	}
	if strings.TrimSpace(data.DisplayName) == "" {
		return ReverseGeocodeResponse{}, errNoResults
	}

	res := ReverseGeocodeResponse{
		Provider:    ProviderOSM,
		Lat:         lat,
		Lon:         lon,
		Address:     strings.TrimSpace(data.DisplayName),
		DisplayName: strings.TrimSpace(data.DisplayName),
	}
	if strings.TrimSpace(data.Name) != "" {
		res.RoadAddress = strings.TrimSpace(data.Name)
	}
	if data.PlaceID != 0 {
		res.PlaceID = strconv.FormatInt(data.PlaceID, 10)
	}
	return res, nil
}
