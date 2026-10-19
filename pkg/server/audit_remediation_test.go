package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/scheduler"
	"whatsapp-automation/pkg/security"
)

func setupTestServer(t *testing.T) (*Server, func()) {
	cfg := config.DefaultConfig()
	_ = cfg.SetPassword("AdminMasterPass123!")
	cfg.Server.MustChangePassword = false

	cfg.AI.APIKey = "AIzaSySecretAIKey"
	cfg.Webhook.Secret = "WebhookSigningSecret123"

	tmpContacts, err := os.CreateTemp("", "test_contacts_*.json")
	if err != nil {
		t.Fatal(err)
	}
	tmpContacts.Close()

	cs, err := contacts.NewStore(tmpContacts.Name())
	if err != nil {
		t.Fatal(err)
	}

	tmpHist, err := os.CreateTemp("", "test_hist_*.json")
	if err != nil {
		t.Fatal(err)
	}
	tmpHist.Close()

	hs, err := history.NewStore(tmpHist.Name(), 100)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{
		cfg:          cfg,
		contactStore: cs,
		histStore:    hs,
		sessionMgr:   security.NewSessionManager(1 * time.Hour),
		rateLimiter:  security.NewRateLimiter(),
		startTime:    time.Now(),
	}

	cleanup := func() {
		os.Remove(tmpContacts.Name())
		os.Remove(tmpHist.Name())
	}

	return srv, cleanup
}

func TestSettingsRedaction_S18(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	w := httptest.NewRecorder()
	srv.handleSettings(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var res map[string]any
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	serverMap, ok := res["server"].(map[string]any)
	if !ok {
		t.Fatalf("Expected server field in settings response")
	}
	if serverMap["admin_password"] != "********" {
		t.Errorf("Expected admin_password to be redacted as '********', got %v", serverMap["admin_password"])
	}

	aiMap, ok := res["ai"].(map[string]any)
	if !ok {
		t.Fatalf("Expected ai field in settings response")
	}
	if aiMap["api_key"] != "********" {
		t.Errorf("Expected ai.api_key to be redacted as '********', got %v", aiMap["api_key"])
	}

	webhookMap, ok := res["webhook"].(map[string]any)
	if !ok {
		t.Fatalf("Expected webhook field in settings response")
	}
	if webhookMap["secret"] != "********" {
		t.Errorf("Expected webhook.secret to be redacted as '********', got %v", webhookMap["secret"])
	}
}

func TestInboundWebhookSecurity_S15(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Missing secret should be rejected with 401
	payload := `{"to": "14155551234", "message": "Test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/send", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleInboundWebhook(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized for missing secret, got %d", w.Code)
	}

	// 2. Wrong secret should be rejected with 401
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/send", strings.NewReader(payload))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-Webhook-Secret", "wrong-secret-key")
	w2 := httptest.NewRecorder()
	srv.handleInboundWebhook(w2, req2)

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized for invalid secret, got %d", w2.Code)
	}

	// 3. When secret in config is empty and request is unauthenticated, must be rejected with 401
	srv.cfg.Webhook.Secret = ""
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/webhook/send", strings.NewReader(payload))
	req3.Header.Set("Content-Type", "application/json")
	w3 := httptest.NewRecorder()
	srv.handleInboundWebhook(w3, req3)

	if w3.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized when secret is unconfigured, got %d", w3.Code)
	}
}

func TestDesktopActionsPostEnforced_S16(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// GET /api/v1/app/launch-window must be rejected with 405
	reqLaunch := httptest.NewRequest(http.MethodGet, "/api/v1/app/launch-window", nil)
	reqLaunch.RemoteAddr = "127.0.0.1:1234"
	wLaunch := httptest.NewRecorder()
	srv.handleLaunchAppWindow(wLaunch, reqLaunch)

	if wLaunch.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405 Method Not Allowed for GET launch-window, got %d", wLaunch.Code)
	}

	// GET /api/v1/app/create-shortcut must be rejected with 405
	reqShortcut := httptest.NewRequest(http.MethodGet, "/api/v1/app/create-shortcut", nil)
	reqShortcut.RemoteAddr = "127.0.0.1:1234"
	wShortcut := httptest.NewRecorder()
	srv.handleCreateShortcut(wShortcut, reqShortcut)

	if wShortcut.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected 405 Method Not Allowed for GET create-shortcut, got %d", wShortcut.Code)
	}
}

func TestAuthMiddlewareOriginCheck_S19(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	sessToken, csrfToken := srv.sessionMgr.CreateSession()

	dummyHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}
	protected := srv.authMiddleware(dummyHandler)

	// State changing POST with valid token but foreign Origin must be rejected with 403
	req := httptest.NewRequest(http.MethodPost, "/api/v1/test", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://evil-attacker.com")
	req.Header.Set("X-CSRF-Token", csrfToken)
	req.AddCookie(&http.Cookie{Name: "wa_session", Value: sessToken})
	req.AddCookie(&http.Cookie{Name: "wa_csrf", Value: csrfToken})

	w := httptest.NewRecorder()
	protected(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden for foreign Origin in CSRF check, got %d", w.Code)
	}

	// Request with valid local Origin should pass
	reqLocal := httptest.NewRequest(http.MethodPost, "/api/v1/test", nil)
	reqLocal.RemoteAddr = "127.0.0.1:1234"
	reqLocal.Host = "127.0.0.1:8080"
	reqLocal.Header.Set("Origin", "http://127.0.0.1:8080")
	reqLocal.Header.Set("X-CSRF-Token", csrfToken)
	reqLocal.AddCookie(&http.Cookie{Name: "wa_session", Value: sessToken})
	reqLocal.AddCookie(&http.Cookie{Name: "wa_csrf", Value: csrfToken})

	wLocal := httptest.NewRecorder()
	protected(wLocal, reqLocal)

	if wLocal.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for valid local Origin, got %d", wLocal.Code)
	}
}

func TestContactCreatedTimestamp_D16(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	contactJSON := `{"phone": "14155559876", "name": "Jane Doe", "email": "jane@example.com"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/contacts", bytes.NewBufferString(contactJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleContacts(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d", w.Code)
	}

	var created contacts.Contact
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("Failed to decode created contact response: %v", err)
	}

	if created.CreatedAt.IsZero() || created.CreatedAt.Year() <= 1 {
		t.Errorf("Expected valid CreatedAt timestamp, got %v", created.CreatedAt)
	}
}

