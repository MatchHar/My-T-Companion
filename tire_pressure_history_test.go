package main

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
)

var tireTestKey = []byte("synthetic-test-only-cursor-key-001")

func tireTestRequest(t *testing.T) tireHistoryRequest {
	t.Helper()
	q, err := parseTireHistoryRequest("1", url.Values{"from": {"2026-08-20T12:00:00Z"}, "to": {"2026-09-20T12:00:00Z"}}, tireTestKey)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestTireHistoryRequestBoundsAndParameters(t *testing.T) {
	q := tireTestRequest(t)
	if q.To.Sub(q.From) != tireHistoryMaxWindow || q.Limit != 1000 {
		t.Fatal("exact31-day/default bounds changed")
	}
	for _, tc := range []struct{ name, car, field, value string }{
		{"zero car", "0", "", ""}, {"negative car", "-1", "", ""}, {"overflow car", "99999999999999999999", "", ""}, {"injection car", "1 OR TRUE", "", ""},
		{"outside PostgreSQL car range", "2147483648", "", ""},
		{"missing from", "1", "from", ""}, {"invalid from", "1", "from", "yesterday"}, {"no timezone", "1", "from", "2026-08-21T12:00:00"},
		{"missing to", "1", "to", ""}, {"invalid to", "1", "to", "2026-09-31T12:00:00Z"}, {"same bounds", "1", "to", "2026-08-20T12:00:00Z"},
		{"reverse bounds", "1", "to", "2026-08-19T12:00:00Z"}, {"too wide", "1", "to", "2026-09-20T12:00:00.000001Z"},
		{"zero limit", "1", "limit", "0"}, {"negative limit", "1", "limit", "-1"}, {"large limit", "1", "limit", "2001"}, {"fractional limit", "1", "limit", "1.5"},
		{"overflow limit", "1", "limit", "99999999999999999999"}, {"unknown parameter", "1", "latitude", "1"}, {"invalid cursor", "1", "cursor", "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			values := url.Values{"from": {q.From.Format(time.RFC3339Nano)}, "to": {q.To.Format(time.RFC3339Nano)}}
			if tc.field != "" {
				values.Set(tc.field, tc.value)
			}
			if _, err := parseTireHistoryRequest(tc.car, values, tireTestKey); err == nil {
				t.Fatal("accepted invalid request")
			}
		})
	}
	for _, limit := range []string{"1", "2000"} {
		values := url.Values{"from": {"2026-08-20T08:00:00-04:00"}, "to": {"2026-09-20T08:00:00-04:00"}, "limit": {limit}}
		parsed, err := parseTireHistoryRequest("1", values, tireTestKey)
		if err != nil || !parsed.From.Equal(q.From) || parsed.From.Location() != time.UTC {
			t.Fatalf("timezone/unit boundary: %+v %v", parsed, err)
		}
		values["from"] = append(values["from"], values.Get("from"))
		if _, err := parseTireHistoryRequest("1", values, tireTestKey); err == nil {
			t.Fatal("duplicate parameter accepted")
		}
	}
}

