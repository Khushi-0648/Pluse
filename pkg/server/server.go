// Package server provides the dashboard, its JSON API and the inbound webhook.
package server

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/bot"
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

//go:embed web
var webFS embed.FS

// Messenger sends messages on behalf of staff and integrations.
type Messenger interface {
	SendTextMessage(ctx context.Context, target, text string) (string, error)
	SendMediaMessage(ctx context.Context, target string, data []byte, fileName, mimeType, caption string) (string, error)
}

// Connection is the WhatsApp Business API connection used by the dashboard.
type Connection interface {
	GetStatus() cloudapi.Status
	Check(ctx context.Context) (cloudapi.Status, error)
	Probe(ctx context.Context, version, phoneID, token string) (cloudapi.PhoneInfo, error)
	Forget()
	HandleWebhook(body []byte) (int, error)
}

// Deps are the services the server needs. Any of them may be nil in tests.
type Deps struct {
	Config    *config.Config
	Client    *cloudapi.Client
	Messenger Messenger
	Conn      Connection
	Contacts  *contacts.Store
	Templates *templates.Store
	History   *history.Store
	Scheduler *scheduler.Scheduler
	Campaigns *campaign.Runner
	Engine    *bot.Engine
	Hooks     *webhook.Dispatcher
	Alerts    interface{ Test() error } // desktop notifications (Settings > Alerts > test)
	// Quit asks the app to close normally; ControlToken (from a file only this Windows
	// user can read) authorises it. Used by "whatsapp-auto stop" and the scripts.
	Quit         func()
	ControlToken string
	DataDir      string
	Profile      string // the -session name; empty for the main profile
	Encrypted    bool
	// OldSessionFiles are where the linked-device versions (before 2.0) kept their
	// WhatsApp session; the owner may delete them from Settings > Privacy.
	OldSessionFiles []string
}

// Server is the HTTP front end.
type Server struct {
	Deps
	cfg         *config.Config
	sessions    *security.SessionManager
	limiter     *security.RateLimiter
	hookLimiter *security.RateLimiter // Meta's webhook, counted apart so it never locks out the dashboard
	httpServer  *http.Server
	handler     http.Handler
	startTime   time.Time
	stopHealth  chan struct{}
	healthOnce  sync.Once
	watchOnce   sync.Once
	sigMu       sync.Mutex           // guards seenSigs
	seenSigs    map[string]time.Time // inbound webhook signatures already used (replay guard)

	rejectMu      sync.Mutex           // guards the two fields below
	rejectLogged  map[string]time.Time // per activity action: when a refused call was last logged
	rejectSkipped map[string]int       // per activity action: refused calls since then not logged
}

const (
	sessionCookie = "pulse_session"
	maxJSONBody   = 1 << 20
)

// New builds the server and its routes.
func New(d Deps) *Server {
	if d.Config == nil {
		d.Config = config.DefaultConfig()
	}
	if d.Client != nil {
		if d.Messenger == nil {
			d.Messenger = d.Client
		}
		if d.Conn == nil {
			d.Conn = d.Client
		}
	}
	s := &Server{Deps: d, cfg: d.Config, sessions: security.NewSessionManager(7 * 24 * time.Hour), limiter: security.NewRateLimiter(), hookLimiter: security.NewRateLimiter(), startTime: time.Now(), stopHealth: make(chan struct{})}
	s.applyTrustedProxies()
	s.handler = s.securityMiddleware(s.routes())
	snap := d.Config.Snapshot()
	s.httpServer = &http.Server{
		Addr:              net.JoinHostPort(snap.Server.Host, fmt.Sprint(snap.ListenPort())),
		Handler:           s.handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	return s
}

// webFile serves one embedded file. It is checked for updates on every load, so a new
// version of Pulse replaces the installed app's service worker at once.
func webFile(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := webFS.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	}
}

// Handler returns the HTTP handler (tests).
func (s *Server) Handler() http.Handler { return s.handler }

