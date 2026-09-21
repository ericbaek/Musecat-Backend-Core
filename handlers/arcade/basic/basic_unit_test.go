package basic

import (
	"math"
	"testing"
)

func TestFloatsEqual(t *testing.T) {
	tests := []struct {
		name string
		a    float64
		b    float64
		want bool
	}{
		{name: "exact equal zero", a: 0.0, b: 0.0, want: true},
		{name: "exact equal positive", a: 123.456, b: 123.456, want: true},
		{name: "exact equal negative", a: -123.456, b: -123.456, want: true},
		{name: "within eps", a: 1.0, b: 1.0 + 1e-7, want: true},
		{name: "beyond eps small", a: 1.0, b: 1.0 + 1e-5, want: false},
		{name: "large magnitude relative tolerance within", a: 1000000.0, b: 1000000.5, want: true}, // diff 0.5 <= 1e-6 * 1e6 = 1.0
		{name: "large magnitude relative tolerance beyond", a: 1000000.0, b: 1000005.0, want: false},
		{name: "negative vs positive", a: 1.0, b: -1.0, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := floatsEqual(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("floatsEqual(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestEqualStringSlices(t *testing.T) {
	tests := []struct {
		name string
		a    []string
		b    []string
		want bool
	}{
		{name: "both nil", a: nil, b: nil, want: true},
		{name: "nil and empty", a: nil, b: []string{}, want: true},
		{name: "empty and nil", a: []string{}, b: nil, want: true},
		{name: "both empty", a: []string{}, b: []string{}, want: true},
		{name: "identical single item", a: []string{"subway 1"}, b: []string{"subway 1"}, want: true},
		{name: "identical with surrounding spaces trimmed", a: []string{"  subway 1  "}, b: []string{"subway 1"}, want: true},
		{name: "different lengths", a: []string{"a", "b"}, b: []string{"a"}, want: false},
		{name: "different items", a: []string{"a", "b"}, b: []string{"a", "c"}, want: false},
		{name: "different order", a: []string{"a", "b"}, b: []string{"b", "a"}, want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := equalStringSlices(tc.a, tc.b)
			if got != tc.want {
				t.Errorf("equalStringSlices(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestIsUsableBasicLocation(t *testing.T) {
	tests := []struct {
		name string
		lat  float64
		lon  float64
		want bool
	}{
		{name: "valid coordinates (Seoul)", lat: 37.5665, lon: 126.9780, want: true},
		{name: "valid coordinates (Tokyo)", lat: 35.6762, lon: 139.6503, want: true},
		{name: "boundary lat 90", lat: 90.0, lon: 100.0, want: true},
		{name: "boundary lat -90", lat: -90.0, lon: 100.0, want: true},
		{name: "boundary lon 180", lat: 50.0, lon: 180.0, want: true},
		{name: "boundary lon -180", lat: 50.0, lon: -180.0, want: true},
		{name: "zero origin excluded (0, 0)", lat: 0.0, lon: 0.0, want: false},
		{name: "lat zero excluded", lat: 0.0, lon: 126.0, want: false},
		{name: "lon zero excluded", lat: 37.0, lon: 0.0, want: false},
		{name: "out of range lat > 90", lat: 90.1, lon: 100.0, want: false},
		{name: "out of range lat < -90", lat: -90.1, lon: 100.0, want: false},
		{name: "out of range lon > 180", lat: 50.0, lon: 180.1, want: false},
		{name: "out of range lon < -180", lat: 50.0, lon: -180.1, want: false},
		{name: "NaN lat", lat: math.NaN(), lon: 100.0, want: false},
		{name: "NaN lon", lat: 50.0, lon: math.NaN(), want: false},
		{name: "Inf lat", lat: math.Inf(1), lon: 100.0, want: false},
		{name: "Inf lon", lat: 50.0, lon: math.Inf(-1), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isUsableBasicLocation(tc.lat, tc.lon)
			if got != tc.want {
				t.Errorf("isUsableBasicLocation(%v, %v) = %v, want %v", tc.lat, tc.lon, got, tc.want)
			}
		})
	}
}
