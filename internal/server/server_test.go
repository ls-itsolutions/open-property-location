package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/ls-itsolutions/open-property-location/internal/categories"
	"github.com/ls-itsolutions/open-property-location/internal/geocode"
	"github.com/ls-itsolutions/open-property-location/internal/importer"
)

func newTestHandler(t *testing.T, photon http.HandlerFunc) http.Handler {
	t.Helper()
	cats, err := categories.Load()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open("testdata/places.geojsonseq")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	dbPath := filepath.Join(t.TempDir(), "places.db")
	if _, err := importer.Import(cats, in, dbPath, importer.Meta{Region: "test", DataVersion: "2026-10-26"}); err != nil {
		t.Fatal(err)
	}
	places, err := Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { places.Close() })

	if photon == nil {
		photon = func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected geocoding request") }
	}
	photonServer := httptest.NewServer(photon)
	t.Cleanup(photonServer.Close)
	geocoder, err := geocode.New(photonServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	return New(places, cats, geocoder)
}

func get(t *testing.T, h http.Handler, target string, wantStatus int, body any) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != wantStatus {
		t.Fatalf("GET %s: status %d, want %d, body %s", target, rec.Code, wantStatus, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("GET %s: content type %q", target, ct)
	}
	if body != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), body); err != nil {
			t.Fatalf("GET %s: %v", target, err)
		}
	}
}

func TestSurroundings(t *testing.T) {
	h := newTestHandler(t, nil)
	var resp surroundingsResponse
	get(t, h, "/v1/surroundings?lat=53.0&lon=8.8&preset=living", http.StatusOK, &resp)

	if resp.DataVersion != "2026-10-26" || resp.Attribution != attribution {
		t.Errorf("meta = %q, %q", resp.DataVersion, resp.Attribution)
	}
	cats, _ := categories.Load()
	living := cats.Presets["living"]
	if len(resp.Categories) != len(living) {
		t.Fatalf("got %d categories, want %d", len(resp.Categories), len(living))
	}
	byID := make(map[string][]Place)
	for i, c := range resp.Categories {
		if c.ID != living[i] {
			t.Errorf("category %d = %s, want %s (preset order)", i, c.ID, living[i])
		}
		if c.Places == nil {
			t.Errorf("category %s: places must be an empty array, not null", c.ID)
		}
		byID[c.ID] = c.Places
	}

	// "Fern" is farther away but has a Wikidata entry and a website, so it
	// ranks first. "Weit" is outside the bakery radius.
	bakeries := byID["bakery"]
	if len(bakeries) != 2 || *bakeries[0].Name != "Fern" || *bakeries[1].Name != "Nah" {
		t.Fatalf("bakeries = %+v", bakeries)
	}
	fern := bakeries[0]
	if fern.ID != "node/2" || fern.OSMURL != "https://www.openstreetmap.org/node/2" {
		t.Errorf("fern id = %s, url = %s", fern.ID, fern.OSMURL)
	}
	if fern.DistanceMeters != 150 || fern.WalkingMinutes != 3 {
		t.Errorf("fern distance = %d m, %d min", fern.DistanceMeters, fern.WalkingMinutes)
	}
	if fern.Website == nil || *fern.Website != "https://fern.example" || fern.Wikidata == nil || *fern.Wikidata != "Q1" {
		t.Errorf("fern website/wikidata = %v, %v", fern.Website, fern.Wikidata)
	}
	if fern.Address.Street == nil || *fern.Address.Street != "Hauptstraße" || fern.Address.City != nil {
		t.Errorf("fern address = %+v", fern.Address)
	}

	if parks := byID["park"]; len(parks) != 1 || parks[0].Name != nil || parks[0].ID != "way/5" {
		t.Errorf("parks = %+v", parks)
	}
	if len(byID["hospital"]) != 0 {
		t.Errorf("hospitals = %+v", byID["hospital"])
	}
}

func TestSurroundingsCollapseSameName(t *testing.T) {
	h := newTestHandler(t, nil)
	var resp surroundingsResponse
	get(t, h, "/v1/surroundings?lat=53.0&lon=8.8&categories=bus_tram_stop,supermarket", http.StatusOK, &resp)
	var names []string
	for _, p := range resp.Categories[0].Places {
		names = append(names, p.ID+" "+*p.Name)
	}
	if want := []string{"node/7 Markt", "node/9 Rathaus"}; !slices.Equal(names, want) {
		t.Errorf("bus stops = %v, want %v", names, want)
	}
	// Supermarkets do not collapse: two shops of a chain are two places.
	if n := len(resp.Categories[1].Places); n != 2 {
		t.Errorf("got %d supermarkets, want 2", n)
	}
}

