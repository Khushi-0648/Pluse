package server

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"rsc.io/qr"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/ai"
	"whatsapp-automation/pkg/bot"
	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/client"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/desktop"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/scheduler"
	"whatsapp-automation/pkg/security"
	"whatsapp-automation/pkg/templates"
)

var (
	dashboardTmpl     *template.Template
	loginTmpl         *template.Template
	setupPasswordTmpl *template.Template
)

func init() {
	var err error
	dashboardTmpl, err = template.New("dashboard").Parse(dashboardHTML)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse dashboard HTML template: %v", err))
	}

	loginTmpl, err = template.New("login").Parse(loginHTML)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse login HTML template: %v", err))
	}

	setupPasswordTmpl, err = template.New("setupPassword").Parse(setupPasswordHTML)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse setupPassword HTML template: %v", err))
	}
}

// Server encapsulates the HTTP API, Security, and Web Dashboard
type Server struct {
	cfg           *config.Config
	client        *client.WhatsAppClient
	contactStore  *contacts.Store
	templateStore *templates.Store
	histStore     *history.Store
	scheduler     *scheduler.Scheduler
	aiEngine      *ai.Engine
	botEngine     *bot.Engine
	broadcaster   *campaign.Broadcaster
	sessionMgr    *security.SessionManager
	rateLimiter   *security.RateLimiter
	httpServer    *http.Server
	startTime     time.Time

	broadcastLock    sync.Mutex
	isBroadcasting   bool
	broadcastCurrent int
	broadcastTotal   int
	broadcastSuccess int
	broadcastFailed  int
	broadcastStatus  string
}

// SetBotEngine registers the bot engine to enable session data wiping
func (s *Server) SetBotEngine(eng *bot.Engine) {
	s.botEngine = eng
}

// NewServer initializes the HTTP REST API server with full security
func NewServer(
	cfg *config.Config,
	c *client.WhatsAppClient,
	cs *contacts.Store,
	ts *templates.Store,
	hs *history.Store,
	sch *scheduler.Scheduler,
	aiEng *ai.Engine,
) *Server {
	bc := campaign.NewBroadcaster(c)
	if hs != nil {
		bc.SetHistoryStore(hs)
	}

	s := &Server{
		cfg:           cfg,
		client:        c,
		contactStore:  cs,
		templateStore: ts,
		histStore:     hs,
		scheduler:     sch,
		aiEngine:      aiEng,
		broadcaster:   bc,
		sessionMgr:    security.NewSessionManager(7 * 24 * time.Hour),
		rateLimiter:   security.NewRateLimiter(),
		startTime:     time.Now(),
	}

	if c != nil {
		c.OnLogout(func() {
			if bc != nil {
				bc.CancelBroadcast()
			}
			if s.histStore != nil {
				_ = s.histStore.Clear()
			}
			if s.contactStore != nil {
				_ = s.contactStore.Clear()
			}
			if s.scheduler != nil {
				_ = s.scheduler.Clear(false)
			}
			if s.botEngine != nil {
				s.botEngine.Reset()
			}
			security.LogAudit("WHATSAPP_LOGOUT", "system", "whatsapp", "WhatsApp device unlinked/logged out: purged session contacts, history, and scheduled tasks", "success")
		})
	}

	mux := http.NewServeMux()

	// Authentication Routes
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/setup-password", s.handleSetupPassword)

	// Web Dashboard
	mux.HandleFunc("/", s.authMiddleware(s.handleDashboard))

	// System, Telemetry & Diagnostics
	mux.HandleFunc("/api/v1/status", s.authMiddleware(s.handleStatus))
	mux.HandleFunc("/api/v1/system-health", s.authMiddleware(s.handleSystemHealth))
	mux.HandleFunc("/api/v1/qr", s.authMiddleware(s.handleQR))
	mux.HandleFunc("/api/v1/qr/regenerate", s.authMiddleware(s.handleRelink))
	mux.HandleFunc("/api/v1/pair-phone", s.authMiddleware(s.handlePairPhone))
	mux.HandleFunc("/api/v1/relink", s.authMiddleware(s.handleRelink))
	mux.HandleFunc("/api/v1/whatsapp/logout", s.authMiddleware(s.handleWhatsAppLogout))
	mux.HandleFunc("/api/v1/session/clear", s.authMiddleware(s.handleWhatsAppLogout))

	// Messaging & Live Chat
	mux.HandleFunc("/api/v1/messages/send", s.authMiddleware(s.handleSendMessage))
	mux.HandleFunc("/api/v1/messages", s.authMiddleware(s.handleMessages))

	// Inbound Webhook for CRM Integration (Zapier / Make / Shopify / n8n)
	mux.HandleFunc("/api/v1/webhook/send", s.handleInboundWebhook)
	mux.HandleFunc("/api/v1/webhook/test-ping", s.authMiddleware(s.handleWebhookTestPing))

	// Scheduled Messages
	mux.HandleFunc("/api/v1/scheduled", s.authMiddleware(s.handleScheduled))
	mux.HandleFunc("/api/v1/scheduled/", s.authMiddleware(s.handleScheduledActions))

	// Campaigns
	mux.HandleFunc("/api/v1/campaigns/broadcast", s.authMiddleware(s.handleBroadcast))
	mux.HandleFunc("/api/v1/campaigns/cancel", s.authMiddleware(s.handleCancelBroadcast))
	mux.HandleFunc("/api/v1/campaigns/status", s.authMiddleware(s.handleCampaignStatus))

	// Rules
	mux.HandleFunc("/api/v1/rules", s.authMiddleware(s.handleRules))
	mux.HandleFunc("/api/v1/rules/", s.authMiddleware(s.handleDeleteRule))
	mux.HandleFunc("/api/v1/test-rule", s.authMiddleware(s.handleTestRule))

	// Contacts API
	mux.HandleFunc("/api/v1/contacts", s.authMiddleware(s.handleContacts))
	mux.HandleFunc("/api/v1/contacts/", s.authMiddleware(s.handleDeleteContact))
	mux.HandleFunc("/api/v1/contacts/import-csv", s.authMiddleware(s.handleImportContactsCSV))
	mux.HandleFunc("/api/v1/contacts/export-csv", s.authMiddleware(s.handleExportContactsCSV))
	mux.HandleFunc("/api/v1/contacts/toggle-dnd", s.authMiddleware(s.handleToggleDND))
	mux.HandleFunc("/api/v1/contacts/resume-bot", s.authMiddleware(s.handleResumeBot))

	// Templates API
	mux.HandleFunc("/api/v1/templates", s.authMiddleware(s.handleTemplates))
	mux.HandleFunc("/api/v1/templates/", s.authMiddleware(s.handleDeleteTemplate))

	// Backup & Restore
	mux.HandleFunc("/api/v1/backup", s.authMiddleware(s.handleBackup))
	mux.HandleFunc("/api/v1/restore", s.authMiddleware(s.handleRestore))

	// Settings & Security
	mux.HandleFunc("/api/v1/settings", s.authMiddleware(s.handleSettings))
	mux.HandleFunc("/api/v1/settings/change-password", s.authMiddleware(s.handleChangePassword))
	mux.HandleFunc("/api/v1/ai/test", s.authMiddleware(s.handleAITest))

	// Privacy & Compliance (C12)
	mux.HandleFunc("/api/v1/privacy/purge", s.authMiddleware(s.handlePrivacyPurge))
	mux.HandleFunc("/api/v1/privacy/export", s.authMiddleware(s.handlePrivacyExport))

	// Audit Trail (S11)
	mux.HandleFunc("/api/v1/audit-log", s.authMiddleware(s.handleAuditLog))

	// Analytics & Reporting (C10)
	mux.HandleFunc("/api/v1/analytics", s.authMiddleware(s.handleAnalytics))

	// Progressive Web App (PWA) & Standalone Desktop Window APIs
	mux.HandleFunc("/manifest.json", s.handleManifest)
	mux.HandleFunc("/sw.js", s.handleServiceWorker)
	mux.HandleFunc("/app-icon.svg", s.handleAppIcon)
	mux.HandleFunc("/static/app.js", s.handleStaticAppJS)
	mux.HandleFunc("/static/auth.js", s.handleStaticAuthJS)
	mux.HandleFunc("/api/v1/app/launch-window", s.authMiddleware(s.handleLaunchAppWindow))
	mux.HandleFunc("/api/v1/app/create-shortcut", s.authMiddleware(s.handleCreateShortcut))

	addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      s.securityHeadersMiddleware(mux),
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	return s
}

