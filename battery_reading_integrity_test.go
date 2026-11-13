package main

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func TestBatteryIntegrityQueuedZeroIsNotRetriedAfterRecovery(t *testing.T) {
	m, _ := newLowBatteryTestMonitor(t, 1)
	calls := 0
	pushRegistry.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody}, nil
	})}
	at := time.Now().UTC()
	m.store.Cars[1] = lowBatteryTestState(64)
	m.evaluate(1, at)
	s := m.store.Cars[1]
	s.BatteryLevel, s.UsableBatteryLevel = lowBatteryLevel(0), lowBatteryLevel(0)
	m.store.Cars[1] = s
	m.evaluate(1, at)
	if calls != 1 || len(pushRegistry.store.Outbox) != 1 {
		t.Fatal("synthetic genuine zero should enter retry queue")
	}
	s = m.store.Cars[1]
	s.BatteryLevel, s.UsableBatteryLevel = lowBatteryLevel(64), lowBatteryLevel(63)
	m.store.Cars[1] = s
	m.evaluate(1, at)
	pushRegistry.retryDue(at.Add(time.Minute))
	if calls != 1 || len(pushRegistry.store.Outbox) != 0 {
		t.Fatal("recovered zero reached relay on retry")
	}
}

func TestBatteryIntegrityPartialTopicOrderingDoesNotMaskLaterTrueZero(t *testing.T) {
	m, captured := newLowBatteryTestMonitor(t, 1)
	at := time.Now().UTC()
	s := lowBatteryTestState(64)
	s.UsableBatteryLevel = lowBatteryLevel(63)
	m.store.Cars[1] = s
	m.evaluate(1, at)
	m.observeMessage(1, "battery_level", "0", at, false)
	stopLowBatteryTimers(m)
	m.evaluate(1, at)
	if len(*captured) != 0 {
		t.Fatal("partial contradictory snapshot pushed")
	}
	m.observeMessage(1, "usable_battery_level", "0", at, false)
	stopLowBatteryTimers(m)
	m.evaluate(1, at)
	if len(*captured) != 1 || (*captured)[0].BatteryLevel != 0 {
		t.Fatal("confirmed later zero was masked")
	}
}

func stopLowBatteryTimers(m *lowBatteryNotificationMonitor) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, timer := range m.timers {
		timer.Stop()
		delete(m.timers, id)
	}
}

func TestBatteryIntegrityLilyZeroAndGarbledGearNeverPush(t *testing.T) {
	m, captured := newLowBatteryTestMonitor(t, 1)
	t.Cleanup(func() { stopLowBatteryTimers(m) })
	at := time.Date(2026, 11, 13, 22, 30, 0, 0, time.UTC)
	m.store.Cars[1] = lowBatteryTestState(64)
	m.evaluate(1, at)
	for _, test := range []struct {
		gear   string
		usable *int
	}{{"P", lowBatteryLevel(63)}, {"37.123456", nil}, {"37.123456", lowBatteryLevel(63)}} {
		s := m.store.Cars[1]
		s.BatteryLevel = lowBatteryLevel(0)
		s.ShiftState = test.gear
		s.UsableBatteryLevel = test.usable
		m.store.Cars[1] = s
		m.evaluate(1, at.Add(time.Minute))
		if len(*captured) != 0 || m.store.Cars[1].Latched {
			t.Fatal("contradictory SOC must not create an episode or push")
		}
	}
	// The actual monitor path must subscribe to and retain the independent SOC.
	m.observeMessage(1, "usable_battery_level", "63", at, false)
	stopLowBatteryTimers(m)
	if m.store.Cars[1].UsableBatteryLevel == nil || *m.store.Cars[1].UsableBatteryLevel != 63 {
		t.Fatal("usable SOC not observed")
	}
	found := false
	for _, f := range lowBatteryMQTTFields {
		if f == "usable_battery_level" {
			found = true
		}
	}
	if !found {
		t.Fatal("independent SOC not subscribed")
	}
}

