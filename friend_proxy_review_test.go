package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestFriendInitializationFailureHasExplicitIsolatedGuestDenial(t *testing.T) {
	mux := http.NewServeMux()
	ownerCalls, fallbackCalls := 0, 0
	registerUnavailableFriendHandlers(mux, func(r *http.Request) bool {
		ownerCalls++
		return r.Header.Get("Authorization") == "Bearer synthetic-owner"
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { fallbackCalls++; w.WriteHeader(599) })
	for _, method := range []string{"GET", "POST"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "https://share.example/friend/v1/nonce", strings.NewReader("{}"))
		mux.ServeHTTP(w, r)
		if w.Code != 503 || w.Header().Get("Content-Type") != "application/json" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || strings.TrimSpace(w.Body.String()) != `{"error":"unavailable"}` {
			t.Fatalf("explicit guest deny missing: %d", w.Code)
		}
	}
	if ownerCalls != 0 || fallbackCalls != 0 {
		t.Fatal("failed guest init reached owner probe or catchall")
	}
	for _, authorized := range []bool{false, true} {
		r := httptest.NewRequest("GET", "https://owner.example/api/v1/friend-together/status", nil)
		if authorized {
			r.Header.Set("Authorization", "Bearer synthetic-owner")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		want := 401
		if authorized {
			want = 503
		}
		if w.Code != want {
			t.Fatal("owner failure handler lost authentication")
		}
	}
}

func TestFriendOwnerProxyMatchersPreserveGuestSeparation(t *testing.T) {
	read := func(name string) string {
		t.Helper()
		bytes, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return string(bytes)
	}
	caddy := read("Caddyfile.snippet")
	if !strings.Contains(caddy, "@my_t_friend_owner path /api/v1/friend-together/*") || !strings.Contains(caddy, "handle @my_t_friend_owner {\n\treverse_proxy 127.0.0.1:8083\n}") {
		t.Fatal("Caddy owner route missing")
	}
	lan := read("Caddyfile.lan.example")
	line := regexp.MustCompile(`path_regexp my_t_companion ([^\n]+)`).FindStringSubmatch(lan)
	if len(line) != 2 {
		t.Fatal("LAN matcher not found")
	}
	matcher, err := regexp.Compile(line[1])
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/friend-together/status", "/api/v1/friend-together/invitations", "/api/v1/notifications/status", "/api/v1/cars/1/states"} {
		if !matcher.MatchString(path) {
			t.Errorf("owner matcher lost %s", path)
		}
	}
	for _, path := range []string{"/friend/v1/nonce", "/api/v1/friend-together-evil/status", "/api/v1/cars/1/vehicle_data"} {
		if matcher.MatchString(path) {
			t.Errorf("owner matcher widened to %s", path)
		}
	}
	nginx := read("nginx.snippet.conf")
	block := regexp.MustCompile(`location \^~ /api/v1/friend-together/ \{([^}]+)\}`).FindStringSubmatch(nginx)
	if len(block) != 2 || !strings.Contains(block[1], "proxy_pass http://127.0.0.1:8083;") || !strings.Contains(block[1], "proxy_set_header Authorization $http_authorization;") || strings.Contains(nginx, "location ^~ /friend/v1/") {
		t.Fatal("nginx owner routing/auth separation")
	}
	for _, name := range []string{"SECURITY.md", "SECURITY.zh-Hant.md", "SECURITY.zh-Hans.md"} {
		policy := read(name)
		if !strings.Contains(policy, "/api/v1/friend-together/*") || !strings.Contains(policy, "/friend/v1/*") || !strings.Contains(policy, "DPoP") {
			t.Errorf("missing explicit auth split: %s", name)
		}
	}
}
