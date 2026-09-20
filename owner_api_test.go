package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCanonicalCapabilitiesAndLegacyShareContract(t *testing.T) {
	oldToken, oldProbe := apiToken, authProbeURL
	apiToken, authProbeURL = "fixture-owner", ""
	t.Cleanup(func() { apiToken, authProbeURL = oldToken, oldProbe })
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/capabilities", handleCapabilities)
	mux.HandleFunc("/", handleStates)
	handler := withHeaders(withOwnerAPI(mux))
	for _, prefix := range []string{"/api/v1/", ownerAPIPrefix} {
		request := httptest.NewRequest("GET", prefix+"capabilities", nil)
		request.Header.Set("Authorization", "Bearer fixture-owner")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		var body struct {
			Service string `json:"service"`
			Owner   struct {
				Prefix  string `json:"prefix"`
				Version int    `json:"protocol_version"`
			} `json:"owner_api"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Service != "my-t-companion" || body.Owner.Prefix != ownerAPIPrefix || body.Owner.Version != 1 {
			t.Fatalf("invalid capabilities contract for %s: %d", prefix, response.Code)
		}
		for _, path := range []string{"capabilities", "cars/1/states", "cars/1/tire-pressure-history"} {
			request := httptest.NewRequest("DELETE", prefix+path, nil)
			request.Header.Set("Authorization", "Bearer fixture-owner")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != 405 {
				t.Errorf("%s%s DELETE=%d", prefix, path, response.Code)
			}
		}
	}
}

func TestCanonicalOwnerBoundary(t *testing.T) {
	oldToken, oldProbe := apiToken, authProbeURL
	apiToken, authProbeURL = "fixture-owner", ""
	t.Cleanup(func() { apiToken, authProbeURL = oldToken, oldProbe })
	for _, item := range []struct {
		path       string
		token      bool
		code       int
		translated string
	}{
		{"/api/companion/v1/capabilities", true, 204, "/api/v1/capabilities"},
		{"/api/companion/v1/cars/2/tire-pressure-history?limit=200", true, 204, "/api/v1/cars/2/tire-pressure-history"},
		{"/api/companion/v1/notifications/software-update/pair", true, 204, "/api/v1/notifications/software-update/pair"},
		{"/api/companion/v1/friend-together/invitations/abc/cancel", true, 204, "/api/v1/friend-together/invitations/abc/cancel"},
		{"/api/companion/v1/friend-together/requests/abc/approve", true, 204, "/api/v1/friend-together/requests/abc/approve"},
		{"/api/companion/v1/capabilities", false, 401, ""},
		{"/api/companion/v1/cars/2/states", false, 401, ""},
		{"/api/companion/v2/capabilities", true, 404, ""},
		{"/api/companion/v1/api/ping", true, 404, ""},
		{"/api/companion/v1/healthz", true, 404, ""},
		{"/api/companion/v1/friend/v1/status", true, 404, ""},
		{"/api/companion/v1/cars/1/commands/unlock", true, 404, ""},
		{"/api/companion/v1//capabilities", true, 404, ""},
		{"/api/companion/v1/../capabilities", true, 404, ""},
		{"/api/companion/v1/%63apabilities", true, 404, ""},
		{"/api/companion-other/v1/capabilities", true, 404, ""},
		{"/friend/v1/status", false, 204, "/friend/v1/status"},
		{"/api/ping", false, 204, "/api/ping"},
	} {
		t.Run(item.path, func(t *testing.T) {
			called := false
			h := withOwnerAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				if r.URL.Path != item.translated {
					t.Errorf("path = %q", r.URL.Path)
				}
				if item.token && !authorized(r) {
					t.Error("verified owner not passed to shared handler")
				}
				w.WriteHeader(204)
			}))
			r := httptest.NewRequest("GET", item.path, nil)
			if item.token {
				r.Header.Set("Authorization", "Bearer fixture-owner")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != item.code {
				t.Fatalf("status=%d want=%d", w.Code, item.code)
			}
			if called != (item.translated != "") {
				t.Fatal("unexpected downstream dispatch")
			}
		})
	}
}

func TestCanonicalOwnerPreservesMethodQueryAndUsesOneAuthProbe(t *testing.T) {
	oldToken, oldProbe, oldClient := apiToken, authProbeURL, authClient
	defer func() { apiToken, authProbeURL, authClient = oldToken, oldProbe, oldClient }()
	probes := 0
	probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes++
		if r.URL.Path != "/api/ping" {
			t.Error("probe must not recurse into Companion")
		}
		w.WriteHeader(200)
	}))
	defer probe.Close()
	apiToken, authProbeURL, authClient = "", probe.URL+"/api/ping", probe.Client()
	h := withOwnerAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) || r.Method != "POST" || r.URL.RawQuery != "x=1" {
			t.Error("request altered")
		}
		w.WriteHeader(204)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/companion/v1/notifications/software-update/pair?x=1", nil))
	if probes != 1 {
		t.Fatalf("auth probes=%d want=1", probes)
	}
}

func TestCanonicalOwnerRejectsSelfAuthenticationProbe(t *testing.T) {
	oldToken, oldProbe, oldClient := apiToken, authProbeURL, authClient
	defer func() { apiToken, authProbeURL, authClient = oldToken, oldProbe, oldClient }()
	probe := httptest.NewServer(withHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })))
	defer probe.Close()
	apiToken, authProbeURL, authClient = "", probe.URL+"/api/ping", probe.Client()
	if authorized(httptest.NewRequest("GET", "/api/companion/v1/capabilities", nil)) {
		t.Fatal("Companion's public health response must not authenticate an owner")
	}
	authProbeURL = probe.URL + "/api/companion/v1/capabilities"
	if authorized(httptest.NewRequest("GET", "/api/companion/v1/capabilities", nil)) {
		t.Fatal("canonical API must not recursively authenticate against itself")
	}
}
