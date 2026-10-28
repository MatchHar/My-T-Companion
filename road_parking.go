package main

// Read-only parking summaries use recorded drive boundaries and saved endpoint
// positions. Neither MQTT lock events nor simplified routes create a stay.
import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const roadParkingCapability = "road_parking_v1"
const roadParkingMaxRows = 20000

var roadParkingPath = regexp.MustCompile(`^/api/v1/cars/(\d+)/road-parking$`)

type roadParkingStay struct {
	Point            [2]float64 `json:"point"`
	ID               string     `json:"id"`
	DriveID          int64      `json:"drive_id"`
	FollowingDriveID *int64     `json:"following_drive_id,omitempty"`
	At               time.Time  `json:"arrived_at"`
	End              *time.Time `json:"departed_at,omitempty"`
	Seconds          *float64   `json:"seconds_in_range,omitempty"`
	ArrivalInRange   bool       `json:"arrival_in_range"`
	Quality          string     `json:"quality"`
}
type roadParkingPlace struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Country      string     `json:"country"`
	State        string     `json:"state"`
	City         string     `json:"city"`
	Point        [2]float64 `json:"point"`
	Arrivals     int        `json:"arrivals"`
	Seconds      float64    `json:"seconds_in_range"`
	KnownStays   int        `json:"known_stays"`
	UnknownStays int        `json:"unknown_stays"`
	Last         time.Time  `json:"last_arrived_at"`
	LastEnd      *time.Time `json:"last_departed_at,omitempty"`
	stays        []roadParkingStay
	anchor       [2]float64
}
type roadParkingData struct {
	Version  int                `json:"version"`
	CarID    int                `json:"car_id"`
	Basis    string             `json:"basis"`
	Complete bool               `json:"complete"`
	Places   []roadParkingPlace `json:"places"`
	Stays    []roadParkingStay  `json:"stays,omitempty"`
	Next     string             `json:"next_cursor,omitempty"`
	revision string
}
type roadParkingRow struct {
	id                                   int64
	nextID                               sql.NullInt64
	at                                   time.Time
	end                                  sql.NullTime
	lat, lon, nextLat, nextLon           sql.NullFloat64
	address                              int64
	geofence, name, country, state, city string
}

// Window over all drives before range filtering preserves the immediately
// following departure even outside the requested period. Joins are car-bound.
const roadParkingSQL = `WITH ordered AS (
 SELECT d.*, lead(id) OVER w AS next_id, lead(start_date) OVER w AS next_start,
 lead(start_position_id) OVER w AS next_position
 FROM drives d WHERE d.car_id=$1 WINDOW w AS (ORDER BY d.start_date,d.id)
)
SELECT d.id,d.next_id,d.end_date,d.next_start,p.latitude::float8,p.longitude::float8,
 n.latitude::float8,n.longitude::float8,coalesce(d.end_address_id,0),
 coalesce(to_jsonb(d)->>'end_geofence_id',''),
 coalesce(nullif(to_jsonb(a)->>'display_name',''),nullif(to_jsonb(a)->>'name',''),nullif(to_jsonb(a)->>'road',''),''),
 coalesce(a.country,''),coalesce(a.state,''),coalesce(a.city,'')
FROM ordered d LEFT JOIN positions p ON p.id=d.end_position_id AND p.car_id=$1
LEFT JOIN positions n ON n.id=d.next_position AND n.car_id=$1
LEFT JOIN addresses a ON a.id=d.end_address_id
WHERE d.end_date IS NOT NULL AND d.end_date>=d.start_date
 AND ($2::timestamp IS NULL OR d.next_start IS NULL OR d.next_start>$2)
 AND ($3::timestamp IS NULL OR d.end_date<$3)
ORDER BY d.end_date,d.id LIMIT 20001`

