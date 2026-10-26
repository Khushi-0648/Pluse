package server

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/security"
)

// The failing tests of the eighth-package audit (24 Oct 2026), replayed against the code
// so none of them can come back unnoticed.

// S34: the developer key reads and sends; it changes and exports nothing.
func TestAudit8APIKeyScope(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_, out := h.api("POST", "/settings/api-key", map[string]any{"password": adminPass, "action": "generate"})
	key := out["api_key"].(string)
	_ = h.cs.Save(contacts.Contact{Phone: "14155553001"})
	h.sess, h.csrf = "", ""
	call := func(method, path, body string) int {
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		return h.raw(method, "/api/v1"+path, rd, map[string]string{"X-API-Key": key, "Content-Type": "application/json"}).Code
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/settings", ""}, {"GET", "/connection", ""}, {"GET", "/audit", ""}, {"GET", "/backups", ""},
		{"GET", "/privacy/export?phone=14155553001", ""}, {"GET", "/contacts/export.csv", ""}, {"GET", "/analytics", ""},
		{"GET", "/analytics/export.csv", ""}, {"GET", "/system", ""}, {"GET", "/rules", ""}, {"GET", "/campaigns", ""},
		{"PATCH", "/settings", `{"automation":{"enforce_24h_window":false}}`},
		{"PATCH", "/settings", `{"storage":{"retention_days":1}}`},
		{"PATCH", "/settings", `{"webhook":{"enabled":true,"url":"https://hooks.example.com/x"}}`},
		{"POST", "/rules", `{"name":"x","type":"exact","pattern":"x","reply":"x","enabled":true}`},
		{"DELETE", "/rules/greeting_menu", ""},
		{"POST", "/templates", `{"name":"x","content":"x"}`}, {"DELETE", "/templates/x", ""},
		{"POST", "/contacts", `{"phone":"14155553002"}`}, {"DELETE", "/contacts/14155553001", ""},
		{"POST", "/contacts/bulk", `{"action":"opt_out","phones":["14155553001"]}`},
		{"POST", "/contacts/consent", `{"phone":"14155553001","consent":true,"source":"crm"}`},
		{"POST", "/contacts/pause-bot", `{"phone":"14155553001","hours":24}`},
		{"POST", "/scheduled", `{"recipient":"14155553001","message":"x","scheduled_at":"2030-01-01T00:00:00Z"}`},
	} {
		if code := call(c.method, c.path, c.body); code != http.StatusForbidden {
			t.Errorf("API key %s %s: got %d, want 403", c.method, c.path, code)
		}
	}
	for _, p := range []string{"/contacts", "/messages", "/conversations", "/templates", "/status"} {
		if code := call("GET", p, ""); code != 200 {
			t.Errorf("API key GET %s: got %d, want 200", p, code)
		}
	}
	if c, _ := h.cs.Get("14155553001"); c == nil || c.Consent || c.IsDND {
		t.Fatal("the key changed a contact")
	}
}

// S35: a flood of forged calls must not block Meta's real, signed delivery.
func TestAudit8WebhookFloodDoesNotBlockMeta(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	if w, out := h.api("POST", "/connection", map[string]any{"password": adminPass, "phone_number_id": "1234567890",
		"access_token": "EAAG-token", "app_secret": "app-secret", "public_url": "https://pulse-shop.trycloudflare.com"}); w.Code != 200 {
		t.Fatalf("save connection: %d %v", w.Code, out)
	}
	for i := 0; i < 400; i++ {
		serve(h, httpReq("POST", config.WebhookPath, `{"x":1}`, "pulse-shop.trycloudflare.com", map[string]string{"X-Hub-Signature-256": "sha256=00"}))
		serve(h, httpReq("GET", config.WebhookPath+"?hub.mode=subscribe&hub.verify_token=wrong&hub.challenge=1", "", "pulse-shop.trycloudflare.com", nil))
	}
	body := `{"object":"whatsapp_business_account","entry":[]}`
	w := serve(h, httpReq("POST", config.WebhookPath, body, "pulse-shop.trycloudflare.com", map[string]string{"X-Hub-Signature-256": metaSign("app-secret", body)}))
	if w.Code != 200 {
		t.Fatalf("Meta's signed delivery after a flood: got %d", w.Code)
	}
}

