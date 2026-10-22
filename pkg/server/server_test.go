package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/scheduler"
	"whatsapp-automation/pkg/security"
	"whatsapp-automation/pkg/templates"
	"whatsapp-automation/pkg/webhook"
)

const adminPass = "river7Lamp"

type fakeWA struct {
	mu        sync.Mutex
	connected bool
	sent      []string
	resets    int
	webhooks  [][]byte
	probes    []string
}

func (f *fakeWA) GetStatus() cloudapi.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloudapi.Status{Connected: f.connected, Configured: f.connected, Phone: "919800000001"}
}
func (f *fakeWA) Check(ctx context.Context) (cloudapi.Status, error) { return f.GetStatus(), nil }
func (f *fakeWA) Probe(ctx context.Context, version, phoneID, token string) (cloudapi.PhoneInfo, error) {
	f.mu.Lock()
	f.probes = append(f.probes, version+"|"+phoneID+"|"+token)
	f.mu.Unlock()
	if token == "bad" {
		return cloudapi.PhoneInfo{}, errors.New("The access token has expired or is wrong.")
	}
	return cloudapi.PhoneInfo{DisplayPhone: "+91 98000 00001", VerifiedName: "Asha Stores", QualityRating: "GREEN"}, nil
}
func (f *fakeWA) Forget() {
	f.mu.Lock()
	f.resets++
	f.connected = false
	f.mu.Unlock()
}
func (f *fakeWA) HandleWebhook(body []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !json.Valid(body) {
		return 0, errors.New("bad json")
	}
	f.webhooks = append(f.webhooks, body)
	return 1, nil
}
func (f *fakeWA) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.connected {
		return "", errors.New("offline")
	}
	f.sent = append(f.sent, to+":"+text)
	return "msg" + to, nil
}
func (f *fakeWA) SendMediaMessage(ctx context.Context, to string, data []byte, name, mime, caption string) (string, error) {
	return f.SendTextMessage(ctx, to, "[media "+name+"] "+caption)
}

type harness struct {
	t    *testing.T
	s    *Server
	wa   *fakeWA
	cfg  *config.Config
	cs   *contacts.Store
	hs   *history.Store
	sess string
	csrf string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	if err := cfg.SetPassword(adminPass); err != nil {
		t.Fatal(err)
	}
	_ = cfg.Update(func(c *config.Config) error {
		c.Automation.WarmupMode = false
		c.Webhook.Secret = "inbound-secret-123456"
		return nil
	})
	cs, _ := contacts.NewStore(filepath.Join(dir, "c.json"))
	hs, _ := history.NewStore(filepath.Join(dir, "h.json"), 1000)
	ts, _ := templates.NewStore(filepath.Join(dir, "t.json"))
	wa := &fakeWA{connected: true}
	runner, _ := campaign.NewRunner(filepath.Join(dir, "camp.json"), wa, cfg, cs, hs)
	sch, _ := scheduler.NewScheduler(filepath.Join(dir, "s.json"), wa, cfg, cs, hs)
	s := New(Deps{Config: cfg, Messenger: wa, Conn: wa, Contacts: cs, Templates: ts, History: hs, Scheduler: sch, Campaigns: runner, Hooks: webhook.New(cfg, ""), DataDir: dir})
	t.Cleanup(func() { s.sessions.Close() })
	return &harness{t: t, s: s, wa: wa, cfg: cfg, cs: cs, hs: hs}
}

func (h *harness) raw(method, target string, body io.Reader, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	req.Host = "127.0.0.1:8085"
	req.RemoteAddr = "127.0.0.1:50000"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if h.sess != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: h.sess})
	}
	w := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, req)
	return w
}

