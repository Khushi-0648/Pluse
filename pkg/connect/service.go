package connect

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/security"
)

//go:embed web
var webFS embed.FS

var signupTmpl = template.Must(template.ParseFS(webFS, "web/signup.html"))

const (
	signupTTL  = 30 * time.Minute // how long a signup page may take
	handoffTTL = 5 * time.Minute  // how long Pulse has to collect the result
	maxPending = 10000
	maxBody    = 1 << 20
	pollMax    = 100
	pollWait   = 25 * time.Second
	nonceName  = "pc_nonce"
)

// Handoff is the result of a signup, given to the user's Pulse exactly once.
type Handoff struct {
	AccessToken   string `json:"access_token"`
	RelayKey      string `json:"relay_key"`
	InstallID     string `json:"install_id"`
	WABAID        string `json:"waba_id"`
	PhoneNumberID string `json:"phone_number_id"`
	DisplayPhone  string `json:"display_phone"`
	VerifiedName  string `json:"verified_name"`
}

type pendingSignup struct {
	state, ret string
	expires    time.Time
}

type pendingHandoff struct {
	h       Handoff
	expires time.Time
}

// Service is the connect service.
type Service struct {
	cfg     Config
	store   *store
	http    *http.Client
	limiter *security.RateLimiter
	hooks   *security.RateLimiter
	now     func() time.Time

	mu       sync.Mutex
	signups  map[string]pendingSignup  // by page nonce
	handoffs map[string]pendingHandoff // by handoff code
}

// New opens the data folder and prepares the service.
func New(cfg Config) (*Service, error) {
	if err := cfg.Check(); err != nil {
		return nil, err
	}
	if cfg.GraphBaseURL == "" {
		cfg.GraphBaseURL = "https://graph.facebook.com"
	}
	if cfg.SDKURL == "" {
		cfg.SDKURL = "https://connect.facebook.net/en_US/sdk.js"
	}
	st, err := openStore(cfg.DataDir, cfg.DataKey)
	if err != nil {
		return nil, err
	}
	security.ResetTrustedProxies()
	for _, p := range cfg.TrustedProxies {
		if err := security.AddTrustedProxy(p); err != nil {
			return nil, fmt.Errorf("CONNECT_TRUSTED_PROXIES: %w", err)
		}
	}
	return &Service{cfg: cfg, store: st, http: &http.Client{Timeout: 30 * time.Second}, limiter: security.NewRateLimiter(), hooks: security.NewRateLimiter(),
		now: time.Now, signups: map[string]pendingSignup{}, handoffs: map[string]pendingHandoff{}}, nil
}

// Handler returns the service's HTTP handler.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, map[string]bool{"ok": true}) })
	mux.HandleFunc("GET /signup", s.handleSignupPage)
	mux.HandleFunc("GET /signup.js", s.static("web/signup.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /signup.css", s.static("web/signup.css", "text/css; charset=utf-8"))
	mux.HandleFunc("POST /signup/complete", s.handleComplete)
	mux.HandleFunc("POST /handoff", s.handleHandoff)
	mux.HandleFunc("GET /webhook", s.handleWebhookVerify)
	mux.HandleFunc("POST /webhook", s.handleWebhook)
	mux.HandleFunc("GET /relay/poll", s.handlePoll)
	mux.HandleFunc("POST /relay/ack", s.handleAck)
	mux.HandleFunc("POST /relay/disconnect", s.handleDisconnect)
	return s.middleware(mux)
}

// pageCSP allows the Facebook JavaScript SDK and its login frames, nothing else.
const pageCSP = "default-src 'none'; script-src 'self' https://connect.facebook.net; style-src 'self'; img-src 'self' data: https://*.facebook.com https://*.fbcdn.net; " +
	"connect-src 'self' https://*.facebook.com https://connect.facebook.net; frame-src https://www.facebook.com https://web.facebook.com https://*.facebook.com; " +
	"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func (s *Service) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		if r.URL.Path == "/signup" || r.URL.Path == "/signup.js" || r.URL.Path == "/signup.css" {
			h.Set("Content-Security-Policy", pageCSP)
			// The SDK's login popup reports back through window.opener.
			h.Set("Cross-Origin-Opener-Policy", "same-origin-allow-popups")
		} else {
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
			h.Set("X-Frame-Options", "DENY")
		}
		// Meta's deliveries are not limited here: they all come from a few Meta addresses
		// (or one, behind a local proxy), so counting them would let forged calls, or a
		// busy hour, block real deliveries for every install. handleWebhook limits only
		// the calls it rejects.
		if r.URL.Path != "/webhook" && !s.limiter.AllowAPIRequest(security.GetClientIP(r)) {
			writeError(w, http.StatusTooManyRequests, "Too many requests. Wait a minute and try again.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		next.ServeHTTP(w, r)
	})
}

