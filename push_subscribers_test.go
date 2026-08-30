package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type capturedPushDelivery struct {
	headerInstallation string
	installationID     string
	eventID            string
	eventType          string
}

func TestAnonymousVehicleAliasIsStableAndServerScoped(t *testing.T) {
	firstNamespace := strings.Repeat("11", 32)
	secondNamespace := strings.Repeat("22", 32)
	first, err := anonymousVehicleAlias(firstNamespace, 1)
	if err != nil {
		t.Fatal(err)
	}
	repeat, err := anonymousVehicleAlias(firstNamespace, 1)
	if err != nil {
		t.Fatal(err)
	}
	otherCar, err := anonymousVehicleAlias(firstNamespace, 2)
	if err != nil {
		t.Fatal(err)
	}
	otherServer, err := anonymousVehicleAlias(secondNamespace, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first != repeat || first == otherCar || first == otherServer {
		t.Fatalf("aliases must be stable per server/car and distinct across scope: %q %q %q %q", first, repeat, otherCar, otherServer)
	}
	if len(first) != 64 {
		t.Fatalf("alias must be a SHA-256 hex digest, got %q", first)
	}
}

func TestVehicleNamespacePersistsWithoutExposingCarIdentity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	first := newPushSubscriberRegistry()
	namespace := first.store.VehicleNamespace
	if len(namespace) != 64 {
		t.Fatalf("expected generated namespace, got %q", namespace)
	}
	second := newPushSubscriberRegistry()
	if second.store.VehicleNamespace != namespace {
		t.Fatalf("vehicle namespace changed across reload: %q != %q", second.store.VehicleNamespace, namespace)
	}
}

func testInstallation() (id, secret, url string) {
	idBytes := make([]byte, 24)
	_, _ = rand.Read(idBytes)
	sec := make([]byte, 32)
	_, _ = rand.Read(sec)
	return hex.EncodeToString(idBytes), hex.EncodeToString(sec), officialSoftwarePushRelayURL
}

