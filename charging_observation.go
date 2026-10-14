package main

// TeslaMate's scalar MQTT topics and cached HTTP responses have no sample
// clock. Only persisted charges.date establishes charging-data freshness here.
// This reader is passive: it never calls Tesla, wakes a car or writes history.
import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/lib/pq"
)

const (
	chargingDatabasePollInterval = 30 * time.Second
	chargingDatabaseReadTimeout  = 2 * time.Second
	chargingSampleMaximumAge     = 180 * time.Second
	chargingDatabaseCheckMaxAge  = 60 * time.Second
)

// At most one process and three indexed sample lookups per enabled car. A
// bound activity reads its exact process, even if another charge has started.
// All row sources recheck car membership and enabled status in the database.
const chargingDatabaseQuery = `SELECT c.id, cp.id, cp.start_date, cp.end_date,
 sample.date, sample.charger_power, sample.battery_level, sample.rated_battery_range_km, previous.date, positive.date
 FROM unnest($1::integer[], $2::integer[]) AS wanted(car_id, process_id)
 JOIN cars c ON c.id = wanted.car_id
 JOIN car_settings cs ON cs.id = c.settings_id AND cs.enabled IS TRUE
 LEFT JOIN LATERAL (
   SELECT id, start_date, end_date FROM charging_processes
   WHERE car_id = c.id AND (wanted.process_id = 0 OR id = wanted.process_id)
   ORDER BY start_date DESC, id DESC LIMIT 1
 ) cp ON TRUE
 LEFT JOIN LATERAL (
   SELECT date, charger_power, battery_level, rated_battery_range_km FROM charges WHERE charging_process_id = cp.id
   ORDER BY date DESC, id DESC LIMIT 1
 ) sample ON TRUE
 LEFT JOIN LATERAL (
   SELECT date FROM charges WHERE charging_process_id = cp.id AND date < sample.date
   ORDER BY date DESC, id DESC LIMIT 1
 ) previous ON TRUE
 LEFT JOIN LATERAL (
   SELECT date FROM charges WHERE charging_process_id = cp.id AND charger_power > 0
   ORDER BY date DESC, id DESC LIMIT 1
 ) positive ON TRUE
 ORDER BY c.id LIMIT 256`

type chargingDatabaseSnapshot struct {
	CarID               int
	ProcessID           int
	StartedAt           time.Time
	CheckedAt           time.Time
	EndedAt             *time.Time
	LastSampleAt        *time.Time
	PreviousSampleAt    *time.Time
	LastPositivePowerAt *time.Time
	PowerKW             *float64
	BatteryLevel        *int
	RatedRangeKM        *float64
	TerminalConfirmedAt *time.Time
}

// Additive companion-status contract. EndedAt is the logger's end_date, never
// a claimed physical stop time. A terminal gap is retained after confirmation.
type chargingDatabaseObservation struct {
	Status                   string     `json:"status"`
	Source                   string     `json:"source"`
	ProcessID                int        `json:"charging_process_id,omitempty"`
	StartedAt                *time.Time `json:"process_started_at,omitempty"`
	EndedAt                  *time.Time `json:"process_ended_at,omitempty"`
	LastSampleAt             *time.Time `json:"last_sample_at,omitempty"`
	LastPositivePowerAt      *time.Time `json:"last_positive_power_at,omitempty"`
	SampleAgeSeconds         *int64     `json:"sample_age_seconds,omitempty"`
	TerminalSampleGapSeconds *int64     `json:"terminal_sample_gap_seconds,omitempty"`
	CheckedAt                *time.Time `json:"checked_at,omitempty"`
	TerminalConfirmedAt      *time.Time `json:"terminal_confirmed_at,omitempty"`
	Error                    string     `json:"error,omitempty"`
}

func chargingSnapshotFresh(s chargingDatabaseSnapshot, now time.Time) bool {
	return s.ProcessID > 0 && !s.StartedAt.IsZero() && s.EndedAt == nil &&
		s.LastSampleAt != nil && !s.LastSampleAt.After(now) && now.Sub(*s.LastSampleAt) <= chargingSampleMaximumAge &&
		!s.CheckedAt.IsZero() && !s.CheckedAt.After(now) && now.Sub(s.CheckedAt) <= chargingDatabaseCheckMaxAge
}

