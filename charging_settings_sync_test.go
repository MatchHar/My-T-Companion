package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func syncFixture(t *testing.T, upstream http.HandlerFunc) (http.Handler, *parkingEventMonitor) {
	t.Helper()
	oldToken, oldProbe, oldParking, oldGate, oldClient := apiToken, authProbeURL, parkingEvents, chargeSettingsGate, chargeSettingsClient
	oldRuntime := mqttSourceRuntime.Load()
	t.Cleanup(func() {
		apiToken, authProbeURL, parkingEvents, chargeSettingsGate, chargeSettingsClient = oldToken, oldProbe, oldParking, oldGate, oldClient
		mqttSourceRuntime.Store(oldRuntime)
	})
	apiToken, authProbeURL = "synthetic-owner", ""
	runtime := newTeslaMateMQTTRuntime(nil)
	runtime.cars[17], runtime.lastFleetSuccess = true, time.Now()
	mqttSourceRuntime.Store(runtime)
	m := &parkingEventMonitor{statePath: filepath.Join(t.TempDir(), "parking-fixture.json"), connected: true, store: parkingEventStore{Cars: map[int]parkingEventCarState{}}, baselines: map[int]map[string]bool{}}
	for field, value := range map[string]string{"state": "suspended", "plugged_in": "true", "charging_state": "stopped", "scheduled_charging_start_time": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)} {
		m.observeMessage(17, field, value, time.Now(), true)
	}
	parkingEvents, chargeSettingsGate = m, &chargeSettingsSyncGate{}
	server := httptest.NewServer(upstream)
	t.Cleanup(server.Close)
	t.Setenv("TESLAMATE_WEB_URL", server.URL)
	chargeSettingsClient = &http.Client{Timeout: time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return withOwnerAPI(http.HandlerFunc(handleStates)), m
}

func syncRequest(h http.Handler, method, path, body string, token bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if token {
		r.Header.Set("Authorization", "Bearer synthetic-owner")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestChargeSettingsSyncExactOwnerRouteAndCooldown(t *testing.T) {
	var calls int
	h, _ := syncFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "PUT" || r.URL.Path != "/api/car/17/logging/resume" || r.URL.RawQuery != "" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected upstream request")
		}
		w.WriteHeader(204)
	})
	path := "/api/companion/v1/cars/17/charging-settings-sync"
	for _, tc := range []struct {
		method, path, body string
		token              bool
		code               int
	}{
		{"POST", path, `{"intent":"overview_pull"}`, false, 401},
		{"GET", path, ``, true, 405},
		{"POST", "/api/v1/cars/17/charging-settings-sync", `{"intent":"overview_pull"}`, true, 404},
		{"POST", path + "?command=wake_up", `{"intent":"overview_pull"}`, true, 400},
		{"POST", path, `{"intent":"background"}`, true, 400},
		{"POST", path, `{"intent":"overview_pull","url":"https://example.invalid"}`, true, 400},
		{"POST", path, `{"intent":"overview_pull"} {}`, true, 400},
		{"POST", strings.Replace(path, "/17/", "/017/", 1), `{"intent":"overview_pull"}`, true, 400},
	} {
		if w := syncRequest(h, tc.method, tc.path, tc.body, tc.token); w.Code != tc.code {
			t.Errorf("%s %s=%d want%d", tc.method, tc.path, w.Code, tc.code)
		}
	}
	if calls != 0 {
		t.Fatal("rejected requests reached logger")
	}
	w := syncRequest(h, "POST", path, `{"intent":"charging_popup"}`, true)
	if w.Code != 202 || !strings.Contains(w.Body.String(), `"outcome":"requested"`) || calls != 1 {
		t.Fatalf("first request=%d %s calls%d", w.Code, w.Body, calls)
	}
	w = syncRequest(h, "POST", path, `{"intent":"overview_pull"}`, true)
	if w.Code != 429 || calls != 1 {
		t.Fatal("cooldown bypass")
	}
}

