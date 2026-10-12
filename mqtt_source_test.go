package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func installMQTTTestRuntime(t *testing.T, cars map[int]bool) *teslaMateMQTTRuntime {
	t.Helper()
	t.Setenv("MQTT_NAMESPACE", "")
	r := newTeslaMateMQTTRuntime(nil)
	r.applyFleet(cars, time.Now().UTC())
	previous := mqttSourceRuntime.Swap(r)
	t.Cleanup(func() { mqttSourceRuntime.Store(previous) })
	return r
}

func TestMQTTNamespaceStrictIsolation(t *testing.T) {
	for _, namespace := range []string{"", "home-1", "garage_2"} {
		t.Run(namespace, func(t *testing.T) {
			t.Setenv("MQTT_NAMESPACE", namespace)
			prefix := "teslamate/"
			if namespace != "" {
				prefix += namespace + "/"
			}
			if teslaMateMQTTTopics([]string{"locked"})[prefix+"cars/+/locked"] != 1 {
				t.Fatal("subscription prefix differs from parser")
			}
			id, field, ok := parseTeslaMateMQTTTopic(prefix + "cars/73/locked")
			if !ok || id != 73 || field != "locked" {
				t.Fatal("valid new car rejected")
			}
			for _, wrong := range []string{"teslamate/other/cars/73/locked", prefix + "cars/0/locked", prefix + "cars/-1/locked", prefix + "cars/073/locked", prefix + "cars/73/locked/more", prefix + "cars/73/+", "other/cars/73/locked"} {
				if _, _, ok := parseTeslaMateMQTTTopic(wrong); ok {
					t.Fatalf("cross-source/invalid topic accepted: %s", wrong)
				}
			}
		})
	}
	for _, invalid := range []string{"a/b", "+", "#", " home ", "with space", "a\nb", "$garage", "'quoted'", "back\\slash", "車", strings.Repeat("n", 129)} {
		t.Setenv("MQTT_NAMESPACE", invalid)
		if _, err := mqttNamespace(); err == nil {
			t.Fatalf("invalid namespace accepted: %q", invalid)
		}
		if len(teslaMateMQTTTopics([]string{"locked"})) != 0 {
			t.Fatal("invalid namespace silently subscribed default source")
		}
	}
}

func TestMQTTDynamicFleetKeepsFailureSnapshotAndBoundsUnknownIDs(t *testing.T) {
	r := installMQTTTestRuntime(t, map[int]bool{1: true, 2: false})
	if !mqttCarEligible(1) || mqttCarEligible(2) || mqttCarEligible(73) {
		t.Fatal("fleet membership ignored")
	}
	if len(r.refreshCh) != 1 {
		t.Fatal("new car did not request bounded refresh")
	}
	for i := 1000; i < 10000; i++ {
		mqttCarEligible(i)
	}
	if len(r.refreshCh) != 1 || len(r.receipts) != 0 {
		t.Fatal("unknown topics grew cache/refresh queue")
	}
	r.fleetFailure("fleet_read_unavailable")
	if !mqttCarEligible(1) {
		t.Fatal("one failed refresh removed known eligible car")
	}
	r.applyFleet(map[int]bool{1: true, 73: true}, time.Now().UTC())
	if !mqttCarEligible(73) {
		t.Fatal("new car needs a restart")
	}
	r.mu.Lock()
	r.lastFleetSuccess = time.Now().Add(-mqttFleetGrace - time.Second)
	r.mu.Unlock()
	if mqttCarEligible(73) {
		t.Fatal("stale fleet allowed new alert")
	}
	if mqttSourceDiagnostics(73)["fleet_snapshot_usable"] != false {
		t.Fatal("stale inventory not diagnosed")
	}
	r.applyFleet(map[int]bool{1: true}, time.Now().UTC())
	if mqttCarEligible(73) {
		t.Fatal("removed car still monitored")
	}
}

