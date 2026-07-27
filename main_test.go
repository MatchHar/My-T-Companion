package main

import (
	"net/http"
	"net/http/httptest"
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