// Start listens until Stop is called.
func (s *Server) Start() error {
	s.watchOnce.Do(func() { go s.healthWatch() }) // Start may be called again after a failure
	if err := s.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop shuts the server down.
func (s *Server) Stop(ctx context.Context) error {
	s.healthOnce.Do(func() { close(s.stopHealth) })
	s.sessions.Close()
	return s.httpServer.Shutdown(ctx)
}

type route struct {
	method string
	path   string
	role   string // "", "operator", "admin", roleOwner
	h      http.HandlerFunc
}

// roleOwner is "admin signed in to the dashboard". The developer (API) key works like the
// admin only for reading customer data and sending messages (S34): it cannot change
// settings, rules, templates, campaigns, the connection, passwords or keys, read the
// activity log, or take customer data out of Pulse (backups, exports, deletion).
// roleSession is "staff or admin signed in to the dashboard", for staff actions the
// API key must not do (it deletes nothing).
const (
	roleOwner   = "owner"
	roleSession = "session"
)

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	assets, _ := fs.Sub(webFS, "web/assets")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", cacheControl(http.FileServer(http.FS(assets)))))
	// Installing the dashboard as an app (Android, desktop browsers) needs these two at the
	// top of the site: a service worker only controls pages under the folder it is in.
	mux.HandleFunc("GET /manifest.webmanifest", webFile("web/manifest.webmanifest", "application/manifest+json"))
	mux.HandleFunc("GET /sw.js", webFile("web/sw.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /login", s.handleLoginPage)
	mux.HandleFunc("POST /login", s.handleLogin)
	mux.HandleFunc("GET /setup-password", s.handleSetupPage)
	mux.HandleFunc("POST /setup-password", s.handleSetupPassword)
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /logout", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusSeeOther) })
	mux.HandleFunc("POST /api/v1/webhook/send", s.handleInboundWebhook)
	mux.HandleFunc("POST /control/quit", s.handleQuit)
	mux.HandleFunc(config.WebhookPath, s.handleCloudWebhook)

	for _, rt := range []route{
		{"GET", "/api/v1/me", "operator", s.handleMe},
		{"POST", "/api/v1/logout", "operator", s.handleLogout},
		{"GET", "/api/v1/overview", "operator", s.handleOverview},
		{"GET", "/api/v1/status", "operator", s.handleStatus},
		{"GET", "/api/v1/health", "operator", s.handleHealth},
		{"GET", "/api/v1/system", "admin", s.handleSystem},
		{"GET", "/api/v1/connection", roleOwner, s.handleGetConnection},
		{"POST", "/api/v1/connection", roleOwner, s.handleSaveConnection},
		{"POST", "/api/v1/connection/test", roleOwner, s.handleTestConnection},
		{"POST", "/api/v1/connection/disconnect", roleOwner, s.handleDisconnect},
		{"POST", "/api/v1/setup/acknowledge-risk", roleOwner, s.handleAcknowledgeRisk},
		{"POST", "/api/v1/setup/test-message", roleOwner, s.handleTestMessage},

		{"GET", "/api/v1/conversations", "operator", s.handleConversations},
		{"POST", "/api/v1/conversations/read", "operator", s.handleMarkRead},
		{"POST", "/api/v1/conversations/meta", "operator", s.handleChatMeta},
		{"GET", "/api/v1/messages", "operator", s.handleMessages},
		{"DELETE", "/api/v1/messages", roleOwner, s.handleDeleteMessages}, // the API key deletes nothing
		{"POST", "/api/v1/messages/send", "operator", s.handleSendMessage},
		{"POST", "/api/v1/media/validate", "operator", s.handleValidateMedia},

		{"GET", "/api/v1/contacts", "operator", s.handleListContacts},
		{"POST", "/api/v1/contacts", "operator", s.handleSaveContact},
		{"GET", "/api/v1/contacts/tags", "operator", s.handleTags},
		{"GET", "/api/v1/contacts/export.csv", roleOwner, s.handleExportContacts},
		{"POST", "/api/v1/contacts/import/preview", roleOwner, s.handleImportPreview},
		{"POST", "/api/v1/contacts/import", roleOwner, s.handleImport},
		{"POST", "/api/v1/contacts/bulk", "operator", s.handleBulkContacts},
		{"POST", "/api/v1/contacts/consent", "operator", s.handleConsent},
		{"POST", "/api/v1/contacts/dnd", "operator", s.handleDND},
		{"POST", "/api/v1/contacts/resume-bot", "operator", s.handleResumeBot},
		{"POST", "/api/v1/contacts/pause-bot", "operator", s.handlePauseBot},
		{"GET", "/api/v1/contacts/{phone}", "operator", s.handleGetContact},
		{"DELETE", "/api/v1/contacts/{phone}", roleOwner, s.handleDeleteContact},

		{"GET", "/api/v1/variables", "operator", s.handleVariables},
		{"GET", "/api/v1/rules", "admin", s.handleListRules}, // staff never see the rules
		{"POST", "/api/v1/rules", roleOwner, s.handleSaveRule},
		{"POST", "/api/v1/rules/toggle", roleOwner, s.handleToggleRule},
		{"POST", "/api/v1/rules/reorder", roleOwner, s.handleReorderRules},
		{"POST", "/api/v1/rules/test", "admin", s.handleTestRule},
		{"POST", "/api/v1/rules/reset-defaults", roleOwner, s.handleResetRules},
		{"DELETE", "/api/v1/rules/{name}", roleOwner, s.handleDeleteRule},
		{"GET", "/api/v1/templates", "operator", s.handleListTemplates},
		{"POST", "/api/v1/templates", roleOwner, s.handleSaveTemplate},
		{"POST", "/api/v1/templates/preview", "operator", s.handlePreviewText},
		{"DELETE", "/api/v1/templates/{id}", roleOwner, s.handleDeleteTemplate},

		{"GET", "/api/v1/campaigns", "admin", s.handleListCampaigns},
		{"POST", "/api/v1/campaigns", roleOwner, s.handleStartCampaign},
		{"POST", "/api/v1/campaigns/preview", "admin", s.handlePreviewCampaign},
		{"POST", "/api/v1/campaigns/test-send", roleOwner, s.handleTestCampaign},
		{"GET", "/api/v1/campaigns/{id}", "admin", s.handleGetCampaign},
		{"GET", "/api/v1/campaigns/{id}/report.csv", "admin", s.handleCampaignCSV},
		{"POST", "/api/v1/campaigns/{id}/{action}", roleOwner, s.handleCampaignAction},
		{"DELETE", "/api/v1/campaigns/{id}", roleOwner, s.handleDeleteCampaign},

		{"GET", "/api/v1/scheduled", "operator", s.handleListScheduled},
		{"POST", "/api/v1/scheduled", "operator", s.handleAddScheduled},
		{"POST", "/api/v1/scheduled/{id}/{action}", "operator", s.handleScheduledAction},
		{"DELETE", "/api/v1/scheduled/{id}", roleSession, s.handleDeleteScheduled},

		{"GET", "/api/v1/settings", roleOwner, s.handleGetSettings}, // S25: staff never need the settings
		{"PATCH", "/api/v1/settings", roleOwner, s.handlePatchSettings},
		{"POST", "/api/v1/settings/password", roleOwner, s.handleChangePassword},
		{"POST", "/api/v1/settings/operator-password", roleOwner, s.handleOperatorPassword},
		{"POST", "/api/v1/settings/api-key", roleOwner, s.handleAPIKey},
		{"POST", "/api/v1/settings/remote-access", roleOwner, s.handleRemoteAccess},
		{"POST", "/api/v1/settings/autostart", roleOwner, s.handleAutostart},
		{"GET", "/api/v1/analytics", "admin", s.handleAnalytics},
		{"GET", "/api/v1/analytics/export.csv", roleOwner, s.handleAnalyticsCSV},
		{"GET", "/api/v1/audit", roleOwner, s.handleAudit},
		{"GET", "/api/v1/audit/export.csv", roleOwner, s.handleAuditCSV},
		{"GET", "/api/v1/privacy/export", roleOwner, s.handlePrivacyExport},
		{"POST", "/api/v1/privacy/purge", roleOwner, s.handlePrivacyPurge},
		{"POST", "/api/v1/privacy/delete-imported", roleOwner, s.handleDeleteImported},
		{"POST", "/api/v1/privacy/forget-contact", roleOwner, s.handleForgetContact},
		{"GET", "/api/v1/backup", roleOwner, s.handleBackup},
		{"POST", "/api/v1/restore", roleOwner, s.handleRestore},
		{"GET", "/api/v1/backups", roleOwner, s.handleListBackups},
		{"POST", "/api/v1/backups/now", roleOwner, s.handleBackupNow},
		{"POST", "/api/v1/backups/restore", roleOwner, s.handleRestoreAutoBackup},
		{"POST", "/api/v1/privacy/delete-old-copies", roleOwner, s.handleDeleteOldCopies},
		{"POST", "/api/v1/privacy/delete-old-session", roleOwner, s.handleDeleteOldSession},
		{"POST", "/api/v1/webhook/test", roleOwner, s.handleWebhookTest},
		{"GET", "/api/v1/webhook/failed", roleOwner, s.handleWebhookFailed},
		{"POST", "/api/v1/webhook/replay", roleOwner, s.handleWebhookReplay},
		{"DELETE", "/api/v1/webhook/failed", roleOwner, s.handleWebhookClearFailed},
		{"POST", "/api/v1/notify/test", roleOwner, s.handleTestAlert},
		{"POST", "/api/v1/app/launch-window", roleOwner, s.handleLaunchWindow},
		{"POST", "/api/v1/app/create-shortcut", roleOwner, s.handleCreateShortcut},
	} {
		mux.HandleFunc(rt.method+" "+rt.path, s.protect(rt.role, rt.h))
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "Unknown API endpoint.")
	})
	return mux
}

