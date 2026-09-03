package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func testParkingMonitor(t *testing.T) *parkingEventMonitor {
	t.Helper()
	return &parkingEventMonitor{
		statePath:     filepath.Join(t.TempDir(), "parking.json"),
		maximumEvents: 1000,
		store: parkingEventStore{
			Cars:   map[int]parkingEventCarState{},
			Events: []parkingObservedEvent{},
		},
	}
}

func TestDisconnectedToStoppedIsNotChargeStop(t *testing.T) {
	m := testParkingMonitor(t)
	now := time.Now().UTC()
	m.observe(1, "charging_state", "Disconnected", now)
	m.observe(1, "charging_state", "Stopped", now.Add(time.Second))
	if len(m.store.Events) != 0 {
		t.Fatalf("plugged/waiting invented a stop: %+v", m.store.Events)
	}
}

func TestAbortedStartingDoesNotFabricateChargeInterval(t *testing.T) {
	m := testParkingMonitor(t)
	now := time.Now().UTC()
	m.observe(1, "charging_state", "Disconnected", now)
	m.observe(1, "charging_state", "Starting", now.Add(time.Second))
	m.observe(1, "charging_state", "Stopped", now.Add(2*time.Second))
	if len(m.store.Events) != 0 {
		t.Fatal("aborted negotiation was not an actual charging interval")
	}
}

func TestChargingToStoppedIsChargeStop(t *testing.T) {
	m := testParkingMonitor(t)
	now := time.Now().UTC()
	m.observe(1, "charging_state", "Disconnected", now)
	m.observe(1, "charging_state", "Charging", now.Add(time.Second))
	m.observe(1, "charging_state", "Stopped", now.Add(2*time.Second))
	if len(m.store.Events) != 2 {
		t.Fatalf("events=%+v", m.store.Events)
	}
	if m.store.Events[0].Type != "charging_started" || m.store.Events[1].Type != "charging_stopped" {
		t.Fatalf("unexpected types: %+v", m.store.Events)
	}
}

func TestCompleteToStoppedIsNotChargeStop(t *testing.T) {
	m := testParkingMonitor(t)
	now := time.Now().UTC()
	m.observe(1, "charging_state", "Disconnected", now)
	m.observe(1, "charging_state", "Charging", now.Add(time.Second))
	m.observe(1, "charging_state", "Complete", now.Add(2*time.Second))
	m.observe(1, "charging_state", "Stopped", now.Add(3*time.Second))
	got := make([]string, 0, len(m.store.Events))
	for _, event := range m.store.Events {
		got = append(got, event.Type)
	}
	if len(got) != 2 || got[0] != "charging_started" || got[1] != "charging_completed" {
		t.Fatalf("complete→stopped changed chronology: %v", got)
	}
}

func TestNoPowerToStoppedIsNotChargeStop(t *testing.T) {
	m := testParkingMonitor(t)
	now := time.Now().UTC()
	m.observe(1, "charging_state", "Disconnected", now)
	m.observe(1, "charging_state", "NoPower", now.Add(time.Second))
	m.observe(1, "charging_state", "Stopped", now.Add(2*time.Second))
	if len(m.store.Events) != 1 || m.store.Events[0].Type != "charging_no_power" {
		t.Fatalf("events=%+v", m.store.Events)
	}
}

func TestChargingRestartAfterStop(t *testing.T) {
	m := testParkingMonitor(t)
	now := time.Now().UTC()
	m.observe(1, "charging_state", "Disconnected", now)
	m.observe(1, "charging_state", "Charging", now.Add(time.Second))
	m.observe(1, "charging_state", "Stopped", now.Add(2*time.Second))
	m.observe(1, "charging_state", "Charging", now.Add(3*time.Second))
	if len(m.store.Events) != 3 {
		t.Fatalf("events=%+v", m.store.Events)
	}
	got := []string{m.store.Events[0].Type, m.store.Events[1].Type, m.store.Events[2].Type}
	want := []string{"charging_started", "charging_stopped", "charging_started"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("restart chronology=%v want=%v", got, want)
		}
	}
}

