package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	ft "mycarmate-states-api/internal/friendtogether"
)

// This driver tests Go conversion/arguments/deadlines, NOT PostgreSQL SQL
// execution. The opt-in local PostgreSQL test is the separate SQL gate.
type friendSourceTestConnector struct {
	query func(context.Context, string, []driver.NamedValue) (driver.Rows, error)
}

func (c friendSourceTestConnector) Connect(context.Context) (driver.Conn, error) {
	return friendSourceTestConn{c}, nil
}
func (c friendSourceTestConnector) Driver() driver.Driver { return friendSourceTestDriver{c} }

type friendSourceTestDriver struct{ connector friendSourceTestConnector }

func (d friendSourceTestDriver) Open(string) (driver.Conn, error) {
	return friendSourceTestConn{d.connector}, nil
}

type friendSourceTestConn struct{ connector friendSourceTestConnector }

func (c friendSourceTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not supported")
}
func (c friendSourceTestConn) Begin() (driver.Tx, error) { return nil, errors.New("not supported") }
func (c friendSourceTestConn) Close() error              { return nil }
func (c friendSourceTestConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.connector.query(ctx, q, args)
}

type friendSourceTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *friendSourceTestRows) Columns() []string { return r.columns }
func (r *friendSourceTestRows) Close() error      { return nil }
func (r *friendSourceTestRows) Next(dest []driver.Value) error {
	if r.index == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}

func TestFriendSourceTimestampTypedNullsAndTraceBounds(t *testing.T) {
	at := time.Date(2026, 9, 8, 18, 0, 0, 123456000, time.UTC)
	consent := at.Add(-time.Minute).UnixMilli()
	key := ft.SourceKey{SourceID: "10000000-0000-4000-8000-000000000001", CarID: 1}
	for _, state := range []string{"parked", "asleep", "offline", "charging", "driving", "unknown"} {
		t.Run(state, func(t *testing.T) {
			queries := 0
			database := sql.OpenDB(friendSourceTestConnector{query: func(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > 2*time.Second {
					t.Fatal("source lost own deadline")
				}
				queries++
				if q == friendLatestPositionQuery {
					if len(args) != 2 || args[0].Value != int64(1) || args[1].Value.(time.Time).UnixMilli() != consent {
						t.Fatal("latest query lost car/consent")
					}
					return &friendSourceTestRows{columns: []string{"date", "latitude", "longitude", "speed", "soc", "range", "drive_id", "state"}, values: [][]driver.Value{{at, []byte("43.123456"), []byte("-79.654321"), int64(0), nil, []byte("400.25"), int64(10), state}}}, nil
				}
				if q != friendTrajectoryQuery || len(args) != 4 || args[0].Value != int64(1) || args[1].Value != int64(10) || args[2].Value.(time.Time).UnixMilli() != consent || !args[3].Value.(time.Time).Equal(at) {
					t.Fatal("trace lost exact car/drive/consent/selected-motion bound")
				}
				return &friendSourceTestRows{columns: []string{"date", "latitude", "longitude"}, values: [][]driver.Value{{at, []byte("43.2"), []byte("-79")}, {at.Add(-time.Microsecond), []byte("43.1"), []byte("-79")}, {at.Add(-time.Second), []byte("43"), []byte("-79")}}}, nil
			}})
			defer database.Close()
			source := friendTeslaMateSource{database: database}
			out, err := source.ReadSource(context.Background(), key, consent, ft.PermissionSet{Location: true, Battery: true, Trajectory: true})
			if err != nil || out.Source != key || out.State != ft.State(state) || *out.Motion.ObservedAtMS != at.UnixMilli() || *out.Motion.SpeedKPH != 0 || out.Motion.HeadingDegrees != nil || out.Battery.Percentage != nil || *out.Battery.RatedRangeKM != 400.25 {
				t.Fatalf("typed source: %+v %v", out, err)
			}
			if queries != 2 || len(out.Trajectory) != 2 || out.Trajectory[0].ObservedAtMS != at.Add(-time.Second).UnixMilli() || out.Trajectory[1].Latitude != 43.2 {
				t.Fatal("trace timestamp/order/newest-id dedup")
			}
		})
	}
}

func TestFriendSourceReadAndOwnershipHonorEarlierCancellation(t *testing.T) {
	database := sql.OpenDB(friendSourceTestConnector{query: func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	defer database.Close()
	source := friendTeslaMateSource{database: database}
	for _, owns := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		started := time.Now()
		var err error
		if owns {
			_, err = source.OwnsCar(ctx, 1)
		} else {
			_, err = source.ReadSource(ctx, ft.SourceKey{CarID: 1}, 1, ft.PermissionSet{Location: true})
		}
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > 500*time.Millisecond {
			t.Fatal("source ignored earlier deadline")
		}
	}
}

func TestFriendDatabasePoolIsSeparateBoundedAndLazy(t *testing.T) {
	t.Setenv("DATABASE_HOST", "127.0.0.1")
	t.Setenv("DATABASE_PORT", "1")
	t.Setenv("DATABASE_USER", "synthetic ' user")
	t.Setenv("DATABASE_PASS", "synthetic ' value -c not_an_option")
	dsn := friendDatabaseDSN()
	if strings.Contains(dsn, "postgres:") || !strings.Contains(dsn, "password='synthetic '' value -c not_an_option'") {
		t.Fatal("friend DSN must quote values instead of a postgres:? URL")
	}
	_, err := openFriendDatabase()
	if err == nil {
		t.Fatal("closed local port must fail the friend-pool ping")
	}
}
