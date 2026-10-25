package main

// TeslaMate 4.4.0 publishes empty payloads to clear topics (for example
// shift_state and speed once the car sleeps). These synthetic round trips pin
// that every MQTT-driven monitor treats value -> empty -> same value as a
// baseline gap, never as a fresh user-visible event.

import (
	"fmt"
	"testing"
	"time"
)

func TestParkingEmptyPayloadRoundTripInventsNoEvent(t *testing.T) {
	fields := map[string]string{
		"locked": "true", "driver_front_door_open": "false", "trunk_open": "false",
		"frunk_open": "false", "windows_open": "false", "plugged_in": "true",
		"charge_port_door_open": "false", "is_user_present": "false",
		"charging_state": "Charging", "geofence": "Home", "state": "asleep", "shift_state": "P",
	}
	for field, value := range fields {
		t.Run(field, func(t *testing.T) {
			m := testParkingMonitor(t)
			at := time.Now().UTC()
			m.observe(1, field, value, at)
			before := len(m.store.Events)
			m.observe(1, field, "", at.Add(time.Second))
			m.observe(1, field, value, at.Add(2*time.Second))
			if len(m.store.Events) != before {
				t.Fatalf("%s value->empty->value invented %+v", field, m.store.Events[before:])
			}
		})
	}
}

func TestChargingEmptyPayloadRoundTripKeepsSessionWithoutNewEvents(t *testing.T) {
	for _, field := range []string{"charging_state", "state", "battery_level", "charger_power", "charger_voltage", "charger_actual_current", "time_to_full_charge", "fast_charger_present", "plugged_in"} {
		t.Run(field, func(t *testing.T) {
			m := testChargingBaselineMonitor(t)
			m.resetMQTTBaseline()
			at := time.Now().UTC()
			live := map[string]string{"state": "charging", "charging_state": "Charging", "battery_level": "30",
				"rated_battery_range_km": "180", "charger_power": "11", "charger_voltage": "230",
				"charger_actual_current": "16", "time_to_full_charge": "2.5", "fast_charger_present": "false", "plugged_in": "true"}
			for _, pair := range [][2]string{{"state", "online"}, {"charging_state", "Disconnected"}, {"battery_level", "30"}, {"rated_battery_range_km", "180"}} {
				m.observeMessage(1, pair[0], pair[1], at, false, true)
			}
			m.observeMessage(1, "state", "charging", at.Add(time.Second), false, true)
			m.observeMessage(1, "charging_state", "Charging", at.Add(2*time.Second), false, true)
			for _, extra := range []string{"charger_power", "charger_voltage", "charger_actual_current", "time_to_full_charge", "fast_charger_present", "plugged_in"} {
				m.observeMessage(1, extra, live[extra], at.Add(3*time.Second), false, true)
			}
			if len(m.queue) == 0 || (<-m.queue).Type != "charging_started" {
				t.Fatal("fixture did not start a live charging session")
			}
			for len(m.queue) > 0 {
				<-m.queue
			}
			session := m.store.Cars[1].SessionStartedAt
			m.observeMessage(1, field, "", at.Add(4*time.Second), false, true)
			m.observeMessage(1, field, live[field], at.Add(5*time.Second), false, true)
			for len(m.queue) > 0 {
				if event := <-m.queue; event.Type != "charging_update" && event.Type != "charging_progress" {
					t.Fatalf("%s empty round trip emitted %s", field, event.Type)
				}
			}
			if got := m.store.Cars[1].SessionStartedAt; got != session {
				t.Fatalf("%s empty round trip restarted session %q -> %q", field, session, got)
			}
		})
	}
}

func TestNavigationEmptyPayloadRoundTripDoesNotRestartTrip(t *testing.T) {
	route := `{"destination":"Example","miles_to_arrival":5,"minutes_to_arrival":10}`
	// active_route is deliberately excluded: an empty route payload is the same
	// "no route" signal as TeslaMate's {"error":...} JSON (see the test below).
	// shift_state and speed are not navigation inputs and must stay ignored.
	for _, field := range []string{"state", "geofence", "shift_state", "speed"} {
		t.Run(field, func(t *testing.T) {
			m := testNavigationMonitor(t)
			t.Cleanup(m.resetMQTTBaseline)
			m.resetMQTTBaseline()
			at := time.Now().UTC()
			m.observeMessage(1, "geofence", "Home", at, true, true)
			m.observeMessage(1, "state", "online", at, true, true)
			m.observeMessage(1, "active_route", "null", at, true, true)
			m.observeMessage(1, "state", "driving", at.Add(time.Second), false, true)
			m.observeMessage(1, "shift_state", "D", at.Add(time.Second), false, true)
			m.observeMessage(1, "speed", "60", at.Add(time.Second), false, true)
			m.observeMessage(1, "active_route", route, at.Add(2*time.Second), false, true)
			if len(m.queue) != 1 {
				t.Fatal("fixture did not start navigation")
			}
			started := <-m.queue
			value := map[string]string{"active_route": route, "state": "driving", "geofence": "", "shift_state": "D", "speed": "60"}[field]
			m.observeMessage(1, field, "", at.Add(3*time.Second), false, true)
			m.observeMessage(1, field, value, at.Add(4*time.Second), false, true)
			for len(m.queue) > 0 {
				event := <-m.queue
				if event.TripStartedAt != nil && started.TripStartedAt != nil && *event.TripStartedAt != *started.TripStartedAt {
					t.Fatalf("%s empty round trip restarted the trip: %+v", field, event)
				}
			}
			for len(m.priorityQueue) > 0 {
				t.Fatalf("%s empty round trip emitted priority event %+v", field, <-m.priorityQueue)
			}
		})
	}
}

