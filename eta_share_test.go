package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type etaFixture struct {
	svc *etaShareService
	nav etaNavigationView
	now time.Time
}

func newETAFixture() *etaFixture {
	f := &etaFixture{now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	km, mins, batt := 12.34, 18, 61
	f.nav = etaNavigationView{Active: true, SessionID: "s1", Destination: "Office", RemainingDistanceKM: &km,
		RemainingMinutes: &mins, ArrivalBatteryLevel: &batt, ObservedAt: f.now}
	f.svc = newETAShareService()
	f.svc.now = func() time.Time { return f.now }
	f.svc.navReader = func(int) (etaNavigationView, bool) { return f.nav, true }
	f.svc.positionReader = func(int) (etaSharePosition, bool) {
		return etaSharePosition{Latitude: 25.033964, Longitude: 121.564468, ObservedAt: f.now}, true
	}
	return f
}

func ownerRequest(method, target, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	return r.WithContext(context.WithValue(r.Context(), ownerAuthenticationContextKey{}, true))
}

func (f *etaFixture) create(t *testing.T, body string) (int, map[string]any) {
	w := httptest.NewRecorder()
	f.svc.handleOwnerCollection(w, ownerRequest(http.MethodPost, "/api/v1/eta-shares", body))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func (f *etaFixture) public(t *testing.T, token string) (int, map[string]any) {
	w := httptest.NewRecorder()
	f.svc.handlePublic(w, httptest.NewRequest(http.MethodGet, etaSharePublicPathPrefix+token+".json", nil))
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestETAShareLifecycleCreateReadRevoke(t *testing.T) {
	f := newETAFixture()
	code, created := f.create(t, `{"car_id":1,"ttl_minutes":90}`)
	if code != http.StatusCreated {
		t.Fatalf("create = %d %v", code, created)
	}
	token := created["token"].(string)
	if !etaShareTokenPattern.MatchString(token) {
		t.Fatalf("token format %q", token)
	}
	code, body := f.public(t, token)
	if code != http.StatusOK || body["destination"] != "Office" || body["remaining_minutes"].(float64) != 18 {
		t.Fatalf("public = %d %v", code, body)
	}
	if _, has := body["position"]; has {
		t.Fatal("position must be opt-in (default off)")
	}
	w := httptest.NewRecorder()
	f.svc.handleOwnerItem(w, ownerRequest(http.MethodPost, "/api/v1/eta-shares/"+created["id"].(string)+"/revoke", ""))
	if w.Code != http.StatusOK {
		t.Fatalf("revoke = %d", w.Code)
	}
	if code, _ := f.public(t, token); code != http.StatusGone {
		t.Fatalf("after revoke = %d", code)
	}
}

func TestETAShareRejectsUnauthorizedAndOverlongTTLAndNoNavigation(t *testing.T) {
	f := newETAFixture()
	w := httptest.NewRecorder()
	f.svc.handleOwnerCollection(w, httptest.NewRequest(http.MethodPost, "/api/v1/eta-shares", strings.NewReader(`{"car_id":1}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d", w.Code)
	}
	if code, _ := f.create(t, `{"car_id":1,"ttl_minutes":241}`); code != http.StatusBadRequest {
		t.Fatalf("ttl > 4h = %d", code)
	}
	f.nav.Active = false
	if code, _ := f.create(t, `{"car_id":1}`); code != http.StatusConflict {
		t.Fatalf("no navigation = %d", code)
	}
}

func TestETAShareExpiresOnArrivalSessionChangeAndTTL(t *testing.T) {
	f := newETAFixture()
	_, a := f.create(t, `{"car_id":1,"ttl_minutes":30}`)
	f.nav.SessionID = "s2" // arrived / new leg
	if code, _ := f.public(t, a["token"].(string)); code != http.StatusGone {
		t.Fatalf("session change = %d", code)
	}
	f.nav.SessionID = "s2"
	_, b := f.create(t, `{"car_id":1,"ttl_minutes":30}`)
	f.now = f.now.Add(31 * time.Minute)
	f.nav.ObservedAt = f.now
	if code, _ := f.public(t, b["token"].(string)); code != http.StatusGone {
		t.Fatalf("ttl expiry = %d", code)
	}
}

func TestETASharePositionOptInIsCoarsened(t *testing.T) {
	f := newETAFixture()
	_, c := f.create(t, `{"car_id":1,"include_position":true}`)
	_, body := f.public(t, c["token"].(string))
	pos, ok := body["position"].(map[string]any)
	if !ok || pos["latitude"].(float64) != 25.034 || pos["longitude"].(float64) != 121.564 {
		t.Fatalf("position = %v", body["position"])
	}
}

func TestETASharePublicRateLimitAndUnknownToken(t *testing.T) {
	f := newETAFixture()
	_, c := f.create(t, `{"car_id":1}`)
	token := c["token"].(string)
	for i := 0; i < etaSharePublicPerMinute; i++ {
		if code, _ := f.public(t, token); code != http.StatusOK {
			t.Fatalf("request %d = %d", i, code)
		}
	}
	if code, _ := f.public(t, token); code != http.StatusTooManyRequests {
		t.Fatalf("rate limit = %d", code)
	}
	if code, _ := f.public(t, strings.Repeat("A", 43)); code != http.StatusGone {
		t.Fatalf("unknown token = %d", code)
	}
	if code, _ := f.public(t, "short"); code != http.StatusNotFound {
		t.Fatalf("malformed token = %d", code)
	}
}

func TestETAShareCreateRateLimitAndActiveCap(t *testing.T) {
	f := newETAFixture()
	for i := 0; i < etaShareMaxActivePerCar; i++ {
		if code, _ := f.create(t, `{"car_id":1}`); code != http.StatusCreated {
			t.Fatalf("create %d = %d", i, code)
		}
	}
	if code, _ := f.create(t, `{"car_id":1}`); code != http.StatusTooManyRequests {
		t.Fatalf("active cap = %d", code)
	}
}

func TestETAShareDisabledByDefault(t *testing.T) {
	t.Setenv("ETA_SHARE_ENABLED", "")
	if etaShareEnabled() {
		t.Fatal("must default off")
	}
	if contains(appendETAShareCapability(nil), "eta_share_v1") {
		t.Fatal("capability must be hidden when off")
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
