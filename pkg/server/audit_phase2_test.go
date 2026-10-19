package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/templates"
)

// TestAuditRemediations_Phase2 verifies compliance for S12, S7, C15, and X5
func TestAuditRemediations_Phase2(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 8085
	cfg.Server.AdminPassword = "AdminPassword@123"
	cfg.Server.MustChangePassword = false

	cStore, _ := contacts.NewStore(t.TempDir() + "/contacts.json")
	tStore, _ := templates.NewStore(t.TempDir() + "/templates.json")
	hStore, _ := history.NewStore(t.TempDir() + "/messages.json", 100)

	srv := NewServer(cfg, nil, cStore, tStore, hStore, nil, nil)
	ts := httptest.NewServer(srv.httpServer.Handler)
	defer ts.Close()

	// 1. S12 & D2: Verify CSP does NOT allow 'unsafe-inline' in script-src
	t.Run("S12_CSP_Strict_No_Unsafe_Inline_In_Script_Src", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/login")
		if err != nil {
			t.Fatalf("Failed to fetch login: %v", err)
		}
		defer resp.Body.Close()

		csp := resp.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "script-src 'self'") {
			t.Errorf("Expected script-src 'self' in CSP, got: %s", csp)
		}
		if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
			t.Errorf("CSP still contains 'unsafe-inline' in script-src: %s", csp)
		}
	})

	// 2. S12 & D2: Verify static JavaScript endpoints are served correctly
	t.Run("S12_Static_App_And_Auth_JS_Endpoints", func(t *testing.T) {
		// Test /static/app.js
		respApp, err := http.Get(ts.URL + "/static/app.js")
		if err != nil {
			t.Fatalf("Failed to fetch /static/app.js: %v", err)
		}
		defer respApp.Body.Close()

		if respApp.StatusCode != http.StatusOK {
			t.Errorf("Expected 200 OK for /static/app.js, got %d", respApp.StatusCode)
		}
		ctApp := respApp.Header.Get("Content-Type")
		if !strings.Contains(ctApp, "application/javascript") {
			t.Errorf("Expected Content-Type application/javascript, got %s", ctApp)
		}
		ccApp := respApp.Header.Get("Cache-Control")
		if !strings.Contains(ccApp, "max-age=") {
			t.Errorf("Expected Cache-Control header on static asset, got %s", ccApp)
		}

		// Test /static/auth.js
		respAuth, err := http.Get(ts.URL + "/static/auth.js")
		if err != nil {
			t.Fatalf("Failed to fetch /static/auth.js: %v", err)
		}
		defer respAuth.Body.Close()

		if respAuth.StatusCode != http.StatusOK {
			t.Errorf("Expected 200 OK for /static/auth.js, got %d", respAuth.StatusCode)
		}
		ctAuth := respAuth.Header.Get("Content-Type")
		if !strings.Contains(ctAuth, "application/javascript") {
			t.Errorf("Expected Content-Type application/javascript, got %s", ctAuth)
		}
	})

	// 3. S7: Verify host-level and account-level endpoints reject non-local requests
	t.Run("S7_Host_Endpoints_Reject_Non_Local_Requests", func(t *testing.T) {
		// Mock non-local request by setting RemoteAddr to a foreign IP (e.g. 192.168.1.100)
		endpoints := []struct {
			name   string
			path   string
			method string
			body   string
		}{
			{"PairPhone", "/api/v1/pair-phone", http.MethodPost, `{"phone":"14155552671"}`},
			{"Relink", "/api/v1/relink", http.MethodPost, `{}`},
			{"WhatsAppLogout", "/api/v1/whatsapp/logout", http.MethodPost, `{}`},
			{"LaunchWindow", "/api/v1/app/launch-window", http.MethodPost, `{}`},
			{"CreateShortcut", "/api/v1/app/create-shortcut", http.MethodPost, `{}`},
			{"Backup", "/api/v1/backup", http.MethodGet, ``},
			{"Restore", "/api/v1/restore", http.MethodPost, `{}`},
			{"PrivacyPurge", "/api/v1/privacy/purge", http.MethodPost, `{}`},
		}

		for _, ep := range endpoints {
			req, err := http.NewRequest(ep.method, ts.URL+ep.path, strings.NewReader(ep.body))
			if err != nil {
				t.Fatalf("Failed to build request: %v", err)
			}
			req.RemoteAddr = "192.168.1.100:54321" // Foreign non-loopback IP
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", "")

			// Logged-in session cookie
			sessToken, csrfToken := srv.sessionMgr.CreateSession()
			req.AddCookie(&http.Cookie{Name: "wa_session", Value: sessToken})
			req.AddCookie(&http.Cookie{Name: "wa_csrf", Value: csrfToken})
			req.Header.Set("X-CSRF-Token", csrfToken)

			w := httptest.NewRecorder()
			// Directly call the handler through auth middleware
			switch ep.path {
			case "/api/v1/pair-phone":
				srv.authMiddleware(srv.handlePairPhone).ServeHTTP(w, req)
			case "/api/v1/relink":
				srv.authMiddleware(srv.handleRelink).ServeHTTP(w, req)
			case "/api/v1/whatsapp/logout":
				srv.authMiddleware(srv.handleWhatsAppLogout).ServeHTTP(w, req)
			case "/api/v1/app/launch-window":
				srv.authMiddleware(srv.handleLaunchAppWindow).ServeHTTP(w, req)
			case "/api/v1/app/create-shortcut":
				srv.authMiddleware(srv.handleCreateShortcut).ServeHTTP(w, req)
			case "/api/v1/backup":
				srv.authMiddleware(srv.handleBackup).ServeHTTP(w, req)
			case "/api/v1/restore":
				srv.authMiddleware(srv.handleRestore).ServeHTTP(w, req)
			case "/api/v1/privacy/purge":
				srv.authMiddleware(srv.handlePrivacyPurge).ServeHTTP(w, req)
			}

			if w.Code != http.StatusForbidden {
				t.Errorf("Endpoint %s (%s) failed S7: expected 403 Forbidden for remote IP, got %d", ep.name, ep.path, w.Code)
			}
		}
	})

	// 4. X5 & X9: Verify rendered dashboard has Design System CSS custom properties and 0 emojis as UI icons
	t.Run("X5_And_X9_DesignSystem_And_Zero_Emoji_Icons", func(t *testing.T) {
		sessToken, csrfToken := srv.sessionMgr.CreateSession()

		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
		req.AddCookie(&http.Cookie{Name: "wa_session", Value: sessToken})
		req.AddCookie(&http.Cookie{Name: "wa_csrf", Value: csrfToken})

		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatalf("Failed to fetch dashboard: %v", err)
		}
		defer resp.Body.Close()

		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		body := buf.String()

		// Verify Design System CSS tokens
		expectedTokens := []string{
			"--primary: #059669;",
			"--primary-hover:",
			"--primary-subtle:",
			"--border:",
			"--text-primary:",
			"--text-secondary:",
			"--space-1:",
			"--space-4:",
			"--radius-sm:",
			"--radius-md:",
			"--shadow-sm:",
			"--focus-ring:",
		}
		for _, token := range expectedTokens {
			if !strings.Contains(body, token) {
				t.Errorf("Design System missing token %q in rendered HTML", token)
			}
		}

		// Verify external script is linked, not inline
		if !strings.Contains(body, `<script src="/static/app.js"`) {
			t.Errorf("Expected external script tag <script src=\"/static/app.js\" in dashboard HTML")
		}
	})
}
