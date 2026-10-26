package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/security"
)

// remoteOn switches remote access on with the public https address.
func remoteOn(t *testing.T, h *harness) {
	t.Helper()
	_ = h.cfg.Update(func(c *config.Config) error {
		c.CloudAPI.PublicURL, c.Server.RemoteAccess = publicURL, true
		return nil
	})
	h.s.applyTrustedProxies()
	t.Cleanup(func() {
		_ = h.cfg.Update(func(c *config.Config) error { c.Server.RemoteAccess = false; return nil })
		h.s.applyTrustedProxies()
	})
}

// S1: with remote access on, a local request that only claims to be the tunnel (any
// Host but the public one) is not believed: no dashboard, no X-Forwarded-For.
func TestRemoteLocalForgedProxyHeadersRefused(t *testing.T) {
	h := newHarness(t)
	remoteOn(t, h)
	for _, host := range []string{"127.0.0.1:8085", "evil.example:8085"} {
		for i := 0; i < 3; i++ {
			req := remoteReq("POST", "/login", url.Values{"password": {"wrong-pass-1"}}.Encode(), "https", fmt.Sprintf("203.0.113.%d", i+1))
			req.Host = host
			req.Header.Del("Origin")
			if w := serveRemote(h, req); w.Code != http.StatusNotFound {
				t.Fatalf("Host %s with forged forwarding headers must get 404, got %d", host, w.Code)
			}
		}
	}
	req := remoteReq("GET", "/login", "", "https", "203.0.113.1")
	req.Host = "127.0.0.1:8085"
	if security.FromTrustedProxy(req) || security.GetClientIP(req) != "127.0.0.1" {
		t.Fatal("X-Forwarded-For of a local request not addressed to the public host must be ignored")
	}
	// The owner on the computer itself is unaffected.
	if h.login(adminPass) != http.StatusSeeOther {
		t.Fatal("local sign-in must still work with remote access on")
	}
}

// S2: changing addresses (X-Forwarded-For, or inside an IPv6 /64) does not buy unlimited
// guesses; the owner at the computer can still sign in while sign-in is paused.
func TestRemoteGlobalLoginBudget(t *testing.T) {
	h := newHarness(t)
	remoteOn(t, h)
	for i := 0; i < 5; i++ {
		remoteLogin(h, "wrong-pass-1", fmt.Sprintf("2001:db8:5:6::%x", i+1))
	}
	if w := remoteLogin(h, adminPass, "2001:db8:5:6::99"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("a new address in the same IPv6 /64 must be locked out, got %d", w.Code)
	}
	for i := 0; i < 20; i++ {
		remoteLogin(h, "wrong-pass-1", fmt.Sprintf("198.18.%d.1", i))
	}
	if w := remoteLogin(h, adminPass, "192.0.2.200"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("after many wrong passwords from all callers, sign-in through the tunnel must pause, got %d", w.Code)
	}
	if h.login(adminPass) != http.StatusSeeOther {
		t.Fatal("the owner at the computer must still be able to sign in")
	}
	if w, _ := h.api("POST", "/settings/remote-access", map[string]any{"enabled": true, "password": adminPass}); w.Code != 200 {
		t.Fatalf("a password re-check at the computer is exempt from the pause, got %d", w.Code)
	}
}

// S3: a flood of wrong passwords writes at most one entry a minute, with a count.
func TestLoginFailuresLoggedThrottled(t *testing.T) {
	h := newHarness(t)
	remoteOn(t, h)
	since := time.Now().Add(-time.Second)
	for i := 0; i < 8; i++ {
		remoteLogin(h, "wrong-pass-1", fmt.Sprintf("198.51.100.%d", i+1))
	}
	if n := security.CountAudit("LOGIN_FAILED", since); n != 1 {
		t.Fatalf("wrong passwords must be logged at most once a minute, got %d entries", n)
	}
	h.s.rejectMu.Lock()
	h.s.rejectLogged["auth:LOGIN_FAILED"] = time.Now().Add(-2 * time.Minute)
	h.s.rejectMu.Unlock()
	remoteLogin(h, "wrong-pass-1", "198.51.100.50")
	ev := security.QueryAuditLog(1, "LOGIN_FAILED", since)
	if len(ev) != 1 || !strings.Contains(ev[0].Details, "(and 7 more") {
		t.Fatalf("the next entry must carry the count left out: %+v", ev)
	}
}

