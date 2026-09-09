package main

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	ft "mycarmate-states-api/internal/friendtogether"
)

// Opt-in only. Requires an independently provisioned empty local database named
// myt_friend_fixture. Never accepts a remote host or DATABASE_* production vars.
// Every test relation is TEMP in this connection; closing it removes all data.
func openFriendPostgresFixture(t *testing.T) *sql.DB {
	t.Helper()
	raw := os.Getenv("MYT_FRIEND_LOCAL_POSTGRES_URL")
	if raw == "" {
		t.Skip("local PostgreSQL fixture unavailable/not explicitly enabled; SQL execution remains unverified")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "postgres" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "::1") || u.Port() == "" || u.Path != "/myt_friend_fixture" || u.RawQuery != "" || u.Fragment != "" {
		t.Fatal("fixture requires literal-loopback postgres://.../myt_friend_fixture with explicit port and no query")
	}
	u.RawQuery = url.Values{"sslmode": {"disable"}, "connect_timeout": {"2"}, "application_name": {"myt-friend-local-fixture"}, "options": {"-c statement_timeout=2000 -c timezone=UTC -c search_path=pg_temp"}}.Encode()
	database, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal("fixture database configuration failed")
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	t.Cleanup(func() { _ = database.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := database.PingContext(ctx); err != nil {
		t.Fatal("explicit local fixture is not reachable")
	}
	// Minimal actual TeslaMate column types and default index shapes, not a
	// reimplementation of all upstream migrations or a production-schema claim.
	fixtureExec(t, database, `CREATE TEMP TABLE cars (id integer PRIMARY KEY);
CREATE TEMP TABLE drives (id integer PRIMARY KEY, car_id integer REFERENCES cars(id), start_date timestamp(6) without time zone, end_date timestamp(6) without time zone);
CREATE TEMP TABLE positions (id bigint PRIMARY KEY, car_id integer REFERENCES cars(id), drive_id integer REFERENCES drives(id), date timestamp(6) without time zone NOT NULL, latitude numeric(8,6), longitude numeric(9,6), speed integer, battery_level integer, rated_battery_range_km numeric(6,2));
CREATE TEMP TABLE states (id integer PRIMARY KEY, car_id integer REFERENCES cars(id), state varchar(255) CHECK(state IN ('online','offline','asleep')), start_date timestamp(6) without time zone, end_date timestamp(6) without time zone);
CREATE TEMP TABLE charging_processes (id integer PRIMARY KEY, car_id integer REFERENCES cars(id), position_id bigint REFERENCES positions(id), start_date timestamp(6) without time zone, end_date timestamp(6) without time zone);
CREATE INDEX ON positions(car_id);
CREATE INDEX ON positions USING brin(date);
CREATE INDEX ON positions USING brin(drive_id,date);
CREATE INDEX ON states(car_id);
CREATE INDEX ON charging_processes(car_id);
CREATE INDEX ON drives(car_id);`)
	return database
}

func fixtureExec(t *testing.T, database *sql.DB, query string, args ...any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := database.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("synthetic SQL fixture: %v", err)
	}
}

func TestFriendLocalPostgresSchemaAndSource(t *testing.T) {
	a := openFriendPostgresFixture(t)
	b := openFriendPostgresFixture(t)
	at := time.Date(2026, 9, 8, 18, 0, 0, 123000000, time.UTC)
	consent := at.Add(-time.Minute).UnixMilli()
	for index, database := range []*sql.DB{a, b} {
		fixtureExec(t, database, `INSERT INTO cars VALUES(1),(2); INSERT INTO states VALUES(1,1,'online','2026-01-01',NULL);`)
		fixtureExec(t, database, `INSERT INTO positions(id,car_id,date,latitude,longitude,speed,battery_level,rated_battery_range_km) VALUES(1,1,$1,$2,-79,0,80,400.25)`, at, 43+index)
	}
	for index, database := range []*sql.DB{a, b} {
		source := &friendTeslaMateSource{database: database}
		key := ft.SourceKey{SourceID: []string{"10000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000001"}[index], CarID: 1}
		out, err := source.ReadSource(context.Background(), key, consent, ft.PermissionSet{Location: true, Battery: true})
		if err != nil || out.Source != key || out.State != ft.StateParked || out.Motion == nil || *out.Motion.ObservedAtMS != at.UnixMilli() || *out.Motion.Latitude != float64(43+index) || *out.Motion.SpeedKPH != 0 || *out.Battery.RatedRangeKM != 400.25 {
			t.Fatalf("two-issuer car1/typed-source isolation: %+v %v", out, err)
		}
		owns, err := source.OwnsCar(context.Background(), 3)
		if err != nil || owns {
			t.Fatal("unknown car became owned")
		}
	}
	source := &friendTeslaMateSource{database: a}
	key := ft.SourceKey{SourceID: "10000000-0000-4000-8000-000000000001", CarID: 1}
	read := func() ft.SourceSnapshot {
		t.Helper()
		out, err := source.ReadSource(context.Background(), key, consent, ft.PermissionSet{Location: true, Battery: true, Trajectory: true})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, state := range []string{"asleep", "offline", "online"} {
		fixtureExec(t, a, `UPDATE states SET state=$1 WHERE id=1`, state)
		out := read()
		want := ft.State(state)
		if state == "online" {
			want = ft.StateParked
		}
		if out.State != want || *out.Motion.ObservedAtMS != at.UnixMilli() {
			t.Fatal("state changed source observation time")
		}
	}
	fixtureExec(t, a, `INSERT INTO drives VALUES(10,1,$1,$2)`, at.Add(-time.Hour), at)
	fixtureExec(t, a, `UPDATE positions SET drive_id=10 WHERE id=1`)
	if read().State != ft.StateParked {
		t.Fatal("closed drive still driving")
	}
	fixtureExec(t, a, `UPDATE drives SET start_date=$1,end_date=NULL WHERE id=10`, at.Add(time.Second))
	if read().State != ft.StateDriving {
		t.Fatal("initial logger-time drive lost exact position membership")
	}
	fixtureExec(t, a, `UPDATE positions SET drive_id=NULL WHERE id=1`)
	fixtureExec(t, a, `INSERT INTO charging_processes VALUES(1,1,1,$1,NULL)`, at.Add(time.Second))
	if read().State != ft.StateCharging {
		t.Fatal("linked charging-start position not recognized")
	}
	fixtureExec(t, a, `UPDATE charging_processes SET end_date=$1 WHERE id=1`, at)
	if read().State != ft.StateParked {
		t.Fatal("charging end boundary not parked")
	}
	out, err := source.ReadSource(context.Background(), key, at.Add(time.Second).UnixMilli(), ft.PermissionSet{Location: true})
	if err != nil || out.Motion != nil || out.State != ft.StateUnknown {
		t.Fatal("preconsent position leaked")
	}
	fixtureExec(t, a, `UPDATE positions SET drive_id=10 WHERE id=1`)
	fixtureExec(t, a, `INSERT INTO positions(id,car_id,drive_id,date,latitude,longitude,speed) SELECT 100+i,1,10,$1::timestamp-i*interval '1 millisecond',43,-79,0 FROM generate_series(1,600) i`, at)
	out = read()
	if len(out.Trajectory) != 500 || out.Trajectory[499].ObservedAtMS != at.UnixMilli() || out.Trajectory[0].ObservedAtMS != at.UnixMilli()-499 {
		t.Fatalf("newest500 consent/order bound: %d", len(out.Trajectory))
	}
	// Directly verify the upper bound with a row committed after motion was read.
	fixtureExec(t, a, `INSERT INTO positions(id,car_id,drive_id,date,latitude,longitude) VALUES(9999,1,10,$1,44,-78)`, at.Add(time.Second))
	rows, err := a.Query(friendTrajectoryQuery, int64(1), int64(10), time.UnixMilli(consent), at)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		var date time.Time
		var lat, lon float64
		if err := rows.Scan(&date, &lat, &lon); err != nil {
			t.Fatal(err)
		}
		if date.After(at) || lat != 43 {
			t.Fatal("future trajectory row leaked")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if count != 500 {
		t.Fatal("SQL row limit changed")
	}
	for _, query := range []string{friendLatestPositionQuery, friendTrajectoryQuery} {
		args := []any{int64(1), time.UnixMilli(consent)}
		if strings.Contains(query, "drive_id=$2") {
			args = []any{int64(1), int64(10), time.UnixMilli(consent), at}
		}
		rows, err := a.Query("EXPLAIN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			t.Log(line)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
}
