package main

// Short-lived live ETA share links for a group drive.
//
// Disabled unless ETA_SHARE_ENABLED=1 (or true). Owner endpoints require normal
// Companion authorization; the public share path only needs the random token.
// Shares live in memory only (a restart revokes everything), last at most 4 h,
// end automatically when the navigation session ends or changes, and can be
// revoked. Live position is opt-in per share and coarsened to ~110 m.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	etaShareMaxTTL            = 4 * time.Hour
	etaShareDefaultTTL        = 1 * time.Hour
	etaShareMaxActivePerCar   = 5
	etaShareCreatePerHour     = 10
	etaSharePublicPerMinute   = 30
	etaSharePublicPathPrefix  = "/share/v1/eta/"
	etaSharePositionMaxAge    = 10 * time.Minute
	etaSharePositionPrecision = 1000.0 // 3 decimals ≈ 110 m
)

var etaShareTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

type etaNavigationView struct {
	Active              bool
	SessionID           string
	Destination         string
	RemainingDistanceKM *float64
	RemainingMinutes    *int
	ArrivalBatteryLevel *int
	ObservedAt          time.Time
}

type etaSharePosition struct {
	Latitude   float64
	Longitude  float64
	ObservedAt time.Time
}

type etaShare struct {
	ID              string
	tokenHash       [32]byte
	CarID           int
	SessionID       string
	CreatedAt       time.Time
	ExpiresAt       time.Time
	IncludePosition bool
	RevokedAt       *time.Time
	EndedAt         *time.Time
}

func (s *etaShare) activeAt(now time.Time) bool {
	return s.RevokedAt == nil && s.EndedAt == nil && now.Before(s.ExpiresAt)
}

type etaShareService struct {
	mu             sync.Mutex
	shares         map[string]*etaShare // by ID
	createLog      map[int][]time.Time
	publicLog      map[string][]time.Time
	now            func() time.Time
	navReader      func(carID int) (etaNavigationView, bool)
	positionReader func(carID int) (etaSharePosition, bool)
}

var etaShares *etaShareService

func etaShareEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(getenv("ETA_SHARE_ENABLED", "0"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func newETAShareService() *etaShareService {
	return &etaShareService{
		shares:         map[string]*etaShare{},
		createLog:      map[int][]time.Time{},
		publicLog:      map[string][]time.Time{},
		now:            func() time.Time { return time.Now().UTC() },
		navReader:      navigationViewForETAShare,
		positionReader: latestPositionForETAShare,
	}
}

func registerETAShare(mux *http.ServeMux) {
	if !etaShareEnabled() {
		return
	}
	etaShares = newETAShareService()
	mux.HandleFunc("/api/v1/eta-shares", etaShares.handleOwnerCollection)
	mux.HandleFunc("/api/v1/eta-shares/", etaShares.handleOwnerItem)
	mux.HandleFunc(etaSharePublicPathPrefix, etaShares.handlePublic)
}

func randomETAToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func randomETAShareID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func pruneTimes(times []time.Time, cutoff time.Time) []time.Time {
	out := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			out = append(out, t)
		}
	}
	return out
}

type etaShareCreateRequest struct {
	CarID           int  `json:"car_id"`
	TTLMinutes      int  `json:"ttl_minutes,omitempty"`
	IncludePosition bool `json:"include_position,omitempty"`
}

func (s *etaShareService) shareJSON(share *etaShare, now time.Time) map[string]any {
	status := "active"
	switch {
	case share.RevokedAt != nil:
		status = "revoked"
	case share.EndedAt != nil:
		status = "ended"
	case !now.Before(share.ExpiresAt):
		status = "expired"
	}
	return map[string]any{
		"id": share.ID, "car_id": share.CarID, "status": status,
		"created_at": share.CreatedAt.Format(time.RFC3339), "expires_at": share.ExpiresAt.Format(time.RFC3339),
		"include_position": share.IncludePosition,
	}
}

