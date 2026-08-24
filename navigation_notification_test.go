package main

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func floatPointer(value float64) *float64 { return &value }

func TestResolveNavigationStartNameIgnoresStickyFenceFromOlderDrive(t *testing.T) {
	// db is nil in unit tests → no SQL fallbacks.
	if got := resolveNavigationStartName(1, "  Home  ", "Office"); got != "Home" {
		t.Fatalf("live geofence: got %q", got)
	}
	if got := resolveNavigationStartName(1, "", "  Office  "); got != "" {
		t.Fatalf("stale sticky geofence leaked into current drive: got %q", got)
	}
	if got := resolveNavigationStartName(1, "   ", ""); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}

func TestObserveStickyLastKnownGeofence(t *testing.T) {
	tmp := t.TempDir()
	m := &navigationNotificationMonitor{
		statePath:     filepath.Join(tmp, "nav.json"),
		historyPath:   filepath.Join(tmp, "history.json"),
		queue:         make(chan navigationLiveActivityEvent, 8),
		priorityQueue: make(chan navigationLiveActivityEvent, 4),
		pending:       map[int]*time.Timer{},
		store: navigationNotificationStore{
			Cars:      map[int]carNavigationState{},
			Delivered: map[string]string{},
		},
		history:        navigationPushHistoryStore{},
		installationID: "install-test",
		enabled:        true,
	}

	now := mustParseTime(t, "2026-08-05T10:00:00Z")
	m.observe(1, "geofence", "Home", now)
	m.observe(1, "state", "driving", now)
	m.observe(1, "geofence", "", now) // left fence

	m.mu.Lock()
	state := m.store.Cars[1]
	m.mu.Unlock()
	if state.Geofence != "" {
		t.Fatalf("live geofence should clear, got %q", state.Geofence)
	}
	if state.LastKnownGeofence != "Home" {
		t.Fatalf("sticky last known: got %q", state.LastKnownGeofence)
	}

	// Start navigation after leaving geofence. The old Home label remains useful
	// only as diagnostic state; the active drive database will own start_name.
	m.observe(1, "active_route", `{"destination":"Airport"}`, now.Add(time.Minute))
	m.mu.Lock()
	state = m.store.Cars[1]
	m.mu.Unlock()
	if !state.Active {
		t.Fatal("expected active navigation session")
	}
	if state.StartName != "" {
		t.Fatalf("stale start_name after leave: got %q", state.StartName)
	}

	// Drain queued start so channel doesn't block later tests in same process.
	select {
	case event := <-m.queue:
		if event.StartName != "" || event.Destination != "Airport" {
			t.Fatalf("queued event start/dest: %+v", event)
		}
	default:
		t.Fatal("expected navigation_started to be queued")
	}
}

func TestReplayActiveNavigationTargetsOnlyNewlyEnabledInstallation(t *testing.T) {
	tmp := t.TempDir()
	m := &navigationNotificationMonitor{
		statePath:     filepath.Join(tmp, "nav.json"),
		historyPath:   filepath.Join(tmp, "history.json"),
		queue:         make(chan navigationLiveActivityEvent, 2),
		priorityQueue: make(chan navigationLiveActivityEvent, 2),
		pending:       map[int]*time.Timer{},
		store: navigationNotificationStore{
			Cars: map[int]carNavigationState{1: {
				DisplayName:      "My T",
				Destination:      "Airport",
				Active:           true,
				SessionID:        "navigation-live-test",
				SessionStartedAt: "2026-08-24T12:00:00Z",
			}},
			Delivered: map[string]string{},
		},
		history: navigationPushHistoryStore{},
	}

	if got := m.replayActiveStarts("new-installation"); got != 1 {
		t.Fatalf("replayed=%d", got)
	}
	event := <-m.queue
	if event.targetInstallationID != "new-installation" || !event.liveActivityOnly {
		t.Fatalf("replay scope=%+v", event)
	}
}

