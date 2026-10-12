package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

var sqlPoolEnvironmentKeys = []string{"DATABASE_MAX_OPEN_CONNS", "DATABASE_MAX_IDLE_CONNS", "DATABASE_CONN_MAX_IDLE_TIME", "DATABASE_CONN_MAX_LIFETIME"}

func clearSQLPoolEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range sqlPoolEnvironmentKeys {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLPoolConfigurationDefaultsAndValidation(t *testing.T) {
	clearSQLPoolEnvironment(t)
	cfg, err := sqlPoolConfigFromEnvironment()
	if err != nil || cfg != (sqlPoolConfig{MaxOpen: 8, MaxIdle: 2, MaxIdleTime: 5 * time.Minute, MaxLifetime: 30 * time.Minute}) {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	for _, tc := range []struct{ key, value string }{
		{"DATABASE_MAX_OPEN_CONNS", "0"}, {"DATABASE_MAX_OPEN_CONNS", "65"}, {"DATABASE_MAX_OPEN_CONNS", "not-a-number"},
		{"DATABASE_MAX_IDLE_CONNS", "-1"}, {"DATABASE_MAX_IDLE_CONNS", "9"}, {"DATABASE_MAX_IDLE_CONNS", ""},
		{"DATABASE_CONN_MAX_IDLE_TIME", "0"}, {"DATABASE_CONN_MAX_IDLE_TIME", "999ms"}, {"DATABASE_CONN_MAX_IDLE_TIME", "25h"},
		{"DATABASE_CONN_MAX_LIFETIME", "secret-looking-input"}, {"DATABASE_CONN_MAX_LIFETIME", ""},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := sqlPoolConfigFromEnvironment()
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("invalid setting accepted: %v", err)
			}
			if tc.value == "secret-looking-input" && strings.Contains(err.Error(), tc.value) {
				t.Fatal("configuration error echoed input")
			}
		})
	}
	for key, value := range map[string]string{"DATABASE_MAX_OPEN_CONNS": "64", "DATABASE_MAX_IDLE_CONNS": "0", "DATABASE_CONN_MAX_IDLE_TIME": "1s", "DATABASE_CONN_MAX_LIFETIME": "24h"} {
		t.Setenv(key, value)
	}
	cfg, err = sqlPoolConfigFromEnvironment()
	if err != nil || cfg.MaxOpen != 64 || cfg.MaxIdle != 0 || cfg.MaxIdleTime != time.Second || cfg.MaxLifetime != 24*time.Hour {
		t.Fatalf("valid bounds: %+v %v", cfg, err)
	}
	t.Setenv("DATABASE_MAX_IDLE_CONNS", "64")
	if cfg, err := sqlPoolConfigFromEnvironment(); err != nil || cfg.MaxIdle != 64 {
		t.Fatalf("max idle bound: %+v %v", cfg, err)
	}
	t.Setenv("DATABASE_MAX_OPEN_CONNS", "1")
	t.Setenv("DATABASE_MAX_IDLE_CONNS", "1")
	if cfg, err := sqlPoolConfigFromEnvironment(); err != nil || cfg.MaxOpen != 1 || cfg.MaxIdle != 1 {
		t.Fatalf("one-connection bound: %+v %v", cfg, err)
	}
}

