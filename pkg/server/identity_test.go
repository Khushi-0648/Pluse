package server

import (
	"net/http"
	"strings"
	"testing"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/security"
)

// F2: an integration send to +6591234567 saves the Singapore number it went to and
// leaves the opted-out Indian contact 916591234567 as it was.
func TestIntegrationSendSavesExactNumber(t *testing.T) {
	h := newHarness(t)
	_ = h.cfg.Update(func(c *config.Config) error { c.Automation.RequireOptIn = false; return nil })
	if err := h.cs.Save(contacts.Contact{Phone: "+91 65912 34567", Name: "Ravi"}); err != nil {
		t.Fatal(err)
	}
	h.cs.SetDND("916591234567", true, "Replied STOP")
	w := h.raw("POST", "/api/v1/webhook/send", strings.NewReader(`{"to":"+6591234567","name":"SG","whatsapp_template":{"name":"hello","language":"en"}}`),
		map[string]string{"Content-Type": "application/json", "X-Webhook-Secret": "inbound-secret-123456"})
	if w.Code != http.StatusOK {
		t.Fatalf("send: %d %s", w.Code, w.Body.String())
	}
	if in, ok := h.cs.GetExact("916591234567"); !ok || in.Name != "Ravi" || !in.IsDND {
		t.Fatalf("the opted-out Indian contact was overwritten: %+v", in)
	}
	if sg, ok := h.cs.GetExact("6591234567"); !ok || sg.Name != "SG" {
		t.Fatalf("the number the message went to must be saved: %+v", sg)
	}
}

// F10: an export for a number that is not valid or not known is refused, never answered
// with everyone's data.
func TestPrivacyExportBadNumberIsRefused(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_ = h.cs.Save(contacts.Contact{Phone: "14155550990", Name: "Someone"})
	for _, q := range []string{"abc", "%2B14155550991"} {
		w, _ := h.api("GET", "/privacy/export?phone="+q, nil)
		if w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "14155550990") {
			t.Fatalf("export of %q: %d %s", q, w.Code, w.Body.String())
		}
	}
	if w, _ := h.api("GET", "/privacy/export?phone=%2B14155550990", nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Someone") {
		t.Fatalf("a known number exports: %d", w.Code)
	}
}

// F8: erasing the Singapore customer 6591234567 also masks the activity-log entries
// Pulse 2.0 wrote for them under 916591234567.
func TestForgetRedactsLegacyFormInAuditLog(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	h.cs.Upsert("6591234567", "Wei", "whatsapp")
	security.LogAudit("OPT_OUT", "+916591234567", "Wei", "Contact +916591234567 replied STOP", "success")
	if w, out := h.api("POST", "/privacy/forget-contact", map[string]any{"phone": "+6591234567", "password": adminPass}); w.Code != 200 {
		t.Fatalf("forget: %d %v", w.Code, out)
	}
	for _, e := range security.GetAuditLog(0) {
		if strings.Contains(e.IP+e.Details, "916591234567") {
			t.Fatalf("the activity log still names the erased customer: %+v", e)
		}
	}
}
