package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func softwareTestMonitor(t *testing.T) (*softwareNotificationMonitor, chan softwareNotificationEvent) {
	t.Helper()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(t.TempDir(), "software.json"))
	registry := newPushSubscriberRegistry()
	id, secret, relay := testInstallation()
	on := true
	if _, err := registry.upsert(pushPairRequest{InstallationID: id, RelayURL: relay, RelaySecret: secret, SoftwareUpdate: &on}); err != nil {
		t.Fatal(err)
	}
	events := make(chan softwareNotificationEvent, 16)
	registry.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var event softwareNotificationEvent
		if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
			return nil, err
		}
		events <- event
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}
	previous := pushRegistry
	pushRegistry = registry
	monitor := &softwareNotificationMonitor{
		statePath: filepath.Join(t.TempDir(), "state.json"), inFlight: map[string]bool{},
		store: softwareNotificationStore{Cars: map[int]carSoftwareState{}, Delivered: map[string]string{}},
	}
	t.Cleanup(func() {
		waitSoftwareDeliveries(t, monitor)
		pushRegistry = previous
	})
	return monitor, events
}

func waitSoftwareDeliveries(t *testing.T, monitor *softwareNotificationMonitor) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		monitor.mu.Lock()
		done := len(monitor.inFlight) == 0
		monitor.mu.Unlock()
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("fake software delivery did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func expectSoftwareEvent(t *testing.T, monitor *softwareNotificationMonitor, events <-chan softwareNotificationEvent, kind, version string) {
	t.Helper()
	select {
	case event := <-events:
		if event.Type != kind || kind == "update_available" && event.UpdateVersion != version || kind == "update_installed" && event.CurrentVersion != version {
			t.Fatalf("unexpected software event: %+v want %s/%s", event, kind, version)
		}
	case <-time.After(time.Second):
		t.Fatalf("missing software event %s/%s", kind, version)
	}
	waitSoftwareDeliveries(t, monitor)
}

func expectNoSoftwareEvent(t *testing.T, monitor *softwareNotificationMonitor, events <-chan softwareNotificationEvent) {
	t.Helper()
	waitSoftwareDeliveries(t, monitor)
	select {
	case event := <-events:
		t.Fatalf("unexpected software notification: %+v", event)
	default:
	}
}

func TestSoftwareAggregatePreventsMixedVersionsAndRetainedEvents(t *testing.T) {
	m, events := softwareTestMonitor(t)
	at := time.Now().UTC()
	m.store.Cars[73] = carSoftwareState{Version: "2025.1", UpdateVersion: "2026.1"}
	m.observeMessage(73, "software_update", `{"installed_version":"2026.1","latest_version":"2026.1"}`, at, true)
	m.observeMessage(73, "update_available", "true", at, false)
	m.observeMessage(73, "update_version", "2026.2", at, false)
	expectNoSoftwareEvent(t, m, events)
	m.observeMessage(73, "software_update", `{"installed_version":"2026.1","latest_version":"2026.2"}`, at, false)
	expectSoftwareEvent(t, m, events, "update_available", "2026.2")
	m.observeMessage(73, "software_update", `{"installed_version":"2026.1","latest_version":"2026.2"}`, at, false)
	expectNoSoftwareEvent(t, m, events)
	m.observeMessage(73, "software_update", `{"installed_version":"2026.2","latest_version":"2026.2"}`, at, true)
	expectNoSoftwareEvent(t, m, events)
	if m.carState(73).UpdateAvailable {
		t.Fatal("equal aggregate versions must not advertise an update")
	}
	m.observeMessage(73, "software_update", `{"installed_version":"2026.3","latest_version":"2026.3"}`, at, false)
	expectSoftwareEvent(t, m, events, "update_installed", "2026.3")
	m.mu.Lock()
	m.resetObservationBaselineLocked()
	m.mu.Unlock()
	m.observeMessage(73, "software_update", `{"installed_version":"2026.4","latest_version":"2026.4"}`, at, true)
	expectNoSoftwareEvent(t, m, events)
}

func TestSoftwareAggregateUnknownRequiresNewBaseline(t *testing.T) {
	for _, invalid := range []string{`null`, `{}`, `{"installed_version":"2026.1","latest_version":null}`, `{"latest_version":"2026.2"}`, `not-json`} {
		t.Run(invalid, func(t *testing.T) {
			m, events := softwareTestMonitor(t)
			at := time.Now().UTC()
			m.observeMessage(2, "software_update", `{"installed_version":"2026.1","latest_version":"2026.1"}`, at, true)
			m.observeMessage(2, "software_update", invalid, at, false)
			m.observeMessage(2, "version", "2026.2", at, false)
			m.observeMessage(2, "software_update", `{"installed_version":"2026.2","latest_version":"2026.3"}`, at, false)
			expectNoSoftwareEvent(t, m, events)
			m.observeMessage(2, "software_update", `{"installed_version":"2026.2","latest_version":"2026.4"}`, at, false)
			expectSoftwareEvent(t, m, events, "update_available", "2026.4")
		})
	}
}