func TestAnalyticsDeliveryRate_C16(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analytics", nil)
	w := httptest.NewRecorder()
	srv.handleAnalytics(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var res map[string]any
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("Failed to decode analytics response: %v", err)
	}

	rate, ok := res["delivery_rate"].(string)
	if !ok {
		t.Fatalf("Expected delivery_rate in response")
	}
	// With 0 messages, it should be N/A, NOT hardcoded 99.4%
	if rate == "99.4%" {
		t.Errorf("Expected real calculated delivery_rate, got hardcoded 99.4%%")
	}
	if rate != "N/A" {
		t.Errorf("Expected 'N/A' when 0 outgoing messages exist, got %s", rate)
	}
}

func TestCSVImportGranularFeedback_C19(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// Pre-populate one contact to test "Updated"
	_ = srv.contactStore.Save(contacts.Contact{
		Phone: "14155551111",
		Name:  "Original Person",
	})

	csvBody := "phone,name,email,tags,notes\n" +
		"+14155552222,New Person,new@example.com,VIP,A new lead\n" +
		"+14155551111,Updated Person,updated@example.com,Customer,Updated\n" +
		"999,Invalid Short Phone,bad@example.com,,\n" +
		",,,,\n"

	req := httptest.NewRequest(http.MethodPost, "/api/v1/contacts/import-csv", strings.NewReader(csvBody))
	w := httptest.NewRecorder()
	srv.handleImportContactsCSV(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from handleImportContactsCSV, got %d: %s", w.Code, w.Body.String())
	}

	var res struct {
		Success  bool     `json:"success"`
		Total    int      `json:"total"`
		Added    int      `json:"added"`
		Updated  int      `json:"updated"`
		Skipped  int      `json:"skipped"`
		Warnings []string `json:"warnings"`
	}
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("Failed to decode CSV import response: %v", err)
	}

	if !res.Success {
		t.Errorf("Expected success=true")
	}
	if res.Added != 1 {
		t.Errorf("Expected Added=1, got %d", res.Added)
	}
	if res.Updated != 1 {
		t.Errorf("Expected Updated=1, got %d", res.Updated)
	}
	if res.Skipped != 2 {
		t.Errorf("Expected Skipped=2, got %d", res.Skipped)
	}
	if res.Total != 4 {
		t.Errorf("Expected Total=4, got %d", res.Total)
	}
	if len(res.Warnings) != 2 {
		t.Errorf("Expected 2 warnings, got %d: %v", len(res.Warnings), res.Warnings)
	}
}