func cacheControl(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		// The files are built into the program, so one fingerprint of all of them changes
		// exactly when an upgrade changes any file: browsers revalidate with a small 304
		// instead of downloading every script on each load, yet never keep an old one.
		// http.FileServer compares If-None-Match with this ETag itself, after finding the
		// file, so a missing file is still a 404.
		w.Header().Set("ETag", webTag())
		h.ServeHTTP(w, r)
	})
}

var (
	webTagOnce sync.Once
	webTagVal  string
)

// webTag is a fingerprint of every embedded dashboard file.
func webTag() string {
	webTagOnce.Do(func() {
		h := sha256.New()
		_ = fs.WalkDir(webFS, "web", func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			data, _ := webFS.ReadFile(path)
			fmt.Fprintf(h, "%s:%d:", path, len(data))
			h.Write(data)
			return nil
		})
		webTagVal = `"` + hex.EncodeToString(h.Sum(nil)[:12]) + `"`
	})
	return webTagVal
}

func (s *Server) allowedHosts() []string {
	snap := s.cfg.Snapshot()
	return append([]string{snap.Server.Host}, snap.Server.AllowedHosts...)
}

// applyTrustedProxies loads the proxies whose forwarding headers are believed. With
// remote access on, the tunnel program (cloudflared, ngrok) runs on this computer and
// connects from here, so this computer counts as one, but only for requests addressed
// to the public host (S1): the real caller's address then comes from X-Forwarded-For,
// and wrong passwords lock out that caller, not everyone. Any other local request (a
// program on this computer, a DNS-rebinding page) keeps the Host check, and its
// X-Forwarded-For is ignored.
func (s *Server) applyTrustedProxies() {
	snap := s.cfg.Snapshot().Server
	security.ResetTrustedProxies()
	for _, p := range snap.TrustedProxies {
		_ = security.AddTrustedProxy(p)
	}
	if snap.RemoteAccess {
		security.SetTunnelHost(s.remoteHost)
	}
}

