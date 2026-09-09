package friendtogether

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const invitationLifeMS int64 = 10 * 60 * 1000

// HTTPService owns only short-lived consent metadata. No returned telemetry is
// persisted here. It never makes requests to an invite's origin.
type HTTPService struct {
	mu                sync.Mutex
	config            HTTPConfig
	origin, host      string
	store             *Store
	state             serviceState
	nonces, proofs    map[string]int64
	returnInvitations map[string]Invitation
	healthy           bool
	windowMS          int64
	windowCount       int
	sourceSlots       chan struct{}
}
type serviceState struct {
	SchemaVersion int                          `json:"schema_version"`
	IssuerID      string                       `json:"issuer_id"`
	LastTimeMS    int64                        `json:"last_time_ms"`
	Invitations   map[string]*invitationRecord `json:"invitations"`
	Requests      map[string]*requestRecord    `json:"requests"`
	Grants        map[string]*grantRecord      `json:"grants"`
}
type invitationRecord struct {
	Envelope          Invitation `json:"envelope"` // RedemptionSecret is always empty.
	SecretHash        string     `json:"secret_hash"`
	CarID             int64      `json:"car_id"`
	ExpectedRecipient *Device    `json:"expected_recipient,omitempty"`
	Used              bool       `json:"used"`
}
type requestRecord struct {
	View         JoinView `json:"view"` // ReturnInvitation is always nil.
	InvitationID string   `json:"invitation_id"`
	TokenHash    string   `json:"token_hash"`
	GrantID      string   `json:"grant_id,omitempty"`
}
type grantRecord struct {
	View             GrantView `json:"view"`
	CarID            int64     `json:"car_id"`
	RequestID        string    `json:"request_id"`
	lastSourceReadMS int64     // Memory-only per-grant read rate limit.
}