// Start launches the HTTP server
func (s *Server) Start() error {
	fmt.Printf("[HTTP] Secure API and Web Dashboard running on http://%s:%d\n", s.cfg.Server.Host, s.cfg.Server.Port)
	if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// Stop shuts down the HTTP server gracefully
func (s *Server) Stop(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

// securityHeadersMiddleware injects security headers, validates Host header, and rate limits API requests
func (s *Server) securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		security.ApplySecurityHeaders(w)

		// S17: DNS Rebinding Protection - reject requests with invalid or foreign Host headers
		if !security.ValidateHostHeader(r.Host, s.cfg.Server.Port) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "Invalid Host header: request rejected for DNS rebinding protection.",
			})
			return
		}

		clientIP := security.GetClientIP(r)

		// Rate limit API calls
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !s.rateLimiter.AllowAPIRequest(clientIP) {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{
					"error": "Rate limit exceeded. Please wait before retrying.",
				})
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// isAuthenticated verifies admin password session cookie or API key
func (s *Server) isAuthenticated(r *http.Request) bool {
	// 1. Check API Key header or query param
	apiKey := s.cfg.Server.APIKey
	if apiKey != "" {
		reqKey := r.Header.Get("X-API-Key")
		if reqKey == "" {
			authHeader := r.Header.Get("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				reqKey = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}
		if reqKey == "" {
			reqKey = r.URL.Query().Get("api_key")
		}
		if reqKey == apiKey {
			return true
		}
	}

	// 2. Check Session Cookie
	cookie, err := r.Cookie("wa_session")
	if err == nil && cookie != nil && s.sessionMgr.ValidateSession(cookie.Value) {
		return true
	}

	return false
}

// authMiddleware enforces authentication, Origin check, and CSRF protection for protected endpoints
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAuthenticated(r) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "Unauthorized: Authentication required",
				})
				return
			}
			// Redirect browser page requests to /login
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		// If first-run password setup is required, block dashboard & APIs until password is set
		if s.cfg.Server.MustChangePassword {
			if r.URL.Path != "/setup-password" && r.URL.Path != "/logout" {
				if strings.HasPrefix(r.URL.Path, "/api/") {
					writeJSON(w, http.StatusForbidden, map[string]string{
						"error": "Password setup required. Please configure your master password at /setup-password.",
					})
					return
				}
				http.Redirect(w, r, "/setup-password", http.StatusSeeOther)
				return
			}
		}

		// S6 & S19: CSRF & Origin protection on state-changing methods for browser sessions
		if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete || r.Method == http.MethodPatch {
			cookie, err := r.Cookie("wa_session")
			if err == nil && cookie != nil && cookie.Value != "" {
				// S19 / Test 7: Strictly reject requests originating from foreign origins
				origin := r.Header.Get("Origin")
				if origin != "" && !security.IsAllowedOrigin(origin, r.Host) {
					writeJSON(w, http.StatusForbidden, map[string]string{
						"error": "Cross-origin request blocked: Origin not allowed.",
					})
					return
				}

				referer := r.Header.Get("Referer")
				if referer != "" && !security.IsAllowedOrigin(referer, r.Host) {
					writeJSON(w, http.StatusForbidden, map[string]string{
						"error": "Cross-origin request blocked: Referer not allowed.",
					})
					return
				}

				csrfCookie, _ := r.Cookie("wa_csrf")
				csrfHeader := r.Header.Get("X-CSRF-Token")
				expectedCSRF := s.sessionMgr.GetCSRFToken(cookie.Value)

				csrfValid := false
				if csrfHeader != "" && expectedCSRF != "" && csrfHeader == expectedCSRF {
					csrfValid = true
				} else if csrfHeader != "" && csrfCookie != nil && csrfHeader == csrfCookie.Value {
					csrfValid = true
				}

				if !csrfValid {
					writeJSON(w, http.StatusForbidden, map[string]string{
						"error": "CSRF verification failed. Include valid 'X-CSRF-Token' header.",
					})
					return
				}
			}
		}

		next(w, r)
	}
}