func readChargingDatabase(ctx context.Context, database *sql.DB, ids, processes []int, pollStartedAt time.Time) ([]chargingDatabaseSnapshot, error) {
	if database == nil || len(ids) == 0 || len(ids) > mqttMaximumCars || len(ids) != len(processes) {
		return nil, errors.New("charging_read_invalid")
	}
	for index, id := range ids {
		if id <= 0 || processes[index] < 0 || (index > 0 && id <= ids[index-1]) {
			return nil, errors.New("charging_read_invalid")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, chargingDatabaseReadTimeout)
	defer cancel()
	rows, err := database.QueryContext(ctx, chargingDatabaseQuery, pq.Array(ids), pq.Array(processes))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]chargingDatabaseSnapshot, 0, len(ids))
	seen := make(map[int]bool, len(ids))
	for rows.Next() {
		var s chargingDatabaseSnapshot
		var process sql.NullInt64
		var start, end, sample, previous, positive sql.NullTime
		var power, ratedRange sql.NullFloat64
		var battery sql.NullInt64
		if err := rows.Scan(&s.CarID, &process, &start, &end, &sample, &power, &battery, &ratedRange, &previous, &positive); err != nil {
			return nil, err
		}
		index := sort.SearchInts(ids, s.CarID)
		if index == len(ids) || ids[index] != s.CarID || seen[s.CarID] || len(out) >= len(ids) {
			return nil, errors.New("charging_read_invalid")
		}
		if process.Valid {
			s.ProcessID = int(process.Int64)
			if s.ProcessID <= 0 || !start.Valid || (processes[index] > 0 && s.ProcessID != processes[index]) {
				return nil, errors.New("charging_read_invalid")
			}
			s.StartedAt = start.Time.UTC()
			if end.Valid {
				at := end.Time.UTC()
				s.EndedAt = &at
			}
			if sample.Valid {
				at := sample.Time.UTC()
				s.LastSampleAt = &at
			}
			if previous.Valid {
				at := previous.Time.UTC()
				s.PreviousSampleAt = &at
			}
			if positive.Valid {
				at := positive.Time.UTC()
				s.LastPositivePowerAt = &at
			}
			if power.Valid && power.Float64 >= 0 && power.Float64 <= 350 {
				value := power.Float64
				s.PowerKW = &value
			}
			if battery.Valid && battery.Int64 >= 0 && battery.Int64 <= 100 {
				value := int(battery.Int64)
				s.BatteryLevel = &value
			}
			if ratedRange.Valid && ratedRange.Float64 >= 0 && ratedRange.Float64 <= 1000 {
				value := ratedRange.Float64
				s.RatedRangeKM = &value
			}
			acquiredAt := time.Now().UTC()
			// Reject malformed/future source clocks rather than treating them as
			// fresh or using them to end an activity. Initial samples may precede
			// the logger-created process by a small acquisition interval.
			if s.StartedAt.After(acquiredAt) || (s.EndedAt != nil && (s.EndedAt.Before(s.StartedAt) || s.EndedAt.After(acquiredAt))) ||
				(s.LastSampleAt != nil && (s.LastSampleAt.Before(s.StartedAt.Add(-2*time.Minute)) || s.LastSampleAt.After(acquiredAt) || (s.EndedAt != nil && s.LastSampleAt.After(*s.EndedAt)))) ||
				(s.PreviousSampleAt != nil && (s.LastSampleAt == nil || !s.PreviousSampleAt.Before(*s.LastSampleAt))) ||
				(s.LastPositivePowerAt != nil && (s.LastSampleAt == nil || s.LastPositivePowerAt.After(*s.LastSampleAt))) {
				return nil, errors.New("charging_read_invalid_clock")
			}
		}
		seen[s.CarID] = true
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Receipt/validation happen after the read. A sample inserted while
	// the query ran is not a future clock merely because it follows the
	// poll start. Read the completion clock itself: reconstructing it from
	// poll start plus elapsed time omits the caller-to-reader entry interval.
	checkedAt := time.Now().UTC()
	if checkedAt.Before(pollStartedAt) {
		return nil, errors.New("charging_read_invalid_clock")
	}
	for index := range out {
		out[index].CheckedAt = checkedAt
	}
	return out, nil
}

func (m *chargingNotificationMonitor) startDatabaseObservation(database *sql.DB) func() {
	m.mu.Lock()
	m.databaseManaged = true
	m.databaseObservations = map[int]chargingDatabaseSnapshot{}
	m.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.reconcileChargingDatabase(ctx, database, time.Now().UTC())
		ticker := time.NewTicker(chargingDatabasePollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.reconcileChargingDatabase(ctx, database, time.Now().UTC())
			}
		}
	}()
	return func() { cancel(); <-done }
}