// remoteHost is the host name phones use with remote access: the public address's, but
// only while remote access is on and that address is https. "" otherwise (S4).
func (s *Server) remoteHost() string {
	snap := s.cfg.Snapshot()
	if !snap.Server.RemoteAccess {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(snap.CloudAPI.PublicURL))
	if err != nil || u.Scheme != "https" {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// remoteDashboard reports whether r may use the dashboard from outside this computer:
// remote access is on with an https public address, the request is addressed to that
// address, it came through a trusted proxy (the tunnel), and the browser used https.
// Everything else through a tunnel stays refused.
func (s *Server) remoteDashboard(r *http.Request) bool {
	rh := s.remoteHost()
	return rh != "" && security.NamesHost(r, rh) && security.FromTrustedProxy(r) &&
		strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

// publicHost is the host name of the configured public address (tunnel), if any.
func (s *Server) publicHost() string {
	u, err := url.Parse(s.cfg.Snapshot().CloudAPI.PublicURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func hostOnly(h string) string {
	if name, _, err := net.SplitHostPort(h); err == nil {
		h = name
	}
	return strings.ToLower(strings.Trim(strings.TrimSpace(h), "[]"))
}

// forwardedHostIs reports whether any X-Forwarded-Host value names host. Proxies append
// to the header, so "evil, public.example" must still count as the public address (S36).
func forwardedHostIs(r *http.Request, host string) bool {
	for _, v := range r.Header.Values("X-Forwarded-Host") {
		for _, h := range strings.Split(v, ",") {
			if hostOnly(h) == host {
				return true
			}
		}
	}
	return false
}

// rateKey picks the request-limit bucket. Every local caller shares the address
// 127.0.0.1, so a web page firing requests at the dashboard would otherwise lock the
// owner out. A signed-in session gets its own bucket (a cross-site request cannot carry
// the SameSite=Strict cookie), and so do API-key callers (a cross-site request cannot set
// that header without a CORS preflight Pulse never grants).
func (s *Server) rateKey(r *http.Request) string {
	ip := security.GetClientIP(r)
	if c, err := r.Cookie(sessionCookie); err == nil && s.sessions.ValidateSession(c.Value) {
		sum := sha256.Sum256([]byte(c.Value))
		return ip + "|s-" + hex.EncodeToString(sum[:8]) // no ":", which the limiter would read as a port
	}
	if r.Header.Get("X-API-Key") != "" || r.Header.Get("Authorization") != "" {
		return ip + "|key"
	}
	if r.URL.Path == "/api/v1/webhook/send" {
		// Its own bucket: refused integration calls (which any web page can make) must
		// not use up the requests the owner needs to sign in.
		return ip + "|hook"
	}
	return ip
}

func (s *Server) securityMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		security.ApplySecurityHeaders(w)
		// Meta's webhook arrives through the owner's public tunnel, under the tunnel's
		// host name. Only this one path is exempt from the host check (it proves itself
		// with the verify token and the app secret signature instead). It is not rate
		// limited here: behind a tunnel every caller shares the tunnel's address, so a
		// limit would let forged calls lock out Meta. Rejected calls are limited in the
		// handler instead.
		if r.URL.Path == config.WebhookPath {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
			return
		}
		// Nothing but the webhook is served under the public address, even if that host
		// was also added to the allowed hosts: the dashboard stays private. A tunnel that
		// rewrites the Host header to 127.0.0.1 is caught by its forwarding headers.
		// Only with remote access switched on does the dashboard answer there, and then
		// only over https through a trusted proxy (see remoteDashboard).
		ph := s.publicHost()
		viaPublic := ph != "" && (hostOnly(r.Host) == ph || forwardedHostIs(r, ph))
		switch {
		case viaPublic || (s.cfg.Snapshot().Server.RemoteAccess && security.ViaUntrustedProxy(r)):
			if !s.remoteDashboard(r) {
				http.NotFound(w, r)
				return
			}
			// S5: the developer key and the integration endpoint are for programs on this
			// computer or network, never through the tunnel (there is no setting for that).
			if r.URL.Path == "/api/v1/webhook/send" || r.Header.Get("X-API-Key") != "" || r.Header.Get("Authorization") != "" {
				http.NotFound(w, r)
				return
			}
			r.Host = s.remoteHost() // the address the browser used: Origin checks compare with it
			security.ApplyRemoteHeaders(w)
			// S6: through the tunnel the browser used https, so its Origin must say so too.
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				if o := r.Header.Get("Origin"); !security.IsAllowedSecureOrigin(o, r.Host) {
					writeError(w, http.StatusForbidden, "Cross-site request blocked.")
					return
				}
			}
		case security.ViaUntrustedProxy(r):
			http.NotFound(w, r)
			return
		case !security.ValidateHostHeader(r.Host, s.allowedHosts()...):
			writeError(w, http.StatusForbidden, "Invalid Host header.")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") || (r.Method == http.MethodPost && (r.URL.Path == "/login" || r.URL.Path == "/setup-password")) {
			w.Header().Set("Cache-Control", "no-store")
			if !s.limiter.AllowAPIRequest(s.rateKey(r)) {
				writeError(w, http.StatusTooManyRequests, "Too many requests. Wait a minute and try again.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type authInfo struct {
	role    string
	session string
	apiKey  bool
}

func (s *Server) authenticate(r *http.Request) (authInfo, bool) {
	got := r.Header.Get("X-API-Key")
	if got == "" {
		if h := r.Header.Get("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
			got = strings.TrimSpace(h[7:])
		}
	}
	if got != "" && s.cfg.CheckAPIKey(got) {
		return authInfo{role: "admin", apiKey: true}, true
	}
	if c, err := r.Cookie(sessionCookie); err == nil && s.sessions.ValidateSession(c.Value) {
		if role := s.sessions.GetSessionRole(c.Value); role != "" {
			return authInfo{role: role, session: c.Value}, true
		}
	}
	return authInfo{}, false
}

type ctxKey struct{}

func authOf(r *http.Request) authInfo {
	a, _ := r.Context().Value(ctxKey{}).(authInfo)
	return a
}

func actor(r *http.Request) string {
	a := authOf(r)
	if a.apiKey {
		return "api-key"
	}
	if a.role == "" {
		return "system"
	}
	return a.role
}

// apiKeyRoutes is everything the developer (API) key may do: read messages, conversations,
// contacts and templates, and send messages (S34). Every other route needs someone signed
// in to the dashboard, so a key kept in another program cannot change settings, rules or
// consent, schedule or delete anything, or read the activity log, reports or exports.
var apiKeyRoutes = map[string]bool{
	"GET /api/v1/me": true, "GET /api/v1/status": true,
	"GET /api/v1/conversations": true, "GET /api/v1/messages": true,
	"POST /api/v1/messages/send": true, "POST /api/v1/media/validate": true,
	"GET /api/v1/contacts": true, "GET /api/v1/contacts/tags": true, "GET /api/v1/contacts/{phone}": true,
	"GET /api/v1/variables": true, "GET /api/v1/templates": true, "POST /api/v1/templates/preview": true,
}

func (s *Server) protect(role string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := s.authenticate(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "Please sign in again.")
			return
		}
		if a.apiKey && !apiKeyRoutes[r.Pattern] {
			writeError(w, http.StatusForbidden, "The developer key can only read messages, contacts and templates and send messages. Sign in to the dashboard for this.")
			return
		}
		if s.cfg.MustChange() && !a.apiKey && r.URL.Path != "/api/v1/me" && r.URL.Path != "/api/v1/logout" {
			writeError(w, http.StatusForbidden, "Choose your own password first.")
			return
		}
		if role == roleSession {
			if a.apiKey {
				writeError(w, http.StatusForbidden, "The developer key cannot do this. Sign in to the dashboard.")
				return
			}
			role = "operator"
		}
		if role == roleOwner {
			if a.apiKey {
				writeError(w, http.StatusForbidden, "The developer key cannot do this. Sign in to the dashboard as the admin.")
				return
			}
			role = "admin"
		}
		if role == "admin" && a.role != "admin" {
			writeError(w, http.StatusForbidden, "Only the admin can do this.")
			return
		}
		if !a.apiKey && r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" && !security.IsAllowedOrigin(o, r.Host) {
				writeError(w, http.StatusForbidden, "Cross-site request blocked.")
				return
			}
			if !security.ConstantTimeEqual(r.Header.Get("X-CSRF-Token"), s.sessions.GetCSRFToken(a.session)) {
				writeError(w, http.StatusForbidden, "Your session expired. Reload the page.")
				return
			}
		}
		h(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)))
	}
}

