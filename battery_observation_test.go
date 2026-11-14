package main

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBatteryObservationReadsOriginalRowAndSampleClock(t *testing.T) {
	at := time.Date(2026, 11, 14, 0, 9, 29, 796000000, time.UTC)
	checked := at.Add(45 * time.Minute)
	database := sqlTestDatabase(t, func(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > batteryObservationReadTimeout || query != batteryObservationQuery || len(args) != 2 || args[0].Value != int64(1) || args[1].Value != checked {
			t.Fatal("battery read lost bounded exact-car/sample query")
		}
		return &friendSourceTestRows{columns: []string{"source", "car", "date", "soc", "usable", "rated", "ideal"}, values: [][]driver.Value{{"teslamate.positions", int64(1), at, int64(63), int64(63), []byte("331.8"), []byte("331.8")}}}, nil
	})
	s, err := readBatteryDatabaseObservation(context.Background(), database, 1, checked)
	if err != nil || s == nil || s.BatteryLevel != 63 || *s.UsableBatteryLevel != 63 || *s.RatedRangeKM != 331.8 || s.Source != "teslamate.positions" || !s.ObservedAt.Equal(at) || !s.CheckedAt.Equal(checked) {
		t.Fatalf("actual battery observation was lost/rewritten: %+v %v", s, err)
	}
}

func TestBatteryObservationRejectsWrongScopeAndFailedSource(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute)
	for _, scenario := range []string{"other_car", "future_sample", "invalid_soc", "invalid_usable", "failure", "no_row"} {
		t.Run(scenario, func(t *testing.T) {
			database := sqlTestDatabase(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				row := []driver.Value{"teslamate.positions", int64(1), at, int64(63), int64(63), []byte("331.8"), nil}
				switch scenario {
				case "other_car":
					row[1] = int64(2)
				case "future_sample":
					row[2] = at.Add(time.Hour)
				case "invalid_soc":
					row[3] = int64(101)
				case "invalid_usable":
					row[3] = int64(0)
				case "failure":
					return nil, errors.New("synthetic read failure")
				case "no_row":
					return sqlEmptyRows(), nil
				}
				return &friendSourceTestRows{columns: []string{"source", "car", "date", "soc", "usable", "rated", "ideal"}, values: [][]driver.Value{row}}, nil
			})
			sample, err := readBatteryDatabaseObservation(context.Background(), database, 1, at.Add(time.Minute))
			if sample != nil || (scenario != "no_row" && err == nil) {
				t.Fatalf("invalid source became battery: %+v %v", sample, err)
			}
		})
	}
}

func TestBatteryObservationGenuineZeroAndMissingOptionalRange(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute)
	database := sqlTestDatabase(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		return &friendSourceTestRows{columns: []string{"source", "car", "date", "soc", "usable", "rated", "ideal"}, values: [][]driver.Value{{"teslamate.positions", int64(1), at, int64(0), int64(0), nil, nil}}}, nil
	})
	sample, err := readBatteryDatabaseObservation(context.Background(), database, 1, time.Now().UTC())
	if err != nil || sample == nil || sample.BatteryLevel != 0 || sample.RatedRangeKM != nil {
		t.Fatal("true zero or missing range changed", sample, err)
	}
}

