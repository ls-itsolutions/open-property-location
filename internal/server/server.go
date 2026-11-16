// Package server implements the HTTP API.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/ls-itsolutions/open-property-location/internal/categories"
	"github.com/ls-itsolutions/open-property-location/internal/geocode"
)

const (
	defaultLimit     = 5
	maxLimit         = 20
	maxAddressLength = 200
)

type server struct {
	places   *Places
	cats     *categories.Config
	geocoder *geocode.Client
}

// New returns the API handler.
func New(places *Places, cats *categories.Config, geocoder *geocode.Client) http.Handler {
	s := &server{places: places, cats: cats, geocoder: geocoder}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/surroundings", s.surroundings)
	mux.HandleFunc("GET /v1/categories", s.categories)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("POST /mcp", s.mcpHTTP)
	return mux
}

type location struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

type categoryResult struct {
	ID     string  `json:"id"`
	Places []Place `json:"places"`
}

type surroundingsResponse struct {
	Location    location         `json:"location"`
	Categories  []categoryResult `json:"categories"`
	DataVersion string           `json:"dataVersion"`
	Attribution string           `json:"attribution"`
}

func (s *server) surroundings(w http.ResponseWriter, r *http.Request) {
	resp, err := s.lookup(r.Context(), r.URL.Query())
	if err != nil {
		writeLookupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// requestError is an error the client caused, with the HTTP status for it.
type requestError struct {
	status int
	msg    string
}

func (e *requestError) Error() string { return e.msg }

func badRequest(err error) error { return &requestError{http.StatusBadRequest, err.Error()} }

// lookup answers a surroundings query given as API query parameters. Errors
// the client caused are *requestError, all others are internal.
func (s *server) lookup(ctx context.Context, q url.Values) (surroundingsResponse, error) {
	req, err := s.parseRequest(q)
	if err != nil {
		return surroundingsResponse{}, badRequest(err)
	}

	loc := location{Lat: req.lat, Lon: req.lon}
	if req.address != "" {
		loc.Lat, loc.Lon, err = s.geocoder.Geocode(ctx, req.address)
		if errors.Is(err, geocode.ErrNotFound) {
			return surroundingsResponse{}, &requestError{http.StatusNotFound, "address not found"}
		}
		if err != nil {
			slog.Error("geocoding failed", "err", err)
			return surroundingsResponse{}, &requestError{http.StatusBadGateway, "geocoding failed"}
		}
	}

	resp := surroundingsResponse{
		Location:    loc,
		Categories:  make([]categoryResult, 0, len(req.categories)),
		DataVersion: s.places.DataVersion(),
		Attribution: s.places.Attribution(),
	}
	for _, cat := range req.categories {
		places, err := s.places.Nearby(ctx, cat, loc.Lat, loc.Lon, req.limit)
		if err != nil {
			return surroundingsResponse{}, fmt.Errorf("query %s: %w", cat.ID, err)
		}
		resp.Categories = append(resp.Categories, categoryResult{ID: cat.ID, Places: places})
	}
	return resp, nil
}

func writeLookupError(w http.ResponseWriter, err error) {
	var reqErr *requestError
	if errors.As(err, &reqErr) {
		writeError(w, reqErr.status, reqErr.msg)
		return
	}
	slog.Error("lookup failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

type request struct {
	lat, lon   float64
	address    string
	categories []*categories.Category
	limit      int
}

func (s *server) parseRequest(q url.Values) (request, error) {
	var req request
	hasCoords := q.Has("lat") || q.Has("lon")
	if hasCoords == q.Has("address") {
		return req, errors.New("provide either lat and lon or address")
	}
	if hasCoords {
		var err error
		if req.lat, err = parseCoordinate(q, "lat", 90); err != nil {
			return req, err
		}
		if req.lon, err = parseCoordinate(q, "lon", 180); err != nil {
			return req, err
		}
	} else {
		req.address = strings.TrimSpace(q.Get("address"))
		if req.address == "" {
			return req, errors.New("address must not be empty")
		}
		if utf8.RuneCountInString(req.address) > maxAddressLength {
			return req, fmt.Errorf("address must be at most %d characters", maxAddressLength)
		}
	}

	var ids []string
	switch {
	case q.Has("preset") == q.Has("categories"):
		return req, errors.New("provide either preset or categories")
	case q.Has("preset"):
		preset, ok := s.cats.Presets[q.Get("preset")]
		if !ok {
			return req, fmt.Errorf("unknown preset %q", q.Get("preset"))
		}
		ids = preset
	default:
		ids = strings.Split(q.Get("categories"), ",")
	}
	seen := make(map[string]bool)
	for _, id := range ids {
		id = strings.TrimSpace(id)
		cat, ok := s.cats.Get(id)
		if !ok {
			return req, fmt.Errorf("unknown category %q", id)
		}
		if !seen[id] {
			seen[id] = true
			req.categories = append(req.categories, cat)
		}
	}

	req.limit = defaultLimit
	if q.Has("limit") {
		limit, err := strconv.Atoi(q.Get("limit"))
		if err != nil || limit < 1 || limit > maxLimit {
			return req, fmt.Errorf("limit must be an integer between 1 and %d", maxLimit)
		}
		req.limit = limit
	}
	return req, nil
}

func parseCoordinate(q url.Values, name string, maxAbs float64) (float64, error) {
	v, err := strconv.ParseFloat(q.Get(name), 64)
	// The negated comparison also rejects NaN.
	if err != nil || !(v >= -maxAbs && v <= maxAbs) {
		return 0, fmt.Errorf("%s must be a number between %g and %g", name, -maxAbs, maxAbs)
	}
	return v, nil
}

type categoryInfo struct {
	ID                string `json:"id"`
	MaxDistanceMeters int    `json:"maxDistanceMeters"`
}

func (s *server) categories(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.categoryList())
}

type categoryList struct {
	Categories []categoryInfo      `json:"categories"`
	Presets    map[string][]string `json:"presets"`
}

func (s *server) categoryList() categoryList {
	list := categoryList{Presets: s.cats.Presets}
	for _, cat := range s.cats.Categories {
		list.Categories = append(list.Categories, categoryInfo{ID: cat.ID, MaxDistanceMeters: cat.MaxDistanceMeters})
	}
	return list
}

func (s *server) health(w http.ResponseWriter, r *http.Request) {
	if err := s.places.Ping(r.Context()); err != nil {
		slog.Error("health check failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	// Encoding only fails when the client has gone away.
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
