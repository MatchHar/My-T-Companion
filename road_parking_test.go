package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func parkingTestRow(id int64, start time.Time, minutes int, lat, lon float64) roadParkingRow {
	return roadParkingRow{id: id, nextID: sql.NullInt64{Int64: id + 1, Valid: true}, at: start, end: sql.NullTime{Time: start.Add(time.Duration(minutes) * time.Minute), Valid: true}, lat: sql.NullFloat64{Float64: lat, Valid: true}, lon: sql.NullFloat64{Float64: lon, Valid: true}, nextLat: sql.NullFloat64{Float64: lat, Valid: true}, nextLon: sql.NullFloat64{Float64: lon, Valid: true}, address: 7, country: "Canada", state: "Ontario", city: "Toronto"}
}
func TestRoadParkingRangeIntersectionAndDistinctArrivals(t *testing.T) {
	at := time.Date(2026, 10, 1, 23, 30, 0, 0, time.UTC)
	from := at.Add(30 * time.Minute)
	to := from.Add(24 * time.Hour)
	a := parkingTestRow(1, at, 120, 43, -79)
	b := parkingTestRow(2, from.Add(12*time.Hour), 45, 43, -79)
	d := buildRoadParking([]roadParkingRow{b, a, a}, roadCoverageRequest{CarID: 1, From: &from, To: &to}, true)
	if !d.Complete || len(d.Places) != 1 {
		t.Fatal("incomplete or duplicated place")
	}
	p := d.Places[0]
	if p.Arrivals != 1 || p.KnownStays != 2 || p.Seconds != 135*60 {
		t.Fatalf("range/dedup mismatch: %+v", p)
	}
	if len(p.stays) != 2 || p.stays[1].ArrivalInRange {
		t.Fatal("continued stay became a fresh arrival")
	}
}
func TestRoadParkingMinimumGapOpenAndMismatchedLocations(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	short := parkingTestRow(1, at, 4, 43, -79)
	exact := parkingTestRow(2, at.Add(time.Hour), 5, 43, -79)
	open := parkingTestRow(3, at.Add(2*time.Hour), 30, 43, -79)
	open.end.Valid = false
	open.nextID.Valid = false
	bad := parkingTestRow(4, at.Add(3*time.Hour), 60, 43, -79)
	bad.nextLat.Float64 = 44
	d := buildRoadParking([]roadParkingRow{short, exact, open, bad}, roadCoverageRequest{CarID: 1}, true)
	p := d.Places[0]
	for _, stay := range p.stays {
		if stay.Quality == "unconfirmed_location" && stay.End != nil {
			t.Fatal("next trip elsewhere became a confirmed departure")
		}
	}
	if p.Arrivals != 3 || p.KnownStays != 1 || p.UnknownStays != 2 || p.Seconds != 300 {
		t.Fatal("short stop/unknown duration counted incorrectly")
	}
}
func TestRoadParkingNeverChainsAcrossNeighbouringPlaces(t *testing.T) {
	at := time.Now().UTC()
	rows := []roadParkingRow{}
	for i := 0; i < 4; i++ {
		rows = append(rows, parkingTestRow(int64(i+1), at.Add(time.Duration(i)*time.Hour), 10, 43, -79+float64(i)*.00035))
	}
	d := buildRoadParking(rows, roadCoverageRequest{CarID: 1}, true)
	if len(d.Places) < 2 {
		t.Fatal("chain merged a whole street")
	}
	for _, p := range d.Places {
		for _, s := range p.stays {
			r := rows[s.DriveID-1]
			if haversineM(p.Point[0], p.Point[1], r.lat.Float64, r.lon.Float64) > 35 {
				t.Fatal("cluster exceeded anchor radius")
			}
		}
	}
	copyRows := append([]roadParkingRow{}, rows...)
	copyRows = append(copyRows, parkingTestRow(9, at.Add(9*time.Hour), 10, 43, -79))
	next := buildRoadParking(copyRows, roadCoverageRequest{CarID: 1}, true)
	found := false
	for _, p := range next.Places {
		if p.ID == d.Places[len(d.Places)-1].ID {
			found = true
		}
	}
	if !found {
		t.Fatal("appending new stay changed existing identity")
	}
}
func TestRoadParkingAddressCarAndCursorIsolation(t *testing.T) {
	at := time.Now().UTC()
	a := parkingTestRow(1, at, 10, 43, -79)
	b := a
	b.id = 2
	b.address = 8
	d := buildRoadParking([]roadParkingRow{a, b}, roadCoverageRequest{CarID: 1}, true)
	if len(d.Places) != 2 {
		t.Fatal("different adjacent addresses merged")
	}
	e := buildRoadParking([]roadParkingRow{a}, roadCoverageRequest{CarID: 2}, true)
	for _, p := range d.Places {
		if p.ID == e.Places[0].ID {
			t.Fatal("car IDs collided")
		}
	}
	c := roadParkingCursor{1, "range", "place", d.revision, 50}
	raw := parkingCursorToken(c)
	decoded, err := parkingCursorRead(raw)
	if err != nil || decoded != c {
		t.Fatal("cursor roundtrip")
	}
	if _, err := parkingCursorRead(raw + "tamper"); err == nil {
		t.Fatal("tampered cursor accepted")
	}
}
func TestRoadParkingLocalPostgresBoundariesOutsideRange(t *testing.T) {
	db := openFriendPostgresFixture(t)
	fixtureExec(t, db, `ALTER TABLE drives ADD COLUMN start_position_id bigint,ADD COLUMN end_position_id bigint,ADD COLUMN end_address_id integer;
CREATE TEMP TABLE addresses(id integer PRIMARY KEY,country text,state text,city text,display_name text);
INSERT INTO cars VALUES(1),(2);INSERT INTO addresses VALUES(1,'Canada','Ontario','Toronto','Library');
INSERT INTO drives VALUES(1,1,'2026-10-01 22:00','2026-10-01 23:30',1,2,1),(2,1,'2026-10-02 01:30','2026-10-02 02:00',3,4,1),(3,2,'2026-10-01 22:00','2026-10-01 23:30',5,6,1);
INSERT INTO positions(id,car_id,drive_id,date,latitude,longitude) VALUES(1,1,1,'2026-10-01 22:00',43,-79),(2,1,1,'2026-10-01 23:30',43,-79),(3,1,2,'2026-10-02 01:30',43,-79),(4,1,2,'2026-10-02 02:00',43,-79),(5,2,3,'2026-10-01 22:00',44,-80),(6,2,3,'2026-10-01 23:30',44,-80);`)
	q, err := parseRoadCoverageRequest("1", url.Values{"from": {"2026-10-02T00:00:00Z"}, "to": {"2026-10-02T01:00:00Z"}})
	if err != nil {
		t.Fatal(err)
	}
	d, err := fetchRoadParking(context.Background(), db, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Places) != 1 || d.Places[0].Arrivals != 0 || d.Places[0].Seconds != 3600 || d.Places[0].Name != "Library" {
		t.Fatalf("range boundary or car isolation: %+v", d)
	}
	// Handler's database global is rebound only during this serial local fixture.
	restore := setParkingFixtureDatabase(db)
	defer restore()
	get := func(path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r = r.WithContext(context.WithValue(r.Context(), ownerAuthenticationContextKey{}, true))
		w := httptest.NewRecorder()
		handleStates(w, r)
		return w
	}
	first := get("/api/v1/cars/1/road-parking")
	var payload struct {
		Data roadParkingData `json:"data"`
	}
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &payload) != nil || len(payload.Data.Places) != 1 {
		t.Fatal("summary handler failed")
	}
	id := payload.Data.Places[0].ID
	page := get("/api/v1/cars/1/road-parking?place=" + id + "&limit=1")
	if json.Unmarshal(page.Body.Bytes(), &payload) != nil || len(payload.Data.Stays) != 1 || payload.Data.Next == "" {
		t.Fatal("first page")
	}
	cursor := payload.Data.Next
	page = get("/api/v1/cars/1/road-parking?place=" + id + "&limit=1&cursor=" + url.QueryEscape(cursor))
	payload.Data = roadParkingData{}
	if page.Code != 200 || json.Unmarshal(page.Body.Bytes(), &payload) != nil || len(payload.Data.Stays) != 1 || payload.Data.Next != "" {
		t.Fatal("second page")
	}
	if get("/api/v1/cars/2/road-parking?place="+id+"&cursor="+url.QueryEscape(cursor)).Code != 400 {
		t.Fatal("cross-car cursor accepted")
	}
	if get("/api/v1/cars/1/road-parking?from=2026-10-01T00:00:00Z&place="+id+"&cursor="+url.QueryEscape(cursor)).Code != 400 {
		t.Fatal("cross-range cursor accepted")
	}

}