func TestSoftwareLegacyLiveVersionCorroboratesAvailability(t *testing.T) {
	for _, versionFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "availability-first", true: "version-first"}[versionFirst], func(t *testing.T) {
			m, events := softwareTestMonitor(t)
			at := time.Now().UTC()
			m.observeMessage(2, "version", "2026.1", at, true)
			m.observeMessage(2, "update_version", "2026.1", at, true)
			m.observeMessage(2, "update_available", "false", at, true)
			if versionFirst {
				m.observe(2, "update_version", "2026.2", at)
				m.observe(2, "update_available", "true", at)
				// Separate topics do not prove that a preceding version belongs
				// to this availability transition. A later live receipt can.
				expectNoSoftwareEvent(t, m, events)
				m.observe(2, "update_version", "2026.2", at)
			} else {
				m.observe(2, "update_available", "true", at)
				expectNoSoftwareEvent(t, m, events)
				m.observe(2, "update_version", "2026.2", at)
			}
			expectSoftwareEvent(t, m, events, "update_available", "2026.2")
			m.observe(2, "version", "2026.3", at)
			expectSoftwareEvent(t, m, events, "update_installed", "2026.3")
			m.observe(2, "version", "null", at)
			m.observe(2, "version", "2026.4", at)
			expectNoSoftwareEvent(t, m, events)
		})
	}
}

func TestSoftwareVersionIdentityAndRememberedLatest(t *testing.T) {
	m, events := softwareTestMonitor(t)
	at := time.Now().UTC()
	m.observeMessage(2, "software_update", `{"installed_version":"2026.8.2 abc123","latest_version":"2026.8.2"}`, at, true)
	if state := m.carState(2); state.UpdateAvailable || state.UpdateAvailabilityStatus != "not_available" {
		t.Fatalf("build hash created false available update: %+v", state)
	}
	m.observe(2, "software_update", `{"installed_version":"2026.8.2 def456","latest_version":"2026.8.2"}`, at)
	expectNoSoftwareEvent(t, m, events)
	m.observe(2, "software_update", `{"installed_version":"2026.9.1 abc789","latest_version":"2026.8.2"}`, at)
	expectSoftwareEvent(t, m, events, "update_installed", "2026.9.1 abc789")
	if state := m.carState(2); state.UpdateAvailable || state.UpdateAvailabilityStatus != "not_available" {
		t.Fatalf("remembered older latest created false available update: %+v", state)
	}
	m.observe(2, "software_update", `{"installed_version":"2026.9.1 abc789","latest_version":"preview-next"}`, at)
	expectNoSoftwareEvent(t, m, events)
	if state := m.carState(2); state.UpdateAvailable || state.UpdateAvailabilityStatus != "unconfirmed" {
		t.Fatalf("unknown version format has invented ordering: %+v", state)
	}
}

func TestSoftwareLegacyCannotReuseVersionBeforeAvailability(t *testing.T) {
	m, events := softwareTestMonitor(t)
	at := time.Now().UTC()
	m.observeMessage(2, "version", "2026.1", at, true)
	m.observeMessage(2, "update_available", "false", at, true)
	m.observe(2, "update_version", "2026.2", at)
	m.observe(2, "update_available", "true", at)
	expectNoSoftwareEvent(t, m, events)
	m.observe(2, "update_version", "2026.3", at)
	expectSoftwareEvent(t, m, events, "update_available", "2026.3")
}

func TestSoftwareDuplicateCannotReplayAnInstallation(t *testing.T) {
	t.Setenv("MQTT_NAMESPACE", "")
	m, events := softwareTestMonitor(t)
	m.handleMQTTMessage(nil, friendMQTTTestMessage{topic: "teslamate/cars/2/software_update", payload: []byte(`{"installed_version":"2026.1","latest_version":"2026.1"}`), retained: true})
	m.handleMQTTMessage(nil, friendMQTTTestMessage{topic: "teslamate/cars/2/software_update", payload: []byte(`{"installed_version":"2026.2","latest_version":"2026.2"}`)})
	expectSoftwareEvent(t, m, events, "update_installed", "2026.2")
	m.handleMQTTMessage(nil, friendMQTTTestMessage{topic: "teslamate/cars/2/software_update", payload: []byte(`{"installed_version":"2026.1","latest_version":"2026.1"}`), duplicate: true})
	expectNoSoftwareEvent(t, m, events)
	if state := m.carState(2); state.Version != "2026.2" {
		t.Fatalf("duplicate rolled back current software state: %+v", state)
	}
}

