package main

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

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