// handleLogin manages authentication login screen and session creation
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	clientIP := security.GetClientIP(r)

	switch r.Method {
	case http.MethodGet:
		if s.isAuthenticated(r) {
			if s.cfg.Server.MustChangePassword {
				http.Redirect(w, r, "/setup-password", http.StatusSeeOther)
				return
			}
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = loginTmpl.Execute(w, map[string]any{"Error": "", "MustChange": s.cfg.Server.MustChangePassword})

	case http.MethodPost:
		if s.rateLimiter.IsLoginLocked(clientIP) {
			security.LogAudit("LOGIN_LOCKOUT", clientIP, "admin", "Rate limit lockout triggered", "failed")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = loginTmpl.Execute(w, map[string]any{
				"Error":      "Too many failed login attempts. Temporarily locked for 5 minutes.",
				"MustChange": s.cfg.Server.MustChangePassword,
			})
			return
		}

		password := r.FormValue("password")
		if !s.cfg.VerifyPassword(password) {
			s.rateLimiter.RecordFailedLogin(clientIP)
			security.LogAudit("LOGIN_FAILED", clientIP, "admin", "Incorrect password attempt", "failed")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusUnauthorized)
			_ = loginTmpl.Execute(w, map[string]any{
				"Error":      "Invalid master password. Please verify and try again.",
				"MustChange": s.cfg.Server.MustChangePassword,
			})
			return
		}

		s.rateLimiter.ResetLoginFails(clientIP)
		sessionToken, csrfToken := s.sessionMgr.CreateSession()
		security.LogAudit("LOGIN_SUCCESS", clientIP, "admin", "Admin logged in successfully", "success")

		http.SetCookie(w, &http.Cookie{
			Name:     "wa_session",
			Value:    sessionToken,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   7 * 24 * 3600,
		})

		http.SetCookie(w, &http.Cookie{
			Name:     "wa_csrf",
			Value:    csrfToken,
			Path:     "/",
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   7 * 24 * 3600,
		})

		if s.cfg.Server.MustChangePassword {
			http.Redirect(w, r, "/setup-password", http.StatusSeeOther)
			return
		}

		http.Redirect(w, r, "/", http.StatusSeeOther)

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

// handleSetupPassword forces the user to choose a permanent master password on first login
func (s *Server) handleSetupPassword(w http.ResponseWriter, r *http.Request) {
	if !s.isAuthenticated(r) {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	// If already set and not required to change, redirect to dashboard
	if !s.cfg.Server.MustChangePassword {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	switch r.Method {
	case http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = setupPasswordTmpl.Execute(w, map[string]any{"Error": ""})

	case http.MethodPost:
		newPass := strings.TrimSpace(r.FormValue("new_password"))
		confirmPass := strings.TrimSpace(r.FormValue("confirm_password"))

		if newPass != confirmPass {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = setupPasswordTmpl.Execute(w, map[string]any{
				"Error": "Passwords do not match. Please re-type carefully.",
			})
			return
		}

		if err := security.ValidatePassword(newPass); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_ = setupPasswordTmpl.Execute(w, map[string]any{
				"Error": err.Error(),
			})
			return
		}

		if err := s.cfg.SetPassword(newPass); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_ = setupPasswordTmpl.Execute(w, map[string]any{
				"Error": "Failed to save password: " + err.Error(),
			})
			return
		}

		clientIP := security.GetClientIP(r)
		security.LogAudit("PASSWORD_INITIALIZED", clientIP, "admin", "Master password configured on first run", "success")

		http.Redirect(w, r, "/", http.StatusSeeOther)

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

// isWhatsAppLoggedIn checks if a WhatsApp device is actively paired and connected
func (s *Server) isWhatsAppLoggedIn() bool {
	if s.client == nil {
		return true // Allow unit tests where client is nil to pass
	}
	return s.client.GetStatus().LoggedIn
}

// ClearUserData purges all contacts, message history, scheduled jobs, and resets WhatsApp session
func (s *Server) ClearUserData() {
	if s.histStore != nil {
		_ = s.histStore.Clear()
	}
	if s.contactStore != nil {
		_ = s.contactStore.Clear()
	}
	if s.scheduler != nil {
		_ = s.scheduler.Clear(false)
	}
	if s.broadcaster != nil {
		s.broadcaster.CancelBroadcast()
	}
	if s.botEngine != nil {
		s.botEngine.Reset()
	}
	if s.client != nil {
		_ = s.client.ResetSession(context.Background())
	}
}

// handleLogout clears user session, unlinks WhatsApp companion device, and redirects to login
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	clientIP := security.GetClientIP(r)
	cookie, err := r.Cookie("wa_session")
	if err == nil && cookie != nil {
		s.sessionMgr.DestroySession(cookie.Value)
	}

	// Purge all user data and actively unlink WhatsApp companion device from phone's Linked Devices
	s.ClearUserData()

	security.LogAudit("LOGOUT", clientIP, "admin", "Admin session ended and WhatsApp device unlinked", "success")

	http.SetCookie(w, &http.Cookie{
		Name:     "wa_session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	http.SetCookie(w, &http.Cookie{
		Name:     "wa_csrf",
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// handleStatus returns current bot and connection telemetry
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var rawStatus client.ClientStatus
	if s.client != nil {
		rawStatus = s.client.GetStatus()
	}
	statusText := "Disconnected"
	if rawStatus.Connected && rawStatus.LoggedIn {
		statusText = "Connected"
	} else if rawStatus.LatestQR != "" {
		statusText = "Waiting for QR"
	}

	contactsCount := 0
	attentionCount := 0
	if s.contactStore != nil && s.isWhatsAppLoggedIn() {
		cList := s.contactStore.List()
		contactsCount = len(cList)
		for _, c := range cList {
			if c.NeedsAttention || (c.BotPausedUntil != nil && time.Now().Before(*c.BotPausedUntil)) {
				attentionCount++
			}
		}
	}
	templatesCount := 0
	if s.templateStore != nil {
		templatesCount = len(s.templateStore.List())
	}
	scheduledCount := 0
	if s.scheduler != nil {
		for _, j := range s.scheduler.List() {
			if j.Status == scheduler.StatusPending {
				scheduledCount++
			}
		}
	}

	inCount, outCount, autoRepliesCount := 0, 0, 0
	if s.histStore != nil {
		inCount, outCount = s.histStore.TotalStats()
		for _, m := range s.histStore.ExportAll() {
			if m.Type == history.TypeAutoReply {
				autoRepliesCount++
			}
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	writeJSON(w, http.StatusOK, map[string]any{
		"whatsapp_status":     statusText,
		"whatsapp_connected":  rawStatus.Connected && rawStatus.LoggedIn,
		"whatsapp_client":     rawStatus,
		"uptime":              time.Since(s.startTime).Round(time.Second).String(),
		"contacts_count":      contactsCount,
		"attention_count":     attentionCount,
		"templates_count":     templatesCount,
		"rules_count":         len(s.cfg.GetRules()),
		"scheduled_count":     scheduledCount,
		"is_broadcasting":     s.isBroadcasting,
		"messages_in":         inCount,
		"messages_out":        outCount,
		"auto_replies":        autoRepliesCount,
		"memory_mb":           fmt.Sprintf("%.1f MB", float64(m.Alloc)/(1024*1024)),
		"goroutines":          runtime.NumGoroutine(),
		"is_default_password": s.cfg.IsDefaultPassword(),
		"version":             "1.3.0",
	})
}

// handleSystemHealth returns deep diagnostics for automated health checks
func (s *Server) handleSystemHealth(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	var waStatus any = "NOT_INITIALIZED"
	if s.client != nil {
		waStatus = s.client.GetStatus()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "UP",
		"timestamp":   time.Now().Format(time.RFC3339),
		"uptime":      time.Since(s.startTime).String(),
		"whatsapp":    waStatus,
		"alloc_bytes": m.Alloc,
		"goroutines":  runtime.NumGoroutine(),
		"version":     "1.3.0",
	})
}

// handleQR returns a clean PNG representation of the pairing QR code
func (s *Server) handleQR(w http.ResponseWriter, r *http.Request) {
	if s.client == nil {
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprintf(w, `<svg width="280" height="280" viewBox="0 0 280 280" fill="none" xmlns="http://www.w3.org/2000/svg">
<rect width="280" height="280" fill="#0b1120" rx="8"/>
<text x="140" y="140" fill="#94a3b8" font-size="13" font-family="sans-serif" text-anchor="middle">Client Offline</text>
</svg>`)
		return
	}
	status := s.client.GetStatus()
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")

	if status.LoggedIn || status.Connected {
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprintf(w, `<svg width="280" height="280" viewBox="0 0 280 280" fill="none" xmlns="http://www.w3.org/2000/svg">
<rect width="280" height="280" fill="#0b1120" rx="8"/>
<circle cx="140" cy="105" r="34" fill="rgba(16, 185, 129, 0.15)"/>
<path d="M126 105l9 9 19-19" stroke="#10b981" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"/>
<text x="140" y="165" fill="#10b981" font-size="16" font-family="-apple-system, BlinkMacSystemFont, Segoe UI, Roboto, sans-serif" font-weight="700" text-anchor="middle">WhatsApp Connected</text>
<text x="140" y="190" fill="#94a3b8" font-size="12" font-family="-apple-system, BlinkMacSystemFont, Segoe UI, Roboto, sans-serif" text-anchor="middle">Device ready for automation</text>
</svg>`)
		return
	}

	if status.LatestQR == "" {
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprintf(w, `<svg width="280" height="280" viewBox="0 0 280 280" fill="none" xmlns="http://www.w3.org/2000/svg">
<rect width="280" height="280" fill="#0b1120" rx="8"/>
<circle cx="140" cy="105" r="30" stroke="#1e293b" stroke-width="4"/>
<circle cx="140" cy="105" r="30" stroke="#10b981" stroke-width="4" stroke-dasharray="60 120" stroke-linecap="round">
  <animateTransform attributeName="transform" type="rotate" from="0 140 105" to="360 140 105" dur="1s" repeatCount="indefinite"/>
</circle>
<text x="140" y="165" fill="#f8fafc" font-size="14" font-family="-apple-system, BlinkMacSystemFont, Segoe UI, Roboto, sans-serif" font-weight="600" text-anchor="middle">Generating Fresh QR...</text>
<text x="140" y="190" fill="#94a3b8" font-size="12" font-family="-apple-system, BlinkMacSystemFont, Segoe UI, Roboto, sans-serif" text-anchor="middle">Connecting to WhatsApp servers</text>
</svg>`)
		return
	}

	code, err := qr.Encode(status.LatestQR, qr.M)
	if err != nil {
		w.Header().Set("Content-Type", "image/svg+xml")
		fmt.Fprintf(w, `<svg width="280" height="280" viewBox="0 0 280 280" xmlns="http://www.w3.org/2000/svg"><rect width="280" height="280" fill="#0f172a"/><text x="140" y="140" fill="#ef4444" font-size="13" font-family="sans-serif" text-anchor="middle">QR Encode Error</text></svg>`)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(code.PNG())
}

// handlePairPhone generates an 8-character pairing code for phone linking (S7 localhost verified)
func (s *Server) handlePairPhone(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	if !security.IsLocalRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Host action only allowed from localhost"})
		return
	}

	var req struct {
		Phone string `json:"phone"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	cleanPhone := contacts.CleanPhone(req.Phone)
	if cleanPhone == "" || len(cleanPhone) < 7 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Valid phone number with country code is required (e.g. +919876543210 or +14155552671)"})
		return
	}

	code, err := s.client.RequestPairCode(r.Context(), cleanPhone)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	security.LogAudit("PHONE_PAIRING_REQUESTED", security.GetClientIP(r), "admin", "Generated pairing code for +"+cleanPhone, "success")
	writeJSON(w, http.StatusOK, map[string]string{
		"pair_code": code,
		"phone":     cleanPhone,
		"message":   "Enter this code on your WhatsApp mobile app under Linked Devices > Link with phone number",
	})
}

// handleRelink resets the WhatsApp session and restarts QR code generation (S7 localhost verified)
func (s *Server) handleRelink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	if !security.IsLocalRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Host action only allowed from localhost"})
		return
	}

	// Cancel active broadcasts on relink / reset
	if s.broadcaster != nil {
		s.broadcaster.CancelBroadcast()
	}

	if s.client != nil {
		err := s.client.ResetSession(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	security.LogAudit("DEVICE_RELINK", security.GetClientIP(r), "admin", "Reset WhatsApp device pairing session and regenerated QR code", "success")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "Session reset requested. Refreshing QR pairing...",
	})
}

// handleWhatsAppLogout unlinks the WhatsApp companion device, terminates session from linked devices, and purges session data
func (s *Server) handleWhatsAppLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	if !security.IsLocalRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Host action only allowed from localhost"})
		return
	}

	if s.broadcaster != nil {
		s.broadcaster.CancelBroadcast()
	}

	s.ClearUserData()

	if s.client != nil {
		_ = s.client.Logout(r.Context())
		_ = s.client.ResetSession(r.Context())
	}

	security.LogAudit("WHATSAPP_LOGOUT", security.GetClientIP(r), "admin", "WhatsApp companion device unlinked and session data purged", "success")
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "WhatsApp unlinked successfully and session data purged",
	})
}

// handleSendMessage dispatches a single message with attachment and size verification (S14)
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var to, name, message string
	var fileBytes []byte
	var fileName, mimeType string

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "multipart/form-data") {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Failed to parse form: " + err.Error()})
			return
		}
		to = r.FormValue("to")
		name = r.FormValue("name")
		message = r.FormValue("message")

		file, header, err := r.FormFile("media")
		if err != nil {
			file, header, err = r.FormFile("file")
		}
		if err == nil && file != nil {
			defer file.Close()
			data, readErr := io.ReadAll(file)
			if readErr == nil && len(data) > 0 {
				fileBytes = data
				fileName = header.Filename
				mimeType = header.Header.Get("Content-Type")
			}
		}
	} else {
		var req struct {
			To        string `json:"to"`
			Name      string `json:"name"`
			Message   string `json:"message"`
			MediaData string `json:"media_data"`
			FileName  string `json:"file_name"`
			MimeType  string `json:"mime_type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
			return
		}
		to = req.To
		name = req.Name
		message = req.Message
		if req.MediaData != "" {
			cleanBase64 := req.MediaData
			if idx := strings.Index(cleanBase64, ";base64,"); idx != -1 {
				cleanBase64 = cleanBase64[idx+8:]
			}
			if data, err := base64.StdEncoding.DecodeString(cleanBase64); err == nil {
				fileBytes = data
				fileName = req.FileName
				mimeType = req.MimeType
			}
		}
	}

	if to == "" || (message == "" && len(fileBytes) == 0) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "'to' and either 'message' or an attachment are required"})
		return
	}

	// S14: Attachment Validation
	if len(fileBytes) > 0 {
		const maxAttachmentSize = 16 * 1024 * 1024 // 16 MB max
		if len(fileBytes) > maxAttachmentSize {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Attachment exceeds 16 MB maximum size limit."})
			return
		}

		ext := strings.ToLower(filepath.Ext(fileName))
		prohibitedExts := map[string]bool{
			".exe": true, ".bat": true, ".cmd": true, ".sh": true, ".ps1": true,
			".vbs": true, ".js": true, ".scr": true, ".jar": true, ".msi": true,
			".php": true, ".py": true, ".com": true, ".pif": true, ".hta": true,
		}
		if prohibitedExts[ext] {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Executable file format '%s' is prohibited.", ext)})
			return
		}

		sniffBuf := fileBytes
		if len(sniffBuf) > 512 {
			sniffBuf = sniffBuf[:512]
		}
		detectedMime := http.DetectContentType(sniffBuf)
		if strings.Contains(detectedMime, "x-dosexec") || strings.Contains(detectedMime, "x-executable") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Executable binary content detected. Upload rejected."})
			return
		}
		if mimeType == "" || mimeType == "application/octet-stream" {
			mimeType = detectedMime
		}
	}

	cleanTo := contacts.CleanPhone(to)
	if cleanTo == "" {
		cleanTo = strings.TrimSpace(to)
	}

	recipientName := strings.TrimSpace(name)
	if recipientName == "" && s.contactStore != nil {
		if c, found := s.contactStore.Get(cleanTo); found {
			recipientName = c.Name
		}
	}

	finalMsg := bot.RenderTemplate(message, recipientName, cleanTo, message)

	if s.client == nil || !s.client.IsConnected() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "WhatsApp companion device is offline or not linked.",
		})
		return
	}

	var msgID string
	var err error
	var histText string

	if len(fileBytes) > 0 {
		msgID, err = s.client.SendMediaMessage(r.Context(), cleanTo, fileBytes, fileName, mimeType, finalMsg)
		if finalMsg != "" {
			histText = fmt.Sprintf("[Attachment: %s] %s", fileName, finalMsg)
		} else {
			histText = fmt.Sprintf("[Attachment: %s]", fileName)
		}
	} else {
		msgID, err = s.client.SendTextMessage(r.Context(), cleanTo, finalMsg)
		histText = finalMsg
	}

	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if s.contactStore != nil {
		if c, found := s.contactStore.Get(cleanTo); found {
			if recipientName != "" && recipientName != "-" && (c.Name == "" || c.Name == c.Phone || c.Name == "+"+c.Phone || c.Name == "-") {
				c.Name = recipientName
				_ = s.contactStore.Save(*c)
			}
			s.contactStore.UpdateLastContacted(cleanTo)
		} else {
			nameToSave := recipientName
			if nameToSave == "" || nameToSave == cleanTo || nameToSave == "+"+cleanTo || nameToSave == "-" {
				nameToSave = "+" + cleanTo
			}
			_ = s.contactStore.Save(contacts.Contact{
				Phone: cleanTo,
				Name:  nameToSave,
				Tags:  []string{"Direct"},
				Notes: "Direct Message",
			})
		}
	}

	if s.histStore != nil {
		s.histStore.Record(history.MessageRecord{
			ID:          msgID,
			Direction:   history.DirectionOutgoing,
			Type:        history.TypeDirectSend,
			Timestamp:   time.Now(),
			SenderPhone: "Me",
			SenderName:  "Direct Dispatch",
			Recipient:   cleanTo,
			Text:        histText,
			Status:      "sent",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message_id": msgID,
		"to":         cleanTo,
		"name":       recipientName,
		"message":    finalMsg,
		"file_name":  fileName,
	})
}

