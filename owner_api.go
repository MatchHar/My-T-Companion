package main

import (
	"context"
	"net/http"
	"path"
	"regexp"
	"strings"
)

// The deployment boundary never changes when a resource is added. Guest sharing
// and private operational probes deliberately remain outside this owner boundary.
const ownerAPIPrefix = "/api/companion/v1/"

type ownerAuthenticationContextKey struct{}

var ownerResourcePath = regexp.MustCompile(`^(capabilities|notifications/(software-update/(status|pair)|charging-live-activity/status|navigation-live-activity/status|lock-secure|low-battery/action)|cars/[0-9]+/(states|parking-events|companion-status|tire-pressure-history|navigation/(current-drive|push-history))|friend-together/(status|invitations|grants|invitations/[^/]+/(cancel|requests)|requests/[^/]+/(approve|reject)|grants/[^/]+/(revoke|restrict)))$`)

// The old addresses are temporary same-handler aliases during client cutover.
// Rewriting is internal only: no redirect, second HTTP request, new authority,
// arbitrary upstream or duplicate business implementation is introduced.
func withOwnerAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/companion") {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !strings.HasPrefix(r.URL.Path, ownerAPIPrefix) || r.URL.RawPath != "" || path.Clean(r.URL.Path) != r.URL.Path {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		if !authorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
			return
		}
		resource := strings.TrimPrefix(r.URL.Path, ownerAPIPrefix)
		if !ownerResourcePath.MatchString(resource) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
			return
		}
		request := r.Clone(context.WithValue(r.Context(), ownerAuthenticationContextKey{}, true))
		request.URL.Path = "/api/v1/" + resource
		request.RequestURI = request.URL.RequestURI()
		next.ServeHTTP(w, request)
	})
}