func (s *Service) static(name, ctype string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := webFS.ReadFile(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", ctype)
		_, _ = w.Write(data)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func randomKey(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the system has no randomness: nothing can be done safely
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

var stateRe = regexp.MustCompile(`^[A-Za-z0-9_-]{32,200}$`)

// ValidReturnURL accepts only Pulse's own finish address on this computer:
// http://127.0.0.1:<port>/cloud/connect/finish or http://localhost:<port>/cloud/connect/finish.
func ValidReturnURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.Path != "/cloud/connect/finish" {
		return false
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil || (host != "127.0.0.1" && host != "localhost") {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n < 65536 && strconv.Itoa(n) == port
}

func (s *Service) handleSignupPage(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state, ret := q.Get("state"), q.Get("return")
	if !stateRe.MatchString(state) || !ValidReturnURL(ret) {
		writeError(w, http.StatusBadRequest, "This sign-up link is not valid. Start again from Pulse: Settings > WhatsApp connection > Connect with Facebook.")
		return
	}
	nonce := randomKey(32)
	now := s.now()
	s.mu.Lock()
	for k, p := range s.signups {
		if now.After(p.expires) {
			delete(s.signups, k)
		}
	}
	if len(s.signups) >= maxPending {
		s.mu.Unlock()
		writeError(w, http.StatusServiceUnavailable, "The service is busy. Try again in a few minutes.")
		return
	}
	s.signups[nonce] = pendingSignup{state: state, ret: ret, expires: now.Add(signupTTL)}
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: nonceName, Value: nonce, Path: "/signup", MaxAge: int(signupTTL.Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = signupTmpl.Execute(w, map[string]string{"AppID": s.cfg.AppID, "ConfigID": s.cfg.ConfigID, "Version": s.cfg.GraphVersion, "State": state, "Nonce": nonce, "SDK": s.cfg.SDKURL})
}

// sameOrigin requires the browser's Origin header to be this service.
func (s *Service) sameOrigin(r *http.Request) bool {
	return r.Header.Get("Origin") != "" && strings.EqualFold(strings.TrimRight(r.Header.Get("Origin"), "/"), s.cfg.PublicURL)
}

var pinRe = regexp.MustCompile(`^\d{6}$`)

func (s *Service) handleComplete(w http.ResponseWriter, r *http.Request) {
	if !s.sameOrigin(r) || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusForbidden, "Cross-site request blocked.")
		return
	}
	var req struct {
		Code          string `json:"code"`
		WABAID        string `json:"waba_id"`
		PhoneNumberID string `json:"phone_number_id"`
		State         string `json:"state"`
		Nonce         string `json:"nonce"`
		PIN           string `json:"pin"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "The request could not be read.")
		return
	}
	c, err := r.Cookie(nonceName)
	if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(req.Nonce)) != 1 {
		writeError(w, http.StatusForbidden, "This page has expired. Start again from Pulse.")
		return
	}
	switch {
	case !digitsRe.MatchString(req.WABAID) || !digitsRe.MatchString(req.PhoneNumberID):
		writeError(w, http.StatusBadRequest, "Facebook did not report a WhatsApp number. Finish every step in the Facebook window and try again.")
		return
	case req.Code == "" || len(req.Code) > 2048:
		writeError(w, http.StatusBadRequest, "Facebook did not confirm the sign-in. Try again.")
		return
	case !pinRe.MatchString(req.PIN):
		writeError(w, http.StatusBadRequest, "Choose a 6-digit PIN for your WhatsApp number.")
		return
	}
	s.mu.Lock()
	p, ok := s.signups[req.Nonce]
	delete(s.signups, req.Nonce) // single use, even when something below fails
	s.mu.Unlock()
	if !ok || s.now().After(p.expires) || subtle.ConstantTimeCompare([]byte(p.state), []byte(req.State)) != 1 {
		writeError(w, http.StatusForbidden, "This page has expired. Start again from Pulse.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	h, err := s.connect(ctx, req.Code, req.WABAID, req.PhoneNumberID, req.PIN)
	if err != nil {
		log.Printf("event=signup_failed waba=%s phone_number_id=%s reason=%q", req.WABAID, req.PhoneNumberID, err.Error())
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	code := randomKey(32)
	s.mu.Lock()
	for k, x := range s.handoffs {
		if s.now().After(x.expires) {
			delete(s.handoffs, k)
		}
	}
	s.handoffs[code] = pendingHandoff{h: h, expires: s.now().Add(handoffTTL)}
	s.mu.Unlock()
	log.Printf("event=signup_completed install=%s waba=%s phone_number_id=%s", h.InstallID, h.WABAID, h.PhoneNumberID)
	writeJSON(w, http.StatusOK, map[string]string{"redirect": p.ret + "?handoff=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(p.state)})
}

// connect finishes the signup with Meta: code exchange, webhook subscription, number
// registration and a check of the number. It then creates the install.
func (s *Service) connect(ctx context.Context, code, waba, phoneID, pin string) (Handoff, error) {
	v := url.Values{"client_id": {s.cfg.AppID}, "client_secret": {s.cfg.AppSecret}, "code": {code}}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := s.graph(ctx, http.MethodGet, "/oauth/access_token?"+v.Encode(), "", nil, &tok); err != nil || tok.AccessToken == "" {
		return Handoff{}, errors.New("Facebook did not accept the sign-in. Start again from Pulse.")
	}
	var ok struct {
		Success bool `json:"success"`
	}
	if err := s.graph(ctx, http.MethodPost, "/"+waba+"/subscribed_apps", tok.AccessToken, nil, &ok); err != nil {
		return Handoff{}, errors.New("The WhatsApp account could not be connected to Pulse's messages service: " + err.Error())
	}
	reg := map[string]string{"messaging_product": "whatsapp", "pin": pin}
	if err := s.graph(ctx, http.MethodPost, "/"+phoneID+"/register", tok.AccessToken, reg, &ok); err != nil && !alreadyRegistered(err) {
		return Handoff{}, errors.New("The phone number could not be registered for WhatsApp: " + err.Error())
	}
	var info struct {
		DisplayPhone string `json:"display_phone_number"`
		VerifiedName string `json:"verified_name"`
	}
	if err := s.graph(ctx, http.MethodGet, "/"+phoneID+"?fields=display_phone_number,verified_name", tok.AccessToken, nil, &info); err != nil {
		return Handoff{}, errors.New("The phone number could not be read: " + err.Error())
	}
	key := randomKey(32)
	in := &Install{ID: "inst-" + randomID(), RelayKeyHash: hashKey(key), WABAID: waba, PhoneNumberID: phoneID, CreatedAt: s.now()}
	if err := s.store.addInstall(in); err != nil {
		return Handoff{}, errors.New("The connection could not be saved. Try again.")
	}
	return Handoff{AccessToken: tok.AccessToken, RelayKey: key, InstallID: in.ID, WABAID: waba, PhoneNumberID: phoneID, DisplayPhone: info.DisplayPhone, VerifiedName: info.VerifiedName}, nil
}

type graphError struct {
	Code    int
	Message string
}

func (e *graphError) Error() string { return e.Message }

func alreadyRegistered(err error) bool {
	var ge *graphError
	return errors.As(err, &ge) && strings.Contains(strings.ToLower(ge.Message), "already")
}

// graph calls the Graph API. The URL (which can hold the app secret) is never logged.
func (s *Service) graph(ctx context.Context, method, path, token string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.cfg.GraphBaseURL, "/")+"/"+s.cfg.GraphVersion+path, rd)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return errors.New("Meta could not be reached")
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error.Message == "" {
			e.Error.Message = fmt.Sprintf("Meta answered HTTP %d", resp.StatusCode)
		}
		return &graphError{Code: e.Error.Code, Message: e.Error.Message}
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// handleHandoff gives a signup's result to the Pulse that started it, once.
func (s *Service) handleHandoff(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Handoff string `json:"handoff"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Handoff == "" {
		writeError(w, http.StatusBadRequest, "Missing handoff code.")
		return
	}
	s.mu.Lock()
	p, ok := s.handoffs[req.Handoff]
	delete(s.handoffs, req.Handoff)
	s.mu.Unlock()
	if !ok || s.now().After(p.expires) {
		writeError(w, http.StatusNotFound, "This connection result has expired or was already collected. Connect with Facebook again.")
		return
	}
	writeJSON(w, http.StatusOK, p.h)
}

func (s *Service) handleWebhookVerify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("hub.mode") != "subscribe" || subtle.ConstantTimeCompare([]byte(q.Get("hub.verify_token")), []byte(s.cfg.VerifyToken)) != 1 {
		writeError(w, http.StatusForbidden, "Wrong verify token.")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, q.Get("hub.challenge"))
}

func validSignature(secret string, body []byte, header string) bool {
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if secret == "" || !strings.HasPrefix(header, "sha256=") || err != nil {
		return false
	}
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return hmac.Equal(got, m.Sum(nil))
}

// handleWebhook receives Meta's deliveries for every connected install and queues each
// change for the install that owns its phone number ID. Unknown numbers are dropped.
func (s *Service) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "Body too large.")
		return
	}
	if !validSignature(s.cfg.AppSecret, body, r.Header.Get("X-Hub-Signature-256")) {
		ip := security.GetClientIP(r)
		if !s.hooks.AllowAPIRequest(ip) { // only rejected calls count
			writeError(w, http.StatusTooManyRequests, "Too many requests.")
			return
		}
		log.Printf("event=webhook_rejected ip=%s", ip)
		writeError(w, http.StatusUnauthorized, "Missing or wrong signature.")
		return
	}
	var b struct {
		Object string `json:"object"`
		Entry  []struct {
			ID      string            `json:"id"`
			Changes []json.RawMessage `json:"changes"`
		} `json:"entry"`
	}
	if err := json.Unmarshal(body, &b); err != nil {
		w.WriteHeader(http.StatusOK) // signed by Meta but unreadable: nothing to retry
		return
	}
	queued, dropped := 0, 0
	for _, e := range b.Entry {
		for _, ch := range e.Changes {
			var c struct {
				Value struct {
					Metadata struct {
						PhoneNumberID string `json:"phone_number_id"`
					} `json:"metadata"`
				} `json:"value"`
			}
			_ = json.Unmarshal(ch, &c)
			id, ok := s.store.byPhone(c.Value.Metadata.PhoneNumberID)
			if !ok {
				dropped++
				continue
			}
			one, _ := json.Marshal(map[string]any{"object": b.Object, "entry": []map[string]any{{"id": e.ID, "changes": []json.RawMessage{ch}}}})
			if err := s.store.enqueue(id, one); err != nil {
				log.Printf("event=queue_write_failed install=%s err=%q", id, err.Error())
				writeError(w, http.StatusInternalServerError, "Could not store the delivery.")
				return // Meta delivers it again later
			}
			queued++
		}
	}
	log.Printf("event=webhook queued=%d dropped=%d", queued, dropped)
	w.WriteHeader(http.StatusOK)
}

