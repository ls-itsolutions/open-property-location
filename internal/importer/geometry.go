package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// coordinateScale rounds coordinates to 7 decimals (about 1 cm), the
// precision OSM stores.
const coordinateScale = 1e7

type geometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

// point is a GeoJSON position: longitude, latitude.
type point [2]float64

// representativePoint returns the point itself for points and the centroid
// for polygons.
func representativePoint(g geometry) (lat, lon float64, err error) {
	var p point
	switch g.Type {
	case "Point":
		err = json.Unmarshal(g.Coordinates, &p)
	case "Polygon":
		var rings [][]point
		if err = json.Unmarshal(g.Coordinates, &rings); err == nil {
			p, err = centroid([][][]point{rings})
		}
	case "MultiPolygon":
		var polygons [][][]point
		if err = json.Unmarshal(g.Coordinates, &polygons); err == nil {
			p, err = centroid(polygons)
		}
	default:
		return 0, 0, fmt.Errorf("unsupported geometry type %q", g.Type)
	}
	if err != nil {
		return 0, 0, err
	}
	lon, lat = p[0], p[1]
	if math.Abs(lat) > 90 || math.Abs(lon) > 180 {
		return 0, 0, fmt.Errorf("coordinates out of range: %v", p)
	}
	return round(lat), round(lon), nil
}

// centroid returns the area-weighted centroid of a multipolygon, treating
// coordinates as planar. The first ring of each polygon is the outer ring,
// the others are holes. Degenerate shapes fall back to the mean of the
// first ring's vertices.
func centroid(polygons [][][]point) (point, error) {
	if len(polygons) == 0 || len(polygons[0]) == 0 || len(polygons[0][0]) == 0 {
		return point{}, errors.New("empty polygon")
	}
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
		return meanPoint(polygons[0][0]), nil
	}
	return point{origin[0] + sumX/area, origin[1] + sumY/area}, nil
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