func TestRoadParkingOwnerBoundaryAndQueryValidation(t *testing.T) {
	if !ownerResourcePath.MatchString("cars/1/road-parking") || !roadParkingPath.MatchString("/api/v1/cars/1/road-parking") || ownerResourcePath.MatchString("cars/1/road-parking/control") {
		t.Fatal("owner route boundary")
	}
	for _, test := range []struct {
		method, path string
		verified     bool
		status       int
	}{{"POST", "/api/v1/cars/1/road-parking", true, 405}, {"GET", "/api/v1/cars/1/road-parking", false, 401}, {"GET", "/api/v1/cars/1/road-parking?limit=201", true, 400}, {"GET", "/api/v1/cars/1/road-parking?cursor=abc", true, 400}, {"GET", "/api/v1/cars/1/road-parking?from=nope", true, 400}} {
		req := httptest.NewRequest(test.method, test.path, nil)
		if test.verified {
			req = req.WithContext(context.WithValue(req.Context(), ownerAuthenticationContextKey{}, true))
		}
		w := httptest.NewRecorder()
		handleStates(w, req)
		if w.Code != test.status {
			t.Fatalf("%s: %d", test.path, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("parking data was cacheable")
		}
	}
}
func TestRoadParkingRecentOpenStopNotPromotedBeforeFiveMinutes(t *testing.T) {
	r := parkingTestRow(1, time.Now().Add(-time.Minute), 10, 43, -79)
	r.end.Valid = false
	if len(buildRoadParking([]roadParkingRow{r}, roadCoverageRequest{CarID: 1}, true).Places) != 0 {
		t.Fatal("one-minute open stop counted")
	}
}

func setParkingFixtureDatabase(value *sql.DB) func() {
	old := db
	db = value
	return func() { db = old }
}

func TestRoadParkingUnclassifiedPlaceCannotBecomeAConfirmedEmptyCity(t *testing.T) {
	r := parkingTestRow(1, time.Now().Add(-time.Hour), 30, 43, -79)
	r.city = ""
	d := buildRoadParking([]roadParkingRow{r}, roadCoverageRequest{CarID: 1}, true)
	if d.Complete || len(d.Places) != 1 {
		t.Fatal("missing administrative identity became complete or dropped valid map place")
	}
}