func buildRoadParking(rows []roadParkingRow, q roadCoverageRequest, complete bool) roadParkingData {
	out := roadParkingData{Version: 1, CarID: q.CarID, Basis: "recorded_drive_boundaries", Complete: complete, Places: []roadParkingPlace{}}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].at.Equal(rows[j].at) {
			return rows[i].id < rows[j].id
		}
		return rows[i].at.Before(rows[j].at)
	})
	now := time.Now()
	seen := map[int64]bool{}
	buckets := map[string][]int{}
	revision := sha256.New()
	for _, r := range rows {
		if seen[r.id] {
			continue
		}
		seen[r.id] = true
		if r.id <= 0 || r.at.IsZero() || !r.lat.Valid || !r.lon.Valid || !validRoadPoint(r.lat.Float64, r.lon.Float64) {
			out.Complete = false
			continue
		}
		if r.end.Valid && r.end.Time.Sub(r.at) < 5*time.Minute || !r.end.Valid && now.Sub(r.at) < 5*time.Minute {
			continue
		}
		if q.To != nil && !r.at.Before(*q.To) || q.From != nil && r.end.Valid && !r.end.Time.After(*q.From) {
			continue
		}
		if strings.TrimSpace(r.country) == "" || strings.TrimSpace(r.city) == "" {
			out.Complete = false
		}
		arrival := (q.From == nil || !r.at.Before(*q.From)) && (q.To == nil || r.at.Before(*q.To))
		stay := roadParkingStay{Point: [2]float64{r.lat.Float64, r.lon.Float64}, ID: strconv.Itoa(q.CarID) + ":" + strconv.FormatInt(r.id, 10), DriveID: r.id, At: r.at.UTC(), ArrivalInRange: arrival, Quality: "open_end"}
		if r.nextID.Valid {
			id := r.nextID.Int64
			stay.FollowingDriveID = &id
		}
		if r.end.Valid {
			end := r.end.Time.UTC()
			stay.End = &end
			if r.nextLat.Valid && r.nextLon.Valid && validRoadPoint(r.nextLat.Float64, r.nextLon.Float64) && haversineM(r.lat.Float64, r.lon.Float64, r.nextLat.Float64, r.nextLon.Float64) <= 100 {
				start := r.at
				if q.From != nil && start.Before(*q.From) {
					start = *q.From
				}
				if q.To != nil && end.After(*q.To) {
					end = *q.To
				}
				seconds := end.Sub(start).Seconds()
				if seconds < 0 {
					out.Complete = false
					continue
				}
				stay.Seconds = &seconds
				stay.Quality = "recorded_pair"
			} else {
				stay.Quality = "unconfirmed_location"
				stay.End = nil // Next trip elsewhere does not confirm departure here.
			}
		}
		ns := "address:" + strconv.FormatInt(r.address, 10)
		if r.geofence != "" {
			ns = "geofence:" + r.geofence
		}
		cell := roadCellFor(r.lat.Float64, r.lon.Float64, 35)
		bucket := func(y, x int64) string { return fmt.Sprintf("%s:%d:%d", ns, y, x) }
		found := -1
		for y := cell.Y - 1; y <= cell.Y+1; y++ {
			for x := cell.X - 1; x <= cell.X+1; x++ {
				for _, i := range buckets[bucket(y, x)] {
					p := out.Places[i]
					if haversineM(p.anchor[0], p.anchor[1], r.lat.Float64, r.lon.Float64) <= 35 && (found < 0 || p.ID < out.Places[found].ID) {
						found = i
					}
				}
			}
		}
		if found < 0 {
			digest := sha256.Sum256([]byte(fmt.Sprintf("parking-v1:%d:%s:%d", q.CarID, ns, r.id)))
			point := [2]float64{r.lat.Float64, r.lon.Float64}
			out.Places = append(out.Places, roadParkingPlace{ID: hex.EncodeToString(digest[:12]), Name: strings.TrimSpace(r.name), Country: r.country, State: r.state, City: r.city, Point: point, anchor: point})
			found = len(out.Places) - 1
			buckets[bucket(cell.Y, cell.X)] = append(buckets[bucket(cell.Y, cell.X)], found)
		}
		p := &out.Places[found]
		p.stays = append(p.stays, stay)
		if arrival {
			p.Arrivals++
		}
		if stay.Seconds != nil {
			p.Seconds += *stay.Seconds
			p.KnownStays++
		} else {
			p.UnknownStays++
		}
		if p.Last.IsZero() || stay.At.After(p.Last) {
			p.Last = stay.At
			p.LastEnd = stay.End
		}
		fmt.Fprintf(revision, "%d:%s:%v:%.6f:%.6f:%s:%s;", r.id, stay.At.Format(time.RFC3339Nano), stay.End, r.lat.Float64, r.lon.Float64, r.name, stay.Quality)
	}
	out.revision = hex.EncodeToString(revision.Sum(nil))
	sort.Slice(out.Places, func(i, j int) bool {
		a, b := out.Places[i], out.Places[j]
		if a.Last.Equal(b.Last) {
			return a.ID < b.ID
		}
		return a.Last.After(b.Last)
	})
	for i := range out.Places {
		sort.Slice(out.Places[i].stays, func(a, b int) bool { return out.Places[i].stays[a].At.After(out.Places[i].stays[b].At) })
	}
	return out
}

