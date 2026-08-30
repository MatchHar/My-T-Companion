package main

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type capturedLowBatteryEvent struct {
	InstallationID string `json:"installation_id"`
	SourceID       string `json:"source_id"`
	CarID          int    `json:"car_id"`
	Type           string `json:"type"`
	EpisodeID      string `json:"episode_id"`
	BatteryLevel   int    `json:"battery_level"`
}

func lowBatteryLevel(value int) *int { return &value }

func newLowBatteryTestMonitor(t *testing.T, installations int) (*lowBatteryNotificationMonitor, *[]capturedLowBatteryEvent) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	registry := newPushSubscriberRegistry()
	on := true
	for index := 0; index < installations; index++ {
		id, secret, relayURL := testInstallation()
		if _, err := registry.upsert(pushPairRequest{
			InstallationID: id,
			SourceID:       "00000000-0000-4000-8000-00000000000" + string(rune('1'+index)),
			RelayURL:       relayURL,
			RelaySecret:    secret,
			LowBattery:     &on,
		}); err != nil {
			t.Fatal(err)
		}
	}
	var captureMu sync.Mutex
	captured := []capturedLowBatteryEvent{}
	registry.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			return nil, err
		}
		var event capturedLowBatteryEvent
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, err
		}
		captureMu.Lock()
		captured = append(captured, event)
		captureMu.Unlock()
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}
	previousRegistry := pushRegistry
	pushRegistry = registry
	t.Cleanup(func() { pushRegistry = previousRegistry })
	monitor := &lowBatteryNotificationMonitor{
		statePath: filepath.Join(dir, "low-battery-state.json"),
		store:     lowBatteryStore{Cars: map[int]lowBatteryCarState{}},
		timers:    map[int]*time.Timer{},
		stopCh:    make(chan struct{}),
	}
	return monitor, &captured
}

func lowBatteryTestState(level int) lowBatteryCarState {
	return lowBatteryCarState{
		DisplayName:   "Model 3",
		State:         "asleep",
		ShiftState:    "P",
		ChargingState: "disconnected",
		BatteryLevel:  lowBatteryLevel(level),
		Actions:       map[string]lowBatteryActionState{},
	}
}

func TestLowBatteryRetainedBaselineNeverCreatesStartupAlert(t *testing.T) {
	monitor, captured := newLowBatteryTestMonitor(t, 1)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	monitor.store.Cars[1] = lowBatteryTestState(19)
	monitor.evaluate(1, now)
	monitor.evaluate(1, now.Add(time.Minute))

	state := monitor.store.Cars[1]
	if !state.Initialized || !state.Latched || state.EpisodeID == "" {
		t.Fatalf("low retained snapshot must establish a silent episode: %+v", state)
	}
	if len(*captured) != 0 {
		t.Fatalf("retained startup snapshot pushed %d event(s)", len(*captured))
	}
}

func TestLowBatteryThresholdsSevereEscalationAndRearm(t *testing.T) {
	monitor, captured := newLowBatteryTestMonitor(t, 1)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	monitor.store.Cars[1] = lowBatteryTestState(50)
	monitor.evaluate(1, now)

	state := monitor.store.Cars[1]
	state.BatteryLevel = lowBatteryLevel(20)
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(time.Minute))
	if len(*captured) != 0 {
		t.Fatal("20 percent must not notify")
	}

	state = monitor.store.Cars[1]
	state.BatteryLevel = lowBatteryLevel(19)
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(2*time.Minute))
	if len(*captured) != 1 || (*captured)[0].Type != "vehicle_low_battery" {
		t.Fatalf("expected one ordinary alert, got %+v", *captured)
	}

	state = monitor.store.Cars[1]
	state.BatteryLevel = lowBatteryLevel(10)
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(3*time.Minute))
	if len(*captured) != 1 {
		t.Fatal("10 percent must not trigger strict severe threshold")
	}

	state = monitor.store.Cars[1]
	state.BatteryLevel = lowBatteryLevel(9)
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(4*time.Minute))
	if len(*captured) != 2 || (*captured)[1].Type != "vehicle_low_battery_severe" {
		t.Fatalf("expected one severe escalation, got %+v", *captured)
	}
	monitor.evaluate(1, now.Add(5*time.Minute))
	if len(*captured) != 2 {
		t.Fatal("severe alert repeated within one episode")
	}

	state = monitor.store.Cars[1]
	state.BatteryLevel = lowBatteryLevel(25)
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(6*time.Minute))
	state = monitor.store.Cars[1]
	if state.Latched || state.EpisodeID != "" {
		t.Fatalf("25 percent must rearm and clear the episode: %+v", state)
	}
	state.BatteryLevel = lowBatteryLevel(9)
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(7*time.Minute))
	if len(*captured) != 3 || (*captured)[2].Type != "vehicle_low_battery_severe" {
		t.Fatalf("new episode entering below 10 must use severe alert: %+v", *captured)
	}
}

