package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokenEqual(t *testing.T) {
	t.Parallel()
	if !tokenEqual("correct-token", "correct-token") {
		t.Fatal("equal tokens must match")
	}
	for _, candidate := range []string{"", "wrong-token", "correct-token-extra"} {
		if tokenEqual(candidate, "correct-token") {
			t.Fatalf("unexpected token match for %q", candidate)
		}
	}
}

func TestSoftwareNotificationRelaySignatureAndPrivacy(t *testing.T) {
	t.Parallel()
	const secret = "relay-secret"
	var received softwareNotificationEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		signature := hmac.New(sha256.New, []byte(secret))
		_, _ = signature.Write(body)
		want := "sha256=" + hex.EncodeToString(signature.Sum(nil))
		if !hmac.Equal([]byte(r.Header.Get("X-My-T-Signature")), []byte(want)) {
			t.Fatal("relay signature mismatch")
		}
		if strings.Contains(string(body), "VIN") || strings.Contains(string(body), "latitude") {
			t.Fatal("payload contains prohibited vehicle data")
		}
		if err := json.Unmarshal(body, &received); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	monitor := &softwareNotificationMonitor{
		relayURL:       server.URL,
		relaySecret:    secret,
		installationID: "installation-1",
		httpClient:     server.Client(),
	}
	event := softwareNotificationEvent{
		EventID:        "event-1",
		InstallationID: "installation-1",
		CarID:          1,
		VehicleName:    "MY CAR",
		Type:           "update_available",
		CurrentVersion: "2026.20.6",
		UpdateVersion:  "2026.26.3",
		ObservedAt:     "2026-07-27T12:00:00Z",
	}
	if err := monitor.deliver(event); err != nil {
		t.Fatal(err)
	}
	if received.UpdateVersion != event.UpdateVersion || received.InstallationID != event.InstallationID {
		t.Fatalf("unexpected relay event: %+v", received)
	}
}

func TestSoftwareNotificationStatePersistence(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state.json")
	monitor := &softwareNotificationMonitor{
		statePath: path,
		store: softwareNotificationStore{
			Cars: map[int]carSoftwareState{
				1: {Version: "2026.20.6", UpdateAvailable: true, UpdateVersion: "2026.26.3"},
			},
			Delivered: map[string]string{"event-1": "2026-07-27T12:00:00Z"},
		},
	}
	monitor.mu.Lock()
	if err := monitor.saveLocked(); err != nil {
		t.Fatal(err)
	}
	monitor.mu.Unlock()

	loaded := &softwareNotificationMonitor{
		statePath: path,
		store: softwareNotificationStore{
			Cars:      map[int]carSoftwareState{},
			Delivered: map[string]string{},
		},
	}
	loaded.load()
	if loaded.store.Cars[1].UpdateVersion != "2026.26.3" {
		t.Fatalf("state was not restored: %+v", loaded.store)
	}
	if _, ok := loaded.store.Delivered["event-1"]; !ok {
		t.Fatal("delivered event deduplication was not restored")
	}
}

func TestSoftwarePushPairingRejectsUntrustedRelay(t *testing.T) {
	monitor := &softwareNotificationMonitor{}
	err := monitor.configure(softwarePushPairing{
		InstallationID: strings.Repeat("a", 48),
		RelayURL:       "https://example.invalid/events",
		RelaySecret:    strings.Repeat("b", 64),
	})
	if err == nil {
		t.Fatal("expected untrusted relay URL to be rejected")
	}
}

func TestParsePageLimit(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		want  int
		ok    bool
	}{
		{"", 5000, true},
		{"1", 1, true},
		{"10000", 10000, true},
		{"0", 0, false},
		{"10001", 0, false},
		{"invalid", 0, false},
	}
	for _, test := range tests {
		got, err := parsePageLimit(test.value)
		if (err == nil) != test.ok || got != test.want {
			t.Errorf("parsePageLimit(%q) = %d, %v; want %d, ok=%v", test.value, got, err, test.want, test.ok)
		}
	}
}

func TestParseDateParam(t *testing.T) {
	t.Parallel()
	got, err := parseDateParam("2026-07-27T12:30:00+08:00")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 7, 27, 4, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %s, want %s", got, want)
	}
	if _, err := parseDateParam(""); err == nil {
		t.Fatal("missing date must fail")
	}
}

func TestCapabilitiesRequiresAuthentication(t *testing.T) {
	oldToken, oldProbe := apiToken, authProbeURL
	apiToken, authProbeURL = "test-token", ""
	t.Cleanup(func() {
		apiToken, authProbeURL = oldToken, oldProbe
	})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil)
	recorder := httptest.NewRecorder()
	handleCapabilities(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d, want 401", recorder.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil)
	request.Header.Set("Authorization", "Bearer test-token")
	recorder = httptest.NewRecorder()
	handleCapabilities(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("got status %d, want 200", recorder.Code)
	}
}
