package importer

import (
	"database/sql"
	"maps"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ls-itsolutions/open-property-location/internal/categories"
	"github.com/ls-itsolutions/open-property-location/internal/outline"
)

func TestImport(t *testing.T) {
	cfg, err := categories.Load()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open("testdata/sample.geojsonseq")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()

	out := filepath.Join(t.TempDir(), "places.db")
	counts, err := Import(cfg, in, nil, out, Meta{Region: "test", DataVersion: "2026-10-26"})
	if err != nil {
		t.Fatal(err)
	}
	wantCounts := map[string]int{"restaurant": 1, "park": 1, "bus_tram_stop": 1, "cafe": 1, "beach": 1}
	if !maps.Equal(counts, wantCounts) {
		t.Errorf("counts = %v, want %v", counts, wantCounts)
	}
	if _, err := os.Stat(out + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}

	db, err := sql.Open("sqlite", out)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	type row struct {
		category   string
		name       sql.NullString
		lat, lon   float64
		importance float64
		tags       string
	}
	get := func(osmType string, osmID int) row {
		t.Helper()
		var r row
		err := db.QueryRow(`SELECT category, name, lat, lon, importance, tags FROM places
			WHERE osm_type = ? AND osm_id = ?`, osmType, osmID).
			Scan(&r.category, &r.name, &r.lat, &r.lon, &r.importance, &r.tags)
		if err != nil {
			t.Fatalf("%s/%d: %v", osmType, osmID, err)
		}
		return r
	}

	restaurant := get("node", 1)
	if math.Abs(restaurant.importance-0.8) > 1e-9 {
		t.Errorf("restaurant importance = %v, want 0.8", restaurant.importance)
	}
	wantTags := `{"addr:city":"Bremen","addr:housenumber":"1","addr:postcode":"28195","addr:street":"Marktplatz","opening_hours":"Mo-Su 11:00-22:00","website":"https://zur-post.example","wikidata":"Q42"}`
	if restaurant.tags != wantTags {
		t.Errorf("restaurant tags = %s", restaurant.tags)
	}

	park := get("way", 4)
	if park.name.Valid || park.lat != 53.001 || park.lon != 8.801 {
		t.Errorf("park = %+v", park)
	}

	stop := get("node", 6)
	if stop.tags != "{}" || stop.importance != 0 {
		t.Errorf("bus stop kept invalid website or wikidata: %+v", stop)
	}

	if beach := get("relation", 8); math.Abs(beach.lat-7.0/3) > 1e-6 || math.Abs(beach.lon-7.0/3) > 1e-6 {
		t.Errorf("beach centroid = %v, %v", beach.lat, beach.lon)
	}

	var rtreeRows int
	if err := db.QueryRow(`SELECT count(*) FROM places_rtree
		WHERE min_lat <= 53.0011 AND max_lat >= 53.0009 AND min_lon <= 8.8011 AND max_lon >= 8.8009
		  AND min_cat = (SELECT key FROM categories WHERE id = 'park')`).
		Scan(&rtreeRows); err != nil || rtreeRows != 1 {
		t.Errorf("rtree lookup = %d, %v", rtreeRows, err)
	}

	// Parks and beaches measure the distance to their edge, so the R*Tree
	// holds their bounding box and the outline is stored. Other places are
	// points.
	box := func(osmType string, osmID int) [4]float64 {
		t.Helper()
		var b [4]float64
		err := db.QueryRow(`SELECT r.min_lat, r.max_lat, r.min_lon, r.max_lon
			FROM places_rtree r JOIN places p ON p.id = r.id WHERE p.osm_type = ? AND p.osm_id = ?`, osmType, osmID).
			Scan(&b[0], &b[1], &b[2], &b[3])
		if err != nil {
			t.Fatalf("%s/%d: %v", osmType, osmID, err)
		}
		return b
	}
	near := func(a, b [4]float64) bool {
		for i := range a {
			// The R*Tree stores 32-bit floats.
			if math.Abs(a[i]-b[i]) > 1e-5 {
				return false
			}
		}
		return true
	}
	if b := box("way", 4); !near(b, [4]float64{53.0, 53.002, 8.8, 8.802}) {
		t.Errorf("park box = %v", b)
	}
	if b := box("relation", 8); !near(b, [4]float64{0, 4, 0, 4}) {
		t.Errorf("beach box = %v", b)
	}
	if b := box("node", 1); !near(b, [4]float64{53.0, 53.0, 8.8, 8.8}) {
		t.Errorf("restaurant box = %v", b)
	}
	var blob []byte
	if err := db.QueryRow(`SELECT outline FROM places WHERE osm_type = 'relation' AND osm_id = 8`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if rings, err := outline.Decode(blob); err != nil || len(rings) != 2 || len(rings[1]) != 5 {
		t.Errorf("beach outline = %v, %v", rings, err)
	}
	var withOutline int
	if err := db.QueryRow(`SELECT count(*) FROM places WHERE outline IS NOT NULL`).Scan(&withOutline); err != nil || withOutline != 2 {
		t.Errorf("places with outline = %d, %v; want park and beach only", withOutline, err)
	}

	var version string
	if err := db.QueryRow(`SELECT value FROM meta WHERE key = 'data_version'`).Scan(&version); err != nil || version != "2026-10-26" {
		t.Errorf("data_version = %q, %v", version, err)
	}
}

func TestImportKeepsExistingFileOnError(t *testing.T) {
	cfg, err := categories.Load()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "places.db")
	if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, geometry := range []string{
		`{"type":"LineString","coordinates":[[0,0],[1,1]]}`,
		`{"type":"MultiPolygon","coordinates":null}`,
		`{"type":"Polygon","coordinates":[[[0,0],[200,0],[0,1],[0,0]]]}`,
	} {
		bad := `{"type":"Feature","geometry":` + geometry + `,"properties":{"@type":"way","@id":1,"leisure":"park"}}`
		if _, err := Import(cfg, strings.NewReader(bad), nil, out, Meta{Region: "test", DataVersion: "2026-10-26"}); err == nil {
			t.Fatalf("%s: expected error", geometry)
		}
	}
	if data, _ := os.ReadFile(out); string(data) != "old" {
		t.Error("existing database was replaced")
	}
	if _, err := os.Stat(out + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file left behind: %v", err)
	}
}

