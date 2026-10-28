package server

import (
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/ls-itsolutions/open-property-location/internal/categories"
	"github.com/ls-itsolutions/open-property-location/internal/transit"
)

type openAPISchema struct {
	Enum       []string       `yaml:"enum"`
	Required   []string       `yaml:"required"`
	Properties map[string]any `yaml:"properties"`
}

// TestOpenAPI keeps openapi.yaml in sync with the response types and
// categories.yaml.
func TestOpenAPI(t *testing.T) {
	data, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Components struct {
			Schemas map[string]openAPISchema `yaml:"schemas"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec.Components.Schemas

	for name, v := range map[string]any{
		"SurroundingsResponse": surroundingsResponse{},
		"Location":             location{},
		"CategoryResult":       categoryResult{},
		"Place":                Place{},
		"Address":              Address{},
		"Line":                 transit.Line{},
	} {
		want := jsonFields(reflect.TypeOf(v))
		s := schemas[name]
		if got := slices.Sorted(maps.Keys(s.Properties)); !slices.Equal(got, want) {
			t.Errorf("%s properties = %v, want %v", name, got, want)
		}
		if got := slices.Sorted(slices.Values(s.Required)); !slices.Equal(got, want) {
			t.Errorf("%s required = %v, want %v", name, got, want)
		}
	}

	cats, err := categories.Load()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, c := range cats.Categories {
		ids = append(ids, c.ID)
	}
	if got := schemas["CategoryId"].Enum; !slices.Equal(got, ids) {
		t.Errorf("CategoryId enum = %v, want %v", got, ids)
	}
	presets := slices.Sorted(maps.Keys(cats.Presets))
	if got := slices.Sorted(slices.Values(schemas["Preset"].Enum)); !slices.Equal(got, presets) {
		t.Errorf("Preset enum = %v, want %v", got, presets)
	}
}

func jsonFields(t reflect.Type) []string {
	var names []string
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
