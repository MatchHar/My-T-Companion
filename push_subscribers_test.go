package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
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
