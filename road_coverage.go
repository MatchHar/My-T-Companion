package main

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// Road coverage ("足跡地圖") is a read-only aggregate of TeslaMate drive
// positions. Only reduced track geometry is returned: trajectories preserve bends
// with bounded simplification error and never bridge gaps, and visit heat is counted
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
	CellsComplete           bool                `json:"cells_complete"`
	GeometryIncluded        bool                `json:"geometry_included"`
	Passport                *roadPassportData   `json:"passport,omitempty"`
	CarID                   int                 `json:"car_id"`
	From                    *time.Time          `json:"from"`
	To                      *time.Time          `json:"to"`
	CellMeters              float64             `json:"cell_m"`
	DriveCount              int                 `json:"drive_count"`
	DistanceKM              float64             `json:"distance_km"`
	FirstDrive              *time.Time          `json:"first_drive_at"`
	LastDrive               *time.Time          `json:"last_drive_at"`
	Cells                   []roadCoverageCell  `json:"cells"`
	Trails                  []roadCoverageTrail `json:"trails"`
	Cities                  []roadCoverageCity  `json:"cities"`
	Anchors                 []roadCoverageCell  `json:"private_anchors"`
	Truncated               bool                `json:"truncated"`
	OmittedTrails           int                 `json:"omitted_trails"`
	PrivacyAnchorsAvailable bool                `json:"privacy_anchors_available"`
	CitiesAvailable         bool                `json:"cities_available"`
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

// Input is streamed in drive/time order. Only one bounded raw chunk is kept;
// retained geometry has a strict global point budget. Gaps never become bridges.
type roadCoverageBuilder struct {
	countOnly                 bool
	cellM, spacingM           float64
	maxPoints, totalPoints    int
	visits                    map[roadCellKey]int
	seen                      map[roadCellKey]int64
	trails                    []roadCoverageTrail
	current, pending          []roadCoveragePoint
	drive                     int64
	haveDrive, haveLast       bool
	last                      roadCoveragePoint
	lastAt                    time.Time
	chunkLength               float64
	truncated, omittedCurrent bool
	omitted                   int
}

func newRoadCoverageBuilder(cellM, spacingM float64, maxPoints int) *roadCoverageBuilder {
	return &roadCoverageBuilder{cellM: cellM, spacingM: spacingM, maxPoints: maxPoints,
		visits: map[roadCellKey]int{}, seen: map[roadCellKey]int64{}}
}

func (b *roadCoverageBuilder) add(p roadCoveragePoint, at time.Time) {
	if !validRoadPoint(p.Lat, p.Lon) {
		b.finishSegment()
		b.haveLast = false
		return
	}
	if !b.haveDrive || b.drive != p.DriveID {
		b.finishSegment()
		b.drive = p.DriveID
		b.haveDrive = true
		b.haveLast = false
	}
	p.Lat, p.Lon = round5(p.Lat), round5(p.Lon)
	k := roadCellFor(p.Lat, p.Lon, b.cellM)
	if b.seen[k] != p.DriveID+1 {
		b.seen[k] = p.DriveID + 1
		b.visits[k]++
	}
	if b.countOnly {
		return
	}
	if b.haveLast {
		distance := haversineM(b.last.Lat, b.last.Lon, p.Lat, p.Lon)
		gap := !at.IsZero() && !b.lastAt.IsZero() && (at.Sub(b.lastAt) > 2*time.Minute || at.Before(b.lastAt))
		if gap || distance > 5000 {
			b.finishSegment()
			b.haveLast = false
		} else if distance < 0.25 {
			b.lastAt = at
			return
		} else {
			b.chunkLength += distance
		}
	}
	b.pending = append(b.pending, p)
	if len(b.pending) >= 4096 || b.chunkLength >= 1000 {
		b.flushChunk()
		b.pending = []roadCoveragePoint{p}
		b.chunkLength = 0
	}
	b.last, b.lastAt, b.haveLast = p, at, true
}

