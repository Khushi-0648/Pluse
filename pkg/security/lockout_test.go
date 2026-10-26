package security

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

// S2: addresses inside one IPv6 /64 share a lockout.
func TestLockoutGroupsIPv6By64(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < 5; i++ {
		if !rl.BeginLoginAttempt(fmt.Sprintf("[2001:db8:1:2::%x]:443", i+1)) {
			t.Fatalf("attempt %d refused too early", i)
		}
	}
	if rl.BeginLoginAttempt("2001:db8:1:2:ffff::9") {
		t.Fatal("a new address in the same /64 must still be locked out")
	}
	if !rl.BeginLoginAttempt("2001:db8:1:3::1") {
		t.Fatal("another /64 has its own lockout")
	}
	if loginKey("1.2.3.4:5") != "1.2.3.4" || loginKey("::ffff:1.2.3.4") != "1.2.3.4" {
		t.Fatal("IPv4 addresses are keyed one by one")
	}
}

// S2: many callers together get a fixed budget; this computer is exempt from it only.
func TestGlobalLoginBudget(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < 20; i++ {
		if !rl.BeginLoginAttempt(fmt.Sprintf("203.0.113.%d", i+1)) {
			t.Fatalf("attempt %d refused before the budget was used up", i)
		}
	}
	if rl.BeginLoginAttempt("198.51.100.1") {
		t.Fatal("after 20 wrong passwords from all callers, a fresh address must be refused")
	}
	if !rl.LoginPausedForAll() {
		t.Fatal("sign-in must be paused for all")
	}
	first := time.Until(rl.globalUntil)
	if first < 14*time.Minute {
		t.Fatalf("the first pause is too short: %v", first)
	}
	if !rl.BeginLocalLoginAttempt("127.0.0.1") {
		t.Fatal("the owner at the computer must still be able to try")
	}
	for i := 0; i < 5; i++ {
		rl.BeginLocalLoginAttempt("127.0.0.1")
	}
	if rl.BeginLocalLoginAttempt("127.0.0.1") {
		t.Fatal("this computer keeps its own lockout")
	}

	// The next pause within a day lasts longer.
	rl.mu.Lock()
	rl.globalUntil = time.Now().Add(-time.Second)
	rl.mu.Unlock()
	for i := 0; i < 20; i++ {
		rl.BeginLoginAttempt(fmt.Sprintf("192.0.2.%d", i+1))
	}
	if rl.BeginLoginAttempt("198.51.100.2") {
		t.Fatal("the budget must apply again after a pause")
	}
	if second := time.Until(rl.globalUntil); second <= first {
		t.Fatalf("the second pause (%v) must be longer than the first (%v)", second, first)
	}
}

// A successful sign-in takes back only its own attempt from the budget.
func TestResetTakesBackOneAttempt(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < 3; i++ {
		rl.BeginLoginAttempt("203.0.113.1")
	}
	rl.ResetLoginFails("203.0.113.1")
	if n := len(rl.globalFails); n != 2 {
		t.Fatalf("earlier failures must stay counted in the budget, got %d", n)
	}
	if !rl.BeginLoginAttempt("203.0.113.1") {
		t.Fatal("the caller's own lockout is cleared")
	}
}

// S8: old failures do not pile up in memory.
func TestLoginFailsPruned(t *testing.T) {
	rl := NewRateLimiter()
	old := time.Now().Add(-time.Hour)
	rl.mu.Lock()
	for i := 0; i < 1500; i++ {
		rl.loginFails[fmt.Sprintf("k%d", i)] = []time.Time{old}
	}
	rl.mu.Unlock()
	rl.BeginLocalLoginAttempt("127.0.0.1")
	if n := len(rl.loginFails); n > 1 {
		t.Fatalf("stale entries must be pruned, %d left", n)
	}
}

// S9: a session with an idle limit ends when unused, and use keeps it alive.
func TestSessionIdleTimeout(t *testing.T) {
	sm := NewSessionManager(time.Hour)
	defer sm.Close()
	tok, _ := sm.CreateSessionWithIdle("admin", time.Hour)
	plain, _ := sm.CreateSessionWithRole("admin")
	if !sm.ValidateSession(tok) {
		t.Fatal("a fresh session is valid")
	}
	sm.mu.Lock()
	sm.sessions[tok].seen = time.Now().Add(-2 * time.Hour)
	sm.sessions[plain].seen = time.Now().Add(-2 * time.Hour)
	sm.mu.Unlock()
	if sm.GetSessionRole(tok) != "" || sm.ValidateSession(tok) {
		t.Fatal("an idle session must end")
	}
	if !sm.ValidateSession(plain) {
		t.Fatal("a session without an idle limit is unaffected")
	}
}

