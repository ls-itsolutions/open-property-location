package gtfs

import (
	"math"
	"testing"
	"time"
)

func readFixture(t *testing.T) *Departures {
	t.Helper()
	d, err := Read("testdata/feed", time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestRead(t *testing.T) {
	d := readFixture(t)
	// Tuesday 2026-11-17 has no WD service, so the next one is busier.
	if got := d.Date.Format(time.DateOnly); got != "2026-11-24" {
		t.Errorf("date = %s, want 2026-11-24", got)
	}
	hours := 14.0
	for _, tc := range []struct {
		name   string
		radius float64
		modes  []string
		want   float64
		found  bool
	}{
		// A and B each have one departure in the window: T2 runs outside
		// it, T3 has no pickup at A, the last stop C is no departure.
		{"both bus stops", 50, []string{"bus"}, 2 / hours, true},
		{"only A", 10, []string{"bus"}, 1 / hours, true},
		{"frequency trip", 10, []string{"train"}, 4 / hours, true},
		{"modes are summed", 10, []string{"bus", "train"}, 5 / hours, true},
		{"no such mode", 50, []string{"ferry"}, 0, false},
	} {
		got, found := d.PerHour(53.0, 8.8, tc.radius, tc.modes)
		if found != tc.found || math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: PerHour = %v, %v, want %v, %v", tc.name, got, found, tc.want, tc.found)
		}
	}
	if _, found := d.PerHour(53.01, 8.8, 50, []string{"bus"}); found {
		t.Error("the last stop of a trip has departures")
	}
}

func TestReadNoService(t *testing.T) {
	if _, err := Read("testdata/feed", time.Date(2027, 6, 1, 0, 0, 0, 0, time.UTC)); err == nil {
		t.Error("no error for a date after the feed")
	}
}

func TestPerHourNil(t *testing.T) {
	var d *Departures
	if _, found := d.PerHour(53.0, 8.8, 50, []string{"bus"}); found {
		t.Error("nil departures found something")
	}
}

func TestModeOf(t *testing.T) {
	for routeType, want := range map[int]string{
		0: "tram", 900: "tram", 1: "subway", 401: "subway", 2: "train", 109: "train",
		3: "bus", 700: "bus", 200: "bus", 4: "ferry", 1000: "ferry", 11: "trolleybus",
		800: "trolleybus", 12: "light_rail", 6: "", 1300: "", 1500: "",
	} {
		if got := modeOf(routeType); got != want {
			t.Errorf("modeOf(%d) = %q, want %q", routeType, got, want)
		}
	}
}

func TestParseTime(t *testing.T) {
	if got, err := parseTime("25:01:02"); err != nil || got != 25*3600+62 {
		t.Errorf("parseTime = %d, %v", got, err)
	}
	if _, err := parseTime("7:00"); err == nil {
		t.Error("no error for HH:MM")
	}
}
