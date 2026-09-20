package main

import (
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type lockCaptureTransport struct{ events chan lockSecurePushEvent }

func (transport lockCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var event lockSecurePushEvent
	if err := json.NewDecoder(request.Body).Decode(&event); err != nil {
		return nil, err
	}
	transport.events <- event
	return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
}

func TestSettledLockDeliversPerVehicleOnceWithoutRealNetwork(t *testing.T) {
	originalRegistry := pushRegistry
	pushRegistry = nil
	defer func() { pushRegistry = originalRegistry }()
	events := make(chan lockSecurePushEvent, 4)
	m := &lockSecureNotificationMonitor{connected: true, paired: true, installationID: strings.Repeat("a", 48), prefs: lockSecurePrefs{Enabled: true}, statePath: filepath.Join(t.TempDir(), "lock.json"), store: lockSecureStore{Cars: map[int]lockSecureCarState{}, Delivered: map[string]string{}}, inFlight: map[string]bool{}, httpClient: &http.Client{Transport: lockCaptureTransport{events: events}}}
	defer m.stop()
	at := time.Now().UTC().Add(-5 * time.Second)
	for _, id := range []int{1, 2} {
		state := presenceFixture()
		state.Locked = parseMQTTBool("false")
		m.store.Cars[id] = state
		m.observe(id, "is_user_present", "false", at)
		m.observe(id, "healthy", "true", at)
		m.observe(id, "locked", "true", at)
		timer := m.pending[id]
		timer.Stop()
		m.finishPending(id, timer)
	}
	seen := map[int]bool{}
	for i := 0; i < 2; i++ {
		select {
		case event := <-events:
			seen[event.CarID] = true
		case <-time.After(2 * time.Second):
			t.Fatal("valid lock did not dispatch")
		}
	}
	if !seen[1] || !seen[2] {
		t.Fatal("vehicles were mixed")
	}
	deadline := time.Now().Add(time.Second)
	for {
		m.mu.Lock()
		done := len(m.inFlight) == 0
		m.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery didn't complete")
		}
		time.Sleep(time.Millisecond)
	}
	m.observe(1, "healthy", "true", time.Now())
	if m.pending[1] != nil {
		t.Fatal("heartbeat rescheduled consumed episode")
	}
	select {
	case <-events:
		t.Fatal("duplicate delivery")
	default:
	}
}

func presenceFixture() lockSecureCarState {
	return lockSecureCarState{Locked: parseMQTTBool("true"), UserPresent: parseMQTTBool("false"),
		State: "online", ShiftState: "P", DoorsOpen: parseMQTTBool("false"), TrunkOpen: parseMQTTBool("false"), FrunkOpen: parseMQTTBool("false")}
}

