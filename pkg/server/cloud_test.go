package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whatsapp-automation/pkg/config"
)

func httpReq(method, target, body, host string, hdr map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Host = host
	r.RemoteAddr = "127.0.0.1:50000"
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	return r
}

func serve(h *harness, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, r)
	return w
}

func metaSign(secret, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// The Business API details need the admin password, are checked, are stored sealed
// and are never sent back to the browser.
func TestSaveConnectionSealsSecrets(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	form := map[string]any{"password": adminPass, "phone_number_id": "1234567890", "business_account_id": "987654321",
		"access_token": "EAAG-real-token", "app_secret": "app-secret-1", "public_url": "https://pulse-shop.trycloudflare.com/"}
	bad := func(k string, v any) map[string]any {
		m := map[string]any{}
		for a, b := range form {
			m[a] = b
		}
		m[k] = v
		return m
	}
	for _, c := range []struct {
		body map[string]any
		code int
	}{
		{bad("password", "wrong"), http.StatusForbidden},
		{bad("phone_number_id", "+91 98000"), http.StatusBadRequest},
		{bad("business_account_id", "abc"), http.StatusBadRequest},
		{bad("public_url", "http://pulse.example.com"), http.StatusBadRequest},
		{bad("public_url", "https://pulse.example.com/cloud/webhook"), http.StatusBadRequest},
		{bad("api_version", "23"), http.StatusBadRequest},
		{bad("access_token", "has space"), http.StatusBadRequest},
		{bad("access_token", ""), http.StatusBadRequest}, // no token saved yet
	} {
		if w, out := h.api("POST", "/connection", c.body); w.Code != c.code {
			t.Errorf("%v: got %d %v, want %d", c.body, w.Code, out, c.code)
		}
	}
	w, out := h.api("POST", "/connection", form)
	if w.Code != 200 {
		t.Fatalf("save failed: %d %v", w.Code, out)
	}
	raw, _ := os.ReadFile(h.cfg.FilePath)
	snap := h.cfg.Snapshot().CloudAPI
	if strings.Contains(snap.AccessToken, "real-token") || strings.Contains(snap.AppSecret, "app-secret") || strings.Contains(string(raw), "real-token") {
		t.Fatal("the token and app secret must be stored sealed")
	}
	if tok, _ := h.cfg.CloudToken(); tok != "EAAG-real-token" {
		t.Fatalf("token = %q", tok)
	}
	if snap.PublicURL != "https://pulse-shop.trycloudflare.com" || !strings.HasPrefix(snap.VerifyToken, "pulse-") || snap.APIVersion != config.DefaultGraphVersion {
		t.Fatalf("saved %+v", snap)
	}
	for _, path := range []string{"/connection", "/settings"} {
		w, _ := h.api("GET", path, nil)
		if body := w.Body.String(); strings.Contains(body, "real-token") || strings.Contains(body, "app-secret-1") || strings.Contains(body, "sealed1:") {
			t.Fatalf("%s leaks a secret: %s", path, body)
		}
	}
	_, view := h.api("GET", "/connection", nil)
	if view["has_access_token"] != true || view["has_app_secret"] != true || view["webhook_url"] != "https://pulse-shop.trycloudflare.com/cloud/webhook" {
		t.Fatalf("view %v", view)
	}
	// Leaving the secrets empty keeps them; the verify token only changes on request.
	verify := snap.VerifyToken
	keep := bad("access_token", "")
	keep["app_secret"] = ""
	if w, _ := h.api("POST", "/connection", keep); w.Code != 200 {
		t.Fatal("saving without retyping the token must keep it")
	}
	if tok, _ := h.cfg.CloudToken(); tok != "EAAG-real-token" || h.cfg.Snapshot().CloudAPI.VerifyToken != verify {
		t.Fatal("token or verify token changed")
	}
	keep["regenerate_verify_token"] = true
	h.api("POST", "/connection", keep)
	if h.cfg.Snapshot().CloudAPI.VerifyToken == verify {
		t.Fatal("verify token was not regenerated")
	}
	// Test connection uses the form's values without saving them.
	if w, out := h.api("POST", "/connection/test", map[string]any{"phone_number_id": "555555", "access_token": "other"}); w.Code != 200 || out["verified_name"] != "Asha Stores" || h.wa.probes[len(h.wa.probes)-1] != "v23.0|555555|other" {
		t.Fatalf("test %d %v %v", w.Code, out, h.wa.probes)
	}
	if w, _ := h.api("POST", "/connection/test", map[string]any{}); w.Code != 200 || h.wa.probes[len(h.wa.probes)-1] != "v23.0|1234567890|EAAG-real-token" {
		t.Fatalf("test with saved values: %v", h.wa.probes)
	}
	if w, out := h.api("POST", "/connection/test", map[string]any{"access_token": "bad"}); w.Code != http.StatusBadGateway || !strings.Contains(out["error"].(string), "expired") {
		t.Fatalf("a refused token must be explained: %v", out)
	}
	if tok, _ := h.cfg.CloudToken(); tok != "EAAG-real-token" {
		t.Fatal("testing must not save anything")
	}
}

// Meta's webhook works without signing in and through the tunnel's host name, but
// only on its own path, only once set up, and only with the verify token or signature.
func TestCloudWebhook(t *testing.T) {
	h := newHarness(t)
	get := func(host, query string) int {
		r := httpReq("GET", config.WebhookPath+query, "", host, nil)
		w := serve(h, r)
		return w.Code
	}
	if c := get("pulse-shop.trycloudflare.com", "?hub.mode=subscribe&hub.verify_token=x&hub.challenge=1"); c != http.StatusNotFound {
		t.Fatalf("before setup the webhook must not exist: %d", c)
	}
	_ = h.cfg.Update(func(c *config.Config) error {
		c.CloudAPI.PhoneNumberID, c.CloudAPI.AccessToken, c.CloudAPI.AppSecret = "111", "tok", "app-secret"
		c.CloudAPI.VerifyToken, c.CloudAPI.PublicURL = "pulse-verify", "https://pulse-shop.trycloudflare.com"
		return nil
	})
	if c := get("pulse-shop.trycloudflare.com", "?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=42"); c != http.StatusForbidden {
		t.Fatalf("wrong verify token: %d", c)
	}
	w := serve(h, httpReq("GET", config.WebhookPath+"?hub.mode=subscribe&hub.verify_token=pulse-verify&hub.challenge=4242", "", "pulse-shop.trycloudflare.com", nil))
	if w.Code != 200 || w.Body.String() != "4242" || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("challenge: %d %q", w.Code, w.Body.String())
	}
	if h.cfg.Snapshot().Setup.WebhookSeenAt == nil {
		t.Fatal("a verified webhook completes the setup step")
	}
	body := `{"object":"whatsapp_business_account","entry":[]}`
	for _, sig := range []string{"", "sha256=00", metaSign("other", body)} {
		w := serve(h, httpReq("POST", config.WebhookPath, body, "pulse-shop.trycloudflare.com", map[string]string{"X-Hub-Signature-256": sig}))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("signature %q accepted: %d", sig, w.Code)
		}
	}
	w = serve(h, httpReq("POST", config.WebhookPath, body, "pulse-shop.trycloudflare.com", map[string]string{"X-Hub-Signature-256": metaSign("app-secret", body), "X-Forwarded-For": "157.240.0.1"}))
	if w.Code != 200 || len(h.wa.webhooks) != 1 {
		t.Fatalf("signed delivery: %d %d", w.Code, len(h.wa.webhooks))
	}
	big := strings.Repeat("x", maxCloudWebhookBody+10)
	if w := serve(h, httpReq("POST", config.WebhookPath, big, "pulse-shop.trycloudflare.com", map[string]string{"X-Hub-Signature-256": metaSign("app-secret", big)})); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized body: %d", w.Code)
	}
	// Everything else stays private under the tunnel's name, even the sign-in page and
	// even when someone adds that name to the allowed hosts.
	_ = h.cfg.Update(func(c *config.Config) error {
		c.Server.AllowedHosts = []string{"pulse-shop.trycloudflare.com"}
		return nil
	})
	for _, p := range []string{"/login", "/", "/api/v1/me", "/api/v1/webhook/send", "/cloud/webhook/x"} {
		if w := serve(h, httpReq("GET", p, "", "pulse-shop.trycloudflare.com", nil)); w.Code != http.StatusNotFound {
			t.Errorf("%s through the tunnel: %d", p, w.Code)
		}
		if w := serve(h, httpReq("GET", p, "", "127.0.0.1:8085", map[string]string{"X-Forwarded-Host": "pulse-shop.trycloudflare.com"})); w.Code != http.StatusNotFound {
			t.Errorf("%s with the tunnel as forwarded host: %d", p, w.Code)
		}
	}
	if w := serve(h, httpReq("GET", "/login", "", "evil.example.com", nil)); w.Code != http.StatusForbidden {
		t.Fatalf("other host names are still refused: %d", w.Code)
	}
}

// Files of the old linked-device connection are listed and deleted only on request.
func TestDeleteOldLinkedDeviceFiles(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	dir := t.TempDir()
	old := []string{filepath.Join(dir, "whatsapp.db.sealed"), filepath.Join(dir, "whatsapp.db-wal")}
	for _, p := range old {
		_ = os.WriteFile(p, []byte("x"), 0600)
	}
	h.s.OldSessionFiles = append(old, filepath.Join(dir, "whatsapp.db"))
	w, _ := h.api("GET", "/backups", nil)
	var lst map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &lst)
	if lst["old_session"] != float64(2) {
		t.Fatalf("old files not listed: %v", lst)
	}
	if w, _ := h.api("POST", "/privacy/delete-old-session", map[string]any{"password": "wrong"}); w.Code != http.StatusForbidden {
		t.Fatal("deleting needs the admin password")
	}
	if w, _ := h.api("POST", "/privacy/delete-old-session", map[string]any{"password": adminPass}); w.Code != 200 {
		t.Fatal("delete failed")
	}
	for _, p := range old {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s still exists", p)
		}
	}
}
