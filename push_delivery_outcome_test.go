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

func TestQueuedRelayIsNotRecordedAsDelivered(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	id, secret, url := testInstallation()
	on := true
	if _, err := reg.upsert(pushPairRequest{
		InstallationID:         id,
		SourceID:               "00000000-0000-4000-8000-000000000001",
		RelayURL:               url,
		RelaySecret:            secret,
		NavigationLiveActivity: &on,
		NavigationTripAlerts:   &on,
	}); err != nil {
		t.Fatal(err)
	}
	reg.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusConflict,
			Header:     http.Header{"Retry-After": []string{"2"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":"live_activity_token_pending","retry_after_seconds":2}`)),
		}, nil
	})}
	previous := pushRegistry
	pushRegistry = reg
	t.Cleanup(func() { pushRegistry = previous })

	m := testNavigationMonitor(t)
	m.installationID = id
	reg.addDeliveryResolver(m.reconcileDeliveryResolution)
	sub := reg.matching(1, func(s pushSubscriber) bool { return s.wantsNavigationLiveActivity(1) })[0]
	event := navigationLiveActivityEvent{
		EventID:        "nav-event-1",
		InstallationID: id,
		CarID:          1,
		Type:           "navigation_started",
		SessionID:      "navigation-logical-1",
		Destination:    "Stop A",
		ObservedAt:     time.Now().UTC().Format(time.RFC3339),
	}
	m.recordHistoryEvent(event, string(pushDeliveryQueued))
	outcome, err := m.deliver(event)
	if err != nil {
		t.Fatalf("queued/awaiting token must not be an error: %v", err)
	}
	if outcome != pushDeliveryAwaitingToken {
		t.Fatalf("outcome=%q", outcome)
	}
	if got := m.history.Sessions[0].LastDeliveryStatus; got != string(pushDeliveryAwaitingToken) {
		t.Fatalf("history status=%q", got)
	}
	if len(reg.store.Outbox) != 2 {
		t.Fatalf("outbox=%d want live+banner queued", len(reg.store.Outbox))
	}
	if snap := reg.snapshot(id)["pending_deliveries"]; snap != 2 {
		t.Fatalf("pending=%v", snap)
	}
	_ = sub
}

func TestOutboxAcceptanceReconcilesHistory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	id, secret, url := testInstallation()
	on := true
	if _, err := reg.upsert(pushPairRequest{
		InstallationID:         id,
		SourceID:               "00000000-0000-4000-8000-000000000002",
		RelayURL:               url,
		RelaySecret:            secret,
		NavigationLiveActivity: &on,
	}); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	reg.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable,
				Body:       io.NopCloser(strings.NewReader(`{"error":"temporary"}`)),
			}, nil
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody}, nil
	})}
	previous := pushRegistry
	pushRegistry = reg
	t.Cleanup(func() { pushRegistry = previous })

	m := testNavigationMonitor(t)
	reg.addDeliveryResolver(m.reconcileDeliveryResolution)
	event := navigationLiveActivityEvent{
		EventID:     "nav-event-2",
		CarID:       1,
		Type:        "navigation_ended",
		SessionID:   "navigation-logical-2",
		Destination: "Stop A",
		EndReason:   "arrived",
		ObservedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	m.recordHistoryEvent(event, string(pushDeliveryQueued))
	outcome, err := m.deliver(event)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != pushDeliveryQueued {
		t.Fatalf("first outcome=%q", outcome)
	}
	if got := m.history.Sessions[0].LastDeliveryStatus; got != string(pushDeliveryQueued) {
		t.Fatalf("queued status=%q", got)
	}
	reg.retryDue(time.Now().UTC().Add(time.Minute))
	if got := m.history.Sessions[0].LastDeliveryStatus; got != string(pushDeliveryAPNsAccepted) {
		t.Fatalf("reconciled status=%q", got)
	}
	if len(reg.store.Outbox) != 0 {
		t.Fatalf("outbox not drained: %d", len(reg.store.Outbox))
	}
}