func (h *harness) api(method, path string, body any) (*httptest.ResponseRecorder, map[string]any) {
	var rd io.Reader
	hdr := map[string]string{"X-CSRF-Token": h.csrf}
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
		hdr["Content-Type"] = "application/json"
	}
	w := h.raw(method, "/api/v1"+path, rd, hdr)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func (h *harness) login(pass string) int {
	h.sess = ""
	w := h.raw("POST", "/login", strings.NewReader(url.Values{"password": {pass}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			h.sess = c.Value
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				h.t.Fatal("session cookie must be HttpOnly and SameSite=Strict")
			}
		}
	}
	if h.sess != "" {
		_, me := h.api("GET", "/me", nil)
		h.csrf, _ = me["csrf"].(string)
	}
	return w.Code
}

func TestLoginAndPages(t *testing.T) {
	h := newHarness(t)
	if w := h.raw("GET", "/", nil, nil); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Fatalf("signed-out users must be sent to /login, got %d", w.Code)
	}
	if code := h.login("wrong"); code != http.StatusUnauthorized || h.sess != "" {
		t.Fatal("wrong password accepted")
	}
	if code := h.login(adminPass); code != http.StatusSeeOther || h.sess == "" || h.csrf == "" {
		t.Fatal("sign-in failed")
	}
	w := h.raw("GET", "/", nil, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "/assets/js/core.js") {
		t.Fatalf("dashboard not served: %d", w.Code)
	}
	if csp := w.Header().Get("Content-Security-Policy"); strings.Contains(csp, "unsafe-inline") || csp == "" {
		t.Fatalf("CSP wrong: %q", csp)
	}
	if w := h.raw("GET", "/assets/js/core.js", nil, nil); w.Code != 200 {
		t.Fatal("assets not served")
	}
}

func TestRequestProtections(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	if w := h.raw("GET", "/api/v1/me", nil, map[string]string{}); w.Code != 200 {
		t.Fatal("me should work")
	}
	req := httptest.NewRequest("GET", "/api/v1/me", nil)
	req.Host = "evil.example"
	w := httptest.NewRecorder()
	h.s.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatal("foreign Host header must be rejected (S17)")
	}
	if w := h.raw("POST", "/api/v1/contacts/dnd", strings.NewReader(`{}`), map[string]string{"Content-Type": "application/json"}); w.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF token must be rejected, got %d", w.Code)
	}
	if w := h.raw("POST", "/api/v1/contacts/dnd", strings.NewReader(`{}`), map[string]string{"X-CSRF-Token": h.csrf, "Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Fatal("foreign Origin must be rejected (S19)")
	}
	if w := h.raw("GET", "/logout", nil, nil); w.Code == 200 {
		t.Fatal("GET must not sign out (CSRF)")
	}
	h.sess = ""
	if w, _ := h.api("GET", "/overview", nil); w.Code != http.StatusUnauthorized {
		t.Fatal("API must require sign-in")
	}
}

