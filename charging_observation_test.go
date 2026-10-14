package main

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func chargingTime(value string) time.Time {
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		panic(err)
	}
	return at
}
func chargingTimePointer(at time.Time) *time.Time { return &at }
func chargingTestSample(car, process int, start, sample, checked time.Time) chargingDatabaseSnapshot {
	power := 5.0
	return chargingDatabaseSnapshot{CarID: car, ProcessID: process, StartedAt: start, LastSampleAt: &sample, LastPositivePowerAt: &sample, CheckedAt: checked, PowerKW: &power, BatteryLevel: baselineIntPointer(49), RatedRangeKM: floatPointer(250)}
}
func chargingDatabaseTestMonitor(t *testing.T) *chargingNotificationMonitor {
	t.Helper()
	m := testChargingBaselineMonitor(t)
	m.databaseManaged = true
	m.databaseObservations = map[int]chargingDatabaseSnapshot{}
	m.resetMQTTBaseline()
	t.Cleanup(m.resetMQTTBaseline)
	return m
}
func chargingTestBaseline(m *chargingNotificationMonitor, car int, at time.Time) {
	for _, pair := range [][2]string{{"state", "charging"}, {"charging_state", "Charging"}, {"battery_level", "49"}, {"rated_battery_range_km", "250"}, {"charger_power", "5"}, {"time_to_full_charge", "1"}} {
		m.observeMessage(car, pair[0], pair[1], at, true, true)
	}
}

func TestChargingDatabaseGapReplayDoesNotCompleteOrRefresh(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	// Neutral synthetic identity/clock; preserve the regression's exact gaps.
	start := chargingTime("2000-01-01T12:00:00Z")
	last := start.Add(13*time.Minute + 5*time.Second + 313*time.Millisecond)
	m.applyChargingDatabaseSnapshot(chargingTestSample(17, 701, start, last, last))
	chargingTestBaseline(m, 17, last)
	m.observeMessage(17, "healthy", "true", last.Add(time.Second), false, true)
	if len(m.queue) != 1 {
		t.Fatal("fresh timestamped source did not recover charge")
	}
	first := <-m.queue
	if first.ObservedAt != last.Format(time.RFC3339Nano) || first.ObservationMode != "database_sample_confirmed" || first.EstimatedCompleteAt != nil {
		t.Fatalf("receipt became sample clock or retained ETA revived: %+v", first)
	}
	state := m.store.Cars[17]
	state.StartDelivered = true
	m.store.Cars[17] = state
	// Live health heartbeats and repeated cache scalars prove transport only.
	for _, offset := range []time.Duration{20 * time.Second, 45 * time.Second, time.Minute} {
		m.observeMessage(17, "healthy", "true", last.Add(offset), false, true)
		m.observeMessage(17, "charger_power", "5", last.Add(offset), true, true)
	}
	if len(m.queue) != 0 || len(m.pending) != 0 {
		t.Fatal("unchanged sample queued fresh charging updates")
	}
	wake := last.Add(29*time.Minute + 27*time.Second + 628*time.Millisecond)
	gap := chargingTestSample(17, 701, start, last, wake.Add(-time.Second))
	m.applyChargingDatabaseSnapshot(gap)
	m.observeMessage(17, "healthy", "true", wake.Add(-time.Second), false, true)
	m.observeMessage(17, "charger_power", "null", wake.Add(-time.Second), false, true)
	if !m.store.Cars[17].Active || len(m.queue) != 0 {
		t.Fatal("data gap or missing power became charge completion")
	}
	observation := m.chargingObservation(17, wake.Add(-time.Second))
	if observation.Status != "data_gap" || observation.SampleAgeSeconds == nil || *observation.SampleAgeSeconds != 1766 || !observation.LastSampleAt.Equal(last) || observation.EndedAt != nil {
		t.Fatalf("gap lost immutable last source clock: %+v", observation)
	}
	terminal := chargingTestSample(17, 701, start, wake, wake.Add(time.Second))
	terminal.EndedAt = &wake
	terminal.PreviousSampleAt = &last
	terminal.LastPositivePowerAt = &last
	terminal.PowerKW = floatPointer(0)
	terminal.BatteryLevel = baselineIntPointer(50)
	terminal.RatedRangeKM = floatPointer(255)
	m.applyChargingDatabaseSnapshot(terminal)
	if m.store.Cars[17].Active || len(m.queue) != 1 {
		t.Fatal("exact persisted terminal did not close the bound activity")
	}
	ended := <-m.queue
	if ended.Type != "charging_ended" || ended.SessionID != first.SessionID || ended.ObservedAt != wake.Format(time.RFC3339Nano) || ended.PowerKW != nil || ended.EstimatedCompleteAt != nil || ended.BatteryLevel != 50 || ended.RatedRangeKM == nil || *ended.RatedRangeKM != 255 {
		t.Fatalf("terminal fabricated physical time/ETA or changed session: %+v", ended)
	}
	terminal.CheckedAt = wake.Add(30 * time.Second)
	m.applyChargingDatabaseSnapshot(terminal)
	observation = m.chargingObservation(17, terminal.CheckedAt)
	if len(m.queue) != 0 || observation.Status != "terminal_confirmed" || *observation.TerminalSampleGapSeconds != 1767 || !observation.TerminalConfirmedAt.Equal(wake.Add(time.Second)) {
		t.Fatalf("terminal replay lost first confirmation/gap: %+v", observation)
	}
	// Even a newer read of an older/open snapshot cannot undo that terminal.
	gap.CheckedAt = wake.Add(31 * time.Second)
	m.applyChargingDatabaseSnapshot(gap)
	m.observeMessage(17, "healthy", "true", gap.CheckedAt, false, true)
	if m.store.Cars[17].Active || len(m.queue) != 0 || m.chargingObservation(17, gap.CheckedAt).Status != "terminal_confirmed" {
		t.Fatal("retained charging resurrected a closed exact process")
	}
	// A genuinely new database process can recover normally.
	next := wake.Add(10 * time.Minute)
	m.applyChargingDatabaseSnapshot(chargingTestSample(17, 702, next, next, next))
	m.observeMessage(17, "healthy", "true", next, false, true)
	if !m.store.Cars[17].Active || m.store.Cars[17].DatabaseProcessID != 702 || len(m.queue) != 1 {
		t.Fatal("terminal precedence blocked a different new charge")
	}
}

