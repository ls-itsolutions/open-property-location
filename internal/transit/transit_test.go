package transit

import (
	"slices"
	"strings"
	"testing"
)

const routes = `<?xml version='1.0' encoding='UTF-8'?>
<osm version="0.6">
  <relation id="1">
    <member type="node" ref="1" role="platform"/>
    <member type="way" ref="2" role="platform"/>
    <member type="way" ref="3" role=""/>
    <member type="node" ref="4" role="stop"/>
    <tag k="route" v="bus"/>
    <tag k="ref" v=" 10 "/>
  </relation>
  <relation id="2">
    <member type="node" ref="1" role=""/>
    <tag k="route" v="tram"/>
    <tag k="ref" v="2"/>
  </relation>
  <relation id="3">
    <member type="node" ref="1" role="platform"/>
    <tag k="route" v="bus"/>
    <tag k="ref" v="10"/>
  </relation>
  <relation id="4">
    <member type="node" ref="1" role="platform"/>
    <tag k="route" v="bus"/>
  </relation>
  <relation id="5">
    <member type="node" ref="1" role=""/>
    <tag k="route" v="hiking"/>
    <tag k="ref" v="E1"/>
  </relation>
  <relation id="6">
    <member type="node" ref="4" role="stop"/>
    <member type="node" ref="5" role=""/>
    <member type="node" ref="7" role="platform"/>
    <tag k="public_transport" v="stop_area"/>
  </relation>
  <relation id="8">
    <member type="node" ref="7" role="platform"/>
    <tag k="route" v="tram"/>
    <tag k="ref" v="3"/>
  </relation>
  <relation id="7">
    <member type="node" ref="5" role=""/>
    <member type="node" ref="6" role=""/>
    <tag k="public_transport" v="stop_area"/>
  </relation>
</osm>`

func TestReadLines(t *testing.T) {
	lines, err := ReadLines(strings.NewReader(routes))
	if err != nil {
		t.Fatal(err)
	}
	bus10 := Line{Mode: "bus", Ref: "10"}
	tests := []struct {
		osmType string
		id      int64
		want    []Line
	}{
		// Duplicates removed, routes without ref and non-transit routes ignored.
		{"node", 1, []Line{{Mode: "tram", Ref: "2"}, bus10}},
		{"way", 2, []Line{bus10}},
		// The road of the route.
		{"way", 3, nil},
		// No own routes: all lines of the stop area.
		{"node", 5, []Line{{Mode: "tram", Ref: "3"}, bus10}},
		// Own routes: only those, not the other lines of the stop area.
		{"node", 4, []Line{bus10}},
		{"node", 7, []Line{{Mode: "tram", Ref: "3"}}},
		// Stop areas do not chain: node 5 has no own route.
		{"node", 6, nil},
		{"node", 99, nil},
	}
	for _, tt := range tests {
		if got := lines.For(tt.osmType, tt.id); !slices.Equal(got, tt.want) {
			t.Errorf("%s/%d: %v, want %v", tt.osmType, tt.id, got, tt.want)
		}
	}
	var none *Lines
	if got := none.For("node", 1); got != nil {
		t.Errorf("nil Lines returned %v", got)
	}
}

func TestReadLinesInvalid(t *testing.T) {
	if _, err := ReadLines(strings.NewReader(`<osm><relation id="1"><member ref="x"/></relation></osm>`)); err == nil {
		t.Error("expected error for an invalid member ref")
	}
}

func TestSort(t *testing.T) {
	in := []Line{{"bus", "S10"}, {"ferry", "F1"}, {"bus", "100"}, {"train", "RE7"}, {"bus", "S2"}, {"bus", "100"}, {"bus", "4"}}
	want := []Line{{"train", "RE7"}, {"bus", "4"}, {"bus", "S2"}, {"bus", "100"}, {"bus", "S10"}, {"ferry", "F1"}}
	if got := Sort(in); !slices.Equal(got, want) {
		t.Errorf("Sort = %v, want %v", got, want)
	}
}