func NewHTTPService(config HTTPConfig) (*HTTPService, error) {
	s := &HTTPService{config: config, store: NewStore(), nonces: map[string]int64{}, proofs: map[string]int64{}, returnInvitations: map[string]Invitation{}, sourceSlots: make(chan struct{}, 2)}
	if !config.Enabled {
		return s, nil
	}
	u, err := validateOrigin(config.GuestOrigin, config.AllowLoopbackHTTP)
	if err != nil || config.AuthenticateOwner == nil || config.Source == nil || !filepath.IsAbs(config.StatePath) {
		return nil, ErrInvalid
	}
	s.origin, s.host = u.String(), u.Host
	s.store.clock = s.now
	s.state = serviceState{SchemaVersion: 1, Invitations: map[string]*invitationRecord{}, Requests: map[string]*requestRecord{}, Grants: map[string]*grantRecord{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	if s.state.IssuerID == "" {
		s.state.IssuerID, err = randomID()
		if err != nil {
			return nil, err
		}
	}
	if !canonicalID(s.state.IssuerID) || s.state.SchemaVersion != 1 || s.state.LastTimeMS > s.now() {
		return nil, ErrInvalid
	}
	// A process restart invalidates all grants and pending invitations. There is
	// no safe continuation without persisted proof/counter/source watermarks.
	for _, i := range s.state.Invitations {
		i.Used = true
	}
	for _, r := range s.state.Requests {
		r.View.Status = "expired"
		r.View.ReturnInvitation = nil
	}
	for _, g := range s.state.Grants {
		if g.View.Status == "active" {
			g.View.Status = "revoked"
			g.View.Revision++
		}
	}
	s.healthy = true
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *HTTPService) now() int64 {
	if s.config.Clock != nil {
		return s.config.Clock()
	}
	return time.Now().UnixMilli()
}
func (s *HTTPService) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config.Enabled && s.healthy && s.clockHealthyLocked()
}
func (s *HTTPService) clockHealthyLocked() bool {
	now := s.now()
	if !validTime(now) || now < s.state.LastTimeMS {
		s.healthy = false
	}
	if s.healthy {
		s.state.LastTimeMS = now
	}
	return s.healthy
}

func (s *HTTPService) OwnerHandler() http.Handler { return bufferedFriendHandler(s.serveOwner) }
func (s *HTTPService) GuestHandler() http.Handler { return bufferedFriendHandler(s.serveGuest) }

// Network reads/writes must never hold the consent mutex. Input is capped before
// dispatch; output is already bounded by grant/request/trajectory limits. The
// existing HTTP server read/write deadlines still bound slow connections.
func bufferedFriendHandler(serve func(http.ResponseWriter, *http.Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		privacyHeaders(w)
		if r.Body != nil {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
			_ = r.Body.Close()
			if err != nil {
				fail(w, 400, "invalid_request")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		buffer := &friendResponseBuffer{header: make(http.Header)}
		serve(buffer, r)
		for key, values := range buffer.header {
			w.Header()[key] = values
		}
		status := buffer.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		_, _ = w.Write(buffer.body.Bytes())
	})
}

type friendResponseBuffer struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *friendResponseBuffer) Header() http.Header { return b.header }
func (b *friendResponseBuffer) WriteHeader(status int) {
	if b.status == 0 {
		b.status = status
	}
}
func (b *friendResponseBuffer) Write(data []byte) (int, error) {
	if b.status == 0 {
		b.status = 200
	}
	return b.body.Write(data)
}
func privacyHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store, private")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
}
func (s *HTTPService) serveOwner(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	if s.config.AuthenticateOwner == nil || !s.config.AuthenticateOwner(r) {
		fail(w, 401, "not_authorized")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/friend-together/")
	if path == r.URL.Path || r.URL.RawQuery != "" || r.URL.RawPath != "" {
		fail(w, 404, "not_found")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if path == "status" && r.Method == http.MethodGet {
		out := map[string]any{"schema_version": 1, "enabled": s.config.Enabled, "ready": s.config.Enabled && s.clockHealthyLocked()}
		if s.config.Enabled {
			out["issuer_id"] = s.state.IssuerID
			out["guest_origin"] = s.origin
			if !s.healthy {
				out["reason"] = "unavailable"
			}
		} else {
			out["reason"] = "disabled"
		}
		writeResponse(w, 200, out)
		return
	}
	if !s.config.Enabled || !s.clockHealthyLocked() {
		fail(w, 503, "unavailable")
		return
	}
	s.expireLocked()
	if path == "invitations" && r.Method == http.MethodPost {
		s.createInvitationLocked(w, r)
		return
	}
	if path == "grants" && r.Method == http.MethodGet {
		out := make([]GrantView, 0, len(s.state.Grants))
		for _, g := range s.state.Grants {
			out = append(out, g.View)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ConsentStartedAtMS > out[j].ConsentStartedAtMS })
		writeResponse(w, 200, map[string]any{"grants": out})
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) != 3 || !canonicalID(parts[1]) {
		fail(w, 404, "not_found")
		return
	}
	switch parts[0] + "/" + parts[2] {
	case "invitations/cancel":
		if r.Method != http.MethodPost {
			fail(w, 405, "method_not_allowed")
			return
		}
		if emptyBody(w, r) != nil {
			fail(w, 400, "invalid_request")
			return
		}
		inv := s.state.Invitations[parts[1]]
		if inv != nil {
			inv.Used = true
		}
		// Owner authentication and healthy issuer state were checked above.
		// Already-cleaned IDs are safely terminal; still revoke any associated
		// request so cancellation remains fail-closed even with an orphan record.
		for _, req := range s.state.Requests {
			if req.InvitationID == parts[1] {
				delete(s.returnInvitations, req.View.RequestID)
				if req.GrantID != "" {
					if g := s.state.Grants[req.GrantID]; g != nil {
						if err := s.revokeLocked(g); err != nil {
							errorResponse(w, err)
							return
						}
					}
				}
				if req.View.Status == "pending" {
					req.View.Status = "rejected"
				}
			}
		}
		if s.saveLocked() != nil {
			fail(w, 503, "unavailable")
			return
		}
		writeResponse(w, 200, map[string]string{"status": "cancelled"})
	case "invitations/requests":
		if r.Method != http.MethodGet {
			fail(w, 405, "method_not_allowed")
			return
		}
		if s.state.Invitations[parts[1]] == nil {
			fail(w, 404, "not_found")
			return
		}
		views := []JoinView{}
		for _, req := range s.state.Requests {
			if req.InvitationID == parts[1] {
				v := req.View
				if ret, ok := s.returnInvitations[v.RequestID]; ok {
					copy := ret
					v.ReturnInvitation = &copy
				}
				views = append(views, v)
			}
		}
		sort.Slice(views, func(i, j int) bool { return views[i].CreatedAtMS < views[j].CreatedAtMS })
		writeResponse(w, 200, map[string]any{"requests": views})
	case "requests/approve", "requests/reject":
		if r.Method != http.MethodPost {
			fail(w, 405, "method_not_allowed")
			return
		}
		if emptyBody(w, r) != nil {
			fail(w, 400, "invalid_request")
			return
		}
		s.decideRequestLocked(w, parts[1], parts[2] == "approve")
	case "grants/revoke":
		if r.Method != http.MethodPost {
			fail(w, 405, "method_not_allowed")
			return
		}
		if emptyBody(w, r) != nil {
			fail(w, 400, "invalid_request")
			return
		}
		g := s.state.Grants[parts[1]]
		if g == nil {
			fail(w, 404, "not_found")
			return
		}
		if err := s.revokeLocked(g); err != nil {
			errorResponse(w, err)
			return
		}
		writeResponse(w, 200, map[string]any{"grant": g.View})
	case "grants/restrict":
		if r.Method != http.MethodPost {
			fail(w, 405, "method_not_allowed")
			return
		}
		s.restrictLocked(w, r, parts[1])
	default:
		fail(w, 404, "not_found")
	}
}
func (s *HTTPService) createInvitationLocked(w http.ResponseWriter, r *http.Request) {
	var req CreateInvitationRequest
	if decodeBody(w, r, &req) != nil || req.SchemaVersion != 1 || req.CarID <= 0 || !req.Permissions.Location || !validMember(req.Member) || !validDevice(req.OwnerDevice) || req.ExpectedRecipient != nil && !validDevice(*req.ExpectedRecipient) {
		fail(w, 400, "invalid_request")
		return
	}
	if req.DurationSeconds != 3600 && req.DurationSeconds != 7200 && req.DurationSeconds != 14400 && req.DurationSeconds != 28800 {
		fail(w, 400, "invalid_request")
		return
	}
	if req.SessionID != "" && !canonicalID(req.SessionID) {
		fail(w, 400, "invalid_request")
		return
	}
	if len(s.state.Invitations) >= 64 || len(s.state.Requests) >= 64 || len(s.state.Grants) >= MaxGrants {
		fail(w, 429, "capacity")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if !s.takeSourceSlotLocked() {
		fail(w, 429, "rate_limited")
		return
	}
	// A slow source must not delay owner stop, nonce issuance or other grants.
	s.mu.Unlock()
	owns, err := s.config.Source.OwnsCar(ctx, req.CarID)
	s.mu.Lock()
	<-s.sourceSlots
	if !s.clockHealthyLocked() {
		fail(w, 503, "unavailable")
		return
	}
	s.expireLocked()
	if len(s.state.Invitations) >= 64 || len(s.state.Requests) >= 64 || len(s.state.Grants) >= MaxGrants {
		fail(w, 429, "capacity")
		return
	}
	if err != nil {
		fail(w, 503, "source_unavailable")
		return
	}
	if !owns {
		fail(w, 400, "invalid_vehicle")
		return
	}
	ids := make([]string, 3)
	for i := range ids {
		ids[i], err = randomID()
		if err != nil {
			fail(w, 503, "unavailable")
			return
		}
	}
	secret, err := randomSecret()
	if err != nil {
		fail(w, 503, "unavailable")
		return
	}
	session := req.SessionID
	if session == "" {
		session = ids[2]
	}
	inv := Invitation{SchemaVersion: 1, GuestOrigin: s.origin, IssuerID: s.state.IssuerID, SessionID: session, MemberID: ids[1], InvitationID: ids[0], ExpiresAtMS: s.now() + invitationLifeMS, SharingDurationSeconds: req.DurationSeconds, Permissions: req.Permissions, Member: req.Member, OwnerDevice: req.OwnerDevice}
	s.state.Invitations[inv.InvitationID] = &invitationRecord{Envelope: inv, SecretHash: hashSecret(secret), CarID: req.CarID, ExpectedRecipient: req.ExpectedRecipient}
	if s.saveLocked() != nil {
		fail(w, 503, "unavailable")
		return
	}
	inv.RedemptionSecret = secret
	writeResponse(w, 201, inv)
}
func (s *HTTPService) decideRequestLocked(w http.ResponseWriter, id string, approve bool) {
	req := s.state.Requests[id]
	if req == nil {
		fail(w, 404, "not_found")
		return
	}
	if req.View.Status == "approved" && approve {
		g := s.state.Grants[req.GrantID]
		if g != nil && g.View.Status == "active" {
			s.approvalResponse(w, req, g)
			return
		}
		fail(w, 410, "inactive")
		return
	}
	if req.View.Status == "rejected" && !approve {
		writeResponse(w, 200, map[string]string{"status": "rejected"})
		return
	}
	if req.View.Status != "pending" || req.View.ExpiresAtMS <= s.now() {
		fail(w, 410, "inactive")
		return
	}
	if !approve {
		req.View.Status = "rejected"
		delete(s.returnInvitations, id)
		if s.saveLocked() != nil {
			fail(w, 503, "unavailable")
			return
		}
		writeResponse(w, 200, map[string]string{"status": "rejected"})
		return
	}
	inv := s.state.Invitations[req.InvitationID]
	if inv == nil {
		fail(w, 410, "inactive")
		return
	}
	grantID, err := randomID()
	if err != nil {
		fail(w, 503, "unavailable")
		return
	}
	approvedAt := s.now()
	if approvedAt >= req.View.ExpiresAtMS || approvedAt >= inv.Envelope.ExpiresAtMS {
		fail(w, 410, "inactive")
		return
	}
	source := SourceKey{SourceID: s.state.IssuerID, CarID: inv.CarID}
	binding := Binding{GrantID: grantID, IssuerID: s.state.IssuerID, SessionID: inv.Envelope.SessionID, MemberID: inv.Envelope.MemberID, RecipientID: req.View.RecipientID}
	// Capture the approval instant once: even if the clock advances while
	// creating/persisting, expiry can never exceed invitation expiry + duration.
	metadata, err := s.store.Create(OwnerPrincipal{IssuerID: s.state.IssuerID, Source: source}, GrantSpec{Binding: binding, Source: source, Permissions: inv.Envelope.Permissions.policy(), ExpiresAtMS: approvedAt + inv.Envelope.SharingDurationSeconds*1000})
	if err != nil {
		errorResponse(w, err)
		return
	}
	g := &grantRecord{View: GrantView{SchemaVersion: 1, Binding: binding, Revision: metadata.Revision, ConsentStartedAtMS: metadata.ConsentStartedAtMS, ExpiresAtMS: metadata.ExpiresAtMS, Permissions: inv.Envelope.Permissions, Member: inv.Envelope.Member, Status: "active"}, CarID: inv.CarID, RequestID: id}
	s.state.Grants[grantID] = g
	req.View.Status = "approved"
	req.GrantID = grantID
	if s.saveLocked() != nil {
		fail(w, 503, "unavailable")
		return
	}
	s.approvalResponse(w, req, g)
}
func (s *HTTPService) approvalResponse(w http.ResponseWriter, r *requestRecord, g *grantRecord) {
	out := map[string]any{"grant": g.View}
	if inv, ok := s.returnInvitations[r.View.RequestID]; ok {
		out["return_invitation"] = inv
	}
	writeResponse(w, 200, out)
}
func (s *HTTPService) restrictLocked(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		Revision    int64         `json:"revision"`
		Permissions PermissionSet `json:"permissions"`
		ExpiresAtMS int64         `json:"expires_at_ms"`
	}
	if decodeBody(w, r, &req) != nil {
		fail(w, 400, "invalid_request")
		return
	}
	g := s.state.Grants[id]
	if g == nil {
		fail(w, 404, "not_found")
		return
	}
	if g.View.Status != "active" {
		fail(w, 410, "inactive")
		return
	}
	source := SourceKey{SourceID: s.state.IssuerID, CarID: g.CarID}
	m, err := s.store.Restrict(OwnerPrincipal{IssuerID: s.state.IssuerID, Source: source}, g.View.Binding, source, req.Revision, req.Permissions.policy(), req.ExpiresAtMS)
	if err != nil {
		errorResponse(w, err)
		return
	}
	g.View.Revision = m.Revision
	g.View.Permissions = req.Permissions
	g.View.ExpiresAtMS = m.ExpiresAtMS
	if s.saveLocked() != nil {
		fail(w, 503, "unavailable")
		return
	}
	writeResponse(w, 200, map[string]any{"grant": g.View})
}
func (s *HTTPService) revokeLocked(g *grantRecord) error {
	if g.View.Status != "active" {
		return nil
	}
	source := SourceKey{SourceID: s.state.IssuerID, CarID: g.CarID}
	m, err := s.store.Revoke(OwnerPrincipal{IssuerID: s.state.IssuerID, Source: source}, g.View.Binding, source, g.View.Revision)
	if err != nil {
		s.healthy = false
		return err
	}
	g.View.Status = "revoked"
	g.View.Revision = m.Revision
	delete(s.returnInvitations, g.RequestID)
	return s.saveLocked()
}

func (s *HTTPService) serveGuest(w http.ResponseWriter, r *http.Request) {
	privacyHeaders(w)
	if !s.config.Enabled {
		fail(w, 404, "not_found")
		return
	}
	// Host and canonical path are pinned to operator configuration, not forwarded
	// headers. Proxy must preserve the original Host on the dedicated ingress.
	if !s.matchesGuestHost(r.Host) || r.URL.RawQuery != "" || r.URL.RawPath != "" || !strings.HasPrefix(r.URL.Path, "/friend/v1/") {
		fail(w, 404, "not_found")
		return
	}
	for _, h := range []string{"X-API-Token", "CF-Access-Client-Id", "CF-Access-Client-Secret", "Cookie"} {
		if r.Header.Get(h) != "" {
			fail(w, 400, "forbidden_credentials")
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.clockHealthyLocked() {
		fail(w, 503, "unavailable")
		return
	}
	s.expireLocked()
	now := s.now()
	if now-s.windowMS >= 1000 {
		s.windowMS = now
		s.windowCount = 0
	}
	s.windowCount++
	if s.windowCount > 64 {
		fail(w, 429, "rate_limited")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/friend/v1/")
	if path == "nonce" && r.Method == http.MethodPost {
		if emptyBody(w, r) != nil || r.Header.Get("Authorization") != "" || r.Header.Get("DPoP") != "" {
			fail(w, 400, "invalid_request")
			return
		}
		if len(s.nonces) >= 256 || len(s.proofs) >= 1024 {
			fail(w, 429, "rate_limited")
			return
		}
		nonce, err := randomSecret()
		if err != nil {
			fail(w, 503, "unavailable")
			return
		}
		s.nonces[nonce] = now + 60000
		writeResponse(w, 200, map[string]any{"nonce": nonce, "issuer_id": s.state.IssuerID, "server_time_ms": now, "expires_at_ms": now + 60000})
		return
	}
	if path == "redeem" && r.Method == http.MethodPost {
		s.redeemLocked(w, r)
		return
	}
	parts := strings.Split(path, "/")
	if len(parts) < 2 || !canonicalID(parts[1]) {
		fail(w, 404, "not_found")
		return
	}
	token, err := guestToken(r)
	if err != nil {
		errorResponse(w, err)
		return
	}
	key, err := s.authenticateGuestLocked(r, token)
	if err != nil {
		errorResponse(w, err)
		return
	}
	var req *requestRecord
	var g *grantRecord
	if (len(parts) == 2 && parts[0] == "requests" && r.Method == http.MethodGet) || (len(parts) == 3 && parts[0] == "requests" && parts[2] == "cancel" && r.Method == http.MethodPost) {
		req = s.state.Requests[parts[1]]
	} else if len(parts) == 3 && parts[0] == "grants" && (parts[2] == "snapshot" && r.Method == http.MethodGet || parts[2] == "leave" && r.Method == http.MethodPost) {
		g = s.state.Grants[parts[1]]
		if g != nil {
			req = s.state.Requests[g.RequestID]
		}
	} else {
		fail(w, 404, "not_found")
		return
	}
	if req == nil || !sameHash(req.TokenHash, hashSecret(token)) || thumbprint(req.View.PublicKey) != thumbprint(key) {
		fail(w, 401, "not_authorized")
		return
	}
	if len(parts) == 3 && parts[0] == "requests" && parts[2] == "cancel" {
		if emptyBody(w, r) != nil {
			fail(w, 400, "invalid_request")
			return
		}
		status := "rejected"
		if req.GrantID != "" {
			if grant := s.state.Grants[req.GrantID]; grant != nil {
				if err := s.revokeLocked(grant); err != nil {
					errorResponse(w, err)
					return
				}
				status = "revoked"
			}
		}
		req.View.Status = "rejected"
		delete(s.returnInvitations, req.View.RequestID)
		if s.saveLocked() != nil {
			fail(w, 503, "unavailable")
			return
		}
		writeResponse(w, 200, map[string]string{"status": status})
		return
	}
	if g == nil {
		out := JoinStatus{Status: req.View.Status, ConfirmationCode: req.View.ConfirmationCode, ExpiresAtMS: req.View.ExpiresAtMS}
		if req.GrantID != "" {
			if grant := s.state.Grants[req.GrantID]; grant != nil {
				copy := grant.View
				out.Grant = &copy
				out.ExpiresAtMS = copy.ExpiresAtMS
				if copy.Status != "active" {
					out.Status = copy.Status
				}
			}
		}
		writeResponse(w, 200, out)
		return
	}
	if g.View.Status != "active" {
		if parts[2] == "leave" {
			writeResponse(w, 200, map[string]any{"grant": g.View})
			return
		}
		fail(w, 410, "inactive")
		return
	}
	if parts[2] == "leave" {
		if emptyBody(w, r) != nil {
			fail(w, 400, "invalid_request")
			return
		}
		if err := s.revokeLocked(g); err != nil {
			errorResponse(w, err)
			return
		}
		writeResponse(w, 200, map[string]any{"grant": g.View})
		return
	}
	if g.lastSourceReadMS != 0 && s.now()-g.lastSourceReadMS < 1000 || !s.takeSourceSlotLocked() {
		fail(w, 429, "rate_limited")
		return
	}
	g.lastSourceReadMS = s.now()
	// Capture values, not pointers into mutable authorization state. Recheck
	// after the unlocked source read so a concurrent stop/restriction always wins.
	view, recipient := g.View, req.View.RecipientID
	sourceKey := SourceKey{SourceID: s.state.IssuerID, CarID: g.CarID}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	s.mu.Unlock()
	source, err := s.config.Source.ReadSource(ctx, sourceKey, view.ConsentStartedAtMS, view.Permissions)
	s.mu.Lock()
	<-s.sourceSlots
	if !s.clockHealthyLocked() {
		fail(w, 503, "unavailable")
		return
	}
	s.expireLocked()
	g = s.state.Grants[view.GrantID]
	if g == nil || g.View.Status != "active" {
		fail(w, 410, "inactive")
		return
	}
	if g.View.Revision != view.Revision {
		errorResponse(w, ErrRevision)
		return
	}
	if err != nil {
		fail(w, 503, "source_unavailable")
		return
	}
	out, err := s.store.Project(RecipientPrincipal{RecipientID: recipient}, view.Binding, view.Revision, source)
	if err != nil {
		errorResponse(w, err)
		return
	}
	// Only metadata-clock is durable, never this DTO. Restart revokes the grant.
	s.state.LastTimeMS = s.now()
	writeResponse(w, 200, out)
}

func (s *HTTPService) matchesGuestHost(host string) bool {
	scheme := "https"
	if strings.HasPrefix(s.origin, "http://") {
		scheme = "http"
	}
	// Authority comparison permits DNS case and the default HTTPS port, not
	// arbitrary rewritten upstream hosts or trusted X-Forwarded-Host values.
	u, err := validateOrigin(scheme+"://"+strings.ToLower(host), s.config.AllowLoopbackHTTP)
	return err == nil && u.Host == s.host
}

func (s *HTTPService) takeSourceSlotLocked() bool {
	select {
	case s.sourceSlots <- struct{}{}:
		return true
	default:
		return false
	}
}
func (s *HTTPService) redeemLocked(w http.ResponseWriter, r *http.Request) {
	key, err := s.authenticateGuestLocked(r, "")
	if err != nil {
		errorResponse(w, err)
		return
	}
	var body RedeemRequest
	if decodeBody(w, r, &body) != nil || body.SchemaVersion != 1 || !canonicalID(body.InvitationID) || !canonicalID(body.RecipientID) || !validDisplay(body.RecipientAlias) || !validSecret(body.RedemptionSecret) {
		fail(w, 400, "invalid_request")
		return
	}
	inv := s.state.Invitations[body.InvitationID]
	if inv == nil || !sameHash(inv.SecretHash, hashSecret(body.RedemptionSecret)) {
		fail(w, 401, "not_authorized")
		return
	}
	if inv.Used || inv.Envelope.ExpiresAtMS <= s.now() {
		fail(w, 410, "inactive")
		return
	}
	if inv.ExpectedRecipient != nil && (inv.ExpectedRecipient.RecipientID != body.RecipientID || thumbprint(inv.ExpectedRecipient.PublicKey) != thumbprint(key)) {
		fail(w, 401, "not_authorized")
		return
	}
	if body.ReturnInvitation != nil && !s.validReturn(*body.ReturnInvitation, inv.Envelope, Device{RecipientID: body.RecipientID, PublicKey: key}) {
		fail(w, 400, "invalid_return_invitation")
		return
	}
	if len(s.state.Requests) >= 64 {
		fail(w, 429, "capacity")
		return
	}
	id, err := randomID()
	if err != nil {
		fail(w, 503, "unavailable")
		return
	}
	token, err := randomSecret()
	if err != nil {
		fail(w, 503, "unavailable")
		return
	}
	// Code checks the concrete request/key, not merely a changeable display name.
	codeHash := hashSecret(id + thumbprint(key))
	var sum uint32
	for _, b := range []byte(codeHash) {
		sum = sum*31 + uint32(b)
	}
	code := fmt.Sprintf("%06d", sum%1000000)
	view := JoinView{RequestID: id, RecipientID: body.RecipientID, RecipientAlias: body.RecipientAlias, ConfirmationCode: code, PublicKey: key, CreatedAtMS: s.now(), ExpiresAtMS: inv.Envelope.ExpiresAtMS, Status: "pending"}
	s.state.Requests[id] = &requestRecord{View: view, InvitationID: body.InvitationID, TokenHash: hashSecret(token)}
	inv.Used = true
	if body.ReturnInvitation != nil {
		s.returnInvitations[id] = *body.ReturnInvitation
	}
	if s.saveLocked() != nil {
		fail(w, 503, "unavailable")
		return
	}
	writeResponse(w, 202, map[string]any{"request_id": id, "request_token": token, "status": "pending", "confirmation_code": code, "expires_at_ms": view.ExpiresAtMS})
}
func (s *HTTPService) validReturn(ret, original Invitation, recipient Device) bool {
	_, err := validateOrigin(ret.GuestOrigin, s.config.AllowLoopbackHTTP)
	return err == nil && ret.SchemaVersion == 1 && canonicalID(ret.IssuerID) && ret.IssuerID != original.IssuerID && ret.SessionID == original.SessionID && canonicalID(ret.MemberID) && canonicalID(ret.InvitationID) && ret.ExpiresAtMS > s.now() && ret.ExpiresAtMS <= s.now()+invitationLifeMS && validSecret(ret.RedemptionSecret) && ret.Permissions.Location && validMember(ret.Member) && validDevice(ret.OwnerDevice) && ret.OwnerDevice.RecipientID == recipient.RecipientID && thumbprint(ret.OwnerDevice.PublicKey) == thumbprint(recipient.PublicKey) && (ret.SharingDurationSeconds == 3600 || ret.SharingDurationSeconds == 7200 || ret.SharingDurationSeconds == 14400 || ret.SharingDurationSeconds == 28800)
}
func (s *HTTPService) expireLocked() {
	now := s.now()
	for k, v := range s.nonces {
		if v <= now {
			delete(s.nonces, k)
		}
	}
	for k, v := range s.proofs {
		if v <= now {
			delete(s.proofs, k)
		}
	}
	for _, r := range s.state.Requests {
		if r.View.Status == "pending" && r.View.ExpiresAtMS <= now {
			r.View.Status = "expired"
			delete(s.returnInvitations, r.View.RequestID)
		}
	}
	for _, g := range s.state.Grants {
		if g.View.Status == "active" && g.View.ExpiresAtMS <= now {
			g.View.Status = "expired"
			delete(s.returnInvitations, g.RequestID)
		}
	}
	// Keep terminal status briefly for retries, then reclaim bounded metadata.
	// No source history is stored, and active grants are never capacity-evicted.
	cutoff := now - invitationLifeMS
	for id, g := range s.state.Grants {
		if g.View.Status != "active" && g.View.ExpiresAtMS <= cutoff {
			delete(s.state.Grants, id)
		}
	}
	for id, req := range s.state.Requests {
		if req.View.Status != "pending" && req.View.ExpiresAtMS <= cutoff && (req.GrantID == "" || s.state.Grants[req.GrantID] == nil) {
			delete(s.state.Requests, id)
			delete(s.returnInvitations, id)
		}
	}
	for id, inv := range s.state.Invitations {
		if inv.Envelope.ExpiresAtMS <= cutoff {
			hasRequest := false
			for _, req := range s.state.Requests {
				if req.InvitationID == id {
					hasRequest = true
					break
				}
			}
			if !hasRequest {
				delete(s.state.Invitations, id)
			}
		}
	}
	s.store.PruneTerminalBefore(cutoff)
}

func (s *HTTPService) load() error {
	info, err := os.Lstat(s.config.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1<<20 {
		return ErrInvalid
	}
	data, err := os.ReadFile(s.config.StatePath)
	if err != nil {
		return err
	}
	validator := json.NewDecoder(strings.NewReader(string(data)))
	validator.UseNumber()
	if scanValue(validator, 0) != nil {
		return ErrInvalid
	}
	if _, err := validator.Token(); err != io.EOF {
		return ErrInvalid
	}
	// Stored state can exceed the HTTP16KiB limit, but remains schema/size bound.
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if d.Decode(&s.state) != nil {
		return ErrInvalid
	}
	if s.state.SchemaVersion != 1 || s.state.Invitations == nil || s.state.Requests == nil || s.state.Grants == nil || len(s.state.Invitations) > 64 || len(s.state.Requests) > 64 || len(s.state.Grants) > 64 {
		return ErrInvalid
	}
	for id, i := range s.state.Invitations {
		if i == nil || !canonicalID(id) || i.Envelope.InvitationID != id || i.Envelope.IssuerID != s.state.IssuerID || i.Envelope.RedemptionSecret != "" || i.CarID < 1 {
			return ErrInvalid
		}
	}
	for id, r := range s.state.Requests {
		if r == nil || !canonicalID(id) || r.View.RequestID != id || r.View.ReturnInvitation != nil || !canonicalID(r.View.RecipientID) {
			return ErrInvalid
		}
	}
	for id, g := range s.state.Grants {
		if g == nil || !canonicalID(id) || g.View.GrantID != id || g.View.IssuerID != s.state.IssuerID || g.CarID < 1 {
			return ErrInvalid
		}
	}
	return nil
}
func (s *HTTPService) saveLocked() error {
	if !s.clockHealthyLocked() {
		return ErrInactive
	}
	s.state.LastTimeMS = s.now()
	dir := filepath.Dir(s.config.StatePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		s.healthy = false
		return err
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		s.healthy = false
		return ErrInvalid
	}
	data, err := json.Marshal(s.state)
	if err != nil || len(data) > 1<<20 {
		s.healthy = false
		return ErrInvalid
	}
	f, err := os.CreateTemp(dir, ".friend-state-")
	if err != nil {
		s.healthy = false
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, s.config.StatePath)
	}
	if err == nil {
		var parent *os.File
		parent, err = os.Open(dir)
		if err == nil {
			err = parent.Sync()
			_ = parent.Close()
		}
	}
	if err != nil {
		s.healthy = false
	}
	return err
}
