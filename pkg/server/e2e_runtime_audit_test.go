package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/bot"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/security"
)

type testMemoryTransport struct {
	handler http.Handler
}

func (m *testMemoryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	reqCopy := req.Clone(req.Context())
	if reqCopy.RemoteAddr == "" {
		reqCopy.RemoteAddr = "127.0.0.1:12345"
	}
	if reqCopy.Host == "" {
		reqCopy.Host = "127.0.0.1:8085"
	}
	m.handler.ServeHTTP(rec, reqCopy)
	res := rec.Result()
	res.Request = req
	return res, nil
}

// TestE2E_FullAuditSuite verifies all 14 runtime test cases from the Auditor's findings register
func TestE2E_FullAuditSuite(t *testing.T) {
	// Configure test environment with strict credentials
	cfg := config.DefaultConfig()
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 8085
	// Default password for test fixture
	cfg.Server.AdminPassword = "admin"
	cfg.Server.MustChangePassword = false
	cfg.AI.APIKey = "AIzaSySecretTestKey123"
	cfg.Webhook.Secret = "WebhookSigningSecret123"

	tmpContacts, err := os.CreateTemp("", "e2e_contacts_*.json")
	if err != nil {
		t.Fatal(err)
	}
	tmpContacts.Close()
	defer os.Remove(tmpContacts.Name())

	cs, err := contacts.NewStore(tmpContacts.Name())
	if err != nil {
		t.Fatal(err)
	}

	tmpHist, err := os.CreateTemp("", "e2e_hist_*.json")
	if err != nil {
		t.Fatal(err)
	}
	tmpHist.Close()
	defer os.Remove(tmpHist.Name())

	hs, err := history.NewStore(tmpHist.Name(), 100)
	if err != nil {
		t.Fatal(err)
	}

	srv := NewServer(cfg, nil, cs, nil, hs, nil, nil)
	baseURL := "http://127.0.0.1:8085"

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	memTransport := &testMemoryTransport{handler: srv.httpServer.Handler}

	// HTTP client that does NOT automatically follow redirects so we can inspect 303s
	noRedirectClient := &http.Client{
		Jar:       jar,
		Transport: memTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// -------------------------------------------------------------
	// TEST 1: Server Reachability (Status & UI)
	// -------------------------------------------------------------
	t.Run("Test01_ServerReachability", func(t *testing.T) {
		resp, err := noRedirectClient.Get(baseURL + "/login")
		if err != nil {
			t.Fatalf("Test 1 Failed: Cannot connect to server: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Test 1 Failed: Expected 200 OK on /login, got %d", resp.StatusCode)
		}
	})

	// -------------------------------------------------------------
	// TEST 2: Unauthenticated Access Redirects
	// -------------------------------------------------------------
	t.Run("Test02_UnauthenticatedRedirect", func(t *testing.T) {
		// Root "/" should redirect 303 to /login
		resp, err := noRedirectClient.Get(baseURL + "/")
		if err != nil {
			t.Fatalf("Test 2 Failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusFound {
			t.Errorf("Test 2 Failed: Expected 303/302 redirect for unauthenticated /, got %d", resp.StatusCode)
		}
		loc := resp.Header.Get("Location")
		if loc != "/login" {
			t.Errorf("Test 2 Failed: Expected redirect to /login, got %s", loc)
		}

		// API /api/v1/status should return 401 Unauthorized
		apiResp, err := noRedirectClient.Get(baseURL + "/api/v1/status")
		if err != nil {
			t.Fatalf("Test 2 API Failed: %v", err)
		}
		defer apiResp.Body.Close()

		if apiResp.StatusCode != http.StatusUnauthorized {
			t.Errorf("Test 2 Failed: Expected 401 for unauthenticated /api/v1/status, got %d", apiResp.StatusCode)
		}
	})

	// -------------------------------------------------------------
	// TEST 3: Brute-Force Rate Limiting (Lockout after 5 failed logins)
	// -------------------------------------------------------------
	t.Run("Test03_BruteForceLockout", func(t *testing.T) {
		loginURL := baseURL + "/login"
		form := url.Values{}
		form.Set("password", "wrongpassword123")

		lockedOut := false
		for i := 0; i < 7; i++ {
			resp, err := noRedirectClient.PostForm(loginURL, form)
			if err != nil {
				t.Fatalf("Test 3 request failed on attempt %d: %v", i+1, err)
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusTooManyRequests {
				lockedOut = true
				break
			}
		}

		if !lockedOut {
			t.Errorf("Test 3 Failed: Brute force lockout (429) was not triggered after consecutive failed logins")
		}
	})

	// -------------------------------------------------------------
	// TEST 4 / S13: Rate Limit Bypass via X-Forwarded-For Spoofing
	// -------------------------------------------------------------
	t.Run("Test04_RateLimitBypassViaProxySpoofingBlocked", func(t *testing.T) {
		// Attempt login with spoofed X-Forwarded-For header
		// Since rate limiter is currently locked for this IP (127.0.0.1), spoofing another IP must NOT bypass it!
		req, err := http.NewRequest(http.MethodPost, baseURL+"/login", strings.NewReader("password=wrongpassword123"))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Forwarded-For", "203.0.113.195")
		req.Header.Set("X-Real-IP", "203.0.113.195")
		req.Header.Set("CF-Connecting-IP", "203.0.113.195")

		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatalf("Test 4 Failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusTooManyRequests {
			t.Errorf("Test 4 Failed: Spoofed X-Forwarded-For bypassed lockout! Expected 429 Too Many Requests, got %d", resp.StatusCode)
		}
	})

	// -------------------------------------------------------------
	// TEST 5: Successful Login & Cookie Verification
	// -------------------------------------------------------------
	// We reset rate limiter for subsequent authenticated tests
	srv.rateLimiter = security.NewRateLimiter()

	var sessionCookie *http.Cookie
	var csrfCookie *http.Cookie

	t.Run("Test05_SuccessfulLogin", func(t *testing.T) {
		srv.rateLimiter.ResetLoginFails("127.0.0.1")
		form := url.Values{}
		form.Set("password", "admin")

		resp, err := noRedirectClient.PostForm(baseURL+"/login", form)
		if err != nil {
			t.Fatalf("Test 5 Failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
			t.Errorf("Test 5 Failed: Expected 303 or 200 on login success, got %d", resp.StatusCode)
		}

		cookies := resp.Cookies()
		for _, c := range cookies {
			if c.Name == "wa_session" {
				sessionCookie = c
			}
			if c.Name == "wa_csrf" {
				csrfCookie = c
			}
		}

		if sessionCookie == nil || sessionCookie.Value == "" {
			t.Errorf("Test 5 Failed: Missing or empty wa_session cookie")
		}
		if csrfCookie == nil || csrfCookie.Value == "" {
			t.Errorf("Test 5 Failed: Missing or empty wa_csrf cookie")
		}
	})

	// -------------------------------------------------------------
	// TEST 6: CSRF Token Enforcement on State-Changing API
	// -------------------------------------------------------------
	t.Run("Test06_CSRFTokenEnforcement", func(t *testing.T) {
		if sessionCookie == nil {
			t.Skip("Session cookie not available")
		}

		contactPayload := `{"phone": "14155550001", "name": "CSRF Test Contact"}`
		req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/contacts", strings.NewReader(contactPayload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(sessionCookie)
		// Omit X-CSRF-Token and wa_csrf cookie

		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatalf("Test 6 Failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Test 6 Failed: Expected 403 Forbidden when CSRF token is omitted, got %d", resp.StatusCode)
		}
	})

	// -------------------------------------------------------------
	// TEST 7 / S19: Foreign Origin Header Rejected (CSRF Protection)
	// -------------------------------------------------------------
	t.Run("Test07_ForeignOriginRejected", func(t *testing.T) {
		if sessionCookie == nil || csrfCookie == nil {
			t.Skip("Session or CSRF cookie not available")
		}

		contactPayload := `{"phone": "14155550002", "name": "Foreign Origin Test"}`
		req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/contacts", strings.NewReader(contactPayload))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://malicious-website.com")
		req.Header.Set("X-CSRF-Token", csrfCookie.Value)
		req.AddCookie(sessionCookie)
		req.AddCookie(csrfCookie)

		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatalf("Test 7 Failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Test 7 Failed: Expected 403 Forbidden for foreign Origin header, got %d", resp.StatusCode)
		}
	})

	// -------------------------------------------------------------
	// TEST 8: Security Response Headers (CSP, Frame-Options, etc.)
	// -------------------------------------------------------------
	t.Run("Test08_SecurityHeaders", func(t *testing.T) {
		resp, err := noRedirectClient.Get(baseURL + "/login")
		if err != nil {
			t.Fatalf("Test 8 Failed: %v", err)
		}
		defer resp.Body.Close()

		xfo := resp.Header.Get("X-Frame-Options")
		if xfo != "DENY" {
			t.Errorf("Test 8 Failed: Expected X-Frame-Options: DENY, got %s", xfo)
		}

		xcto := resp.Header.Get("X-Content-Type-Options")
		if xcto != "nosniff" {
			t.Errorf("Test 8 Failed: Expected X-Content-Type-Options: nosniff, got %s", xcto)
		}

		csp := resp.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'self'") {
			t.Errorf("Test 8 Failed: CSP missing default-src 'self', got: %s", csp)
		}
	})

	// -------------------------------------------------------------
	// TEST 9 & 10 / S4 / D16: Contact Creation, Year-0001 Fix & DOM XSS Prevention
	// -------------------------------------------------------------
	t.Run("Test09_And_Test10_ContactCreation_And_DOM_XSS", func(t *testing.T) {
		if sessionCookie == nil || csrfCookie == nil {
			t.Skip("Session or CSRF cookie not available")
		}

		// Payload with quotes and script tag intended for DOM XSS
		xssName := `Test <script>alert('xss')</script> " ' &`
		contactMap := map[string]string{
			"phone": "14155558888",
			"name":  xssName,
			"notes": "VIP",
		}
		contactData, _ := json.Marshal(contactMap)
		req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/contacts", bytes.NewReader(contactData))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", baseURL)
		req.Header.Set("X-CSRF-Token", csrfCookie.Value)
		req.AddCookie(sessionCookie)
		req.AddCookie(csrfCookie)

		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatalf("Test 9 Failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("Test 9 Failed: Expected 201 Created, got %d. Body: %s", resp.StatusCode, string(body))
		}

		var created contacts.Contact
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			t.Fatalf("Test 9 Failed to decode response: %v", err)
		}

		// Verify D16: CreatedAt is not 0001-01-01
		if created.CreatedAt.IsZero() || created.CreatedAt.Year() <= 1 {
			t.Errorf("Test 9 / D16 Failed: Expected valid CreatedAt timestamp, got %v", created.CreatedAt)
		}

		// Verify Test 10 / S4: Fetch dashboard and check that contact rendered in UI doesn't have inline onclick with raw quotes
		dashReq, err := http.NewRequest(http.MethodGet, baseURL+"/", nil)
		if err != nil {
			t.Fatal(err)
		}
		dashReq.AddCookie(sessionCookie)
		dashReq.AddCookie(csrfCookie)

		dashResp, err := noRedirectClient.Do(dashReq)
		if err != nil {
			t.Fatalf("Failed to fetch dashboard: %v", err)
		}
		defer dashResp.Body.Close()

		dashBody, _ := io.ReadAll(dashResp.Body)
		dashHTML := string(dashBody)

		// The vulnerable pattern was: onclick="selectChat('14155558888', 'Test <script>...')"
		if strings.Contains(dashHTML, "onclick=\"selectChat('14155558888',") {
			t.Errorf("Test 10 / S4 Failed: Found vulnerable inline onclick selectChat handler in dashboard HTML")
		}
		// Confirm safe data attributes are used instead of dangerous string interpolation
		if !strings.Contains(dashHTML, "selectChat(this.getAttribute") {
			t.Errorf("Test 10 / S4 Failed: Expected safe selectChat(this.getAttribute...) pattern in dashboard HTML")
		}
	})

	// -------------------------------------------------------------
	// TEST 11 / S18: Settings Secrets Redaction ("********")
	// -------------------------------------------------------------
	t.Run("Test11_SettingsSecretsRedaction", func(t *testing.T) {
		if sessionCookie == nil || csrfCookie == nil {
			t.Skip("Session or CSRF cookie not available")
		}

		req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/settings", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(sessionCookie)
		req.AddCookie(csrfCookie)

		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatalf("Test 11 Failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("Test 11 Failed: Expected 200 OK, got %d", resp.StatusCode)
		}

		var settingsData map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&settingsData); err != nil {
			t.Fatalf("Test 11 Failed to parse json: %v", err)
		}

		serverMap := settingsData["server"].(map[string]any)
		if serverMap["admin_password"] != "********" {
			t.Errorf("Test 11 Failed: Expected admin_password to be '********', got %v", serverMap["admin_password"])
		}

		aiMap := settingsData["ai"].(map[string]any)
		if aiMap["api_key"] != "********" {
			t.Errorf("Test 11 Failed: Expected ai.api_key to be '********', got %v", aiMap["api_key"])
		}

		webhookMap := settingsData["webhook"].(map[string]any)
		if webhookMap["secret"] != "********" {
			t.Errorf("Test 11 Failed: Expected webhook.secret to be '********', got %v", webhookMap["secret"])
		}
	})

	// -------------------------------------------------------------
	// TEST 12 / C18: Rule Evaluation Compliance (STOP and START)
	// -------------------------------------------------------------
	t.Run("Test12_RuleEvaluation_Compliance", func(t *testing.T) {
		rules := []config.Rule{
			{
				Name:    "welcome_greeting",
				Type:    "exact",
				Pattern: "hi, hello, menu",
				Reply:   "Welcome!",
				Enabled: true,
			},
		}
		startTime := time.Now()

		// STOP keyword
		resStop := bot.EvaluateMessage("STOP", "Alice", "+14155550000", rules, startTime)
		if !resStop.Matched || resStop.RuleName != "system:opt_out" {
			t.Errorf("Test 12 Failed: STOP did not match system:opt_out, got %+v", resStop)
		}

		// START keyword
		resStart := bot.EvaluateMessage("START", "Alice", "+14155550000", rules, startTime)
		if !resStart.Matched || resStart.RuleName != "system:opt_in" {
			t.Errorf("Test 12 Failed: START did not match system:opt_in, got %+v", resStart)
		}
	})

	// -------------------------------------------------------------
	// TEST 13 / S16: Desktop Actions Enforce POST (Reject GET with 405)
	// -------------------------------------------------------------
	t.Run("Test13_DesktopActions_EnforcePOST", func(t *testing.T) {
		if sessionCookie == nil || csrfCookie == nil {
			t.Skip("Session or CSRF cookie not available")
		}

		// GET /api/v1/app/launch-window
		reqLaunch, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/app/launch-window", nil)
		reqLaunch.AddCookie(sessionCookie)
		reqLaunch.AddCookie(csrfCookie)

		respLaunch, err := noRedirectClient.Do(reqLaunch)
		if err != nil {
			t.Fatal(err)
		}
		respLaunch.Body.Close()
		if respLaunch.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("Test 13 Failed: Expected 405 Method Not Allowed for GET launch-window, got %d", respLaunch.StatusCode)
		}

		// GET /api/v1/app/create-shortcut
		reqShortcut, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/app/create-shortcut", nil)
		reqShortcut.AddCookie(sessionCookie)
		reqShortcut.AddCookie(csrfCookie)

		respShortcut, err := noRedirectClient.Do(reqShortcut)
		if err != nil {
			t.Fatal(err)
		}
		respShortcut.Body.Close()
		if respShortcut.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("Test 13 Failed: Expected 405 Method Not Allowed for GET create-shortcut, got %d", respShortcut.StatusCode)
		}
	})

	// -------------------------------------------------------------
	// TEST 14 / S15: Inbound Webhook Requires Authentication/Secret
	// -------------------------------------------------------------
	t.Run("Test14_InboundWebhookSecurity", func(t *testing.T) {
		webhookURL := baseURL + "/api/v1/webhook/send"
		payload := `{"to": "14155550000", "message": "Webhook Test"}`
		unauthClient := &http.Client{Transport: memTransport}

		// 1. Unauthenticated request without secret -> 401
		reqNoSecret, _ := http.NewRequest(http.MethodPost, webhookURL, strings.NewReader(payload))
		reqNoSecret.Header.Set("Content-Type", "application/json")

		respNoSecret, err := unauthClient.Do(reqNoSecret)
		if err != nil {
			t.Fatal(err)
		}
		respNoSecret.Body.Close()
		if respNoSecret.StatusCode != http.StatusUnauthorized {
			t.Errorf("Test 14 Failed: Expected 401 Unauthorized without secret, got %d", respNoSecret.StatusCode)
		}

		// 2. Unauthenticated request with wrong secret -> 401
		reqWrongSecret, _ := http.NewRequest(http.MethodPost, webhookURL, strings.NewReader(payload))
		reqWrongSecret.Header.Set("Content-Type", "application/json")
		reqWrongSecret.Header.Set("X-Webhook-Secret", "incorrect-secret")

		respWrongSecret, err := unauthClient.Do(reqWrongSecret)
		if err != nil {
			t.Fatal(err)
		}
		respWrongSecret.Body.Close()
		if respWrongSecret.StatusCode != http.StatusUnauthorized {
			t.Errorf("Test 14 Failed: Expected 401 Unauthorized with incorrect secret, got %d", respWrongSecret.StatusCode)
		}

		// 3. Request with valid secret -> Should pass auth check (may fail on WhatsApp client not linked, returning 503 or 400, but NOT 401)
		reqValidSecret, _ := http.NewRequest(http.MethodPost, webhookURL, strings.NewReader(payload))
		reqValidSecret.Header.Set("Content-Type", "application/json")
		reqValidSecret.Header.Set("X-Webhook-Secret", "WebhookSigningSecret123")

		respValidSecret, err := unauthClient.Do(reqValidSecret)
		if err != nil {
			t.Fatal(err)
		}
		respValidSecret.Body.Close()
		if respValidSecret.StatusCode == http.StatusUnauthorized {
			t.Errorf("Test 14 Failed: Expected valid secret to be authorized, but got 401")
		}
	})
}