func TestMQTTReceiptTimesNeverBecomeVehicleObservation(t *testing.T) {
	r := installMQTTTestRuntime(t, map[int]bool{73: true})
	beginMQTTConnection("parking")
	// A broker can deliver retained messages before the SUBACK token completes.
	for _, field := range []string{"battery_level", "rated_battery_range_km", "locked"} {
		value := "50"
		if field == "locked" {
			value = "true"
		}
		acceptTeslaMateMQTTMessage(friendMQTTTestMessage{retained: true, topic: "teslamate/cars/73/" + field, payload: []byte(value)}, "parking")
	}
	recordMQTTConnection("parking", true)
	snapshot := mqttSourceDiagnostics(73)
	if snapshot["core_current_connection_complete"] != true || snapshot["receipt_is_vehicle_sample_time"] != false || snapshot["source_observed_at"] != nil {
		t.Fatal("retained baseline misidentified")
	}
	fields := snapshot["fields"].(map[string]mqttFieldReceipt)
	if !fields["parking.locked"].Retained || fields["parking.locked"].LastLiveReceiptAt != nil || fields["parking.locked"].SourceObservedAt != nil {
		t.Fatal("retained receipt forged freshness")
	}
	acceptTeslaMateMQTTMessage(friendMQTTTestMessage{topic: "teslamate/cars/73/locked", payload: []byte("true")}, "parking")
	fields = mqttSourceDiagnostics(73)["fields"].(map[string]mqttFieldReceipt)
	if fields["parking.locked"].LastLiveReceiptAt == nil || fields["parking.locked"].SourceObservedAt != nil {
		t.Fatal("live receipt metadata wrong")
	}
	recordMQTTConnection("parking", false)
	if mqttSourceDiagnostics(73)["core_current_connection_complete"] != false || mqttSourceDiagnostics(73)["status"] != "last_known_only" {
		t.Fatal("disconnect kept current evidence")
	}
	if len(r.receipts) != 1 {
		t.Fatal("disconnect erased last-known metadata")
	}
}

func TestMQTTInvalidValuesAndPayloadBounds(t *testing.T) {
	installMQTTTestRuntime(t, map[int]bool{1: true})
	for _, item := range []struct{ field, value string }{{"locked", "junk"}, {"battery_level", "101"}, {"battery_level", "NaN"}, {"rated_battery_range_km", "+Inf"}, {"rated_battery_range_km", "-1"}, {"active_route", "bad-json"}} {
		if mqttReceiptValueStatus(item.field, []byte(item.value)) != "invalid" {
			t.Fatalf("invalid baseline accepted: %+v", item)
		}
	}
	if mqttReceiptValueStatus("locked", []byte("null")) != "unknown" {
		t.Fatal("null treated known")
	}
	if _, _, ok := acceptTeslaMateMQTTMessage(friendMQTTTestMessage{topic: "teslamate/cars/1/active_route", payload: make([]byte, (16<<10)+1)}); ok {
		t.Fatal("oversize MQTT accepted")
	}
}

func TestMQTTFleetDiagnosticsDistinguishUnknownEmptyAndStale(t *testing.T) {
	r := installMQTTTestRuntime(t, map[int]bool{})
	if got := mqttFleetDiagnostics(); got["status"] != "available" || got["fleet_snapshot_usable"] != true {
		t.Fatal("successful empty fleet was not available")
	}
	r.mu.Lock()
	r.lastFleetSuccess = time.Time{}
	r.mu.Unlock()
	r.fleetFailure("fleet_read_unavailable")
	if got := mqttFleetDiagnostics(); got["status"] != "fleet_unavailable" || got["fleet_snapshot_usable"] != false || got["fleet_last_success_at"] != nil {
		t.Fatal("failed initial fleet looked like successful empty fleet")
	}
	r.applyFleet(map[int]bool{1: true}, time.Now().UTC())
	beginMQTTConnection("parking")
	for field, value := range map[string]string{"battery_level": "50", "rated_battery_range_km": "280", "locked": "true"} {
		acceptTeslaMateMQTTMessage(friendMQTTTestMessage{topic: "teslamate/cars/1/" + field, payload: []byte(value)}, "parking")
	}
	recordMQTTConnection("parking", true)
	r.mu.Lock()
	r.lastFleetSuccess = time.Now().Add(-mqttFleetGrace - time.Second)
	r.mu.Unlock()
	if got := mqttSourceDiagnostics(1); got["status"] != "stale_fleet" || got["core_current_connection_complete"] != false || got["core_baseline_complete"] != true {
		t.Fatal("old complete receipt baseline claimed current fleet readiness")
	}
	if mqttFleetDiagnostics()["status"] != "stale_fleet" || mqttFleetSnapshotUsable(time.Now().Add(time.Minute), time.Now()) {
		t.Fatal("stale/future fleet timestamp accepted")
	}
}