func TestExpireStaleNavigationSessionQueuesTerminalEvent(t *testing.T) {
	tmp := t.TempDir()
	now := mustParseTime(t, "2026-08-23T12:00:00Z")
	m := &navigationNotificationMonitor{
		statePath:     filepath.Join(tmp, "nav.json"),
		historyPath:   filepath.Join(tmp, "history.json"),
		queue:         make(chan navigationLiveActivityEvent, 2),
		priorityQueue: make(chan navigationLiveActivityEvent, 2),
		pending:       map[int]*time.Timer{},
		store: navigationNotificationStore{
			Cars: map[int]carNavigationState{
				1: {
					DisplayName:      "My T",
					Destination:      "Office",
					Active:           true,
					SessionID:        "navigation-stale-test",
					SessionStartedAt: now.Add(-navigationTransientMaximumAge - time.Minute).Format(time.RFC3339),
					LastObservedAt:   now.Add(-time.Minute).Format(time.RFC3339),
				},
			},
			Delivered: map[string]string{},
		},
		history:        navigationPushHistoryStore{},
		installationID: "install-test",
		enabled:        true,
	}

	m.expireStaleSessions(now)
	m.mu.Lock()
	state := m.store.Cars[1]
	m.mu.Unlock()
	if state.Active {
		t.Fatal("stale navigation session remained active")
	}
	select {
	case event := <-m.priorityQueue:
		if event.Type != "navigation_ended" || event.EndReason != "stale" {
			t.Fatalf("unexpected terminal event: %+v", event)
		}
	default:
		t.Fatal("expected stale navigation terminal event")
	}
}

func TestDestinationLabelUpgradeKeepsNavigationSession(t *testing.T) {
	tmp := t.TempDir()
	m := &navigationNotificationMonitor{
		statePath:     filepath.Join(tmp, "nav.json"),
		historyPath:   filepath.Join(tmp, "history.json"),
		queue:         make(chan navigationLiveActivityEvent, 8),
		priorityQueue: make(chan navigationLiveActivityEvent, 4),
		pending:       map[int]*time.Timer{},
		store: navigationNotificationStore{
			Cars:      map[int]carNavigationState{},
			Delivered: map[string]string{},
		},
		history:        navigationPushHistoryStore{},
		installationID: "install-test",
		enabled:        true,
	}

	now := mustParseTime(t, "2026-08-14T09:00:00Z")
	m.observe(1, "state", "driving", now)
	m.observe(1, "active_route", `{"destination":"20 Hi Mount Dr","miles_to_arrival":12.5}`, now)
	start := <-m.queue

	m.observe(1, "active_route", `{"destination":"Home","miles_to_arrival":12.4}`, now.Add(5*time.Second))
	m.mu.Lock()
	state := m.store.Cars[1]
	m.mu.Unlock()

	if state.SessionID != start.SessionID {
		t.Fatalf("label-only update replaced session: got %q want %q", state.SessionID, start.SessionID)
	}
	if state.Destination != "Home" {
		t.Fatalf("latest destination label was not kept: %q", state.Destination)
	}
	select {
	case event := <-m.priorityQueue:
		t.Fatalf("label-only update queued an end event: %+v", event)
	default:
	}
}

func TestDestinationDistanceChangeStartsNewSession(t *testing.T) {
	if navigationDestinationChangeStartsNewSession(
		"Office",
		"Airport",
		floatPointer(20.4),
		floatPointer(7.1),
	) != true {
		t.Fatal("material route change must start a new session")
	}
	if navigationDestinationChangeStartsNewSession(
		"20 Hi Mount Dr",
		"Home",
		floatPointer(20.4),
		floatPointer(20.1),
	) != false {
		t.Fatal("near-identical route distance must keep the current session")
	}
	if navigationDestinationChangeStartsNewSession(
		"住家",
		"Highway 7, Unionville, Markham",
		nil,
		floatPointer(19.6),
	) != false {
		t.Fatal("label swap with missing remaining km must keep the current session")
	}
	if navigationDestinationChangeStartsNewSession(
		"Highway 7, Unionville, Markham",
		"住家",
		floatPointer(19.6),
		nil,
	) != false {
		t.Fatal("label swap after remaining km arrives must keep the current session")
	}
}

func TestNavigationEndReasonIsSerialized(t *testing.T) {
	event := navigationLiveActivityEvent{
		EventID:        "event-1",
		InstallationID: "install-test",
		CarID:          1,
		Type:           "navigation_ended",
		SessionID:      "navigation-test",
		Destination:    "Home",
		EndReason:      "arrived",
		ObservedAt:     "2026-08-14T09:30:00Z",
	}
	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) == "" || !json.Valid(payload) {
		t.Fatal("invalid navigation event JSON")
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["end_reason"] != "arrived" {
		t.Fatalf("end_reason missing from relay payload: %s", payload)
	}
}

func mustParseTime(t *testing.T, raw string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}