// S36: no tunnel or proxy trick reaches the dashboard.
func TestAudit8TunnelCannotReachDashboard(t *testing.T) {
	h := newHarness(t)
	_ = h.cfg.Update(func(c *config.Config) error {
		c.CloudAPI.PublicURL = "https://pulse-shop.trycloudflare.com"
		return nil
	})
	for name, hdr := range map[string]map[string]string{
		"Host rewritten, Cloudflare headers": {"Cf-Connecting-Ip": "203.0.113.5", "X-Forwarded-For": "203.0.113.5"},
		"X-Forwarded-Host list":              {"X-Forwarded-Host": "evil.example, pulse-shop.trycloudflare.com"},
		"Forwarded header only":              {"Forwarded": "for=203.0.113.5;host=pulse-shop.trycloudflare.com"},
		"X-Original-Host":                    {"X-Original-Host": "pulse-shop.trycloudflare.com"},
	} {
		if w := serve(h, httpReq("GET", "/login", "", "127.0.0.1:8085", hdr)); w.Code != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", name, w.Code)
		}
	}
	// Absolute-form request line naming the public host, with a local Host header.
	r := httpReq("GET", "https://pulse-shop.trycloudflare.com/login", "", "127.0.0.1:8085", nil)
	r.Host = "pulse-shop.trycloudflare.com"
	if w := serve(h, r); w.Code != http.StatusNotFound {
		t.Errorf("absolute-form request: got %d, want 404", w.Code)
	}
}

// S42: settings that can never work are refused.
func TestAudit8OddSettingsRefused(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	for _, u := range []string{"https://localhost", "https://10.0.0.5", "https://127.0.0.1", "https://pulse.local"} {
		if w, _ := h.api("POST", "/connection", map[string]any{"password": adminPass, "phone_number_id": "1234567890",
			"access_token": "EAAG-token", "app_secret": "s", "public_url": u}); w.Code != http.StatusBadRequest {
			t.Errorf("public address %s: got %d, want 400", u, w.Code)
		}
	}
	if w, _ := h.api("POST", "/connection", map[string]any{"password": adminPass, "phone_number_id": "1234567890",
		"access_token": strings.Repeat("A", 20000), "app_secret": "s"}); w.Code != http.StatusBadRequest {
		t.Errorf("a 20 KB token must be refused: got %d", w.Code)
	}
	for _, dir := range []string{`C:\`, `C:\Windows\System32`} {
		if w, _ := h.api("PATCH", "/settings", map[string]any{"storage": map[string]any{"backup_copy_dir": dir}}); w.Code != http.StatusBadRequest {
			t.Errorf("backup folder %s: got %d, want 400", dir, w.Code)
		}
	}
}

// S38: after "delete everything" no customer number is left in the activity log.
func TestAudit8DeleteAllLeavesNoNumbersInLog(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	security.LogAudit("OPT_OUT", "+14155553101", "Asha", "Customer opted out with \"STOP\"", "success")
	security.LogAudit("CONTACT_SAVED", "127.0.0.1", "admin", "Saved +14155553102", "success")
	if w, out := h.api("POST", "/privacy/purge", map[string]any{"password": adminPass, "all": true}); w.Code != 200 {
		t.Fatalf("delete all: %d %v", w.Code, out)
	}
	for _, e := range security.GetAuditLog(0) {
		line := fmt.Sprint(e.IP, " ", e.User, " ", e.Details)
		if strings.Contains(line, "4155553101") || strings.Contains(line, "4155553102") || e.User == "Asha" {
			t.Fatalf("a customer is still in the activity log: %s (%s)", line, e.Timestamp.Format(time.RFC3339))
		}
	}
}
