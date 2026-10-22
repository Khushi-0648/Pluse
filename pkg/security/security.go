package security

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SessionManager manages authenticated web sessions, their roles and CSRF tokens.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*session
	duration time.Duration
	stop     chan struct{}
}

type session struct {
	expires time.Time
	csrf    string
	role    string
}

// NewSessionManager creates a session manager with the given TTL.
func NewSessionManager(duration time.Duration) *SessionManager {
	if duration <= 0 {
		duration = 7 * 24 * time.Hour
	}
	sm := &SessionManager{sessions: make(map[string]*session), duration: duration, stop: make(chan struct{})}
	go func() {
		t := time.NewTicker(30 * time.Minute)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				sm.cleanup()
			case <-sm.stop:
				return
			}
		}
	}()
	return sm
}

// Close stops the background cleanup goroutine.
func (sm *SessionManager) Close() {
	select {
	case <-sm.stop:
	default:
		close(sm.stop)
	}
}

// RandomToken returns n random bytes hex-encoded.
func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateSessionWithRole creates a session for role and returns the session and CSRF tokens.
func (sm *SessionManager) CreateSessionWithRole(role string) (string, string) {
	if role == "" {
		role = "operator"
	}
	tok, csrf := RandomToken(32), RandomToken(32)
	sm.mu.Lock()
	sm.sessions[tok] = &session{expires: time.Now().Add(sm.duration), csrf: csrf, role: role}
	sm.mu.Unlock()
	return tok, csrf
}

// GetSessionRole returns the role of a valid session, or "" when the session is unknown.
func (sm *SessionManager) GetSessionRole(token string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if s, ok := sm.sessions[token]; ok && time.Now().Before(s.expires) {
		return s.role
	}
	return ""
}

// GetCSRFToken returns the CSRF token bound to a session.
func (sm *SessionManager) GetCSRFToken(token string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if s, ok := sm.sessions[token]; ok {
		return s.csrf
	}
	return ""
}

// ValidateSession reports whether token is a live session.
func (sm *SessionManager) ValidateSession(token string) bool {
	if token == "" {
		return false
	}
	sm.mu.RLock()
	s, ok := sm.sessions[token]
	sm.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(s.expires) {
		sm.DestroySession(token)
		return false
	}
	return true
}

// DestroySession invalidates a session.
func (sm *SessionManager) DestroySession(token string) {
	sm.mu.Lock()
	delete(sm.sessions, token)
	sm.mu.Unlock()
}

// DestroyAll invalidates every session (used after a password change).
func (sm *SessionManager) DestroyAll(except string) {
	sm.mu.Lock()
	for t := range sm.sessions {
		if t != except {
			delete(sm.sessions, t)
		}
	}
	sm.mu.Unlock()
}

// DestroyRole invalidates every session of one role (used when the staff password changes).
func (sm *SessionManager) DestroyRole(role string) {
	sm.mu.Lock()
	for t, s := range sm.sessions {
		if s.role == role {
			delete(sm.sessions, t)
		}
	}
	sm.mu.Unlock()
}

func (sm *SessionManager) cleanup() {
	now := time.Now()
	sm.mu.Lock()
	for t, s := range sm.sessions {
		if now.After(s.expires) {
			delete(sm.sessions, t)
		}
	}
	sm.mu.Unlock()
}

// RateLimiter protects endpoints from brute force and request floods.
type RateLimiter struct {
	mu           sync.Mutex
	loginFails   map[string][]time.Time
	apiRequests  map[string][]time.Time
	maxFails     int
	lockDuration time.Duration
	apiPerMinute int
}

// NewRateLimiter creates an IP-based rate limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		loginFails:   make(map[string][]time.Time),
		apiRequests:  make(map[string][]time.Time),
		maxFails:     5,
		lockDuration: 5 * time.Minute,
		apiPerMinute: 300,
	}
}

// RecordFailedLogin records a failed attempt for an IP.
func (rl *RateLimiter) RecordFailedLogin(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k := normalizeIP(ip)
	rl.loginFails[k] = append(rl.loginFails[k], time.Now())
}

// ResetLoginFails clears failed attempts after a successful login.
func (rl *RateLimiter) ResetLoginFails(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.loginFails, normalizeIP(ip))
}

// IsLoginLocked reports whether ip exceeded the allowed failed attempts.
func (rl *RateLimiter) IsLoginLocked(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k := normalizeIP(ip)
	cutoff := time.Now().Add(-rl.lockDuration)
	recent := rl.loginFails[k][:0]
	for _, t := range rl.loginFails[k] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(rl.loginFails, k)
		return false
	}
	rl.loginFails[k] = recent
	return len(recent) >= rl.maxFails
}

// BeginLoginAttempt checks the lock and counts the attempt as failed in one step, so
// parallel guesses cannot all slip past the check. Call ResetLoginFails on success.
func (rl *RateLimiter) BeginLoginAttempt(ip string) bool {
	if rl.IsLoginLocked(ip) {
		return false
	}
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k := normalizeIP(ip)
	cutoff := time.Now().Add(-rl.lockDuration)
	n := 0
	for _, t := range rl.loginFails[k] {
		if t.After(cutoff) {
			n++
		}
	}
	if n >= rl.maxFails {
		return false
	}
	rl.loginFails[k] = append(rl.loginFails[k], time.Now())
	return true
}

