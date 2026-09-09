package friendtogether

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type httpFixtureSource struct{ now *atomic.Int64 }

func (f httpFixtureSource) OwnsCar(_ context.Context, id int64) (bool, error) { return id == 1, nil }
func (f httpFixtureSource) ReadSource(_ context.Context, key SourceKey, _ int64, p PermissionSet) (SourceSnapshot, error) {
	now := f.now.Load()
	lat, lon, zero := 43.0, -79.0, 0.0
	soc, km := 80.0, 400.0
	out := SourceSnapshot{Source: key, State: StateParked, Motion: &Motion{Latitude: &lat, Longitude: &lon, SpeedKPH: &zero, ObservedAtMS: &now}}
	if p.Battery {
		out.Battery = &Battery{Percentage: &soc, RatedRangeKM: &km, ObservedAtMS: now}
	}
	return out, nil
}

type httpFixture struct {
	s          *HTTPService
	clock      *atomic.Int64
	config     HTTPConfig
	ownerCalls *atomic.Int64
}

func newHTTPFixture(t *testing.T, origin string) *httpFixture {
	t.Helper()
	clock := &atomic.Int64{}
	clock.Store(1788888888000)
	calls := &atomic.Int64{}
	cfg := HTTPConfig{Enabled: true, GuestOrigin: origin, StatePath: filepath.Join(t.TempDir(), "grants.json"), Source: httpFixtureSource{clock}, Clock: clock.Load, AuthenticateOwner: func(r *http.Request) bool {
		calls.Add(1)
		return r.Header.Get("Authorization") == "Bearer synthetic-owner"
	}}
	s, err := NewHTTPService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return &httpFixture{s, clock, cfg, calls}
}
func testDevice(t *testing.T) (Device, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := randomID()
	return Device{RecipientID: id, PublicKey: PublicKey{KTY: "EC", CRV: "P-256", X: rawURL.EncodeToString(key.X.FillBytes(make([]byte, 32))), Y: rawURL.EncodeToString(key.Y.FillBytes(make([]byte, 32)))}}, key
}
func httpJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func decodeResponse[T any](t *testing.T, r *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if json.Unmarshal(r.Body.Bytes(), &v) != nil {
		t.Fatalf("invalid JSON response %d", r.Code)
	}
	return v
}
func (f *httpFixture) owner(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, f.s.origin+"/api/v1/friend-together/"+path, bytes.NewReader(httpJSON(t, body)))
	r.Header.Set("Authorization", "Bearer synthetic-owner")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	f.s.OwnerHandler().ServeHTTP(w, r)
	return w
}
func (f *httpFixture) nonce(t *testing.T) string {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", f.s.origin+"/friend/v1/nonce", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	f.s.GuestHandler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("nonce: %d", w.Code)
	}
	return decodeResponse[struct {
		Nonce string `json:"nonce"`
	}](t, w).Nonce
}
func (f *httpFixture) signedRequest(t *testing.T, method, path, token string, body any, dev Device, key *ecdsa.PrivateKey) *http.Request {
	t.Helper()
	nonce := f.nonce(t)
	jti, _ := randomID()
	claims := proofClaims{JTI: jti, HTM: method, HTU: f.s.origin + path, IAT: f.clock.Load() / 1000, Nonce: nonce}
	if token != "" {
		claims.ATH = hashSecret(token)
	}
	header := proofHeader{Type: "dpop+jwt", Algorithm: "ES256", JWK: dev.PublicKey}
	a := rawURL.EncodeToString(httpJSON(t, header))
	b := rawURL.EncodeToString(httpJSON(t, claims))
	sum := sha256.Sum256([]byte(a + "." + b))
	r, s, err := ecdsa.Sign(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	req := httptest.NewRequest(method, f.s.origin+path, bytes.NewReader(httpJSON(t, body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("DPoP", a+"."+b+"."+rawURL.EncodeToString(sig))
	if token != "" {
		req.Header.Set("Authorization", "DPoP "+token)
	}
	return req
}
func (f *httpFixture) guest(t *testing.T, method, path, token string, body any, dev Device, key *ecdsa.PrivateKey) *httptest.ResponseRecorder {
	t.Helper()
	r := f.signedRequest(t, method, path, token, body, dev, key)
	w := httptest.NewRecorder()
	f.s.GuestHandler().ServeHTTP(w, r)
	return w
}
func (f *httpFixture) invite(t *testing.T, owner Device, expected *Device, session string) Invitation {
	t.Helper()
	body := CreateInvitationRequest{SchemaVersion: 1, CarID: 1, DurationSeconds: 3600, Permissions: PermissionSet{Location: true, Battery: true}, Member: Member{Alias: "Synthetic car", Model: "model_3"}, OwnerDevice: owner, ExpectedRecipient: expected, SessionID: session}
	w := f.owner(t, "POST", "invitations", body)
	if w.Code != 201 {
		t.Fatalf("invite: %d %s", w.Code, w.Body.String())
	}
	return decodeResponse[Invitation](t, w)
}

type redemptionResult struct {
	RequestID    string `json:"request_id"`
	RequestToken string `json:"request_token"`
	Status       string `json:"status"`
	Code         string `json:"confirmation_code"`
}

func (f *httpFixture) redeem(t *testing.T, inv Invitation, dev Device, key *ecdsa.PrivateKey, ret *Invitation) redemptionResult {
	t.Helper()
	body := RedeemRequest{SchemaVersion: 1, InvitationID: inv.InvitationID, RedemptionSecret: inv.RedemptionSecret, RecipientID: dev.RecipientID, RecipientAlias: "Test friend", ReturnInvitation: ret}
	w := f.guest(t, "POST", "/friend/v1/redeem", "", body, dev, key)
	if w.Code != 202 {
		t.Fatalf("redeem: %d %s", w.Code, w.Body.String())
	}
	return decodeResponse[redemptionResult](t, w)
}
func (f *httpFixture) approve(t *testing.T, id string) GrantView {
	t.Helper()
	w := f.owner(t, "POST", "requests/"+id+"/approve", struct{}{})
	if w.Code != 200 {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	return decodeResponse[struct {
		Grant GrantView `json:"grant"`
	}](t, w).Grant
}

func TestHTTPGrantPendingApprovalAndScopedSnapshot(t *testing.T) {
	f := newHTTPFixture(t, "https://a.example")
	owner, _ := testDevice(t)
	friend, key := testDevice(t)
	inv := f.invite(t, owner, nil, "")
	result := f.redeem(t, inv, friend, key, nil)
	if result.Status != "pending" || len(result.Code) != 6 {
		t.Fatal("not pending")
	}
	w := f.guest(t, "GET", "/friend/v1/requests/"+result.RequestID, result.RequestToken, nil, friend, key)
	status := decodeResponse[JoinStatus](t, w)
	if status.Grant != nil || strings.Contains(w.Body.String(), "latitude") {
		t.Fatal("pending data leak")
	}
	g := f.approve(t, result.RequestID)
	f.clock.Add(1000)
	w = f.guest(t, "GET", "/friend/v1/grants/"+g.GrantID+"/snapshot", result.RequestToken, nil, friend, key)
	if w.Code != 200 {
		t.Fatalf("snapshot %d %s", w.Code, w.Body.String())
	}
	snapshot := decodeResponse[Snapshot](t, w)
	if snapshot.Binding != g.Binding || snapshot.Motion.SpeedKPH == nil || *snapshot.Motion.SpeedKPH != 0 || snapshot.Battery == nil {
		t.Fatal("projection")
	}
	for _, denied := range []string{"car_id", "vin", "odometer", "redemption_secret", "token", "source_id"} {
		if strings.Contains(w.Body.String(), `"`+denied+`"`) {
			t.Fatalf("leaked %s", denied)
		}
	}
	if w.Header().Get("Cache-Control") != "no-store, private" {
		t.Fatal("cache")
	}
}
func TestHTTPGuestNeverUsesOwnerAuthentication(t *testing.T) {
	f := newHTTPFixture(t, "https://a.example")
	before := f.ownerCalls.Load()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", f.s.origin+"/friend/v1/requests/00000000-0000-4000-8000-000000000001", nil)
	r.Header.Set("Authorization", "Bearer synthetic-owner")
	f.s.GuestHandler().ServeHTTP(w, r)
	if w.Code != 401 || f.ownerCalls.Load() != before {
		t.Fatal("guest reached owner auth")
	}
	for _, h := range []string{"Cookie", "X-API-Token", "CF-Access-Client-Secret"} {
		w = httptest.NewRecorder()
		r = httptest.NewRequest("POST", f.s.origin+"/friend/v1/nonce", strings.NewReader("{}"))
		r.Header.Set(h, "sensitive")
		f.s.GuestHandler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatal(h)
		}
	}
}
func TestHTTPDPoPWrongKeyTokenOriginAndReplay(t *testing.T) {
	f := newHTTPFixture(t, "https://a.example")
	owner, _ := testDevice(t)
	friend, key := testDevice(t)
	inv := f.invite(t, owner, nil, "")
	result := f.redeem(t, inv, friend, key, nil)
	g := f.approve(t, result.RequestID)
	other, otherKey := testDevice(t)
	w := f.guest(t, "GET", "/friend/v1/grants/"+g.GrantID+"/snapshot", result.RequestToken, nil, other, otherKey)
	if w.Code != 401 {
		t.Fatal("key binding")
	}
	wrongToken, _ := randomSecret()
	w = f.guest(t, "GET", "/friend/v1/grants/"+g.GrantID+"/snapshot", wrongToken, nil, friend, key)
	if w.Code != 401 {
		t.Fatal("token binding")
	}
	req := f.signedRequest(t, "GET", "/friend/v1/grants/"+g.GrantID+"/snapshot", result.RequestToken, nil, friend, key)
	w = httptest.NewRecorder()
	f.s.GuestHandler().ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	f.s.GuestHandler().ServeHTTP(w, req.Clone(context.Background()))
	if w.Code != 401 {
		t.Fatal("replay")
	}
	req = f.signedRequest(t, "GET", "/friend/v1/grants/"+g.GrantID+"/snapshot", result.RequestToken, nil, friend, key)
	req.Host = "evil.example"
	w = httptest.NewRecorder()
	f.s.GuestHandler().ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatal("host")
	}
}
func TestHTTPMutualTwoIssuersSameCarOne(t *testing.T) {
	a := newHTTPFixture(t, "https://a.example")
	b := newHTTPFixture(t, "https://b.example")
	adev, akey := testDevice(t)
	bdev, bkey := testDevice(t)
	ai := a.invite(t, adev, nil, "")
	bi := b.invite(t, bdev, &adev, ai.SessionID)
	aj := a.redeem(t, ai, bdev, bkey, &bi)
	listed := a.owner(t, "GET", "invitations/"+ai.InvitationID+"/requests", nil)
	views := decodeResponse[struct {
		Requests []JoinView `json:"requests"`
	}](t, listed)
	if len(views.Requests) != 1 || views.Requests[0].ReturnInvitation == nil {
		t.Fatal("missing inverse")
	}
	ag := a.approve(t, aj.RequestID)
	bj := b.redeem(t, bi, adev, akey, nil)
	bg := b.approve(t, bj.RequestID)
	if ag.IssuerID == bg.IssuerID || ag.MemberID == bg.MemberID || ag.GrantID == bg.GrantID || ag.SessionID != bg.SessionID {
		t.Fatal("identity collision")
	}
	if w := b.guest(t, "GET", "/friend/v1/grants/"+bg.GrantID+"/snapshot", aj.RequestToken, nil, bdev, bkey); w.Code != 401 {
		t.Fatal("cross issuer token")
	}
	if w := a.guest(t, "GET", "/friend/v1/grants/"+ag.GrantID+"/snapshot", aj.RequestToken, nil, bdev, bkey); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := b.guest(t, "GET", "/friend/v1/grants/"+bg.GrantID+"/snapshot", bj.RequestToken, nil, adev, akey); w.Code != 200 {
		t.Fatal(w.Code)
	}
}
func TestHTTPExpectedRecipientAndSingleUse(t *testing.T) {
	f := newHTTPFixture(t, "https://a.example")
	owner, _ := testDevice(t)
	friend, key := testDevice(t)
	wrong, badkey := testDevice(t)
	inv := f.invite(t, owner, &friend, "")
	body := RedeemRequest{SchemaVersion: 1, InvitationID: inv.InvitationID, RedemptionSecret: inv.RedemptionSecret, RecipientID: wrong.RecipientID, RecipientAlias: "Other"}
	if w := f.guest(t, "POST", "/friend/v1/redeem", "", body, wrong, badkey); w.Code != 401 {
		t.Fatal("forwarded invite")
	}
	f.redeem(t, inv, friend, key, nil)
	body.RecipientID = friend.RecipientID
	if w := f.guest(t, "POST", "/friend/v1/redeem", "", body, friend, key); w.Code != 410 {
		t.Fatal("one use")
	}
}
func TestHTTPLeaveAndCancelStopRaces(t *testing.T) {
	for _, mode := range []string{"owner_before", "guest_before", "owner_after", "guest_after", "leave"} {
		t.Run(mode, func(t *testing.T) {
			f := newHTTPFixture(t, "https://a.example")
			owner, _ := testDevice(t)
			friend, key := testDevice(t)
			inv := f.invite(t, owner, nil, "")
			j := f.redeem(t, inv, friend, key, nil)
			var g GrantView
			if strings.HasSuffix(mode, "after") || mode == "leave" {
				g = f.approve(t, j.RequestID)
			}
			var w *httptest.ResponseRecorder
			switch {
			case strings.HasPrefix(mode, "owner"):
				w = f.owner(t, "POST", "invitations/"+inv.InvitationID+"/cancel", struct{}{})
			case mode == "leave":
				w = f.guest(t, "POST", "/friend/v1/grants/"+g.GrantID+"/leave", j.RequestToken, struct{}{}, friend, key)
			default:
				w = f.guest(t, "POST", "/friend/v1/requests/"+j.RequestID+"/cancel", j.RequestToken, struct{}{}, friend, key)
			}
			if w.Code != 200 {
				t.Fatal(w.Code)
			}
			if w := f.owner(t, "POST", "requests/"+j.RequestID+"/approve", struct{}{}); w.Code != 410 {
				t.Fatalf("approve after stop %d", w.Code)
			}
			if g.GrantID != "" {
				if w := f.guest(t, "GET", "/friend/v1/grants/"+g.GrantID+"/snapshot", j.RequestToken, nil, friend, key); w.Code != 410 {
					t.Fatal("data after stop")
				}
			}
		})
	}
}
func TestHTTPRestrictionExpiryAndRestartFailClosed(t *testing.T) {
	f := newHTTPFixture(t, "https://a.example")
	owner, _ := testDevice(t)
	friend, key := testDevice(t)
	inv := f.invite(t, owner, nil, "")
	j := f.redeem(t, inv, friend, key, nil)
	g := f.approve(t, j.RequestID)
	w := f.owner(t, "POST", "grants/"+g.GrantID+"/restrict", map[string]any{"revision": g.Revision, "permissions": PermissionSet{Location: true}, "expires_at_ms": g.ExpiresAtMS})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	w = f.guest(t, "GET", "/friend/v1/grants/"+g.GrantID+"/snapshot", j.RequestToken, nil, friend, key)
	snap := decodeResponse[Snapshot](t, w)
	if snap.Battery != nil || snap.Revision != 2 {
		t.Fatal("restrict")
	}
	data, err := os.ReadFile(f.config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{j.RequestToken, inv.RedemptionSecret, "latitude", "longitude", "43.0", "-79.0"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatal("sensitive durable value")
		}
	}
	restored, err := NewHTTPService(f.config)
	if err != nil {
		t.Fatal(err)
	}
	f.s = restored
	w = f.guest(t, "GET", "/friend/v1/grants/"+g.GrantID+"/snapshot", j.RequestToken, nil, friend, key)
	if w.Code != 410 {
		t.Fatal("restart resurrected", w.Code)
	}
	w = f.owner(t, "GET", "grants", nil)
	rows := decodeResponse[struct {
		Grants []GrantView `json:"grants"`
	}](t, w)
	if len(rows.Grants) != 1 || rows.Grants[0].Status != "revoked" {
		t.Fatal("restart visibility")
	}
}
func TestHTTPExpiryAndClockRollback(t *testing.T) {
	f := newHTTPFixture(t, "https://a.example")
	owner, _ := testDevice(t)
	friend, key := testDevice(t)
	inv := f.invite(t, owner, nil, "")
	j := f.redeem(t, inv, friend, key, nil)
	g := f.approve(t, j.RequestID)
	f.clock.Store(g.ExpiresAtMS)
	w := f.guest(t, "GET", "/friend/v1/grants/"+g.GrantID+"/snapshot", j.RequestToken, nil, friend, key)
	if w.Code != 410 {
		t.Fatal(w.Code)
	}
	f.clock.Add(-1)
	if f.s.Ready() {
		t.Fatal("clock rollback ready")
	}
}
func TestHTTPStrictJSONAndUUIDNormalization(t *testing.T) {
	var req RedeemRequest
	if strictJSON([]byte(`{"schema_version":1,"schema_version":1}`), &req) == nil {
		t.Fatal("duplicate")
	}
	if strictJSON([]byte(`{"unknown":1}`), &req) == nil {
		t.Fatal("unknown")
	}
	if strictJSON([]byte(`{} {}`), &req) == nil {
		t.Fatal("trailing")
	}
	if strictJSON([]byte(`{"recipient_id":"AAAAAAAA-BBBB-4CCC-8DDD-EEEEEEEEEEEE"}`), &req) != nil || req.RecipientID != "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee" {
		t.Fatal("Foundation UUID")
	}
	var key PublicKey
	if strictJSON([]byte(`{"kty":"EC","crv":"P-256","x":"x","y":"y","d":"secret"}`), &key) == nil {
		t.Fatal("private JWK")
	}
}
func TestHTTPConfigurationAndWriteFailure(t *testing.T) {
	for _, origin := range []string{"http://public.example", "https://a.example/path", "https://user:pass@a.example", "https://a.example#x", "https://a.example?x"} {
		if _, err := validateOrigin(origin, false); err == nil {
			t.Fatal(origin)
		}
	}
	if _, err := validateOrigin("http://127.0.0.1:5678", true); err != nil {
		t.Fatal(err)
	}
	f := newHTTPFixture(t, "https://a.example")
	owner, _ := testDevice(t)
	friend, key := testDevice(t)
	inv := f.invite(t, owner, nil, "")
	j := f.redeem(t, inv, friend, key, nil)
	g := f.approve(t, j.RequestID)
	// Make only this test state's parent path invalid; no real runtime mutation.
	f.s.config.StatePath = filepath.Join(f.config.StatePath, "invalid-child")
	w := f.owner(t, "POST", "grants/"+g.GrantID+"/revoke", struct{}{})
	if w.Code != 503 || f.s.Ready() {
		t.Fatal("write failure not latched")
	}
}