func sqlTestDatabase(t *testing.T, query func(context.Context, string, []driver.NamedValue) (driver.Rows, error)) *sql.DB {
	t.Helper()
	database := sql.OpenDB(friendSourceTestConnector{query: query})
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func sqlEmptyRows() driver.Rows { return &friendSourceTestRows{columns: []string{"unused"}} }

func useSQLTestDatabase(t *testing.T, database *sql.DB) {
	t.Helper()
	oldDB, oldLocation := db, location
	db, location = database, time.UTC
	t.Cleanup(func() { db, location = oldDB, oldLocation })
}

func TestSQLPoolActuallyBoundsConcurrentQueries(t *testing.T) {
	started, release := make(chan struct{}, 2), make(chan struct{})
	database := sqlTestDatabase(t, func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
		started <- struct{}{}
		select {
		case <-release:
			return sqlEmptyRows(), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	applySQLPoolConfig(database, sqlPoolConfig{MaxOpen: 2, MaxIdle: 1, MaxIdleTime: time.Second, MaxLifetime: time.Hour})
	finished := make(chan error, 2)
	for range 2 {
		go func() {
			rows, err := database.QueryContext(context.Background(), "synthetic")
			if rows != nil {
				rows.Close()
			}
			finished <- err
		}()
	}
	defer close(release)
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("bounded pool did not start queries")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := database.QueryContext(ctx, "must wait for a connection")
	if !errors.Is(err, context.DeadlineExceeded) || database.Stats().MaxOpenConnections != 2 || database.Stats().OpenConnections != 2 || database.Stats().WaitCount != 1 {
		t.Fatalf("pool did not enforce bound: %v %+v", err, database.Stats())
	}
	// Release and drain before the test closes the DB; no goroutine is left behind.
	release <- struct{}{}
	release <- struct{}{}
	for range 2 {
		if err := <-finished; err != nil {
			t.Fatal(err)
		}
	}
}

func TestSQLFeatureSchemaFailuresAndEarlierCancellation(t *testing.T) {
	for _, code := range []pq.ErrorCode{"42703", "42P01", "42883", "42804", "42501", "08006", "57014"} {
		t.Run(string(code), func(t *testing.T) {
			database := sqlTestDatabase(t, func(ctx context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > sqlFeatureProbeTimeout || !strings.HasSuffix(query, "LIMIT 0") {
					t.Fatal("probe is not bounded or reads source rows")
				}
				return nil, &pq.Error{Code: code, Message: "private database detail"}
			})
			err := checkSQLFeature(context.Background(), database, sqlStatesFeature)
			var failure *sqlFeatureError
			if !errors.As(err, &failure) || failure.Unsupported != (code == "42703" || code == "42P01" || code == "42883" || code == "42804") {
				t.Fatalf("classification: %v", err)
			}
			recorder := httptest.NewRecorder()
			writeSQLFeatureError(recorder, sqlStatesFeature, err)
			want := http.StatusServiceUnavailable
			if failure.Unsupported {
				want = http.StatusNotImplemented
			}
			if recorder.Code != want || strings.Contains(recorder.Body.String(), "private") || !strings.Contains(recorder.Body.String(), `"feature":"states"`) {
				t.Fatalf("unsafe/incorrect response: %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
	database := sqlTestDatabase(t, func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := checkSQLFeature(ctx, database, sqlCurrentDriveFeature)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Fatal("probe ignored request cancellation")
	}
}

func TestSQLCapabilityProbesRunConcurrentlyAndRespectRequestBudget(t *testing.T) {
	started := make(chan struct{}, 4)
	query := func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	mainDB, friendDB := sqlTestDatabase(t, query), sqlTestDatabase(t, query)
	useSQLTestDatabase(t, mainDB)
	oldFriendDB := friendSQLProbeDatabase.Load()
	setFriendSQLProbeDatabase(friendDB)
	t.Cleanup(func() { setFriendSQLProbeDatabase(oldFriendDB) })
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	payload := map[string]any{"capabilities": []string{"parking_state_history", "current_drive_trajectory", "friend_together_v1", "vehicle_software_update_events"}}
	at := time.Now()
	addSQLFeatureCapabilities(ctx, payload)
	if time.Since(at) > 500*time.Millisecond || len(started) != 4 {
		t.Fatalf("probes were serialized or exceeded request budget: started=%d duration=%s", len(started), time.Since(at))
	}
	for feature, item := range payload["sql_features"].(map[string]any) {
		result := item.(map[string]any)
		if result["status"] != "unknown" || result["supported"] != nil {
			t.Fatalf("cancelled %s claimed unsupported/available: %+v", feature, result)
		}
	}
}

func TestSQLCapabilitiesAreFeatureScopedAndVersionIndependent(t *testing.T) {
	for _, schemaErr := range []error{nil, &pq.Error{Code: "42703"}, errors.New("transport unavailable")} {
		t.Run(fmtErrorForTest(schemaErr), func(t *testing.T) {
			database := sqlTestDatabase(t, func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
				if query == sqlStatesSchemaQuery && schemaErr != nil {
					return nil, schemaErr
				}
				if query != sqlStatesSchemaQuery && query != sqlCurrentDriveSchemaQuery && query != sqlMQTTFleetSchemaQuery {
					t.Fatal("unexpected schema query")
				}
				return sqlEmptyRows(), nil
			})
			applySQLPoolConfig(database, sqlPoolConfig{MaxOpen: 8, MaxIdle: 2, MaxIdleTime: time.Minute, MaxLifetime: time.Hour})
			useSQLTestDatabase(t, database)
			payload := map[string]any{"teslamate_version": "unknown-future-version", "capabilities": []string{"parking_state_history", "current_drive_trajectory", "vehicle_software_update_events", "door_detail_status"}}
			addSQLFeatureCapabilities(context.Background(), payload)
			features := payload["sql_features"].(map[string]any)
			states := features[sqlStatesFeature].(map[string]any)
			current := features[sqlCurrentDriveFeature].(map[string]any)
			if current["status"] != "available" || current["supported"] != true {
				t.Fatalf("unrelated feature disabled: %+v", current)
			}
			wantStatus := "available"
			if schemaErr != nil {
				wantStatus = "unknown"
				if _, ok := schemaErr.(*pq.Error); ok {
					wantStatus = "unsupported_schema"
				}
			}
			if states["status"] != wantStatus {
				t.Fatalf("schema status: %+v", states)
			}
			caps := payload["capabilities"].([]string)
			if containsStringForSQLTest(caps, "parking_state_history") != (schemaErr == nil) || !containsStringForSQLTest(caps, "current_drive_trajectory") || !containsStringForSQLTest(caps, "vehicle_software_update_events") || !containsStringForSQLTest(caps, "door_detail_status") || containsStringForSQLTest(caps, "state_history_keyset_pagination_v1") != (schemaErr == nil) {
				t.Fatalf("capability filtering: %v", caps)
			}
			if payload["database_pool"].(map[string]any)["max_open_connections"] != 8 {
				t.Fatal("pool diagnostics missing")
			}
		})
	}
	useSQLTestDatabase(t, nil)
	payload := map[string]any{"capabilities": []string{"parking_state_history"}}
	addSQLFeatureCapabilities(context.Background(), payload)
	states := payload["sql_features"].(map[string]any)[sqlStatesFeature].(map[string]any)
	if states["status"] != "unknown" || states["supported"] != nil {
		t.Fatal("unavailable database claimed unsupported or empty")
	}
}

func TestSQLFriendAndFleetProbesUseActualQueryShapesAndDedicatedPool(t *testing.T) {
	oldFriendDB := friendSQLProbeDatabase.Load()
	t.Cleanup(func() { setFriendSQLProbeDatabase(oldFriendDB) })
	mainDB := sqlTestDatabase(t, func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
		if query != sqlStatesSchemaQuery && query != sqlCurrentDriveSchemaQuery && query != sqlMQTTFleetSchemaQuery {
			t.Fatal("Friend probe used main pool")
		}
		return sqlEmptyRows(), nil
	})
	useSQLTestDatabase(t, mainDB)
	for _, item := range []struct{ name, query string }{{"available", ""}, {"ownership", sqlFriendOwnershipSchemaQuery}, {"latest_position", sqlFriendLatestSchemaQuery}, {"trajectory", sqlFriendTrajectorySchemaQuery}} {
		failingQuery := item.query
		t.Run(item.name, func(t *testing.T) {
			queries := 0
			friendDB := sqlTestDatabase(t, func(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
				queries++
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > sqlFeatureProbeTimeout || !strings.HasSuffix(query, "LIMIT 0") {
					t.Fatal("Friend probe is unbounded")
				}
				if len(args) == 0 || args[0].Value != int64(1) {
					t.Fatal("Friend probe lost safe bound parameters")
				}
				switch query {
				case sqlFriendOwnershipSchemaQuery:
					if len(args) != 1 {
						t.Fatal("ownership shape")
					}
				case sqlFriendLatestSchemaQuery:
					if len(args) != 2 || !args[1].Value.(time.Time).Equal(time.Unix(0, 0)) || !strings.Contains(query, friendLatestPositionQuery) {
						t.Fatal("latest-position shape")
					}
				case sqlFriendTrajectorySchemaQuery:
					if len(args) != 4 || args[1].Value != int64(1) || !strings.Contains(query, friendTrajectoryQuery) {
						t.Fatal("trajectory shape")
					}
				default:
					t.Fatal("unexpected Friend query")
				}
				if query == failingQuery {
					return nil, &pq.Error{Code: "42703"}
				}
				return sqlEmptyRows(), nil
			})
			setFriendSQLProbeDatabase(friendDB)
			payload := map[string]any{"capabilities": []string{"friend_together_v1", "parking_state_history", "current_drive_trajectory", "vehicle_software_update_events"}}
			addSQLFeatureCapabilities(context.Background(), payload)
			feature := payload["sql_features"].(map[string]any)[sqlFriendSourceFeature].(map[string]any)
			wantStatus := "available"
			if failingQuery != "" {
				wantStatus = "unsupported_schema"
			}
			if feature["status"] != wantStatus || containsStringForSQLTest(payload["capabilities"].([]string), "friend_together_v1") != (failingQuery == "") || queries < 1 || (failingQuery == "" && queries != 3) {
				t.Fatalf("Friend source capability: %+v queries=%d", feature, queries)
			}
		})
	}
	setFriendSQLProbeDatabase(nil)
	for _, failure := range []error{&pq.Error{Code: "42703"}, &pq.Error{Code: "42804"}, errors.New("unavailable")} {
		database := sqlTestDatabase(t, func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
			if query == sqlMQTTFleetSchemaQuery {
				return nil, failure
			}
			return sqlEmptyRows(), nil
		})
		useSQLTestDatabase(t, database)
		payload := map[string]any{"capabilities": []string{"friend_together_v1", "vehicle_software_update_events", "current_drive_trajectory", "parking_state_history", "navigation_push_history", "push_subscribers"}}
		addSQLFeatureCapabilities(context.Background(), payload)
		caps := payload["capabilities"].([]string)
		if containsStringForSQLTest(caps, "friend_together_v1") || containsStringForSQLTest(caps, "vehicle_software_update_events") || !containsStringForSQLTest(caps, "parking_state_history") || !containsStringForSQLTest(caps, "current_drive_trajectory") || !containsStringForSQLTest(caps, "navigation_push_history") || !containsStringForSQLTest(caps, "push_subscribers") {
			t.Fatalf("fleet failure disabled independent history/config: %v", caps)
		}
		feature := payload["sql_features"].(map[string]any)[sqlMQTTFleetFeature].(map[string]any)
		if _, ok := failure.(*pq.Error); ok {
			if feature["status"] != "unsupported_schema" || feature["supported"] != false {
				t.Fatalf("fleet missing schema: %+v", feature)
			}
		} else if feature["status"] != "unknown" || feature["supported"] != nil {
			t.Fatalf("fleet connectivity failure: %+v", feature)
		}
	}
	if !strings.Contains(sqlMQTTFleetSchemaQuery, "cs.id = c.settings_id") || !strings.Contains(sqlMQTTFleetSchemaQuery, "cs.enabled") || !strings.Contains(sqlMQTTFleetSchemaQuery, "source.enabled IS TRUE") {
		t.Fatal("fleet probe lost real settings FK or boolean logging flag")
	}
}

func fmtErrorForTest(err error) string {
	if err == nil {
		return "available"
	}
	return err.Error()
}
func containsStringForSQLTest(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestStatesHistoryCursorBoundsBindingAndTamper(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 123456000, time.UTC)
	to := from.Add(24 * time.Hour)
	cursor := statesHistoryCursor{Version: 1, CarID: 2, From: from, To: to, Date: from.Add(-16 * 24 * time.Hour), ID: 7}
	encoded, err := encodeStatesHistoryCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	page, err := parseStatesHistoryPage(url.Values{"limit": {"1"}, "cursor": {encoded}}, 2, from, to)
	if err != nil || page.Limit != 1 || !reflect.DeepEqual(*page.Cursor, cursor) {
		t.Fatalf("overlapping long-park cursor: %+v %v", page, err)
	}
	for _, values := range []url.Values{{"limit": {"0"}}, {"limit": {"2001"}}, {"limit": {""}}, {"limit": {"1", "2"}}, {"cursor": {""}}, {"cursor": {encoded + "x"}}, {"cursor": {encoded, encoded}}} {
		if _, err := parseStatesHistoryPage(values, 2, from, to); err == nil {
			t.Fatalf("invalid pagination accepted: %v", values)
		}
	}
	for _, binding := range []struct {
		car      int
		from, to time.Time
	}{{3, from, to}, {2, from.Add(time.Microsecond), to}, {2, from, to.Add(time.Microsecond)}} {
		if _, err := parseStatesHistoryPage(url.Values{"cursor": {encoded}}, binding.car, binding.from, binding.to); err == nil {
			t.Fatal("cursor replay crossed car/window")
		}
	}
	if page, err := parseStatesHistoryPage(url.Values{}, 2, from, to); err != nil || page != nil {
		t.Fatal("legacy request unexpectedly paginated")
	}
	if page, err := parseStatesHistoryPage(url.Values{"cursor": {encoded}}, 2, from, to); err != nil || page.Limit != 1000 {
		t.Fatal("default page size")
	}
	oldKey := statesHistoryCursorKey
	statesHistoryCursorKey = []byte("synthetic next process key")
	defer func() { statesHistoryCursorKey = oldKey }()
	if _, err := parseStatesHistoryPage(url.Values{"cursor": {encoded}}, 2, from, to); err == nil {
		t.Fatal("cursor survived process key change")
	}
}

func sqlStateTestRows(values ...[]driver.Value) driver.Rows {
	return &friendSourceTestRows{columns: []string{"state", "start_date", "end_date", "name", "start_date_sample", "start_soc", "start_range", "end_before_date", "end_before_soc", "end_before_range", "end_sample_date", "end_soc", "end_range", "id"}, values: values}
}

func TestStatesTemporalConversionAndPagination(t *testing.T) {
	at := time.Date(2026, 10, 1, 12, 0, 0, 123456000, time.UTC)
	longStart, oldSample, end := at.Add(-16*24*time.Hour), at.Add(-16*24*time.Hour-time.Minute), at.Add(time.Hour)
	values := [][]driver.Value{
		{"asleep", longStart, end, "Car", oldSample, nil, []byte("400.25"), at, int64(70), nil, end.Add(time.Microsecond), int64(69), nil, int64(1)},
		{"online", end, end, "Car", nil, nil, nil, nil, nil, nil, nil, nil, nil, int64(2)},
		{"offline", end, nil, "Car", end, nil, nil, at, int64(50), nil, nil, nil, nil, int64(3)},
	}
	queries := 0
	database := sqlTestDatabase(t, func(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		if query == sqlStatesSchemaQuery {
			return sqlEmptyRows(), nil
		}
		if query == `SELECT name FROM cars WHERE id = $1` {
			return &friendSourceTestRows{columns: []string{"name"}, values: [][]driver.Value{{"Car"}}}, nil
		}
		queries++
		if !strings.Contains(query, "ORDER BY s.start_date ASC, s.id ASC") || args[0].Value != int64(2) || !args[1].Value.(time.Time).Equal(at) || !args[2].Value.(time.Time).Equal(at.Add(2*time.Hour)) {
			t.Fatal("history ordering/car/window changed")
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 12*time.Second {
			t.Fatal("history lost timeout")
		}
		if len(args) == 3 {
			if strings.Contains(query, "LIMIT $") {
				t.Fatal("legacy history silently limited")
			}
			return sqlStateTestRows(values...), nil
		}
		if len(args) == 4 {
			if args[3].Value != int64(3) {
				t.Fatal("page does not fetch one extra row")
			}
			return sqlStateTestRows(values...), nil
		}
		if len(args) != 6 || !strings.Contains(query, `(s.start_date, s.id) > ($4, $5)`) || !args[3].Value.(time.Time).Equal(end) || args[4].Value != int64(2) || args[5].Value != int64(3) {
			t.Fatal("equal timestamp keyset lost id tie-break")
		}
		return sqlStateTestRows(values[2]), nil
	})
	useSQLTestDatabase(t, database)
	data, err := fetchStates(context.Background(), 2, at, at.Add(2*time.Hour))
	if err != nil || len(data.States) != 3 || data.Car.CarName == nil {
		t.Fatalf("legacy history: %+v %v", data, err)
	}
	sleep := data.States[0]
	if sleep.StartDate != longStart.Format(time.RFC3339Nano) || *sleep.EndDate != end.Format(time.RFC3339Nano) || sleep.StartTelemetry.Date != oldSample.Format(time.RFC3339Nano) || sleep.StartTelemetry.BatteryLevel != nil || *sleep.StartTelemetry.RatedRangeKM != 400.25 || sleep.EndTelemetry.Date != end.Add(time.Microsecond).Format(time.RFC3339Nano) {
		t.Fatalf("source time/null/precision lost: %+v", sleep)
	}
	if data.States[1].StartTelemetry != nil || data.States[1].EndTelemetry != nil || data.States[1].StartDate != *data.States[1].EndDate || data.States[2].EndDate != nil || data.States[2].EndTelemetry != nil {
		t.Fatal("missing/open/equal state boundary fabricated telemetry")
	}
	data, page, err := fetchStatesPage(context.Background(), 2, at, at.Add(2*time.Hour), &statesHistoryPageRequest{Limit: 2})
	if err != nil || len(data.States) != 2 || !page.HasMore || page.ReturnedCount != 2 || page.NextCursor == nil {
		t.Fatalf("first page: %+v %+v %v", data, page, err)
	}
	request, err := parseStatesHistoryPage(url.Values{"limit": {"2"}, "cursor": {*page.NextCursor}}, 2, at, at.Add(2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	data, page, err = fetchStatesPage(context.Background(), 2, at, at.Add(2*time.Hour), request)
	if err != nil || len(data.States) != 1 || page.HasMore || page.NextCursor != nil || queries != 3 {
		t.Fatalf("last page: %+v %+v %v", data, page, err)
	}
}

func TestSQLUnknownCarAndKnownEmptyHistoryAreDistinct(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(strconvBoolForSQLTest(exists), func(t *testing.T) {
			database := sqlTestDatabase(t, func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
				if query == sqlStatesSchemaQuery || query == sqlCurrentDriveSchemaQuery {
					return sqlEmptyRows(), nil
				}
				if query == `SELECT name FROM cars WHERE id = $1` {
					rows := &friendSourceTestRows{columns: []string{"name"}}
					if exists {
						rows.values = [][]driver.Value{{nil}}
					}
					return rows, nil
				}
				if strings.Contains(query, "FROM states s") {
					return sqlStateTestRows(), nil
				}
				return &friendSourceTestRows{columns: []string{"id", "start_date", "end_date"}}, nil
			})
			useSQLTestDatabase(t, database)
			states, err := fetchStates(context.Background(), 99, time.Now().Add(-time.Hour), time.Now())
			if (exists && err != nil) || (!exists && !errors.Is(err, errSQLCarNotFound)) || len(states.States) != 0 {
				t.Fatalf("unknown/empty history: %+v %v", states, err)
			}
			drive, err := fetchCurrentDrive(context.Background(), 99, 0, 5)
			if (exists && err != nil) || (!exists && !errors.Is(err, errSQLCarNotFound)) || drive.Drive != nil {
				t.Fatalf("unknown/no current drive: %+v %v", drive, err)
			}
			if !exists {
				recorder := httptest.NewRecorder()
				writeSQLFeatureError(recorder, sqlCurrentDriveFeature, err)
				if recorder.Code != http.StatusNotFound {
					t.Fatal("unknown car did not return 404")
				}
			}
		})
	}
}

func TestStatesFullHistoricalWindowExceedingPageLimitIsNeverTruncated(t *testing.T) {
	from, to := time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	const total = 2505
	values := make([][]driver.Value, total)
	for index := range values {
		values[index] = []driver.Value{"online", from, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, int64(index + 1)}
	}
	database := sqlTestDatabase(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		if query == sqlStatesSchemaQuery {
			return sqlEmptyRows(), nil
		}
		if query == `SELECT name FROM cars WHERE id = $1` {
			return &friendSourceTestRows{columns: []string{"name"}, values: [][]driver.Value{{nil}}}, nil
		}
		if !args[1].Value.(time.Time).Equal(from) || !args[2].Value.(time.Time).Equal(to) {
			t.Fatal("historical window was capped")
		}
		if len(args) == 3 {
			if strings.Contains(query, "LIMIT $") {
				t.Fatal("legacy full history capped")
			}
			return sqlStateTestRows(values...), nil
		}
		if len(args) == 4 {
			if args[3].Value != int64(2001) {
				t.Fatal("wrong max page look-ahead")
			}
			return sqlStateTestRows(values[:2001]...), nil
		}
		if len(args) != 6 || args[4].Value != int64(2000) || args[5].Value != int64(2001) {
			t.Fatal("large history continuation lost exact row")
		}
		return sqlStateTestRows(values[2000:]...), nil
	})
	useSQLTestDatabase(t, database)
	complete, err := fetchStates(context.Background(), 1, from, to)
	if err != nil || len(complete.States) != total {
		t.Fatalf("legacy full history: count=%d %v", len(complete.States), err)
	}
	part, page, err := fetchStatesPage(context.Background(), 1, from, to, &statesHistoryPageRequest{Limit: statesHistoryMaxPageLimit})
	if err != nil || len(part.States) != 2000 || !page.HasMore {
		t.Fatalf("bounded first page: %+v %v", page, err)
	}
	request, err := parseStatesHistoryPage(url.Values{"limit": {"2000"}, "cursor": {*page.NextCursor}}, 1, from, to)
	if err != nil {
		t.Fatal(err)
	}
	last, page, err := fetchStatesPage(context.Background(), 1, from, to, request)
	if err != nil || len(last.States) != 505 || page.HasMore || !reflect.DeepEqual(append(part.States, last.States...), complete.States) {
		t.Fatalf("full history lost rows: %+v %v", page, err)
	}
}

func strconvBoolForSQLTest(value bool) string {
	if value {
		return "known"
	}
	return "unknown"
}

func TestSQLHandlersReturnStructuredFeatureFailuresAndPreserveLegacyShape(t *testing.T) {
	oldToken, oldProbe := apiToken, authProbeURL
	apiToken, authProbeURL = "synthetic-test-token", ""
	t.Cleanup(func() { apiToken, authProbeURL = oldToken, oldProbe })
	for _, feature := range []string{sqlStatesFeature, sqlCurrentDriveFeature} {
		for _, mode := range []string{"unsupported_probe", "unsupported_after_probe", "unavailable", "unknown_car", "empty", "paged"} {
			t.Run(feature+"/"+mode, func(t *testing.T) {
				database := sqlTestDatabase(t, func(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
					if query == sqlStatesSchemaQuery || query == sqlCurrentDriveSchemaQuery {
						if mode == "unsupported_probe" {
							return nil, &pq.Error{Code: "42703", Message: "private SQL detail"}
						}
						if mode == "unavailable" {
							return nil, &pq.Error{Code: "42501", Message: "private permission detail"}
						}
						return sqlEmptyRows(), nil
					}
					if query == `SELECT name FROM cars WHERE id = $1` {
						rows := &friendSourceTestRows{columns: []string{"name"}}
						if mode != "unknown_car" {
							rows.values = [][]driver.Value{{"Synthetic car"}}
						}
						return rows, nil
					}
					if mode == "unsupported_after_probe" {
						return nil, &pq.Error{Code: "42P01", Message: "private SQL detail"}
					}
					if strings.Contains(query, "FROM states s") {
						return sqlStateTestRows(), nil
					}
					return &friendSourceTestRows{columns: []string{"id", "start_date", "end_date"}}, nil
				})
				useSQLTestDatabase(t, database)
				path := "/api/v1/cars/1/states?startDate=2026-09-01&endDate=2026-10-01"
				if feature == sqlCurrentDriveFeature {
					path = "/api/v1/cars/1/navigation/current-drive"
				}
				if mode == "paged" {
					path += "&limit=2"
					if feature == sqlCurrentDriveFeature {
						path = "/api/v1/cars/1/navigation/current-drive?limit=2"
					}
				}
				request := httptest.NewRequest(http.MethodGet, path, nil)
				request.Header.Set("Authorization", "Bearer synthetic-test-token")
				recorder := httptest.NewRecorder()
				handleStates(recorder, request)
				wantStatus, wantCode := http.StatusOK, ""
				switch mode {
				case "unsupported_probe", "unsupported_after_probe":
					wantStatus, wantCode = http.StatusNotImplemented, feature+"_unsupported"
				case "unavailable":
					wantStatus, wantCode = http.StatusServiceUnavailable, feature+"_unavailable"
				case "unknown_car":
					wantStatus, wantCode = http.StatusNotFound, "car_not_found"
				}
				if recorder.Code != wantStatus || strings.Contains(recorder.Body.String(), "private") {
					t.Fatalf("response: %d %s", recorder.Code, recorder.Body.String())
				}
				if wantCode != "" {
					var failure map[string]string
					if err := json.Unmarshal(recorder.Body.Bytes(), &failure); err != nil || failure["feature"] != feature || failure["error"] != wantCode {
						t.Fatalf("unstructured response: %s", recorder.Body.String())
					}
				} else if feature == sqlStatesFeature {
					var result envelope
					if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil || result.Data.States == nil || result.Data.Car.CarName == nil {
						t.Fatalf("empty result lost legacy shape: %s", recorder.Body.String())
					}
					if (result.Meta.StateHistoryPage != nil) != (mode == "paged") {
						t.Fatalf("unexpected page metadata: %+v", result.Meta)
					}
				}
			})
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/cars/1/states?startDate=2026-09-01&endDate=2026-10-01&limit=0", nil)
	request.Header.Set("Authorization", "Bearer synthetic-test-token")
	recorder := httptest.NewRecorder()
	handleStates(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid optional page limit: %d", recorder.Code)
	}
}

func TestTelemetryNullAndNonFiniteAreNotZeroOrFresh(t *testing.T) {
	oldLocation := location
	location = time.UTC
	defer func() { location = oldLocation }()
	at := time.Date(2026, 9, 1, 0, 0, 0, 123456000, time.UTC)
	if makeTelemetrySample(sql.NullTime{}, sql.NullInt64{Valid: true, Int64: 50}, sql.NullFloat64{}) != nil {
		t.Fatal("missing sample time fabricated observation")
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		sample := makeTelemetrySample(sql.NullTime{Valid: true, Time: at}, sql.NullInt64{}, sql.NullFloat64{Valid: true, Float64: value})
		if sample.Date != at.Format(time.RFC3339Nano) || sample.BatteryLevel != nil || sample.RatedRangeKM != nil {
			t.Fatal("nonfinite/null sample became zero or fresh")
		}
		if _, err := json.Marshal(sample); err != nil {
			t.Fatal("nonfinite sample broke JSON")
		}
	}
}

func TestCurrentDrivePreservesInitialLoggerAndPositionTimes(t *testing.T) {
	at := time.Date(2026, 10, 1, 0, 0, 0, 123456000, time.UTC)
	positionAt := at.Add(-time.Second)
	database := sqlTestDatabase(t, func(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		if query == sqlCurrentDriveSchemaQuery {
			return sqlEmptyRows(), nil
		}
		if query == `SELECT name FROM cars WHERE id = $1` {
			return &friendSourceTestRows{columns: []string{"name"}, values: [][]driver.Value{{"Car"}}}, nil
		}
		if strings.Contains(query, "FROM drives") {
			return &friendSourceTestRows{columns: []string{"id", "start_date", "end_date"}, values: [][]driver.Value{{int64(7), at, nil}}}, nil
		}
		if args[0].Value != int64(7) || !strings.Contains(query, "WHERE drive_id = $1") {
			t.Fatal("drive points not tied to exact drive")
		}
		return &friendSourceTestRows{columns: []string{"id", "date", "latitude", "longitude", "speed", "odometer"}, values: [][]driver.Value{{int64(10), positionAt, []byte("43.123456"), []byte("-79.654321"), nil, nil}}}, nil
	})
	useSQLTestDatabase(t, database)
	data, err := fetchCurrentDrive(context.Background(), 2, 0, 5)
	if err != nil || data.Drive == nil || data.Drive.StartDate != at.Format(time.RFC3339Nano) || data.Drive.FirstPoint.Date != positionAt.Format(time.RFC3339Nano) || data.Drive.Points[0].Speed != nil || data.Drive.Points[0].Odometer != nil {
		t.Fatalf("logger/position times or nulls changed: %+v %v", data, err)
	}
}

// This optional gate executes the production SELECT against disposable TEMP
// tables only. It never accepts DATABASE_* or a non-loopback database URL.
func TestSQLLocalPostgresTemporalStatesAndPagination(t *testing.T) {
	database := openFriendPostgresFixture(t)
	fixtureExec(t, database, `ALTER TABLE pg_temp.cars ADD COLUMN name varchar(255); ALTER TABLE pg_temp.cars ADD COLUMN settings_id integer; CREATE TEMP TABLE car_settings(id integer PRIMARY KEY, enabled boolean); ALTER TABLE pg_temp.positions ADD COLUMN odometer numeric(8,2);`)
	useSQLTestDatabase(t, database)
	fixtureExec(t, database, `INSERT INTO cars VALUES(1,'Synthetic fixture',11),(2,NULL,22); INSERT INTO car_settings VALUES(11,true),(22,false);
INSERT INTO states VALUES(1,1,'asleep','2026-09-01 12:00:00.123456','2026-09-17 12:00:00.123456'),(2,1,'online','2026-09-17 12:00:00.123456','2026-09-17 12:00:00.123456'),(3,1,'offline','2026-09-17 12:00:00.123456',NULL);
INSERT INTO positions(id,car_id,date,battery_level,rated_battery_range_km) VALUES
(1,1,'2026-09-01 11:00:00',99,499),
(2,1,'2026-09-01 11:59:00.123456',NULL,400.25),
(3,1,'2026-09-17 12:00:00.123457',70,NULL),
(4,1,'2026-09-17 12:00:00.123457',69,NULL);`)
	from := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	data, err := fetchStates(context.Background(), 1, from, to)
	if err != nil || len(data.States) != 3 || data.States[0].StartDate != "2026-09-01T12:00:00.123456Z" || data.States[0].StartTelemetry.Date != "2026-09-01T11:59:00.123456Z" || data.States[0].StartTelemetry.BatteryLevel != nil || *data.States[0].EndTelemetry.BatteryLevel != 70 || data.States[1].StartTelemetry != nil || data.States[1].EndTelemetry != nil || data.States[2].EndTelemetry != nil {
		t.Fatalf("actual SQL temporal selection: %+v %v", data, err)
	}
	var all []stateInterval
	pageRequest := &statesHistoryPageRequest{Limit: 1}
	for range 4 {
		part, page, err := fetchStatesPage(context.Background(), 1, from, to, pageRequest)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, part.States...)
		if !page.HasMore {
			break
		}
		pageRequest, err = parseStatesHistoryPage(url.Values{"limit": {"1"}, "cursor": {*page.NextCursor}}, 1, from, to)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(all, data.States) {
		t.Fatal("actual SQL pagination lost long interval or equal start timestamp row")
	}
	if _, err := fetchStates(context.Background(), 999, from, to); !errors.Is(err, errSQLCarNotFound) {
		t.Fatal("actual SQL unknown car")
	}
	known, err := fetchStates(context.Background(), 2, from, to)
	if err != nil || len(known.States) != 0 {
		t.Fatal("known car without history is not empty")
	}
	for _, feature := range []string{sqlFriendSourceFeature, sqlMQTTFleetFeature} {
		if err := checkSQLFeature(context.Background(), database, feature); err != nil {
			t.Fatalf("actual SQL source shape %s: %v", feature, err)
		}
	}
	fixtureExec(t, database, `ALTER TABLE pg_temp.car_settings DROP COLUMN enabled`)
	err = checkSQLFeature(context.Background(), database, sqlMQTTFleetFeature)
	var fleetUnsupported *sqlFeatureError
	if !errors.As(err, &fleetUnsupported) || !fleetUnsupported.Unsupported {
		t.Fatalf("actual SQL missing logging flag: %v", err)
	}
	fixtureExec(t, database, `ALTER TABLE pg_temp.positions DROP COLUMN rated_battery_range_km`)
	err = checkSQLFeature(context.Background(), database, sqlStatesFeature)
	var unsupported *sqlFeatureError
	if !errors.As(err, &unsupported) || !unsupported.Unsupported {
		t.Fatalf("actual SQL missing column was not unsupported: %v", err)
	}
	err = checkSQLFeature(context.Background(), database, sqlFriendSourceFeature)
	if !errors.As(err, &unsupported) || !unsupported.Unsupported {
		t.Fatalf("actual SQL Friend missing range: %v", err)
	}
	if err := checkSQLFeature(context.Background(), database, sqlCurrentDriveFeature); err != nil {
		t.Fatalf("unrelated actual SQL current drive disabled: %v", err)
	}
}
