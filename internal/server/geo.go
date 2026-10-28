package server

import "math"

const (
	earthRadiusMeters = 6371008.8

	// Walking time is an estimate: straight-line distance times a detour
	// factor, at 80 m/min (about 4.8 km/h).
	walkingDetourFactor    = 1.3
	walkingMetersPerMinute = 80.0
)

func radians(deg float64) float64 { return deg * math.Pi / 180 }
func degrees(rad float64) float64 { return rad * 180 / math.Pi }

// haversine returns the great-circle distance in meters.
func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	dLat := radians(lat2 - lat1)
	dLon := radians(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(radians(lat1))*math.Cos(radians(lat2))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusMeters * math.Asin(math.Min(1, math.Sqrt(a)))
}

func walkingMinutes(distanceMeters float64) int {
	return int(math.Ceil(distanceMeters * walkingDetourFactor / walkingMetersPerMinute))
}

// rankScore orders places within a category: lower is better. Important
// places (Wikidata entry, website, ...) may be a bit farther away than
// unimportant ones and still rank higher.
func rankScore(distanceMeters, importance float64) float64 {
	return distanceMeters / (1 + importance)
}

// boundingBox returns a box that contains every point within radius meters
// of lat/lon. Boxes crossing the antimeridian are not wrapped.
func boundingBox(lat, lon, radiusMeters float64) (minLat, maxLat, minLon, maxLon float64) {
	d := radiusMeters / earthRadiusMeters
	minLat, maxLat = lat-degrees(d), lat+degrees(d)
	if minLat <= -90 || maxLat >= 90 {
		return math.Max(minLat, -90), math.Min(maxLat, 90), -180, 180
	}
	dLon := degrees(math.Asin(math.Sin(d) / math.Cos(radians(lat))))
	return minLat, maxLat, lon - dLon, lon + dLon
}
