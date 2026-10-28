package server

import (
	"math"
	"testing"
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
