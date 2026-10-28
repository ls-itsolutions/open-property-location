package server

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	_ "modernc.org/sqlite"

	"github.com/ls-itsolutions/open-property-location/internal/categories"
)

// maxConnections bounds memory use: every connection has its own page cache.
const maxConnections = 4

// Places is read-only access to places.db.
type Places struct {
	db           *sql.DB
	dataVersion  string
	categoryKeys map[string]int
}

// Open opens places.db read-only.
func Open(path string) (*Places, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// immutable: the file never changes while the server runs, so SQLite
	// can skip locking.
	dsn := (&url.URL{Scheme: "file", Path: abs, RawQuery: "mode=ro&immutable=1"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(maxConnections)
	db.SetMaxIdleConns(maxConnections)

	p := &Places{db: db}
	if err := p.readMeta(); err != nil {
		db.Close()
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return p, nil
}

func (p *Places) readMeta() error {
	if err := p.db.QueryRow(`SELECT value FROM meta WHERE key = 'data_version'`).Scan(&p.dataVersion); err != nil {
		return err
	}
	rows, err := p.db.Query(`SELECT key, id FROM categories`)
	if err != nil {
		return err
	}
	defer rows.Close()
	p.categoryKeys = make(map[string]int)
	for rows.Next() {
		var key int
		var id string
		if err := rows.Scan(&key, &id); err != nil {
			return err
		}
		p.categoryKeys[id] = key
	}
	return rows.Err()
}

func (p *Places) Close() error { return p.db.Close() }

func (p *Places) DataVersion() string { return p.dataVersion }

// Ping checks that the database is readable.
func (p *Places) Ping(ctx context.Context) error {
	var one int
	return p.db.QueryRowContext(ctx, `SELECT 1 FROM meta LIMIT 1`).Scan(&one)
}

// Place is one result in the API response. Optional fields are nil when the
// OSM tag is missing.
type Place struct {
	ID             string  `json:"id"`
	Name           *string `json:"name"`
	Lat            float64 `json:"lat"`
	Lon            float64 `json:"lon"`
	DistanceMeters int     `json:"distanceMeters"`
	// WalkingMinutes is estimated from the straight-line distance.
	WalkingMinutes int     `json:"walkingMinutes"`
	Address        Address `json:"address"`
	OpeningHours   *string `json:"openingHours"`
	Website        *string `json:"website"`
	Wikidata       *string `json:"wikidata"`
	OSMURL         string  `json:"osmUrl"`
}

type Address struct {
	Street      *string `json:"street"`
	HouseNumber *string `json:"houseNumber"`
	Postcode    *string `json:"postcode"`
	City        *string `json:"city"`
}

type candidate struct {
	osmType    string
	osmID      int64
	name       sql.NullString
	lat, lon   float64
	importance float64
	tags       string
	distance   float64
	score      float64
}

// Nearby returns up to limit places of the category within its search
// radius, best ranked first.
func (p *Places) Nearby(ctx context.Context, cat *categories.Category, lat, lon float64, limit int) ([]Place, error) {
	key, ok := p.categoryKeys[cat.ID]
	if !ok {
		// The database was built before the category was added.
		return []Place{}, nil
	}
	radius := float64(cat.MaxDistanceMeters)
	minLat, maxLat, minLon, maxLon := boundingBox(lat, lon, radius)
	rows, err := p.db.QueryContext(ctx, `
		SELECT p.osm_type, p.osm_id, p.name, p.lat, p.lon, p.importance, p.tags
		FROM places_rtree r JOIN places p ON p.id = r.id
		WHERE r.min_lat <= ? AND r.max_lat >= ? AND r.min_lon <= ? AND r.max_lon >= ?
		  AND r.min_cat = ?`,
		maxLat, minLat, maxLon, minLon, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var candidates []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.osmType, &c.osmID, &c.name, &c.lat, &c.lon, &c.importance, &c.tags); err != nil {
			return nil, err
		}
		c.distance = haversine(lat, lon, c.lat, c.lon)
		if c.distance > radius {
			continue
		}
		c.score = rankScore(c.distance, c.importance)
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	slices.SortFunc(candidates, func(a, b candidate) int {
		return cmp.Or(
			cmp.Compare(a.score, b.score),
			cmp.Compare(a.distance, b.distance),
			cmp.Compare(a.osmType, b.osmType),
			cmp.Compare(a.osmID, b.osmID),
		)
	})

	places := make([]Place, 0, min(limit, len(candidates)))
	seenNames := make(map[string]bool)
	for _, c := range candidates {
		if len(places) == limit {
			break
		}
		if cat.CollapseSameName && c.name.Valid {
			if seenNames[c.name.String] {
				continue
			}
			seenNames[c.name.String] = true
		}
		place, err := c.toPlace()
		if err != nil {
			return nil, err
		}
		places = append(places, place)
	}
	return places, nil
}

func (c candidate) toPlace() (Place, error) {
	var tags map[string]string
	if err := json.Unmarshal([]byte(c.tags), &tags); err != nil {
		return Place{}, fmt.Errorf("%s/%d: invalid tags: %w", c.osmType, c.osmID, err)
	}
	optional := func(key string) *string {
		if v, ok := tags[key]; ok {
			return &v
		}
		return nil
	}
	id := c.osmType + "/" + strconv.FormatInt(c.osmID, 10)
	place := Place{
		ID:             id,
		Lat:            c.lat,
		Lon:            c.lon,
		DistanceMeters: int(math.Round(c.distance)),
		WalkingMinutes: walkingMinutes(c.distance),
		Address: Address{
			Street:      optional("addr:street"),
			HouseNumber: optional("addr:housenumber"),
			Postcode:    optional("addr:postcode"),
			City:        optional("addr:city"),
		},
		OpeningHours: optional("opening_hours"),
		Website:      optional("website"),
		Wikidata:     optional("wikidata"),
		OSMURL:       "https://www.openstreetmap.org/" + id,
	}
	if c.name.Valid {
		place.Name = &c.name.String
	}
	return place, nil
}