func TestChargingDatabaseTerminalRequiresExactPreviouslyBoundProcess(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute)
	for _, scenario := range []string{"unbound_history", "different_process", "different_start", "missing_process", "zero_power"} {
		t.Run(scenario, func(t *testing.T) {
			m := chargingDatabaseTestMonitor(t)
			m.store.Cars[1] = carChargingState{Active: true, SessionID: "current", DatabaseProcessID: 7, DatabaseStartedAt: at.Format(time.RFC3339Nano)}
			s := chargingTestSample(1, 7, at, at, at.Add(time.Minute))
			end := at.Add(30 * time.Second)
			s.EndedAt = &end
			switch scenario {
			case "unbound_history":
				state := m.store.Cars[1]
				state.DatabaseProcessID = 0
				state.DatabaseStartedAt = ""
				m.store.Cars[1] = state
			case "different_process":
				s.ProcessID = 8
			case "different_start":
				s.StartedAt = at.Add(-time.Hour)
			case "missing_process":
				s = chargingDatabaseSnapshot{CarID: 1, CheckedAt: at.Add(time.Minute)}
			case "zero_power":
				s.EndedAt = nil
				s.PowerKW = floatPointer(0)
			}
			m.applyChargingDatabaseSnapshot(s)
			if !m.store.Cars[1].Active || len(m.queue) != 0 {
				t.Fatal("unrelated/missing/zero sample terminated an activity")
			}
		})
	}
}

func TestChargingDatabaseCoherentMQTTTerminalCannotResurrectBeforeDBClose(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	at := time.Now().UTC()
	m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, at.Add(-time.Minute), at, at))
	chargingTestBaseline(m, 1, at)
	m.observeMessage(1, "healthy", "true", at, false, true)
	<-m.queue
	m.observeMessage(1, "state", "online", at.Add(time.Second), false, true)
	m.observeMessage(1, "charging_state", "Complete", at.Add(2*time.Second), false, true)
	if len(m.queue) != 1 || m.store.Cars[1].Active {
		t.Fatal("coherent real terminal was not preserved")
	}
	<-m.queue
	m.observeMessage(1, "state", "charging", at.Add(3*time.Second), false, true)
	m.observeMessage(1, "charging_state", "Charging", at.Add(4*time.Second), false, true)
	m.observeMessage(1, "healthy", "true", at.Add(5*time.Second), false, true)
	if len(m.queue) != 0 || m.store.Cars[1].Active {
		t.Fatal("lagging open SQL row resurrected just-ended MQTT session")
	}
	loaded := testChargingBaselineMonitor(t)
	loaded.statePath = m.statePath
	loaded.load()
	if loaded.store.Cars[1].MQTTTerminalProcessID != 7 || loaded.store.Cars[1].DatabaseTerminalProcessID != 0 {
		t.Fatal("terminal barrier was not durable")
	}
}

func TestChargingDatabaseUnknownDoesNotAdvanceSampleClock(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	at := time.Now().UTC()
	s := chargingTestSample(1, 7, at.Add(-time.Minute), at, at)
	m.applyChargingDatabaseSnapshot(s)
	for _, offset := range []time.Duration{61 * time.Second, 5 * time.Minute} {
		out := m.chargingObservation(1, at.Add(offset))
		if out.Status != "unknown" || !out.LastSampleAt.Equal(at) {
			t.Fatal("expired database check kept asserting fresh source")
		}
	}
	m.mu.Lock()
	m.databaseError = "charging_read_unavailable"
	m.mu.Unlock()
	out := m.chargingObservation(1, at.Add(time.Second))
	if out.Status != "unknown" || out.Error == "" || !out.LastSampleAt.Equal(at) {
		t.Fatal("failed read became fresh or terminal")
	}
}

func TestChargingDatabaseReadBoundsTypedNullsAndClocks(t *testing.T) {
	at := time.Now().UTC().Add(-time.Minute)
	columns := []string{"car", "process", "start", "end", "sample", "power", "battery", "range", "previous", "positive"}
	calls := 0
	database := sqlTestDatabase(t, func(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		calls++
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > chargingDatabaseReadTimeout || query != chargingDatabaseQuery || len(args) != 2 || args[0].Value != "{1,2}" || args[1].Value != "{7,0}" {
			t.Fatal("read lost bounded deadline/exact process/fleet arguments")
		}
		return &friendSourceTestRows{columns: columns, values: [][]driver.Value{{int64(1), int64(7), at, nil, at, []byte("5"), int64(49), []byte("250"), nil, at}, {int64(2), nil, nil, nil, nil, nil, nil, nil, nil, nil}}}, nil
	})
	out, err := readChargingDatabase(context.Background(), database, []int{1, 2}, []int{7, 0}, at)
	if err != nil || calls != 1 || len(out) != 2 || out[0].PowerKW == nil || *out[0].PowerKW != 5 || out[1].ProcessID != 0 || out[1].LastSampleAt != nil {
		t.Fatalf("typed source/null clock conversion: %+v %v", out, err)
	}
	for _, ids := range [][]int{{2, 1}, {1, 1}, {-1, 2}, make([]int, 257)} {
		if _, err := readChargingDatabase(context.Background(), database, ids, make([]int, len(ids)), at); err == nil {
			t.Fatal("unbounded/duplicate/invalid input accepted")
		}
	}
	if calls != 1 {
		t.Fatal("invalid scope reached database")
	}
	for _, scenario := range []string{"future_sample", "wrong_bound_process", "wrong_car", "query_failure"} {
		t.Run(scenario, func(t *testing.T) {
			database := sqlTestDatabase(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
				row := []driver.Value{int64(1), int64(7), at, nil, at, int64(5), int64(49), []byte("250"), nil, at}
				switch scenario {
				case "future_sample":
					row[4] = time.Now().UTC().Add(time.Hour)
				case "wrong_bound_process":
					row[1] = int64(8)
				case "wrong_car":
					row[0] = int64(2)
				case "query_failure":
					return nil, errors.New("synthetic unavailable")
				}
				return &friendSourceTestRows{columns: columns, values: [][]driver.Value{row}}, nil
			})
			if _, err := readChargingDatabase(context.Background(), database, []int{1}, []int{7}, at); err == nil {
				t.Fatal("invalid/failing source accepted")
			}
		})
	}
}

