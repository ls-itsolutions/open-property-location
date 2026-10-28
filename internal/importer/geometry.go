package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/ls-itsolutions/open-property-location/internal/outline"
)

// coordinateScale rounds coordinates to 7 decimals (about 1 cm), the
// precision OSM stores.
const coordinateScale = 1e7

type geometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

// point is a GeoJSON position: longitude, latitude.
type point = [2]float64

// shape is a parsed point or area.
type shape struct {
	point    point
	polygons [][][]point // nil for points
}

func parseGeometry(g geometry) (shape, error) {
	var s shape
	var err error
	switch g.Type {
	case "Point":
		err = json.Unmarshal(g.Coordinates, &s.point)
	case "Polygon":
		var rings [][]point
		if err = json.Unmarshal(g.Coordinates, &rings); err == nil {
			s.polygons = [][][]point{rings}
		}
	case "MultiPolygon":
		err = json.Unmarshal(g.Coordinates, &s.polygons)
	default:
		return shape{}, fmt.Errorf("unsupported geometry type %q", g.Type)
	}
	if err != nil {
		return shape{}, err
	}
	if g.Type == "Point" {
		return s, checkRange(s.point)
	}
	if len(s.polygons) == 0 || len(s.polygons[0]) == 0 || len(s.polygons[0][0]) == 0 {
		return shape{}, errors.New("empty polygon")
	}
	for _, rings := range s.polygons {
		for _, ring := range rings {
			for _, p := range ring {
				if err := checkRange(p); err != nil {
					return shape{}, err
				}
			}
		}
	}
	return s, nil
}

func checkRange(p point) error {
	if math.Abs(p[1]) > 90 || math.Abs(p[0]) > 180 {
		return fmt.Errorf("coordinates out of range: %v", p)
	}
	return nil
}

// representativePoint returns the point itself for points and the centroid
// for areas.
func (s shape) representativePoint() (lat, lon float64) {
	p := s.point
	if s.polygons != nil {
		p = centroid(s.polygons)
	}
	return round(p[1]), round(p[0])
}

// bounds returns the bounding box of all vertices of an area.
func (s shape) bounds() (minLat, maxLat, minLon, maxLon float64) {
	minLat, maxLat, minLon, maxLon = 90, -90, 180, -180
	for _, rings := range s.polygons {
		for _, ring := range rings {
			for _, p := range ring {
				minLat, maxLat = math.Min(minLat, p[1]), math.Max(maxLat, p[1])
				minLon, maxLon = math.Min(minLon, p[0]), math.Max(maxLon, p[0])
			}
		}
	}
	return minLat, maxLat, minLon, maxLon
}

// rings returns all rings of all polygons. Distances and point-in-area tests
// with the even-odd rule do not need to know which ring is a hole.
func (s shape) rings() []outline.Ring {
	var rings []outline.Ring
	for _, polygon := range s.polygons {
		for _, ring := range polygon {
			rings = append(rings, ring)
		}
	}
	return rings
}

// centroid returns the area-weighted centroid of a multipolygon, treating
// coordinates as planar. The first ring of each polygon is the outer ring,
// the others are holes. Degenerate shapes fall back to the mean of the
// first ring's vertices. polygons must not be empty.
func centroid(polygons [][][]point) point {
	// Coordinates relative to a nearby origin keep the cross products small.
	origin := polygons[0][0][0]
	var area, sumX, sumY float64
	for _, rings := range polygons {
		for i, ring := range rings {
			a, x, y := ringCentroid(ring, origin)
			w := math.Abs(a)
			if i > 0 {
				w = -w
			}
			area += w
			sumX += w * x
			sumY += w * y
		}
	}
	if area <= 0 {
		return meanPoint(polygons[0][0])
	}
	return point{origin[0] + sumX/area, origin[1] + sumY/area}
}

// ringCentroid returns the signed area and the centroid of a closed ring,
// relative to origin (shoelace formula).
func ringCentroid(ring []point, origin point) (area, x, y float64) {
	var cross2, sumX, sumY float64
	for i := 0; i+1 < len(ring); i++ {
		x0, y0 := ring[i][0]-origin[0], ring[i][1]-origin[1]
		x1, y1 := ring[i+1][0]-origin[0], ring[i+1][1]-origin[1]
		c := x0*y1 - x1*y0
		cross2 += c
		sumX += (x0 + x1) * c
		sumY += (y0 + y1) * c
	}
	if cross2 == 0 {
		return 0, 0, 0
	}
	return cross2 / 2, sumX / (3 * cross2), sumY / (3 * cross2)
}

func meanPoint(points []point) point {
	if n := len(points); n > 1 && points[0] == points[n-1] {
		points = points[:n-1]
	}
	var m point
	for _, p := range points {
		m[0] += p[0]
		m[1] += p[1]
	}
	n := float64(len(points))
	return point{m[0] / n, m[1] / n}
}

func round(v float64) float64 {
	return math.Round(v*coordinateScale) / coordinateScale
}
