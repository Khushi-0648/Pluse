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
	idle    time.Duration // 0: no idle limit; otherwise the session ends after this long unused
	seen    time.Time     // last use, for the idle limit
}

// live reports whether s is still valid at now.
func (s *session) live(now time.Time) bool {
	return now.Before(s.expires) && (s.idle <= 0 || now.Sub(s.seen) <= s.idle)
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
	return sm.CreateSessionWithIdle(role, 0)
}

// CreateSessionWithIdle is CreateSessionWithRole for a session that also ends after idle
// without use (sessions from outside this computer, S9). idle <= 0 means no idle limit.
func (sm *SessionManager) CreateSessionWithIdle(role string, idle time.Duration) (string, string) {
	if role == "" {
		role = "operator"
	}
	tok, csrf := RandomToken(32), RandomToken(32)
	now := time.Now()
	sm.mu.Lock()
	sm.sessions[tok] = &session{expires: now.Add(sm.duration), csrf: csrf, role: role, idle: idle, seen: now}
	sm.mu.Unlock()
	return tok, csrf
}

// GetSessionRole returns the role of a valid session, or "" when the session is unknown.
func (sm *SessionManager) GetSessionRole(token string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	if s, ok := sm.sessions[token]; ok && s.live(time.Now()) {
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

// ValidateSession reports whether token is a live session, and counts it as used.
func (sm *SessionManager) ValidateSession(token string) bool {
	if token == "" {
		return false
	}
	now := time.Now()
	sm.mu.Lock()
	defer sm.mu.Unlock()
	s, ok := sm.sessions[token]
	if !ok {
		return false
	}
	if !s.live(now) {
		delete(sm.sessions, token)
		return false
	}
	s.seen = now
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
		if !s.live(now) {
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
	apiPruned    time.Time // when apiRequests was last cleared of idle callers

	// The budget across all callers (S2): a caller with many addresses (an IPv6 range, or
	// one that sets X-Forwarded-For through a tunnel) must not get 5 guesses per address.
	globalFails    []loginFail
	globalMax      int           // failures allowed within globalWindow before sign-in pauses
	globalWindow   time.Duration // the window globalMax is counted in
	globalBase     time.Duration // the first pause; it doubles each time up to globalMaxPause
	globalMaxPause time.Duration
	globalUntil    time.Time // sign-in (except from this computer) is paused until then
	globalLevel    int       // pauses in a row; reset after a day without one
	globalLast     time.Time // when the last pause began

	// known are callers (address, or IPv6 /64) that gave a correct password recently. A
	// pause triggered by other callers' guesses does not apply to them, so an attacker
	// cannot keep the owner's phone signed out by guessing every few hours (N1). Their
	// own 5-failure lockout still applies.
	known map[string]time.Time
}

const (
	knownFor = 30 * 24 * time.Hour
	knownMax = 256
)

type loginFail struct {
	at  time.Time
	key string
}

// NewRateLimiter creates an IP-based rate limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		loginFails:     make(map[string][]time.Time),
		apiRequests:    make(map[string][]time.Time),
		maxFails:       5,
		lockDuration:   5 * time.Minute,
		apiPerMinute:   300,
		globalMax:      20,
		globalWindow:   15 * time.Minute,
		globalBase:     15 * time.Minute,
		globalMaxPause: 4 * time.Hour,
	}
}

// loginKey is the lockout bucket for a caller: one IPv4 address, or one IPv6 /64 (S2).
// A single home, office or phone network gets a whole /64, so changing addresses inside
// it must not give a caller fresh guesses.
func loginKey(addr string) string {
	h := normalizeIP(addr)
	ip := net.ParseIP(strings.Trim(h, "[]"))
	if ip == nil {
		return h
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

// pruneLoginFailsLocked drops callers whose failures are all older than cutoff (S8).
// rl.mu must be held.
func (rl *RateLimiter) pruneLoginFailsLocked(cutoff time.Time) {
	if len(rl.loginFails) <= 1000 {
		return
	}
	for k, ts := range rl.loginFails {
		if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
			delete(rl.loginFails, k)
		}
	}
}

// RecordFailedLogin records a failed attempt for an IP.
func (rl *RateLimiter) RecordFailedLogin(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k, now := loginKey(ip), time.Now()
	rl.pruneLoginFailsLocked(now.Add(-rl.lockDuration))
	rl.loginFails[k] = append(rl.loginFails[k], now)
	rl.globalFails = append(rl.globalFails, loginFail{at: now, key: k})
}

// ResetLoginFails clears failed attempts after a successful login. Of the budget across
// all callers it takes back only the attempt that just succeeded: earlier failures stay
// counted, so knowing the staff password does not buy extra guesses at the admin's.
func (rl *RateLimiter) ResetLoginFails(ip string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k := loginKey(ip)
	delete(rl.loginFails, k)
	rl.rememberLocked(k, time.Now())
	for i := len(rl.globalFails) - 1; i >= 0; i-- {
		if rl.globalFails[i].key == k {
			rl.globalFails = append(rl.globalFails[:i], rl.globalFails[i+1:]...)
			break
		}
	}
}

// LoginPausedForAll reports whether sign-in is paused for every caller (except this
// computer) because too many wrong passwords were tried in total.
func (rl *RateLimiter) LoginPausedForAll() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return time.Now().Before(rl.globalUntil)
}

// IsLoginLocked reports whether ip exceeded the allowed failed attempts.
func (rl *RateLimiter) IsLoginLocked(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k := loginKey(ip)
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
// Besides the caller's own lockout, more than globalMax failures from all callers
// together within globalWindow pause every sign-in and password check (S2).
func (rl *RateLimiter) BeginLoginAttempt(ip string) bool {
	return rl.beginAttempt(ip, true)
}

// BeginLocalLoginAttempt is BeginLoginAttempt for a request made directly on this
// computer (not through a proxy): only its own lockout applies, so the owner can still
// sign in at the computer while sign-in from outside is paused.
func (rl *RateLimiter) BeginLocalLoginAttempt(ip string) bool {
	return rl.beginAttempt(ip, false)
}

func (rl *RateLimiter) beginAttempt(ip string, global bool) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k, now := loginKey(ip), time.Now()
	cutoff := now.Add(-rl.lockDuration)
	rl.pruneLoginFailsLocked(cutoff)
	if global && now.Before(rl.globalUntil) && !rl.isKnownLocked(k, now) {
		return false
	}
	recent := rl.loginFails[k][:0]
	for _, t := range rl.loginFails[k] {
		if t.After(cutoff) {
			recent = append(recent, t)
		}
	}
	if len(recent) >= rl.maxFails {
		rl.loginFails[k] = recent
		return false
	}
	if global {
		gcut := now.Add(-rl.globalWindow)
		kept := rl.globalFails[:0]
		for _, f := range rl.globalFails {
			if f.at.After(gcut) {
				kept = append(kept, f)
			}
		}
		rl.globalFails = kept
		if len(kept) >= rl.globalMax {
			// Pause everyone; each pause within a day of the last one lasts twice as long.
			if now.Sub(rl.globalLast) < 24*time.Hour {
				rl.globalLevel++
			} else {
				rl.globalLevel = 1
			}
			pause := rl.globalBase
			for i := 1; i < rl.globalLevel && pause < rl.globalMaxPause; i++ {
				pause *= 2
			}
			if pause > rl.globalMaxPause {
				pause = rl.globalMaxPause
			}
			rl.globalLast, rl.globalUntil, rl.globalFails = now, now.Add(pause), nil
			rl.loginFails[k] = recent
			return false
		}
		rl.globalFails = append(rl.globalFails, loginFail{at: now, key: k})
	}
	rl.loginFails[k] = append(recent, now)
	return true
}

// rememberLocked records k as a caller that gave a correct password. Callers on this
// computer and unidentified tunnel callers are never remembered: they share one key.
func (rl *RateLimiter) rememberLocked(k string, now time.Time) {
	if ip := net.ParseIP(k); (ip != nil && ip.IsLoopback()) || k == UnknownTunnelCaller {
		return
	}
	if rl.known == nil {
		rl.known = map[string]time.Time{}
	}
	if _, ok := rl.known[k]; !ok && len(rl.known) >= knownMax {
		oldest, at := "", now
		for key, t := range rl.known {
			if t.Before(at) {
				oldest, at = key, t
			}
		}
		delete(rl.known, oldest)
	}
	rl.known[k] = now
}

func (rl *RateLimiter) isKnownLocked(k string, now time.Time) bool {
	t, ok := rl.known[k]
	return ok && now.Sub(t) < knownFor
}

// AllowAPIRequest enforces a sliding per-minute request limit.
func (rl *RateLimiter) AllowAPIRequest(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	k := loginKey(ip) // an IPv6 caller counts per /64, so rotating addresses gives no extra room
	cutoff := time.Now().Add(-time.Minute)
	if len(rl.apiRequests) > 1000 && time.Since(rl.apiPruned) > 10*time.Second {
		rl.apiPruned = time.Now()
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
	tunnelHost     func() string // remote access: the public host the tunnel on this computer serves
)

// SetTunnelHost registers the tunnel program on this computer (remote access). host
// returns the public host name it serves, or "" while remote access is unusable. A
// loopback peer then counts as a trusted proxy only for requests addressed to that host
// (Host, or the last X-Forwarded-Host entry): any other local program or a DNS-rebinding
// page still gets the Host check, and its X-Forwarded-For is ignored (S1). nil switches
// this off; ResetTrustedProxies does too.
func SetTunnelHost(host func() string) {
	proxyMu.Lock()
	tunnelHost = host
	proxyMu.Unlock()
}

// NamesHost reports whether r is addressed to host: its Host header, or, for a proxy that
// rewrote Host, the last X-Forwarded-Host entry (the one the nearest proxy added).
func NamesHost(r *http.Request, host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	bare := func(h string) string {
		h = strings.TrimSpace(h)
		if name, _, err := net.SplitHostPort(h); err == nil {
			h = name
		}
		return strings.ToLower(strings.Trim(h, "[]"))
	}
	if bare(r.Host) == host {
		return true
	}
	vals := r.Header.Values("X-Forwarded-Host")
	if len(vals) == 0 {
		return false
	}
	parts := strings.Split(vals[len(vals)-1], ",")
	return bare(parts[len(parts)-1]) == host
}

// viaTunnel reports whether r is a loopback request addressed to the registered tunnel host.
func viaTunnel(r *http.Request, ip net.IP) bool {
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	proxyMu.RLock()
	f := tunnelHost
	proxyMu.RUnlock()
	if f == nil {
		return false
	}
	return NamesHost(r, f())
}

// trustedPeer reports whether r's TCP peer is a proxy whose forwarding headers are believed.
func trustedPeer(r *http.Request) bool {
	ip := net.ParseIP(normalizeIP(r.RemoteAddr))
	return ip != nil && (isTrustedProxy(ip) || viaTunnel(r, ip))
}

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
	trustedProxies, tunnelHost = nil, nil
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

// proxyHeaders are added by tunnels and reverse proxies (cloudflared, ngrok, Caddy,
// Nginx, Apache, IIS, CDNs) but never by a browser talking to 127.0.0.1 directly. A
// proxy that adds none of them (plain Nginx proxy_pass) cannot be recognised, which is
// why the docs say to forward only /cloud/webhook.
var proxyHeaders = []string{"Cf-Ray", "Cf-Connecting-Ip", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto",
	"X-Forwarded-Port", "X-Forwarded-Server", "X-Forwarded-Scheme", "Forwarded", "X-Real-Ip", "X-Original-Host",
	"X-Original-Url", "X-Original-Forwarded-For", "True-Client-Ip", "X-Client-Ip", "X-Cluster-Client-Ip", "Via"}

// HasProxyHeaders reports whether r carries any header a tunnel or reverse proxy adds.
func HasProxyHeaders(r *http.Request) bool {
	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// FromTrustedProxy reports whether r came straight from a configured trusted proxy.
func FromTrustedProxy(r *http.Request) bool {
	return trustedPeer(r)
}

// ViaUntrustedProxy reports whether r carries forwarding headers but did not come from
// a configured trusted proxy, i.e. it most likely arrived through the public tunnel.
func ViaUntrustedProxy(r *http.Request) bool {
	if trustedPeer(r) {
		return false
	}
	for _, h := range proxyHeaders {
		if r.Header.Get(h) != "" {
			return true
		}
	}
	return false
}

// UnknownTunnelCaller is the address used for a request that came through the tunnel
// without a usable X-Forwarded-For entry. It must not share this computer's address, or
// wrong passwords sent through the tunnel would lock the owner out at the computer (N3).
const UnknownTunnelCaller = "tunnel-unknown"

// GetClientIP returns the peer IP. Forwarding headers are honoured only from trusted proxies.
func GetClientIP(r *http.Request) string {
	host := normalizeIP(r.RemoteAddr)
	ip := net.ParseIP(host)
	tunnel := ip != nil && !isTrustedProxy(ip) && viaTunnel(r, ip)
	if ip != nil && (tunnel || isTrustedProxy(ip)) {
		// Only X-Forwarded-For, read from the right: each proxy appends the address it
		// saw, so the first entry that is not a trusted proxy is the real caller. Headers
		// such as X-Real-IP or CF-Connecting-IP are passed through unchanged by many
		// proxies, so a caller could set them to dodge the lockout and request limits.
		// Behind the tunnel, loopback entries are hops on this computer too.
		parts := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			p := net.ParseIP(strings.TrimSpace(parts[i]))
			if p == nil {
				break // a malformed entry: do not trust anything left of it
			}
			if !isTrustedProxy(p) && !(tunnel && p.IsLoopback()) {
				return p.String()
			}
		}
		if tunnel {
			return UnknownTunnelCaller
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
	for _, h := range proxyHeaders {
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
		rh, rp = requestHost, "" // behind a reverse proxy: the Host header has no port
	}
	rh = strings.ToLower(strings.Trim(rh, "[]"))
	// Another program on a different local port is a different website (S6), even
	// though browsers send it the same cookies. Only a Host without a port (a reverse
	// proxy on 80 or 443) accepts either standard port.
	if rp == "" {
		if op != "80" && op != "443" {
			return false
		}
	} else if op != rp {
		return false
	}
	if oh == rh {
		return true
	}
	loop := func(h string) bool { return h == "localhost" || h == "127.0.0.1" || h == "::1" }
	return loop(oh) && loop(rh)
}

// IsAllowedSecureOrigin is IsAllowedOrigin for requests that came through the https
// tunnel (remote access): an Origin, when present, must also use https (S6).
func IsAllowedSecureOrigin(origin, requestHost string) bool {
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "https" && IsAllowedOrigin(origin, requestHost)
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

// ApplyRemoteHeaders adds the headers for responses served through the https tunnel
// (remote access): browsers then never use plain http for that address (S6).
func ApplyRemoteHeaders(w http.ResponseWriter) {
	w.Header().Set("Strict-Transport-Security", "max-age=31536000")
}

var blockedNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "fc00::/7", "fe80::/10", "ff00::/8", "64:ff9b::/96",
		// IPv6 forms that can carry a private IPv4 address or are not public: IPv4-compatible
		// (::a.b.c.d), 6to4, Teredo, old site-local, discard and documentation ranges.
		"::/96", "2002::/16", "2001::/32", "fec0::/10", "100::/64", "2001:db8::/32",
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
			// The signature headers travel with a redirect, and 307/308 resend the body:
			// never follow one to another host or from https down to http.
			if via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
				return errors.New("redirect from https to http refused")
			}
			if !strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				return fmt.Errorf("redirect to another address (%s) refused: enter the final address instead", req.URL.Host)
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
	ra, ca := keyPos(a)
	rb, cb := keyPos(b)
	if ra < 0 || rb < 0 {
		return false
	}
	if ra == rb {
		return cb == ca+1 || cb == ca-1 // next key on the same row
	}
	// The key above or below: rows are staggered, so 1 sits over q, q over a, a over z,
	// and each key also touches the one up and to the right (1q2w3e4r, qaz2wsx) (S24).
	if rb == ra+1 {
		return cb == ca || cb == ca-1
	}
	if rb == ra-1 {
		return cb == ca || cb == ca+1
	}
	return false
}

func keyPos(r rune) (row, col int) {
	for i, k := range keyboardRows {
		if j := strings.IndexRune(k, r); j >= 0 {
			return i, j
		}
	}
	return -1, -1
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
		// A common word with one or two letters added (hello2024x) is just as easy (S24).
		l := letters.String()
		for w := range commonWords {
			if len(w) >= 4 && strings.Contains(l, w) && len(l)-len(w) <= 2 && len(digits.String()) <= 4 {
				return true
			}
		}
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
