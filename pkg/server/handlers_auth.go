package server

import (
	"html/template"
	"net/http"
	"strings"

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

func (s *Server) setSession(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
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
	if !s.limiter.BeginLoginAttempt(ip) {
		security.LogAudit("LOGIN_LOCKED", ip, "unknown", "Too many failed sign-ins", "failed")
		s.page(w, "login.html", http.StatusTooManyRequests, "Too many wrong passwords. Wait 5 minutes and try again.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	role, ok := s.cfg.Authenticate(r.FormValue("password"))
	if !ok {
		security.LogAudit("LOGIN_FAILED", ip, "unknown", "Wrong password", "failed")
		s.page(w, "login.html", http.StatusUnauthorized, "That password is not correct.")
		return
	}
	if s.cfg.MustChange() && role != "admin" {
		s.page(w, "login.html", http.StatusForbidden, "The admin has to finish setting up Pulse before staff can sign in.")
		return
	}
	s.limiter.ResetLoginFails(ip)
	token, _ := s.sessions.CreateSessionWithRole(string(role))
	s.setSession(w, token, 7*24*3600)
	security.LogAudit("LOGIN", ip, string(role), "Signed in", "success")
	if s.cfg.MustChange() && role == "admin" {
		http.Redirect(w, r, "/setup-password", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
	s.setSession(w, "", -1)
	security.LogAudit("LOGOUT", clientIP(r), actor(r), "Signed out of the dashboard", "success")
	writeOK(w, map[string]any{"message": "Signed out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	a := authOf(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"role": a.role, "csrf": s.sessions.GetCSRFToken(a.session), "product": version.Product, "version": version.Version,
		"must_change_password": s.cfg.MustChange(),
	})
}
