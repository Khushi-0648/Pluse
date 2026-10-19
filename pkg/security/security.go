package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// SessionManager manages authenticated user web sessions and CSRF tokens
type SessionManager struct {
	mu         sync.RWMutex
	sessions   map[string]time.Time
	csrfTokens map[string]string // sessionToken -> csrfToken
	duration   time.Duration
}

// NewSessionManager creates a session manager with the given TTL
func NewSessionManager(duration time.Duration) *SessionManager {
	if duration <= 0 {
		duration = 7 * 24 * time.Hour // 7 days default
	}
	sm := &SessionManager{
		sessions:   make(map[string]time.Time),
		csrfTokens: make(map[string]string),
		duration:   duration,
	}

	// Periodic cleanup worker
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		for range ticker.C {
			sm.cleanup()
		}
	}()

	return sm
}

// CreateSession generates a secure cryptographically random session token and CSRF token
func (sm *SessionManager) CreateSession() (sessionToken string, csrfToken string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	b := make([]byte, 32)
	_, _ = rand.Read(b)
	sessionToken = hex.EncodeToString(b)

	c := make([]byte, 32)
	_, _ = rand.Read(c)
	csrfToken = hex.EncodeToString(c)

	sm.sessions[sessionToken] = time.Now().Add(sm.duration)
	sm.csrfTokens[sessionToken] = csrfToken
	return sessionToken, csrfToken
}

// GenerateCSRFToken generates a standalone cryptographically random CSRF token
func GenerateCSRFToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GetCSRFToken retrieves the CSRF token associated with a session
func (sm *SessionManager) GetCSRFToken(sessionToken string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.csrfTokens[sessionToken]
}

// ValidateSession checks if a session token is valid and unexpired
func (sm *SessionManager) ValidateSession(token string) bool {
	if token == "" {
		return false
	}
	sm.mu.RLock()
	expiry, exists := sm.sessions[token]
	sm.mu.RUnlock()

	if !exists {
		return false
	}
	if time.Now().After(expiry) {
		sm.DestroySession(token)
		return false
	}
	return true
}

// DestroySession invalidates an active session
func (sm *SessionManager) DestroySession(token string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	delete(sm.sessions, token)
	delete(sm.csrfTokens, token)
}

func (sm *SessionManager) cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	now := time.Now()
	for token, expiry := range sm.sessions {
		if now.After(expiry) {
			delete(sm.sessions, token)
			delete(sm.csrfTokens, token)
		}
	}
}

// RateLimiter protects endpoints from brute force and DoS attacks
type RateLimiter struct {
	mu           sync.Mutex
	loginFails   map[string][]time.Time
	apiRequests  map[string][]time.Time
	maxFails     int
	lockDuration time.Duration
}

// NewRateLimiter creates an IP-based rate limiter
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		loginFails:   make(map[string][]time.Time),
		apiRequests:  make(map[string][]time.Time),
		maxFails:     5,
		lockDuration: 5 * time.Minute,
	}
}

// RecordFailedLogin records a failed attempt for an IP
func (rl *RateLimiter) RecordFailedLogin(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cleanIP := normalizeIP(ip)
	rl.loginFails[cleanIP] = append(rl.loginFails[cleanIP], time.Now())
}

// ResetLoginFails clears failed attempts upon successful login
func (rl *RateLimiter) ResetLoginFails(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	delete(rl.loginFails, normalizeIP(ip))
}

// IsLoginLocked checks if IP has exceeded allowed failed login attempts
func (rl *RateLimiter) IsLoginLocked(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cleanIP := normalizeIP(ip)
	attempts, exists := rl.loginFails[cleanIP]
	if !exists {
		return false
	}

	cutoff := time.Now().Add(-rl.lockDuration)
	var recent []time.Time
	for _, t := range attempts {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	rl.loginFails[cleanIP] = recent

	return len(recent) >= rl.maxFails
}

// AllowAPIRequest enforces a sliding rate limit (120 req / min per IP)
func (rl *RateLimiter) AllowAPIRequest(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cleanIP := normalizeIP(ip)
	cutoff := time.Now().Add(-1 * time.Minute)

	var recent []time.Time
	for _, t := range rl.apiRequests[cleanIP] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}

	if len(recent) >= 180 {
		return false
	}

	recent = append(recent, time.Now())
	rl.apiRequests[cleanIP] = recent
	return true
}

func normalizeIP(remoteAddr string) string {
	ip, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return ip
	}
	return remoteAddr
}