func TestMQTTSoftwarePercentRequiresKnownCurrentReceipt(t *testing.T) {
	installMQTTTestRuntime(t, map[int]bool{1: true})
	beginMQTTConnection("software")
	if mqttSoftwarePercent(1, "download_perc", 0) != nil {
		t.Fatal("missing percentage became zero")
	}
	message := friendMQTTTestMessage{topic: "teslamate/cars/1/download_perc", payload: []byte("0"), retained: true}
	acceptTeslaMateMQTTMessage(message, "software")
	recordMQTTConnection("software", true)
	if mqttSoftwarePercent(1, "download_perc", 0) != 0 {
		t.Fatal("actual zero was lost")
	}
	for _, value := range []string{"null", "junk", "101", "NaN", "10.0", "1e1"} {
		message.payload = []byte(value)
		acceptTeslaMateMQTTMessage(message, "software")
		if mqttSoftwarePercent(1, "download_perc", 0) != nil {
			t.Fatalf("unknown/invalid percentage became zero: %s", value)
		}
	}
	message.payload = []byte("75")
	acceptTeslaMateMQTTMessage(message, "software")
	recordMQTTConnection("software", false)
	if mqttSoftwarePercent(1, "download_perc", 75) != nil {
		t.Fatal("disconnected percentage looked current")
	}
	beginMQTTConnection("software")
	recordMQTTConnection("software", true)
	if mqttSoftwarePercent(1, "download_perc", 75) != nil {
		t.Fatal("previous connection percentage looked current")
	}
}

func TestMQTTDuplicateCannotEstablishRejectedReceiptBaseline(t *testing.T) {
	r := installMQTTTestRuntime(t, map[int]bool{1: true})
	beginMQTTConnection("parking")
	for field, value := range map[string]string{"battery_level": "50", "rated_battery_range_km": "280", "locked": "true"} {
		if _, _, ok := acceptTeslaMateMQTTMessage(friendMQTTTestMessage{topic: "teslamate/cars/1/" + field, payload: []byte(value), duplicate: true}, "parking"); ok {
			t.Fatal("duplicate accepted as current-connection evidence")
		}
	}
	recordMQTTConnection("parking", true)
	if len(r.receipts) != 0 || mqttSourceDiagnostics(1)["status"] != "waiting_for_mqtt" {
		t.Fatal("rejected duplicate completed baseline")
	}
}

func TestMQTTBaselineRetryIsBoundedAndRecoversWithoutAnotherNewCar(t *testing.T) {
	r := installMQTTTestRuntime(t, map[int]bool{1: true})
	var calls int
	registerMQTTFleetMonitor("test", func() { calls++ }, nil)
	for i := 0; i < 1000; i++ {
		requestMQTTBaselineRetry()
	}
	r.mu.RLock()
	due := r.baselineRetryAt
	r.mu.RUnlock()
	r.retryBaselines(due.Add(-time.Nanosecond))
	if calls != 0 {
		t.Fatal("retry ignored backoff")
	}
	r.retryBaselines(due)
	r.retryBaselines(due.Add(time.Hour))
	if calls != 1 || !r.baselineRetryAt.IsZero() {
		t.Fatal("retry grew unbounded or required another new car")
	}
}

func TestMQTTConnectionEpochRejectsStaleSUBACK(t *testing.T) {
	r := installMQTTTestRuntime(t, map[int]bool{1: true})
	first := beginMQTTConnection("software")
	if first == 0 || mqttConnectionEpoch("software") != first || !recordMQTTConnection("software", true, first) {
		t.Fatal("current connection epoch rejected")
	}
	recordMQTTConnection("software", false)
	if recordMQTTConnection("software", true, first) || r.connections["software"].Connected {
		t.Fatal("disconnected connection accepted its delayed SUBACK")
	}
	second := beginMQTTConnection("software")
	if second <= first || recordMQTTConnection("software", true, first) || !recordMQTTConnection("software", true, second) {
		t.Fatal("new connection did not distinguish old and current SUBACK")
	}
	previous := mqttSourceRuntime.Swap(nil)
	defer mqttSourceRuntime.Store(previous)
	if beginMQTTConnection("isolated") != 0 || mqttConnectionEpoch("isolated") != 0 || !recordMQTTConnection("isolated", true, 0) {
		t.Fatal("isolated constructors without a runtime were not allowed")
	}
}