func fetchRoadParking(ctx context.Context, database *sql.DB, q roadCoverageRequest) (roadParkingData, error) {
	if database == nil {
		return roadParkingData{}, errors.New("database unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var exists bool
	if err := database.QueryRowContext(ctx, tireHistoryCarQuery, q.CarID).Scan(&exists); err != nil {
		return roadParkingData{}, tireHistorySchemaError(err)
	}
	if !exists {
		return roadParkingData{}, errTireHistoryCarNotFound
	}
	rs, err := database.QueryContext(ctx, roadParkingSQL, q.CarID, nullableTime(q.From), nullableTime(q.To))
	if err != nil {
		return roadParkingData{}, tireHistorySchemaError(err)
	}
	defer rs.Close()
	rows := []roadParkingRow{}
	complete := true
	for rs.Next() {
		if len(rows) >= roadParkingMaxRows {
			complete = false
			break
		}
		var r roadParkingRow
		if err := rs.Scan(&r.id, &r.nextID, &r.at, &r.end, &r.lat, &r.lon, &r.nextLat, &r.nextLon, &r.address, &r.geofence, &r.name, &r.country, &r.state, &r.city); err != nil {
			return roadParkingData{}, err
		}
		rows = append(rows, r)
	}
	if err := rs.Err(); err != nil {
		return roadParkingData{}, err
	}
	if err := ctx.Err(); err != nil {
		return roadParkingData{}, err
	}
	return buildRoadParking(rows, q, complete), nil
}

type roadParkingCursor struct {
	Car                    int
	Scope, Place, Revision string
	Offset                 int
}

func parkingCursorToken(c roadParkingCursor) string {
	b, _ := json.Marshal(c)
	h := hmac.New(sha256.New, tireHistoryCursorKey)
	h.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func parkingCursorRead(raw string) (roadParkingCursor, error) {
	var c roadParkingCursor
	p := strings.Split(raw, ".")
	if len(p) != 2 || len(raw) > 2048 {
		return c, errors.New("invalid cursor")
	}
	b, e := base64.RawURLEncoding.DecodeString(p[0])
	if e != nil {
		return c, e
	}
	sig, e := base64.RawURLEncoding.DecodeString(p[1])
	if e != nil {
		return c, e
	}
	h := hmac.New(sha256.New, tireHistoryCursorKey)
	h.Write(b)
	if !hmac.Equal(sig, h.Sum(nil)) {
		return c, errors.New("invalid cursor")
	}
	e = json.Unmarshal(b, &c)
	return c, e
}
func parkingScope(q roadCoverageRequest) string { return fmt.Sprintf("%v|%v", q.From, q.To) }

func handleRoadParking(w http.ResponseWriter, r *http.Request, car string) {
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
		writeJSON(w, 400, map[string]string{"error": "invalid_query"})
		return
	}
	q, err := parseRoadCoverageRequest(car, values)
	if err != nil || q.PassportOnly {
		writeJSON(w, 400, map[string]string{"error": "invalid_query"})
		return
	}
	if len(values.Get("place")) > 128 || values.Get("cursor") != "" && values.Get("place") == "" {
		writeJSON(w, 400, map[string]string{"error": "invalid_query"})
		return
	}
	limit := 50
	if raw := values.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 200 {
			writeJSON(w, 400, map[string]string{"error": "invalid_limit"})
			return
		}
	}
	data, err := fetchRoadParking(r.Context(), db, q)
	if err != nil {
		status := 503
		code := "road_parking_unavailable"
		if errors.Is(err, errTireHistoryUnsupported) {
			status = 501
			code = "road_parking_unsupported"
		}
		if errors.Is(err, errTireHistoryCarNotFound) {
			status = 404
			code = "car_not_found"
		}
		writeJSON(w, status, map[string]string{"error": code})
		return
	}
	place := values.Get("place")
	offset := 0
	if raw := values.Get("cursor"); raw != "" {
		c, e := parkingCursorRead(raw)
		if e != nil || c.Car != q.CarID || c.Scope != parkingScope(q) || c.Place != place || c.Revision != data.revision || c.Offset < 0 {
			writeJSON(w, 400, map[string]string{"error": "invalid_cursor"})
			return
		}
		offset = c.Offset
	}
	if place != "" {
		found := -1
		for i, p := range data.Places {
			if p.ID == place {
				found = i
				break
			}
		}
		if found < 0 {
			writeJSON(w, 404, map[string]string{"error": "place_not_found"})
			return
		}
		p := data.Places[found]
		if offset > len(p.stays) {
			writeJSON(w, 400, map[string]string{"error": "invalid_cursor"})
			return
		}
		end := offset + limit
		if end > len(p.stays) {
			end = len(p.stays)
		}
		data.Stays = p.stays[offset:end]
		data.Places = []roadParkingPlace{p}
		if end < len(p.stays) {
			data.Next = parkingCursorToken(roadParkingCursor{q.CarID, parkingScope(q), place, data.revision, end})
		}
	}
	writeJSON(w, 200, map[string]any{"data": data, "meta": map[string]any{"basis": data.Basis, "minimum_stay_seconds": 300, "duration": "intersection_with_range", "arrivals": "arrival_in_range", "source": "saved_drive_endpoints"}})
}
