package main

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// Road coverage ("足跡地圖") is a read-only aggregate of TeslaMate drive
// positions. Raw positions never leave the server: trails are decimated to
// roughly one point per roadCoverageSpacingM metres, and visit heat is counted
// per ~200 m grid cell as the number of distinct drives touching that cell.
const (
	roadCoverageCapability   = "road_coverage_v1"
	roadCoverageCellM        = 200.0
	roadCoverageSpacingM     = 60.0
	roadCoverageMaxPoints    = 80000
	roadCoverageQueryTimeout = 45 * time.Second
	roadCoverageAnchorMin    = 3
)

var roadCoveragePath = regexp.MustCompile(`^/api/v1/cars/(\d+)/road-coverage$`)

type roadCoveragePoint struct {
	DriveID  int64
	Lat, Lon float64
}

type roadCoverageCell struct {
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
	Visits int     `json:"visits"`
}

type roadCoverageTrail struct {
	DriveID int64        `json:"drive_id"`
	Mode    string       `json:"mode"` // reserved for autopilot/FSD; always "unknown" today
	Points  [][2]float64 `json:"points"`
	Heat    []int        `json:"heat"` // visits of the cell each point falls in
}

type roadCoverageCity struct {
	City   string `json:"city"`
	Drives int    `json:"drives"`
}

type roadCoverageData struct {
	CarID      int                 `json:"car_id"`
	From       *time.Time          `json:"from"`
	To         *time.Time          `json:"to"`
	CellMeters float64             `json:"cell_m"`
	DriveCount int                 `json:"drive_count"`
	DistanceKM float64             `json:"distance_km"`
	FirstDrive *time.Time          `json:"first_drive_at"`
	LastDrive  *time.Time          `json:"last_drive_at"`
	Cells      []roadCoverageCell  `json:"cells"`
	Trails     []roadCoverageTrail `json:"trails"`
	Cities     []roadCoverageCity  `json:"cities"`
	Anchors    []roadCoverageCell  `json:"private_anchors"`
	Truncated  bool                `json:"truncated"`
}

type roadCellKey struct{ Y, X int64 }

func roadCellFor(lat, lon, cellM float64) roadCellKey {
	latStep := cellM / 111320.0
	// Longitude step uses the latitude band so cells stay roughly square.
	band := math.Floor(lat / latStep)
	center := (band + 0.5) * latStep
	lonStep := cellM / (111320.0 * math.Max(math.Cos(center*math.Pi/180), 0.01))
	return roadCellKey{int64(band), int64(math.Floor(lon / lonStep))}
}

func roadCellCenter(k roadCellKey, cellM float64) (float64, float64) {
	latStep := cellM / 111320.0
	lat := (float64(k.Y) + 0.5) * latStep
	lonStep := cellM / (111320.0 * math.Max(math.Cos(lat*math.Pi/180), 0.01))
	return lat, (float64(k.X) + 0.5) * lonStep
}

