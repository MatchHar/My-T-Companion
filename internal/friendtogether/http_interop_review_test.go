package friendtogether

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPGuestHostCanonicalDefaultPortAndStrictAuthority(t *testing.T) {
	f := newHTTPFixture(t, "https://a.example:443")
	for _, host := range []string{"a.example", "a.example:443", "A.Example", "A.EXAMPLE:443"} {
		r := httptest.NewRequest("POST", "https://a.example/friend/v1/nonce", strings.NewReader("{}"))
		r.Host = host
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.s.GuestHandler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("canonical host %q rejected: %d", host, w.Code)
		}
	}
	for _, host := range []string{"a.example:8443", "a.example:", "a.example:0443", "a.example.", "b.example", "a.example@b.example", "a.example/path", "a.example?x=1", "a.example#x", "a.example ", "127.0.0.1:8083"} {
		r := httptest.NewRequest("POST", "https://a.example/friend/v1/nonce", strings.NewReader("{}"))
		r.Host = host
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Forwarded-Host", "a.example")
		w := httptest.NewRecorder()
		f.s.GuestHandler().ServeHTTP(w, r)
		if w.Code != 404 {
			t.Errorf("noncanonical authority %q accepted: %d", host, w.Code)
		}
	}
	owner, _ := testDevice(t)
	friend, key := testDevice(t)
	inv := f.invite(t, owner, nil, "")
	joined := f.redeem(t, inv, friend, key, nil)
	r := f.signedRequest(t, "GET", "/friend/v1/requests/"+joined.RequestID, joined.RequestToken, nil, friend, key)
	r.Host = "A.EXAMPLE:443"
	w := httptest.NewRecorder()
	f.s.GuestHandler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal("canonical DPoP htu rejected for equivalent Host")
	}
}

func TestHTTPJSONContentTypeParametersAndAmbiguity(t *testing.T) {
	f := newHTTPFixture(t, "https://a.example")
	for _, contentType := range []string{"application/json", "application/json; charset=utf-8", `Application/JSON; Charset="UTF-8"`} {
		r := httptest.NewRequest("POST", f.s.origin+"/friend/v1/nonce", strings.NewReader("{}"))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		f.s.GuestHandler().ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("valid JSON MIME rejected: %q %d", contentType, w.Code)
		}
	}
	for _, contentType := range []string{"", "text/json", "text/plain; charset=utf-8", "application/json; charset=", "application/json, text/plain", "application/json; charset=utf-8; charset=utf-16"} {
		r := httptest.NewRequest("POST", f.s.origin+"/friend/v1/nonce", strings.NewReader("{}"))
		r.Header.Set("Content-Type", contentType)
		w := httptest.NewRecorder()
		f.s.GuestHandler().ServeHTTP(w, r)
		if w.Code != 400 {
			t.Errorf("ambiguous/nonJSON MIME accepted: %q", contentType)
		}
	}
	r := httptest.NewRequest("POST", f.s.origin+"/friend/v1/nonce", strings.NewReader("{}"))
	r.Header.Add("Content-Type", "application/json")
	r.Header.Add("Content-Type", "application/json; charset=utf-8")
	w := httptest.NewRecorder()
	f.s.GuestHandler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal("duplicate Content-Type accepted")
	}
}

