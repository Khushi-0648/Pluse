package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
)

// C3: test messages follow the same rules as the inbox: opt-outs, blocked numbers,
// practice mode and the daily limit.
func TestTestSendsFollowTheSendingRules(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_ = h.cs.Save(contacts.Contact{Phone: "919811111112"})
	h.cs.SetDND("919811111112", true, "Replied STOP")
	for _, path := range []string{"/setup/test-message", "/campaigns/test-send"} {
		if w, _ := h.api("POST", path, map[string]any{"message": "Hi", "phone": "+919811111112"}); w.Code != http.StatusConflict {
			t.Fatalf("%s: an opted-out number must not get a test: %d", path, w.Code)
		}
	}
	_ = h.cfg.Update(func(c *config.Config) error { c.Automation.BlacklistPhones = []string{"9811111113"}; return nil })
	if w, _ := h.api("POST", "/setup/test-message", map[string]any{"phone": "+919811111113"}); w.Code != http.StatusConflict {
		t.Fatalf("a blocked number must not get a test: %d", w.Code)
	}
	_ = h.cfg.Update(func(c *config.Config) error { c.Automation.DryRun = true; return nil })
	if w, out := h.api("POST", "/campaigns/test-send", map[string]any{"message": "Hi", "phone": "+919811111114"}); w.Code != 200 || out["dry_run"] != true || len(h.wa.sent) != 0 {
		t.Fatalf("practice mode must only record the test: %d %v %v", w.Code, out, h.wa.sent)
	}
	if h.cfg.Snapshot().Setup.TestMessageSentAt != nil {
		t.Fatal("a practice test does not count as a sent test message")
	}
	_ = h.cfg.Update(func(c *config.Config) error {
		c.Automation.DryRun = false
		c.Automation.DailyLimit = 1
		return nil
	})
	if w, _ := h.api("POST", "/setup/test-message", map[string]any{"phone": "+919811111114"}); w.Code != 200 || len(h.wa.sent) != 1 {
		t.Fatalf("the first test is sent: %d", w.Code)
	}
	if w, _ := h.api("POST", "/campaigns/test-send", map[string]any{"message": "Hi", "phone": "+919811111114"}); w.Code != http.StatusTooManyRequests || len(h.wa.sent) != 1 {
		t.Fatalf("a test over the daily limit must be refused: %d", w.Code)
	}
}

// uncertainWA answers every send like Meta answering 5xx: the message may have gone out.
type uncertainWA struct{ *fakeWA }

func (u *uncertainWA) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	return "", &cloudapi.APIError{HTTPStatus: 500, Plain: "WhatsApp had a problem and may or may not have sent this message."}
}