func TestDeliverJSONStillReturnsNilWhenQueued(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	id, secret, url := testInstallation()
	on := true
	if _, err := reg.upsert(pushPairRequest{
		InstallationID: id,
		RelayURL:       url,
		RelaySecret:    secret,
		SoftwareUpdate: &on,
	}); err != nil {
		t.Fatal(err)
	}
	reg.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	})}
	sub := reg.matching(1, func(s pushSubscriber) bool { return s.SoftwareUpdate })[0]
	if err := reg.deliverJSON(sub, []byte(`{"event_id":"event-keep-nil","type":"update_available"}`)); err != nil {
		t.Fatalf("queued deliverJSON must remain nil: %v", err)
	}
}

func TestClassifyAwaitingToken(t *testing.T) {
	if got := classifyRelayResponse(http.StatusConflict, []byte(`{"error":"live_activity_token_pending"}`)); got != pushDeliveryAwaitingToken {
		t.Fatalf("got %q", got)
	}
	if got := classifyRelayResponse(http.StatusAccepted, nil); got != pushDeliveryAPNsAccepted {
		t.Fatalf("got %q", got)
	}
	if got := classifyRelayResponse(http.StatusServiceUnavailable, nil); got != pushDeliveryQueued {
		t.Fatalf("got %q", got)
	}
}

func TestLateUpdateCannotRewriteTerminalHistoryOrNewerDelivery(t *testing.T) {
	m := testNavigationMonitor(t)
	event := navigationLiveActivityEvent{SessionID: "terminal-leg", CarID: 1, Destination: "A",
		Type: "navigation_ended", EndReason: "arrived", Revision: 8,
		ObservedAt: time.Now().UTC().Format(time.RFC3339), RemainingDistanceKM: floatPointer(0)}
	m.recordHistoryEvent(event, string(pushDeliveryQueued))
	late := event
	late.Revision = 7
	late.Type = "navigation_updated"
	late.RemainingDistanceKM = floatPointer(5)
	m.recordHistoryEvent(late, string(pushDeliveryAPNsAccepted))
	got := m.history.Sessions[0]
	if got.LastEventType != "navigation_ended" || got.LastRemainingDistanceKM == nil || *got.LastRemainingDistanceKM != 0 {
		t.Fatalf("late update rewrote terminal snapshot: %+v", got)
	}
	m.mu.Lock()
	m.upsertInstallationDeliveryLocked(event.SessionID, navigationInstallationDelivery{
		InstallationID: "phone", EventType: "navigation_updated", EventID: "new", Revision: 7, Status: string(pushDeliveryAwaitingToken)})
	m.upsertInstallationDeliveryLocked(event.SessionID, navigationInstallationDelivery{
		InstallationID: "phone", EventType: "navigation_updated", EventID: "old", Revision: 6, Status: string(pushDeliveryAPNsAccepted)})
	m.mu.Unlock()
	if m.history.Sessions[0].Deliveries[0].EventID != "new" {
		t.Fatal("older retry replaced the newer delivery result")
	}
}

func TestLogicalSessionIDIsPresentOnFanOutPayload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	id, secret, url := testInstallation()
	on := true
	source := "00000000-0000-4000-8000-000000000003"
	if _, err := reg.upsert(pushPairRequest{
		InstallationID:         id,
		SourceID:               source,
		RelayURL:               url,
		RelaySecret:            secret,
		NavigationLiveActivity: &on,
	}); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	reg.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: http.StatusAccepted, Body: http.NoBody}, nil
	})}
	previous := pushRegistry
	pushRegistry = reg
	t.Cleanup(func() { pushRegistry = previous })

	m := testNavigationMonitor(t)
	event := navigationLiveActivityEvent{
		EventID:     "nav-event-3",
		CarID:       1,
		Type:        "navigation_updated",
		SessionID:   "navigation-logical-3",
		Destination: "Home",
		ObservedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	outcome, err := m.deliver(event)
	if err != nil {
		t.Fatal(err)
	}
	if outcome != pushDeliveryAPNsAccepted {
		t.Fatalf("outcome=%q", outcome)
	}
	if payload["source_id"] != source || payload["car_id"].(float64) != 1 {
		t.Fatalf("identity missing: %+v", payload)
	}
	if payload["logical_session_id"] != "navigation-logical-3" {
		t.Fatalf("logical_session_id=%v", payload["logical_session_id"])
	}
	if payload["session_id"] == "navigation-logical-3" {
		t.Fatal("fan-out session_id should be source-scoped")
	}
}
