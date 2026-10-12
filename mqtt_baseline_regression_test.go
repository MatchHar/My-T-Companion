package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func baselineIntPointer(n int) *int { return &n }

func testChargingBaselineMonitor(t *testing.T) *chargingNotificationMonitor {
	t.Helper()
	m := &chargingNotificationMonitor{
		statePath:      filepath.Join(t.TempDir(), "charge.json"),
		installationID: "test-installation",
		pending:        map[int]*time.Timer{},
		queue:          make(chan chargingLiveActivityEvent, 64),
		store:          chargingNotificationStore{Cars: map[int]carChargingState{}, Delivered: map[string]string{}},
	}
	t.Cleanup(m.resetMQTTBaseline)
	return m
}

func TestParkingReconnectRetainedUpdatesBaselineWithoutInventingAction(t *testing.T) {
	m := testParkingMonitor(t)
	at := time.Now().UTC()
	m.observe(1, "driver_front_door_open", "false", at)
	reloaded := testParkingMonitor(t)
	reloaded.statePath = m.statePath
	reloaded.load()
	reloaded.resetMQTTBaseline()
	reloaded.observeMessage(1, "driver_front_door_open", "true", at.Add(time.Hour), true)
	if len(reloaded.store.Events) != 0 || reloaded.currentValues(1)["driver_front_door_open"] != "true" {
		t.Fatal("retained replay should update current baseline silently")
	}
	reloaded.observeMessage(1, "driver_front_door_open", "false", at.Add(time.Hour+time.Second), false)
	if len(reloaded.store.Events) != 1 || reloaded.store.Events[0].Type != "driver_front_door_closed" {
		t.Fatal("genuine change after baseline was lost")
	}
	reloaded.resetMQTTBaseline()
	reloaded.observeMessage(1, "driver_front_door_open", "true", at.Add(2*time.Hour), false)
	if len(reloaded.store.Events) != 1 {
		t.Fatal("first live receipt after connection gap is a baseline too")
	}
}

func TestParkingNewCarHandlerAndDuplicateIsolation(t *testing.T) {
	t.Setenv("MQTT_NAMESPACE", "")
	m := testParkingMonitor(t)
	message := friendMQTTTestMessage{retained: true, topic: "teslamate/cars/73/locked", payload: []byte("false")}
	m.handleMQTTMessage(nil, message)
	message.retained = false
	message.payload = []byte("true")
	m.handleMQTTMessage(nil, message)
	message.duplicate = true
	message.payload = []byte("false")
	m.handleMQTTMessage(nil, message)
	if len(m.store.Events) != 1 || m.store.Events[0].CarID != 73 || m.currentValues(73)["locked"] != "true" {
		t.Fatal("new car required restart or QoS duplicate rewrote its current state")
	}
}

func TestParkingUnknownGapRebaselinesBothBooleanDirections(t *testing.T) {
	for _, unknown := range []string{"null", "unknown", "nil", "", "unexpected"} {
		for _, known := range []string{"true", "false"} {
			t.Run(fmt.Sprintf("%s/%s", unknown, known), func(t *testing.T) {
				m := testParkingMonitor(t)
				at := time.Now().UTC()
				m.observe(1, "locked", known, at)
				m.observe(1, "locked", unknown, at.Add(time.Second))
				m.observe(1, "locked", known, at.Add(2*time.Second))
				if len(m.store.Events) != 0 {
					t.Fatal("unknown-to-known invented an action")
				}
				other := "true"
				if known == "true" {
					other = "false"
				}
				m.observe(1, "locked", other, at.Add(3*time.Second))
				if len(m.store.Events) != 1 {
					t.Fatal("valid later transition was lost")
				}
			})
		}
	}
}

