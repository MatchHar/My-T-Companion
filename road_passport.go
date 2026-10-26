package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Administrative identity comes from saved TeslaMate addresses. These are
// recorded trip endpoints, not an inference that every crossed city was visited.
type roadPassportVisit struct {
	DriveID    int64     `json:"drive_id"`
	At         time.Time `json:"at"`
	DistanceKM *float64  `json:"distance_km,omitempty"`
}
type roadPassportRegion struct {
	ID      string              `json:"id"`
	Kind    string              `json:"kind"`
	Name    string              `json:"name"`
	Country string              `json:"country"`
	State   string              `json:"state"`
	City    string              `json:"city"`
	First   time.Time           `json:"first_recorded_at"`
	Last    time.Time           `json:"last_recorded_at"`
	Visits  []roadPassportVisit `json:"visits"`
	Center  *[2]float64         `json:"center,omitempty"`
	// Region area is deliberately absent: endpoints cannot establish admin borders.
}
type roadPassportData struct {
	Version   int                  `json:"version"`
	Basis     string               `json:"basis"`
	Complete  bool                 `json:"complete"`
	LongestKM *float64             `json:"longest_drive_km,omitempty"`
	Regions   []roadPassportRegion `json:"regions"`
}
type roadPassportEndpoint struct {
	drive                int64
	at                   time.Time
	distance             *float64
	country, state, city string
	addressID            int64
	point                *[2]float64
}
type roadPassportGroup struct {
	region roadPassportRegion
	visits map[int64]roadPassportVisit
	points [][2]float64
}

func passportIdentity(kind, country, state, city string, addressID int64) string {
	fields := []string{kind, strings.ToLower(strings.TrimSpace(country)), strings.ToLower(strings.TrimSpace(state)), strings.ToLower(strings.TrimSpace(city))}
	// Unknown administrative context cannot merge identically named places.
	if country == "" {
		fields = append(fields, strconv.FormatInt(addressID, 10))
	}
	return base64.RawURLEncoding.EncodeToString([]byte(strings.Join(fields, "\x00")))
}
func buildRoadPassport(points []roadPassportEndpoint, longest *float64, complete bool) *roadPassportData {
	groups := map[string]*roadPassportGroup{}
	for _, p := range points {
		p.country = strings.TrimSpace(p.country)
		p.state = strings.TrimSpace(p.state)
		p.city = strings.TrimSpace(p.city)
		for _, kind := range []string{"country", "state", "city"} {
			country, state, city, name := p.country, "", "", p.country
			if kind == "state" {
				state = p.state
				name = state
			}
			if kind == "city" {
				state = p.state
				city = p.city
				name = city
			}
			if name == "" || p.drive <= 0 || p.at.IsZero() {
				continue
			}
			key := passportIdentity(kind, country, state, city, p.addressID)
			g := groups[key]
			if g == nil {
				g = &roadPassportGroup{region: roadPassportRegion{ID: key, Kind: kind, Name: name, Country: country, State: state, City: city, First: p.at, Last: p.at}, visits: map[int64]roadPassportVisit{}}
				groups[key] = g
			}
			if p.at.Before(g.region.First) {
				g.region.First = p.at
			}
			if p.at.After(g.region.Last) {
				g.region.Last = p.at
			}
			// Both ends in the same region count once, at the latest recorded endpoint.
			v, ok := g.visits[p.drive]
			if !ok || p.at.After(v.At) {
				g.visits[p.drive] = roadPassportVisit{p.drive, p.at, p.distance}
			}
			if p.point != nil {
				g.points = append(g.points, *p.point)
			}
		}
	}
	result := &roadPassportData{Version: 1, Basis: "drive_endpoints", Complete: complete, LongestKM: longest, Regions: []roadPassportRegion{}}
	for _, g := range groups {
		for _, v := range g.visits {
			g.region.Visits = append(g.region.Visits, v)
		}
		sort.Slice(g.region.Visits, func(i, j int) bool {
			a, b := g.region.Visits[i], g.region.Visits[j]
			if a.At.Equal(b.At) {
				return a.DriveID > b.DriveID
			}
			return a.At.After(b.At)
		})
		if len(g.points) > 0 { // circular longitude mean remains near the dateline
			lat, x, y := 0.0, 0.0, 0.0
			for _, p := range g.points {
				lat += p[0]
				x += math.Cos(p[1] * math.Pi / 180)
				y += math.Sin(p[1] * math.Pi / 180)
			}
			c := [2]float64{lat / float64(len(g.points)), math.Atan2(y, x) * 180 / math.Pi}
			g.region.Center = &c
		}
		result.Regions = append(result.Regions, g.region)
	}
	sort.Slice(result.Regions, func(i, j int) bool { return result.Regions[i].ID < result.Regions[j].ID })
	return result
}

const roadPassportEndpointsSQL = `SELECT d.id, d.distance::float8, e.at, coalesce(a.country,''), coalesce(a.state,''), coalesce(a.city,''), a.id, p.latitude::float8, p.longitude::float8
FROM drives d CROSS JOIN LATERAL (VALUES (d.start_address_id,d.start_position_id,d.start_date),(d.end_address_id,d.end_position_id,coalesce(d.end_date,d.start_date))) e(address_id,position_id,at)
JOIN addresses a ON a.id=e.address_id LEFT JOIN positions p ON p.id=e.position_id
WHERE d.car_id=$1` + roadCoverageRangeSQL + ` ORDER BY d.id DESC,e.at DESC,a.id LIMIT 50001`

func fetchRoadPassport(ctx context.Context, database *sql.DB, q roadCoverageRequest) *roadPassportData {
	args := []any{q.CarID, nullableTime(q.From), nullableTime(q.To)}
	var longest sql.NullFloat64
	if database.QueryRowContext(ctx, `SELECT max(d.distance)::float8 FROM drives d WHERE d.car_id=$1 AND d.end_date IS NOT NULL`+roadCoverageRangeSQL, args...).Scan(&longest) != nil {
		return nil
	}
	rows, err := database.QueryContext(ctx, roadPassportEndpointsSQL, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	points := []roadPassportEndpoint{}
	complete := true
	for rows.Next() {
		if len(points) >= 50000 {
			complete = false
			break
		}
		var p roadPassportEndpoint
		var distance, lat, lon sql.NullFloat64
		if rows.Scan(&p.drive, &distance, &p.at, &p.country, &p.state, &p.city, &p.addressID, &lat, &lon) != nil {
			return nil
		}
		if distance.Valid && !math.IsNaN(distance.Float64) && !math.IsInf(distance.Float64, 0) && distance.Float64 >= 0 {
			v := distance.Float64
			p.distance = &v
		}
		p.at = p.at.UTC()
		if lat.Valid && lon.Valid && validRoadPoint(lat.Float64, lon.Float64) {
			v := [2]float64{lat.Float64, lon.Float64}
			p.point = &v
		}
		points = append(points, p)
	}
	if rows.Err() != nil {
		return nil
	}
	var max *float64
	if longest.Valid && longest.Float64 >= 0 && !math.IsNaN(longest.Float64) && !math.IsInf(longest.Float64, 0) {
		v := longest.Float64
		max = &v
	}
	return buildRoadPassport(points, max, complete)
}
