// Package categories loads the category definitions from categories.yaml and
// maps OSM tags to categories.
package categories

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

//go:embed categories.yaml
var embedded []byte

var (
	idPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	tagPattern = regexp.MustCompile(`^[A-Za-z0-9_:.-]+$`)
)

type Category struct {
	ID                string
	RequireName       bool
	MaxDistanceMeters int
	CollapseSameName  bool
	matchers          []matcher
}

type matcher struct {
	key    string
	values []string
	raw    string
}

func (m matcher) matches(tags map[string]string) bool {
	v, ok := tags[m.key]
	if !ok {
		return false
	}
	for _, want := range m.values {
		if v == want {
			return true
		}
	}
	return false
}

// Config holds all categories in file order and the presets.
type Config struct {
	Categories []*Category
	Presets    map[string][]string
	byID       map[string]*Category
}

type fileFormat struct {
	Categories []struct {
		ID                string   `yaml:"id"`
		Match             []string `yaml:"match"`
		RequireName       bool     `yaml:"requireName"`
		MaxDistanceMeters int      `yaml:"maxDistanceMeters"`
		CollapseSameName  bool     `yaml:"collapseSameName"`
	} `yaml:"categories"`
	Presets map[string][]string `yaml:"presets"`
}

// Load parses the embedded categories.yaml.
func Load() (*Config, error) {
	return Parse(embedded)
}

// Parse parses and validates a categories file.
func Parse(data []byte) (*Config, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var f fileFormat
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse categories: %w", err)
	}
	if len(f.Categories) == 0 {
		return nil, errors.New("no categories defined")
	}

	cfg := &Config{Presets: f.Presets, byID: make(map[string]*Category)}
	for _, c := range f.Categories {
		if !idPattern.MatchString(c.ID) {
			return nil, fmt.Errorf("invalid category id %q", c.ID)
		}
		if _, dup := cfg.byID[c.ID]; dup {
			return nil, fmt.Errorf("duplicate category id %q", c.ID)
		}
		if c.MaxDistanceMeters <= 0 {
			return nil, fmt.Errorf("category %s: maxDistanceMeters must be positive", c.ID)
		}
		if len(c.Match) == 0 {
			return nil, fmt.Errorf("category %s: no matchers", c.ID)
		}
		cat := &Category{
			ID:                c.ID,
			RequireName:       c.RequireName,
			MaxDistanceMeters: c.MaxDistanceMeters,
			CollapseSameName:  c.CollapseSameName,
		}
		for _, raw := range c.Match {
			m, err := parseMatcher(raw)
			if err != nil {
				return nil, fmt.Errorf("category %s: %w", c.ID, err)
			}
			cat.matchers = append(cat.matchers, m)
		}
		cfg.Categories = append(cfg.Categories, cat)
		cfg.byID[c.ID] = cat
	}

	for name, ids := range cfg.Presets {
		if !idPattern.MatchString(name) {
			return nil, fmt.Errorf("invalid preset name %q", name)
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("preset %s is empty", name)
		}
		seen := make(map[string]bool)
		for _, id := range ids {
			if _, ok := cfg.byID[id]; !ok {
				return nil, fmt.Errorf("preset %s: unknown category %q", name, id)
			}
			if seen[id] {
				return nil, fmt.Errorf("preset %s: duplicate category %q", name, id)
			}
			seen[id] = true
		}
	}
	return cfg, nil
}

func parseMatcher(raw string) (matcher, error) {
	key, list, ok := strings.Cut(raw, "=")
	if !ok || !tagPattern.MatchString(key) {
		return matcher{}, fmt.Errorf("invalid matcher %q, want key=value[,value...]", raw)
	}
	values := strings.Split(list, ",")
	for _, v := range values {
		if !tagPattern.MatchString(v) {
			return matcher{}, fmt.Errorf("invalid value %q in matcher %q", v, raw)
		}
	}
	return matcher{key: key, values: values, raw: raw}, nil
}

// Get returns the category with the given id.
func (c *Config) Get(id string) (*Category, bool) {
	cat, ok := c.byID[id]
	return cat, ok
}

// Match returns the first category whose matchers fit the tags.
func (c *Config) Match(tags map[string]string) (*Category, bool) {
	for _, cat := range c.Categories {
		for _, m := range cat.matchers {
			if m.matches(tags) {
				return cat, true
			}
		}
	}
	return nil, false
}

// FilterExpressions returns one osmium tags-filter expression per matcher,
// applied to nodes, ways and relations.
func (c *Config) FilterExpressions() []string {
	var exprs []string
	for _, cat := range c.Categories {
		for _, m := range cat.matchers {
			exprs = append(exprs, "nwr/"+m.raw)
		}
	}
	return exprs
}
