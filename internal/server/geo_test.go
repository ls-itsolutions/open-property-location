package server

import (
	"math"
	"testing"

	"github.com/ls-itsolutions/open-property-location/internal/outline"
)

func TestHaversine(t *testing.T) {
	// One degree of latitude along a meridian.
	if d := haversine(53, 8, 54, 8); math.Abs(d-111195) > 1 {
		t.Errorf("1° latitude = %.0f m", d)
	}
	// Berlin Alexanderplatz to Hamburg Hauptbahnhof, about 255 km.
	if d := haversine(52.5219, 13.4132, 53.5528, 10.0067); math.Abs(d-255_300) > 1000 {
		t.Errorf("Berlin–Hamburg = %.0f m", d)
	}
	if d := haversine(53, 8, 53, 8); d != 0 {
		t.Errorf("same point = %v", d)
	}
}

func TestWalkingMinutes(t *testing.T) {
	tests := map[float64]int{0: 0, 10: 1, 61.5: 1, 62: 2, 1000: 17}
	for d, want := range tests {
		if got := walkingMinutes(d); got != want {
			t.Errorf("walkingMinutes(%v) = %d, want %d", d, got, want)
		}
	}
}

func TestRankScore(t *testing.T) {
	near := rankScore(100, 0)
	fartherButImportant := rankScore(150, 0.7)
	if fartherButImportant >= near {
		t.Errorf("important place at 150 m should rank before plain place at 100 m")
	}
	if rankScore(100, 0) != 100 {
		t.Error("without importance the score is the distance")
	}
}

func TestBoundingBoxContainsRadius(t *testing.T) {
	const radius = 15000
	for _, lat := range []float64{0, 47.5, 54.9, 80} {
		minLat, maxLat, minLon, maxLon := boundingBox(lat, 10, radius)
		north := haversine(lat, 10, maxLat, 10)
		east := haversine(lat, 10, lat, maxLon)
		if north < radius-1 || east < radius-1 {
			t.Errorf("lat %v: box too small (north %.0f m, east %.0f m)", lat, north, east)
		}
		if minLat >= lat || minLon >= 10 {
			t.Errorf("lat %v: box does not extend south/west", lat)
		}
	}
	if _, _, minLon, maxLon := boundingBox(89.99, 0, radius); minLon != -180 || maxLon != 180 {
		t.Errorf("box near the pole should span all longitudes, got %v..%v", minLon, maxLon)
	}
}

func TestDistanceToArea(t *testing.T) {
	// A square of 0.02° with a hole of 0.01° in the middle, around 53°N 8°E.
	rings := []outline.Ring{
		{{8.0, 53.0}, {8.02, 53.0}, {8.02, 53.02}, {8.0, 53.02}, {8.0, 53.0}},
		{{8.005, 53.005}, {8.005, 53.015}, {8.015, 53.015}, {8.015, 53.005}, {8.005, 53.005}},
	}
	tests := []struct {
		name     string
		lat, lon float64
		want     float64
	}{
		{"inside", 53.002, 8.01, 0},
		{"in the hole", 53.01, 8.01, haversine(53.01, 8.01, 53.01, 8.015)},
		{"south", 52.99, 8.01, haversine(52.99, 8.01, 53.0, 8.01)},
		{"east", 53.01, 8.03, haversine(53.01, 8.03, 53.01, 8.02)},
		{"corner", 52.99, 7.99, haversine(52.99, 7.99, 53.0, 8.0)},
	}
	for _, tt := range tests {
		if got := distanceToArea(rings, tt.lat, tt.lon); math.Abs(got-tt.want) > 1 {
			t.Errorf("%s: %.1f m, want %.1f m", tt.name, got, tt.want)
		}
	}
}