func TestChargingObservationIsAdditiveOwnerStatusOnly(t *testing.T) {
	oldCharging, oldToken := chargingPush, apiToken
	t.Cleanup(func() { chargingPush, apiToken = oldCharging, oldToken })
	chargingPush = chargingDatabaseTestMonitor(t)
	apiToken = "synthetic-local-only"
	at := time.Now().UTC()
	chargingPush.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, at.Add(-time.Minute), at, at))
	r := httptest.NewRequest("GET", "/api/cars/1/companion-status", nil)
	r.Header.Set("Authorization", "Bearer "+apiToken)
	w := httptest.NewRecorder()
	handleCompanionStatus(w, r, "1")
	var out struct {
		Data struct {
			Charging chargingDatabaseObservation `json:"charging_observation"`
		} `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Data.Charging.ProcessID != 7 || out.Data.Charging.Status != "fresh" {
		t.Fatalf("owner additive contract missing: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handleCompanionStatus(w, httptest.NewRequest("GET", "/api/cars/1/companion-status", nil), "1")
	if w.Code != 401 || strings.Contains(w.Body.String(), "charging_process_id") {
		t.Fatal("charging diagnostics bypassed owner authorization")
	}
}

// This is the separate SQL-execution gate, using only the repository's
// explicitly opted-in literal-loopback TEMP-table PostgreSQL fixture.
func TestChargingDatabaseLocalPostgresExactSessionAndDisabledCars(t *testing.T) {
	database := openFriendPostgresFixture(t)
	fixtureExec(t, database, `ALTER TABLE cars ADD COLUMN settings_id integer;
CREATE TEMP TABLE car_settings(id integer PRIMARY KEY, enabled boolean);
CREATE TEMP TABLE charges(id bigint PRIMARY KEY, charging_process_id integer REFERENCES charging_processes(id), date timestamp(6) without time zone NOT NULL, charger_power integer, battery_level integer, rated_battery_range_km numeric(6,2));
CREATE INDEX ON charges(charging_process_id);
INSERT INTO car_settings VALUES(1,true),(2,false);
INSERT INTO cars VALUES(1,1),(2,2),(3,NULL),(4,1);`)
	at := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	start := at.Add(-43 * time.Minute)
	last := at.Add(-29*time.Minute - 27*time.Second)
	fixtureExec(t, database, `INSERT INTO charging_processes(id,car_id,start_date) VALUES(85,1,$1),(86,1,$2),(87,2,$1)`, start, at.Add(-time.Minute))
	fixtureExec(t, database, `INSERT INTO charges VALUES(1,85,$1,5,49,250),(2,85,$2,0,50,255),(3,86,$3,10,50,255),(4,87,$3,5,50,255)`, last, at, at)
	fixtureExec(t, database, `UPDATE charging_processes SET end_date=$1 WHERE id=85`, at)
	fixtureExec(t, database, `SET default_transaction_read_only=on`)
	out, err := readChargingDatabase(context.Background(), database, []int{1, 2, 3, 4}, []int{85, 0, 0, 0}, at.Add(time.Second))
	if err != nil || len(out) != 2 || out[0].ProcessID != 85 || out[0].EndedAt == nil || !out[0].LastPositivePowerAt.Equal(last) || !out[0].PreviousSampleAt.Equal(last) || out[1].CarID != 4 || out[1].ProcessID != 0 {
		t.Fatalf("real SQL exact process/disabled/null handling: %+v %v", out, err)
	}
	m := chargingDatabaseTestMonitor(t)
	m.store.Cars[1] = carChargingState{Active: true, SessionID: "existing", DatabaseProcessID: 85, DatabaseStartedAt: start.Format(time.RFC3339Nano)}
	m.applyChargingDatabaseSnapshot(out[0])
	if m.store.Cars[1].Active || len(m.queue) != 1 {
		t.Fatal("real database terminal failed lifecycle reconciliation")
	}
	latest, err := readChargingDatabase(context.Background(), database, []int{1}, []int{0}, at.Add(time.Second))
	if err != nil || len(latest) != 1 || latest[0].ProcessID != 86 || !chargingSnapshotFresh(latest[0], latest[0].CheckedAt) {
		t.Fatalf("real SQL new charge recovery: %+v %v", latest, err)
	}
}

func TestChargingDatabaseAdvancingSampleUpdatesOnlySourceClockAndPower(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	at := time.Now().UTC().Add(-5 * time.Minute)
	m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, at.Add(-time.Minute), at, at))
	chargingTestBaseline(m, 1, at)
	m.observeMessage(1, "healthy", "true", at, false, true)
	<-m.queue
	state := m.store.Cars[1]
	state.StartDelivered = true
	m.store.Cars[1] = state
	m.observeMessage(1, "time_to_full_charge", "1", at.Add(time.Second), false, true)
	next := at.Add(190 * time.Second)
	sample := chargingTestSample(1, 7, at.Add(-time.Minute), next, next)
	sample.PowerKW = floatPointer(10)
	m.applyChargingDatabaseSnapshot(sample)
	m.observeMessage(1, "healthy", "true", next.Add(time.Second), false, true)
	if len(m.queue) != 1 {
		t.Fatal("advancing database sample did not update existing activity")
	}
	updated := <-m.queue
	if updated.Type != "charging_updated" || updated.ObservedAt != next.Format(time.RFC3339Nano) || updated.PowerKW == nil || *updated.PowerKW != 10 || updated.EstimatedCompleteAt != nil || updated.RemainingSeconds != nil {
		t.Fatalf("new sample reused stale MQTT power/countdown or receipt time: %+v", updated)
	}
	m.observeMessage(1, "healthy", "true", next.Add(50*time.Second), false, true)
	if len(m.queue) != 0 {
		t.Fatal("heartbeats rebased source after a valid update")
	}
}

func TestChargingDatabasePassiveObserverCancelsReadAndDoesNotWake(t *testing.T) {
	old := mqttSourceRuntime.Load()
	runtime := &teslaMateMQTTRuntime{cars: map[int]bool{1: true}, lastFleetSuccess: time.Now(), refreshCh: make(chan struct{}, 1)}
	mqttSourceRuntime.Store(runtime)
	t.Cleanup(func() { mqttSourceRuntime.Store(old) })
	entered := make(chan struct{})
	database := sqlTestDatabase(t, func(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
		if query != chargingDatabaseQuery || len(args) != 2 {
			t.Fatal("passive observer attempted another source")
		}
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	m := chargingDatabaseTestMonitor(t)
	stop := m.startDatabaseObservation(database)
	select {
	case <-entered:
	case <-time.After(time.Second):
		stop()
		t.Fatal("observer did not begin bounded database read")
	}
	before := time.Now()
	stop()
	if time.Since(before) > time.Second || len(m.queue) != 0 {
		t.Fatal("passive read could not cancel promptly or manufactured push event")
	}
}

func TestChargingDatabaseLegacyUnboundActivityCannotBorrowNewProcessData(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	at := time.Now().UTC()
	m.store.Cars[1] = carChargingState{Active: true, SessionID: "legacy", StartDelivered: true, StartBatteryLevel: 20, VehicleState: "charging", ChargingState: "charging", BatteryLevel: baselineIntPointer(49), RatedRangeKM: floatPointer(250)}
	m.applyChargingDatabaseSnapshot(chargingTestSample(1, 99, at.Add(-time.Minute), at, at))
	chargingTestBaseline(m, 1, at)
	m.observeMessage(1, "healthy", "true", at, false, true)
	m.flushUpdate(1)
	if m.replayActiveStarts("synthetic-installation") != 0 || len(m.queue) != 0 || !m.store.Cars[1].Active || m.store.Cars[1].DatabaseProcessID != 0 {
		t.Fatal("unrelated fresh process refreshed/replayed an unbound legacy activity")
	}
}

func TestChargingDatabaseReadAcceptsSampleInsertedDuringAcquisition(t *testing.T) {
	at := time.Now().UTC()
	columns := []string{"car", "process", "start", "end", "sample", "power", "battery", "range", "previous", "positive"}
	database := sqlTestDatabase(t, func(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
		// Simulate a timestamped source sample committed after poll start,
		// before its result is acquired, without sleeping or remote I/O.
		sample := time.Now().UTC()
		return &friendSourceTestRows{columns: columns, values: [][]driver.Value{{int64(1), int64(7), at.Add(-time.Minute), nil, sample, int64(5), int64(49), []byte("250"), nil, sample}}}, nil
	})
	out, err := readChargingDatabase(context.Background(), database, []int{1}, []int{7}, at)
	if err != nil || len(out) != 1 || out[0].LastSampleAt.After(out[0].CheckedAt) || !chargingSnapshotFresh(out[0], out[0].CheckedAt) {
		t.Fatalf("concurrent normal insert rejected as future: %+v %v", out, err)
	}
}

func TestChargingDatabaseRetainedTerminalHealthyDoesNotEndOpenProcess(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	at := time.Now().UTC()
	m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, at.Add(-time.Minute), at, at))
	chargingTestBaseline(m, 1, at)
	m.observeMessage(1, "healthy", "true", at, false, true)
	<-m.queue
	state := m.store.Cars[1]
	state.StartDelivered = true
	m.store.Cars[1] = state
	m.resetMQTTBaseline()
	for _, pair := range [][2]string{{"state", "online"}, {"charging_state", "Complete"}, {"battery_level", "49"}, {"rated_battery_range_km", "250"}} {
		m.observeMessage(1, pair[0], pair[1], at.Add(time.Second), true, true)
	}
	m.observeMessage(1, "healthy", "true", at.Add(2*time.Second), false, true)
	if !m.store.Cars[1].Active || len(m.queue) != 0 || m.store.Cars[1].DatabaseTerminalProcessID != 0 {
		t.Fatal("retained terminal plus healthy ended SQL-open process")
	}
}

func TestChargingDatabaseSameProcessResumeNeedsOrderedLivePrimaryStates(t *testing.T) {
	for _, power := range []*float64{nil, floatPointer(0), floatPointer(5)} {
		m := chargingDatabaseTestMonitor(t)
		at := time.Now().UTC()
		start := at.Add(-time.Minute)
		m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, start, at, at))
		chargingTestBaseline(m, 1, at)
		m.observeMessage(1, "healthy", "true", at, false, true)
		<-m.queue
		m.observeMessage(1, "state", "online", at.Add(time.Second), false, true)
		m.observeMessage(1, "charging_state", "Stopped", at.Add(2*time.Second), false, true)
		if m.store.Cars[1].Active || len(m.queue) != 1 {
			t.Fatal("genuine MQTT terminal did not pause activity")
		}
		<-m.queue
		resumed := at.Add(30 * time.Second)
		s := chargingTestSample(1, 7, start, resumed, resumed)
		s.PowerKW = power
		m.applyChargingDatabaseSnapshot(s)
		m.observeMessage(1, "healthy", "true", resumed, false, true)
		if m.store.Cars[1].Active || len(m.queue) != 0 {
			t.Fatal("new sample/healthy alone resumed terminal activity")
		}
		m.observeMessage(1, "state", "charging", resumed.Add(time.Second), true, true)
		m.observeMessage(1, "charging_state", "Charging", resumed.Add(2*time.Second), true, true)
		m.observeMessage(1, "healthy", "true", resumed.Add(3*time.Second), false, true)
		if m.store.Cars[1].Active || len(m.queue) != 0 {
			t.Fatal("new sample/retained primary states resumed terminal activity")
		}
		m.observeMessage(1, "state", "charging", resumed.Add(4*time.Second), false, true)
		m.observeMessage(1, "charging_state", "Charging", resumed.Add(5*time.Second), false, true)
		if !m.store.Cars[1].Active || len(m.queue) != 1 || m.store.Cars[1].DatabaseProcessID != 7 {
			t.Fatal("ordered real Charging publications and new sample could not resume same open process")
		}
	}
}

func TestChargingDatabaseSourceSOCAndRangeNeverBorrowOldMQTTValues(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	at := time.Now().UTC().Add(-time.Minute)
	start := at.Add(-time.Minute)
	m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, start, at, at))
	chargingTestBaseline(m, 1, at)
	m.observeMessage(1, "battery_level", "20", at, true, true)
	m.observeMessage(1, "rated_battery_range_km", "100", at, true, true)
	m.observeMessage(1, "healthy", "true", at, false, true)
	first := <-m.queue
	if first.StartBatteryLevel != 49 || first.BatteryLevel != 49 || first.RatedRangeKM == nil || *first.RatedRangeKM != 250 {
		t.Fatal("start stamped stale scalar SOC/range with database sample time")
	}
	state := m.store.Cars[1]
	state.StartDelivered = true
	m.store.Cars[1] = state
	next := at.Add(time.Minute)
	s := chargingTestSample(1, 7, start, next, next)
	s.BatteryLevel = baselineIntPointer(51)
	s.RatedRangeKM = nil
	m.applyChargingDatabaseSnapshot(s)
	m.observeMessage(1, "healthy", "true", next, false, true)
	updated := <-m.queue
	if updated.BatteryLevel != 51 || updated.AddedBatteryPercent != 2 || updated.RatedRangeKM != nil || updated.AddedRangeKM != nil {
		t.Fatal("source update borrowed unclocked old scalar SOC/range")
	}
}

func TestChargingDatabaseMixedRetainedTerminalCannotEndOpenProcess(t *testing.T) {
	for _, lastField := range []string{"state", "charging_state"} {
		m := chargingDatabaseTestMonitor(t)
		at := time.Now().UTC()
		m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, at.Add(-time.Minute), at, at))
		chargingTestBaseline(m, 1, at)
		m.observeMessage(1, "healthy", "true", at, false, true)
		<-m.queue
		m.resetMQTTBaseline()
		for _, pair := range [][2]string{{"state", "online"}, {"charging_state", "Complete"}, {"battery_level", "49"}, {"rated_battery_range_km", "250"}} {
			m.observeMessage(1, pair[0], pair[1], at.Add(time.Second), true, true)
		}
		m.observeMessage(1, "healthy", "true", at.Add(2*time.Second), false, true)
		value := "offline"
		if lastField == "charging_state" {
			value = "Complete"
		}
		m.observeMessage(1, lastField, value, at.Add(3*time.Second), false, true)
		if !m.store.Cars[1].Active || len(m.queue) != 0 {
			t.Fatal("one live primary combined with retained terminal ended an SQL-open charge")
		}
	}
}

func TestChargingDatabaseTerminalMissingSOCRetainsOnlyConfirmedSourceValue(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	at := time.Now().UTC()
	start := at.Add(-time.Minute)
	m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, start, at, at))
	chargingTestBaseline(m, 1, at)
	m.observeMessage(1, "healthy", "true", at, false, true)
	if len(m.queue) != 1 {
		t.Fatal("fresh source start missing")
	}
	<-m.queue
	state := m.store.Cars[1]
	state.StartDelivered = true
	m.store.Cars[1] = state
	next := at.Add(46 * time.Second)
	s := chargingTestSample(1, 7, start, next, next)
	s.BatteryLevel = baselineIntPointer(52)
	m.applyChargingDatabaseSnapshot(s)
	m.observeMessage(1, "healthy", "true", next, false, true)
	if len(m.queue) != 1 {
		t.Fatal("advancing source update missing")
	}
	<-m.queue
	state = m.store.Cars[1]
	state.BatteryLevel = baselineIntPointer(99) // Unclocked scalar is unrelated.
	m.store.Cars[1] = state
	end := next.Add(30 * time.Second)
	s = chargingTestSample(1, 7, start, end, end)
	s.EndedAt = &end
	s.BatteryLevel, s.RatedRangeKM = nil, nil
	m.applyChargingDatabaseSnapshot(s)
	if len(m.queue) != 1 {
		t.Fatal("exact terminal missing")
	}
	event := <-m.queue
	if event.Type != "charging_ended" || event.BatteryLevel != 52 || event.RatedRangeKM != nil || event.PowerKW != nil || event.RemainingSeconds != nil || event.EstimatedCompleteAt != nil {
		t.Fatalf("terminal fabricated missing sample values from MQTT: %+v", event)
	}
}

func TestChargingDatabaseFirstFreshReadStartsAfterMQTTBaselineWithoutAnotherMessage(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	at := time.Now().UTC().Add(-time.Minute)
	chargingTestBaseline(m, 1, at)
	m.observeMessage(1, "healthy", "true", at, false, true)
	if len(m.queue) != 0 || m.store.Cars[1].Active {
		t.Fatal("MQTT without a database sample started an activity")
	}
	m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, at.Add(-time.Minute), at, at.Add(time.Second)))
	if len(m.queue) != 1 || !m.store.Cars[1].Active {
		t.Fatal("first fresh database result stranded a ready MQTT baseline until another message")
	}
	event := <-m.queue
	if event.Type != "charging_started" || event.ObservedAt != at.Format(time.RFC3339Nano) || m.store.Cars[1].SessionStartedAt != "" || event.PowerKW == nil || *event.PowerKW != 5 || event.EstimatedCompleteAt != nil {
		t.Fatalf("database-driven recovery invented a start clock or incoherent readings: %+v", event)
	}
}

func TestChargingDatabaseAdvancingReadUpdatesWithoutMQTTAndPreservesThrottle(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	at := time.Now().UTC().Add(-2 * time.Minute)
	start := at.Add(-time.Minute)
	m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, start, at, at))
	chargingTestBaseline(m, 1, at)
	m.observeMessage(1, "healthy", "true", at, false, true)
	<-m.queue
	m.mu.Lock()
	state := m.store.Cars[1]
	state.StartDelivered = true
	m.store.Cars[1] = state
	m.mu.Unlock()
	next := at.Add(46 * time.Second)
	sample := chargingTestSample(1, 7, start, next, next)
	sample.PowerKW = floatPointer(9)
	m.applyChargingDatabaseSnapshot(sample)
	if len(m.queue) != 1 {
		t.Fatal("advancing database sample needed an unrelated MQTT message to update")
	}
	event := <-m.queue
	if event.Type != "charging_updated" || event.ObservedAt != next.Format(time.RFC3339Nano) || event.PowerKW == nil || *event.PowerKW != 9 {
		t.Fatalf("database-driven update lost source sample identity: %+v", event)
	}
	sample.CheckedAt = next.Add(time.Second)
	m.applyChargingDatabaseSnapshot(sample)
	if len(m.queue) != 0 || len(m.pending) != 0 {
		t.Fatal("unchanged database sample refreshed an activity")
	}
	soon := next.Add(10 * time.Second)
	m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, start, soon, soon))
	if len(m.queue) != 0 || len(m.pending) != 1 {
		t.Fatal("database-driven update bypassed the existing 45-second throttle")
	}
}

func TestChargingDatabaseFreshReadCannotBypassMQTTReadinessOrLegacyBinding(t *testing.T) {
	for _, scenario := range []string{"retained_only", "inconsistent", "unhealthy", "legacy_unbound", "read_failure"} {
		t.Run(scenario, func(t *testing.T) {
			m := chargingDatabaseTestMonitor(t)
			at := time.Now().UTC().Add(-time.Minute)
			chargingTestBaseline(m, 1, at)
			switch scenario {
			case "retained_only":
			case "inconsistent":
				m.observeMessage(1, "state", "offline", at, false, true)
			case "unhealthy":
				m.observeMessage(1, "healthy", "false", at, false, true)
			case "legacy_unbound":
				m.observeMessage(1, "healthy", "true", at, false, true)
				m.store.Cars[1] = carChargingState{Active: true, SessionID: "legacy", StartDelivered: true, VehicleState: "charging", ChargingState: "charging", BatteryLevel: baselineIntPointer(49), RatedRangeKM: floatPointer(250)}
			case "read_failure":
				m.observeMessage(1, "healthy", "true", at, false, true)
				m.databaseError = "charging_read_unavailable"
			}
			m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, at.Add(-time.Minute), at, at))
			if len(m.queue) != 0 || len(m.pending) != 0 {
				t.Fatal("database event bypassed source readiness or exact session binding")
			}
		})
	}
}

func TestChargingDatabaseNewEpisodeDoesNotBorrowPreviousEpisodeETA(t *testing.T) {
	for _, nextProcess := range []int{7, 8} {
		t.Run(fmt.Sprintf("process_%d", nextProcess), func(t *testing.T) {
			m := chargingDatabaseTestMonitor(t)
			at := time.Now().UTC().Add(-time.Minute)
			start := at.Add(-time.Minute)
			m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, start, at, at))
			chargingTestBaseline(m, 1, at)
			m.observeMessage(1, "healthy", "true", at, false, true)
			<-m.queue
			m.observeMessage(1, "time_to_full_charge", "1", at.Add(time.Second), false, true)
			m.observeMessage(1, "state", "online", at.Add(2*time.Second), false, true)
			m.observeMessage(1, "charging_state", "Complete", at.Add(3*time.Second), false, true)
			if len(m.queue) != 1 || m.store.Cars[1].Active {
				t.Fatal("prior episode did not end")
			}
			<-m.queue
			next := at.Add(30 * time.Second)
			if nextProcess != 7 {
				start = next
			}
			m.applyChargingDatabaseSnapshot(chargingTestSample(1, nextProcess, start, next, next))
			m.observeMessage(1, "state", "charging", next.Add(time.Second), false, true)
			m.observeMessage(1, "charging_state", "Charging", next.Add(2*time.Second), false, true)
			if len(m.queue) != 1 || !m.store.Cars[1].Active {
				t.Fatal("new coherent episode did not recover")
			}
			event := <-m.queue
			if event.RemainingSeconds != nil || event.EstimatedCompleteAt != nil {
				t.Fatalf("new episode borrowed previous ETA: %+v", event)
			}
		})
	}
}

func TestChargingDatabaseETARequiresReceiptBoundToAlreadyKnownProcess(t *testing.T) {
	for _, databaseFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("database_first_%t", databaseFirst), func(t *testing.T) {
			m := chargingDatabaseTestMonitor(t)
			at := time.Now().UTC().Add(-time.Minute)
			chargingTestBaseline(m, 1, at)
			if databaseFirst {
				m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, at.Add(-time.Minute), at, at))
			}
			m.observeMessage(1, "time_to_full_charge", "1", at.Add(time.Second), false, true)
			m.observeMessage(1, "healthy", "true", at.Add(2*time.Second), false, true)
			if !databaseFirst {
				m.applyChargingDatabaseSnapshot(chargingTestSample(1, 7, at.Add(-time.Minute), at, at.Add(3*time.Second)))
			}
			if len(m.queue) != 1 {
				t.Fatal("coherent initial episode did not start")
			}
			event := <-m.queue
			if (event.RemainingSeconds != nil) != databaseFirst || (event.EstimatedCompleteAt != nil) != databaseFirst {
				t.Fatal("ETA received before process identification was retroactively rebound, or known-process ETA was lost")
			}
		})
	}
}

func chargingLifecycleDeliveryFixture(t *testing.T) (*chargingNotificationMonitor, *pushSubscriberRegistry, pushSubscriber, chargingLiveActivityEvent, time.Time) {
	t.Helper()
	previousMonitor, previousRegistry := chargingPush, pushRegistry
	t.Cleanup(func() { chargingPush, pushRegistry = previousMonitor, previousRegistry })
	registry, sub := pushEligibilityFixture(t)
	m := chargingDatabaseTestMonitor(t)
	chargingPush, pushRegistry = m, registry
	at := time.Now().UTC().Add(-time.Second)
	m.applyChargingDatabaseSnapshot(chargingTestSample(2, 7, at.Add(-time.Minute), at, at))
	chargingTestBaseline(m, 2, at)
	m.observeMessage(2, "healthy", "true", at, false, true)
	if len(m.queue) != 1 {
		t.Fatal("synthetic charging start missing")
	}
	return m, registry, sub, <-m.queue, at
}

func chargingLifecycleTerminal(t *testing.T, m *chargingNotificationMonitor, start time.Time) chargingLiveActivityEvent {
	t.Helper()
	end := time.Now().UTC()
	sample := chargingTestSample(2, 7, start.Add(-time.Minute), end, end)
	sample.EndedAt = &end
	m.applyChargingDatabaseSnapshot(sample)
	if len(m.queue) != 1 {
		t.Fatal("synthetic database terminal missing")
	}
	return <-m.queue
}

func TestChargingLifecycleLateStartAfterDatabaseTerminalIsNotDelivered(t *testing.T) {
	m, registry, _, started, at := chargingLifecycleDeliveryFixture(t)
	var types []string
	registry.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var metadata pushPayloadMetadata
		if err := json.NewDecoder(request.Body).Decode(&metadata); err != nil {
			t.Fatal(err)
		}
		types = append(types, metadata.Type)
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}
	ended := chargingLifecycleTerminal(t, m, at)
	if err := m.deliver(ended); err != nil {
		t.Fatal(err)
	}
	// A previously constructed start arrives after the terminal was queued/sent.
	m.deliverWithRetry(started)
	if len(types) != 1 || types[0] != "charging_ended" || m.store.Cars[2].Active || m.store.Cars[2].StartDelivered {
		t.Fatal("late start revived or marked delivered after an exact terminal")
	}
}

func TestChargingLifecycleDurableRetryExpiresAfterDatabaseTerminal(t *testing.T) {
	m, registry, sub, started, at := chargingLifecycleDeliveryFixture(t)
	posts := 0
	transport := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		posts++
		status := http.StatusNoContent
		if posts == 1 {
			status = http.StatusServiceUnavailable
		}
		return &http.Response{StatusCode: status, Body: http.NoBody}, nil
	})}
	registry.http = transport
	if err := m.deliver(started); err != nil || len(registry.store.Outbox) != 1 {
		t.Fatal("retryable synthetic start did not enter durable outbox")
	}
	ended := chargingLifecycleTerminal(t, m, at)
	if err := m.deliver(ended); err != nil {
		t.Fatal(err)
	}
	// Both monitor terminal state and registry outbox are loaded from disk.
	reloadedMonitor := chargingDatabaseTestMonitor(t)
	reloadedMonitor.statePath = m.statePath
	reloadedMonitor.load()
	chargingPush = reloadedMonitor
	reloadedRegistry := newPushSubscriberRegistry()
	reloadedRegistry.http = transport
	reloadedRegistry.retryDue(time.Now().UTC().Add(time.Minute))
	if posts != 2 || len(reloadedRegistry.store.Outbox) != 0 || reloadedMonitor.store.Cars[2].Active {
		t.Fatal("durable start retry resurrected a persisted terminal")
	}
	wrong := pushPayloadMetadata{InstallationID: sub.InstallationID, SourceID: "other-source", EventID: "wrong-source", Type: "charging_started", CarID: 2, SessionID: scopedLiveActivitySessionID(started.SessionID, sub.SourceID, "charge"), ObservedAt: started.ObservedAt}
	payload, _ := json.Marshal(wrong)
	if result := reloadedRegistry.deliverJSONResult(sub, payload); result.Outcome != pushDeliveryExpired || posts != 2 {
		t.Fatal("old-source retry bypassed source scoping")
	}
}

func TestChargingLifecycleInFlightStartCompletesBeforeTerminalHTTP(t *testing.T) {
	m, registry, _, started, at := chargingLifecycleDeliveryFixture(t)
	entered := make(chan struct{})
	releaseStart := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(releaseStart) }) })
	var transportMu sync.Mutex
	var order []string
	registry.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var metadata pushPayloadMetadata
		if err := json.NewDecoder(request.Body).Decode(&metadata); err != nil {
			return nil, err
		}
		if metadata.Type == "charging_started" {
			close(entered)
			select {
			case <-releaseStart:
			case <-request.Context().Done():
				return nil, request.Context().Err()
			}
		}
		transportMu.Lock()
		order = append(order, metadata.Type)
		transportMu.Unlock()
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}
	results := make(chan error, 2)
	go func() { results <- m.deliver(started) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("synthetic start did not enter transport")
	}
	ended := chargingLifecycleTerminal(t, m, at)
	go func() { results <- m.deliver(ended) }()
	releaseOnce.Do(func() { close(releaseStart) })
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("charging HTTP sequencing blocked terminal cleanup")
		}
	}
	transportMu.Lock()
	defer transportMu.Unlock()
	if len(order) != 2 || order[0] != "charging_started" || order[1] != "charging_ended" || m.store.Cars[2].Active {
		t.Fatal("concurrent durable delivery could complete a start after terminal cleanup")
	}
}

func TestChargingLifecycleDeliveryGateHonorsCancellation(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	release, err := m.acquireChargingDelivery(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if unlocked, err := m.acquireChargingDelivery(ctx); !errors.Is(err, context.Canceled) || unlocked != nil {
		t.Fatal("blocked charging delivery ignored request cancellation")
	}
}

func TestChargingLifecycleDeliveryRejectsStaleDisconnectedDisabledOrReplacedStarts(t *testing.T) {
	for _, scenario := range []string{"expired_sample", "future_sample", "invalid_sample", "replaced_session", "stale_database_check", "database_error", "disconnected", "fleet_disabled", "changed_source"} {
		t.Run(scenario, func(t *testing.T) {
			m, registry, sub, started, _ := chargingLifecycleDeliveryFixture(t)
			posts := 0
			registry.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				posts++
				return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
			})}
			switch scenario {
			case "expired_sample":
				started.ObservedAt = time.Now().UTC().Add(-chargingSampleMaximumAge - time.Second).Format(time.RFC3339Nano)
			case "future_sample":
				started.ObservedAt = time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
			case "invalid_sample":
				started.ObservedAt = "not-a-clock"
			case "replaced_session":
				state := m.store.Cars[2]
				state.SessionID = "replacement-session"
				m.store.Cars[2] = state
			case "stale_database_check":
				sample := m.databaseObservations[2]
				sample.CheckedAt = time.Now().UTC().Add(-chargingDatabaseCheckMaxAge - time.Second)
				m.databaseObservations[2] = sample
			case "database_error":
				m.databaseError = "charging_read_unavailable"
			case "disconnected":
				m.resetMQTTBaseline()
			case "fleet_disabled":
				runtime := newTeslaMateMQTTRuntime(nil)
				runtime.applyFleet(map[int]bool{2: false}, time.Now().UTC())
				previous := mqttSourceRuntime.Swap(runtime)
				t.Cleanup(func() { mqttSourceRuntime.Store(previous) })
			case "changed_source":
				metadata := pushPayloadMetadata{InstallationID: sub.InstallationID, SourceID: "other-source", EventID: "wrong-active-source", Type: "charging_started", CarID: 2, SessionID: scopedLiveActivitySessionID(started.SessionID, sub.SourceID, "charge"), ObservedAt: started.ObservedAt}
				payload, _ := json.Marshal(metadata)
				if result := registry.deliverJSONResult(sub, payload); result.Outcome != pushDeliveryExpired || posts != 0 {
					t.Fatal("active session bypassed exact source binding")
				}
				return
			}
			if err := m.deliver(started); err != nil || posts != 0 || len(registry.store.Outbox) != 0 {
				t.Fatal("stale/ineligible or replaced start was sent or queued")
			}
		})
	}
}

func TestChargingLifecycleCurrentReplacementSessionAndDisabledTerminalCleanupRemainDeliverable(t *testing.T) {
	m, registry, _, oldStart, at := chargingLifecycleDeliveryFixture(t)
	var types []string
	registry.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var metadata pushPayloadMetadata
		if err := json.NewDecoder(request.Body).Decode(&metadata); err != nil {
			return nil, err
		}
		types = append(types, metadata.Type)
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}
	state := m.store.Cars[2]
	state.SessionID = "replacement-session"
	m.store.Cars[2] = state
	replacement := oldStart
	replacement.SessionID, replacement.EventID = state.SessionID, "replacement-start"
	if err := m.deliver(oldStart); err != nil || len(types) != 0 {
		t.Fatal("old logical session remained deliverable")
	}
	if err := m.deliver(replacement); err != nil || len(types) != 1 || types[0] != "charging_started" {
		t.Fatal("exact fresh replacement session was blocked")
	}
	runtime := newTeslaMateMQTTRuntime(nil)
	runtime.applyFleet(map[int]bool{2: false}, time.Now().UTC())
	previous := mqttSourceRuntime.Swap(runtime)
	t.Cleanup(func() { mqttSourceRuntime.Store(previous) })
	ended := chargingLifecycleTerminal(t, m, at)
	if err := m.deliver(ended); err != nil || len(types) != 2 || types[1] != "charging_ended" {
		t.Fatal("known exact terminal cleanup was blocked by fleet disable")
	}
}

func TestChargingDatabaseFleetDisabledBeforeApplyCannotScheduleStart(t *testing.T) {
	m := chargingDatabaseTestMonitor(t)
	at := time.Now().UTC().Add(-time.Second)
	chargingTestBaseline(m, 2, at)
	m.observeMessage(2, "healthy", "true", at, false, true)
	runtime := newTeslaMateMQTTRuntime(nil)
	runtime.applyFleet(map[int]bool{2: false}, time.Now().UTC())
	previous := mqttSourceRuntime.Swap(runtime)
	t.Cleanup(func() { mqttSourceRuntime.Store(previous) })
	m.applyChargingDatabaseSnapshot(chargingTestSample(2, 7, at.Add(-time.Minute), at, at))
	if len(m.queue) != 0 || m.store.Cars[2].Active {
		t.Fatal("fresh query result scheduled a start after logging was disabled")
	}
}

func TestChargingLifecycleStaleInitialStartIsNotAcknowledgedAndFreshReadRecoversStart(t *testing.T) {
	m, registry, _, started, at := chargingLifecycleDeliveryFixture(t)
	posts := 0
	registry.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		posts++
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}
	started.ObservedAt = time.Now().UTC().Add(-chargingSampleMaximumAge - time.Second).Format(time.RFC3339Nano)
	state := m.store.Cars[2]
	state.LastQueuedAt, state.LastEventSampleAt = started.ObservedAt, started.ObservedAt
	m.store.Cars[2] = state
	m.deliverWithRetry(started)
	if posts != 0 || m.store.Cars[2].StartDelivered || len(m.store.Delivered) != 0 || !m.store.Cars[2].Active {
		t.Fatal("active but unsent stale start was acknowledged as delivered")
	}
	now := time.Now().UTC()
	m.applyChargingDatabaseSnapshot(chargingTestSample(2, 7, at.Add(-time.Minute), now, now))
	if len(m.queue) != 1 {
		t.Fatal("fresh read did not recover the missing initial start")
	}
	recovered := <-m.queue
	if recovered.Type != "charging_started" {
		t.Fatal("missing initial card was incorrectly recovered with only an update")
	}
	m.deliverWithRetry(recovered)
	if posts != 1 || !m.store.Cars[2].StartDelivered {
		t.Fatal("actual accepted fresh start was not acknowledged")
	}
}

func TestChargingLifecycleQueuedStartWaitsForActualAcceptanceWithoutDuplicateStart(t *testing.T) {
	m, registry, _, started, at := chargingLifecycleDeliveryFixture(t)
	registry.addDeliveryResolver(m.reconcileChargingDeliveryResolution)
	posts := 0
	registry.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		posts++
		status := http.StatusNoContent
		if posts == 1 {
			status = http.StatusServiceUnavailable
		}
		return &http.Response{StatusCode: status, Body: http.NoBody}, nil
	})}
	m.deliverWithRetry(started)
	if posts != 1 || len(registry.store.Outbox) != 1 || m.store.Cars[2].StartDelivered || len(m.store.Delivered) != 0 {
		t.Fatal("durably queued start was mistaken for actual push acceptance")
	}
	state := m.store.Cars[2]
	state.LastQueuedAt = time.Now().UTC().Add(-chargingUpdateMinimumInterval - time.Second).Format(time.RFC3339)
	m.store.Cars[2] = state
	now := time.Now().UTC()
	m.applyChargingDatabaseSnapshot(chargingTestSample(2, 7, at.Add(-time.Minute), now, now))
	if len(m.queue) != 0 || posts != 1 {
		t.Fatal("fresh read duplicated an exact still-usable queued start")
	}
	registry.retryDue(now.Add(time.Minute))
	if posts != 2 || len(registry.store.Outbox) != 0 || !m.store.Cars[2].StartDelivered {
		t.Fatal("actual outbox acceptance did not resolve the current exact start")
	}
	if len(m.queue) != 1 || (<-m.queue).Type != "charging_updated" {
		t.Fatal("accepted queued start did not advance with exactly one latest update")
	}
}

func TestChargingLifecycleQueuedStartRestartBeforeReadyCanRecoverFromFreshData(t *testing.T) {
	m, registry, _, started, at := chargingLifecycleDeliveryFixture(t)
	posts := 0
	transport := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		posts++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
	})}
	registry.http = transport
	m.deliverWithRetry(started)
	m.mu.Lock()
	state := m.store.Cars[2]
	state.LastQueuedAt = time.Now().UTC().Add(-chargingUpdateMinimumInterval - time.Second).Format(time.RFC3339)
	m.store.Cars[2] = state
	if err := m.saveLocked(); err != nil {
		m.mu.Unlock()
		t.Fatal(err)
	}
	m.mu.Unlock()
	restored := chargingDatabaseTestMonitor(t)
	restored.statePath = m.statePath
	restored.load()
	chargingPush = restored
	restoredRegistry := newPushSubscriberRegistry()
	restoredRegistry.http = transport
	pushRegistry = restoredRegistry
	restoredRegistry.addDeliveryResolver(restored.reconcileChargingDeliveryResolution)
	restoredRegistry.retryDue(time.Now().UTC().Add(time.Minute))
	if posts != 1 || len(restoredRegistry.store.Outbox) != 0 || restored.store.Cars[2].StartDelivered || !restored.store.Cars[2].Active {
		t.Fatal("restart without readiness acknowledged an unsent queued start")
	}
	now := time.Now().UTC()
	// MQTT recovers first; the next timestamped DB read must not need another message.
	chargingTestBaseline(restored, 2, now)
	restored.observeMessage(2, "healthy", "true", now, false, true)
	restored.applyChargingDatabaseSnapshot(chargingTestSample(2, 7, at.Add(-time.Minute), now, now))
	if len(restored.queue) != 1 || (<-restored.queue).Type != "charging_started" {
		t.Fatal("fresh recovery after queued-start expiry produced no push-to-start")
	}
}

func TestChargingLifecycleExpiredPendingStartCannotBlockFreshStart(t *testing.T) {
	m, registry, _, started, at := chargingLifecycleDeliveryFixture(t)
	registry.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
	})}
	m.deliverWithRetry(started)
	registry.mu.Lock()
	var metadata map[string]any
	if len(registry.store.Outbox) != 1 || json.Unmarshal(registry.store.Outbox[0].Payload, &metadata) != nil {
		registry.mu.Unlock()
		t.Fatal("queued synthetic start missing")
	}
	metadata["observed_at"] = time.Now().UTC().Add(-chargingSampleMaximumAge - time.Second).Format(time.RFC3339Nano)
	registry.store.Outbox[0].Payload, _ = json.Marshal(metadata)
	registry.mu.Unlock()
	state := m.store.Cars[2]
	state.LastQueuedAt = time.Now().UTC().Add(-chargingUpdateMinimumInterval - time.Second).Format(time.RFC3339)
	m.store.Cars[2] = state
	now := time.Now().UTC()
	m.applyChargingDatabaseSnapshot(chargingTestSample(2, 7, at.Add(-time.Minute), now, now))
	if len(m.queue) != 1 || (<-m.queue).Type != "charging_started" {
		t.Fatal("expired pending payload was re-freshened by queue receipt and stranded a new start")
	}
}