func TestChargeSettingsSyncRejectsSleepDisconnectedOldBaselineAndOtherCar(t *testing.T) {
	for _, mode := range []string{"asleep", "sleeping", "offline", "online", "charging", "unknown", "unplugged", "complete", "past", "disconnected", "baseline", "disabled", "no_runtime", "other_car"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			h, m := syncFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) })
			path := "/api/companion/v1/cars/17/charging-settings-sync"
			m.mu.Lock()
			v := m.store.Cars[17].Values
			switch mode {
			case "unplugged":
				v["plugged_in"] = "false"
			case "complete":
				v["charging_state"] = "complete"
			case "past":
				v["scheduled_charging_start_time"] = time.Now().Add(-time.Hour).Format(time.RFC3339)
			case "disconnected":
				m.connected = false
			case "baseline":
				m.baselines = nil
			case "disabled":
				mqttSourceRuntime.Load().cars[17] = false
			case "no_runtime":
				mqttSourceRuntime.Store(nil)
			case "other_car":
				path = strings.Replace(path, "/17/", "/18/", 1)
			default:
				v["state"] = mode
			}
			m.mu.Unlock()
			w := syncRequest(h, "POST", path, `{"intent":"overview_pull"}`, true)
			if w.Code != 200 || calls != 0 || !strings.Contains(w.Body.String(), "not_eligible") {
				t.Fatalf("unexpected request %d calls%d", w.Code, calls)
			}
		})
	}
}

func TestChargeSettingsSyncRejectsNon204AndNeverFollowsRedirect(t *testing.T) {
	for _, code := range []int{200, 302, 401, 403, 500} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			calls := 0
			h, _ := syncFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/wake_up")
				w.WriteHeader(code)
			})
			w := syncRequest(h, "POST", "/api/companion/v1/cars/17/charging-settings-sync", `{"intent":"overview_pull"}`, true)
			if w.Code != 502 || calls != 1 {
				t.Fatalf("code%d calls%d", w.Code, calls)
			}
		})
	}
}

func TestChargeSettingsSyncCancelledContextDoesNotReachLogger(t *testing.T) {
	calls := 0
	h, _ := syncFixture(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(204) })
	r := httptest.NewRequest("POST", "/api/companion/v1/cars/17/charging-settings-sync", strings.NewReader(`{"intent":"overview_pull"}`))
	r.Header.Set("Authorization", "Bearer synthetic-owner")
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	h.ServeHTTP(httptest.NewRecorder(), r.WithContext(ctx))
	if calls != 0 {
		t.Fatal("cancelled request reached logger")
	}
}

func TestChargeSettingsSyncTimeoutHasNoRetryAndKeepsCooldown(t *testing.T) {
	var calls atomic.Int32
	h, _ := syncFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	})
	chargeSettingsClient.Timeout = 20 * time.Millisecond
	path := "/api/companion/v1/cars/17/charging-settings-sync"
	started := time.Now()
	w := syncRequest(h, "POST", path, `{"intent":"overview_pull"}`, true)
	if w.Code != 502 || time.Since(started) > time.Second || calls.Load() != 1 {
		t.Fatalf("timeout result=%d calls=%d elapsed=%s", w.Code, calls.Load(), time.Since(started))
	}
	if again := syncRequest(h, "POST", path, `{"intent":"charging_popup"}`, true); again.Code != 429 || calls.Load() != 1 {
		t.Fatalf("timeout retried logger: status=%d calls=%d", again.Code, calls.Load())
	}
}

func TestChargeSettingsSyncGateConcurrentAndCarIsolation(t *testing.T) {
	g := &chargeSettingsSyncGate{}
	now := time.Now()
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if g.reserve(17, now) {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || !g.reserve(18, now) {
		t.Fatal("singleflight or car isolation failed")
	}
	g.finish(17)
	if g.reserve(17, now.Add(-time.Hour)) || g.reserve(17, now.Add(299*time.Second)) || !g.reserve(17, now.Add(300*time.Second)) {
		t.Fatal("cooldown clock boundary failed")
	}
}

func TestChargeSettingsResumeURLRejectsCredentialsAndKeepsExactOperation(t *testing.T) {
	for _, raw := range []string{"file:///tmp/a", "https://user:secret@example.invalid", "http://example.invalid/%2fsettings"} {
		if _, err := chargeSettingsResumeURL(raw, 17); err == nil {
			t.Errorf("allowed unsafe source %s", raw)
		}
	}
	u, err := chargeSettingsResumeURL("http://teslamate:4000/settings?x=1#fragment", 17)
	if err != nil || u.String() != "http://teslamate:4000/api/car/17/logging/resume" {
		t.Fatal("wrong fixed URL")
	}
}
