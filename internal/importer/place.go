package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/ls-itsolutions/open-property-location/internal/categories"
)

const (
	importanceWiki       = 0.5
	importanceWebsite    = 0.2
	importanceHours      = 0.1
	importanceAttraction = 0.2
	maxImportance        = 1.0
)

// apiTags are copied to the tags column as-is when present. website and
// wikidata are validated separately.
var apiTags = []string{"addr:street", "addr:housenumber", "addr:postcode", "addr:city", "opening_hours"}

var wikidataPattern = regexp.MustCompile(`^Q[1-9][0-9]*$`)

type feature struct {
	Geometry   geometry       `json:"geometry"`
	Properties map[string]any `json:"properties"`
}

type place struct {
	osmType    string
	osmID      int64
	category   string
	name       string
	lat, lon   float64
	importance float64
	tags       map[string]string
}

// toPlace maps a feature to a place. It reports false for features that do
// not belong to any category or are filtered out.
func toPlace(cfg *categories.Config, f feature) (place, bool, error) {
	tags := make(map[string]string, len(f.Properties))
	for k, v := range f.Properties {
		if s, ok := v.(string); ok && !strings.HasPrefix(k, "@") {
			tags[k] = s
		}
	}
	if access := tags["access"]; access == "private" || access == "no" {
		return place{}, false, nil
	}
	cat, ok := cfg.Match(tags)
	if !ok {
		return place{}, false, nil
	}
	name := strings.TrimSpace(tags["name"])
	if cat.RequireName && name == "" {
		return place{}, false, nil
	}

	osmType, osmID, err := osmRef(f.Properties)
	if err != nil {
		return place{}, false, err
	}
	lat, lon, err := representativePoint(f.Geometry)
	if err != nil {
		return place{}, false, fmt.Errorf("%s/%d: %w", osmType, osmID, err)
	}

	return place{
		osmType:    osmType,
		osmID:      osmID,
		category:   cat.ID,
		name:       name,
		lat:        lat,
		lon:        lon,
		importance: importance(tags),
		tags:       storedTags(tags),
	}, true, nil
}

func osmRef(props map[string]any) (string, int64, error) {
	osmType, _ := props["@type"].(string)
	if osmType != "node" && osmType != "way" && osmType != "relation" {
		return "", 0, errors.New("missing or invalid @type, export with -a type,id")
	}
	num, _ := props["@id"].(json.Number)
	id, err := num.Int64()
	if err != nil || id <= 0 {
		return "", 0, errors.New("missing or invalid @id, export with -a type,id")
	}
	return osmType, id, nil
}

func importance(tags map[string]string) float64 {
	var score float64
	if wikidataPattern.MatchString(tags["wikidata"]) || tags["wikipedia"] != "" {
		score += importanceWiki
	}
	if website(tags) != "" {
		score += importanceWebsite
	}
	if tags["opening_hours"] != "" {
		score += importanceHours
	}
	if tags["tourism"] == "attraction" || tags["historic"] != "" {
		score += importanceAttraction
	}
	return math.Min(score, maxImportance)
}

func storedTags(tags map[string]string) map[string]string {
	stored := make(map[string]string)
	for _, key := range apiTags {
		if v := strings.TrimSpace(tags[key]); v != "" {
			stored[key] = v
		}
	}
	if w := website(tags); w != "" {
		stored["website"] = w
	}
	if q := tags["wikidata"]; wikidataPattern.MatchString(q) {
		stored["wikidata"] = q
	}
	return stored
}

// website returns the first http(s) URL from website or contact:website.
// Other schemes are dropped because clients render the value as a link.
func website(tags map[string]string) string {
	raw := tags["website"]
	if raw == "" {
		raw = tags["contact:website"]
	}
	raw, _, _ = strings.Cut(raw, ";")
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return raw
}