// S4: remote access serves nothing unless the public address is https.
func TestRemoteNeedsHTTPSPublicAddress(t *testing.T) {
	h := newHarness(t)
	remoteOn(t, h)
	if w := serveRemote(h, remoteReq("GET", "/login", "", "https", "198.51.100.7")); w.Code != 200 {
		t.Fatalf("with an https address the sign-in page loads, got %d", w.Code)
	}
	_ = h.cfg.Update(func(c *config.Config) error { c.CloudAPI.PublicURL = "http://pulse.example.com"; return nil })
	if w := serveRemote(h, remoteReq("GET", "/login", "", "https", "198.51.100.7")); w.Code != http.StatusNotFound {
		t.Fatalf("a plain http public address must serve nothing, got %d", w.Code)
	}
	_ = h.cfg.Update(func(c *config.Config) error { c.CloudAPI.PublicURL = ""; return nil })
	if w := serveRemote(h, remoteReq("GET", "/login", "", "https", "198.51.100.7")); w.Code != http.StatusNotFound {
		t.Fatalf("without a public address remote access serves nothing, got %d", w.Code)
	}
}

// S5: the developer key and the integration endpoint do not work through the tunnel.
func TestRemoteRefusesAPIKeyAndIntegration(t *testing.T) {
	h := newHarness(t)
	remoteOn(t, h)
	req := remoteReq("GET", "/api/v1/contacts", "", "https", "198.51.100.7")
	req.Header.Set("X-API-Key", "any-key")
	if w := serveRemote(h, req); w.Code != http.StatusNotFound {
		t.Fatalf("the API key through the tunnel must get 404, got %d", w.Code)
	}
	req = remoteReq("GET", "/api/v1/contacts", "", "https", "198.51.100.7")
	req.Header.Set("Authorization", "Bearer any-key")
	if w := serveRemote(h, req); w.Code != http.StatusNotFound {
		t.Fatalf("a bearer key through the tunnel must get 404, got %d", w.Code)
	}
	req = remoteReq("POST", "/api/v1/webhook/send", `{"to":"14155550901","message":"hi"}`, "https", "198.51.100.7")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Secret", "inbound-secret-123456")
	if w := serveRemote(h, req); w.Code != http.StatusNotFound {
		t.Fatalf("the integration endpoint through the tunnel must get 404, got %d", w.Code)
	}
}

// S6: through the tunnel an http Origin is refused and HSTS is sent; not on the computer.
func TestRemoteOriginHTTPSAndHSTS(t *testing.T) {
	h := newHarness(t)
	remoteOn(t, h)
	req := remoteReq("POST", "/login", url.Values{"password": {adminPass}}.Encode(), "https", "198.51.100.7")
	req.Header.Set("Origin", "http://pulse.example.com")
	if w := serveRemote(h, req); w.Code != http.StatusForbidden {
		t.Fatalf("an http Origin through the tunnel must be refused, got %d", w.Code)
	}
	w := serveRemote(h, remoteReq("GET", "/login", "", "https", "198.51.100.7"))
	if w.Header().Get("Strict-Transport-Security") != "max-age=31536000" {
		t.Fatalf("HSTS missing through the tunnel: %v", w.Header())
	}
	if lw := h.raw("GET", "/login", nil, nil); lw.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("no HSTS on the computer's plain http address")
	}
}

// F9: a DNS-rebinding page on this computer can set X-Forwarded-Host to the public host
// (it is not a forbidden header), but its Host is still the rebound name: it must not be
// treated as the tunnel, not even for GET requests.
func TestRebindingPageNamingThePublicHostIsRefused(t *testing.T) {
	h := newHarness(t)
	remoteOn(t, h)
	req := remoteReq("GET", "/login", "", "https", "203.0.113.9")
	req.Host = "evil.example:8085"
	req.Header.Set("X-Forwarded-Host", hostOnly(strings.TrimPrefix(publicURL, "https://")))
	if w := serveRemote(h, req); w.Code != http.StatusNotFound {
		t.Fatalf("a rebinding page naming the public host in X-Forwarded-Host must get 404, got %d", w.Code)
	}
	if security.FromTrustedProxy(req) {
		t.Fatal("its X-Forwarded-For must not be believed")
	}
}