func TestMQTTFleetReconcilePreservesOtherCarsAndCallbacks(t *testing.T) {
	r := installMQTTTestRuntime(t, map[int]bool{1: true, 2: true})
	var calls int
	registerMQTTFleetMonitor("test", nil, func(cars map[int]bool) {
		calls++
		if !cars[1] {
			t.Fatal("healthy car lost")
		}
	})
	acceptTeslaMateMQTTMessage(friendMQTTTestMessage{topic: "teslamate/cars/2/locked", payload: []byte("true")}, "parking")
	r.applyFleet(map[int]bool{1: true, 2: false, 73: true}, time.Now().UTC())
	if calls != 2 || len(r.receipts) != 0 {
		t.Fatal("inactive receipt/cache not removed")
	}
	if mqttSourceDiagnostics(2)["status"] != "logging_disabled" || mqttSourceDiagnostics(73)["status"] != "waiting_for_mqtt" {
		t.Fatal("new/disabled phase confused")
	}
}

func TestNewCarFirstPacketBeforeInventoryRecoveredByResubscribe(t *testing.T) {
	r := installMQTTTestRuntime(t, map[int]bool{1: true})
	parking := testParkingMonitor(t)
	message := friendMQTTTestMessage{topic: "teslamate/cars/73/locked", payload: []byte("true"), retained: true}
	parking.handleMQTTMessage(nil, message)
	if len(parking.store.Cars) != 0 {
		t.Fatal("unknown car bypassed source fleet")
	}
	var resubscribed int
	registerMQTTFleetMonitor("parking", func() { resubscribed++; parking.handleMQTTMessage(nil, message) }, parking.reconcileFleet)
	r.applyFleet(map[int]bool{1: true, 73: true}, time.Now().UTC())
	if resubscribed != 1 || parking.currentValues(73)["locked"] != "true" || len(parking.store.Events) != 0 {
		t.Fatal("new car bootstrap not recovered silently")
	}
}

func TestCompanionStatusAddsPrivatePerCarDiagnostics(t *testing.T) {
	installMQTTTestRuntime(t, map[int]bool{73: true})
	previousToken, previousAuth := apiToken, authProbeURL
	apiToken, authProbeURL = "test-owner", ""
	t.Cleanup(func() { apiToken, authProbeURL = previousToken, previousAuth })
	request := httptest.NewRequest("GET", "/api/v1/cars/73/companion-status", nil)
	w := httptest.NewRecorder()
	handleCompanionStatus(w, request, "73")
	if w.Code != 401 {
		t.Fatal("diagnostics exposed without owner auth")
	}
	request.Header.Set("Authorization", "Bearer test-owner")
	w = httptest.NewRecorder()
	handleCompanionStatus(w, request, "73")
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Data["mqtt_diagnostics"] == nil {
		t.Fatal("missing additive diagnostics")
	}
	if string(body.Data["locked"]) != "null" {
		t.Fatal("waiting new car shown as unlocked")
	}
	for _, key := range []string{"update_available", "download_percent", "install_percent"} {
		if string(body.Data[key]) != "null" {
			t.Fatalf("waiting new car fabricated %s: %s", key, body.Data[key])
		}
	}
	if string(body.Data["update_availability_status"]) != `"unconfirmed"` {
		t.Fatal("unknown software status not explained")
	}
}

func TestLowBatteryReconnectRetainedCannotEscalateAndKeepsSnooze(t *testing.T) {
	m, events := newLowBatteryTestMonitor(t, 1)
	t.Cleanup(m.stop)
	at := time.Now().UTC()
	state := lowBatteryTestState(19)
	state.Initialized = true
	state.Latched = true
	state.EpisodeID = "existing"
	state.Actions["phone"] = lowBatteryActionState{Action: "snooze", SnoozeUntil: at.Add(time.Hour).Format(time.RFC3339)}
	m.store.Cars[1] = state
	m.resetObservationBaseline()
	for field, value := range map[string]string{"state": "asleep", "charging_state": "disconnected", "battery_level": "9"} {
		m.observeMessage(1, field, value, at, true)
	}
	m.evaluate(1, at)
	if len(*events) != 0 || m.store.Cars[1].EpisodeID != "existing" || m.store.Cars[1].Actions["phone"].Action != "snooze" {
		t.Fatal("retained reconnect created new low episode or lost action")
	}
	if m.store.Cars[1].LastObservedAt != "" {
		t.Fatal("retained receipt became observation time")
	}
	// A high baseline rearms; the next real low episode can notify normally.
	m.observeMessage(1, "battery_level", "30", at, true)
	m.evaluate(1, at)
	if m.store.Cars[1].Latched {
		t.Fatal("high reconnect baseline failed to rearm")
	}
	m.observeMessage(1, "battery_level", "19", at, false)
	m.evaluate(1, at)
	if len(*events) != 1 {
		t.Fatal("next genuine low episode was suppressed")
	}
}