func TestAllFourDoorTransitionsAreStored(t *testing.T) {
	for _, field := range namedDoorMQTTFields {
		m := testParkingMonitor(t)
		now := time.Now().UTC()
		m.observe(1, field, "false", now)
		m.observe(1, field, "true", now.Add(time.Second))
		m.observe(1, field, "false", now.Add(2*time.Second))
		if len(m.store.Events) != 2 {
			t.Fatalf("%s events=%d", field, len(m.store.Events))
		}
		diag := m.doorDiagnostics(1)
		found := false
		for _, rec := range diag {
			if rec.Field != field {
				continue
			}
			found = true
			if rec.ReceivedCount != 3 || rec.StoredEventCount != 2 || rec.BaselineOnly {
				t.Fatalf("%s diagnostics=%+v", field, rec)
			}
		}
		if !found {
			t.Fatalf("missing diagnostics for %s", field)
		}
	}
}

func TestAggregateDoorsOpenDoesNotFabricateNamedDoorHistory(t *testing.T) {
	m := testParkingMonitor(t)
	now := time.Now().UTC()
	m.observe(1, "doors_open", "false", now)
	m.observe(1, "doors_open", "true", now.Add(time.Second))
	m.observe(1, "locked", "true", now.Add(2*time.Second))
	m.observe(1, "locked", "false", now.Add(3*time.Second))
	for _, event := range m.store.Events {
		if event.Field == "driver_front_door_open" || event.Type == "driver_front_door_opened" {
			t.Fatalf("fabricated named door history: %+v", event)
		}
	}
	diag := m.doorDiagnostics(1)
	if len(diag) != 4 {
		t.Fatalf("expected four door diagnostic slots, got %d", len(diag))
	}
	for _, rec := range diag {
		if rec.StoredEventCount != 0 || rec.ReceivedCount != 0 || rec.LastValue != "" {
			t.Fatalf("missing named-door history was invented: %+v", rec)
		}
	}
}

func TestDoorDiagnosticsReportBaselineWithoutStoring(t *testing.T) {
	m := testParkingMonitor(t)
	now := time.Now().UTC()
	m.observe(1, "driver_front_door_open", "false", now)
	if len(m.store.Events) != 0 {
		t.Fatal("retained baseline stored a transition")
	}
	diag := m.doorDiagnostics(1)
	var front parkingDoorReceipt
	for _, rec := range diag {
		if rec.Field == "driver_front_door_open" {
			front = rec
		}
	}
	if !front.BaselineOnly || front.ReceivedCount != 1 || front.StoredEventCount != 0 {
		t.Fatalf("baseline diagnostics=%+v", front)
	}
}

func TestCompanionStatusIncludesDoorDiagnostics(t *testing.T) {
	oldToken, oldProbe, oldParkingEvents := apiToken, authProbeURL, parkingEvents
	apiToken, authProbeURL = "test-token", ""
	m := testParkingMonitor(t)
	now := time.Date(2026, 9, 2, 16, 0, 0, 0, time.UTC)
	m.observe(2, "driver_front_door_open", "false", now)
	m.observe(2, "driver_front_door_open", "true", now.Add(time.Second))
	parkingEvents = m
	t.Cleanup(func() {
		apiToken, authProbeURL, parkingEvents = oldToken, oldProbe, oldParkingEvents
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/cars/2/companion-status", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	recorder := httptest.NewRecorder()
	handleCompanionStatus(recorder, request, "2")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d", recorder.Code)
	}
	var payload struct {
		Data struct {
			DoorMQTTDiagnostics []parkingDoorReceipt `json:"door_mqtt_diagnostics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Data.DoorMQTTDiagnostics) != 4 {
		t.Fatalf("diagnostics=%+v", payload.Data.DoorMQTTDiagnostics)
	}
}