func (b *roadCoverageBuilder) flushChunk() {
	if len(b.pending) == 0 {
		return
	}
	epsilon := math.Min(10, b.spacingM/6)
	if epsilon <= 0 {
		epsilon = 10
	}
	points := simplifyRoadPoints(b.pending, epsilon)
	for _, p := range points {
		if len(b.current) > 0 {
			last := b.current[len(b.current)-1]
			if last.Lat == p.Lat && last.Lon == p.Lon {
				continue
			}
		}
		if b.maxPoints > 0 && b.totalPoints >= b.maxPoints {
			b.truncated, b.omittedCurrent = true, true
			continue
		}
		b.current = append(b.current, p)
		b.totalPoints++
	}
	b.pending = nil
}

func (b *roadCoverageBuilder) finishSegment() {
	b.flushChunk()
	if len(b.current) >= 2 {
		t := roadCoverageTrail{DriveID: b.drive, Mode: "unknown", Points: make([][2]float64, 0, len(b.current))}
		for _, p := range b.current {
			t.Points = append(t.Points, [2]float64{p.Lat, p.Lon})
		}
		b.trails = append(b.trails, t)
	} else {
		b.totalPoints -= len(b.current)
	}
	if b.omittedCurrent {
		b.omitted++
	}
	b.current, b.pending = nil, nil
	b.chunkLength = 0
	b.omittedCurrent = false
}

func (b *roadCoverageBuilder) finish() ([]roadCoverageCell, []roadCoverageTrail, bool) {
	b.finishSegment()
	for i := range b.trails {
		t := &b.trails[i]
		t.Heat = make([]int, len(t.Points))
		for j, p := range t.Points {
			t.Heat[j] = b.visits[roadCellFor(p[0], p[1], b.cellM)]
		}
	}
	cells := make([]roadCoverageCell, 0, len(b.visits))
	for k, v := range b.visits {
		lat, lon := roadCellCenter(k, b.cellM)
		cells = append(cells, roadCoverageCell{Lat: round5(lat), Lon: round5(lon), Visits: v})
	}
	sortRoadCells(cells)
	if b.trails == nil {
		b.trails = []roadCoverageTrail{}
	}
	return cells, b.trails, b.truncated
}

// Ramer-Douglas-Peucker on <=1 km chunks: preserve first/last points and bends
// within ten metres. Budget exhaustion omits geometry instead of drawing shortcuts.
func simplifyRoadPoints(points []roadCoveragePoint, epsilon float64) []roadCoveragePoint {
	if len(points) < 3 {
		return points
	}
	keep := make([]bool, len(points))
	keep[0], keep[len(points)-1] = true, true
	type interval struct{ lo, hi int }
	stack := []interval{{0, len(points) - 1}}
	for len(stack) > 0 {
		r := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		maximum, chosen := epsilon, -1
		for i := r.lo + 1; i < r.hi; i++ {
			if d := roadSegmentDistance(points[i], points[r.lo], points[r.hi]); d > maximum {
				maximum, chosen = d, i
			}
		}
		if chosen >= 0 {
			keep[chosen] = true
			stack = append(stack, interval{r.lo, chosen}, interval{chosen, r.hi})
		}
	}
	result := make([]roadCoveragePoint, 0, len(points))
	for i, p := range points {
		if keep[i] {
			result = append(result, p)
		}
	}
	return result
}

func roadSegmentDistance(p, a, b roadCoveragePoint) float64 {
	wrap := func(v float64) float64 {
		if v > 180 {
			return v - 360
		}
		if v < -180 {
			return v + 360
		}
		return v
	}
	xScale := 111320 * math.Cos((a.Lat+b.Lat)*math.Pi/360)
	px, py := wrap(p.Lon-a.Lon)*xScale, (p.Lat-a.Lat)*111320
	bx, by := wrap(b.Lon-a.Lon)*xScale, (b.Lat-a.Lat)*111320
	den := bx*bx + by*by
	if den < 1e-9 {
		return math.Hypot(px, py)
	}
	t := math.Max(0, math.Min(1, (px*bx+py*by)/den))
	return math.Hypot(px-t*bx, py-t*by)
}

// Compatibility helper for deterministic tests. Production feeds the builder
// directly from rows rather than first materializing all raw positions.
func buildRoadCoverage(points []roadCoveragePoint, cellM, spacingM float64, maxPoints int) ([]roadCoverageCell, []roadCoverageTrail, bool) {
	b := newRoadCoverageBuilder(cellM, spacingM, maxPoints)
	for _, p := range points {
		b.add(p, time.Time{})
	}
	return b.finish()
}