func TestNavigationEmptyActiveRouteMatchesExplicitNoRoute(t *testing.T) {
	route := `{"destination":"Example","miles_to_arrival":5,"minutes_to_arrival":10}`
	types := map[string][]string{}
	for _, cleared := range []string{"", `{"error":"No active route available"}`} {
		m := testNavigationMonitor(t)
		m.resetMQTTBaseline()
		at := time.Now().UTC()
		m.observeMessage(1, "geofence", "Home", at, true, true)
		m.observeMessage(1, "state", "online", at, true, true)
		m.observeMessage(1, "active_route", "null", at, true, true)
		m.observeMessage(1, "state", "driving", at.Add(time.Second), false, true)
		m.observeMessage(1, "active_route", route, at.Add(2*time.Second), false, true)
		<-m.queue
		m.observeMessage(1, "active_route", cleared, at.Add(3*time.Second), false, true)
		for len(m.queue) > 0 {
			types[cleared] = append(types[cleared], string((<-m.queue).Type))
		}
		m.resetMQTTBaseline()
	}
	if fmt.Sprint(types[""]) != fmt.Sprint(types[`{"error":"No active route available"}`]) {
		t.Fatalf("empty active_route diverged from explicit no-route: %v", types)
	}
}

func TestLowBatteryEmptyPayloadRoundTripDoesNotRealert(t *testing.T) {
	for _, field := range []string{"battery_level", "shift_state", "state", "charging_state"} {
		t.Run(field, func(t *testing.T) {
			monitor, captured := newLowBatteryTestMonitor(t, 1)
			now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
			step := func(f, v string, at time.Time) {
				monitor.observeMessage(1, f, v, at, false)
				monitor.mu.Lock()
				for id, timer := range monitor.timers {
					timer.Stop()
					delete(monitor.timers, id)
				}
				monitor.mu.Unlock()
				monitor.evaluate(1, at)
			}
			values := map[string]string{"state": "asleep", "shift_state": "P", "charging_state": "Disconnected", "battery_level": "50"}
			for _, f := range []string{"state", "shift_state", "charging_state", "battery_level"} {
				step(f, values[f], now)
			}
			step("battery_level", "19", now.Add(time.Minute))
			if len(*captured) != 1 {
				t.Fatalf("fixture expected one alert, got %+v", *captured)
			}
			values["battery_level"] = "19"
			step(field, "", now.Add(2*time.Minute))
			step(field, values[field], now.Add(3*time.Minute))
			if len(*captured) != 1 {
				t.Fatalf("%s empty round trip re-alerted: %+v", field, *captured)
			}
		})
	}
}

func TestSoftwareEmptyPayloadRoundTripIsSilent(t *testing.T) {
	for _, field := range []string{"version", "update_available", "update_version", "software_update"} {
		t.Run(field, func(t *testing.T) {
			m, events := softwareTestMonitor(t)
			at := time.Now().UTC()
			aggregate := `{"installed_version":"2026.1","latest_version":"2026.2"}`
			m.observeMessage(5, "software_update", aggregate, at, true)
			m.observeMessage(5, "version", "2026.1", at, true)
			m.observeMessage(5, "update_available", "true", at, true)
			m.observeMessage(5, "update_version", "2026.2", at, true)
			expectNoSoftwareEvent(t, m, events)
			value := map[string]string{"version": "2026.1", "update_available": "true", "update_version": "2026.2", "software_update": aggregate}[field]
			m.observeMessage(5, field, "", at.Add(time.Second), false)
			m.observeMessage(5, field, value, at.Add(2*time.Second), false)
			expectNoSoftwareEvent(t, m, events)
		})
	}
}

func TestLockSecureEmptyPayloadRoundTripArmsNoLockEpisode(t *testing.T) {
	for _, pair := range [][2]string{{"locked", "true"}, {"is_user_present", "false"}, {"doors_open", "false"}, {"trunk_open", "false"}, {"frunk_open", "false"}, {"state", "online"}, {"shift_state", "P"}} {
		t.Run(pair[0], func(t *testing.T) {
			monitor := newLockSecureShiftTestMonitor(t)
			at := time.Date(2026, 10, 24, 20, 0, 0, 0, time.UTC)
			observeLockSecureClosedCar(monitor, at)
			monitor.observeMessage(1, "shift_state", "P", at, true)
			monitor.observeMessage(1, pair[0], "", at.Add(time.Minute), false)
			monitor.observeMessage(1, pair[0], pair[1], at.Add(2*time.Minute), false)
			monitor.mu.Lock()
			defer monitor.mu.Unlock()
			if !monitor.evidence[1].LockAt.IsZero() || monitor.pending[1] != nil {
				t.Fatalf("%s empty round trip armed a lock-secure episode: %+v", pair[0], monitor.evidence[1])
			}
			if !monitor.store.Cars[1].LastSecure {
				t.Fatalf("%s round trip lost the secure state: %+v", pair[0], monitor.store.Cars[1])
			}
		})
	}
}