func TestSurroundingsNullFields(t *testing.T) {
	h := newTestHandler(t, nil)
	var raw struct {
		Categories []struct {
			Places []map[string]any `json:"places"`
		} `json:"categories"`
	}
	get(t, h, "/v1/surroundings?lat=53.0&lon=8.8&categories=park", http.StatusOK, &raw)
	park := raw.Categories[0].Places[0]
	for _, key := range []string{"name", "openingHours", "website", "wikidata"} {
		if v, ok := park[key]; !ok || v != nil {
			t.Errorf("%s = %v (present %v), want null", key, v, ok)
		}
	}
}

func TestSurroundingsCategoriesAndLimit(t *testing.T) {
	h := newTestHandler(t, nil)
	var resp surroundingsResponse
	get(t, h, "/v1/surroundings?lat=53.0&lon=8.8&categories=supermarket,%20bakery,supermarket&limit=1", http.StatusOK, &resp)
	if len(resp.Categories) != 2 || resp.Categories[0].ID != "supermarket" || resp.Categories[1].ID != "bakery" {
		t.Fatalf("categories = %+v", resp.Categories)
	}
	if n := len(resp.Categories[1].Places); n != 1 {
		t.Errorf("limit=1 returned %d bakeries", n)
	}
}

func TestSurroundingsBadRequest(t *testing.T) {
	h := newTestHandler(t, nil)
	for _, query := range []string{
		"",
		"preset=stay",
		"lat=53&preset=stay",
		"lat=53&lon=8.8&address=Bremen&preset=stay",
		"lat=abc&lon=8.8&preset=stay",
		"lat=NaN&lon=8.8&preset=stay",
		"lat=53&lon=181&preset=stay",
		"lat=53&lon=8.8",
		"lat=53&lon=8.8&preset=stay&categories=cafe",
		"lat=53&lon=8.8&preset=unknown",
		"lat=53&lon=8.8&categories=cafe,unknown",
		"lat=53&lon=8.8&categories=",
		"lat=53&lon=8.8&preset=stay&limit=0",
		"lat=53&lon=8.8&preset=stay&limit=21",
		"lat=53&lon=8.8&preset=stay&limit=2.5",
		"address=&preset=stay",
		"address=" + strings.Repeat("a", maxAddressLength+1) + "&preset=stay",
	} {
		var body map[string]string
		get(t, h, "/v1/surroundings?"+query, http.StatusBadRequest, &body)
		if body["error"] == "" {
			t.Errorf("%q: missing error message", query)
		}
	}
}

func TestSurroundingsAddress(t *testing.T) {
	var gotQuery string
	h := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		switch gotQuery {
		case "Hauptstraße 1, Bremen":
			w.Write([]byte(`{"features":[{"geometry":{"coordinates":[8.8,53.0]}}]}`))
		case "Nowhere":
			w.Write([]byte(`{"features":[]}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})

	var resp surroundingsResponse
	get(t, h, "/v1/surroundings?address=Hauptstra%C3%9Fe+1%2C+Bremen&categories=bakery", http.StatusOK, &resp)
	if gotQuery != "Hauptstraße 1, Bremen" {
		t.Errorf("photon got q=%q", gotQuery)
	}
	if resp.Location != (location{Lat: 53.0, Lon: 8.8}) || len(resp.Categories[0].Places) != 2 {
		t.Errorf("resp = %+v", resp)
	}

	get(t, h, "/v1/surroundings?address=Nowhere&categories=bakery", http.StatusNotFound, nil)
	get(t, h, "/v1/surroundings?address=fail&categories=bakery", http.StatusBadGateway, nil)
}

func TestCategoriesEndpoint(t *testing.T) {
	h := newTestHandler(t, nil)
	var resp struct {
		Categories []categoryInfo      `json:"categories"`
		Presets    map[string][]string `json:"presets"`
	}
	get(t, h, "/v1/categories", http.StatusOK, &resp)
	if len(resp.Categories) == 0 || resp.Categories[0].ID != "restaurant" || resp.Categories[0].MaxDistanceMeters != 2000 {
		t.Errorf("categories = %+v", resp.Categories)
	}
	if len(resp.Presets["stay"]) == 0 || len(resp.Presets["living"]) == 0 {
		t.Errorf("presets = %+v", resp.Presets)
	}
}

func TestHealth(t *testing.T) {
	h := newTestHandler(t, nil)
	var body map[string]string
	get(t, h, "/health", http.StatusOK, &body)
	if body["status"] != "ok" {
		t.Errorf("health = %v", body)
	}
}
