package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestResolveNavigationStartNamePrefersLiveThenSticky(t *testing.T) {
	// db is nil in unit tests → no SQL fallbacks.
	if got := resolveNavigationStartName(1, "  Home  ", "Office"); got != "Home" {
		t.Fatalf("live geofence: got %q", got)
	}
	if got := resolveNavigationStartName(1, "", "  Office  "); got != "Office" {
		t.Fatalf("sticky geofence: got %q", got)
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

	// Start navigation after leaving geofence — start_name must still resolve to Home.
	m.observe(1, "active_route", `{"destination":"Airport"}`, now.Add(time.Minute))
	m.mu.Lock()
	state = m.store.Cars[1]
	m.mu.Unlock()
	if !state.Active {
		t.Fatal("expected active navigation session")
	}
	if state.StartName != "Home" {
		t.Fatalf("start_name after leave: got %q want Home", state.StartName)
	}

	// Drain queued start so channel doesn't block later tests in same process.
	select {
	case event := <-m.queue:
		if event.StartName != "Home" || event.Destination != "Airport" {
			t.Fatalf("queued event start/dest: %+v", event)
		}
	default:
		t.Fatal("expected navigation_started to be queued")
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
