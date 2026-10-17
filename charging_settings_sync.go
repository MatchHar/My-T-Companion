package main

// A single, owner-only logging resume, never a general vehicle-command proxy.
import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const chargeSettingsSyncCooldown = 5 * time.Minute

type chargeSettingsSyncGate struct {
	mu       sync.Mutex
	attempts map[int]time.Time
	active   map[int]bool
}

func (g *chargeSettingsSyncGate) reserve(car int, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.active[car] {
		return false
	}
	if last, ok := g.attempts[car]; ok && now.Sub(last) < chargeSettingsSyncCooldown {
		return false
	}
	if g.attempts == nil {
		g.attempts = map[int]time.Time{}
		g.active = map[int]bool{}
	}
	for id, at := range g.attempts {
		if !g.active[id] && now.Sub(at) >= chargeSettingsSyncCooldown {
			delete(g.attempts, id)
		}
	}
	if len(g.attempts) >= mqttMaximumCars {
		return false
	}
	g.attempts[car], g.active[car] = now, true
	return true
}
func (g *chargeSettingsSyncGate) finish(car int) { g.mu.Lock(); delete(g.active, car); g.mu.Unlock() }

var chargeSettingsGate = &chargeSettingsSyncGate{}
var chargeSettingsClient = &http.Client{Timeout: 6 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// Read the monitor's current-connection baseline atomically, not persisted values
// from before a reconnect. Scalar clocks remain receipt clocks, not observations.
func (m *parkingEventMonitor) permitsChargeSettingsSync(car int, now time.Time) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.connected {
		return false
	}
	for _, field := range []string{"state", "plugged_in", "charging_state", "scheduled_charging_start_time"} {
		if !m.baselines[car][field] {
			return false
		}
	}
	v := m.store.Cars[car].Values
	if v["state"] != "suspended" || v["plugged_in"] != "true" {
		return false
	}
	if v["charging_state"] != "stopped" && v["charging_state"] != "paused" && v["charging_state"] != "nopower" {
		return false
	}
	start, err := time.Parse(time.RFC3339Nano, v["scheduled_charging_start_time"])
	return err == nil && start.After(now) && start.Before(now.Add(8*24*time.Hour))
}

func chargeSettingsResumeURL(raw string, car int) (*url.URL, error) {
	u, err := teslaMateSettingsURL(raw)
	if err != nil || car <= 0 || u.User != nil || u.RawPath != "" {
		return nil, fmt.Errorf("invalid logging source")
	}
	u.Path = strings.TrimSuffix(u.Path, "/settings") + "/api/car/" + strconv.Itoa(car) + "/logging/resume"
	u.RawQuery, u.Fragment = "", ""
	return u, nil
}

func handleChargeSettingsSync(w http.ResponseWriter, r *http.Request, carText string) {
	// Only the canonical owner namespace can reach this write operation.
	verified, _ := r.Context().Value(ownerAuthenticationContextKey{}).(bool)
	if !verified {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	car, err := strconv.Atoi(carText)
	if err != nil || car <= 0 || strconv.Itoa(car) != carText || r.URL.RawQuery != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	var body struct {
		Intent string `json:"intent"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		(body.Intent != "charging_popup" && body.Intent != "overview_pull") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_intent"})
		return
	}
	eligible := func() bool {
		return mqttSourceRuntime.Load() != nil && mqttCarEligible(car) && parkingEvents != nil && parkingEvents.permitsChargeSettingsSync(car, time.Now().UTC())
	}
	respond := func(code int, outcome string) {
		writeJSON(w, code, map[string]any{"data": map[string]any{"car_id": car, "outcome": outcome, "retry_after_seconds": 300}})
	}
	if !eligible() {
		respond(http.StatusOK, "not_eligible")
		return
	}
	if !chargeSettingsGate.reserve(car, time.Now().UTC()) {
		w.Header().Set("Retry-After", "300")
		respond(http.StatusTooManyRequests, "cooldown")
		return
	}
	defer chargeSettingsGate.finish(car)
	u, err := chargeSettingsResumeURL(os.Getenv("TESLAMATE_WEB_URL"), car)
	if err != nil {
		respond(http.StatusServiceUnavailable, "unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	if ctx.Err() != nil || !eligible() {
		respond(http.StatusOK, "not_eligible")
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, u.String(), nil)
	if err != nil {
		respond(http.StatusServiceUnavailable, "unavailable")
		return
	}
	request.Header.Set("Accept", "application/json")
	response, err := chargeSettingsClient.Do(request)
	if err != nil {
		respond(http.StatusBadGateway, "unavailable")
		return
	}
	defer response.Body.Close()
	// TeslaMate 4.3 CarController acknowledges logging resume with 204, not
	// arbitrary HTTP 200/HTML, a redirected sign-in page or a vehicle sample.
	if response.StatusCode != http.StatusNoContent {
		respond(http.StatusBadGateway, "unavailable")
		return
	}
	respond(http.StatusAccepted, "requested") // Not a claim of new vehicle data.
}