func TestPushSubscriberUpsertDoesNotDuplicate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	id, secret, url := testInstallation()
	on := true
	first, err := reg.upsert(pushPairRequest{
		InstallationID:         id,
		RelayURL:               url,
		RelaySecret:            secret,
		SoftwareUpdate:         &on,
		ChargingLiveActivity:   &on,
		NavigationLiveActivity: &on,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first["subscriber_count"] != 1 {
		t.Fatalf("count=%v", first["subscriber_count"])
	}
	second, err := reg.upsert(pushPairRequest{
		InstallationID: id,
		RelayURL:       url,
		RelaySecret:    secret,
		Status:         "paused",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second["subscriber_count"] != 1 {
		t.Fatalf("switch must not mint a new id, count=%v", second["subscriber_count"])
	}
	if second["self_status"] != "paused" {
		t.Fatalf("status=%v", second["self_status"])
	}
	resumed, err := reg.upsert(pushPairRequest{
		InstallationID: id,
		RelayURL:       url,
		RelaySecret:    secret,
		Status:         "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resumed["subscriber_count"] != 1 || resumed["self_status"] != "active" {
		t.Fatalf("resume=%v", resumed)
	}
}

func TestPushSubscriberStoresTripAlertsSeparatelyFromLiveActivity(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	id, secret, url := testInstallation()
	on, off := true, false
	snap, err := reg.upsert(pushPairRequest{
		InstallationID:         id,
		RelayURL:               url,
		RelaySecret:            secret,
		ChargingLiveActivity:   &on,
		NavigationLiveActivity: &on,
		NavigationTripAlerts:   &off,
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap["navigation_live_activity"] != true {
		t.Fatalf("live activity=%v", snap["navigation_live_activity"])
	}
	if snap["navigation_trip_alerts"] != false {
		t.Fatalf("trip alerts=%v", snap["navigation_trip_alerts"])
	}
	updated, err := reg.upsert(pushPairRequest{
		InstallationID:       id,
		RelayURL:             url,
		RelaySecret:          secret,
		NavigationTripAlerts: &on,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated["navigation_live_activity"] != true {
		t.Fatal("upsert must not clear live-activity when only trip alerts change")
	}
	if updated["navigation_trip_alerts"] != true {
		t.Fatalf("trip alerts=%v", updated["navigation_trip_alerts"])
	}
}

func TestPushSubscriberPauseUnknownIsNoop(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	id, _, _ := testInstallation()
	snap, err := reg.pause(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap["subscriber_count"] != 0 {
		t.Fatalf("pause of unknown id must not create a row: %v", snap)
	}
}

func TestPushSubscriberDeleteWithoutIDRejectedWhenMany(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	on := true
	for i := 0; i < 2; i++ {
		id, secret, url := testInstallation()
		if _, err := reg.upsert(pushPairRequest{
			InstallationID: id,
			RelayURL:       url,
			RelaySecret:    secret,
			SoftwareUpdate: &on,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := reg.unsubscribe(""); err == nil || err.Error() != "need_installation_id" {
		t.Fatalf("expected need_installation_id, got %v", err)
	}
}

func TestPushSubscriberReplaceRemovesOthers(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	on := true
	id1, secret1, url := testInstallation()
	id2, secret2, _ := testInstallation()
	_, _ = reg.upsert(pushPairRequest{InstallationID: id1, RelayURL: url, RelaySecret: secret1, SoftwareUpdate: &on})
	_, _ = reg.upsert(pushPairRequest{InstallationID: id2, RelayURL: url, RelaySecret: secret2, SoftwareUpdate: &on})
	out, err := reg.upsert(pushPairRequest{
		InstallationID: id2,
		RelayURL:       url,
		RelaySecret:    secret2,
		Mode:           "replace",
		SoftwareUpdate: &on,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out["subscriber_count"] != 1 {
		t.Fatalf("replace should leave one, got %v", out)
	}
	if out["self_status"] != "active" {
		t.Fatalf("self=%v", out["self_status"])
	}
}

func TestPushSubscriberMigratesLegacyPairing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	id, secret, url := testInstallation()
	pairing := []byte(`{"installation_id":"` + id + `","relay_url":"` + url + `","relay_secret":"` + secret + `"}`)
	if err := os.WriteFile(filepath.Join(dir, "software-push-pairing.json"), pairing, 0600); err != nil {
		t.Fatal(err)
	}
	reg := newPushSubscriberRegistry()
	snap := reg.snapshot(id)
	if snap["subscriber_count"] != 1 || snap["self_status"] != "active" {
		t.Fatalf("migration failed: %v", snap)
	}
}

func TestPushDeliveryPersistsRetryableFailure(t *testing.T) {
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
		return nil, errors.New("temporary network failure")
	})}
	sub := reg.matching(1, func(s pushSubscriber) bool { return s.SoftwareUpdate })[0]
	payload := []byte(`{"event_id":"event-1","type":"update_available"}`)
	if err := reg.deliverJSON(sub, payload); err != nil {
		t.Fatalf("durably queued delivery should be accepted: %v", err)
	}
	if got := reg.snapshot(id)["pending_deliveries"]; got != 1 {
		t.Fatalf("pending=%v", got)
	}
	reloaded := newPushSubscriberRegistry()
	if got := reloaded.snapshot(id)["pending_deliveries"]; got != 1 {
		t.Fatalf("persisted pending=%v", got)
	}
}

func TestPushDeliveryPausesInvalidInstallation(t *testing.T) {
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
		return &http.Response{StatusCode: http.StatusGone, Body: http.NoBody}, nil
	})}
	sub := reg.matching(1, func(s pushSubscriber) bool { return s.SoftwareUpdate })[0]
	err := reg.deliverJSON(sub, []byte(`{"event_id":"event-2","type":"update_available"}`))
	if err == nil {
		t.Fatal("permanent invalid-installation response must be reported")
	}
	if got := reg.snapshot(id)["self_status"]; got != "paused" {
		t.Fatalf("status=%v", got)
	}
}

func TestChargingAndNavigationFanOutUsePerInstallationEventIDs(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	firstID, firstSecret, relayURL := testInstallation()
	secondID, secondSecret, _ := testInstallation()
	on := true
	for _, pairing := range []pushPairRequest{
		{
			InstallationID:         firstID,
			RelayURL:               relayURL,
			RelaySecret:            firstSecret,
			ChargingLiveActivity:   &on,
			NavigationLiveActivity: &on,
			NavigationTripAlerts:   &on,
		},
		{
			InstallationID:         secondID,
			RelayURL:               relayURL,
			RelaySecret:            secondSecret,
			ChargingLiveActivity:   &on,
			NavigationLiveActivity: &on,
			NavigationTripAlerts:   &on,
		},
	} {
		if _, err := reg.upsert(pairing); err != nil {
			t.Fatal(err)
		}
	}

	var captureMu sync.Mutex
	captured := make([]capturedPushDelivery, 0, 4)
	reg.http = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		payload, err := io.ReadAll(request.Body)
		if err != nil {
			t.Fatal(err)
		}
		var event struct {
			InstallationID string `json:"installation_id"`
			EventID        string `json:"event_id"`
			Type           string `json:"type"`
		}
		if err := json.Unmarshal(payload, &event); err != nil {
			t.Fatal(err)
		}
		captureMu.Lock()
		captured = append(captured, capturedPushDelivery{
			headerInstallation: request.Header.Get("X-My-T-Installation"),
			installationID:     event.InstallationID,
			eventID:            event.EventID,
			eventType:          event.Type,
		})
		captureMu.Unlock()
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}

	previousRegistry := pushRegistry
	pushRegistry = reg
	t.Cleanup(func() { pushRegistry = previousRegistry })

	baseEventID := strings.Repeat("a", 32)
	charging := &chargingNotificationMonitor{}
	if err := charging.deliver(chargingLiveActivityEvent{
		EventID:   baseEventID,
		CarID:     1,
		Type:      "charging_updated",
		SessionID: "charge-test-session",
	}); err != nil {
		t.Fatal(err)
	}
	assertPerInstallationDeliveries(t, captured, baseEventID, "charging_updated", firstID, secondID)

	captureMu.Lock()
	captured = captured[:0]
	captureMu.Unlock()
	navigation := &navigationNotificationMonitor{}
	if err := navigation.deliver(navigationLiveActivityEvent{
		EventID:     baseEventID,
		CarID:       1,
		Type:        "navigation_started",
		SessionID:   "navigation-test-session",
		Destination: "Home",
	}); err != nil {
		t.Fatal(err)
	}
	byType := map[string][]capturedPushDelivery{}
	for _, delivery := range captured {
		byType[delivery.eventType] = append(byType[delivery.eventType], delivery)
	}
	assertPerInstallationDeliveries(t, byType["navigation_started"], baseEventID, "navigation_started", firstID, secondID)
	alertBaseID := baseEventID + ":destination_trip_started"
	assertPerInstallationDeliveries(t, byType["destination_trip_started"], alertBaseID, "destination_trip_started", firstID, secondID)
	for _, installationID := range []string{firstID, secondID} {
		liveID := targetPushEventID(baseEventID, installationID, "navigation_started")
		alertID := targetPushEventID(alertBaseID, installationID, "destination_trip_started")
		if liveID == alertID {
			t.Fatalf("live activity and trip banner share event_id for %s", installationID)
		}
	}
}

func assertPerInstallationDeliveries(
	t *testing.T,
	captured []capturedPushDelivery,
	baseEventID, eventType, firstID, secondID string,
) {
	t.Helper()
	if len(captured) != 2 {
		t.Fatalf("captured=%d deliveries=%+v", len(captured), captured)
	}
	ids := map[string]string{}
	for _, delivery := range captured {
		if delivery.headerInstallation != delivery.installationID {
			t.Fatalf("header/body installation mismatch: %+v", delivery)
		}
		if delivery.eventType != eventType {
			t.Fatalf("type=%q want=%q", delivery.eventType, eventType)
		}
		if !regexp.MustCompile(`^[0-9a-f]{32}$`).MatchString(delivery.eventID) {
			t.Fatalf("event_id=%q", delivery.eventID)
		}
		want := targetPushEventID(baseEventID, delivery.installationID, eventType)
		if delivery.eventID != want {
			t.Fatalf("event_id=%q want=%q", delivery.eventID, want)
		}
		ids[delivery.installationID] = delivery.eventID
	}
	if ids[firstID] == "" || ids[secondID] == "" || ids[firstID] == ids[secondID] {
		t.Fatalf("per-installation IDs must both exist and differ: %+v", ids)
	}
}

func TestRelayRetryAfterDefersDurableOutboxRetry(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PUSH_STATE_PATH", filepath.Join(dir, "software-notifications.json"))
	reg := newPushSubscriberRegistry()
	id, secret, relayURL := testInstallation()
	sub := pushSubscriber{
		InstallationID: id,
		RelayURL:       relayURL,
		RelaySecret:    secret,
		Status:         pushStatusActive,
	}
	reg.http = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusConflict,
			Header:     http.Header{"Retry-After": []string{"90"}},
			Body:       http.NoBody,
		}, nil
	})}
	payload := []byte(`{"event_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","type":"vehicle_lock_secure"}`)
	started := time.Now().UTC()
	if err := reg.deliverJSON(sub, payload); err != nil {
		t.Fatal(err)
	}
	if len(reg.store.Outbox) != 1 {
		t.Fatalf("outbox=%+v", reg.store.Outbox)
	}
	next, err := time.Parse(time.RFC3339, reg.store.Outbox[0].NextAttemptAt)
	if err != nil {
		t.Fatal(err)
	}
	if next.Before(started.Add(89 * time.Second)) {
		t.Fatalf("next retry %s did not honor 90-second lease hint", next)
	}
	if got := relayRetryAfter("999999"); got != maxPushRetryDelay {
		t.Fatalf("retry-after cap=%s want=%s", got, maxPushRetryDelay)
	}
}