func (s *etaShareService) handleOwnerCollection(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	if !authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}
	switch r.Method {
	case http.MethodGet:
		carID, _ := strconv.Atoi(r.URL.Query().Get("car_id"))
		now := s.now()
		s.mu.Lock()
		s.reconcileLocked(now)
		items := []map[string]any{}
		for _, share := range s.shares {
			if carID == 0 || share.CarID == carID {
				items = append(items, s.shareJSON(share, now))
			}
		}
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"shares": items})
	case http.MethodPost:
		s.create(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *etaShareService) create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	defer r.Body.Close()
	var body etaShareCreateRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || body.CarID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_body"})
		return
	}
	ttl := etaShareDefaultTTL
	if body.TTLMinutes != 0 {
		if body.TTLMinutes < 5 || time.Duration(body.TTLMinutes)*time.Minute > etaShareMaxTTL {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_ttl"})
			return
		}
		ttl = time.Duration(body.TTLMinutes) * time.Minute
	}
	nav, ok := s.navReader(body.CarID)
	if !ok || !nav.Active || nav.SessionID == "" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "no_active_navigation"})
		return
	}
	now := s.now()
	token, err := randomETAToken()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token_failed"})
		return
	}
	id, err := randomETAShareID()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "token_failed"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reconcileLocked(now)
	s.createLog[body.CarID] = pruneTimes(s.createLog[body.CarID], now.Add(-time.Hour))
	if len(s.createLog[body.CarID]) >= etaShareCreatePerHour {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}
	active := 0
	for _, share := range s.shares {
		if share.CarID == body.CarID && share.activeAt(now) {
			active++
		}
	}
	if active >= etaShareMaxActivePerCar {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too_many_active_shares"})
		return
	}
	share := &etaShare{ID: id, tokenHash: sha256.Sum256([]byte(token)), CarID: body.CarID, SessionID: nav.SessionID,
		CreatedAt: now, ExpiresAt: now.Add(ttl), IncludePosition: body.IncludePosition}
	s.shares[id] = share
	s.createLog[body.CarID] = append(s.createLog[body.CarID], now)
	payload := s.shareJSON(share, now)
	payload["token"] = token
	payload["path"] = etaSharePublicPathPrefix + token
	writeJSON(w, http.StatusCreated, payload)
}

func (s *etaShareService) handleOwnerItem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	if !authorized(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Unauthorized"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/eta-shares/")
	id, action, _ := strings.Cut(rest, "/")
	if r.Method != http.MethodPost || action != "revoke" || id == "" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	share := s.shares[id]
	if share == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	if share.RevokedAt == nil {
		share.RevokedAt = &now
	}
	writeJSON(w, http.StatusOK, s.shareJSON(share, now))
}

// reconcileLocked ends shares whose navigation session ended/changed and
// drops records 24 h after they stopped being active.
func (s *etaShareService) reconcileLocked(now time.Time) {
	for id, share := range s.shares {
		if share.activeAt(now) {
			if nav, ok := s.navReader(share.CarID); !ok || !nav.Active || nav.SessionID != share.SessionID {
				share.EndedAt = &now
			}
		}
		stoppedAt := share.ExpiresAt
		if share.RevokedAt != nil && share.RevokedAt.Before(stoppedAt) {
			stoppedAt = *share.RevokedAt
		}
		if share.EndedAt != nil && share.EndedAt.Before(stoppedAt) {
			stoppedAt = *share.EndedAt
		}
		if now.Sub(stoppedAt) > 24*time.Hour {
			delete(s.shares, id)
		}
	}
}

func (s *etaShareService) lookupLocked(token string) *etaShare {
	hash := sha256.Sum256([]byte(token))
	var found *etaShare
	for _, share := range s.shares {
		if subtle.ConstantTimeCompare(share.tokenHash[:], hash[:]) == 1 {
			found = share
		}
	}
	return found
}

func publicRateKey(r *http.Request, token string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	sum := sha256.Sum256([]byte(token))
	return host + "|" + hex.EncodeToString(sum[:6])
}