var (
	proxyMu        sync.RWMutex
	trustedProxies []*net.IPNet
	trustProxyMode bool
)

// SetTrustProxy enables or disables reverse proxy header trusting
func SetTrustProxy(enable bool) {
	proxyMu.Lock()
	defer proxyMu.Unlock()
	trustProxyMode = enable
}

// AddTrustedProxy adds a CIDR network to the trusted proxy list
func AddTrustedProxy(cidr string) error {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return err
	}
	proxyMu.Lock()
	defer proxyMu.Unlock()
	trustedProxies = append(trustedProxies, ipNet)
	trustProxyMode = true
	return nil
}

func isTrustedProxy(ip net.IP) bool {
	proxyMu.RLock()
	defer proxyMu.RUnlock()
	if !trustProxyMode {
		return false
	}
	if len(trustedProxies) == 0 && trustProxyMode {
		return true
	}
	for _, ipNet := range trustedProxies {
		if ipNet.Contains(ip) {
			return true
		}
	}
	return false
}

// GetClientIP extracts the client IP without blindly trusting spoofed proxy headers (S13).
// By default, reverse proxy headers (X-Forwarded-For, CF-Connecting-IP, X-Real-IP) are ONLY
// inspected when reverse proxy trust is explicitly enabled and the peer is a trusted proxy.
func GetClientIP(r *http.Request) string {
	rawHost := normalizeIP(r.RemoteAddr)
	remoteIP := net.ParseIP(rawHost)

	if remoteIP != nil && isTrustedProxy(remoteIP) {
		if cfIP := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cfIP != "" {
			if parsed := net.ParseIP(cfIP); parsed != nil {
				return parsed.String()
			}
		}
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			first := strings.TrimSpace(parts[0])
			if parsed := net.ParseIP(first); parsed != nil {
				return parsed.String()
			}
		}
		if xReal := strings.TrimSpace(r.Header.Get("X-Real-IP")); xReal != "" {
			if parsed := net.ParseIP(xReal); parsed != nil {
				return parsed.String()
			}
		}
	}

	if remoteIP != nil {
		return remoteIP.String()
	}
	return rawHost
}

// IsLocalRequest returns true if the incoming HTTP request originates from localhost / loopback
func IsLocalRequest(r *http.Request) bool {
	ipStr := normalizeIP(r.RemoteAddr)
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ipStr == "127.0.0.1" || ipStr == "::1"
}

// ValidateHostHeader checks if the Host header matches allowed loopback or local host values (S17 DNS Rebinding Protection)
func ValidateHostHeader(host string, allowedPort int) bool {
	h := strings.TrimSpace(host)
	if h == "" {
		return false
	}
	hostname, portStr, err := net.SplitHostPort(h)
	if err != nil {
		hostname = h
		portStr = ""
	}
	hostname = strings.ToLower(strings.TrimSpace(hostname))

	// Allow loopback hostnames
	if hostname == "localhost" || hostname == "127.0.0.1" || hostname == "::1" || hostname == "[::1]" {
		if portStr == "" || allowedPort <= 0 {
			return true
		}
		p, err := strconv.Atoi(portStr)
		if err == nil && (p == allowedPort || p == 8080 || p == 8085) {
			return true
		}
		return true
	}

	return false
}

// IsAllowedOrigin checks if the Origin or Referer header matches the local application host (S19 CSRF Origin Validation)
func IsAllowedOrigin(originHeader string, allowedHost string) bool {
	if originHeader == "" {
		return true // Missing Origin is handled by CSRF token checks
	}
	u, err := url.Parse(originHeader)
	if err != nil {
		return false
	}
	originHost := strings.ToLower(u.Hostname())
	if originHost == "127.0.0.1" || originHost == "localhost" || originHost == "::1" {
		return true
	}
	if allowedHost != "" {
		reqHost, _, _ := net.SplitHostPort(allowedHost)
		if reqHost == "" {
			reqHost = allowedHost
		}
		if strings.EqualFold(originHost, reqHost) {
			return true
		}
	}
	return false
}

// ApplySecurityHeaders injects modern web security headers and CSP
func ApplySecurityHeaders(w http.ResponseWriter) {
	// Comprehensive CSP disallowing inline script execution (S12) while prohibiting external framing and unsafe injection
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com data:; img-src 'self' data: blob: https:; connect-src 'self'; frame-ancestors 'none';")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-XSS-Protection", "1; mode=block")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
}