// AllowAPIRequest enforces a sliding per-minute request limit.
func (rl *RateLimiter) AllowAPIRequest(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k := normalizeIP(ip)
	cutoff := time.Now().Add(-time.Minute)
	if len(rl.apiRequests) > 1000 {
		for ip, ts := range rl.apiRequests {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(rl.apiRequests, ip)
			}
		}
	}
	recent := rl.apiRequests[k][:0]
	for _, t := range rl.apiRequests[k] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= rl.apiPerMinute {
		rl.apiRequests[k] = recent
		return false
	}
	rl.apiRequests[k] = append(recent, time.Now())
	return true
}

func normalizeIP(remoteAddr string) string {
	if ip, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return ip
	}
	return remoteAddr
}

var (
	proxyMu        sync.RWMutex
	trustedProxies []*net.IPNet
)

// AddTrustedProxy marks a CIDR as a reverse proxy whose forwarding headers may be trusted.
func AddTrustedProxy(cidr string) error {
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	proxyMu.Lock()
	trustedProxies = append(trustedProxies, n)
	proxyMu.Unlock()
	return nil
}

// ResetTrustedProxies clears the trusted proxy list.
func ResetTrustedProxies() {
	proxyMu.Lock()
	trustedProxies = nil
	proxyMu.Unlock()
}

func isTrustedProxy(ip net.IP) bool {
	proxyMu.RLock()
	defer proxyMu.RUnlock()
	for _, n := range trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// GetClientIP returns the peer IP. Forwarding headers are honoured only from trusted proxies.
func GetClientIP(r *http.Request) string {
	host := normalizeIP(r.RemoteAddr)
	ip := net.ParseIP(host)
	if ip != nil && isTrustedProxy(ip) {
		for _, h := range []string{"CF-Connecting-IP", "X-Real-IP"} {
			if p := net.ParseIP(strings.TrimSpace(r.Header.Get(h))); p != nil {
				return p.String()
			}
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if p := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); p != nil {
				return p.String()
			}
		}
	}
	if ip != nil {
		return ip.String()
	}
	return host
}

// IsLocalRequest reports whether the TCP peer is the local machine. A request that
// passed through a proxy (forwarding headers present) is not local, even when the
// proxy itself runs on this computer.
func IsLocalRequest(r *http.Request) bool {
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Real-IP"} {
		if r.Header.Get(h) != "" {
			return false
		}
	}
	ip := net.ParseIP(normalizeIP(r.RemoteAddr))
	return ip != nil && ip.IsLoopback()
}

// ValidateHostHeader accepts loopback host names and any extra host names configured
// for the server (DNS-rebinding protection).
func ValidateHostHeader(host string, extra ...string) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	name, _, err := net.SplitHostPort(h)
	if err != nil {
		name = h
	}
	name = strings.ToLower(strings.Trim(name, "[]"))
	if name == "localhost" || name == "127.0.0.1" || name == "::1" {
		return true
	}
	for _, e := range extra {
		e = strings.ToLower(strings.Trim(strings.TrimSpace(e), "[]"))
		if e != "" && e != "0.0.0.0" && e != "::" && e == name {
			return true
		}
	}
	return false
}

// IsAllowedOrigin checks an Origin or Referer header against the request host.
func IsAllowedOrigin(origin, requestHost string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	oh, op := strings.ToLower(u.Hostname()), u.Port()
	if op == "" {
		op = map[string]string{"https": "443"}[u.Scheme]
		if op == "" {
			op = "80"
		}
	}
	rh, rp, err := net.SplitHostPort(requestHost)
	if err != nil {
		rh, rp = requestHost, "80"
	}
	rh = strings.ToLower(strings.Trim(rh, "[]"))
	// Another program on a different local port is a different website (S6), even
	// though browsers send it the same cookies.
	if op != rp && rp != "443" && op != "443" {
		return false
	}
	if oh == rh {
		return true
	}
	loop := func(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" }
	return loop(oh) && loop(rh)
}

// ContentSecurityPolicy is applied to every response. No inline script or style is allowed.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// ApplySecurityHeaders injects browser security headers.
func ApplySecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", ContentSecurityPolicy)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
}

var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8", "64:ff9b::/96",
	} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// IsDisallowedIP reports whether ip is loopback, private, link-local, unspecified,
// multicast or otherwise not a public unicast destination.
func IsDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, n := range blockedNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateWebhookURL rejects URLs that point at the local machine or private networks.
// The check is repeated at connect time by SafeHTTPClient to defeat DNS rebinding.
func ValidateWebhookURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("URL must start with http:// or https://")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return errors.New("URL has no host")
	}
	if u.User != nil {
		return errors.New("URL must not contain a username or password")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") || host == "metadata.google.internal" {
		return errors.New("local and internal host names are not allowed")
	}
	if ip := net.ParseIP(host); ip != nil {
		if IsDisallowedIP(ip) {
			return errors.New("the address points to this computer or a private network")
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("could not resolve %s: %w", host, err)
	}
	for _, ip := range ips {
		if IsDisallowedIP(ip) {
			return errors.New("the address resolves to this computer or a private network")
		}
	}
	return nil
}