func TestLockEvidenceRequiresCurrentEpisodeAndKnownFields(t *testing.T) {
	now := time.Now().UTC()
	base := presenceFixture()
	valid := lockSecureEvidence{PresenceLive: true, LastHealthyAt: now, LockAt: now.Add(-4 * time.Second), StableSince: now.Add(-4 * time.Second)}
	if !valid.ready(base, now) {
		t.Fatal("valid settled lock not ready")
	}
	for _, field := range []string{"lock", "presence", "doors", "trunk", "frunk", "state", "gear"} {
		t.Run(field, func(t *testing.T) {
			state := base
			switch field {
			case "lock":
				state.Locked = nil
			case "presence":
				state.UserPresent = nil
			case "doors":
				state.DoorsOpen = nil
			case "trunk":
				state.TrunkOpen = nil
			case "frunk":
				state.FrunkOpen = nil
			case "state":
				state.State = "offline"
			case "gear":
				state.ShiftState = ""
			}
			if valid.ready(state, now) {
				t.Fatal("unknown/offline accepted")
			}
		})
	}
	for _, scenario := range []string{"no_presence", "no_lock", "no_health", "old_health", "old_episode", "unsettled", "future"} {
		t.Run(scenario, func(t *testing.T) {
			e := valid
			switch scenario {
			case "no_presence":
				e.PresenceLive = false
			case "no_lock":
				e.LockAt = time.Time{}
			case "no_health":
				e.LastHealthyAt = time.Time{}
			case "old_health":
				e.LastHealthyAt = now.Add(-91 * time.Second)
			case "old_episode":
				e.LockAt = now.Add(-31 * time.Second)
			case "unsettled":
				e.StableSince = now
			case "future":
				e.LockAt = now.Add(time.Second)
			}
			if e.ready(base, now) {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func TestLockEvidenceRetainedAndContextInvalidation(t *testing.T) {
	now := time.Now()
	state := presenceFixture()
	for _, field := range []string{"locked", "is_user_present", "state", "doors_open"} {
		e := lockSecureEvidence{PresenceLive: true, LockAt: now}
		e.observe(state, state, field, true, now)
		if !e.LockAt.IsZero() {
			t.Fatalf("retained %s emitted an episode", field)
		}
	}
	for _, field := range []string{"locked", "is_user_present", "state", "shift_state", "doors_open", "healthy"} {
		e := lockSecureEvidence{PresenceLive: true, LockAt: now}
		changed := state
		switch field {
		case "locked":
			changed.Locked = parseMQTTBool("false")
		case "is_user_present":
			changed.UserPresent = parseMQTTBool("true")
		case "state":
			changed.State = "asleep"
		case "shift_state":
			changed.ShiftState = "D"
		case "doors_open":
			changed.DoorsOpen = parseMQTTBool("true")
		case "healthy":
			changed.Healthy = parseMQTTBool("false")
		}
		e.observe(state, changed, field, false, now)
		if !e.LockAt.IsZero() {
			t.Fatalf("%s failed to cancel", field)
		}
	}
}

func TestLockUnchangedPresenceNeedsNoSecondFalseMessage(t *testing.T) {
	now := time.Now()
	state := presenceFixture()
	e := lockSecureEvidence{}
	e.observe(state, state, "is_user_present", false, now.Add(-time.Hour))
	state.Healthy = parseMQTTBool("true")
	e.observe(state, state, "healthy", false, now)
	previous := state
	previous.Locked = parseMQTTBool("false")
	e.observe(previous, state, "locked", false, now)
	if !e.ready(state, now.Add(4*time.Second)) {
		t.Fatal("unchanged presence wrongly expires despite continuous context and heartbeat")
	}
}

func TestLockPresenceAfterLockCanCompleteReorderedBatch(t *testing.T) {
	now := time.Now()
	state := presenceFixture()
	e := lockSecureEvidence{LastHealthyAt: now}
	previous := state
	previous.Locked = parseMQTTBool("false")
	state.UserPresent = parseMQTTBool("true")
	e.observe(previous, state, "locked", false, now)
	previous = state
	state.UserPresent = parseMQTTBool("false")
	e.observe(previous, state, "is_user_present", false, now.Add(time.Second))
	if !e.ready(state, now.Add(5*time.Second)) {
		t.Fatal("late absence within lock episode not handled")
	}
}

func TestLockResetDiscardsPersistedEligibilityAndKeepsDedupe(t *testing.T) {
	m := &lockSecureNotificationMonitor{statePath: filepath.Join(t.TempDir(), "lock.json"), store: lockSecureStore{Cars: map[int]lockSecureCarState{}, Delivered: map[string]string{"sent": "time"}}}
	state := presenceFixture()
	state.Initialized = true
	state.LastSecure = true
	state.LastPushedAt = "old-delivery"
	m.store.Cars[1] = state
	m.store.Cars[2] = state
	m.mu.Lock()
	m.resetEvidenceLocked()
	m.mu.Unlock()
	for _, id := range []int{1, 2} {
		if m.store.Cars[id].UserPresent != nil || m.store.Cars[id].Initialized || m.store.Cars[id].LastPushedAt != "old-delivery" {
			t.Fatal("unsafe restart or lost cooldown")
		}
	}
	if m.store.Delivered["sent"] != "time" {
		t.Fatal("dedupe lost")
	}
	now := time.Now()
	for field, value := range map[string]string{"locked": "true", "is_user_present": "false", "state": "online", "shift_state": "P", "doors_open": "false", "trunk_open": "false", "frunk_open": "false"} {
		m.observeMessage(1, field, value, now, true)
	}
	if len(m.pending) != 0 || !m.evidence[1].LockAt.IsZero() {
		t.Fatal("reconnect replay scheduled alert")
	}
}

func TestPresenceSnapshotIsPerCarAndHonestAboutRetainedData(t *testing.T) {
	now := time.Now().UTC()
	m := &lockSecureNotificationMonitor{connected: true, store: lockSecureStore{Cars: map[int]lockSecureCarState{1: presenceFixture(), 2: presenceFixture()}}, evidence: map[int]lockSecureEvidence{1: {PresenceLive: true, PresenceReceivedAt: now.Add(-time.Hour), LastHealthyAt: now}}}
	if m.presenceSnapshot(1, now)["context_current"] != true {
		t.Fatal("live connection evidence missing")
	}
	second := m.presenceSnapshot(2, now)
	if second["context_current"] != false || second["received_at"] != nil {
		t.Fatal("retained receipt manufactured")
	}
	b, err := json.Marshal(m.presenceSnapshot(3, now))
	if err != nil {
		t.Fatal(err)
	}
	var unknown map[string]any
	_ = json.Unmarshal(b, &unknown)
	if unknown["value"] != nil {
		t.Fatal("unknown converted to false")
	}
	m.connected = false
	if m.presenceSnapshot(1, now)["context_current"] != false {
		t.Fatal("disconnected snapshot current")
	}
}

func TestPendingTimerCancelledOnUnlockAndCarsIndependent(t *testing.T) {
	now := time.Now()
	m := &lockSecureNotificationMonitor{connected: true, statePath: filepath.Join(t.TempDir(), "lock.json"), store: lockSecureStore{Cars: map[int]lockSecureCarState{}, Delivered: map[string]string{}}, inFlight: map[string]bool{}}
	defer m.stop()
	for _, id := range []int{1, 2} {
		s := presenceFixture()
		s.Locked = parseMQTTBool("false")
		m.store.Cars[id] = s
		m.observe(id, "is_user_present", "false", now)
		m.observe(id, "healthy", "true", now)
		m.observe(id, "locked", "true", now)
	}
	old := m.pending[1]
	if old == nil || m.pending[2] == nil {
		t.Fatal("lock didn't schedule")
	}
	m.observe(1, "locked", "false", now.Add(time.Second))
	if m.pending[1] != nil || m.pending[2] == nil {
		t.Fatal("cancel leaked across vehicles")
	}
	m.finishPending(1, old)
	if len(m.inFlight) != 0 {
		t.Fatal("cancelled timer sent notification")
	}
}
