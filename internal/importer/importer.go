// Package importer converts an osmium GeoJSON export into places.db.
package importer

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ls-itsolutions/open-property-location/internal/categories"
	"github.com/ls-itsolutions/open-property-location/internal/transit"
)

const schema = `
PRAGMA journal_mode = OFF;
PRAGMA synchronous = OFF;

CREATE TABLE places (
  id          INTEGER PRIMARY KEY,
  osm_type    TEXT NOT NULL,
  osm_id      INTEGER NOT NULL,
  category    TEXT NOT NULL,
  name        TEXT,
  lat         REAL NOT NULL,
  lon         REAL NOT NULL,
  importance  REAL NOT NULL,
  tags        TEXT NOT NULL,
  lines       TEXT,
  outline     BLOB
);

CREATE VIRTUAL TABLE places_rtree USING rtree(
  id, min_lat, max_lat, min_lon, max_lon, min_cat, max_cat
);

CREATE TABLE categories (key INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE);

CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

const source = "OpenStreetMap"

// Meta describes the data set and is stored in the meta table.
type Meta struct {
	Region      string
	DataVersion string // date of the OSM extract, YYYY-MM-DD
}

// Import reads GeoJSON features as written by
// `osmium export -f geojsonseq -x print_record_separator=false -a type,id`
// and creates a new database at outPath. lines may be nil. The database is
// written to a temporary file first and renamed at the end, so an existing
// file is only replaced by a complete one. It returns the number of places
// per category.
func Import(cfg *categories.Config, in io.Reader, lines *transit.Lines, outPath string, meta Meta) (map[string]int, error) {
	if meta.Region == "" {
		return nil, errors.New("region is required")
	}
	if _, err := time.Parse(time.DateOnly, meta.DataVersion); err != nil {
		return nil, fmt.Errorf("data version must be YYYY-MM-DD: %w", err)
	}

	tmpPath := outPath + ".tmp"
	if err := os.Remove(tmpPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	counts, err := write(cfg, in, lines, tmpPath, meta)
	if err == nil {
		err = os.Rename(tmpPath, outPath)
	}
	if err != nil {
		os.Remove(tmpPath)
		return nil, err
	}
	return counts, nil
}

func write(cfg *categories.Config, in io.Reader, lines *transit.Lines, path string, meta Meta) (counts map[string]int, err error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := db.Close(); err == nil {
			err = cerr
		}
	}()
	// Pragmas apply per connection, so keep everything on one.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// The R*Tree indexes the category as a third dimension, so a query for
	// one category does not visit the places of all others in the area.
	categoryKeys := make(map[string]int)
	for i, cat := range cfg.Categories {
		categoryKeys[cat.ID] = i + 1
		if _, err := tx.Exec(`INSERT INTO categories (key, id) VALUES (?, ?)`, i+1, cat.ID); err != nil {
			return nil, err
		}
	}

	insertPlace, err := tx.Prepare(`INSERT INTO places
		(osm_type, osm_id, category, name, lat, lon, importance, tags, lines, outline)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, err
	}
	insertBox, err := tx.Prepare(`INSERT INTO places_rtree
		(id, min_lat, max_lat, min_lon, max_lon, min_cat, max_cat) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, err
	}

	counts = make(map[string]int)
	dec := json.NewDecoder(bufio.NewReader(in))
	dec.UseNumber()
	for n := 1; ; n++ {
		var f feature
		if err := dec.Decode(&f); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("feature %d: %w", n, err)
		}
		p, ok, err := toPlace(cfg, lines, f)
		if err != nil {
			return nil, fmt.Errorf("feature %d: %w", n, err)
		}
		if !ok {
			continue
		}

		tags, err := json.Marshal(p.tags)
		if err != nil {
			return nil, err
		}
		var name, placeLines, placeOutline any
		if p.name != "" {
			name = p.name
		}
		if len(p.lines) > 0 {
			b, err := json.Marshal(p.lines)
			if err != nil {
				return nil, err
			}
			placeLines = string(b)
		}
		if p.outline != nil {
			placeOutline = p.outline
		}
		res, err := insertPlace.Exec(p.osmType, p.osmID, p.category, name, p.lat, p.lon,
			p.importance, string(tags), placeLines, placeOutline)
		if err != nil {
			return nil, err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return nil, err
		}
		key := categoryKeys[p.category]
		if _, err := insertBox.Exec(id, p.minLat, p.maxLat, p.minLon, p.maxLon, key, key); err != nil {
			return nil, err
		}
		counts[p.category]++
	}

	for key, value := range map[string]string{
		"data_version": meta.DataVersion,
		"region":       meta.Region,
		"source":       source,
	} {
		if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, key, value); err != nil {
			return nil, err
		}
	}
	return counts, tx.Commit()
}
