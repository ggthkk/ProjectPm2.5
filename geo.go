package main

import "math"

func distanceKM(a, b Coordinates) float64 {
	rad := math.Pi / 180
	dLat, dLon := (b.Lat-a.Lat)*rad, (b.Lon-a.Lon)*rad
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Pow(math.Sin(dLon/2), 2)
	return 6371 * 2 * math.Asin(math.Sqrt(min(1, max(0, h))))
}

func nearestDistanceKM(point Coordinates, locations []Coordinates) float64 {
	best := math.Inf(1)
	for _, location := range locations {
		best = min(best, distanceKM(point, location))
	}
	return best
}