func (s *etaShareService) handlePublic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, etaSharePublicPathPrefix)
	wantsJSON := strings.HasSuffix(token, ".json")
	token = strings.TrimSuffix(token, ".json")
	if !etaShareTokenPattern.MatchString(token) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	now := s.now()
	s.mu.Lock()
	key := publicRateKey(r, token)
	s.publicLog[key] = pruneTimes(s.publicLog[key], now.Add(-time.Minute))
	if len(s.publicLog[key]) >= etaSharePublicPerMinute {
		s.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
		return
	}
	s.publicLog[key] = append(s.publicLog[key], now)
	s.reconcileLocked(now)
	share := s.lookupLocked(token)
	var snapshot etaShare
	active := share != nil && share.activeAt(now)
	if share != nil {
		snapshot = *share
	}
	s.mu.Unlock()
	if !active {
		writeJSON(w, http.StatusGone, map[string]string{"error": "expired"})
		return
	}
	nav, ok := s.navReader(snapshot.CarID)
	if !ok || !nav.Active || nav.SessionID != snapshot.SessionID {
		writeJSON(w, http.StatusGone, map[string]string{"error": "expired"})
		return
	}
	payload := map[string]any{
		"destination": nav.Destination,
		"updated_at":  nav.ObservedAt.UTC().Format(time.RFC3339),
		"expires_at":  snapshot.ExpiresAt.Format(time.RFC3339),
	}
	if nav.RemainingDistanceKM != nil {
		payload["remaining_distance_km"] = math.Round(*nav.RemainingDistanceKM*10) / 10
	}
	if nav.RemainingMinutes != nil {
		payload["remaining_minutes"] = *nav.RemainingMinutes
		payload["estimated_arrival_at"] = nav.ObservedAt.Add(time.Duration(*nav.RemainingMinutes) * time.Minute).UTC().Format(time.RFC3339)
	}
	if nav.ArrivalBatteryLevel != nil {
		payload["arrival_battery_level"] = *nav.ArrivalBatteryLevel
	}
	if snapshot.IncludePosition && s.positionReader != nil {
		if pos, ok := s.positionReader(snapshot.CarID); ok && now.Sub(pos.ObservedAt) <= etaSharePositionMaxAge {
			payload["position"] = map[string]any{
				"latitude":    math.Round(pos.Latitude*etaSharePositionPrecision) / etaSharePositionPrecision,
				"longitude":   math.Round(pos.Longitude*etaSharePositionPrecision) / etaSharePositionPrecision,
				"observed_at": pos.ObservedAt.UTC().Format(time.RFC3339),
			}
		}
	}
	if wantsJSON || strings.Contains(r.Header.Get("Accept"), "application/json") {
		writeJSON(w, http.StatusOK, payload)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	eta := "—"
	if v, ok := payload["estimated_arrival_at"].(string); ok {
		eta = v
	}
	remaining := "—"
	if v, ok := payload["remaining_distance_km"].(float64); ok {
		remaining = fmt.Sprintf("%.1f km", v)
	}
	minutes := "—"
	if nav.RemainingMinutes != nil {
		minutes = fmt.Sprintf("%d min", *nav.RemainingMinutes)
	}
	_, _ = fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="refresh" content="30"><title>My T ETA</title>
<style>body{font-family:-apple-system,system-ui,sans-serif;background:#000;color:#fff;margin:0;padding:24px}h1{font-size:18px;color:#9aa}p{font-size:28px;margin:8px 0}small{color:#889}</style></head>
<body><h1>%s</h1><p>ETA <time datetime="%s">%s</time></p><p>%s · %s</p><small>Updated %s · link expires %s</small></body></html>`,
		html.EscapeString(nav.Destination), html.EscapeString(eta), html.EscapeString(eta),
		html.EscapeString(remaining), html.EscapeString(minutes),
		html.EscapeString(payload["updated_at"].(string)), html.EscapeString(snapshot.ExpiresAt.Format(time.RFC3339)))
}

func navigationViewForETAShare(carID int) (etaNavigationView, bool) {
	if navigationPush == nil {
		return etaNavigationView{}, false
	}
	m := navigationPush
	m.mu.Lock()
	defer m.mu.Unlock()
	state, ok := m.store.Cars[carID]
	if !ok {
		return etaNavigationView{}, false
	}
	observed, _ := time.Parse(time.RFC3339, state.LastObservedAt)
	return etaNavigationView{
		Active: state.Active, SessionID: state.SessionID, Destination: state.Destination,
		RemainingDistanceKM: cloneFloat(state.RemainingDistanceKM), RemainingMinutes: cloneInt(state.RemainingMinutes),
		ArrivalBatteryLevel: cloneInt(state.ArrivalBatteryLevel), ObservedAt: observed,
	}, true
}

func latestPositionForETAShare(carID int) (etaSharePosition, bool) {
	if db == nil {
		return etaSharePosition{}, false
	}
	var pos etaSharePosition
	err := db.QueryRow(`SELECT latitude::float8, longitude::float8, date FROM positions WHERE car_id = $1 ORDER BY id DESC LIMIT 1`, carID).
		Scan(&pos.Latitude, &pos.Longitude, &pos.ObservedAt)
	if err != nil {
		return etaSharePosition{}, false
	}
	return pos, true
}

func appendETAShareCapability(list []string) []string {
	if etaShareEnabled() {
		return append(list, "eta_share_v1")
	}
	return list
}