func TestParkingCapacityIsExactAndCurrentCachesAreBounded(t *testing.T) {
	m := testParkingMonitor(t)
	at := time.Now().UTC()
	for i := 0; i < m.maximumEvents; i++ {
		m.store.Events = append(m.store.Events, parkingObservedEvent{CarID: 99, ObservedAt: at.Format(time.RFC3339Nano)})
	}
	m.observe(1, "locked", "false", at)
	m.observe(1, "locked", "true", at.Add(time.Second))
	if len(m.store.Events) != m.maximumEvents {
		t.Fatal("capacity exceeded after append")
	}
	for id := 2; id < maximumParkingCurrentCars+40; id++ {
		m.observe(id, "driver_front_door_open", "false", at.Add(time.Duration(id)*time.Second))
	}
	if len(m.store.Cars) > maximumParkingCurrentCars || len(m.store.DoorReceipts) > maximumParkingCurrentCars*4 || len(m.baselines) > maximumParkingCurrentCars {
		t.Fatal("disposable per-car cache exceeded its bound")
	}
	if len(m.store.Events) != m.maximumEvents {
		t.Fatal("cache eviction removed history")
	}
	// Restored oversized state and null-only scalar receipts must obey the same
	// bound, including the auxiliary maps.
	m = testParkingMonitor(t)
	for id := 1; id < maximumParkingCurrentCars+40; id++ {
		m.store.Cars[id] = parkingEventCarState{}
	}
	m.observe(1, "battery_level", "null", at)
	if len(m.store.Cars) > maximumParkingCurrentCars {
		t.Fatal("restored oversized cache exceeded bound when incoming car was evicted")
	}
	for id := maximumParkingCurrentCars + 40; id < maximumParkingCurrentCars*3; id++ {
		m.observe(id, "battery_level", "null", at.Add(time.Duration(id)*time.Second))
	}
	if len(m.store.Cars) > maximumParkingCurrentCars || len(m.baselines) > maximumParkingCurrentCars || len(m.lastReceipt) > maximumParkingCurrentCars {
		t.Fatal("null-only scalar receipts escaped current-cache bound")
	}
}