// handleMessages returns recorded chat history with pagination support (D7)
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		limit := 50
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
				limit = l
			}
		}
		phone := r.URL.Query().Get("phone")
		search := r.URL.Query().Get("search")

		if s.histStore == nil {
			writeJSON(w, http.StatusOK, []history.MessageRecord{})
			return
		}

		msgs := s.histStore.List(limit, phone, search)
		for i := range msgs {
			if s.client != nil {
				msgs[i].SenderPhone = s.client.ResolveString(r.Context(), msgs[i].SenderPhone)
				msgs[i].Recipient = s.client.ResolveString(r.Context(), msgs[i].Recipient)
			}
			targetPhone := msgs[i].SenderPhone
			if msgs[i].Direction == history.DirectionOutgoing {
				targetPhone = msgs[i].Recipient
			}
			cleanP := contacts.CleanPhone(targetPhone)

			resolvedName := ""
			if s.contactStore != nil && cleanP != "" {
				if c, found := s.contactStore.Get(cleanP); found && strings.TrimSpace(c.Name) != "" && c.Name != "-" && !client.IsLID(c.Name) && c.Name != cleanP && c.Name != "+"+cleanP {
					resolvedName = c.Name
				}
			}
			if resolvedName == "" && s.client != nil && cleanP != "" {
				resolvedName = s.client.GetContactName(r.Context(), cleanP)
				if resolvedName != "" && s.contactStore != nil {
					_ = s.contactStore.Save(contacts.Contact{
						Phone:     cleanP,
						Name:      resolvedName,
						CreatedAt: time.Now(),
						Notes:     "WhatsApp Contact",
					})
				}
			}

			if msgs[i].Direction == history.DirectionIncoming {
				if resolvedName != "" {
					msgs[i].SenderName = resolvedName
				} else if client.IsLID(msgs[i].SenderName) || msgs[i].SenderName == "" || msgs[i].SenderName == "-" {
					msgs[i].SenderName = "+" + cleanP
				}
			} else {
				if client.IsLID(msgs[i].SenderName) || msgs[i].SenderName == "" || msgs[i].SenderName == "-" {
					if resolvedName != "" {
						msgs[i].SenderName = resolvedName
					} else if cleanP != "" {
						msgs[i].SenderName = "+" + cleanP
					}
				}
			}
		}
		writeJSON(w, http.StatusOK, msgs)

	case http.MethodDelete:
		phone := strings.TrimSpace(r.URL.Query().Get("phone"))
		if phone != "" {
			count := 0
			if s.histStore != nil {
				count = s.histStore.ClearPhone(phone)
			}
			security.LogAudit("CLEAR_CHAT", security.GetClientIP(r), "admin", fmt.Sprintf("Cleared %d messages for %s", count, phone), "success")
			writeJSON(w, http.StatusOK, map[string]any{
				"success": true,
				"deleted": count,
				"message": fmt.Sprintf("Cleared %d messages for %s", count, phone),
			})
			return
		}

		if s.histStore != nil {
			_ = s.histStore.Clear()
		}
		security.LogAudit("CLEAR_MESSAGES", security.GetClientIP(r), "admin", "Cleared all chat history and inbox", "success")
		writeJSON(w, http.StatusOK, map[string]any{
			"success": true,
			"message": "All chat history and inbox cleared",
		})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