// S1: with remote access the local tunnel is trusted only for the public host.
func TestTunnelTrustNeedsPublicHost(t *testing.T) {
	ResetTrustedProxies()
	SetTunnelHost(func() string { return "pulse.example.com" })
	t.Cleanup(ResetTrustedProxies)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Host = "127.0.0.1:8085"
	r.Header.Set("X-Forwarded-Proto", "https")
	r.Header.Set("X-Forwarded-For", "203.0.113.7")
	if FromTrustedProxy(r) || GetClientIP(r) != "127.0.0.1" || !ViaUntrustedProxy(r) {
		t.Fatal("a local request for another host must not be treated as the tunnel")
	}
	r.Host = "evil.example:8085" // DNS rebinding
	if FromTrustedProxy(r) {
		t.Fatal("a rebinding page must not be treated as the tunnel")
	}
	r.Host = "pulse.example.com"
	if !FromTrustedProxy(r) || GetClientIP(r) != "203.0.113.7" {
		t.Fatalf("the tunnel's requests carry the caller's address: %s", GetClientIP(r))
	}
	r.Header.Set("X-Forwarded-For", "203.0.113.7, 127.0.0.1")
	if GetClientIP(r) != "203.0.113.7" {
		t.Fatal("loopback hops behind the tunnel are skipped")
	}
	r.Host = "127.0.0.1:8085"
	r.Header.Set("X-Forwarded-Host", "pulse.example.com, evil.example")
	if FromTrustedProxy(r) {
		t.Fatal("only the last X-Forwarded-Host entry counts")
	}
	r.Header.Set("X-Forwarded-Host", "evil.example, pulse.example.com")
	if FromTrustedProxy(r) {
		t.Fatal("F9: X-Forwarded-Host alone (settable by a rebinding page) must not make a local request the tunnel")
	}
	r.Host = "pulse.example.com"
	if !FromTrustedProxy(r) {
		t.Fatal("the tunnel passes the public host in Host")
	}
	r.Host = "127.0.0.1:8085"
	r.RemoteAddr = "203.0.113.9:4000"
	if FromTrustedProxy(r) {
		t.Fatal("only a loopback peer can be the tunnel")
	}
	SetTunnelHost(func() string { return "" })
	r.RemoteAddr, r.Host = "127.0.0.1:5000", "pulse.example.com"
	if FromTrustedProxy(r) {
		t.Fatal("no public host: nothing is the tunnel")
	}
}

// S6: through the tunnel the Origin must be https; HSTS is sent.
func TestSecureOriginAndHSTS(t *testing.T) {
	if !IsAllowedSecureOrigin("", "pulse.example.com") || !IsAllowedSecureOrigin("https://pulse.example.com", "pulse.example.com") {
		t.Fatal("an https Origin for the same host is allowed")
	}
	if IsAllowedSecureOrigin("http://pulse.example.com", "pulse.example.com") {
		t.Fatal("an http Origin must be refused through the tunnel")
	}
	if IsAllowedSecureOrigin("https://evil.example", "pulse.example.com") {
		t.Fatal("another site stays refused")
	}
	w := httptest.NewRecorder()
	ApplyRemoteHeaders(w)
	if w.Header().Get("Strict-Transport-Security") != "max-age=31536000" {
		t.Fatal("HSTS missing")
	}
}

// N1: guesses from other addresses cannot keep a caller that recently signed in
// correctly (the owner's phone) shut out; its own lockout still applies.
func TestKnownCallerKeepsAccessDuringAPause(t *testing.T) {
	rl := NewRateLimiter()
	phone := "198.51.100.50"
	if !rl.BeginLoginAttempt(phone) {
		t.Fatal("first attempt refused")
	}
	rl.ResetLoginFails(phone) // correct password
	for i := 0; i < 20; i++ {
		rl.BeginLoginAttempt(fmt.Sprintf("203.0.113.%d", i+1))
	}
	if rl.BeginLoginAttempt("192.0.2.77") || !rl.LoginPausedForAll() {
		t.Fatal("unknown callers must be paused")
	}
	if !rl.BeginLoginAttempt(phone) {
		t.Fatal("a caller that recently signed in correctly must not be paused by others' guesses")
	}
	for i := 0; i < 5; i++ {
		rl.BeginLoginAttempt(phone)
	}
	if rl.BeginLoginAttempt(phone) {
		t.Fatal("the known caller keeps its own lockout")
	}
	rl.ResetLoginFails("127.0.0.1")
	rl.ResetLoginFails(UnknownTunnelCaller)
	if rl.isKnownLocked("127.0.0.1", time.Now()) || rl.isKnownLocked(UnknownTunnelCaller, time.Now()) {
		t.Fatal("shared keys must never be remembered")
	}
}

// N3: a tunnel request without X-Forwarded-For does not share this computer's lockout.
func TestTunnelWithoutForwardedForIsNotThisComputer(t *testing.T) {
	ResetTrustedProxies()
	SetTunnelHost(func() string { return "pulse.example.com" })
	t.Cleanup(ResetTrustedProxies)
	r := httptest.NewRequest("POST", "/login", nil)
	r.RemoteAddr, r.Host = "127.0.0.1:5000", "pulse.example.com"
	r.Header.Set("X-Forwarded-Proto", "https")
	if got := GetClientIP(r); got != UnknownTunnelCaller {
		t.Fatalf("got %q", got)
	}
}

// N6: the request limit counts an IPv6 caller per /64.
func TestAPILimitGroupsIPv6(t *testing.T) {
	rl := NewRateLimiter()
	rl.apiPerMinute = 3
	for i := 1; i <= 3; i++ {
		if !rl.AllowAPIRequest(fmt.Sprintf("[2001:db8:1:2::%x]:443", i)) {
			t.Fatalf("request %d refused", i)
		}
	}
	if rl.AllowAPIRequest("[2001:db8:1:2::ffff]:443") {
		t.Fatal("another address in the same /64 must share the limit")
	}
	if !rl.AllowAPIRequest("[2001:db8:9:9::1]:443") {
		t.Fatal("another /64 has its own limit")
	}
}
