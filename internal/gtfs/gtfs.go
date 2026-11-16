// Package gtfs counts how often public transport departs from each stop on
// a typical weekday, from a GTFS schedule feed.
package gtfs

import (
	"archive/zip"
	"bufio"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// Departures are counted on a Tuesday between 06:00 and 20:00 and
	// averaged per hour: a Tuesday is rarely a holiday or the first day of
	// a timetable change.
	countWeekday    = time.Tuesday
	countFromSecond = 6 * 3600
	countToSecond   = 20 * 3600
	// candidateDays is how many days from the start date are searched for
	// the Tuesday with the most service.
	candidateDays = 35

	gridDegrees       = 0.01
	earthRadiusMeters = 6371008.8
)

// Departures holds the average departures per hour of each stop, by mode.
type Departures struct {
	// Date is the day the departures were counted on.
	Date  time.Time
	stops []stop
	grid  map[cell][]int
}

type stop struct {
	lat, lon float64
	perHour  map[string]float64
}

type cell struct{ x, y int }

func cellOf(lat, lon float64) cell {
	return cell{int(math.Floor(lon / gridDegrees)), int(math.Floor(lat / gridDegrees))}
}

// PerHour returns the departures per hour of the given modes, summed over
// all stops within the radius. It reports false when there is no stop with
// such departures nearby.
func (d *Departures) PerHour(lat, lon, radiusMeters float64, modes []string) (float64, bool) {
	if d == nil {
		return 0, false
	}
	dLat := radiusMeters / earthRadiusMeters * 180 / math.Pi
	dLon := dLat / math.Cos(lat*math.Pi/180)
	lo, hi := cellOf(lat-dLat, lon-dLon), cellOf(lat+dLat, lon+dLon)
	var sum float64
	found := false
	for x := lo.x; x <= hi.x; x++ {
		for y := lo.y; y <= hi.y; y++ {
			for _, i := range d.grid[cell{x, y}] {
				s := d.stops[i]
				if distance(lat, lon, s.lat, s.lon) > radiusMeters {
					continue
				}
				for _, m := range modes {
					if n, ok := s.perHour[m]; ok {
						sum += n
						found = true
					}
				}
			}
		}
	}
	return sum, found
}

// distance is an equirectangular approximation, accurate enough for the few
// hundred meters between a stop in OSM and in GTFS.
func distance(lat1, lon1, lat2, lon2 float64) float64 {
	x := (lon2 - lon1) * math.Cos((lat1+lat2)/2*math.Pi/180)
	y := lat2 - lat1
	return math.Hypot(x, y) * math.Pi / 180 * earthRadiusMeters
}

// Read counts the departures in a GTFS feed, a zip file or a directory, on
// a Tuesday in the weeks after from (or after the start of the feed, if that
// is later).
func Read(path string, from time.Time) (*Departures, error) {
	f := feed{fsys: os.DirFS(path)}
	if info, err := os.Stat(path); err != nil {
		return nil, err
	} else if !info.IsDir() {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		f.fsys = zr
	}

	services, err := f.readServices()
	if err != nil {
		return nil, err
	}
	routeModes, err := f.readRouteModes()
	if err != nil {
		return nil, err
	}
	trips, err := f.readTrips(routeModes)
	if err != nil {
		return nil, err
	}
	date, active := services.busiestWeekday(from, trips)
	if len(active) == 0 {
		return nil, fmt.Errorf("no service on any %s in the %d days after %s", countWeekday, candidateDays, from.Format(time.DateOnly))
	}
	tripModes := make(map[string]string)
	for id, t := range trips {
		if active[t.service] {
			tripModes[id] = t.mode
		}
	}
	frequencies, err := f.readFrequencies(tripModes)
	if err != nil {
		return nil, err
	}
	counts, err := f.countDepartures(tripModes, frequencies)
	if err != nil {
		return nil, err
	}
	d := &Departures{Date: date, grid: make(map[cell][]int)}
	if err := f.readStops(counts, d); err != nil {
		return nil, err
	}
	return d, nil
}

type feed struct{ fsys fs.FS }