// relayInstall authenticates Pulse by its relay key.
func (s *Service) relayInstall(w http.ResponseWriter, r *http.Request) (Install, bool) {
	h := r.Header.Get("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
		writeError(w, http.StatusUnauthorized, "Missing relay key.")
		return Install{}, false
	}
	in, ok := s.store.byKey(strings.TrimSpace(h[7:]))
	if !ok {
		writeError(w, http.StatusUnauthorized, "This Pulse is not connected any more. Connect with Facebook again.")
		return Install{}, false
	}
	return in, true
}

type pollItem struct {
	ID   string          `json:"id"`
	Body json.RawMessage `json:"body"`
}

func (s *Service) handlePoll(w http.ResponseWriter, r *http.Request) {
	in, ok := s.relayInstall(w, r)
	if !ok {
		return
	}
	wait := pollWait
	if v := r.URL.Query().Get("wait"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && time.Duration(n)*time.Second < pollWait {
			wait = time.Duration(n) * time.Second
		}
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		items, more := s.store.take(in.ID, pollMax)
		if len(items) > 0 || wait == 0 {
			out := make([]pollItem, 0, len(items))
			for _, it := range items {
				out = append(out, pollItem{ID: it.ID, Body: it.Body})
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": out})
			return
		}
		select {
		case <-more:
			if _, still := s.store.byKey(strings.TrimSpace(r.Header.Get("Authorization")[7:])); !still {
				writeError(w, http.StatusUnauthorized, "This Pulse is not connected any more. Connect with Facebook again.")
				return
			}
		case <-deadline.C:
			writeJSON(w, http.StatusOK, map[string]any{"items": []pollItem{}})
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (s *Service) handleAck(w http.ResponseWriter, r *http.Request) {
	in, ok := s.relayInstall(w, r)
	if !ok {
		return
	}
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.IDs) > 1000 {
		writeError(w, http.StatusBadRequest, "Send the delivered item IDs.")
		return
	}
	n, err := s.store.ack(in.ID, req.IDs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not update the queue.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

func (s *Service) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	in, ok := s.relayInstall(w, r)
	if !ok {
		return
	}
	if err := s.store.removeInstall(in.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not remove the connection.")
		return
	}
	log.Printf("event=install_removed install=%s", in.ID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