func TestImportance(t *testing.T) {
	tests := []struct {
		tags map[string]string
		want float64
	}{
		{map[string]string{}, 0},
		{map[string]string{"wikipedia": "de:Roland"}, 0.5},
		{map[string]string{"website": "https://a.example"}, 0.2},
		{map[string]string{"website": "ftp://a.example"}, 0},
		{map[string]string{"opening_hours": "24/7"}, 0.1},
		{map[string]string{"historic": "monument"}, 0.2},
		{map[string]string{"wikidata": "Q1", "website": "https://a.example", "opening_hours": "24/7", "tourism": "attraction"}, 1},
	}
	for _, tt := range tests {
		if got := importance(tt.tags); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("importance(%v) = %v, want %v", tt.tags, got, tt.want)
		}
	}
}

func TestWebsite(t *testing.T) {
	tests := map[string]string{
		"https://a.example/x":              "https://a.example/x",
		"HTTP://a.example":                 "HTTP://a.example",
		"https://a.example;https://b.test": "https://a.example",
		"www.a.example":                    "",
		"javascript:alert(1)":              "",
		"https://":                         "",
	}
	for in, want := range tests {
		if got := website(map[string]string{"website": in}); got != want {
			t.Errorf("website(%q) = %q, want %q", in, got, want)
		}
	}
	if got := website(map[string]string{"contact:website": "https://c.example"}); got != "https://c.example" {
		t.Errorf("contact:website fallback = %q", got)
	}
}

func TestCentroidDegenerate(t *testing.T) {
	line := [][][]point{{{{1, 1}, {3, 3}, {1, 1}}}}
	got := centroid(line)
	if want := (point{2, 2}); math.Abs(got[0]-want[0]) > 1e-9 || math.Abs(got[1]-want[1]) > 1e-9 {
		t.Errorf("centroid = %v, want %v", got, want)
	}
}