func haversineM(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := p2-p1, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

func round5(v float64) float64 { return math.Round(v*1e5) / 1e5 }

// buildRoadCoverage expects points ordered by drive then time.
func buildRoadCoverage(points []roadCoveragePoint, cellM, spacingM float64, maxPoints int) ([]roadCoverageCell, []roadCoverageTrail, bool) {
	visits := map[roadCellKey]int{}
	seen := map[roadCellKey]int64{}
	type raw struct {
		drive int64
		pts   [][2]float64
		cells []roadCellKey
	}
	var drives []raw
	var cur *raw
	var lastLat, lastLon float64
	for _, p := range points {
		if !validRoadPoint(p.Lat, p.Lon) {
			continue
		}
		k := roadCellFor(p.Lat, p.Lon, cellM)
		if seen[k] != p.DriveID+1 {
			seen[k] = p.DriveID + 1
			visits[k]++
		}
		if cur == nil || cur.drive != p.DriveID {
			drives = append(drives, raw{drive: p.DriveID})
			cur = &drives[len(drives)-1]
			cur.pts = append(cur.pts, [2]float64{round5(p.Lat), round5(p.Lon)})
			cur.cells = append(cur.cells, k)
			lastLat, lastLon = p.Lat, p.Lon
			continue
		}
		if haversineM(lastLat, lastLon, p.Lat, p.Lon) >= spacingM {
			cur.pts = append(cur.pts, [2]float64{round5(p.Lat), round5(p.Lon)})
			cur.cells = append(cur.cells, k)
			lastLat, lastLon = p.Lat, p.Lon
		}
	}
	total := 0
	for _, d := range drives {
		total += len(d.pts)
	}
	stride, truncated := 1, false
	if maxPoints > 0 && total > maxPoints {
		stride = int(math.Ceil(float64(total) / float64(maxPoints)))
		truncated = true
	}
	trails := make([]roadCoverageTrail, 0, len(drives))
	for _, d := range drives {
		t := roadCoverageTrail{DriveID: d.drive, Mode: "unknown", Points: [][2]float64{}, Heat: []int{}}
		for i := range d.pts {
			if i%stride != 0 && i != len(d.pts)-1 {
				continue
			}
			t.Points = append(t.Points, d.pts[i])
			t.Heat = append(t.Heat, visits[d.cells[i]])
		}
		if len(t.Points) >= 2 {
			trails = append(trails, t)
		}
	}
	cells := make([]roadCoverageCell, 0, len(visits))
	for k, v := range visits {
		lat, lon := roadCellCenter(k, cellM)
		cells = append(cells, roadCoverageCell{Lat: round5(lat), Lon: round5(lon), Visits: v})
	}
	sortRoadCells(cells)
	return cells, trails, truncated
}

func sortRoadCells(cells []roadCoverageCell) {
	// Deterministic output: most visited first, then by position.
	for i := 1; i < len(cells); i++ {
		for j := i; j > 0 && roadCellLess(cells[j], cells[j-1]); j-- {
			cells[j], cells[j-1] = cells[j-1], cells[j]
		}
	}
}

func roadCellLess(a, b roadCoverageCell) bool {
	if a.Visits != b.Visits {
		return a.Visits > b.Visits
	}
	if a.Lat != b.Lat {
		return a.Lat < b.Lat
	}
	return a.Lon < b.Lon
}

func validRoadPoint(lat, lon float64) bool {
	if math.IsNaN(lat) || math.IsNaN(lon) || math.Abs(lat) > 90 || math.Abs(lon) > 180 {
		return false
	}
	return !(math.Abs(lat) < 0.0001 && math.Abs(lon) < 0.0001)
}

type roadCoverageRequest struct {
	CarID    int
	From, To *time.Time
}

func parseRoadCoverageRequest(car string, values url.Values) (roadCoverageRequest, error) {
	var q roadCoverageRequest
	id, err := strconv.ParseInt(car, 10, 32)
	if err != nil || id <= 0 {
		return q, errors.New("Invalid car id")
	}
	q.CarID = int(id)
	for name, dst := range map[string]**time.Time{"from": &q.From, "to": &q.To} {
		if raw := values.Get(name); raw != "" {
			t, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return q, errors.New("Invalid " + name)
			}
			t = t.UTC()
			*dst = &t
		}
	}
	if q.From != nil && q.To != nil && !q.From.Before(*q.To) {
		return q, errors.New("from must be before to")
	}
	return q, nil
}

const roadCoverageRangeSQL = ` AND ($2::timestamp IS NULL OR d.start_date >= $2) AND ($3::timestamp IS NULL OR d.start_date < $3)`

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

