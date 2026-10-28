// Package transit reads the public transport lines serving each stop from
// OSM route and stop_area relations.
package transit

import (
	"cmp"
	"encoding/xml"
	"io"
	"slices"
	"strings"
)

// modes are the route types whose lines are attached to stops, in the order
// the lines of a stop are listed.
var modes = []string{"train", "light_rail", "subway", "tram", "trolleybus", "bus", "ferry"}

// Line is a public transport line serving a stop.
type Line struct {
	Mode string `json:"mode"`
	Ref  string `json:"ref"`
}

type member struct {
	osmType string
	id      int64
}

// Lines maps OSM elements to the lines that serve them.
type Lines struct {
	direct map[member][]Line
	// Lines of the stop areas an element without direct lines belongs to.
	viaStopArea map[member][]Line
}

// FilterExpressions returns the osmium tags-filter expressions that keep the
// relations ReadLines needs.
func FilterExpressions() []string {
	return []string{"r/route=" + strings.Join(modes, ","), "r/public_transport=stop_area"}
}

type osmRelation struct {
	Members []struct {
		Type string `xml:"type,attr"`
		Ref  int64  `xml:"ref,attr"`
		Role string `xml:"role,attr"`
	} `xml:"member"`
	Tags []struct {
		K string `xml:"k,attr"`
		V string `xml:"v,attr"`
	} `xml:"tag"`
}

// ReadLines reads route and stop_area relations in OSM XML, as written by
// `osmium tags-filter --omit-referenced` with FilterExpressions. A stop is
// served by the routes it is a member of. A stop or station that is not a
// member of any route is served by all routes of the stop areas it belongs
// to: routes often list the stop position on the road, not the platform or
// station that is the place.
func ReadLines(r io.Reader) (*Lines, error) {
	lines := &Lines{direct: make(map[member][]Line), viaStopArea: make(map[member][]Line)}
	var stopAreas [][]member
	dec := xml.NewDecoder(r)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok || start.Name.Local != "relation" {
			continue
		}
		var rel osmRelation
		if err := dec.DecodeElement(&rel, &start); err != nil {
			return nil, err
		}
		tags := make(map[string]string, len(rel.Tags))
		for _, t := range rel.Tags {
			tags[t.K] = t.V
		}
		switch {
		case tags["public_transport"] == "stop_area":
			var members []member
			for _, m := range rel.Members {
				members = append(members, member{m.Type, m.Ref})
			}
			stopAreas = append(stopAreas, members)
		case slices.Contains(modes, tags["route"]):
			ref := strings.TrimSpace(tags["ref"])
			if ref == "" {
				continue
			}
			line := Line{Mode: tags["route"], Ref: ref}
			for _, m := range rel.Members {
				// The other way members are the roads or tracks of the route.
				if m.Type == "node" || strings.HasPrefix(m.Role, "platform") || strings.HasPrefix(m.Role, "stop") {
					key := member{m.Type, m.Ref}
					lines.direct[key] = append(lines.direct[key], line)
				}
			}
		}
	}

	for _, members := range stopAreas {
		var union []Line
		for _, m := range members {
			union = append(union, lines.direct[m]...)
		}
		union = Sort(union)
		for _, m := range members {
			if len(lines.direct[m]) == 0 {
				lines.viaStopArea[m] = append(lines.viaStopArea[m], union...)
			}
		}
	}
	return lines, nil
}

// For returns the lines serving an element, sorted and without duplicates.
func (l *Lines) For(osmType string, id int64) []Line {
	if l == nil {
		return nil
	}
	m := member{osmType, id}
	if direct := l.direct[m]; len(direct) > 0 {
		return Sort(direct)
	}
	return Sort(l.viaStopArea[m])
}

// Sort returns the lines sorted by mode and then by ref, shorter refs first
// so that "S2" comes before "S10", without duplicates (routes often exist
// once per direction).
func Sort(lines []Line) []Line {
	lines = slices.Clone(lines)
	slices.SortFunc(lines, compareLines)
	return slices.Compact(lines)
}

func compareLines(a, b Line) int {
	return cmp.Or(
		cmp.Compare(slices.Index(modes, a.Mode), slices.Index(modes, b.Mode)),
		cmp.Compare(len(a.Ref), len(b.Ref)),
		cmp.Compare(a.Ref, b.Ref),
	)
}