func sortRoadCells(cells []roadCoverageCell) {
	sort.Slice(cells, func(i, j int) bool { return roadCellLess(cells[i], cells[j]) })
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
	PassportOnly bool
	CarID        int
	From, To     *time.Time
}

func parseRoadCoverageRequest(car string, values url.Values) (roadCoverageRequest, error) {
	var q roadCoverageRequest
	id, err := strconv.ParseInt(car, 10, 32)
	if err != nil || id <= 0 {
		return q, errors.New("Invalid car id")
	}
	q.CarID = int(id)
	switch values.Get("view") {
	case "":
	case "passport":
		q.PassportOnly = true
	default:
		return q, errors.New("Invalid view")
	}

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
	data := roadCoverageData{GeometryIncluded: !q.PassportOnly, CarID: q.CarID, From: q.From, To: q.To, CellMeters: roadCoverageCellM,
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
	rows, err := database.QueryContext(ctx, `SELECT p.drive_id, p.latitude::float8, p.longitude::float8, p.date FROM positions p JOIN drives d ON d.id = p.drive_id WHERE d.car_id = $1`+roadCoverageRangeSQL+` ORDER BY p.drive_id DESC, p.date, p.id`, args...)
	if err != nil {
		return data, tireHistorySchemaError(err)
	}
	builder := newRoadCoverageBuilder(roadCoverageCellM, roadCoverageSpacingM, roadCoverageMaxPoints)
	builder.countOnly = q.PassportOnly
	n := 0
	for rows.Next() {
		var p roadCoveragePoint
		var at time.Time
		var lat, lon sql.NullFloat64
		n++
		if n%256 == 0 {
			if err := ctx.Err(); err != nil {
				rows.Close()
				return data, err
			}
		}
		if err := rows.Scan(&p.DriveID, &lat, &lon, &at); err != nil {
			rows.Close()
			return data, err
		}
		if lat.Valid && lon.Valid {
			p.Lat, p.Lon = lat.Float64, lon.Float64
		} else {
			p.Lat, p.Lon = math.NaN(), math.NaN()
		}
		builder.add(p, at)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return data, err
	}
	data.Cells, data.Trails, data.Truncated = builder.finish()
	// Reaching EOF without scan/context error confirms the full footprint grid.
	// Route simplification/omission is a separate visualization concern.
	data.CellsComplete = true
	data.OmittedTrails = builder.omitted
	data.Passport = fetchRoadPassport(ctx, database, q)

	cityRows, err := database.QueryContext(ctx, `SELECT a.city, count(DISTINCT d.id) FROM drives d JOIN addresses a ON a.id IN (d.start_address_id, d.end_address_id) WHERE d.car_id = $1`+roadCoverageRangeSQL+` AND a.city IS NOT NULL AND a.city <> '' GROUP BY a.city ORDER BY 2 DESC, 1 LIMIT 100`, args...)
	if err == nil {
		scanned := true
		for cityRows.Next() {
			var c roadCoverageCity
			if cityRows.Scan(&c.City, &c.Drives) == nil {
				data.Cities = append(data.Cities, c)
			} else {
				scanned = false
			}
		}
		data.CitiesAvailable = scanned && cityRows.Err() == nil && len(data.Cities) < 100
		cityRows.Close()
	}
	// Frequent drive endpoints (home/work) at ~100 m precision so share images
	// can blur them. Only endpoints used repeatedly are reported.
	anchorRows, err := database.QueryContext(ctx, `SELECT round(p.latitude::numeric, 3)::float8, round(p.longitude::numeric, 3)::float8, count(*) FROM drives d JOIN positions p ON p.id IN (d.start_position_id, d.end_position_id) WHERE d.car_id = $1`+roadCoverageRangeSQL+` GROUP BY 1, 2 HAVING count(*) >= `+strconv.Itoa(roadCoverageAnchorMin)+` ORDER BY 3 DESC LIMIT 4`, args...)
	if err == nil {
		scanned := true
		for anchorRows.Next() {
			var c roadCoverageCell
			if anchorRows.Scan(&c.Lat, &c.Lon, &c.Visits) == nil {
				data.Anchors = append(data.Anchors, c)
			} else {
				scanned = false
			}
		}
		data.PrivacyAnchorsAvailable = scanned && anchorRows.Err() == nil
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
