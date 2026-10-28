// Package outline stores the rings of an area in a compact binary form:
// per ring the number of points, then the points as zigzag varint deltas of
// longitude and latitude in units of 1e-7 degrees.
package outline

import (
	"encoding/binary"
	"errors"
	"math"
)

// scale is the precision OSM stores coordinates in (about 1 cm).
const scale = 1e7

// Ring is a closed ring of [lon, lat] positions, as in GeoJSON.
type Ring [][2]float64

// Encode returns the binary form of the rings.
func Encode(rings []Ring) []byte {
	var buf []byte
	var prevLon, prevLat int64
	for _, ring := range rings {
		buf = binary.AppendUvarint(buf, uint64(len(ring)))
		for _, p := range ring {
			lon, lat := int64(math.Round(p[0]*scale)), int64(math.Round(p[1]*scale))
			buf = binary.AppendVarint(buf, lon-prevLon)
			buf = binary.AppendVarint(buf, lat-prevLat)
			prevLon, prevLat = lon, lat
		}
	}
	return buf
}

var errCorrupt = errors.New("corrupt outline")

// Decode parses the output of Encode.
func Decode(buf []byte) ([]Ring, error) {
	var rings []Ring
	var lon, lat int64
	for len(buf) > 0 {
		n, k := binary.Uvarint(buf)
		// Every point takes at least two bytes.
		if k <= 0 || n > uint64(len(buf)-k)/2 {
			return nil, errCorrupt
		}
		buf = buf[k:]
		ring := make(Ring, n)
		for i := range ring {
			dLon, k1 := binary.Varint(buf)
			if k1 <= 0 {
				return nil, errCorrupt
			}
			dLat, k2 := binary.Varint(buf[k1:])
			if k2 <= 0 {
				return nil, errCorrupt
			}
			buf = buf[k1+k2:]
			lon, lat = lon+dLon, lat+dLat
			ring[i] = [2]float64{float64(lon) / scale, float64(lat) / scale}
		}
		rings = append(rings, ring)
	}
	return rings, nil
}