// SafeHTTPClient returns an HTTP client whose dialer refuses non-public destinations,
// including after redirects and DNS changes.
func SafeHTTPClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: timeout}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if IsDisallowedIP(ip.IP) {
					return nil, fmt.Errorf("blocked connection to non-public address %s", ip.IP)
				}
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("no addresses for %s", host)
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       60 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("too many redirects")
			}
			return nil
		},
	}
}

// SignWebhookHMAC returns the hex HMAC-SHA256 of payload.
func SignWebhookHMAC(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhookHMAC verifies a "sha256=<hex>" or bare hex signature.
func VerifyWebhookHMAC(secret string, payload []byte, provided string) bool {
	if secret == "" || provided == "" {
		return false
	}
	provided = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(provided), "sha256="))
	return subtle.ConstantTimeCompare([]byte(provided), []byte(SignWebhookHMAC(secret, payload))) == 1
}

// ConstantTimeEqual compares two secrets without leaking timing.
func ConstantTimeEqual(a, b string) bool {
	return a != "" && b != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// ValidatePassword is the single password policy used by the server and mirrored by the login pages.
func ValidatePassword(password string) error {
	p := strings.TrimSpace(password)
	if len(p) < 8 {
		return errors.New("Password must be at least 8 characters long.")
	}
	if len(p) > 128 {
		return errors.New("Password cannot be longer than 128 characters.")
	}
	var hasLetter, hasDigit bool
	for _, r := range p {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			hasLetter = true
		case r >= '0' && r <= '9':
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return errors.New("Password must contain both letters and numbers.")
	}
	lower := strings.ToLower(p)
	for _, banned := range []string{"admin", "password", "passw0rd", "root", "qwerty", "letmein", "welcome", "changeme", "whatsapp", "pulse"} {
		if strings.Contains(lower, banned) {
			return fmt.Errorf("Password cannot contain %q.", banned)
		}
	}
	for _, seq := range []string{"12345678", "87654321", "abcdefgh"} {
		if strings.Contains(lower, seq) {
			return errors.New("Password cannot contain an easy sequence such as 12345678.")
		}
	}
	distinct := map[rune]bool{}
	for _, r := range lower {
		distinct[r] = true
	}
	if len(distinct) < 4 {
		return errors.New("Password must use at least 4 different characters.")
	}
	if Guessable(lower) {
		return errors.New("This password is too easy to guess (like abcd1234 or hello2024). Add an unusual word or a few more characters.")
	}
	return nil
}

var keyboardRows = []string{"1234567890", "qwertyuiop", "asdfghjkl", "zxcvbnm"}

// commonWords are words people often pair with a few digits. Mirrored in auth.js.
var commonWords = map[string]bool{
	"test": true, "hello": true, "india": true, "love": true, "iloveyou": true, "shop": true, "store": true, "user": true,
	"guest": true, "login": true, "secret": true, "summer": true, "monkey": true, "dragon": true, "sunshine": true,
	"owner": true, "business": true, "office": true, "mobile": true, "abc": true, "abcd": true,
}

// patterned reports whether a and b are neighbours in an easy pattern: the same
// character, the next or previous letter or digit, or the next key on the keyboard.
func patterned(a, b rune) bool {
	if a == b || b == a+1 || b == a-1 {
		return true
	}
	for _, row := range keyboardRows {
		i, j := strings.IndexRune(row, a), strings.IndexRune(row, b)
		if i >= 0 && j >= 0 && (j == i+1 || j == i-1) {
			return true
		}
	}
	return false
}

// Guessable catches passwords built only from easy pieces (abcd1234, qwer1234,
// aaaa1111) and a common word plus a number (hello2024, india123). lower must be lowercase.
func Guessable(lower string) bool {
	rs := []rune(lower)
	// 1. Made entirely of runs of 3+ patterned characters.
	allRuns, start := true, 0
	for i := 1; i <= len(rs); i++ {
		if i < len(rs) && patterned(rs[i-1], rs[i]) {
			continue
		}
		if i-start < 3 {
			allRuns = false
			break
		}
		start = i
	}
	if allRuns {
		return true
	}
	// 2. A common word with only digits (and symbols) around it.
	var letters, digits strings.Builder
	for _, r := range rs {
		switch {
		case r >= 'a' && r <= 'z':
			letters.WriteRune(r)
		case r >= '0' && r <= '9':
			digits.WriteRune(r)
		}
	}
	if !commonWords[letters.String()] {
		return false
	}
	d := []rune(digits.String())
	if len(d) <= 4 {
		return true // word + short number or year
	}
	for i := 1; i < len(d); i++ {
		if !patterned(d[i-1], d[i]) {
			return false
		}
	}
	return true
}