func TestSoftwareFirstLiveAggregateIsBaselineOnly(t *testing.T) {
	m, events := softwareTestMonitor(t)
	m.store.Cars[2] = carSoftwareState{Version: "2025.1"}
	m.observe(2, "software_update", `{"installed_version":"2026.1","latest_version":"2026.2"}`, time.Now().UTC())
	expectNoSoftwareEvent(t, m, events)
}

func TestSoftwareFleetReconciliationKeepsOtherCarsAndDedupe(t *testing.T) {
	m, events := softwareTestMonitor(t)
	at := time.Now().UTC()
	for _, carID := range []int{2, 3} {
		m.observeMessage(carID, "software_update", `{"installed_version":"2026.1","latest_version":"2026.1"}`, at, true)
	}
	m.store.Delivered["2:update_available:2025.1"] = at.Format(time.RFC3339)
	oldGeneration, oldEpoch := m.generation, m.observations[2].Epoch
	m.reconcileFleet(map[int]bool{2: false, 3: true})
	if _, exists := m.store.Cars[2]; exists {
		t.Fatal("disabled car still has current software state")
	}
	if _, exists := m.observations[2]; exists {
		t.Fatal("disabled car still has connection-local evidence")
	}
	if m.store.Delivered["2:update_available:2025.1"] == "" {
		t.Fatal("fleet reconciliation erased dedupe history")
	}
	// Re-enabling the same ID requires a new baseline, even without reconnecting.
	m.reconcileFleet(map[int]bool{2: true, 3: true})
	m.observe(2, "software_update", `{"installed_version":"2026.2","latest_version":"2026.2"}`, at)
	expectNoSoftwareEvent(t, m, events)
	// Work captured before removal must not dispatch against the new incarnation.
	m.fanOutSoftware("old-in-flight", 2, carSoftwareState{Version: "2026.1", UpdateVersion: "2025.1"}, "update_available", "2025.1", at, oldGeneration, oldEpoch)
	expectNoSoftwareEvent(t, m, events)
	// The unaffected car's valid baseline remains usable.
	m.observe(3, "software_update", `{"installed_version":"2026.1","latest_version":"2026.3"}`, at)
	expectSoftwareEvent(t, m, events, "update_available", "2026.3")
}

func TestSoftwareReconnectDoesNotExposePersistedAvailabilityAsCurrent(t *testing.T) {
	m, events := softwareTestMonitor(t)
	at := time.Now().UTC()
	m.observeMessage(2, "software_update", `{"installed_version":"2026.1","latest_version":"2026.2"}`, at, true)
	if got := m.carState(2); got.UpdateAvailabilityStatus != "available" || !got.UpdateAvailable {
		t.Fatal("current retained baseline lost reported availability")
	}
	m.mu.Lock()
	m.resetObservationBaselineLocked()
	m.mu.Unlock()
	if got := m.carState(2); got.UpdateAvailabilityStatus != "unconfirmed" || got.UpdateAvailable {
		t.Fatal("persisted availability masqueraded as current after disconnect")
	}
	if stored := m.store.Cars[2]; stored.UpdateAvailabilityStatus != "available" || !stored.UpdateAvailable {
		t.Fatal("diagnostic invalidation erased last-known history")
	}
	expectNoSoftwareEvent(t, m, events)
}

func TestSoftwareUnknownInstalledLabelRequiresNewRecognizedBaseline(t *testing.T) {
	for _, aggregate := range []bool{false, true} {
		t.Run(map[bool]string{false: "scalar", true: "aggregate"}[aggregate], func(t *testing.T) {
			m, events := softwareTestMonitor(t)
			at := time.Now().UTC()
			observe := func(version string) {
				if aggregate {
					m.observe(2, "software_update", `{"installed_version":"`+version+`","latest_version":"`+version+`"}`, at)
				} else {
					m.observe(2, "version", version, at)
				}
			}
			observe("2026.1")
			observe("not-a-version")
			observe("2026.2")
			expectNoSoftwareEvent(t, m, events)
			observe("2026.3")
			expectSoftwareEvent(t, m, events, "update_installed", "2026.3")
		})
	}
}