// each calls fn for every record of a GTFS file with a lookup of the
// columns by name. A missing optional file is skipped.
func (f feed) each(name string, optional bool, fn func(col func(string) string) error) error {
	file, err := f.fsys.Open(name)
	if err != nil {
		if optional && errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	defer file.Close()
	r := csv.NewReader(bufio.NewReaderSize(file, 1<<20))
	r.ReuseRecord = true
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err == io.EOF {
		return nil
	} else if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	index := make(map[string]int, len(header))
	for i, h := range header {
		index[strings.TrimSpace(strings.TrimPrefix(h, "\uFEFF"))] = i
	}
	for {
		rec, err := r.Read()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		col := func(key string) string {
			if i, ok := index[key]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		if err := fn(col); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
}

type services struct {
	weekly []weeklyService
	// exceptions maps a date (YYYYMMDD) to the services added (true) or
	// removed (false) on it.
	exceptions map[string]map[string]bool
	first      time.Time
}

type weeklyService struct {
	id         string
	weekdays   [7]bool
	start, end string // YYYYMMDD, compared as strings
}

func (f feed) readServices() (*services, error) {
	s := &services{exceptions: make(map[string]map[string]bool)}
	noteDate := func(date string) error {
		t, err := time.Parse("20060102", date)
		if err != nil {
			return fmt.Errorf("invalid date %q", date)
		}
		if s.first.IsZero() || t.Before(s.first) {
			s.first = t
		}
		return nil
	}
	days := []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}
	err := f.each("calendar.txt", true, func(col func(string) string) error {
		w := weeklyService{id: col("service_id"), start: col("start_date"), end: col("end_date")}
		for i, day := range days {
			w.weekdays[i] = col(day) == "1"
		}
		s.weekly = append(s.weekly, w)
		return noteDate(w.start)
	})
	if err != nil {
		return nil, err
	}
	err = f.each("calendar_dates.txt", true, func(col func(string) string) error {
		date := col("date")
		if s.exceptions[date] == nil {
			s.exceptions[date] = make(map[string]bool)
		}
		s.exceptions[date][col("service_id")] = col("exception_type") == "1"
		return noteDate(date)
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (s *services) activeOn(t time.Time) map[string]bool {
	date := t.Format("20060102")
	active := make(map[string]bool)
	for _, w := range s.weekly {
		if w.weekdays[t.Weekday()] && w.start <= date && date <= w.end {
			active[w.id] = true
		}
	}
	for id, added := range s.exceptions[date] {
		if added {
			active[id] = true
		} else {
			delete(active, id)
		}
	}
	return active
}

// busiestWeekday returns the counting weekday with the most trips in the
// weeks after from, and the services active on it. Holiday timetables have
// fewer trips but often more, smaller services.
func (s *services) busiestWeekday(from time.Time, trips map[string]trip) (time.Time, map[string]bool) {
	if s.first.After(from) {
		from = s.first
	}
	perService := make(map[string]int)
	for _, t := range trips {
		perService[t.service]++
	}
	var best time.Time
	var bestActive map[string]bool
	bestTrips := 0
	for i := range candidateDays {
		t := from.AddDate(0, 0, i)
		if t.Weekday() != countWeekday {
			continue
		}
		active := s.activeOn(t)
		n := 0
		for id := range active {
			n += perService[id]
		}
		if n > bestTrips {
			best, bestActive, bestTrips = t, active, n
		}
	}
	return best, bestActive
}

func (f feed) readRouteModes() (map[string]string, error) {
	modes := make(map[string]string)
	err := f.each("routes.txt", false, func(col func(string) string) error {
		routeType, err := strconv.Atoi(col("route_type"))
		if err != nil {
			return fmt.Errorf("route %s: invalid route_type", col("route_id"))
		}
		if mode := modeOf(routeType); mode != "" {
			modes[col("route_id")] = mode
		}
		return nil
	})
	return modes, err
}

// modeOf maps a GTFS route type, basic or extended, to an OSM route mode.
// Types without a matching stop category (cable cars, taxis, ...) map to "".
func modeOf(routeType int) string {
	switch {
	case routeType == 0, routeType >= 900 && routeType < 1000:
		return "tram"
	case routeType == 1, routeType >= 400 && routeType < 500:
		return "subway"
	case routeType == 2, routeType >= 100 && routeType < 200:
		return "train"
	case routeType == 3, routeType >= 200 && routeType < 300, routeType >= 700 && routeType < 800:
		return "bus"
	case routeType == 4, routeType >= 1000 && routeType < 1100, routeType == 1200:
		return "ferry"
	case routeType == 11, routeType == 800:
		return "trolleybus"
	case routeType == 12:
		return "light_rail"
	}
	return ""
}

type trip struct{ service, mode string }

// readTrips returns the trips of routes with a mode.
func (f feed) readTrips(routeModes map[string]string) (map[string]trip, error) {
	trips := make(map[string]trip)
	err := f.each("trips.txt", false, func(col func(string) string) error {
		if mode, ok := routeModes[col("route_id")]; ok {
			trips[col("trip_id")] = trip{service: col("service_id"), mode: mode}
		}
		return nil
	})
	return trips, err
}

type frequency struct{ start, end, headway int }

func (f feed) readFrequencies(trips map[string]string) (map[string][]frequency, error) {
	freqs := make(map[string][]frequency)
	err := f.each("frequencies.txt", true, func(col func(string) string) error {
		id := col("trip_id")
		if _, ok := trips[id]; !ok {
			return nil
		}
		start, err1 := parseTime(col("start_time"))
		end, err2 := parseTime(col("end_time"))
		headway, err3 := strconv.Atoi(col("headway_secs"))
		if err := errors.Join(err1, err2, err3); err != nil || headway <= 0 {
			return fmt.Errorf("trip %s: invalid frequency", id)
		}
		freqs[id] = append(freqs[id], frequency{start, end, headway})
		return nil
	})
	return freqs, err
}

// parseTime parses HH:MM:SS, which may exceed 24:00:00 for trips after
// midnight, into seconds since the start of the service day.
func parseTime(s string) (int, error) {
	h, rest, ok1 := strings.Cut(s, ":")
	m, sec, ok2 := strings.Cut(rest, ":")
	hours, err1 := strconv.Atoi(h)
	minutes, err2 := strconv.Atoi(m)
	seconds, err3 := strconv.Atoi(sec)
	if !ok1 || !ok2 || errors.Join(err1, err2, err3) != nil {
		return 0, fmt.Errorf("invalid time %q", s)
	}
	return hours*3600 + minutes*60 + seconds, nil
}

type stopTime struct {
	stop     string
	sequence int
	time     int // -1 if the feed has no time for the stop
	pickup   bool
}

// modeCounts counts departures per mode in the counting window.
type modeCounts map[string]float64

// countDepartures counts the departures in the counting window per stop.
// The last stop of a trip is not a departure, and neither is a stop
// without pickup.
func (f feed) countDepartures(trips map[string]string, freqs map[string][]frequency) (map[string]modeCounts, error) {
	counts := make(map[string]modeCounts)
	add := func(stopID, mode string, n float64) {
		if n == 0 {
			return
		}
		if counts[stopID] == nil {
			counts[stopID] = make(modeCounts)
		}
		counts[stopID][mode] += n
	}
	// The highest stop of each trip seen so far: counted once a later stop
	// shows that it is not the last one.
	pending := make(map[string]stopTime)
	// Frequency-based trips are templates: their times are offsets.
	templates := make(map[string][]stopTime)
	err := f.each("stop_times.txt", false, func(col func(string) string) error {
		tripID := col("trip_id")
		mode, ok := trips[tripID]
		if !ok {
			return nil
		}
		st, err := readStopTime(col)
		if err != nil {
			return fmt.Errorf("trip %s: %w", tripID, err)
		}
		if _, ok := freqs[tripID]; ok {
			templates[tripID] = append(templates[tripID], st)
			return nil
		}
		prev, ok := pending[tripID]
		if ok && st.sequence < prev.sequence {
			st, prev = prev, st
		}
		pending[tripID] = st
		if ok && prev.pickup && inWindow(prev.time) {
			add(prev.stop, mode, 1)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	for tripID, sts := range templates {
		slices.SortFunc(sts, func(a, b stopTime) int { return a.sequence - b.sequence })
		first := sts[0].time
		for _, st := range sts[:len(sts)-1] {
			if !st.pickup || st.time < 0 || first < 0 {
				continue
			}
			offset := st.time - first
			for _, fr := range freqs[tripID] {
				add(st.stop, trips[tripID], float64(startsInWindow(fr, offset)))
			}
		}
	}
	return counts, nil
}

func readStopTime(col func(string) string) (stopTime, error) {
	seq, err := strconv.Atoi(col("stop_sequence"))
	if err != nil {
		return stopTime{}, fmt.Errorf("invalid stop_sequence %q", col("stop_sequence"))
	}
	st := stopTime{stop: col("stop_id"), sequence: seq, time: -1, pickup: col("pickup_type") != "1"}
	raw := col("departure_time")
	if raw == "" {
		raw = col("arrival_time")
	}
	// shortcut: stops without a time (interpolated by the consumer) are not
	// counted; interpolate between timepoints if a feed relies on that.
	if raw != "" {
		if st.time, err = parseTime(raw); err != nil {
			return stopTime{}, err
		}
	}
	return st, nil
}

func inWindow(t int) bool { return t >= countFromSecond && t < countToSecond }

// startsInWindow counts the trips of a frequency whose departure at a stop
// offset seconds after the first stop falls into the counting window.
func startsInWindow(fr frequency, offset int) int {
	n := 0
	for start := fr.start; start < fr.end; start += fr.headway {
		if inWindow(start + offset) {
			n++
		}
	}
	return n
}

func (f feed) readStops(counts map[string]modeCounts, d *Departures) error {
	hours := float64(countToSecond-countFromSecond) / 3600
	return f.each("stops.txt", false, func(col func(string) string) error {
		c, ok := counts[col("stop_id")]
		if !ok {
			return nil
		}
		lat, err1 := strconv.ParseFloat(col("stop_lat"), 64)
		lon, err2 := strconv.ParseFloat(col("stop_lon"), 64)
		if errors.Join(err1, err2) != nil {
			return fmt.Errorf("stop %s: invalid coordinates", col("stop_id"))
		}
		perHour := make(map[string]float64, len(c))
		for mode, n := range c {
			perHour[mode] = n / hours
		}
		d.grid[cellOf(lat, lon)] = append(d.grid[cellOf(lat, lon)], len(d.stops))
		d.stops = append(d.stops, stop{lat: lat, lon: lon, perHour: perHour})
		return nil
	})
}
