package server

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"whatsapp-automation/pkg/security"
	"whatsapp-automation/pkg/version"
)

var pageTmpl = template.Must(template.ParseFS(webFS, "web/login.html", "web/setup.html", "web/index.html"))

type pageData struct {
	Product string
	Version string
	Error   string
}

func (s *Server) page(w http.ResponseWriter, name string, code int, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = pageTmpl.ExecuteTemplate(w, name, pageData{Product: version.Product, Version: version.Version, Error: errMsg})
}

// setSession sets the session cookie. It is marked Secure when the browser reached
// Pulse over https: directly, or through a configured trusted proxy that says so (S44).
func (s *Server) setSession(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	secure := r.TLS != nil || security.FromTrustedProxy(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	a, ok := s.authenticate(r)
	if !ok || a.apiKey {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if s.cfg.MustChange() {
		http.Redirect(w, r, "/setup-password", http.StatusSeeOther)
		return
	}
	s.page(w, "index.html", http.StatusOK, "")
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if a, ok := s.authenticate(r); ok && !a.apiKey {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.page(w, "login.html", http.StatusOK, "")
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if o := r.Header.Get("Origin"); o != "" && !security.IsAllowedOrigin(o, r.Host) {
		s.page(w, "login.html", http.StatusForbidden, "This sign-in request came from another website and was blocked.")
		return
	}
	if !s.beginPasswordCheck(r, ip) {
		if !security.IsLocalRequest(r) && s.limiter.LoginPausedForAll() {
			s.logThrottled("LOGIN_LOCKED", ip, "Sign-in paused: too many wrong passwords from all callers")
			s.page(w, "login.html", http.StatusTooManyRequests, "Too many wrong passwords were tried. Sign-in from other devices is paused for a while; on the computer running Pulse you can still sign in.")
			return
		}
		s.logThrottled("LOGIN_LOCKED", ip, "Too many failed sign-ins")
		s.page(w, "login.html", http.StatusTooManyRequests, "Too many wrong passwords. Wait 5 minutes and try again.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	role, ok := s.cfg.Authenticate(r.FormValue("password"))
	if !ok {
		s.logThrottled("LOGIN_FAILED", ip, "Wrong password")
		s.page(w, "login.html", http.StatusUnauthorized, "That password is not correct.")
		return
	}
	if s.cfg.MustChange() && role != "admin" {
		s.page(w, "login.html", http.StatusForbidden, "The admin has to finish setting up Pulse before staff can sign in.")
		return
	}
	s.limiter.ResetLoginFails(ip)
	// A session from outside this computer (a phone through the tunnel) also ends after
	// 12 hours without use (S9).
	var idle time.Duration
	if !security.IsLocalRequest(r) {
		idle = remoteSessionIdle
	}
	token, _ := s.sessions.CreateSessionWithIdle(string(role), idle)
	s.setSession(w, r, token, 7*24*3600)
	security.LogAudit("LOGIN", ip, string(role), "Signed in", "success")
	if s.cfg.MustChange() && role == "admin" {
		http.Redirect(w, r, "/setup-password", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// remoteSessionIdle ends a session created from outside this computer after this long
// without use.
const remoteSessionIdle = 12 * time.Hour

// logThrottled writes a failed sign-in of one kind (action) to the activity log at most
// once a minute, with the number left out since then, so guessing through the public
// tunnel cannot push older entries out of the log (S3).
func (s *Server) logThrottled(action, ip, reason string) {
	key := "auth:" + action
	s.rejectMu.Lock()
	if s.rejectLogged == nil {
		s.rejectLogged, s.rejectSkipped = map[string]time.Time{}, map[string]int{}
	}
	now := time.Now()
	if now.Sub(s.rejectLogged[key]) < time.Minute {
		s.rejectSkipped[key]++
		s.rejectMu.Unlock()
		return
	}
	skipped := s.rejectSkipped[key]
	s.rejectLogged[key], s.rejectSkipped[key] = now, 0
	s.rejectMu.Unlock()
	if skipped > 0 {
		reason += fmt.Sprintf(" (and %d more in the minute before)", skipped)
	}
	security.LogAudit(action, ip, "unknown", reason, "failed")
}

func (s *Server) handleSetupPage(w http.ResponseWriter, r *http.Request) {
	a, ok := s.authenticate(r)
	if !ok || a.role != "admin" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if !s.cfg.MustChange() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.page(w, "setup.html", http.StatusOK, "")
}

func (s *Server) handleSetupPassword(w http.ResponseWriter, r *http.Request) {
	a, ok := s.authenticate(r)
	if !ok || a.role != "admin" || a.apiKey {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	if o := r.Header.Get("Origin"); o != "" && !security.IsAllowedOrigin(o, r.Host) {
		s.page(w, "setup.html", http.StatusForbidden, "This request came from another website and was blocked.")
		return
	}
	if !s.cfg.MustChange() {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	p1, p2 := strings.TrimSpace(r.FormValue("new_password")), strings.TrimSpace(r.FormValue("confirm_password"))
	if p1 != p2 {
		s.page(w, "setup.html", http.StatusBadRequest, "The two passwords do not match.")
		return
	}
	if err := s.cfg.SetPassword(p1); err != nil {
		s.page(w, "setup.html", http.StatusBadRequest, err.Error())
		return
	}
	s.sessions.DestroyAll(a.session)
	security.LogAudit("PASSWORD_SET", clientIP(r), "admin", "Admin password chosen on first sign-in", "success")
	removeFirstRunNotice(s.DataDir)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// handleLogout ends this device's dashboard session, for the admin and staff alike.
// WhatsApp stays connected and no data is touched: disconnecting and deleting everything are
// separate, password-protected actions (Settings > Privacy and Settings > WhatsApp connection).
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	if a.session != "" {
		s.sessions.DestroySession(a.session)
	}
	s.setSession(w, r, "", -1)
	security.LogAudit("LOGOUT", clientIP(r), actor(r), "Signed out of the dashboard", "success")
	writeOK(w, map[string]any{"message": "Signed out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"role": a.role, "csrf": s.sessions.GetCSRFToken(a.session), "product": version.Product, "version": version.Version,
		"must_change_password": s.cfg.MustChange(),
		// local: opened on the computer running Pulse (shortcuts, start with Windows and
		// closing Pulse only work there, not from a phone).
		"local": security.IsLocalRequest(r), "remote_access": s.cfg.Snapshot().Server.RemoteAccess,
	})
}

// handleRemoteAccess switches using the dashboard from phones (through the public https
// address) on or off. It needs the admin password, and a fixed https address to switch on.
func (s *Server) handleRemoteAccess(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled  bool   `json:"enabled"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password) {
		return
	}
	if req.Enabled {
		u, err := url.Parse(s.cfg.Snapshot().CloudAPI.PublicURL)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" {
			writeError(w, http.StatusBadRequest, "Enter your fixed https address first (Settings > WhatsApp connection > Public address, for example https://pulse.yourshop.com). Phones reach Pulse through it.")
			return
		}
	}
	if err := s.cfg.Update(func(c *configT) error { c.Server.RemoteAccess = req.Enabled; return nil }); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.applyTrustedProxies()
	state := "off"
	if req.Enabled {
		state = "on"
	}
	security.LogAudit("REMOTE_ACCESS", clientIP(r), actor(r), "Using the dashboard from phones and other devices switched "+state, "warning")
	writeOK(w, map[string]any{"remote_access": req.Enabled, "address": s.cfg.Snapshot().CloudAPI.PublicURL})
}
