package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"whatsapp-automation/pkg/config"
)

const publicURL = "https://pulse.example.com"

// remoteReq builds a request as the tunnel (cloudflared on this computer) delivers it
// from a phone: from 127.0.0.1, Host = the public name, with forwarding headers.
func remoteReq(method, target, body, proto, client string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, target, nil)
	}
	req.Host = "pulse.example.com"
	req.RemoteAddr = "127.0.0.1:51000"
	req.Header.Set("X-Forwarded-Proto", proto)
	req.Header.Set("X-Forwarded-For", client)
	req.Header.Set("Cf-Connecting-Ip", client)
	req.Header.Set("Origin", publicURL)
	return req
}

func serveRemote(h *harness, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, req)
	return w
}

func remoteLogin(h *harness, pass, client string) *httptest.ResponseRecorder {
	return serveRemote(h, remoteReq("POST", "/login", url.Values{"password": {pass}}.Encode(), "https", client))
}

// With remote access off (the default) the public address answers only Meta's webhook.
func TestRemoteAccessOffKeepsDashboardPrivate(t *testing.T) {
	h := newHarness(t)
	_ = h.cfg.Update(func(c *config.Config) error { c.CloudAPI.PublicURL = publicURL; return nil })
	h.s.applyTrustedProxies()
	for _, p := range []string{"/", "/login", "/api/v1/me", "/manifest.webmanifest"} {
		if w := serveRemote(h, remoteReq("GET", p, "", "https", "198.51.100.7")); w.Code != http.StatusNotFound {
			t.Errorf("remote access off: %s through the tunnel must be 404, got %d", p, w.Code)
		}
	}
	if w := remoteLogin(h, adminPass, "198.51.100.7"); w.Code != http.StatusNotFound {
		t.Fatalf("remote access off: signing in through the tunnel must be impossible, got %d", w.Code)
	}
}

// Switching it on needs the admin password and a fixed https address.
func TestRemoteAccessNeedsPasswordAndHTTPSAddress(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	if w, _ := h.api("POST", "/settings/remote-access", map[string]any{"enabled": true, "password": "wrong-pass-1"}); w.Code != http.StatusForbidden {
		t.Fatalf("a wrong password must be refused, got %d", w.Code)
	}
	if w, _ := h.api("POST", "/settings/remote-access", map[string]any{"enabled": true, "password": adminPass}); w.Code != http.StatusBadRequest {
		t.Fatalf("without a public https address it must be refused, got %d", w.Code)
	}
	_ = h.cfg.Update(func(c *config.Config) error { c.CloudAPI.PublicURL = "http://pulse.example.com"; return nil })
	if w, _ := h.api("POST", "/settings/remote-access", map[string]any{"enabled": true, "password": adminPass}); w.Code != http.StatusBadRequest {
		t.Fatalf("a plain http address must be refused, got %d", w.Code)
	}
	_ = h.cfg.Update(func(c *config.Config) error { c.CloudAPI.PublicURL = publicURL; return nil })
	if w, _ := h.api("POST", "/settings/remote-access", map[string]any{"enabled": true, "password": adminPass}); w.Code != 200 || !h.cfg.Snapshot().Server.RemoteAccess {
		t.Fatalf("with the password and an https address it switches on, got %d", w.Code)
	}
}

// With remote access on, a phone signs in over https through the tunnel; plain http,
// forged headers from outside and computer-only actions stay refused.
func TestRemoteAccessOnServesPhonesSafely(t *testing.T) {
	h := newHarness(t)
	_ = h.cfg.Update(func(c *config.Config) error {
		c.CloudAPI.PublicURL, c.Server.RemoteAccess = publicURL, true
		return nil
	})
	h.s.applyTrustedProxies()
	t.Cleanup(func() {
		_ = h.cfg.Update(func(c *config.Config) error { c.Server.RemoteAccess = false; return nil })
		h.s.applyTrustedProxies()
	})

	if w := serveRemote(h, remoteReq("GET", "/login", "", "https", "198.51.100.7")); w.Code != 200 {
		t.Fatalf("the sign-in page must load on the phone, got %d", w.Code)
	}
	if w := serveRemote(h, remoteReq("GET", "/login", "", "http", "198.51.100.7")); w.Code != http.StatusNotFound {
		t.Fatalf("plain http through the tunnel must be refused, got %d", w.Code)
	}
	forged := remoteReq("GET", "/login", "", "https", "198.51.100.7")
	forged.RemoteAddr = "203.0.113.9:4000" // not the tunnel on this computer
	if w := serveRemote(h, forged); w.Code != http.StatusNotFound {
		t.Fatalf("forwarding headers from an untrusted address must be refused, got %d", w.Code)
	}

	w := remoteLogin(h, adminPass, "198.51.100.7")
	var sess *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			sess = c
		}
	}
	if w.Code != http.StatusSeeOther || sess == nil || !sess.Secure || !sess.HttpOnly {
		t.Fatalf("sign-in over https must work with a Secure, HttpOnly cookie: %d %+v", w.Code, sess)
	}
	me := remoteReq("GET", "/api/v1/me", "", "https", "198.51.100.7")
	me.AddCookie(sess)
	mw := serveRemote(h, me)
	var info map[string]any
	_ = json.Unmarshal(mw.Body.Bytes(), &info)
	if mw.Code != 200 || info["local"] != false || info["remote_access"] != true {
		t.Fatalf("the phone must be signed in and known as not local: %d %v", mw.Code, info)
	}
	csrf, _ := info["csrf"].(string)
	auto := remoteReq("POST", "/api/v1/settings/autostart", "", "https", "198.51.100.7")
	auto.Body = http.NoBody
	auto.Header.Set("Content-Type", "application/json")
	auto.Header.Set("X-CSRF-Token", csrf)
	auto.AddCookie(sess)
	if aw := serveRemote(h, auto); aw.Code != http.StatusForbidden {
		t.Fatalf("start-with-Windows must only work on the computer itself, got %d", aw.Code)
	}
	quit := remoteReq("POST", "/control/quit", "", "https", "198.51.100.7")
	quit.Header.Set("X-Pulse-Control", h.s.ControlToken)
	if qw := serveRemote(h, quit); qw.Code == http.StatusOK {
		t.Fatal("closing Pulse must never work from outside the computer")
	}
}

// Wrong passwords lock out the caller who typed them (real address from the tunnel),
// not every phone.
func TestRemoteLockoutIsPerCaller(t *testing.T) {
	h := newHarness(t)
	_ = h.cfg.Update(func(c *config.Config) error {
		c.CloudAPI.PublicURL, c.Server.RemoteAccess = publicURL, true
		return nil
	})
	h.s.applyTrustedProxies()
	t.Cleanup(func() {
		_ = h.cfg.Update(func(c *config.Config) error { c.Server.RemoteAccess = false; return nil })
		h.s.applyTrustedProxies()
	})
	for i := 0; i < 6; i++ {
		remoteLogin(h, "wrong-pass-1", "203.0.113.50")
	}
	if w := remoteLogin(h, adminPass, "203.0.113.50"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the guessing caller must be locked out, got %d", w.Code)
	}
	if w := remoteLogin(h, adminPass, "198.51.100.7"); w.Code != http.StatusSeeOther {
		t.Fatalf("another phone must still be able to sign in, got %d", w.Code)
	}
}