func TestTireHistoryCursorScopeAndTampering(t *testing.T) {
	q := tireTestRequest(t)
	cursor := tireHistoryCursor{Version: 1, CarID: 1, From: q.From, To: q.To, Date: q.From.Add(time.Microsecond), ID: 42}
	raw, err := encodeTireHistoryCursor(cursor, tireTestKey)
	if err != nil {
		t.Fatal(err)
	}
	values := url.Values{"from": {q.From.Format(time.RFC3339Nano)}, "to": {q.To.Format(time.RFC3339Nano)}, "cursor": {raw}}
	parsed, err := parseTireHistoryRequest("1", values, tireTestKey)
	if err != nil || parsed.Cursor.ID != 42 || !parsed.Cursor.Date.Equal(cursor.Date) {
		t.Fatalf("cursor precision lost: %+v %v", parsed, err)
	}
	if _, err := parseTireHistoryRequest("2", values, tireTestKey); err == nil {
		t.Fatal("cursor crossed cars")
	}
	if _, err := parseTireHistoryRequest("1", values, []byte("another-source-key")); err == nil {
		t.Fatal("cursor crossed sources/processes")
	}
	values.Set("from", q.From.Add(time.Second).Format(time.RFC3339Nano))
	if _, err := parseTireHistoryRequest("1", values, tireTestKey); err == nil {
		t.Fatal("cursor crossed window")
	}
	values.Set("from", q.From.Format(time.RFC3339Nano))
	for _, change := range []func(*tireHistoryCursor){
		func(c *tireHistoryCursor) { c.Version = 2 }, func(c *tireHistoryCursor) { c.ID = 0 },
		func(c *tireHistoryCursor) { c.Date = q.From.Add(-time.Nanosecond) }, func(c *tireHistoryCursor) { c.Date = q.To },
	} {
		bad := cursor
		change(&bad)
		encoded, _ := encodeTireHistoryCursor(bad, tireTestKey)
		values.Set("cursor", encoded)
		if _, err := parseTireHistoryRequest("1", values, tireTestKey); err == nil {
			t.Fatal("invalid signed boundary accepted")
		}
	}
	parts := strings.Split(raw, ".")
	tampered := base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"car":2}`)) + "." + parts[1]
	for _, bad := range []string{tampered, raw + "x", strings.Repeat("x", 1025), "..", "a.%%%"} {
		if _, err := decodeTireHistoryCursor(bad, tireTestKey); err == nil {
			t.Fatal("tampered/oversized cursor accepted")
		}
	}
}

func tireHistoryFakeDB(t *testing.T, schemaErr error, exists bool, query func(context.Context, string, []driver.NamedValue) (driver.Rows, error)) *sql.DB {
	t.Helper()
	database := sql.OpenDB(friendSourceTestConnector{query: func(ctx context.Context, statement string, args []driver.NamedValue) (driver.Rows, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > tireHistoryQueryTimeout {
			t.Fatal("history query has no own bounded deadline")
		}
		if !strings.HasPrefix(statement, "SELECT ") {
			t.Fatal("non-read-only query")
		}
		switch statement {
		case tireHistorySchemaQuery:
			if len(args) != 0 {
				t.Fatal("schema query unexpected args")
			}
			return &friendSourceTestRows{columns: []string{"id"}}, schemaErr
		case tireHistoryCarQuery:
			if len(args) != 1 || args[0].Value != int64(1) {
				t.Fatal("car ownership query lost scope")
			}
			return &friendSourceTestRows{columns: []string{"exists"}, values: [][]driver.Value{{exists}}}, nil
		default:
			if query == nil {
				t.Fatal("unexpected history query")
			}
			return query(ctx, statement, args)
		}
	}})
	t.Cleanup(func() { _ = database.Close() })
	return database
}

var tireTestColumns = []string{"id", "car_id", "date", "fl", "fr", "rl", "rr", "outside", "speed", "drive"}

func tireTestRow(id int64, car int64, at time.Time) []driver.Value {
	return []driver.Value{id, car, at, []byte("2.875"), nil, []byte("0"), []byte("2.90"), []byte("-12.75"), int64(0), nil}
}

func TestTireHistoryKeysetPaginationPreservesEveryScopedRecord(t *testing.T) {
	q := tireTestRequest(t)
	q.Limit = 2
	at := q.From.Add(24 * time.Hour)
	fixture := [][]driver.Value{tireTestRow(1, 1, at), tireTestRow(2, 1, at), tireTestRow(3, 1, at), tireTestRow(4, 1, at.Add(time.Microsecond)), tireTestRow(5, 1, q.From), tireTestRow(6, 1, q.To), tireTestRow(7, 1, q.From.Add(-time.Microsecond)), tireTestRow(8, 2, at)}
	sort.Slice(fixture, func(i, j int) bool {
		a, b := fixture[i], fixture[j]
		if a[2].(time.Time).Equal(b[2].(time.Time)) {
			return a[0].(int64) > b[0].(int64)
		}
		return a[2].(time.Time).After(b[2].(time.Time))
	})
	queries := 0
	database := tireHistoryFakeDB(t, nil, true, func(_ context.Context, statement string, args []driver.NamedValue) (driver.Rows, error) {
		queries++
		want := tireHistoryQuery + ` ORDER BY date DESC, id DESC LIMIT $4`
		if len(args) == 6 {
			want = tireHistoryQuery + ` AND (date, id) < ($4, $5) ORDER BY date DESC, id DESC LIMIT $6`
		}
		if statement != want || args[0].Value != int64(1) || !args[1].Value.(time.Time).Equal(q.From) || !args[2].Value.(time.Time).Equal(q.To) || args[len(args)-1].Value != int64(3) {
			t.Fatalf("query lost parameterized scope/keyset/limit+1: %s", statement)
		}
		selected := [][]driver.Value{}
		for _, row := range fixture {
			date, id := row[2].(time.Time), row[0].(int64)
			if row[1].(int64) != 1 || date.Before(q.From) || !date.Before(q.To) {
				continue
			}
			if len(args) == 6 {
				cursorDate, cursorID := args[3].Value.(time.Time), args[4].Value.(int64)
				if date.After(cursorDate) || (date.Equal(cursorDate) && id >= cursorID) {
					continue
				}
			}
			selected = append(selected, row)
			if len(selected) == 3 {
				break
			}
		}
		return &friendSourceTestRows{columns: tireTestColumns, values: selected}, nil
	})
	ids := []int64{}
	for {
		data, err := fetchTireHistory(context.Background(), database, q, tireTestKey)
		if err != nil || data.ReturnedCount != len(data.Points) {
			t.Fatalf("fetch: %+v %v", data, err)
		}
		for _, point := range data.Points {
			ids = append(ids, point.PositionID)
			if point.CarID != 1 || point.SensorMeasuredAt != nil {
				t.Fatal("car/time identity fabricated")
			}
		}
		if !data.HasMore {
			if data.NextCursor != nil {
				t.Fatal("terminal cursor should be null")
			}
			break
		}
		if data.NextCursor == nil {
			t.Fatal("missing next cursor")
		}
		cursor, err := decodeTireHistoryCursor(*data.NextCursor, tireTestKey)
		if err != nil {
			t.Fatal(err)
		}
		q.Cursor = &cursor
		if queries > 3 {
			t.Fatal("pagination did not terminate")
		}
	}
	if queries != 3 || len(ids) != 5 || ids[0] != 4 || ids[1] != 3 || ids[2] != 2 || ids[3] != 1 || ids[4] != 5 {
		t.Fatalf("lost/duplicated/reordered records: %v", ids)
	}
}

func TestTireHistoryNullZeroPrecisionAndNonfiniteValues(t *testing.T) {
	q := tireTestRequest(t)
	row := tireTestRow(42, 1, q.From.Add(time.Microsecond))
	database := tireHistoryFakeDB(t, nil, true, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		return &friendSourceTestRows{columns: tireTestColumns, values: [][]driver.Value{row}}, nil
	})
	data, err := fetchTireHistory(context.Background(), database, q, tireTestKey)
	if err != nil {
		t.Fatal(err)
	}
	point := data.Points[0]
	if *point.PressureFL != 2.875 || point.PressureFR != nil || *point.PressureRL != 0 || *point.OutsideTemp != -12.75 || *point.Speed != 0 || point.DriveID != nil || point.SensorMeasuredAt != nil {
		t.Fatalf("source precision/null/zero changed: %+v", point)
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
		row[3], row[4], row[5], row[6], row[8] = value, value, value, value, value
		row[7] = math.NaN()
		row[9] = int64(-1)
		data, err = fetchTireHistory(context.Background(), database, q, tireTestKey)
		if err != nil {
			t.Fatal(err)
		}
		point = data.Points[0]
		if point.PressureFL != nil || point.PressureFR != nil || point.PressureRL != nil || point.PressureRR != nil || point.OutsideTemp != nil || point.Speed != nil || point.DriveID != nil {
			t.Fatal("invalid source value survived")
		}
		encoded, err := json.Marshal(data)
		if err != nil || !strings.Contains(string(encoded), `"sensor_measured_at":null`) {
			t.Fatal("invalid number or fabricated sensor clock in JSON")
		}
	}
}

func TestTireHistoryUnsupportedUnavailableAndUnknownCar(t *testing.T) {
	q := tireTestRequest(t)
	for _, code := range []pq.ErrorCode{"42703", "42P01"} {
		database := tireHistoryFakeDB(t, &pq.Error{Code: code}, true, nil)
		if _, err := fetchTireHistory(context.Background(), database, q, tireTestKey); !errors.Is(err, errTireHistoryUnsupported) {
			t.Fatalf("old schema not safely unsupported: %v", err)
		}
	}
	database := tireHistoryFakeDB(t, nil, false, nil)
	if _, err := fetchTireHistory(context.Background(), database, q, tireTestKey); !errors.Is(err, errTireHistoryCarNotFound) {
		t.Fatalf("unknown car: %v", err)
	}
	if _, err := fetchTireHistory(context.Background(), nil, q, tireTestKey); err == nil || errors.Is(err, errTireHistoryUnsupported) {
		t.Fatal("unavailable DB not distinguished from unsupported schema")
	}
	database = tireHistoryFakeDB(t, nil, true, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		return nil, &pq.Error{Code: "42703"}
	})
	if _, err := fetchTireHistory(context.Background(), database, q, tireTestKey); !errors.Is(err, errTireHistoryUnsupported) {
		t.Fatal("schema race not safely unsupported")
	}
}

func TestTireHistoryCancellationIsBounded(t *testing.T) {
	database := sql.OpenDB(friendSourceTestConnector{query: func(ctx context.Context, _ string, _ []driver.NamedValue) (driver.Rows, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	defer database.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fetchTireHistory(ctx, database, tireTestRequest(t), tireTestKey); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}

func TestTireHistoryHTTPAuthenticationValidationAndPrivacy(t *testing.T) {
	oldDB, oldToken, oldProbe := db, apiToken, authProbeURL
	apiToken, authProbeURL = "history-test-token", ""
	t.Cleanup(func() { db, apiToken, authProbeURL = oldDB, oldToken, oldProbe })
	q := tireTestRequest(t)
	db = tireHistoryFakeDB(t, nil, true, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		return &friendSourceTestRows{columns: tireTestColumns, values: [][]driver.Value{tireTestRow(42, 1, q.From)}}, nil
	})
	path := "/api/v1/cars/1/tire-pressure-history?from=2026-08-20T12:00:00Z&to=2026-09-20T12:00:00Z"
	for _, tc := range []struct {
		method, path, token string
		status              int
	}{
		{http.MethodGet, path, "", 401}, {http.MethodGet, path, "wrong", 401}, {http.MethodPost, path, "history-test-token", 405},
		{http.MethodGet, strings.Replace(path, "/1/", "/0/", 1), "history-test-token", 400},
		{http.MethodGet, path + "&limit=0", "history-test-token", 400}, {http.MethodGet, path + "&cursor=garbage", "history-test-token", 400},
		{http.MethodGet, path + "&from=duplicate", "history-test-token", 400}, {http.MethodGet, path + "&cursor=%xx", "history-test-token", 400},
		{http.MethodGet, path, "history-test-token", 200},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		rec := httptest.NewRecorder()
		handleStates(rec, req)
		if rec.Code != tc.status {
			t.Fatalf("%s got%d want%d: %s", tc.path, rec.Code, tc.status, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("history response, including failure, may be cached by shared proxies")
		}
		if tc.status == 200 {
			body := rec.Body.String()
			for _, want := range []string{`"pressure_unit":"bar"`, `"temperature_unit":"C"`, `"speed_unit":"km/h"`, `"recorded_at":"2026-08-20T12:00:00Z"`, `"sensor_measured_at":null`, `"position_id":42`} {
				if !strings.Contains(body, want) {
					t.Fatalf("missing explicit provenance/unit %s", want)
				}
			}
			for _, forbidden := range []string{"latitude", "longitude", "VIN", "history-test-token", "sensor_measured_at\":\""} {
				if strings.Contains(body, forbidden) {
					t.Fatal("private/fabricated field leaked")
				}
			}
			if rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("history may be cached by shared proxies")
			}
		}
	}
	for _, schemaErr := range []error{&pq.Error{Code: "42703"}, errors.New("synthetic private database detail")} {
		db = tireHistoryFakeDB(t, schemaErr, true, nil)
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer history-test-token")
		rec := httptest.NewRecorder()
		handleStates(rec, req)
		want := 503
		if errors.Is(tireHistorySchemaError(schemaErr), errTireHistoryUnsupported) {
			want = 501
		}
		if rec.Code != want || strings.Contains(rec.Body.String(), "private database detail") {
			t.Fatalf("unsafe failure response: %d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestTireHistoryCapabilityDependsOnSchema(t *testing.T) {
	oldDB := db
	t.Cleanup(func() { db = oldDB })
	for _, tc := range []struct {
		err       error
		status    string
		supported bool
	}{
		{nil, "available", true}, {&pq.Error{Code: "42703"}, "unsupported_schema", false}, {errors.New("connection failed"), "unavailable", false},
	} {
		db = tireHistoryFakeDB(t, tc.err, true, nil)
		payload := map[string]any{"capabilities": []string{"existing"}}
		addTireHistoryCapability(context.Background(), payload)
		meta := payload["tire_pressure_history"].(map[string]any)
		caps := payload["capabilities"].([]string)
		if meta["status"] != tc.status || meta["supported"] != tc.supported || (len(caps) == 2) != tc.supported || caps[0] != "existing" {
			t.Fatalf("false capability claim: %+v", payload)
		}
	}
	db = tireHistoryFakeDB(t, nil, true, nil)
	addTireHistoryCapability(context.Background(), nil)
	for _, value := range []any{nil, "unexpected", []any{"other"}} {
		payload := map[string]any{"capabilities": value}
		addTireHistoryCapability(context.Background(), payload)
		if payload["tire_pressure_history"].(map[string]any)["supported"] != true {
			t.Fatal("capability metadata lost on malformed input")
		}
	}
}

func TestTireHistoryRouteCoverage(t *testing.T) {
	for _, file := range []string{"Caddyfile.snippet", "Caddyfile.lan.example", "nginx.snippet.conf", "install.sh"} {
		contents, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(contents), "tire-pressure-history") {
			t.Fatalf("%s missing history route", file)
		}
	}
}