func TestBatteryIntegrityRealZeroStillAlertsAndRecoveryRearms(t *testing.T) {
	m, captured := newLowBatteryTestMonitor(t, 1)
	at := time.Date(2026, 11, 13, 22, 30, 0, 0, time.UTC)
	m.store.Cars[1] = lowBatteryTestState(64)
	m.evaluate(1, at)
	s := m.store.Cars[1]
	s.BatteryLevel = lowBatteryLevel(0)
	s.UsableBatteryLevel = lowBatteryLevel(0)
	m.store.Cars[1] = s
	m.evaluate(1, at.Add(time.Minute))
	if len(*captured) != 1 || (*captured)[0].BatteryLevel != 0 || (*captured)[0].Type != "vehicle_low_battery_severe" {
		t.Fatalf("true zero must alert: %+v", *captured)
	}
	s = m.store.Cars[1]
	s.BatteryLevel = lowBatteryLevel(64)
	s.UsableBatteryLevel = lowBatteryLevel(63)
	m.store.Cars[1] = s
	m.evaluate(1, at.Add(2*time.Minute))
	if m.store.Cars[1].Latched {
		t.Fatal("recovery did not rearm")
	}
	s = m.store.Cars[1]
	s.BatteryLevel = lowBatteryLevel(19)
	s.UsableBatteryLevel = lowBatteryLevel(18)
	m.store.Cars[1] = s
	m.evaluate(1, at.Add(3*time.Minute))
	if len(*captured) != 2 || (*captured)[1].Type != "vehicle_low_battery" {
		t.Fatal("subsequent real low lost")
	}
}

func TestBatteryIntegrityUnknownContextDoesNotConsumeSnoozeOrRearm(t *testing.T) {
	m, captured := newLowBatteryTestMonitor(t, 1)
	at := time.Date(2026, 11, 13, 22, 30, 0, 0, time.UTC)
	m.store.Cars[1] = lowBatteryTestState(50)
	m.evaluate(1, at)
	s := m.store.Cars[1]
	s.BatteryLevel = lowBatteryLevel(19)
	m.store.Cars[1] = s
	m.evaluate(1, at.Add(time.Minute))
	id := (*captured)[0].InstallationID
	s = m.store.Cars[1]
	s.ShiftState = "37.123456"
	s.BatteryLevel = lowBatteryLevel(70)
	s.Actions[id] = lowBatteryActionState{Action: "snooze_4h", SnoozeUntil: at.Add(time.Minute).Format(time.RFC3339)}
	m.store.Cars[1] = s
	m.evaluate(1, at.Add(2*time.Minute))
	m.processDueReminders(at.Add(3 * time.Minute))
	s = m.store.Cars[1]
	if !s.Latched || s.Actions[id].Action != "snooze_4h" {
		t.Fatal("unknown context must not rearm or consume reminder")
	}
	s.ShiftState = "P"
	s.BatteryLevel = lowBatteryLevel(19)
	m.store.Cars[1] = s
	m.processDueReminders(at.Add(4 * time.Minute))
	if len(*captured) != 2 || (*captured)[1].Type != "vehicle_low_battery_reminder" {
		t.Fatal("verified reminder must resume")
	}
}

func TestBatteryIntegrityPendingDeliveryChecksCarEpisodeAndRecovery(t *testing.T) {
	m, _ := newLowBatteryTestMonitor(t, 1)
	s := lowBatteryTestState(0)
	s.Initialized = true
	s.Latched = true
	s.EpisodeID = "synthetic"
	m.store.Cars[1] = s
	payload := func(car int, episode string) []byte {
		b, _ := json.Marshal(lowBatteryPushEvent{CarID: car, EpisodeID: episode, BatteryLevel: 0, Type: "vehicle_low_battery_severe"})
		return b
	}
	if !m.acceptsPayload(payload(1, "synthetic")) {
		t.Fatal("verified zero rejected")
	}
	if m.acceptsPayload(payload(2, "synthetic")) || m.acceptsPayload(payload(1, "old")) {
		t.Fatal("cross-car or old episode accepted")
	}
	s.ShiftState = "37.123456"
	m.store.Cars[1] = s
	if m.acceptsPayload(payload(1, "synthetic")) {
		t.Fatal("garbled current context accepted")
	}
	s.ShiftState = "P"
	s.BatteryLevel = lowBatteryLevel(64)
	m.store.Cars[1] = s
	if m.acceptsPayload(payload(1, "synthetic")) {
		t.Fatal("recovered zero retried")
	}
	s.BatteryLevel = lowBatteryLevel(9)
	m.store.Cars[1] = s
	if m.acceptsPayload(payload(1, "synthetic")) {
		t.Fatal("old zero replaced current9")
	}
}

