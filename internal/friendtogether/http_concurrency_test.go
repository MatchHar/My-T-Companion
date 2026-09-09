package friendtogether

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type heldSource struct {
	httpFixtureSource
	entered chan struct{}
	release chan struct{}
}

type heldBody struct {
	entered, release chan struct{}
	read             bool
}

func (b *heldBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.EOF
	}
	b.read = true
	close(b.entered)
	<-b.release
	return copy(p, "{}"), nil
}
func (b *heldBody) Close() error { return nil }

type heldWriter struct {
	*httptest.ResponseRecorder
	entered, release chan struct{}
}

func (w heldWriter) Write(p []byte) (int, error) {
	close(w.entered)
	<-w.release
	return w.ResponseRecorder.Write(p)
}

func TestHTTPSlowNetworkIOCannotHoldConsentMutex(t *testing.T) {
	for _, stage := range []string{"request_body", "response_body"} {
		t.Run(stage, func(t *testing.T) {
			f := newHTTPFixture(t, "https://a.example")
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			r := httptest.NewRequest("POST", f.s.origin+"/friend/v1/nonce", strings.NewReader("{}"))
			var w http.ResponseWriter = httptest.NewRecorder()
			if stage == "request_body" {
				r.Body = &heldBody{entered: entered, release: release}
			} else {
				w = heldWriter{httptest.NewRecorder(), entered, release}
			}
			go func() { f.s.GuestHandler().ServeHTTP(w, r); close(done) }()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("slow IO did not start")
			}
			control := make(chan int, 1)
			go func() { control <- f.owner(t, "GET", "status", nil).Code }()
			select {
			case code := <-control:
				if code != 200 {
					t.Errorf("owner status: %d", code)
				}
			case <-time.After(time.Second):
				close(release)
				<-done
				t.Fatal("network IO held consent mutex")
			}
			close(release)
			<-done
		})
	}
}

func (s *heldSource) ReadSource(ctx context.Context, key SourceKey, consent int64, permissions PermissionSet) (SourceSnapshot, error) {
	s.entered <- struct{}{}
	select {
	case <-ctx.Done():
		return SourceSnapshot{}, ctx.Err()
	case <-s.release:
		return s.httpFixtureSource.ReadSource(ctx, key, consent, permissions)
	}
}

func TestHTTPSourceReadDoesNotBlockControlAndRechecksConsent(t *testing.T) {
	for _, action := range []string{"revoke", "restrict", "expiry", "clock_rollback"} {
		t.Run(action, func(t *testing.T) {
			f := newHTTPFixture(t, "https://a.example")
			owner, _ := testDevice(t)
			friend, key := testDevice(t)
			inv := f.invite(t, owner, nil, "")
			joined := f.redeem(t, inv, friend, key, nil)
			grant := f.approve(t, joined.RequestID)
			source := &heldSource{httpFixtureSource{f.clock}, make(chan struct{}, 2), make(chan struct{})}
			f.s.config.Source = source
			r := f.signedRequest(t, "GET", "/friend/v1/grants/"+grant.GrantID+"/snapshot", joined.RequestToken, nil, friend, key)
			response := httptest.NewRecorder()
			done := make(chan struct{})
			go func() { f.s.GuestHandler().ServeHTTP(response, r); close(done) }()
			select {
			case <-source.entered:
			case <-time.After(time.Second):
				t.Fatal("source read did not start")
			}
			controlDone := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				switch action {
				case "revoke":
					controlDone <- f.owner(t, "POST", "grants/"+grant.GrantID+"/revoke", struct{}{})
				case "restrict":
					controlDone <- f.owner(t, "POST", "grants/"+grant.GrantID+"/restrict", map[string]any{"revision": grant.Revision, "permissions": PermissionSet{Location: true}, "expires_at_ms": grant.ExpiresAtMS})
				case "expiry":
					f.clock.Store(grant.ExpiresAtMS)
					controlDone <- f.owner(t, "GET", "grants", nil)
				case "clock_rollback":
					f.clock.Add(-1)
					controlDone <- f.owner(t, "GET", "status", nil)
				}
			}()
			select {
			case control := <-controlDone:
				if control.Code != 200 {
					t.Errorf("control failed: %d", control.Code)
				}
			case <-time.After(time.Second):
				close(source.release)
				<-done
				t.Fatal("slow source blocked owner control")
			}
			close(source.release)
			<-done
			want := 410
			if action == "restrict" {
				want = 409
			}
			if action == "clock_rollback" {
				want = 503
			}
			if response.Code != want {
				t.Fatalf("response after %s = %d, want %d", action, response.Code, want)
			}
			if len(response.Body.Bytes()) > 100 {
				t.Fatal("revoked/restricted response may contain telemetry")
			}
		})
	}
}

func TestHTTPSourceConcurrencyAndPerGrantRateAreBounded(t *testing.T) {
	f := newHTTPFixture(t, "https://a.example")
	owner, _ := testDevice(t)
	friend, key := testDevice(t)
	var grants []GrantView
	var joined []redemptionResult
	for i := 0; i < 3; i++ {
		inv := f.invite(t, owner, nil, "")
		joined = append(joined, f.redeem(t, inv, friend, key, nil))
		grants = append(grants, f.approve(t, joined[i].RequestID))
	}
	source := &heldSource{httpFixtureSource{f.clock}, make(chan struct{}, 2), make(chan struct{})}
	f.s.config.Source = source
	done := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		r := f.signedRequest(t, "GET", "/friend/v1/grants/"+grants[i].GrantID+"/snapshot", joined[i].RequestToken, nil, friend, key)
		go func() { w := httptest.NewRecorder(); f.s.GuestHandler().ServeHTTP(w, r); done <- w }()
		select {
		case <-source.entered:
		case <-time.After(time.Second):
			t.Fatal("source blocked")
		}
	}
	for _, i := range []int{0, 2} {
		w := f.guest(t, "GET", "/friend/v1/grants/"+grants[i].GrantID+"/snapshot", joined[i].RequestToken, nil, friend, key)
		if w.Code != 429 {
			t.Errorf("source limit for %d = %d", i, w.Code)
		}
	}
	if w := f.owner(t, "GET", "status", nil); w.Code != 200 {
		t.Fatal("status unavailable under bounded load")
	}
	close(source.release)
	for i := 0; i < 2; i++ {
		if w := <-done; w.Code != 200 {
			t.Errorf("initial read: %d", w.Code)
		}
	}
	if w := f.guest(t, "GET", "/friend/v1/grants/"+grants[0].GrantID+"/snapshot", joined[0].RequestToken, nil, friend, key); w.Code != 429 {
		t.Fatal("one-second per-grant bound not enforced")
	}
}
