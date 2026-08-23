package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func resetTeslaMateVersionCache() {
	teslaMateVersionState.Lock()
	teslaMateVersionState.version = ""
	teslaMateVersionState.source = ""
	teslaMateVersionState.checkedAt = time.Time{}
	teslaMateVersionState.Unlock()
}

func TestCurrentTeslaMateVersionPrefersLiveSettingsOverStaleMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/settings" {
			t.Fatalf("path = %q, want /settings", r.URL.Path)
		}
		_, _ = w.Write([]byte(`<table class="about"><tr><th align="right">Version</th><td>4.2.0<strong>Update available</strong></td></tr></table>`))
	}))
	defer server.Close()

	oldClient := teslaMateVersionClient
	teslaMateVersionClient = server.Client()
	t.Cleanup(func() { teslaMateVersionClient = oldClient; resetTeslaMateVersionCache() })
	t.Setenv("TESLAMATE_WEB_URL", server.URL)
	t.Setenv("TESLAMATE_VERSION", "4.0.1")
	resetTeslaMateVersionCache()

	version, source, _ := currentTeslaMateVersion(context.Background())
	if version != "4.2.0" || source != "live_web" {
		t.Fatalf("got %q from %q, want live 4.2.0", version, source)
	}
}

func TestCurrentTeslaMateVersionUsesExplicitFallbackWhenLiveProbeFails(t *testing.T) {
	oldClient := teslaMateVersionClient
	teslaMateVersionClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: http.NoBody, Header: make(http.Header)}, nil
	})}
	t.Cleanup(func() { teslaMateVersionClient = oldClient; resetTeslaMateVersionCache() })
	t.Setenv("TESLAMATE_WEB_URL", "http://teslamate:4000")
	t.Setenv("TESLAMATE_VERSION", "4.1.0")
	resetTeslaMateVersionCache()

	version, source, _ := currentTeslaMateVersion(context.Background())
	if version != "4.1.0" || source != "install_metadata" {
		t.Fatalf("got %q from %q, want fallback 4.1.0", version, source)
	}
}

func TestTeslaMateSettingsURLAndVersionValidation(t *testing.T) {
	got, err := teslaMateSettingsURL("http://teslamate:4000/")
	if err != nil || got.String() != "http://teslamate:4000/settings" {
		t.Fatalf("settings URL = %v, %v", got, err)
	}
	if normalizeTeslaMateVersion("v4.2.0") != "4.2.0" {
		t.Fatal("valid semantic version was rejected")
	}
	for _, invalid := range []string{"latest", "4.2", "sha256:abc", "4.2.0@sha256:abc"} {
		if normalizeTeslaMateVersion(invalid) != "" {
			t.Fatalf("invalid version %q was accepted", invalid)
		}
	}
}

func TestLiveVersionProbeDoesNotDependOnTeslaMateUILanguage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<table class="about table is-narrow"><tbody><tr><th>Versión</th><td>4.2.0</td></tr></tbody></table>`))
	}))
	defer server.Close()

	oldClient := teslaMateVersionClient
	teslaMateVersionClient = server.Client()
	t.Cleanup(func() { teslaMateVersionClient = oldClient; resetTeslaMateVersionCache() })
	t.Setenv("TESLAMATE_WEB_URL", server.URL)
	resetTeslaMateVersionCache()

	version, err := fetchLiveTeslaMateVersion(context.Background())
	if err != nil || version != "4.2.0" {
		t.Fatalf("got %q, %v; want localized live 4.2.0", version, err)
	}
}