// handleInboundWebhook allows external services (Shopify, CRM, Zapier, n8n) to trigger WhatsApp messages
func (s *Server) handleInboundWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed. Use POST."})
		return
	}

	secret := strings.TrimSpace(s.cfg.Webhook.Secret)
	isAuthorized := false

	reqSecret := r.Header.Get("X-Webhook-Secret")
	if reqSecret == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
			reqSecret = strings.TrimSpace(authHeader[7:])
		}
	}
	if reqSecret == "" {
		reqSecret = r.URL.Query().Get("secret")
	}
	if reqSecret == "" {
		reqSecret = r.URL.Query().Get("api_key")
	}

	if s.isAuthenticated(r) {
		isAuthorized = true
	} else if secret != "" && reqSecret != "" && subtle.ConstantTimeCompare([]byte(reqSecret), []byte(secret)) == 1 {
		isAuthorized = true
	}

	if !isAuthorized {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Unauthorized: Invalid or missing webhook secret key.",
		})
		return
	}

	var recipient, messageText, customerName, email string
	var tags []string

	ct := r.Header.Get("Content-Type")
	if strings.Contains(ct, "application/json") {
		var payload struct {
			To       string   `json:"to"`
			Phone    string   `json:"phone"`
			Number   string   `json:"number"`
			Message  string   `json:"message"`
			Text     string   `json:"text"`
			Name     string   `json:"name"`
			Customer string   `json:"customer_name"`
			Email    string   `json:"email"`
			Tags     []string `json:"tags"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Malformed JSON: " + err.Error()})
			return
		}
		recipient = payload.To
		if recipient == "" {
			recipient = payload.Phone
		}
		if recipient == "" {
			recipient = payload.Number
		}

		messageText = payload.Message
		if messageText == "" {
			messageText = payload.Text
		}

		customerName = payload.Name
		if customerName == "" {
			customerName = payload.Customer
		}
		email = payload.Email
		tags = payload.Tags
	} else {
		_ = r.ParseForm()
		recipient = r.FormValue("to")
		if recipient == "" {
			recipient = r.FormValue("phone")
		}
		messageText = r.FormValue("message")
		if messageText == "" {
			messageText = r.FormValue("text")
		}
		customerName = r.FormValue("name")
		email = r.FormValue("email")
		if tagStr := r.FormValue("tags"); tagStr != "" {
			tags = strings.Split(tagStr, ",")
		}
	}

	if recipient == "" || messageText == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "Missing required fields: 'to' (or 'phone') and 'message' (or 'text')",
		})
		return
	}

	cleanTo := contacts.CleanPhone(recipient)
	if cleanTo == "" {
		cleanTo = strings.TrimSpace(recipient)
	}

	finalMessage := bot.RenderTemplate(messageText, customerName, cleanTo, messageText)

	if s.client == nil || !s.client.IsConnected() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "WhatsApp companion device is offline or not linked.",
		})
		return
	}

	msgID, err := s.client.SendTextMessage(r.Context(), cleanTo, finalMessage)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": fmt.Sprintf("Failed to send WhatsApp message: %v", err),
		})
		return
	}

	if s.contactStore != nil && cleanTo != "" {
		if _, found := s.contactStore.Get(cleanTo); found {
			s.contactStore.UpdateLastContacted(cleanTo)
		} else {
			_ = s.contactStore.Save(contacts.Contact{
				Phone: cleanTo,
				Name:  customerName,
				Email: email,
				Tags:  tags,
				Notes: "Registered via Inbound CRM Webhook",
			})
		}
	}

	if s.histStore != nil {
		s.histStore.Record(history.MessageRecord{
			ID:          msgID,
			Direction:   history.DirectionOutgoing,
			Type:        history.TypeDirectSend,
			Timestamp:   time.Now(),
			SenderPhone: "CRM/Webhook",
			SenderName:  "CRM Inbound API",
			Recipient:   cleanTo,
			Text:        finalMessage,
			Status:      "sent",
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message_id": msgID,
		"recipient":  cleanTo,
		"timestamp":  time.Now().Format(time.RFC3339),
	})
}

// handleWebhookTestPing tests connectivity to the configured outbound webhook destination URL (SSRF protected)
func (s *Server) handleWebhookTestPing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var req struct {
		URL    string `json:"url"`
		Secret string `json:"secret"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	destURL := strings.TrimSpace(req.URL)
	if destURL == "" {
		destURL = strings.TrimSpace(s.cfg.Webhook.URL)
	}
	if destURL == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Destination Webhook URL is empty."})
		return
	}

	// S8: SSRF validation
	if err := security.ValidateWebhookURL(destURL); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("Security rejection (SSRF protection): %v", err),
		})
		return
	}

	secret := req.Secret
	if secret == "" {
		secret = s.cfg.Webhook.Secret
	}

	testPayload := map[string]any{
		"event":      "ping",
		"message":    "Test ping from Pulse WhatsApp Automation Platform",
		"timestamp":  time.Now().Format(time.RFC3339),
		"bot_status": "online",
	}
	payloadBytes, _ := json.Marshal(testPayload)

	client := &http.Client{Timeout: 8 * time.Second}
	start := time.Now()
	httpReq, err := http.NewRequestWithContext(r.Context(), "POST", destURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("Invalid destination URL: %v", err)})
		return
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if secret != "" {
		httpReq.Header.Set("X-Webhook-Secret", secret)
		sig := security.SignWebhookHMAC(secret, payloadBytes)
		httpReq.Header.Set("X-Signature-SHA256", "sha256="+sig)
	}

	resp, err := client.Do(httpReq)
	duration := time.Since(start).Round(time.Millisecond).String()
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success":  false,
			"error":    fmt.Sprintf("Failed to reach webhook: %v", err),
			"duration": duration,
		})
		return
	}
	defer resp.Body.Close()

	security.LogAudit("WEBHOOK_TEST_PING", security.GetClientIP(r), "admin", "Pinging "+destURL, "success")
	writeJSON(w, http.StatusOK, map[string]any{
		"success":     resp.StatusCode >= 200 && resp.StatusCode < 300,
		"status_code": resp.StatusCode,
		"duration":    duration,
		"url":         destURL,
	})
}

func parseScheduledTime(val string) (time.Time, error) {
	val = strings.TrimSpace(val)
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"02/01/2006 15:04",
		"2006/01/02 15:04",
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, val, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("invalid date/time format. Please choose a valid future date and time")
}

// handleScheduled lists or schedules new messages
func (s *Server) handleScheduled(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if s.scheduler == nil {
			writeJSON(w, http.StatusOK, []scheduler.ScheduledJob{})
			return
		}
		writeJSON(w, http.StatusOK, s.scheduler.List())

	case http.MethodPost:
		if s.scheduler == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Scheduler service unavailable"})
			return
		}

		var req struct {
			Recipient   string `json:"recipient"`
			Name        string `json:"name"`
			Message     string `json:"message"`
			ScheduledAt string `json:"scheduled_at"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
			return
		}

		t, err := parseScheduledTime(req.ScheduledAt)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		cleanRecipient := contacts.CleanPhone(req.Recipient)
		if cleanRecipient == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Valid recipient phone number is required"})
			return
		}

		job, err := s.scheduler.Add(cleanRecipient, req.Message, t, req.Name)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		security.LogAudit("SCHEDULE_MESSAGE", security.GetClientIP(r), "admin", "Scheduled message for "+cleanRecipient, "success")
		writeJSON(w, http.StatusCreated, job)

	case http.MethodDelete:
		if s.scheduler == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Scheduler unavailable"})
			return
		}
		_ = s.scheduler.Clear(false)
		writeJSON(w, http.StatusOK, map[string]string{"message": "Scheduled tasks cleared"})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

// handleScheduledActions cancels or deletes a scheduled message
func (s *Server) handleScheduledActions(w http.ResponseWriter, r *http.Request) {
	subPath := strings.TrimPrefix(r.URL.Path, "/api/v1/scheduled/")
	parts := strings.Split(strings.Trim(subPath, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Job ID required"})
		return
	}
	id := parts[0]

	if s.scheduler == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Scheduler unavailable"})
		return
	}

	if r.Method == http.MethodDelete {
		if err := s.scheduler.Delete(id); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "Scheduled task cancelled"})
		return
	}

	if r.Method == http.MethodPost {
		job, err := s.scheduler.Retry(id)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		security.LogAudit("SCHEDULE_RETRY", security.GetClientIP(r), "admin", "Retried scheduled job "+id, "success")
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Scheduled task reset to pending for immediate dispatch", "job": job})
		return
	}

	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
}

// handleBroadcast dispatches a bulk messaging campaign with safeguards (C11)
func (s *Server) handleBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var req struct {
		Contacts       []campaign.Contact `json:"contacts"`
		UseAllContacts bool               `json:"use_all_contacts"`
		Tag            string             `json:"tag"`
		Message        string             `json:"message"`
		TemplateID     string             `json:"template_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid JSON body"})
		return
	}

	messageText := req.Message
	if messageText == "" && req.TemplateID != "" && s.templateStore != nil {
		if tmpl, ok := s.templateStore.Get(req.TemplateID); ok {
			messageText = tmpl.Content
		}
	}

	if messageText == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Message or template_id is required"})
		return
	}

	var targetContacts []campaign.Contact

	if s.contactStore != nil && (req.UseAllContacts || req.Tag != "") {
		var stored []contacts.Contact
		if req.Tag != "" {
			stored = s.contactStore.GetByTag(req.Tag)
		} else {
			stored = s.contactStore.List()
		}

		for _, sc := range stored {
			if sc.IsDND {
				continue
			}
			targetContacts = append(targetContacts, campaign.Contact{
				Phone: sc.Phone,
				Name:  sc.Name,
				Variables: map[string]string{
					"name":  sc.Name,
					"email": sc.Email,
					"notes": sc.Notes,
				},
			})
		}
	} else {
		for _, rc := range req.Contacts {
			cleanP := contacts.CleanPhone(rc.Phone)
			if s.contactStore != nil {
				if sc, found := s.contactStore.Get(cleanP); found && sc.IsDND {
					continue
				}
			}
			targetContacts = append(targetContacts, rc)
		}
	}

	if len(targetContacts) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "No eligible recipient contacts found (all contacts may be opted-out/DND)"})
		return
	}

	// C11: Safeguards & Warmup Mode check
	maxRecipients := 1000
	if s.cfg.Automation.DailyLimit > 0 {
		maxRecipients = s.cfg.Automation.DailyLimit
	}
	if s.cfg.Automation.WarmupMode && maxRecipients > 50 {
		maxRecipients = 50
	}
	if len(targetContacts) > maxRecipients {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("Safety threshold exceeded: Campaign contains %d recipients, but daily limit is %d (warmup mode: %v). Please reduce list size to protect against account bans.", len(targetContacts), maxRecipients, s.cfg.Automation.WarmupMode),
		})
		return
	}

	s.broadcastLock.Lock()
	if s.isBroadcasting {
		s.broadcastLock.Unlock()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Another broadcast campaign is currently in progress"})
		return
	}
	s.isBroadcasting = true
	s.broadcastCurrent = 0
	s.broadcastTotal = len(targetContacts)
	s.broadcastSuccess = 0
	s.broadcastFailed = 0
	s.broadcastStatus = "running"
	s.broadcastLock.Unlock()

	clientIP := security.GetClientIP(r)
	security.LogAudit("BROADCAST_START", clientIP, "admin", fmt.Sprintf("Initiated broadcast to %d contacts", len(targetContacts)), "success")

	go func() {
		defer func() {
			s.broadcastLock.Lock()
			s.isBroadcasting = false
			if s.broadcastStatus == "running" {
				s.broadcastStatus = "completed"
			}
			s.broadcastLock.Unlock()
		}()

		bCfg := campaign.DefaultBroadcastConfig()
		res := s.broadcaster.RunBroadcast(context.Background(), targetContacts, messageText, bCfg, func(curr, total, succ, fail int, phone string, err error) {
			s.broadcastLock.Lock()
			s.broadcastCurrent = curr
			s.broadcastTotal = total
			s.broadcastSuccess = succ
			s.broadcastFailed = fail
			s.broadcastLock.Unlock()
		})
		security.LogAudit("BROADCAST_COMPLETE", clientIP, "admin", fmt.Sprintf("Finished broadcast: %d sent, %d failed", res.SuccessCount, res.FailureCount), "success")
	}()

	writeJSON(w, http.StatusOK, map[string]any{
		"success":          true,
		"message":          "Broadcast campaign started in background with anti-ban pacing",
		"total_recipients": len(targetContacts),
	})
}