func TestBoundedFloatRejectsNonfiniteAndParkingRemainsPersistable(t *testing.T) {
	for _, raw := range []string{"NaN", "+Inf", "-Inf", "Infinity", "1e999"} {
		if parseBoundedFloat(raw, 0, 1000) != nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	m := testParkingMonitor(t)
	m.observe(1, "rated_battery_range_km", "NaN", time.Now().UTC())
	if _, err := json.Marshal(m.store); err != nil {
		t.Fatal(err)
	}
}

func TestChargingRetainedStartupRequiresLiveEvidenceAndLabelsRecovery(t *testing.T) {
	m := testChargingBaselineMonitor(t)
	m.resetMQTTBaseline()
	at := time.Now().UTC()
	for _, pair := range [][2]string{{"state", "charging"}, {"charging_state", "Charging"}, {"battery_level", "30"}, {"rated_battery_range_km", "180"}, {"time_to_full_charge", "1"}, {"healthy", "true"}} {
		m.observeMessage(73, pair[0], pair[1], at, true, true)
	}
	if len(m.queue) != 0 || m.store.Cars[73].Active {
		t.Fatal("retained bootstrap became a new charge")
	}
	m.observeMessage(73, "healthy", "false", at.Add(time.Second), false, true)
	if len(m.queue) != 0 {
		t.Fatal("unhealthy bootstrap started a card")
	}
	m.observeMessage(73, "healthy", "true", at.Add(2*time.Second), false, true)
	if len(m.queue) != 1 {
		t.Fatal("unchanged scalar state never recovered with live upstream evidence")
	}
	event := <-m.queue
	if event.ObservationMode != "recovered_current_snapshot" || m.store.Cars[73].SessionStartedAt != "" || event.EstimatedCompleteAt != nil || event.RemainingSeconds != nil {
		t.Fatal("recovery invented a source start time")
	}
	m.observeMessage(73, "time_to_full_charge", "1", at.Add(3*time.Second), false, true)
	m.mu.Lock()
	state := m.store.Cars[73]
	live := m.makeEventLocked(73, &state, "charging_updated", at.Add(time.Minute))
	m.mu.Unlock()
	if live.EstimatedCompleteAt == nil || live.RemainingSeconds == nil || *live.RemainingSeconds != 3600 || *live.EstimatedCompleteAt != at.Add(3*time.Second+time.Hour).Unix() {
		t.Fatal("live duration did not anchor estimate to its own receipt time")
	}
}

func TestChargingReconnectPreservesSessionAndUnknownDoesNotEndIt(t *testing.T) {
	m := testChargingBaselineMonitor(t)
	at := time.Now().UTC().Add(-time.Hour)
	m.store.Cars[1] = carChargingState{Active: true, SessionID: "existing", SessionStartedAt: at.Format(time.RFC3339), StartDelivered: true, VehicleState: "charging", BatteryLevel: baselineIntPointer(30), RatedRangeKM: floatPointer(180)}
	m.resetMQTTBaseline()
	m.observeMessage(1, "state", "charging", at.Add(time.Hour), true, true)
	m.observeMessage(1, "charging_state", "Charging", at.Add(time.Hour), true, true)
	m.observeMessage(1, "battery_level", "31", at.Add(time.Hour), true, true)
	m.observeMessage(1, "rated_battery_range_km", "181", at.Add(time.Hour), true, true)
	if len(m.queue) != 0 {
		t.Fatal("retained reconnect emitted an update")
	}
	m.observeMessage(1, "healthy", "true", at.Add(time.Hour), false, true)
	event := <-m.queue
	if event.SessionID != "existing" || event.Type != "charging_updated" || event.ObservationMode != "recovered_current_snapshot" || m.store.Cars[1].SessionStartedAt != at.Format(time.RFC3339) {
		t.Fatal("existing charge was restarted or its timestamp replaced")
	}
	m.observeMessage(1, "state", "null", at.Add(time.Hour+time.Second), false, true)
	if len(m.queue) != 0 || !m.store.Cars[1].Active {
		t.Fatal("unknown ended an existing card")
	}
	m.resetMQTTBaseline()
	m.flushUpdate(1)
	if len(m.queue) != 0 {
		t.Fatal("old update timer published after disconnect")
	}
}

func TestChargingLiveTransitionAfterBaselineRemainsAvailable(t *testing.T) {
	m := testChargingBaselineMonitor(t)
	m.resetMQTTBaseline()
	at := time.Now().UTC()
	for _, pair := range [][2]string{{"state", "online"}, {"charging_state", "Disconnected"}, {"battery_level", "30"}, {"rated_battery_range_km", "180"}} {
		m.observeMessage(1, pair[0], pair[1], at, false, true)
	}
	m.observeMessage(1, "state", "charging", at.Add(time.Second), false, true)
	if len(m.queue) != 0 {
		t.Fatal("mixed charging fields started an incoherent snapshot")
	}
	m.observeMessage(1, "charging_state", "Charging", at.Add(2*time.Second), false, true)
	if len(m.queue) != 1 {
		t.Fatal("live charging transition was lost")
	}
	event := <-m.queue
	if event.Type != "charging_started" || event.ObservationMode != "mqtt_live_transition" || m.store.Cars[1].SessionStartedAt == "" {
		t.Fatal("live transition mislabeled as bootstrap")
	}
}

func TestChargingTerminalCleanupDoesNotRequireNumericalSnapshot(t *testing.T) {
	m := testChargingBaselineMonitor(t)
	m.store.Cars[1] = carChargingState{Active: true, SessionID: "existing", StartDelivered: true, VehicleState: "charging", ChargingState: "charging"}
	m.resetMQTTBaseline()
	at := time.Now().UTC()
	m.observeMessage(1, "battery_level", "null", at, true, true)
	m.observeMessage(1, "rated_battery_range_km", "null", at, true, true)
	m.observeMessage(1, "healthy", "false", at, false, true)
	m.observeMessage(1, "state", "online", at, false, true)
	if len(m.queue) != 0 {
		t.Fatal("partially known primary state ended the card")
	}
	m.observeMessage(1, "charging_state", "Disconnected", at.Add(time.Second), false, true)
	if len(m.queue) != 1 {
		t.Fatal("null numerical snapshot stranded existing card despite coherent live end")
	}
	if event := <-m.queue; event.Type != "charging_ended" || event.SessionID != "existing" || m.store.Cars[1].Active {
		t.Fatal("terminal cleanup replaced the existing session")
	}
}

func TestColdLiveBootstrapIsCurrentSnapshotNotObservedStart(t *testing.T) {
	at := time.Now().UTC()
	charge := testChargingBaselineMonitor(t)
	charge.resetMQTTBaseline()
	for _, pair := range [][2]string{{"state", "charging"}, {"charging_state", "Charging"}, {"battery_level", "30"}, {"rated_battery_range_km", "180"}} {
		charge.observeMessage(1, pair[0], pair[1], at, false, true)
	}
	if len(charge.queue) != 1 {
		t.Fatal("live current charge did not recover card")
	}
	if event := <-charge.queue; event.ObservationMode != "recovered_current_snapshot" || charge.store.Cars[1].SessionStartedAt != "" {
		t.Fatal("cold live charging snapshot invented a charging start")
	}
	nav := testNavigationMonitor(t)
	t.Cleanup(nav.resetMQTTBaseline)
	nav.resetMQTTBaseline()
	nav.observeMessage(1, "geofence", "Home", at, false, true)
	nav.observeMessage(1, "state", "driving", at, false, true)
	nav.observeMessage(1, "active_route", `{"destination":"Example","miles_to_arrival":5,"minutes_to_arrival":10}`, at, false, true)
	if len(nav.queue) != 1 {
		t.Fatal("live current navigation did not recover card")
	}
	if event := <-nav.queue; !event.liveActivityOnly || event.TripStartedAt != nil || event.ObservationMode != "recovered_current_snapshot" {
		t.Fatal("cold live route invented an ordinary trip-start alert")
	}
}

func TestNavigationRetainedStartupHasNoFreshRouteTimeOrOrdinaryStartAlert(t *testing.T) {
	m := testNavigationMonitor(t)
	t.Cleanup(m.resetMQTTBaseline)
	m.resetMQTTBaseline()
	at := time.Now().UTC()
	m.observeMessage(73, "state", "driving", at, true, true)
	m.observeMessage(73, "active_route", `{"destination":"Example","miles_to_arrival":5,"minutes_to_arrival":10}`, at, true, true)
	m.observeMessage(73, "healthy", "true", at, true, true)
	if len(m.queue) != 0 || m.store.Cars[73].LastObservedAt != "" {
		t.Fatal("retained route gained live time or start")
	}
	m.observeMessage(73, "healthy", "false", at.Add(time.Second), false, true)
	if len(m.queue) != 0 {
		t.Fatal("unhealthy retained route started card")
	}
	m.observeMessage(73, "healthy", "true", at.Add(2*time.Second), false, true)
	if len(m.queue) != 1 {
		t.Fatal("unchanged route failed to recover with live upstream evidence")
	}
	event := <-m.queue
	if !event.liveActivityOnly || event.ObservationMode != "recovered_current_snapshot" || event.TripStartedAt != nil || event.EstimatedArrivalAt != nil || event.RemainingMinutes != nil || m.store.Cars[73].LastObservedAt != "" || m.store.Cars[73].SessionStartedAt != "" {
		t.Fatal("recovery became a fresh trip or ordinary banner")
	}
	m.observeMessage(73, "active_route", `{"destination":"Example","miles_to_arrival":5,"minutes_to_arrival":10}`, at.Add(3*time.Second), false, true)
	m.mu.Lock()
	state := m.store.Cars[73]
	live := m.makeEventLocked(73, &state, "navigation_updated", at.Add(time.Minute))
	m.mu.Unlock()
	if live.EstimatedArrivalAt == nil || live.RemainingMinutes == nil || *live.RemainingMinutes != 10 || *live.EstimatedArrivalAt != at.Add(3*time.Second+10*time.Minute).Unix() {
		t.Fatal("live route did not anchor estimate to its own receipt time")
	}
}

func TestNavigationChangedRetainedDestinationRecoversOnlyCurrentCard(t *testing.T) {
	m := testNavigationMonitor(t)
	m.distanceReader = func(int) (int64, *float64, bool) { return 0, nil, false }
	m.startNameReader = func(int) string { return "" }
	t.Cleanup(m.resetMQTTBaseline)
	at := time.Now().UTC()
	oldStart := at.Add(-time.Hour).Format(time.RFC3339)
	m.store.Cars[1] = carNavigationState{Active: true, SessionID: "old-session", SessionStartedAt: oldStart, LastObservedAt: oldStart, StartDelivered: true, StartAlertDelivered: true, VehicleState: "driving", Destination: "Old destination", StartName: "Old origin"}
	m.history.Sessions = []navigationPushHistorySession{{SessionID: "old-session", CarID: 1, StartedAt: oldStart, Destination: "Old destination", LastEventType: "navigation_started"}}
	m.resetMQTTBaseline()
	m.observeMessage(1, "active_route", `{"destination":"New destination","miles_to_arrival":5,"minutes_to_arrival":10}`, at, true, true)
	m.observeMessage(1, "healthy", "true", at, true, true)
	m.observeMessage(1, "healthy", "false", at, false, true)
	m.observeMessage(1, "healthy", "true", at, false, true)
	if len(m.queue) != 0 {
		t.Fatal("missing current-connection state allowed changed-route recovery")
	}
	m.observeMessage(1, "state", "driving", at, true, true)
	if len(m.queue) != 0 {
		t.Fatal("retained primary receipt emitted recovery by itself")
	}
	m.observeMessage(1, "healthy", "false", at.Add(time.Second), false, true)
	if len(m.queue) != 0 {
		t.Fatal("unhealthy changed route recovered")
	}
	m.observeMessage(1, "healthy", "true", at.Add(2*time.Second), false, true)
	if len(m.queue) != 1 || len(m.priorityQueue) != 1 {
		t.Fatal("changed retained route remained blocked after coherent live liveness")
	}
	end, start := <-m.priorityQueue, <-m.queue
	if end.Type != "navigation_ended" || end.SessionID != "old-session" || !end.liveActivityOnly || end.EndReason != "stale" || end.TripEndedAt != nil || end.DurationMinutes != nil || end.ObservationMode != "recovered_session_cleanup" {
		t.Fatal("old exact session cleanup became a physical arrival/end event")
	}
	state := m.store.Cars[1]
	if start.Type != "navigation_started" || start.SessionID == "old-session" || !start.liveActivityOnly || start.Destination != "New destination" || start.TripStartedAt != nil || start.EstimatedArrivalAt != nil || start.RemainingMinutes != nil || start.ObservationMode != "recovered_current_snapshot" || state.SessionStartedAt != "" || state.LastObservedAt != "" || state.StartName != "" || len(m.pendingStartAlerts) != 0 {
		t.Fatal("new current card invented trip boundary/countdown/ordinary start alert")
	}
	for _, history := range m.history.Sessions {
		switch history.SessionID {
		case "old-session":
			if history.StartedAt != oldStart || history.EndedAt != "" || history.LastEventType != "navigation_ended" || history.EndReason != "stale" {
				t.Fatal("cleanup fabricated old physical history end")
			}
		case start.SessionID:
			if history.StartedAt != "" || history.FirstObservedAt != start.ObservedAt || history.ObservationMode != "recovered_current_snapshot" {
				t.Fatal("recovery fabricated history start time")
			}
		}
	}
	// An unrelated liveness packet cannot replace unknown physical start time.
	m.recordHistoryEvent(start, "queued")
	for _, history := range m.history.Sessions {
		if history.SessionID == start.SessionID && history.StartedAt != "" {
			t.Fatal("later delivery stamped an unknown history start")
		}
	}
}

func TestNavigationReconnectPreservesSessionAndCancelsStaleCallbacks(t *testing.T) {
	m := testNavigationMonitor(t)
	t.Cleanup(m.resetMQTTBaseline)
	at := time.Now().UTC().Add(-time.Hour)
	m.store.Cars[1] = carNavigationState{Active: true, SessionID: "existing", SessionStartedAt: at.Format(time.RFC3339), LastObservedAt: at.Format(time.RFC3339), StartDelivered: true, StartAlertDelivered: true, VehicleState: "driving", Destination: "Example"}
	m.resetMQTTBaseline()
	m.observeMessage(1, "state", "driving", at.Add(time.Hour), true, true)
	m.observeMessage(1, "active_route", `{"destination":"Example","miles_to_arrival":5,"minutes_to_arrival":10}`, at.Add(time.Hour), true, true)
	if len(m.queue) != 0 {
		t.Fatal("retained reconnect emitted an event")
	}
	m.observeMessage(1, "healthy", "true", at.Add(time.Hour), false, true)
	event := <-m.queue
	if event.SessionID != "existing" || event.Type != "navigation_updated" || m.store.Cars[1].SessionStartedAt != at.Format(time.RFC3339) || m.store.Cars[1].LastObservedAt != at.Format(time.RFC3339) {
		t.Fatal("retained reconnect rewrote session or source freshness")
	}
	m.resetMQTTBaseline()
	m.flushUpdate(1)
	m.flushStartAlert(1, "existing")
	if len(m.queue) != 0 {
		t.Fatal("stale callbacks published after disconnect")
	}
}

func TestNavigationLiveStartAfterBaselineStillSendsNormalStart(t *testing.T) {
	m := testNavigationMonitor(t)
	t.Cleanup(m.resetMQTTBaseline)
	m.resetMQTTBaseline()
	at := time.Now().UTC()
	m.observeMessage(1, "geofence", "Home", at, true, true)
	m.observeMessage(1, "state", "online", at, true, true)
	m.observeMessage(1, "active_route", "null", at, true, true)
	m.observeMessage(1, "state", "driving", at.Add(time.Second), false, true)
	m.observeMessage(1, "active_route", `{"destination":"Example","miles_to_arrival":5,"minutes_to_arrival":10}`, at.Add(2*time.Second), false, true)
	if len(m.queue) != 1 {
		t.Fatal("actual live state and route did not start navigation")
	}
	event := <-m.queue
	if event.ObservationMode != "mqtt_live_transition" || event.liveActivityOnly || event.TripStartedAt == nil {
		t.Fatal("actual live trip was mislabeled as recovery")
	}
}

func TestFleetReconcileKeepsHistoryAndOnlyCleansExistingCards(t *testing.T) {
	at := time.Now().UTC()
	parking := testParkingMonitor(t)
	parking.observe(1, "locked", "false", at)
	parking.observe(1, "locked", "true", at.Add(time.Second))
	parking.observe(1, "driver_front_door_open", "false", at)
	parking.reconcileFleet(map[int]bool{2: true})
	if len(parking.store.Cars) != 0 || len(parking.store.DoorReceipts) != 0 || len(parking.store.Events) != 1 {
		t.Fatal("membership pruning erased history or kept stale current values")
	}
	charge := testChargingBaselineMonitor(t)
	charge.store.Cars[1] = carChargingState{Active: true, SessionID: "charge-existing", StartDelivered: true}
	charge.reconcileFleet(map[int]bool{})
	if len(charge.store.Cars) != 0 || len(charge.queue) != 1 {
		t.Fatal("charge cache or cleanup missing")
	}
	if event := <-charge.queue; event.Type != "charging_ended" || event.ObservationMode != "source_membership_cleanup" {
		t.Fatal("charge removal was not explicit cleanup")
	}
	nav := testNavigationMonitor(t)
	nav.store.Cars[1] = carNavigationState{Active: true, SessionID: "nav-existing", Destination: "Example", StartDelivered: true}
	nav.history.Sessions = []navigationPushHistorySession{{SessionID: "historical-unrelated"}}
	nav.reconcileFleet(map[int]bool{})
	if len(nav.store.Cars) != 0 || len(nav.priorityQueue) != 1 {
		t.Fatal("navigation cache or terminal cleanup missing")
	}
	event := <-nav.priorityQueue
	if event.Type != "navigation_ended" || !event.liveActivityOnly || event.ObservationMode != "source_membership_cleanup" {
		t.Fatal("removal invented ordinary trip-end alert")
	}
	found := false
	for _, session := range nav.history.Sessions {
		if session.SessionID == "historical-unrelated" {
			found = true
		}
	}
	if !found {
		t.Fatal("fleet reconcile deleted historical navigation")
	}
}

func TestMQTTMonitorRefreshRejectsPreviousConnectionSUBACK(t *testing.T) {
	for _, kind := range []string{"parking", "charging", "navigation"} {
		t.Run(kind, func(t *testing.T) {
			r := installMQTTTestRuntime(t, map[int]bool{1: true})
			beginMQTTConnection(kind)
			client := &notificationSubscriptionClient{}
			var refresh func()
			var disconnect func()
			var connected func() bool
			var markReplacement func()
			var lastError func() string
			switch kind {
			case "parking":
				m := testParkingMonitor(t)
				m.client = client
				refresh = m.refreshFleetSubscription
				disconnect = func() { m.mu.Lock(); m.connected = false; m.mu.Unlock() }
				connected = m.mqttConnected
				markReplacement = func() { m.mu.Lock(); m.connected = true; m.lastError = "replacement diagnostic"; m.mu.Unlock() }
				lastError = func() string { m.mu.RLock(); defer m.mu.RUnlock(); return m.lastError }
			case "charging":
				m := testChargingBaselineMonitor(t)
				m.client = client
				refresh = m.refreshFleetSubscription
				disconnect = func() { m.mu.Lock(); m.connected = false; m.mu.Unlock() }
				connected = func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.connected }
				markReplacement = func() { m.mu.Lock(); m.connected = true; m.lastError = "replacement diagnostic"; m.mu.Unlock() }
				lastError = func() string { m.mu.Lock(); defer m.mu.Unlock(); return m.lastError }
			case "navigation":
				m := testNavigationMonitor(t)
				m.client = client
				refresh = m.refreshFleetSubscription
				disconnect = func() { m.mu.Lock(); m.connected = false; m.mu.Unlock() }
				connected = func() bool { m.mu.Lock(); defer m.mu.Unlock(); return m.connected }
				markReplacement = func() { m.mu.Lock(); m.connected = true; m.lastError = "replacement diagnostic"; m.mu.Unlock() }
				lastError = func() string { m.mu.Lock(); defer m.mu.Unlock(); return m.lastError }
			}
			client.onSubscribe = func(mqtt.MessageHandler) {
				// Simulate ConnectionLost while the subscription result is still
				// pending. The Paho client object itself survives reconnects.
				recordMQTTConnection(kind, false)
				disconnect()
			}
			refresh()
			r.mu.RLock()
			connection := r.connections[kind]
			r.mu.RUnlock()
			if connected() || connection.Connected {
				t.Fatal("late successful SUBACK revived the previous connection")
			}
			client.onSubscribe = nil
			refresh()
			r.mu.RLock()
			current := r.connections[kind]
			r.mu.RUnlock()
			if !connected() || !current.Connected || current.Epoch != connection.Epoch {
				t.Fatal("current subscription did not recover without incrementing epoch")
			}
			client.err = fmt.Errorf("old SUBACK error")
			client.onSubscribe = func(mqtt.MessageHandler) {
				epoch := beginMQTTConnection(kind)
				recordMQTTConnection(kind, true, epoch)
				markReplacement()
			}
			refresh()
			r.mu.RLock()
			current = r.connections[kind]
			retryAt := r.baselineRetryAt
			r.mu.RUnlock()
			if !connected() || !current.Connected || lastError() != "replacement diagnostic" || !retryAt.IsZero() {
				t.Fatal("late failed SUBACK clobbered or retried a replacement connection")
			}
		})
	}
}