func TestLowBatteryWaitsUntilParkedAndNotCharging(t *testing.T) {
	monitor, captured := newLowBatteryTestMonitor(t, 1)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	monitor.store.Cars[1] = lowBatteryTestState(50)
	monitor.evaluate(1, now)

	state := monitor.store.Cars[1]
	state.BatteryLevel = lowBatteryLevel(19)
	state.ShiftState = "D"
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(time.Minute))
	if len(*captured) != 0 {
		t.Fatal("driving vehicle must not notify")
	}
	state = monitor.store.Cars[1]
	state.ShiftState = "P"
	state.ChargingState = "charging"
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(2*time.Minute))
	if len(*captured) != 0 {
		t.Fatal("charging vehicle must not notify")
	}
	state = monitor.store.Cars[1]
	state.ChargingState = "disconnected"
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(3*time.Minute))
	if len(*captured) != 1 {
		t.Fatalf("parked and not charging should notify once, got %+v", *captured)
	}
}

func TestLowBatterySnoozeIsPerInstallationAndOneShot(t *testing.T) {
	monitor, captured := newLowBatteryTestMonitor(t, 2)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	monitor.store.Cars[1] = lowBatteryTestState(50)
	monitor.evaluate(1, now)
	state := monitor.store.Cars[1]
	state.BatteryLevel = lowBatteryLevel(19)
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(time.Minute))
	if len(*captured) != 2 {
		t.Fatalf("initial alert must fan out to both installations: %+v", *captured)
	}
	firstID := (*captured)[0].InstallationID
	secondID := (*captured)[1].InstallationID
	episodeID := monitor.store.Cars[1].EpisodeID
	if err := monitor.applyAction(lowBatteryActionRequest{
		InstallationID: firstID, CarID: 1, EpisodeID: episodeID, Action: "snooze_4h",
	}, now.Add(-lowBatterySnoozeDelay)); err != nil {
		t.Fatal(err)
	}
	if err := monitor.applyAction(lowBatteryActionRequest{
		InstallationID: secondID, CarID: 1, EpisodeID: episodeID, Action: "ack",
	}, now); err != nil {
		t.Fatal(err)
	}
	monitor.processDueReminders(now.Add(time.Minute))
	if len(*captured) != 3 || (*captured)[2].InstallationID != firstID || (*captured)[2].Type != "vehicle_low_battery_reminder" {
		t.Fatalf("only snoozing installation should get one reminder: %+v", *captured)
	}
	monitor.processDueReminders(now.Add(2 * time.Minute))
	if len(*captured) != 3 {
		t.Fatal("completed snooze reminder repeated")
	}
}

func TestLowBatteryChargingCancelsPendingSnooze(t *testing.T) {
	monitor, captured := newLowBatteryTestMonitor(t, 1)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	monitor.store.Cars[1] = lowBatteryTestState(50)
	monitor.evaluate(1, now)
	state := monitor.store.Cars[1]
	state.BatteryLevel = lowBatteryLevel(19)
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(time.Minute))
	installationID := (*captured)[0].InstallationID
	episodeID := monitor.store.Cars[1].EpisodeID
	if err := monitor.applyAction(lowBatteryActionRequest{
		InstallationID: installationID, CarID: 1, EpisodeID: episodeID, Action: "snooze_4h",
	}, now); err != nil {
		t.Fatal(err)
	}
	state = monitor.store.Cars[1]
	state.ChargingState = "charging"
	monitor.store.Cars[1] = state
	monitor.evaluate(1, now.Add(2*time.Minute))
	monitor.processDueReminders(now.Add(5 * time.Hour))
	if len(*captured) != 1 {
		t.Fatalf("charging must cancel the reminder, got %+v", *captured)
	}
}