// handleCancelBroadcast cancels an in-flight broadcast campaign
func (s *Server) handleCancelBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	s.broadcastLock.Lock()
	if s.broadcaster != nil {
		s.broadcaster.CancelBroadcast()
	}
	s.isBroadcasting = false
	s.broadcastStatus = "cancelled"
	s.broadcastLock.Unlock()

	security.LogAudit("BROADCAST_CANCEL", security.GetClientIP(r), "admin", "User cancelled active broadcast campaign", "success")
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Broadcast campaign cancelled"})
}

// handleCampaignStatus returns live broadcast progress
func (s *Server) handleCampaignStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	s.broadcastLock.Lock()
	defer s.broadcastLock.Unlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"is_broadcasting": s.isBroadcasting,
		"current":         s.broadcastCurrent,
		"total":           s.broadcastTotal,
		"success":         s.broadcastSuccess,
		"failed":          s.broadcastFailed,
		"status":          s.broadcastStatus,
	})
}

// handleRules provides CRUD for automation rules
func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.cfg.GetRules())

	case http.MethodPost:
		var rule config.Rule
		if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid rule JSON"})
			return
		}

		if rule.Name == "" || rule.Pattern == "" || rule.Reply == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Name, Pattern, and Reply are required"})
			return
		}

		if rule.Type == "" {
			rule.Type = "exact"
		}
		rule.Enabled = true

		if err := s.cfg.AddOrUpdateRule(rule); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		_ = s.cfg.Save()

		security.LogAudit("RULE_SAVED", security.GetClientIP(r), "admin", "Saved rule: "+rule.Name, "success")
		writeJSON(w, http.StatusOK, rule)

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

// handleDeleteRule deletes a rule by name with URL unescaping (D3)
func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	ruleName := strings.TrimPrefix(r.URL.Path, "/api/v1/rules/")
	if ruleName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Rule name required"})
		return
	}
	if unescaped, err := url.PathUnescape(ruleName); err == nil {
		ruleName = unescaped
	}

	if !s.cfg.DeleteRule(ruleName) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Rule not found"})
		return
	}
	_ = s.cfg.Save()

	security.LogAudit("RULE_DELETED", security.GetClientIP(r), "admin", "Deleted rule: "+ruleName, "success")
	writeJSON(w, http.StatusOK, map[string]string{"message": "Rule deleted"})
}

// handleTestRule provides interactive sandbox rule testing
func (s *Server) handleTestRule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var req struct {
		Message string `json:"message"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	rules := s.cfg.GetRules()
	res := bot.EvaluateMessage(req.Message, "Test User", "+14155552671", rules, s.startTime)

	writeJSON(w, http.StatusOK, map[string]any{
		"matched":   res.Matched,
		"rule_name": res.RuleName,
		"reply":     res.ReplyText,
	})
}

// handleContacts provides CRUD for CRM contacts (D7)
func (s *Server) handleContacts(w http.ResponseWriter, r *http.Request) {
	if s.contactStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Contacts store not configured"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		tag := r.URL.Query().Get("tag")
		search := r.URL.Query().Get("search")

		if tag != "" {
			writeJSON(w, http.StatusOK, s.contactStore.GetByTag(tag))
		} else if search != "" {
			writeJSON(w, http.StatusOK, s.contactStore.Search(search))
		} else {
			writeJSON(w, http.StatusOK, s.contactStore.List())
		}

	case http.MethodPost:
		var c contacts.Contact
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid contact JSON"})
			return
		}

		c.Phone = contacts.CleanPhone(c.Phone)
		if c.Phone == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Valid phone number is required"})
			return
		}

		if c.CreatedAt.IsZero() {
			c.CreatedAt = time.Now()
		}

		if err := s.contactStore.Save(c); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		saved, found := s.contactStore.Get(c.Phone)
		if !found {
			saved = &c
		}
		security.LogAudit("CONTACT_SAVED", security.GetClientIP(r), "admin", fmt.Sprintf("Saved contact: +%s (%s)", saved.Phone, saved.Name), "success")
		writeJSON(w, http.StatusCreated, saved)

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

// handleDeleteContact removes a contact with URL unescaping (D3)
func (s *Server) handleDeleteContact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	phone := strings.TrimPrefix(r.URL.Path, "/api/v1/contacts/")
	if phone == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Phone required"})
		return
	}
	if unescaped, err := url.PathUnescape(phone); err == nil {
		phone = unescaped
	}

	if s.contactStore != nil && s.contactStore.Delete(phone) {
		security.LogAudit("CONTACT_DELETED", security.GetClientIP(r), "admin", "Deleted contact: +"+phone, "success")
		writeJSON(w, http.StatusOK, map[string]string{"message": "Contact deleted"})
	} else {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Contact not found"})
	}
}

// handleResumeBot re-enables automated replies for a contact (C4)
func (s *Server) handleResumeBot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var req struct {
		Phone string `json:"phone"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	cleanPhone := contacts.CleanPhone(req.Phone)
	if cleanPhone == "" {
		cleanPhone = strings.TrimSpace(req.Phone)
	}

	if s.contactStore != nil {
		s.contactStore.ResumeBot(cleanPhone)
		security.LogAudit("BOT_RESUMED", security.GetClientIP(r), "admin", "Auto-replies re-enabled for +"+cleanPhone, "success")
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Bot auto-replies resumed for " + cleanPhone,
	})
}