func TestBatteryObservationOwnerHTTPContractIndependentOfMQTT(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	database := sqlTestDatabase(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		return &friendSourceTestRows{columns: []string{"source", "car", "date", "soc", "usable", "rated", "ideal"}, values: [][]driver.Value{{"teslamate.positions", int64(1), at, int64(63), int64(63), []byte("331.8"), []byte("331.8")}}}, nil
	})
	useSQLTestDatabase(t, database)
	oldToken := apiToken
	apiToken = "synthetic-local-battery-test"
	t.Cleanup(func() { apiToken = oldToken })
	request := httptest.NewRequest("GET", "/api/companion/v1/cars/1/companion-status?include=battery_observation", nil)
	response := httptest.NewRecorder()
	handleCompanionStatus(response, request, "1")
	if response.Code != 401 {
		t.Fatal("unauthorized battery access")
	}
	request.Header.Set("Authorization", "Bearer "+apiToken)
	response = httptest.NewRecorder()
	handleCompanionStatus(response, request, "1")
	var result struct {
		Data struct {
			Battery *batteryDatabaseObservation `json:"battery_observation"`
		} `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.Battery == nil || result.Data.Battery.BatteryLevel != 63 || !result.Data.Battery.ObservedAt.Equal(at) {
		t.Fatalf("owner HTTP lost original battery: %d %s", response.Code, response.Body.String())
	}
}

func TestBatteryObservationLocalPostgresSelectsLatestSameCarAndNoFuture(t *testing.T) {
	database := openFriendPostgresFixture(t)
	fixtureExec(t, database, `ALTER TABLE positions ADD COLUMN usable_battery_level integer;
ALTER TABLE positions ADD COLUMN ideal_battery_range_km numeric(6,2);
CREATE TEMP TABLE charges(id bigint PRIMARY KEY, charging_process_id integer REFERENCES charging_processes(id), date timestamp(6) without time zone NOT NULL, battery_level integer, usable_battery_level integer, rated_battery_range_km numeric(6,2), ideal_battery_range_km numeric(6,2));
INSERT INTO cars VALUES (1),(2);`)
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	fixtureExec(t, database, `INSERT INTO positions(id,car_id,date,battery_level,usable_battery_level,rated_battery_range_km,ideal_battery_range_km) VALUES
(1,1,$1,64,63,333.42,333.42),(2,1,$2,63,63,331.8,331.8),(3,2,$2,42,42,239.42,239.42),(4,1,$3,0,0,0,0)`, at.Add(-time.Hour), at, at.Add(time.Hour))
	sample, err := readBatteryDatabaseObservation(context.Background(), database, 1, time.Now().UTC())
	if err != nil || sample == nil || sample.BatteryLevel != 63 || !sample.ObservedAt.Equal(at) {
		t.Fatalf("latest same-car SQL sample: %+v %v", sample, err)
	}
	fixtureExec(t, database, `INSERT INTO charging_processes(id,car_id,start_date) VALUES(1,1,$1);`, at)
	fixtureExec(t, database, `INSERT INTO charges(id,charging_process_id,date,battery_level,usable_battery_level,rated_battery_range_km,ideal_battery_range_km) VALUES(1,1,$1,80,80,420.6,420.6)`, at.Add(10*time.Second))
	sample, err = readBatteryDatabaseObservation(context.Background(), database, 1, time.Now().UTC())
	if err != nil || sample == nil || sample.BatteryLevel != 80 || sample.Source != "teslamate.charges" {
		t.Fatalf("newer charging sample lost: %+v %v", sample, err)
	}
	// A later malformed persisted stream row must not hide the preceding
	// actual sample; its original clock is retained, not advanced to that row.
	fixtureExec(t, database, `INSERT INTO positions(id,car_id,date,battery_level,usable_battery_level,rated_battery_range_km) VALUES(5,1,$1,0,63,331.8)`, at.Add(20*time.Second))
	sample, err = readBatteryDatabaseObservation(context.Background(), database, 1, time.Now().UTC())
	if err != nil || sample == nil || sample.BatteryLevel != 80 || !sample.ObservedAt.Equal(at.Add(10*time.Second)) {
		t.Fatalf("bad latest row hid or retimed actual sample: %+v %v", sample, err)
	}
	fixtureExec(t, database, `INSERT INTO positions(id,car_id,date,battery_level,usable_battery_level,rated_battery_range_km) VALUES(6,1,$1,0,0,0)`, at.Add(30*time.Second))
	sample, err = readBatteryDatabaseObservation(context.Background(), database, 1, time.Now().UTC())
	if err != nil || sample == nil || sample.BatteryLevel != 0 || !sample.ObservedAt.Equal(at.Add(30*time.Second)) {
		t.Fatalf("real latest zero lost to older sample: %+v %v", sample, err)
	}
}

func TestOrdinaryCompanionStatusDoesNotQueryBatteryHistory(t *testing.T) {
	database := sqlTestDatabase(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		t.Fatal("ordinary companion status unexpectedly read battery history")
		return nil, errors.New("unexpected battery read")
	})
	useSQLTestDatabase(t, database)
	oldToken := apiToken
	apiToken = "synthetic-local-battery-test"
	t.Cleanup(func() { apiToken = oldToken })
	for _, suffix := range []string{"", "?include=other"} {
		request := httptest.NewRequest("GET", "/api/companion/v1/cars/1/companion-status"+suffix, nil)
		request.Header.Set("Authorization", "Bearer "+apiToken)
		response := httptest.NewRecorder()
		handleCompanionStatus(response, request, "1")
		var result struct {
			Data struct {
				Battery *batteryDatabaseObservation `json:"battery_observation"`
				Status  string                      `json:"battery_observation_status"`
			} `json:"data"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.Data.Battery != nil || result.Data.Status != "not_requested" {
			t.Fatalf("ordinary status changed: %d %s", response.Code, response.Body.String())
		}
	}
}