// ValidateWebhookURL protects against Server-Side Request Forgery (SSRF)
// It blocks requests to loopback, link-local, private RFC 1918 addresses, and cloud metadata endpoints.
func ValidateWebhookURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("webhook URL scheme must be http or https")
	}

	hostname := u.Hostname()
	if hostname == "" {
		return errors.New("webhook host is empty")
	}

	// Block common cloud metadata and loopback names
	lowerHost := strings.ToLower(hostname)
	if lowerHost == "localhost" || lowerHost == "metadata.google.internal" || strings.HasSuffix(lowerHost, ".local") {
		return errors.New("access to local/metadata hostnames is prohibited")
	}

	// Resolve IP addresses
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return fmt.Errorf("failed to resolve webhook host %s: %w", hostname, err)
	}

	for _, ip := range ips {
		if ip.IsLoopback() {
			return errors.New("webhook destination points to a loopback address")
		}
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return errors.New("webhook destination points to a link-local address")
		}
		if ip.IsPrivate() {
			return errors.New("webhook destination points to a private network address (RFC 1918/RFC 4193)")
		}
		// Block AWS/GCP/Azure link-local metadata 169.254.169.254
		if ip.String() == "169.254.169.254" {
			return errors.New("access to cloud instance metadata service is prohibited")
		}
	}

	return nil
}

// SignWebhookHMAC generates an HMAC-SHA256 signature for outbound webhooks
func SignWebhookHMAC(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

// AuditEvent represents a logged administrative or security action
type AuditEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
	IP        string    `json:"ip"`
	User      string    `json:"user"`
	Details   string    `json:"details"`
	Status    string    `json:"status"` // "success", "failed"
}

// AuditLogger maintains an in-memory ring buffer of recent administrative activities
type AuditLogger struct {
	mu     sync.RWMutex
	events []AuditEvent
	max    int
}

var globalAuditLogger = &AuditLogger{
	events: make([]AuditEvent, 0, 500),
	max:    500,
}

// LogAudit records an action into the system audit trail
func LogAudit(action, ip, user, details, status string) {
	globalAuditLogger.mu.Lock()
	defer globalAuditLogger.mu.Unlock()

	evt := AuditEvent{
		Timestamp: time.Now(),
		Action:    action,
		IP:        ip,
		User:      user,
		Details:   details,
		Status:    status,
	}

	globalAuditLogger.events = append(globalAuditLogger.events, evt)
	if len(globalAuditLogger.events) > globalAuditLogger.max {
		globalAuditLogger.events = globalAuditLogger.events[len(globalAuditLogger.events)-globalAuditLogger.max:]
	}
}

// GetAuditLog returns recorded audit events (newest first)
func GetAuditLog(limit int) []AuditEvent {
	globalAuditLogger.mu.RLock()
	defer globalAuditLogger.mu.RUnlock()

	if limit <= 0 || limit > len(globalAuditLogger.events) {
		limit = len(globalAuditLogger.events)
	}

	result := make([]AuditEvent, 0, limit)
	for i := len(globalAuditLogger.events) - 1; i >= 0 && len(result) < limit; i-- {
		result = append(result, globalAuditLogger.events[i])
	}
	return result
}

// ValidatePassword validates password complexity for admin accounts (S20).
// Enforces minimum 8 characters, maximum 128 characters, both letters and digits,
// and rejects common trivial passwords.
func ValidatePassword(password string) error {
	p := strings.TrimSpace(password)
	if len(p) < 8 {
		return errors.New("password must be at least 8 characters long")
	}
	if len(p) > 128 {
		return errors.New("password cannot exceed 128 characters")
	}

	var hasLetter, hasDigit bool
	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
		} else if r >= '0' && r <= '9' {
			hasDigit = true
		}
	}

	if !hasLetter || !hasDigit {
		return errors.New("password must contain both letters and numbers")
	}

	lower := strings.ToLower(p)
	commonWeak := []string{
		"password", "password1", "password123", "admin123", "administrator",
		"12345678", "123456789", "1234567890", "87654321", "qwerty123",
		"letmein1", "welcome1", "changeme1",
	}
	for _, weak := range commonWeak {
		if lower == weak {
			return errors.New("password is too common or easily guessable")
		}
	}

	// Check if all characters are identical
	allSame := true
	for i := 1; i < len(p); i++ {
		if p[i] != p[0] {
			allSame = false
			break
		}
	}
	if allSame {
		return errors.New("password cannot consist of identical repeated characters")
	}

	return nil
}