func fetchRoadCoverage(ctx context.Context, database *sql.DB, q roadCoverageRequest) (roadCoverageData, error) {
	data := roadCoverageData{CarID: q.CarID, From: q.From, To: q.To, CellMeters: roadCoverageCellM,
		Cells: []roadCoverageCell{}, Trails: []roadCoverageTrail{}, Cities: []roadCoverageCity{}, Anchors: []roadCoverageCell{}}
	if database == nil {
		return data, errors.New("database unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, roadCoverageQueryTimeout)
	defer cancel()
	var exists bool
	if err := database.QueryRowContext(ctx, tireHistoryCarQuery, q.CarID).Scan(&exists); err != nil {
		return data, tireHistorySchemaError(err)
	}
	if !exists {
		return data, errTireHistoryCarNotFound
	}
	args := []any{q.CarID, nullableTime(q.From), nullableTime(q.To)}
	var first, last sql.NullTime
	if err := database.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(d.distance),0)::float8, min(d.start_date), max(d.start_date) FROM drives d WHERE d.car_id = $1`+roadCoverageRangeSQL, args...).
		Scan(&data.DriveCount, &data.DistanceKM, &first, &last); err != nil {
		return data, tireHistorySchemaError(err)
	}
	if first.Valid {
		f := first.Time.UTC()
		data.FirstDrive = &f
	}
	if last.Valid {
		l := last.Time.UTC()
		data.LastDrive = &l
	}
	rows, err := database.QueryContext(ctx, `SELECT p.drive_id, p.latitude::float8, p.longitude::float8 FROM positions p JOIN drives d ON d.id = p.drive_id WHERE d.car_id = $1`+roadCoverageRangeSQL+` AND p.latitude IS NOT NULL AND p.longitude IS NOT NULL ORDER BY p.drive_id, p.date, p.id`, args...)
	if err != nil {
		return data, tireHistorySchemaError(err)
	}
	var points []roadCoveragePoint
	for rows.Next() {
		var p roadCoveragePoint
		if err := rows.Scan(&p.DriveID, &p.Lat, &p.Lon); err != nil {
			rows.Close()
			return data, err
		}
		points = append(points, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return data, err
	}
	data.Cells, data.Trails, data.Truncated = buildRoadCoverage(points, roadCoverageCellM, roadCoverageSpacingM, roadCoverageMaxPoints)

	cityRows, err := database.QueryContext(ctx, `SELECT a.city, count(DISTINCT d.id) FROM drives d JOIN addresses a ON a.id IN (d.start_address_id, d.end_address_id) WHERE d.car_id = $1`+roadCoverageRangeSQL+` AND a.city IS NOT NULL AND a.city <> '' GROUP BY a.city ORDER BY 2 DESC, 1 LIMIT 100`, args...)
	if err == nil {
		for cityRows.Next() {
			var c roadCoverageCity
			if cityRows.Scan(&c.City, &c.Drives) == nil {
				data.Cities = append(data.Cities, c)
			}
		}
		cityRows.Close()
	}
	// Frequent drive endpoints (home/work) at ~100 m precision so share images
	// can blur them. Only endpoints used repeatedly are reported.
	anchorRows, err := database.QueryContext(ctx, `SELECT round(p.latitude::numeric, 3)::float8, round(p.longitude::numeric, 3)::float8, count(*) FROM drives d JOIN positions p ON p.id IN (d.start_position_id, d.end_position_id) WHERE d.car_id = $1`+roadCoverageRangeSQL+` GROUP BY 1, 2 HAVING count(*) >= `+strconv.Itoa(roadCoverageAnchorMin)+` ORDER BY 3 DESC LIMIT 4`, args...)
	if err == nil {
		for anchorRows.Next() {
			var c roadCoverageCell
			if anchorRows.Scan(&c.Lat, &c.Lon, &c.Visits) == nil {
				data.Anchors = append(data.Anchors, c)
			}
		}
		anchorRows.Close()
	}
	return data, nil
}

func addRoadCoverageCapability(payload map[string]any) {
	if payload == nil || db == nil {
		return
	}
	if capabilities, ok := payload["capabilities"].([]string); ok || payload["capabilities"] == nil {
		payload["capabilities"] = append(capabilities, roadCoverageCapability)
	}
	payload["road_coverage"] = map[string]any{"supported": true, "cell_m": roadCoverageCellM, "trail_spacing_m": roadCoverageSpacingM, "modes": []string{"unknown"}}
}

func handleRoadCoverage(w http.ResponseWriter, r *http.Request, car string) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid query"})
		return
	}
	q, err := parseRoadCoverageRequest(car, values)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	data, err := fetchRoadCoverage(r.Context(), db, q)
	if err != nil {
		status, code := http.StatusServiceUnavailable, "road_coverage_unavailable"
		if errors.Is(err, errTireHistoryUnsupported) {
			status, code = http.StatusNotImplemented, "road_coverage_unsupported"
		}
		if errors.Is(err, errTireHistoryCarNotFound) {
			status, code = http.StatusNotFound, "car_not_found"
		}
		writeJSON(w, status, map[string]string{"error": code})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"data": data, "meta": map[string]any{
		"generated_at": time.Now().UTC().Format(time.RFC3339Nano), "source_table": "positions",
		"aggregation": "decimated_trails_and_grid_cells", "visit_unit": "distinct_drives_per_cell",
	}})
}
