package categories

import (
	"slices"
	"strings"
	"testing"
)

func TestLoadEmbedded(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, preset := range []string{"stay", "living"} {
		if len(cfg.Presets[preset]) == 0 {
			t.Errorf("preset %s missing", preset)
		}
	}
	if cat, ok := cfg.Get("hospital"); !ok || cat.MaxDistanceMeters != 15000 || !cat.RequireName || cat.CollapseSameName {
		t.Errorf("hospital = %+v, %v", cat, ok)
	}
	if cat, ok := cfg.Get("bus_tram_stop"); !ok || !cat.CollapseSameName {
		t.Error("bus_tram_stop should collapse places with the same name")
	}
}

func TestMatch(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		tags map[string]string
		want string
	}{
		{map[string]string{"amenity": "pub"}, "bar"},
		{map[string]string{"railway": "halt"}, "train_station"},
		{map[string]string{"historic": "castle"}, "attraction"},
		{map[string]string{"shop": "bakery", "amenity": "cafe"}, "cafe"},
		{map[string]string{"amenity": "bench"}, ""},
		{map[string]string{"historic": "wayside_cross"}, ""},
		{map[string]string{}, ""},
	}
	for _, tt := range tests {
		got := ""
		if cat, ok := cfg.Match(tt.tags); ok {
			got = cat.ID
		}
		if got != tt.want {
			t.Errorf("Match(%v) = %q, want %q", tt.tags, got, tt.want)
		}
	}
}

func TestFilterExpressions(t *testing.T) {
	cfg, err := Parse([]byte(`
categories:
  - id: bar
    match:
      - amenity=bar,pub
    requireName: true
    maxDistanceMeters: 100
  - id: sight
    match:
      - tourism=museum
      - historic=castle
    requireName: false
    maxDistanceMeters: 100
`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"nwr/amenity=bar,pub", "nwr/tourism=museum", "nwr/historic=castle"}
	if got := cfg.FilterExpressions(); !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	const valid = `
  - id: bar
    match:
      - amenity=bar
    requireName: true
    maxDistanceMeters: 100
`
	tests := map[string]string{
		"unknown field":     "categories:" + valid + "    radius: 5\n",
		"duplicate id":      "categories:" + valid + valid,
		"bad id":            "categories:" + strings.Replace(valid, "id: bar", "id: Bar", 1),
		"no distance":       "categories:" + strings.Replace(valid, "maxDistanceMeters: 100", "", 1),
		"matcher no value":  "categories:" + strings.Replace(valid, "amenity=bar", "amenity", 1),
		"matcher empty val": "categories:" + strings.Replace(valid, "amenity=bar", "amenity=bar,", 1),
		"matcher space":     "categories:" + strings.Replace(valid, "amenity=bar", `"amenity=bar pub"`, 1),
		"unknown preset id": "categories:" + valid + "presets:\n  p: [cafe]\n",
		"duplicate preset":  "categories:" + valid + "presets:\n  p: [bar, bar]\n",
		"empty preset":      "categories:" + valid + "presets:\n  p: []\n",
		"no categories":     "categories: []\n",
	}
	for name, input := range tests {
		if _, err := Parse([]byte(input)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