func TestBatteryIntegrityValidContextAndSOCBounds(t *testing.T) {
	for _, n := range []int{0, 1, 9, 19, 20, 64, 100} {
		s := lowBatteryTestState(n)
		if !lowBatteryReadingConfirmed(s) {
			t.Fatalf("valid SOC%d rejected", n)
		}
	}
	for _, n := range []int{-1, 101} {
		if lowBatteryReadingConfirmed(lowBatteryTestState(n)) {
			t.Fatal("invalid SOC accepted")
		}
	}
	for _, gear := range []string{"", "P", "D", "N", "R", " p "} {
		s := lowBatteryTestState(0)
		s.ShiftState = gear
		if !lowBatteryReadingConfirmed(s) {
			t.Fatal("valid gear rejected")
		}
	}
	s := lowBatteryTestState(18)
	s.UsableBatteryLevel = lowBatteryLevel(20)
	if !lowBatteryReadingConfirmed(s) {
		t.Fatal("rounding tolerance lost")
	}
	s.UsableBatteryLevel = lowBatteryLevel(21)
	if lowBatteryReadingConfirmed(s) {
		t.Fatal("contradictory SOC accepted")
	}
}

func TestBatteryIntegrityEmptyOrInvalidUsableCannotClearIndependentGuard(t *testing.T) {
	for _, raw := range []string{"", "NaN", "-1", "101"} {
		for _, retained := range []bool{false, true} {
			t.Run(raw+string(rune('0'+boolNumber(retained))), func(t *testing.T) {
				m, captured := newLowBatteryTestMonitor(t, 1)
				at := time.Now().UTC()
				s := lowBatteryTestState(64)
				s.UsableBatteryLevel = lowBatteryLevel(63)
				m.store.Cars[1] = s
				m.evaluate(1, at)
				m.observeMessage(1, "battery_level", "0", at, false)
				stopLowBatteryTimers(m)
				m.evaluate(1, at)
				m.observeMessage(1, "usable_battery_level", raw, at, retained)
				stopLowBatteryTimers(m)
				m.evaluate(1, at)
				s = m.store.Cars[1]
				if s.UsableBatteryLevel == nil || *s.UsableBatteryLevel != 63 || len(*captured) != 0 || s.Latched {
					t.Fatal("unknown usable removed the independent SOC guard")
				}
			})
		}
	}
}
func boolNumber(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestBatteryIntegrityQueuedTrueZeroSurvivesReconnectUntilConfirmedBaseline(t *testing.T) {
	m, _ := newLowBatteryTestMonitor(t, 1)
	calls := 0
	succeed := false
	pushRegistry.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		status := http.StatusServiceUnavailable
		if succeed {
			status = http.StatusNoContent
		}
		return &http.Response{StatusCode: status, Body: http.NoBody}, nil
	})}
	at := time.Now().UTC()
	m.store.Cars[1] = lowBatteryTestState(64)
	m.evaluate(1, at)
	s := m.store.Cars[1]
	s.BatteryLevel = lowBatteryLevel(0)
	s.UsableBatteryLevel = lowBatteryLevel(0)
	m.store.Cars[1] = s
	m.evaluate(1, at)
	if calls != 1 || len(pushRegistry.store.Outbox) != 1 {
		t.Fatal("genuine zero was not queued")
	}
	m.resetObservationBaseline()
	pushRegistry.retryDue(at.Add(3 * time.Second))
	if calls != 1 || len(pushRegistry.store.Outbox) != 1 {
		t.Fatal("unconfirmed reconnect must defer, not delete or deliver")
	}
	for _, pair := range [][2]string{{"state", "asleep"}, {"charging_state", "Disconnected"}, {"shift_state", "P"}, {"battery_level", "0"}, {"usable_battery_level", "0"}} {
		m.observeMessage(1, pair[0], pair[1], at, true)
		stopLowBatteryTimers(m)
	}
	m.evaluate(1, at.Add(4*time.Second))
	succeed = true
	pushRegistry.retryDue(at.Add(time.Minute))
	if calls != 2 || len(pushRegistry.store.Outbox) != 0 {
		t.Fatal("still-low verified zero must resume the original queued delivery")
	}
}