// reauth checks the admin password for destructive actions (S7).
func (s *Server) reauth(w http.ResponseWriter, r *http.Request, password string) bool {
	if authOf(r).apiKey {
		writeError(w, http.StatusForbidden, "This action needs the admin signed in to the dashboard; the API key cannot do it.")
		return false
	}
	// The same counter as sign-in: 5 wrong passwords in total lock both for 5 minutes, so
	// a stolen session gets no extra guesses.
	ip := security.GetClientIP(r)
	if !s.beginPasswordCheck(r, ip) {
		writeError(w, http.StatusTooManyRequests, "Too many wrong passwords. Wait 5 minutes.")
		return false
	}
	if !s.cfg.VerifyPassword(password) {
		security.LogAudit("REAUTH_FAILED", ip, actor(r), "Wrong password for "+r.URL.Path, "failed")
		writeError(w, http.StatusForbidden, "The password is not correct.")
		return false
	}
	s.limiter.ResetLoginFails(ip)
	return true
}

// beginPasswordCheck counts one password attempt (sign-in or re-check) for ip. Besides
// the caller's own lockout, too many wrong passwords from all callers together pause
// every attempt from outside (S2); a request made directly on this computer is exempt
// from that pause only, so the owner can always sign in at the computer.
func (s *Server) beginPasswordCheck(r *http.Request, ip string) bool {
	if security.IsLocalRequest(r) {
		return s.limiter.BeginLocalLoginAttempt(ip)
	}
	return s.limiter.BeginLoginAttempt(ip)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeOK(w http.ResponseWriter, extra map[string]any) {
	if extra == nil {
		extra = map[string]any{}
	}
	extra["ok"] = true
	writeJSON(w, http.StatusOK, extra)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxJSONBody))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "The request is too large.")
		return false
	}
	body = []byte(strings.TrimPrefix(string(body), "\xef\xbb\xbf"))
	if len(strings.TrimSpace(string(body))) == 0 {
		body = []byte("{}")
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeError(w, http.StatusBadRequest, "Pulse could not read that request. Reload the page and try again.")
		return false
	}
	return true
}

// handleQuit closes the app normally (saving and encrypting everything) when asked from
// this computer with the control token.
func (s *Server) handleQuit(w http.ResponseWriter, r *http.Request) {
	if !security.IsLocalRequest(r) || s.Quit == nil || !security.ConstantTimeEqual(r.Header.Get("X-Pulse-Control"), s.ControlToken) {
		writeError(w, http.StatusForbidden, "Not allowed.")
		return
	}
	writeOK(w, map[string]any{"message": "Pulse is closing."})
	go s.Quit()
}

// uploadError reports a failed multipart upload: too large, or simply unreadable.
func uploadError(w http.ResponseWriter, err error, tooLarge string) {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		writeError(w, http.StatusRequestEntityTooLarge, tooLarge)
		return
	}
	writeError(w, http.StatusBadRequest, "The file could not be read. Choose it again and retry.")
}

func clientIP(r *http.Request) string { return security.GetClientIP(r) }

func (s *Server) status() cloudapi.Status {
	if s.Conn != nil {
		return s.Conn.GetStatus()
	}
	return cloudapi.Status{}
}