// handleToggleDND flips Do Not Disturb status for a contact
func (s *Server) handleToggleDND(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var req struct {
		Phone string `json:"phone"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if s.contactStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Contacts store unavailable"})
		return
	}

	isDND, err := s.contactStore.ToggleDND(req.Phone)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	security.LogAudit("TOGGLE_DND", security.GetClientIP(r), "admin", fmt.Sprintf("Toggled DND for %s (now: %v)", req.Phone, isDND), "success")
	writeJSON(w, http.StatusOK, map[string]any{"phone": req.Phone, "is_dnd": isDND})
}

// handleImportContactsCSV imports contacts matching schema phone,name,email,tags,notes (C13)
func (s *Server) handleImportContactsCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Empty CSV body"})
		return
	}

	res, err := s.contactStore.ImportCSV(strings.NewReader(string(data)))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	auditMsg := fmt.Sprintf("Imported %d contacts (%d added, %d updated, %d skipped)", res.Total, res.Added, res.Updated, res.Skipped)
	security.LogAudit("IMPORT_CONTACTS_CSV", security.GetClientIP(r), "admin", auditMsg, "success")
	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"message":    fmt.Sprintf("CSV imported: %d added, %d updated, %d skipped", res.Added, res.Updated, res.Skipped),
		"count":      res.Added + res.Updated,
		"total":      res.Total,
		"added":      res.Added,
		"updated":    res.Updated,
		"skipped":    res.Skipped,
		"warnings":   res.Warnings,
		"overwrites": res.Overwrites,
	})

}

// handleExportContactsCSV exports contacts as CSV
func (s *Server) handleExportContactsCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=\"contacts.csv\"")

	if s.contactStore == nil {
		_, _ = w.Write([]byte("phone,name,email,tags,notes\n"))
		return
	}

	var buf bytes.Buffer
	if err := s.contactStore.ExportCSV(&buf); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_, _ = w.Write(buf.Bytes())
}

// handleTemplates provides CRUD for templates
func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	if s.templateStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Template store unavailable"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.templateStore.List())

	case http.MethodPost:
		var t templates.Template
		if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid template JSON"})
			return
		}

		if t.ID == "" || t.Content == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ID and Content are required"})
			return
		}

		if err := s.templateStore.Save(t); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		security.LogAudit("TEMPLATE_SAVED", security.GetClientIP(r), "admin", "Saved template: "+t.ID, "success")
		writeJSON(w, http.StatusOK, t)

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

// handleDeleteTemplate deletes a template with URL unescaping (D3)
func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	tmplID := strings.TrimPrefix(r.URL.Path, "/api/v1/templates/")
	if tmplID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Template ID required"})
		return
	}
	if unescaped, err := url.PathUnescape(tmplID); err == nil {
		tmplID = unescaped
	}

	if s.templateStore.Delete(tmplID) {
		security.LogAudit("TEMPLATE_DELETED", security.GetClientIP(r), "admin", "Deleted template: "+tmplID, "success")
		writeJSON(w, http.StatusOK, map[string]string{"message": "Template deleted"})
	} else {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Template not found"})
	}
}

// handleBackup exports system configuration and state as JSON (S7 localhost verified)
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if !security.IsLocalRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Host backup only allowed from localhost"})
		return
	}

	backup := map[string]any{
		"config":     s.cfg,
		"contacts":   s.contactStore.List(),
		"templates":  s.templateStore.List(),
		"scheduled":  s.scheduler.List(),
		"timestamp":  time.Now().Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", "attachment; filename=\"whatsapp-backup.json\"")
	_ = json.NewEncoder(w).Encode(backup)
}

// handleRestore restores database state from JSON (localhost only)
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	if !security.IsLocalRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Host restore only allowed from localhost"})
		return
	}

	var bundle struct {
		Contacts  []contacts.Contact   `json:"contacts"`
		Templates []templates.Template `json:"templates"`
	}

	if err := json.NewDecoder(r.Body).Decode(&bundle); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid backup payload"})
		return
	}

	for _, c := range bundle.Contacts {
		_ = s.contactStore.Save(c)
	}
	for _, t := range bundle.Templates {
		_ = s.templateStore.Save(t)
	}

	security.LogAudit("RESTORE_BACKUP", security.GetClientIP(r), "admin", "Restored system backup", "success")
	writeJSON(w, http.StatusOK, map[string]string{"message": "Backup restored successfully"})
}

// handleSettings updates server, AI, and automation configurations
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		safeCfg := s.cfg.SafeClone()
		writeJSON(w, http.StatusOK, safeCfg)

	case http.MethodPost:
		var incoming struct {
			Automation    *config.AutomationConfig    `json:"automation"`
			BusinessHours *config.BusinessHoursConfig `json:"business_hours"`
			AI            *config.AIConfig            `json:"ai"`
			Webhook       *config.WebhookConfig       `json:"webhook"`
		}

		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Failed to read request body"})
			return
		}
		bodyBytes = bytes.TrimPrefix(bodyBytes, []byte("\xef\xbb\xbf"))

		if err := json.Unmarshal(bodyBytes, &incoming); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid settings payload"})
			return
		}

		if incoming.Automation != nil {
			s.cfg.Automation = *incoming.Automation
		}
		if incoming.BusinessHours != nil {
			s.cfg.BusinessHours = *incoming.BusinessHours
		}
		if incoming.AI != nil {
			if incoming.AI.APIKey == "********" {
				incoming.AI.APIKey = s.cfg.AI.APIKey
			}
			s.cfg.AI = *incoming.AI
			if s.aiEngine != nil {
				s.aiEngine.UpdateConfig(ai.Config{
					Enabled:      incoming.AI.Enabled,
					APIKey:       incoming.AI.APIKey,
					Model:        incoming.AI.Model,
					SystemPrompt: incoming.AI.SystemPrompt,
				})
			}
		}
		if incoming.Webhook != nil {
			if incoming.Webhook.Secret == "********" {
				incoming.Webhook.Secret = s.cfg.Webhook.Secret
			}
			s.cfg.Webhook = *incoming.Webhook
		}

		_ = s.cfg.Save()
		security.LogAudit("SETTINGS_SAVED", security.GetClientIP(r), "admin", "Updated system settings", "success")
		writeJSON(w, http.StatusOK, map[string]string{"message": "Settings updated successfully"})

	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
	}
}

// handleAITest tests a Gemini API key with a test prompt
func (s *Server) handleAITest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Failed to read request body"})
		return
	}
	bodyBytes = bytes.TrimPrefix(bodyBytes, []byte("\xef\xbb\xbf"))

	var req struct {
		APIKey string `json:"api_key"`
		Model  string `json:"model"`
	}
	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	apiKey := strings.TrimSpace(req.APIKey)
	if apiKey == "" {
		apiKey = s.cfg.AI.APIKey
	}
	if apiKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Please enter a Gemini API Key to test"})
		return
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = s.cfg.AI.Model
	}
	if model == "" {
		model = ai.DefaultModel
	}

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()

	engine := s.aiEngine
	if engine == nil {
		engine = ai.NewEngine(ai.Config{Model: model, APIKey: apiKey, Enabled: true})
	}

	reply, err := engine.TestConnection(ctx, apiKey, model)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"message": "Gemini API connected successfully!",
		"reply":   reply,
		"model":   model,
	})
}

// handleChangePassword updates the master password with bcrypt hashing (S2)
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid request body"})
		return
	}

	if !s.cfg.VerifyPassword(req.CurrentPassword) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "Current master password is incorrect"})
		return
	}

	if err := security.ValidatePassword(req.NewPassword); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}


	if err := s.cfg.SetPassword(req.NewPassword); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Failed to update password: " + err.Error()})
		return
	}

	security.LogAudit("PASSWORD_CHANGED", security.GetClientIP(r), "admin", "Master admin password updated", "success")
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": "Admin password updated successfully"})
}

// handlePrivacyPurge removes message history older than N days (C12, S7 localhost verified)
func (s *Server) handlePrivacyPurge(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	if !security.IsLocalRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Host purge only allowed from localhost"})
		return
	}

	days := 30
	if dStr := r.URL.Query().Get("days"); dStr != "" {
		if d, err := strconv.Atoi(dStr); err == nil {
			days = d
		}
	}

	purged := 0
	if s.histStore != nil {
		purged = s.histStore.PurgeOlderThan(days)
	}

	security.LogAudit("DATA_PURGE", security.GetClientIP(r), "admin", fmt.Sprintf("Purged %d messages older than %d days", purged, days), "success")
	writeJSON(w, http.StatusOK, map[string]any{
		"success":      true,
		"purged_count": purged,
		"days":         days,
		"message":      fmt.Sprintf("Successfully purged %d message records older than %d days.", purged, days),
	})
}

// handlePrivacyExport exports customer bundle (contacts, messages, audit) (C12)
func (s *Server) handlePrivacyExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var contactsList []contacts.Contact
	if s.contactStore != nil {
		contactsList = s.contactStore.List()
	}

	var historyList []history.MessageRecord
	if s.histStore != nil {
		historyList = s.histStore.ExportAll()
	}

	auditList := security.GetAuditLog(500)

	exportBundle := map[string]any{
		"app":         "Pulse WhatsApp Automation Platform",
		"version":     "1.3.0",
		"exported_at": time.Now().Format(time.RFC3339),
		"contacts":    contactsList,
		"messages":    historyList,
		"audit_log":   auditList,
	}

	security.LogAudit("DATA_EXPORT", security.GetClientIP(r), "admin", "Exported customer data bundle", "success")

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"whatsapp-data-export-%s.json\"", time.Now().Format("20060102-150405")))
	_ = json.NewEncoder(w).Encode(exportBundle)
}

// handleAuditLog returns recent security and administrative events (S11)
func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	limit := 50
	if lStr := r.URL.Query().Get("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}

	events := security.GetAuditLog(limit)
	writeJSON(w, http.StatusOK, events)
}

// handleAnalytics aggregates delivery and performance statistics (C10)
func (s *Server) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}

	var totalIn, totalOut, autoReplies, broadcasts, directSends int
	var sentTotal, sentSuccess int
	ruleCounts := make(map[string]int)

	if s.histStore != nil {
		all := s.histStore.ExportAll()
		for _, m := range all {
			if m.Direction == history.DirectionIncoming {
				totalIn++
			} else {
				totalOut++
				sentTotal++
				if m.Status == "sent" || m.Status == "delivered" {
					sentSuccess++
				}
				switch m.Type {
				case history.TypeAutoReply:
					autoReplies++
				case history.TypeBroadcast:
					broadcasts++
				case history.TypeDirectSend:
					directSends++
				}
			}
			if m.RuleTriggered != "" {
				ruleCounts[m.RuleTriggered]++
			}
		}
	}

	type RuleStat struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	var topRules []RuleStat
	for k, v := range ruleCounts {
		topRules = append(topRules, RuleStat{Name: k, Count: v})
	}
	for i := 0; i < len(topRules); i++ {
		for j := i + 1; j < len(topRules); j++ {
			if topRules[j].Count > topRules[i].Count {
				topRules[i], topRules[j] = topRules[j], topRules[i]
			}
		}
	}
	if len(topRules) > 5 {
		topRules = topRules[:5]
	}

	contactsCount := 0
	dndCount := 0
	attentionCount := 0
	if s.contactStore != nil && s.isWhatsAppLoggedIn() {
		list := s.contactStore.List()
		contactsCount = len(list)
		for _, c := range list {
			if c.IsDND {
				dndCount++
			}
			if c.NeedsAttention || (c.BotPausedUntil != nil && time.Now().Before(*c.BotPausedUntil)) {
				attentionCount++
			}
		}
	}

	deliveryRate := "N/A"
	if sentTotal > 0 {
		deliveryRate = fmt.Sprintf("%.1f%%", float64(sentSuccess)*100.0/float64(sentTotal))
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_incoming":   totalIn,
		"total_outgoing":   totalOut,
		"total_messages":   totalIn + totalOut,
		"auto_replies":     autoReplies,
		"broadcasts":       broadcasts,
		"direct_sends":     directSends,
		"contacts_count":   contactsCount,
		"dnd_count":        dndCount,
		"needs_attention":  attentionCount,
		"top_rules":        topRules,
		"uptime":           time.Since(s.startTime).Round(time.Second).String(),
		"delivery_rate":    deliveryRate,
		"delivered_count":  sentSuccess,
	})
}

// Progressive Web App manifest
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	manifest := map[string]any{
		"name":             "Pulse WhatsApp Automation Platform",
		"short_name":       "Pulse Auto",
		"start_url":        "/",
		"display":          "standalone",
		"background_color": "#0b1120",
		"theme_color":      "#10b981",
		"icons": []map[string]any{
			{"src": "/app-icon.svg", "sizes": "any", "type": "image/svg+xml", "purpose": "any maskable"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	_ = json.NewEncoder(w).Encode(manifest)
}

// Service Worker
func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	fmt.Fprint(w, `self.addEventListener('install', () => self.skipWaiting());
self.addEventListener('activate', (e) => e.waitUntil(self.clients.claim()));
self.addEventListener('fetch', (e) => e.respondWith(fetch(e.request).catch(() => caches.match(e.request))));`)
}

// App Icon SVG
func (s *Server) handleAppIcon(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	fmt.Fprint(w, `<svg width="512" height="512" viewBox="0 0 512 512" fill="none" xmlns="http://www.w3.org/2000/svg">
  <rect width="512" height="512" rx="120" fill="url(#iconBg)"/>
  <defs>
    <linearGradient id="iconBg" x1="0" y1="0" x2="512" y2="512" gradientUnits="userSpaceOnUse">
      <stop stop-color="#10B981"/>
      <stop offset="0.6" stop-color="#059669"/>
      <stop offset="1" stop-color="#064E3B"/>
    </linearGradient>
    <linearGradient id="iconBolt" x1="220" y1="130" x2="290" y2="395" gradientUnits="userSpaceOnUse">
      <stop stop-color="#FFFFFF"/>
      <stop offset="1" stop-color="#E2E8F0"/>
    </linearGradient>
  </defs>
  <path d="M256 100C178.68 100 116 162.68 116 240C116 266.27 123.23 290.87 135.79 311.97L121.5 370L181.22 355.67C203.79 367.62 229.38 374.58 256 374.58C333.32 374.58 396 311.9 396 234.58C396 160.7 333.32 100 256 100Z" fill="white" fill-opacity="0.22"/>
  <path d="M285 130L170 275H250L225 395L342 250H262L285 130Z" fill="url(#iconBolt)"/>
</svg>`)
}

// handleStaticAppJS serves client-side application bundle for Web Dashboard (S12)
func (s *Server) handleStaticAppJS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(dashboardJS))
}

// handleStaticAuthJS serves client-side authentication validation bundle (S12)
func (s *Server) handleStaticAuthJS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed"})
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(authJS))
}

// handleLaunchAppWindow launches the native standalone desktop window (S7 localhost verified, S16 POST enforced)
func (s *Server) handleLaunchAppWindow(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed. Use POST."})
		return
	}
	if !security.IsLocalRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Host action only allowed from localhost"})
		return
	}

	appURL := fmt.Sprintf("http://localhost:%d", s.cfg.Server.Port)
	_, err := desktop.LaunchAppWindow(appURL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Desktop app window launched successfully"})
}

// handleCreateShortcut creates the Windows Desktop shortcut (S7 localhost verified, S16 POST enforced)
func (s *Server) handleCreateShortcut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "Method not allowed. Use POST."})
		return
	}
	if !security.IsLocalRequest(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Host action only allowed from localhost"})
		return
	}

	err := desktop.EnsureDesktopShortcut("")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "Desktop shortcut 'WhatsApp Automation.lnk' created on your Desktop"})
}

// handleDashboard renders the modern Glassmorphic interface
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	// Ensure CSRF cookie is active
	if csrfCookie, err := r.Cookie("wa_csrf"); err != nil || csrfCookie.Value == "" {
		token := security.GenerateCSRFToken()
		http.SetCookie(w, &http.Cookie{
			Name:     "wa_csrf",
			Value:    token,
			Path:     "/",
			HttpOnly: false,
			SameSite: http.SameSiteLaxMode,
			MaxAge:   7 * 24 * 3600,
		})
	}

	var rawStatus client.ClientStatus
	if s.client != nil {
		rawStatus = s.client.GetStatus()
	}
	statusText := "Disconnected"
	if rawStatus.Connected && rawStatus.LoggedIn {
		statusText = "Connected"
	} else if rawStatus.LatestQR != "" {
		statusText = "Waiting for QR"
	}

	rules := s.cfg.GetRules()

	var contactList []contacts.Contact
	var tagsList []string
	if s.contactStore != nil && s.isWhatsAppLoggedIn() {
		contactList = s.contactStore.List()
		tagsList = s.contactStore.GetAllTags()
	}

	var templateList []templates.Template
	if s.templateStore != nil {
		templateList = s.templateStore.List()
	}

	var scheduledList []scheduler.ScheduledJob
	if s.scheduler != nil {
		scheduledList = s.scheduler.List()
	}

	inCount, outCount := 0, 0
	if s.histStore != nil {
		inCount, outCount = s.histStore.TotalStats()
	}

	aiCfg := s.cfg.AI
	if s.aiEngine != nil {
		c := s.aiEngine.GetConfig()
		aiCfg = config.AIConfig{
			Enabled:      c.Enabled,
			APIKey:       c.APIKey,
			Model:        c.Model,
			SystemPrompt: c.SystemPrompt,
		}
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	data := map[string]any{
		"Status":            statusText,
		"ClientStatus":      rawStatus,
		"Uptime":            time.Since(s.startTime).Round(time.Second).String(),
		"Rules":             rules,
		"RulesCount":        len(rules),
		"Contacts":          contactList,
		"ContactsCount":     len(contactList),
		"Tags":              tagsList,
		"Templates":         templateList,
		"TemplatesCount":    len(templateList),
		"Scheduled":         scheduledList,
		"ScheduledCount":    len(scheduledList),
		"MessagesIn":        inCount,
		"MessagesOut":       outCount,
		"IsBroadcasting":    s.isBroadcasting,
		"BusinessHours":     s.cfg.BusinessHours,
		"AI":                aiCfg,
		"Automation":        s.cfg.Automation,
		"Webhook":           s.cfg.Webhook,
		"ServerPort":        s.cfg.Server.Port,
		"MemUsageMB":        fmt.Sprintf("%.1f MB", float64(m.Alloc)/(1024*1024)),
		"Goroutines":        runtime.NumGoroutine(),
		"IsDefaultPassword": s.cfg.IsDefaultPassword(),
	}

	var buf bytes.Buffer
	if err := dashboardTmpl.Execute(&buf, data); err != nil {
		http.Error(w, fmt.Sprintf("Template rendering error: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}