// C4: an integration send with an unknown outcome is recorded (labelled, counted) and
// answered 202 "do not retry"; an idempotency key is refused when repeated.
func TestIntegrationUnknownOutcomeAndIdempotency(t *testing.T) {
	h := newHarness(t)
	post := func(body string) *httptest.ResponseRecorder {
		return h.raw("POST", "/api/v1/webhook/send", strings.NewReader(body), map[string]string{"Content-Type": "application/json", "X-Webhook-Secret": "inbound-secret-123456"})
	}
	_ = h.cs.Save(contacts.Contact{Phone: "14155550801"})
	h.cs.RecordInbound("14155550801", time.Now())
	key := "order-42-" + time.Now().Format("150405.000000")
	body := `{"to":"14155550801","message":"Your order is ready","idempotency_key":"` + key + `"}`
	if w := post(body); w.Code != 200 || len(h.wa.sent) != 1 {
		t.Fatalf("first request is sent: %d %s", w.Code, w.Body.String())
	}
	if w := post(body); w.Code != http.StatusConflict || len(h.wa.sent) != 1 {
		t.Fatalf("a repeated idempotency key must be refused: %d %s", w.Code, w.Body.String())
	}
	h.s.Messenger = &uncertainWA{h.wa}
	w := post(`{"to":"14155550801","message":"Second","idempotency_key":"` + key + `-b"}`)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"status":"unknown"`) || !strings.Contains(w.Body.String(), "do not retry") {
		t.Fatalf("an unknown outcome must be 202 unknown: %d %s", w.Code, w.Body.String())
	}
	var rec history.MessageRecord
	for _, m := range h.hs.Query(history.Query{Phone: "14155550801"}) {
		if m.Text == "Second" {
			rec = m
		}
	}
	if rec.Status != history.StatusFailed || !rec.Uncertain || !strings.HasPrefix(rec.Label, IntegrationLabel) {
		t.Fatalf("the send must be recorded as uncertain with a label: %+v", rec)
	}
	if h.hs.SentSince(time.Now().Add(-time.Hour)) != 2 {
		t.Fatal("a send that may have gone out counts towards the daily limit")
	}
	if !h.hs.ResolveLabel(rec.Label, "wamid.LATE", history.StatusDelivered) {
		t.Fatal("Meta's report must correct the record")
	}
	if w := post(`{"to":"14155550801","message":"Second","idempotency_key":"` + key + `-b"}`); w.Code != http.StatusConflict {
		t.Fatalf("a key whose send may have gone out must not be sent again: %d", w.Code)
	}
}

func TestIdempotencyKeysAreBounded(t *testing.T) {
	k := &idempotencyKeys{}
	now := time.Now()
	for i := 0; i < idempotencyMax+5; i++ {
		k.claim(strings.Repeat("k", 1+i%7)+time.Duration(i).String(), now.Add(time.Duration(i)*time.Millisecond))
	}
	if len(k.seen) > idempotencyMax {
		t.Fatalf("the key memory grew to %d", len(k.seen))
	}
	if len(k.busy) > idempotencyMax {
		t.Fatalf("the in-progress memory grew to %d", len(k.busy))
	}
	if k.claim("old", now.Add(-25*time.Hour)) != claimNew || k.claim("old", now) != claimNew {
		t.Fatal("a key older than 24 hours may be used again")
	}
}

// C1 follow-up: a reply from the inbox to a contact whose number came from WhatsApp keeps
// the country. Singapore's 6591234567 must not become the Indian 916591234567.
func TestInboxReplyKeepsAForeignNumber(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	h.cs.Upsert(contacts.CleanWAID("6591234567"), "Wei", "whatsapp")
	h.cs.RecordInbound("6591234567", time.Now())
	w, out := h.api("POST", "/messages/send", map[string]any{"to": "6591234567", "message": "Hello"})
	if w.Code != http.StatusOK {
		t.Fatalf("send: %d %v", w.Code, out)
	}
	h.wa.mu.Lock()
	defer h.wa.mu.Unlock()
	if len(h.wa.sent) != 1 || !strings.HasPrefix(h.wa.sent[0], "6591234567:") {
		t.Fatalf("the reply must go to the Singapore number: %v", h.wa.sent)
	}
}

// S7: with a separate key for sending, the key that signs Pulse's own updates no longer
// lets another system send messages.
func TestSeparateInboundKey(t *testing.T) {
	h := newHarness(t)
	_ = h.cfg.Update(func(c *config.Config) error { c.Webhook.InboundSecret = "sending-key-abcdef12345"; return nil })
	send := func(key string) int {
		body := `{"to":"+919811111119","message":"Hi"}`
		return h.raw("POST", "/api/v1/webhook/send", strings.NewReader(body), map[string]string{"Content-Type": "application/json", "X-Webhook-Secret": key}).Code
	}
	if c := send("inbound-secret-123456"); c != http.StatusUnauthorized {
		t.Fatalf("the signing key must not authorise sends once a sending key is set: %d", c)
	}
	if c := send("sending-key-abcdef12345"); c == http.StatusUnauthorized {
		t.Fatal("the sending key must be accepted")
	}
	h.login(adminPass)
	if w, _ := h.api("PATCH", "/settings", map[string]any{"webhook": map[string]any{"enabled": false, "url": "", "secret": "inbound-secret-123456", "inbound_secret": "short", "timeout_seconds": 8, "max_retries": 5}, "password": adminPass}); w.Code != http.StatusBadRequest {
		t.Fatal("a sending key shorter than 16 characters must be refused")
	}
	if w, _ := h.api("PATCH", "/settings", map[string]any{"webhook": map[string]any{"enabled": false, "url": "", "secret": "inbound-secret-123456", "inbound_secret": "inbound-secret-123456", "timeout_seconds": 8, "max_retries": 5}, "password": adminPass}); w.Code != http.StatusBadRequest {
		t.Fatal("the sending key must differ from the signing key")
	}
}

// The valid case of TestSeparateInboundKey: a long, different sending key is saved.
func TestSendingKeyIsSaved(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	w, out := h.api("PATCH", "/settings", map[string]any{"webhook": map[string]any{"enabled": false, "url": "", "secret": "inbound-secret-123456", "inbound_secret": "sending-key-abcdef12345", "timeout_seconds": 8, "max_retries": 5}, "password": adminPass})
	if w.Code != http.StatusOK || h.cfg.Snapshot().Webhook.InboundSecret != "sending-key-abcdef12345" {
		t.Fatalf("a valid sending key must be saved: %d %v", w.Code, out)
	}
}

// U8: the Excel download follows the range chosen in Reports.
func TestMessageExportFollowsTheRange(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	h.hs.Record(history.MessageRecord{ID: "old1", Direction: history.DirectionIncoming, Type: history.TypeUserMessage, Phone: "919811111120", Text: "old one", Timestamp: time.Now().AddDate(0, 0, -40)})
	h.hs.Record(history.MessageRecord{ID: "new1", Direction: history.DirectionIncoming, Type: history.TypeUserMessage, Phone: "919811111120", Text: "new one", Timestamp: time.Now()})
	week := h.raw("GET", "/api/v1/analytics/export.csv?days=7", nil, nil).Body.String()
	all := h.raw("GET", "/api/v1/analytics/export.csv", nil, nil).Body.String()
	if !strings.Contains(week, "new one") || strings.Contains(week, "old one") || !strings.Contains(all, "old one") {
		t.Fatalf("range not applied:\n7 days: %s\nall: %s", week, all)
	}
}

// U13: dashboard files carry a fingerprint, and an unchanged file is answered with 304.
func TestAssetsRevalidateWithETag(t *testing.T) {
	h := newHarness(t)
	first := h.raw("GET", "/assets/app.css", nil, nil)
	tag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || tag == "" {
		t.Fatalf("first load: %d etag %q", first.Code, tag)
	}
	if again := h.raw("GET", "/assets/app.css", nil, map[string]string{"If-None-Match": tag}); again.Code != http.StatusNotModified || again.Body.Len() != 0 {
		t.Fatalf("unchanged file must be 304: %d", again.Code)
	}
	if stale := h.raw("GET", "/assets/app.css", nil, map[string]string{"If-None-Match": `"old"`}); stale.Code != http.StatusOK {
		t.Fatalf("an old fingerprint must get the file: %d", stale.Code)
	}
}

// N7: a file that does not exist is a 404 even with the current fingerprint.
func TestMissingAssetIsNotFoundEvenWithETag(t *testing.T) {
	h := newHarness(t)
	tag := h.raw("GET", "/assets/app.css", nil, nil).Header().Get("ETag")
	if w := h.raw("GET", "/assets/nope.js", nil, map[string]string{"If-None-Match": tag}); w.Code != http.StatusNotFound {
		t.Fatalf("missing file: %d", w.Code)
	}
}

// N4: the dashboard is told when the old shared key still lets other systems send.
func TestLegacyInboundKeyIsReported(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_, out := h.api("GET", "/settings", nil)
	if out["legacy_inbound_key"] != true {
		t.Fatalf("expected the warning flag: %v", out["legacy_inbound_key"])
	}
	_ = h.cfg.Update(func(c *config.Config) error { c.Webhook.InboundSecret = "sending-key-abcdef12345"; return nil })
	if _, out = h.api("GET", "/settings", nil); out["legacy_inbound_key"] != false {
		t.Fatal("no warning once a sending key is set")
	}
}