func (m *chargingNotificationMonitor) reconcileChargingDatabase(ctx context.Context, database *sql.DB, now time.Time) {
	runtime := mqttSourceRuntime.Load()
	if runtime == nil {
		return
	}
	runtime.mu.RLock()
	ids := make([]int, 0, len(runtime.cars))
	if mqttFleetSnapshotUsable(runtime.lastFleetSuccess, now) {
		for id, enabled := range runtime.cars {
			if enabled {
				ids = append(ids, id)
			}
		}
	}
	runtime.mu.RUnlock()
	sort.Ints(ids)
	if len(ids) == 0 {
		return
	}
	m.mu.Lock()
	processes := make([]int, len(ids))
	for index, id := range ids {
		if state := m.store.Cars[id]; state.Active {
			processes[index] = state.DatabaseProcessID
		}
	}
	m.mu.Unlock()
	snapshots, err := readChargingDatabase(ctx, database, ids, processes, now)
	m.mu.Lock()
	if err != nil {
		m.databaseError = "charging_read_unavailable"
		var pgError *pq.Error
		switch {
		case errors.Is(err, context.DeadlineExceeded):
			m.databaseError = "charging_read_timeout"
		case errors.As(err, &pgError) && (pgError.Code == "42703" || pgError.Code == "42P01" || pgError.Code == "42883"):
			m.databaseError = "charging_schema_unsupported"
		case err.Error() == "charging_read_invalid" || err.Error() == "charging_read_invalid_clock":
			m.databaseError = err.Error()
		}
		m.mu.Unlock()
		return
	}
	m.databaseError = ""
	m.mu.Unlock()
	for _, s := range snapshots {
		// A car can be disabled/removed while the bounded query is in flight.
		if mqttCarEligible(s.CarID) {
			m.applyChargingDatabaseSnapshot(s)
		}
	}
}

func (m *chargingNotificationMonitor) applyChargingDatabaseSnapshot(s chargingDatabaseSnapshot) {
	m.mu.Lock()
	if m.databaseObservations == nil {
		m.databaseObservations = map[int]chargingDatabaseSnapshot{}
	}
	old := m.databaseObservations[s.CarID]
	state := m.store.Cars[s.CarID]
	if !old.CheckedAt.IsZero() && s.CheckedAt.Before(old.CheckedAt) {
		m.mu.Unlock()
		return
	}
	if old.ProcessID == s.ProcessID && old.StartedAt.Equal(s.StartedAt) && old.EndedAt != nil && s.EndedAt == nil {
		// A delayed/open snapshot cannot undo a confirmed terminal for the
		// same immutable process. Refresh only the database check clock.
		old.CheckedAt = s.CheckedAt
		s = old
	}
	if s.EndedAt != nil {
		at := s.CheckedAt
		s.TerminalConfirmedAt = &at
		if old.ProcessID == s.ProcessID && old.StartedAt.Equal(s.StartedAt) && old.TerminalConfirmedAt != nil {
			s.TerminalConfirmedAt = old.TerminalConfirmedAt
		} else if state.DatabaseProcessID == s.ProcessID && state.DatabaseStartedAt == s.StartedAt.Format(time.RFC3339Nano) {
			if first, err := time.Parse(time.RFC3339Nano, state.DatabaseTerminalConfirmedAt); err == nil && !first.After(s.CheckedAt) {
				s.TerminalConfirmedAt = &first
			}
		}
	}
	m.databaseObservations[s.CarID] = s
	// Pre-upgrade activities without an immutable process binding remain
	// unbound: even a currently open/latest process cannot prove it is the
	// same charge. Their coherent MQTT terminal still clears them normally.

	var event *chargingLiveActivityEvent
	stateChanged := false
	if s.ProcessID > 0 && state.DatabaseProcessID == s.ProcessID && state.DatabaseStartedAt == s.StartedAt.Format(time.RFC3339Nano) && s.EndedAt != nil {
		stateChanged = state.DatabaseTerminalProcessID != s.ProcessID || state.DatabaseTerminalConfirmedAt == ""
		state.DatabaseTerminalProcessID = s.ProcessID
		state.DatabaseTerminalConfirmedAt = s.TerminalConfirmedAt.Format(time.RFC3339Nano)
		if state.Active {
			stateChanged = true
			state.Active = false
			if timer := m.pending[s.CarID]; timer != nil {
				timer.Stop()
				delete(m.pending, s.CarID)
			}
			ended := m.makeEventLocked(s.CarID, &state, "charging_ended", s.CheckedAt)
			ended.ObservedAt = s.EndedAt.Format(time.RFC3339Nano)
			ended.ObservationMode = "database_terminal_confirmed"
			ended.PowerKW, ended.RemainingSeconds, ended.EstimatedCompleteAt = nil, nil, nil
			event = &ended
		}
	}
	if stateChanged {
		m.store.Cars[s.CarID] = state
		_ = m.saveLocked()
	}
	if event == nil && s.EndedAt == nil && chargingSnapshotFresh(s, s.CheckedAt) && m.mqttManaged && m.chargingMQTTReadyLocked(s.CarID) {
		// MQTT may finish its reconnect baseline before the first database
		// read. A later advancing source sample is itself a scheduling event;
		// it must not wait for an unrelated scalar publication to make progress.
		event = m.reconcileChargingActivityLocked(s.CarID, s.CheckedAt, false)
	}
	m.mu.Unlock()
	if event != nil {
		m.enqueue(*event)
	}
}