func TestFirstPasswordGate(t *testing.T) {
	h := newHarness(t)
	gen := ""
	_ = h.cfg.Update(func(c *config.Config) error { c.Server.AdminPassword = ""; return nil })
	gen, _ = h.cfg.EnsureAdminPassword()
	h.login(gen)
	if w, _ := h.api("GET", "/overview", nil); w.Code != http.StatusForbidden {
		t.Fatal("API must be blocked until the password is changed")
	}
	if w := h.raw("GET", "/", nil, nil); w.Header().Get("Location") != "/setup-password" {
		t.Fatal("dashboard should send the user to choose a password")
	}
	w := h.raw("POST", "/setup-password", strings.NewReader(url.Values{"new_password": {"Admin1234"}, "confirm_password": {"Admin1234"}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if w.Code != http.StatusBadRequest {
		t.Fatal("weak password must be rejected by the server (S20)")
	}
	w = h.raw("POST", "/setup-password", strings.NewReader(url.Values{"new_password": {"harbor42Blue"}, "confirm_password": {"harbor42Blue"}}.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if w.Code != http.StatusSeeOther || h.cfg.MustChange() {
		t.Fatal("password setup failed")
	}
	if w, _ := h.api("GET", "/overview", nil); w.Code != 200 {
		t.Fatal("API should open after the password is chosen")
	}
}

func TestOperatorRole(t *testing.T) {
	h := newHarness(t)
	_ = h.cfg.SetOperatorPassword("staff42Desk")
	h.login("staff42Desk")
	if w, _ := h.api("GET", "/contacts", nil); w.Code != 200 {
		t.Fatal("staff can see contacts")
	}
	if w, _ := h.api("PATCH", "/settings", map[string]any{"business": map[string]any{"name": "x"}}); w.Code != http.StatusForbidden {
		t.Fatal("staff must not change settings")
	}
	if w, _ := h.api("GET", "/connection", nil); w.Code != http.StatusForbidden {
		t.Fatal("staff must not see the WhatsApp connection details")
	}
	if w, _ := h.api("GET", "/settings", nil); w.Code != http.StatusForbidden {
		t.Fatal("staff must not read the settings (S25)")
	}
}

func TestSettingsPartialUpdate(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	before := h.cfg.Snapshot().Automation
	if w, out := h.api("PATCH", "/settings", map[string]any{"automation": map[string]any{"dry_run": true}}); w.Code != 200 {
		t.Fatalf("patch failed: %v", out)
	}
	after := h.cfg.Snapshot().Automation
	if !after.DryRun || after.IgnoreGroups != before.IgnoreGroups || after.DailyLimit != before.DailyLimit || after.CooldownSeconds != before.CooldownSeconds || after.Pace != before.Pace {
		t.Fatalf("a partial update changed other settings (D20): %+v", after)
	}
	if w, _ := h.api("PATCH", "/settings", map[string]any{"automation": map[string]any{"daily_limit": 0}}); w.Code != http.StatusBadRequest {
		t.Fatal("daily limit 0 must be rejected")
	}
	if w, out := h.api("PATCH", "/settings", map[string]any{"automation": map[string]any{"enabled": true}}); w.Code != http.StatusBadRequest || !strings.Contains(out["error"].(string), "business name") {
		t.Fatalf("automation must not switch on before setup: %v", out)
	}
	if w, _ := h.api("PATCH", "/settings", map[string]any{"automation": map[string]any{"pace": "fast"}}); w.Code != 200 || h.cfg.Snapshot().Automation.DailyLimit != 800 {
		t.Fatal("pace preset not applied")
	}
	if w, _ := h.api("PATCH", "/settings", map[string]any{"webhook": map[string]any{"url": "http://0.0.0.0:8085/x"}}); w.Code != http.StatusBadRequest {
		t.Fatal("webhook to 0.0.0.0 must be rejected (S22)")
	}
	if w, _ := h.api("PATCH", "/settings", map[string]any{"server": map[string]any{"host": "0.0.0.0"}}); w.Code != http.StatusBadRequest {
		t.Fatal("server section must not be editable through the API")
	}
	_, got := h.api("GET", "/settings", nil)
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "$2a$") || strings.Contains(string(raw), "inbound-secret") {
		t.Fatal("settings must not expose the password hash or secrets (S18)")
	}
}

func TestRulesAndTemplates(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	if w, _ := h.api("POST", "/rules", map[string]any{"name": "Delivery", "pattern": "delivery", "reply": "2 days", "enabled": false}); w.Code != 200 {
		t.Fatal("rule save failed")
	}
	for _, r := range h.cfg.GetRules() {
		if r.Name == "Delivery" && r.Enabled {
			t.Fatal("enabled:false must be honoured (D25)")
		}
	}
	if w, _ := h.api("POST", "/rules/toggle", map[string]any{"name": "Delivery", "enabled": true}); w.Code != 200 {
		t.Fatal("toggle failed")
	}
	if w, _ := h.api("POST", "/rules", map[string]any{"name": "Bad", "pattern": "x", "reply": "Use {{coupon_code}}"}); w.Code != http.StatusBadRequest {
		t.Fatal("unknown variables in a rule must be rejected (C23)")
	}
	if w, _ := h.api("POST", "/templates", map[string]any{"name": "Promo", "content": "Code {{promo_link}}"}); w.Code != http.StatusBadRequest {
		t.Fatal("unknown variables in a template must be rejected (C23)")
	}
	w, out := h.api("POST", "/rules/test", map[string]any{"message": "talk to a person"})
	if w.Code != 200 || out["handover"] != true {
		t.Fatalf("rule tester wrong: %v", out)
	}
}

func multipartBody(t *testing.T, fields map[string]string, fileName string, data []byte) (*bytes.Buffer, string) {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	if fileName != "" {
		fw, _ := mw.CreateFormFile("file", fileName)
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	return &b, mw.FormDataContentType()
}

func TestSendPolicyAndAttachments(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_ = h.cs.Save(contacts.Contact{Phone: "14155550401", Name: "Cold", Consent: true, ConsentSource: "form"})
	// Outside the 24-hour window WhatsApp only accepts approved templates: free text is
	// refused before anything is sent, even with an opt-in.
	if w, out := h.api("POST", "/messages/send", map[string]any{"to": "+14155550401", "message": "hi"}); w.Code != http.StatusConflict || !strings.Contains(fmt.Sprint(out["error"]), "24 hours") || len(h.wa.sent) != 0 {
		t.Fatalf("free text outside the window must be refused: %d %v", w.Code, out)
	}
	h.cs.RecordInbound("14155550401", time.Now())
	if w, _ := h.api("POST", "/messages/send", map[string]any{"to": "+14155550401", "message": "Hello {{first_name}}"}); w.Code != 200 {
		t.Fatal("a send inside the window should work")
	}
	if len(h.wa.sent) != 1 || !strings.HasSuffix(h.wa.sent[0], "Hello Cold") {
		t.Fatalf("message not sent or not rendered: %v", h.wa.sent)
	}
	h.cs.SetDND("14155550401", true, "STOP")
	if w, _ := h.api("POST", "/messages/send", map[string]any{"to": "14155550401", "message": "hi", "confirm_outside_window": true}); w.Code != http.StatusConflict {
		t.Fatal("opted-out customers must be blocked")
	}

	h.cs.RecordInbound("14155550402", time.Now())
	_ = h.cs.Save(contacts.Contact{Phone: "14155550402"})
	h.cs.RecordInbound("14155550402", time.Now())
	fake := append([]byte("MZ"), make([]byte, 100)...)
	body, ct := multipartBody(t, map[string]string{"to": "14155550402", "message": "file"}, "evil.png", fake)
	if w := h.raw("POST", "/api/v1/messages/send", body, map[string]string{"Content-Type": ct, "X-CSRF-Token": h.csrf}); w.Code != http.StatusBadRequest {
		t.Fatalf("disguised executable accepted (S14): %d", w.Code)
	}
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 100)...)
	body, ct = multipartBody(t, map[string]string{"to": "14155550402", "message": "photo"}, "a.png", png)
	if w := h.raw("POST", "/api/v1/messages/send", body, map[string]string{"Content-Type": ct, "X-CSRF-Token": h.csrf}); w.Code != 200 {
		t.Fatalf("valid image rejected: %d %s", w.Code, w.Body.String())
	}
	big := append(append([]byte{}, png...), make([]byte, security.MaxAttachmentBytes+10)...)
	body, ct = multipartBody(t, map[string]string{"to": "14155550402"}, "big.png", big)
	if w := h.raw("POST", "/api/v1/messages/send", body, map[string]string{"Content-Type": ct, "X-CSRF-Token": h.csrf}); w.Code == 200 {
		t.Fatal("oversized file accepted")
	}
}

func TestInboundWebhookAuth(t *testing.T) {
	h := newHarness(t)
	payload := `{"to":"14155550501","message":"Order {{order_id}} shipped","variables":{"order_id":"A1"},"consent":true,"consent_source":"checkout"}`
	if w := h.raw("POST", "/api/v1/webhook/send", strings.NewReader(payload), map[string]string{"Content-Type": "application/json"}); w.Code != http.StatusUnauthorized {
		t.Fatal("no secret must be rejected (S15)")
	}
	if w := h.raw("POST", "/api/v1/webhook/send", strings.NewReader(payload), map[string]string{"Content-Type": "application/json", "X-Webhook-Secret": "wrong"}); w.Code != http.StatusUnauthorized {
		t.Fatal("wrong secret must be rejected")
	}
	if w := h.raw("POST", "/api/v1/webhook/send?secret=inbound-secret-123456", strings.NewReader(payload), map[string]string{"Content-Type": "application/json"}); w.Code != http.StatusUnauthorized {
		t.Fatal("secrets in the query string must not be accepted")
	}
	old := fmt.Sprint(time.Now().Add(-time.Hour).Unix())
	oldSig := "sha256=" + security.SignWebhookHMAC("inbound-secret-123456", []byte(old+"."+payload))
	if w := h.raw("POST", "/api/v1/webhook/send", strings.NewReader(payload), map[string]string{"Content-Type": "application/json", "X-Signature-SHA256": oldSig, "X-Webhook-Timestamp": old}); w.Code != http.StatusUnauthorized {
		t.Fatal("an old signed request must not be accepted again (replay)")
	}
	ts := fmt.Sprint(time.Now().Unix())
	sig := "sha256=" + security.SignWebhookHMAC("inbound-secret-123456", []byte(ts+"."+payload))
	signed := map[string]string{"Content-Type": "application/json", "X-Signature-SHA256": sig, "X-Webhook-Timestamp": ts}
	if w := h.raw("POST", "/api/v1/webhook/send", strings.NewReader(payload), signed); w.Code != http.StatusConflict || len(h.wa.sent) != 0 {
		t.Fatalf("consent declared by the caller must not open the 24-hour window: %d %s", w.Code, w.Body.String())
	}
	if c, _ := h.cs.Get("14155550501"); c == nil || c.Consent {
		t.Fatal("consent declared by the caller must not be recorded")
	}
	h.cs.SetConsent("14155550501", true, "Signed up in store")
	h.cs.RecordInbound("14155550501", time.Now()) // the customer wrote: free text is allowed
	ts = fmt.Sprint(time.Now().Unix() + 1)
	sig = "sha256=" + security.SignWebhookHMAC("inbound-secret-123456", []byte(ts+"."+payload))
	signed = map[string]string{"Content-Type": "application/json", "X-Signature-SHA256": sig, "X-Webhook-Timestamp": ts}
	w := h.raw("POST", "/api/v1/webhook/send", strings.NewReader(payload), signed)
	if w.Code != 200 || len(h.wa.sent) != 1 || !strings.Contains(h.wa.sent[0], "Order A1 shipped") {
		t.Fatalf("signed request failed: %d %s %v", w.Code, w.Body.String(), h.wa.sent)
	}
	if w := h.raw("POST", "/api/v1/webhook/send", strings.NewReader(payload), signed); w.Code != http.StatusConflict || len(h.wa.sent) != 1 {
		t.Fatalf("the same signed request sent again must be refused (replay): %d", w.Code)
	}
	form := "to=14155550501&message=hi"
	if w := h.raw("POST", "/api/v1/webhook/send", strings.NewReader(form), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "X-Webhook-Secret": "inbound-secret-123456"}); w.Code != http.StatusUnsupportedMediaType {
		t.Fatal("form bodies must be refused")
	}
	cold := `{"to":"14155550502","message":"hi"}`
	if w := h.raw("POST", "/api/v1/webhook/send", strings.NewReader(cold), map[string]string{"Content-Type": "application/json", "X-Webhook-Secret": "inbound-secret-123456"}); w.Code != http.StatusConflict {
		t.Fatal("integration messages to customers without opt-in must be refused")
	}
}

func TestDisconnectNeedsPasswordAndKeepsData(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_ = h.cfg.Update(func(c *config.Config) error {
		c.CloudAPI.PhoneNumberID, c.CloudAPI.AccessToken = "1234567", "EAAG-token"
		return nil
	})
	_ = h.cs.Save(contacts.Contact{Phone: "14155550601", Name: "Keep"})
	h.hs.Record(history.MessageRecord{ID: "k1", Direction: history.DirectionIncoming, Phone: "14155550601", Text: "hi"})
	if w, _ := h.api("POST", "/connection/disconnect", map[string]any{"password": "nope"}); w.Code != http.StatusForbidden || h.wa.resets != 0 {
		t.Fatal("disconnecting must require the admin password (S7)")
	}
	if w, _ := h.api("POST", "/connection/disconnect", map[string]any{"password": adminPass}); w.Code != 200 || h.wa.resets != 1 {
		t.Fatal("disconnect failed")
	}
	if ca := h.cfg.Snapshot().CloudAPI; ca.AccessToken != "" || ca.PhoneNumberID != "" {
		t.Fatalf("the Business API details must be removed: %+v", ca)
	}
	if h.cs.Count() != 1 || len(h.hs.ExportAll()) != 1 {
		t.Fatal("unlinking without delete_data must keep contacts and messages (D17)")
	}
	if w, _ := h.api("GET", "/contacts", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "Keep") {
		t.Fatal("contacts must stay visible while WhatsApp is disconnected (D17)")
	}
	h.wa.connected = true
	if w, _ := h.api("POST", "/logout", nil); w.Code != 200 || h.cs.Count() != 1 || len(h.hs.ExportAll()) != 1 || h.wa.resets != 1 {
		t.Fatal("signing out of the dashboard must only end the session: WhatsApp, contacts and chats stay")
	}
	if w, _ := h.api("GET", "/overview", nil); w.Code != http.StatusUnauthorized {
		t.Fatal("session should be gone after sign-out")
	}
}

func TestCampaignPreviewAndOverview(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_ = h.cs.Save(contacts.Contact{Phone: "14155550701", Tags: []string{"VIP"}})
	w, out := h.api("POST", "/campaigns/preview", map[string]any{"message": "Hi {{first_name}}", "audience": map[string]any{"tag": "VIP"}})
	if w.Code != 200 || out["recipients"].(float64) != 0 || !strings.Contains(w.Body.String(), "No opt-in recorded") {
		t.Fatalf("preview should explain who is left out (C20): %s", w.Body.String())
	}
	h.hs.Record(history.MessageRecord{ID: "imp", Direction: history.DirectionIncoming, Type: history.TypeHistorySync, Phone: "1", Timestamp: time.Now()})
	w, out = h.api("GET", "/overview", nil)
	if w.Code != 200 || out["setup"] == nil || out["health"] == nil {
		t.Fatalf("overview incomplete: %v", out)
	}
	if today := out["today"].(map[string]any); today["received"].(float64) != 0 {
		t.Fatal("imported history must not count as activity (C16)")
	}
	w, _ = h.api("GET", "/analytics?days=7", nil)
	if !strings.Contains(w.Body.String(), `"imported_excluded":1`) {
		t.Fatalf("analytics should state excluded imported messages: %s", w.Body.String())
	}
}

// The business number cannot message itself, so tests go to the owner's own phone.
func TestTestMessageGoesToOwnersPhone(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	h.wa.connected = false
	if w, _ := h.api("POST", "/setup/test-message", map[string]any{"phone": "+919811111111"}); w.Code != http.StatusConflict {
		t.Fatal("test message needs a connection")
	}
	h.wa.connected = true
	if w, _ := h.api("POST", "/setup/test-message", map[string]any{"phone": "+919800000001"}); w.Code != http.StatusBadRequest {
		t.Fatal("the business number cannot be the test recipient")
	}
	if w, _ := h.api("POST", "/setup/test-message", nil); w.Code != http.StatusBadRequest {
		t.Fatal("a phone number is required")
	}
	if w, _ := h.api("POST", "/setup/test-message", map[string]any{"phone": "+91 98111 11111"}); w.Code != 200 || h.cfg.Snapshot().Setup.TestMessageSentAt == nil || !strings.HasPrefix(h.wa.sent[0], "919811111111:") {
		t.Fatalf("test message not sent or not recorded: %v", h.wa.sent)
	}
	if w, _ := h.api("POST", "/campaigns/test-send", map[string]any{"message": "Hi {{first_name}}", "phone": "+919811111111"}); w.Code != 200 || !strings.Contains(h.wa.sent[1], "[Test] Hi Asha") {
		t.Fatalf("campaign test not sent: %d %v", w.Code, h.wa.sent)
	}
}

func TestAPIKeyCannotDoOwnerActions(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_, out := h.api("POST", "/settings/api-key", map[string]any{"password": adminPass, "action": "generate"})
	key, _ := out["api_key"].(string)
	if key == "" || strings.Contains(h.cfg.Snapshot().Server.APIKey, key) {
		t.Fatalf("the API key must be shown once and stored only as a hash: %v", out)
	}
	h.sess, h.csrf = "", ""
	call := func(method, path, body string) int {
		hdr := map[string]string{"X-API-Key": key, "Content-Type": "application/json"}
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		return h.raw(method, "/api/v1"+path, rd, hdr).Code
	}
	if c := call("GET", "/contacts", ""); c != 200 {
		t.Fatalf("the API key must still read data: %d", c)
	}
	h.cs.RecordInbound("14155550801", time.Now())
	_ = h.cs.Save(contacts.Contact{Phone: "14155550801"})
	h.cs.RecordInbound("14155550801", time.Now())
	if c := call("POST", "/messages/send", `{"to":"14155550801","message":"hi"}`); c != 200 {
		t.Fatalf("the API key must still send messages: %d", c)
	}
	for _, rq := range [][3]string{
		{"PATCH", "/settings", `{"business":{"name":"x"}}`},
		{"GET", "/backup", ""},
		{"POST", "/backups/now", "{}"},
		{"POST", "/restore", "{}"},
		{"GET", "/privacy/export", ""},
		{"POST", "/privacy/purge", "{}"},
		{"GET", "/contacts/export.csv", ""},
		{"POST", "/settings/password", "{}"},
		{"POST", "/settings/operator-password", "{}"},
		{"POST", "/settings/api-key", `{"action":"revoke"}`},
		{"POST", "/connection", `{"phone_number_id":"123456"}`},
		{"POST", "/connection/disconnect", "{}"},
		{"POST", "/privacy/delete-old-session", "{}"},
	} {
		if c := call(rq[0], rq[1], rq[2]); c != http.StatusForbidden {
			t.Errorf("%s %s with the API key: got %d, want 403", rq[0], rq[1], c)
		}
	}
}

func TestDirectSendPracticeModeAndDailyLimit(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_ = h.cs.Save(contacts.Contact{Phone: "14155550901"})
	h.cs.RecordInbound("14155550901", time.Now())
	_ = h.cfg.Update(func(c *config.Config) error { c.Automation.DryRun = true; return nil })
	w, out := h.api("POST", "/messages/send", map[string]any{"to": "14155550901", "message": "practice"})
	if w.Code != 200 || out["dry_run"] != true || len(h.wa.sent) != 0 {
		t.Fatalf("practice mode must not send: %d %v %v", w.Code, out, h.wa.sent)
	}
	recs := h.hs.ExportAll()
	if len(recs) != 1 || recs[0].Status != history.StatusDryRun || !strings.HasPrefix(recs[0].ID, "dry_") {
		t.Fatalf("practice send must be recorded as test only: %+v", recs)
	}
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 100)...)
	body, ct := multipartBody(t, map[string]string{"to": "14155550901", "message": "photo"}, "a.png", png)
	if w := h.raw("POST", "/api/v1/messages/send", body, map[string]string{"Content-Type": ct, "X-CSRF-Token": h.csrf}); w.Code != 200 || len(h.wa.sent) != 0 {
		t.Fatalf("practice mode must not send attachments: %d %v", w.Code, h.wa.sent)
	}
	_ = h.cfg.Update(func(c *config.Config) error { c.Automation.DryRun = false; c.Automation.DailyLimit = 1; return nil })
	if w, _ := h.api("POST", "/messages/send", map[string]any{"to": "14155550901", "message": "one"}); w.Code != 200 {
		t.Fatalf("first send under the limit failed: %d", w.Code)
	}
	if w, _ := h.api("POST", "/messages/send", map[string]any{"to": "14155550901", "message": "two"}); w.Code != http.StatusTooManyRequests || len(h.wa.sent) != 1 {
		t.Fatalf("the daily limit must stop direct sends: %d %v", w.Code, h.wa.sent)
	}
}