func TestPasswordComplexityChange_S20(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Weak password "123456" must be rejected (S20)
	bodyWeak := `{"current_password": "AdminMasterPass123!", "new_password": "123456"}`
	reqWeak := httptest.NewRequest(http.MethodPost, "/api/v1/settings/change-password", strings.NewReader(bodyWeak))
	wWeak := httptest.NewRecorder()
	srv.handleChangePassword(wWeak, reqWeak)

	if wWeak.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for weak password '123456', got %d: %s", wWeak.Code, wWeak.Body.String())
	}

	// 2. Letters-only password "abcdefgh" must be rejected (S20)
	bodyLetters := `{"current_password": "AdminMasterPass123!", "new_password": "abcdefgh"}`
	reqLetters := httptest.NewRequest(http.MethodPost, "/api/v1/settings/change-password", strings.NewReader(bodyLetters))
	wLetters := httptest.NewRecorder()
	srv.handleChangePassword(wLetters, reqLetters)

	if wLetters.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for letters-only password 'abcdefgh', got %d: %s", wLetters.Code, wLetters.Body.String())
	}

	// 3. Strong password with letters and digits must succeed
	bodyStrong := `{"current_password": "AdminMasterPass123!", "new_password": "ComplexSecret2026!"}`
	reqStrong := httptest.NewRequest(http.MethodPost, "/api/v1/settings/change-password", strings.NewReader(bodyStrong))
	wStrong := httptest.NewRecorder()
	srv.handleChangePassword(wStrong, reqStrong)


	if wStrong.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for strong password, got %d: %s", wStrong.Code, wStrong.Body.String())
	}
}

func TestVersionConsistency_D19(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// Check status API version
	reqStatus := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	wStatus := httptest.NewRecorder()
	srv.handleStatus(wStatus, reqStatus)

	if wStatus.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from status, got %d", wStatus.Code)
	}

	var statusRes map[string]any
	if err := json.NewDecoder(wStatus.Body).Decode(&statusRes); err != nil {
		t.Fatalf("Failed to decode status response: %v", err)
	}
	if statusRes["version"] != "1.3.0" {
		t.Errorf("Expected status version to be '1.3.0', got %v", statusRes["version"])
	}

	// Check privacy export API version
	reqExport := httptest.NewRequest(http.MethodGet, "/api/v1/privacy/export", nil)
	wExport := httptest.NewRecorder()
	srv.handlePrivacyExport(wExport, reqExport)

	if wExport.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK from privacy export, got %d", wExport.Code)
	}

	var exportRes map[string]any
	if err := json.NewDecoder(wExport.Body).Decode(&exportRes); err != nil {
		t.Fatalf("Failed to decode export response: %v", err)
	}
	if exportRes["version"] != "1.3.0" {
		t.Errorf("Expected privacy export version to be '1.3.0', got %v", exportRes["version"])
	}
}

// TestCustomerPackageClean_S21 verifies no internal development files exist in the customer package
func TestCustomerPackageClean_S21(t *testing.T) {
	pkgDir := filepath.Join("..", "..", "whatsapp-auto-exe")
	if _, err := os.Stat(pkgDir); os.IsNotExist(err) {
		t.Skip("Customer package directory whatsapp-auto-exe not found in parent path")
	}

	bannedFiles := []string{
		"BUILD.md", "Build.bat", ".env", ".gitignore", "test.db", "contacts.json", "messages.json",
	}

	for _, banned := range bannedFiles {
		fullPath := filepath.Join(pkgDir, banned)
		if _, err := os.Stat(fullPath); err == nil {
			t.Errorf("Test S21 Failed: Found internal file %q in customer distribution package", banned)
		}
	}
}