func (m *chargingNotificationMonitor) chargingObservation(carID int, now time.Time) chargingDatabaseObservation {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.databaseObservations[carID]
	out := chargingDatabaseObservation{Status: "unknown", Source: "teslamate_database", Error: m.databaseError}
	if !s.CheckedAt.IsZero() {
		at := s.CheckedAt
		out.CheckedAt = &at
	}
	if s.ProcessID > 0 {
		out.ProcessID = s.ProcessID
		at := s.StartedAt
		out.StartedAt = &at
		out.EndedAt, out.LastSampleAt, out.LastPositivePowerAt = s.EndedAt, s.LastSampleAt, s.LastPositivePowerAt
		if s.LastSampleAt != nil && !s.LastSampleAt.After(now) {
			age := int64(now.Sub(*s.LastSampleAt).Seconds())
			out.SampleAgeSeconds = &age
		}
		if s.EndedAt != nil && s.PreviousSampleAt != nil && !s.EndedAt.Before(*s.PreviousSampleAt) {
			gap := int64(s.EndedAt.Sub(*s.PreviousSampleAt).Seconds())
			out.TerminalSampleGapSeconds = &gap
		}
		out.TerminalConfirmedAt = s.TerminalConfirmedAt
	}
	if s.EndedAt != nil {
		// A confirmed immutable historical terminal does not turn back into
		// active/unknown merely because a later database read failed.
		out.Status = "terminal_confirmed"
		return out
	}
	if out.Error != "" || s.CheckedAt.IsZero() || s.CheckedAt.After(now) || now.Sub(s.CheckedAt) > chargingDatabaseCheckMaxAge {
		return out
	}
	if s.ProcessID == 0 {
		out.Status = "no_session"
		return out
	}
	if s.LastSampleAt == nil {
		return out
	}
	out.Status = "data_gap"
	if chargingSnapshotFresh(s, now) {
		out.Status = "fresh"
	}
	return out
}

// Repeated healthy/MQTT scalar receipts cannot advance this sample clock.
func (m *chargingNotificationMonitor) chargingDataPublishableLocked(carID int, state carChargingState, now time.Time, requireAdvance bool) bool {
	if !m.databaseManaged {
		return true
	} // isolated MQTT-only tests/constructors
	s := m.databaseObservations[carID]
	if state.Active && (state.DatabaseProcessID == 0 || state.DatabaseStartedAt != s.StartedAt.Format(time.RFC3339Nano)) {
		return false
	}
	if m.databaseError != "" || !chargingSnapshotFresh(s, now) || s.BatteryLevel == nil || (state.DatabaseProcessID != 0 && state.DatabaseProcessID != s.ProcessID) || (state.DatabaseTerminalProcessID != 0 && state.DatabaseTerminalProcessID == s.ProcessID) {
		return false
	}
	if state.MQTTTerminalProcessID == s.ProcessID {
		baseline := m.mqttBaselines[carID]
		current := m.store.Cars[carID]
		terminal, err := time.Parse(time.RFC3339Nano, state.MQTTTerminalObservedAt)
		// A scalar terminal is an observation, not a permanent database end.
		// Require a later source sample AND both later, real Charging state
		// publications. Advancing a timestamp/healthy receipt alone cannot
		// resume it; valid zero-power charging establishment remains possible.
		if err != nil || !s.LastSampleAt.After(terminal) || !baseline.stateLive || !baseline.chargeLive ||
			!baseline.stateLiveAt.After(terminal) || !baseline.chargeLiveAt.After(terminal) ||
			current.VehicleState != "charging" || current.ChargingState != "charging" {
			return false
		}
	}
	if requireAdvance {
		last, _ := time.Parse(time.RFC3339Nano, state.LastEventSampleAt)
		if !s.LastSampleAt.After(last) {
			return false
		}
	}
	return true
}