func TestOnlyAdminClearsOptOut(t *testing.T) {
	h := newHarness(t)
	_ = h.cfg.SetOperatorPassword("staff42Desk")
	_ = h.cs.Save(contacts.Contact{Phone: "14155551001", Name: "Stopped"})
	h.cs.SetDND("14155551001", true, "Replied STOP")
	h.login("staff42Desk")
	if w, _ := h.api("POST", "/contacts/dnd", map[string]any{"phone": "14155551001", "dnd": false}); w.Code != http.StatusForbidden {
		t.Fatalf("staff must not clear an opt-out: %d", w.Code)
	}
	if w, _ := h.api("POST", "/contacts/consent", map[string]any{"phone": "14155551001", "consent": true, "source": "asked"}); w.Code != http.StatusForbidden {
		t.Fatalf("staff must not lift an opt-out by recording consent: %d", w.Code)
	}
	if w, _ := h.api("POST", "/contacts", map[string]any{"phone": "14155551001", "name": "Stopped", "is_dnd": false}); w.Code != http.StatusForbidden {
		t.Fatalf("staff must not clear an opt-out by editing the contact: %d", w.Code)
	}
	if w, _ := h.api("POST", "/contacts", map[string]any{"phone": "14155551001", "name": "Renamed", "is_dnd": true}); w.Code != 200 {
		t.Fatalf("staff can still edit an opted-out contact's details: %d", w.Code)
	}
	if c, _ := h.cs.Get("14155551001"); !c.IsDND {
		t.Fatal("opt-out was cleared")
	}
	_ = h.cs.Save(contacts.Contact{Phone: "14155551002"})
	if w, _ := h.api("POST", "/contacts/dnd", map[string]any{"phone": "14155551002", "dnd": true}); w.Code != 200 {
		t.Fatal("staff can mark a customer do-not-message")
	}
	h.login(adminPass)
	if w, _ := h.api("POST", "/contacts/dnd", map[string]any{"phone": "14155551001", "dnd": false}); w.Code != 200 {
		t.Fatal("the admin can clear an opt-out")
	}
}

func TestBackupCopyFolderMustBeLocal(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	for _, dir := range []string{`\\server\share`, `//server/share`, `\\?\UNC\server\share`} {
		w, out := h.api("PATCH", "/settings", map[string]any{"storage": map[string]any{"backup_copy_dir": dir}})
		if w.Code != http.StatusBadRequest || !strings.Contains(fmt.Sprint(out["error"]), "folder on this computer") {
			t.Errorf("network folder %q must be refused: %d %v", dir, w.Code, out)
		}
	}
}