func TestHTTPCancelAfterExpiryCleanupDoesNotSwallowGuestAuthentication(t *testing.T) {
	f := newHTTPFixture(t, "https://a.example")
	owner, _ := testDevice(t)
	friend, key := testDevice(t)
	inv := f.invite(t, owner, nil, "")
	joined := f.redeem(t, inv, friend, key, nil)
	grant := f.approve(t, joined.RequestID)
	f.clock.Store(grant.ExpiresAtMS + invitationLifeMS + 1)
	w := f.owner(t, "POST", "invitations/"+inv.InvitationID+"/cancel", struct{}{})
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"status":"cancelled"}` {
		t.Fatalf("authenticated owner cleanup retry: %d", w.Code)
	}
	if len(f.s.state.Grants) != 0 || len(f.s.state.Requests) != 0 || len(f.s.state.Invitations) != 0 {
		t.Fatal("cleanup fixture did not actually prune metadata")
	}
	for i := 0; i < 2; i++ {
		w = f.owner(t, "POST", "invitations/"+inv.InvitationID+"/cancel", struct{}{})
		if w.Code != 200 {
			t.Fatal("owner retry not idempotent")
		}
	}
	w = f.guest(t, "POST", "/friend/v1/requests/"+joined.RequestID+"/cancel", joined.RequestToken, struct{}{}, friend, key)
	if w.Code != 401 {
		t.Fatal("deleted token evidence must not silently authenticate")
	}
	wrongFriend, wrongKey := testDevice(t)
	w = f.guest(t, "POST", "/friend/v1/requests/"+joined.RequestID+"/cancel", joined.RequestToken, struct{}{}, wrongFriend, wrongKey)
	if w.Code != 401 {
		t.Fatal("wrong device mistaken for terminal guest success")
	}
	r := httptest.NewRequest("POST", f.s.origin+"/api/v1/friend-together/invitations/"+inv.InvitationID+"/cancel", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	f.s.OwnerHandler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("owner cancellation bypassed owner authentication")
	}
}

func TestHTTPLatestApprovalExpiryHasStrictInvitationBound(t *testing.T) {
	for _, duration := range []int64{3600, 7200, 14400, 28800} {
		for _, before := range []bool{true, false} {
			f := newHTTPFixture(t, "https://a.example")
			owner, _ := testDevice(t)
			friend, key := testDevice(t)
			body := CreateInvitationRequest{SchemaVersion: 1, CarID: 1, DurationSeconds: duration, Permissions: PermissionSet{Location: true}, Member: Member{Alias: "Synthetic", Model: "generic"}, OwnerDevice: owner}
			w := f.owner(t, "POST", "invitations", body)
			if w.Code != 201 {
				t.Fatal("create")
			}
			inv := decodeResponse[Invitation](t, w)
			joined := f.redeem(t, inv, friend, key, nil)
			at := inv.ExpiresAtMS
			if before {
				at--
			}
			f.clock.Store(at)
			w = f.owner(t, "POST", "requests/"+joined.RequestID+"/approve", struct{}{})
			if !before {
				if w.Code != 410 {
					t.Fatal("approval at expiry accepted")
				}
				continue
			}
			if w.Code != 200 {
				t.Fatal("last valid instant rejected")
			}
			grant := decodeResponse[struct {
				Grant GrantView `json:"grant"`
			}](t, w).Grant
			if grant.ExpiresAtMS != at+duration*1000 || grant.ExpiresAtMS >= inv.ExpiresAtMS+duration*1000 {
				t.Fatal("expiry exceeded conservative client bound")
			}
		}
	}
}

func TestHTTPNonceIdentifiesExactIssuerAfterMetadataCleanup(t *testing.T) {
	a, b := newHTTPFixture(t, "https://a.example"), newHTTPFixture(t, "https://b.example")
	if a.s.state.IssuerID == b.s.state.IssuerID {
		t.Fatal("independent issuers collided")
	}
	for _, f := range []*httpFixture{a, b} {
		f.clock.Add(MaxGrantLifetimeMS + 2*invitationLifeMS)
		r := httptest.NewRequest("POST", f.s.origin+"/friend/v1/nonce", strings.NewReader("{}"))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		f.s.GuestHandler().ServeHTTP(w, r)
		out := decodeResponse[struct {
			IssuerID     string `json:"issuer_id"`
			ServerTimeMS int64  `json:"server_time_ms"`
			ExpiresAtMS  int64  `json:"expires_at_ms"`
			Nonce        string `json:"nonce"`
		}](t, w)
		if w.Code != 200 || out.IssuerID != f.s.state.IssuerID || out.ServerTimeMS != f.clock.Load() || out.ExpiresAtMS != out.ServerTimeMS+60000 || !validSecret(out.Nonce) {
			t.Fatal("nonce clock lacks exact issuer binding")
		}
	}
}