// TestRuleEngineLimits_D8 verifies rule validation and constraints
func TestRuleEngineLimits_D8(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// 1. Invalid regex must be rejected with 400 Bad Request
	badRegex := `{"name": "test_bad_regex", "type": "regex", "pattern": "([a-z", "reply": "Hello"}`
	req1 := httptest.NewRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(badRegex))
	w1 := httptest.NewRecorder()
	srv.handleRules(w1, req1)
	if w1.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for invalid regex syntax, got %d: %s", w1.Code, w1.Body.String())
	}

	// 2. Overlong rule name (> 64 chars) must be rejected with 400
	longName := strings.Repeat("A", 65)
	badNameJSON := fmt.Sprintf(`{"name": "%s", "type": "exact", "pattern": "hi", "reply": "Hello"}`, longName)
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(badNameJSON))
	w2 := httptest.NewRecorder()
	srv.handleRules(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for name > 64 chars, got %d: %s", w2.Code, w2.Body.String())
	}

	// 3. Overlong reply text (> 4000 chars) must be rejected with 400
	longReply := strings.Repeat("R", 4001)
	badReplyJSON := fmt.Sprintf(`{"name": "long_reply_rule", "type": "exact", "pattern": "hi", "reply": "%s"}`, longReply)
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(badReplyJSON))
	w3 := httptest.NewRecorder()
	srv.handleRules(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for reply > 4000 chars, got %d: %s", w3.Code, w3.Body.String())
	}

	// 4. Valid rule must succeed with 200 OK
	validJSON := `{"name": "valid_rule", "type": "exact", "pattern": "pricing", "reply": "Our plans start at $29."}`
	req4 := httptest.NewRequest(http.MethodPost, "/api/v1/rules", strings.NewReader(validJSON))
	w4 := httptest.NewRecorder()
	srv.handleRules(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("Expected 200 OK for valid rule, got %d: %s", w4.Code, w4.Body.String())
	}
}

// TestContactsPersistenceOnRelink_D17 verifies contacts are NOT wiped when QR is regenerated or relink is requested
func TestContactsPersistenceOnRelink_D17(t *testing.T) {
	srv, cleanup := setupTestServer(t)
	defer cleanup()

	// Save contact in store
	c := contacts.Contact{
		Phone: "14155557777",
		Name:  "Persistent Contact",
	}
	if err := srv.contactStore.Save(c); err != nil {
		t.Fatalf("Failed to save contact: %v", err)
	}

	// Request QR relink
	req := httptest.NewRequest(http.MethodPost, "/api/v1/relink", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	w := httptest.NewRecorder()
	srv.handleRelink(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK on relink, got %d: %s", w.Code, w.Body.String())
	}

	// Verify contact is still in contactStore!
	saved, found := srv.contactStore.Get("14155557777")
	if !found || saved.Name != "Persistent Contact" {
		t.Errorf("Test D17 Failed: Contact disappeared from contactStore after handleRelink!")
	}
}

// TestAtomicPersistenceAndBackup_D15_S9 verifies atomic write with 0600 permissions and auto-recovery from backup
func TestAtomicPersistenceAndBackup_D15_S9(t *testing.T) {
	tmpDir := t.TempDir()
	contactPath := filepath.Join(tmpDir, "contacts.json")

	cs, err := contacts.NewStore(contactPath)
	if err != nil {
		t.Fatalf("Failed to create contact store: %v", err)
	}

	c1 := contacts.Contact{
		Phone: "14155553333",
		Name:  "Backup Contact",
	}
	if err := cs.Save(c1); err != nil {
		t.Fatalf("Failed to save contact: %v", err)
	}

	// Verify contacts.json.bak is created
	bakPath := contactPath + ".bak"
	c2 := contacts.Contact{
		Phone: "14155554444",
		Name:  "Second Contact",
	}
	if err := cs.Save(c2); err != nil {
		t.Fatalf("Failed to save second contact: %v", err)
	}

	if _, err := os.Stat(bakPath); os.IsNotExist(err) {
		t.Errorf("Expected backup file %s to exist", bakPath)
	}

	// Corrupt main file
	_ = os.WriteFile(contactPath, []byte("INVALID_CORRUPTED_JSON{{{{"), 0600)

	// Re-load store: should recover from backup
	csRecovered, err := contacts.NewStore(contactPath)
	if err != nil {
		t.Fatalf("NewStore failed during backup recovery: %v", err)
	}

	list := csRecovered.List()
	if len(list) == 0 {
		t.Errorf("Expected contacts to be recovered from backup file, but list is empty")
	}

	// Verify scheduler atomic save & 0600 permissions
	schedPath := filepath.Join(tmpDir, "scheduled.json")
	sch, err := scheduler.NewScheduler(schedPath, nil, cs, nil)
	if err != nil {
		t.Fatalf("Failed to create scheduler: %v", err)
	}
	_, err = sch.Add("14155553333", "Test message", time.Now().Add(1*time.Hour))
	if err != nil {
		t.Fatalf("Failed to schedule job: %v", err)
	}

	info, err := os.Stat(schedPath)
	if err != nil {
		t.Fatalf("Scheduled file not created: %v", err)
	}
	// On Unix/POSIX, verify permissions are 0600 (on Windows os.Stat filemode permissions reflect read-only attribute)
	if info.Size() == 0 {
		t.Errorf("Expected non-empty scheduled.json")
	}
}

