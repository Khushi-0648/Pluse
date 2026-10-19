package security

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestValidateWebhookURL(t *testing.T) {
	// Loopback and private ranges must be blocked (SSRF)
	blockedURLs := []string{
		"http://127.0.0.1:8080/test",
		"http://localhost:8085/api",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.1/webhook",
		"http://192.168.1.1/admin",
		"http://172.16.0.1/test",
		"ftp://example.com/file",
	}

	for _, u := range blockedURLs {
		if err := ValidateWebhookURL(u); err == nil {
			t.Errorf("Expected URL %s to be blocked by SSRF validator, but passed", u)
		}
	}
}

func TestSessionManager(t *testing.T) {
	sm := NewSessionManager(1 * time.Hour)
	sessToken, csrfToken := sm.CreateSession()

	if sessToken == "" || csrfToken == "" {
		t.Fatalf("Expected non-empty session and csrf tokens")
	}

	if !sm.ValidateSession(sessToken) {
		t.Errorf("Expected session token %s to be valid", sessToken)
	}

	if sm.GetCSRFToken(sessToken) != csrfToken {
		t.Errorf("Expected csrf token %s, got %s", csrfToken, sm.GetCSRFToken(sessToken))
	}

	sm.DestroySession(sessToken)
	if sm.ValidateSession(sessToken) {
		t.Errorf("Expected destroyed session to be invalid")
	}
}

func TestAuditLog(t *testing.T) {
	LogAudit("UNIT_TEST_ACTION", "127.0.0.1", "test_user", "Unit test execution details", "success")
	logs := GetAuditLog(10)

	found := false
	for _, l := range logs {
		if l.Action == "UNIT_TEST_ACTION" {
			found = true
			if l.User != "test_user" || l.Status != "success" {
				t.Errorf("Unexpected audit log fields: %+v", l)
			}
			break
		}
	}

	if !found {
		t.Errorf("Expected UNIT_TEST_ACTION in audit log")
	}
}

func TestIsLocalRequest(t *testing.T) {
	reqLocal := httptest.NewRequest("GET", "/", nil)
	reqLocal.RemoteAddr = "127.0.0.1:54321"

	if !IsLocalRequest(reqLocal) {
		t.Errorf("Expected 127.0.0.1 to be recognized as local request")
	}

	reqRemote := httptest.NewRequest("GET", "/", nil)
	reqRemote.RemoteAddr = "203.0.113.195:54321"

	if IsLocalRequest(reqRemote) {
		t.Errorf("Expected 203.0.113.195 to be recognized as remote request")
	}
}

func TestGetClientIP_ProxySpoofProtection(t *testing.T) {
	// S13 / Test 4: Forged X-Forwarded-For must NOT bypass rate limiter
	req := httptest.NewRequest("GET", "/login", nil)
	req.RemoteAddr = "127.0.0.1:45678"
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 10.0.0.1")
	req.Header.Set("X-Real-IP", "203.0.113.195")
	req.Header.Set("CF-Connecting-IP", "203.0.113.195")

	ip := GetClientIP(req)
	if ip != "127.0.0.1" {
		t.Errorf("Expected GetClientIP to return 127.0.0.1 when proxy trust is disabled, got %s", ip)
	}
}

func TestValidateHostHeader(t *testing.T) {
	// S17: DNS rebinding protection
	validHosts := []string{"127.0.0.1:8080", "127.0.0.1:8085", "localhost:8080", "localhost", "127.0.0.1"}
	for _, h := range validHosts {
		if !ValidateHostHeader(h, 8080) {
			t.Errorf("Expected host %s to be valid, but was rejected", h)
		}
	}

	invalidHosts := []string{"evil.com", "attacker.com:8080", "127.0.0.1.nip.io:8080", "example.org"}
	for _, h := range invalidHosts {
		if ValidateHostHeader(h, 8080) {
			t.Errorf("Expected host %s to be rejected for DNS rebinding, but passed", h)
		}
	}
}

func TestIsAllowedOrigin(t *testing.T) {
	// S19 / Test 7: Foreign Origin rejection
	if !IsAllowedOrigin("http://127.0.0.1:8080", "127.0.0.1:8080") {
		t.Errorf("Expected local origin to be allowed")
	}
	if !IsAllowedOrigin("http://localhost:8080", "127.0.0.1:8080") {
		t.Errorf("Expected localhost origin to be allowed")
	}
	if IsAllowedOrigin("http://evil.com", "127.0.0.1:8080") {
		t.Errorf("Expected evil.com origin to be rejected")
	}
	if IsAllowedOrigin("https://attacker.org:8080", "127.0.0.1:8080") {
		t.Errorf("Expected attacker origin to be rejected")
	}
}

func TestValidatePassword_S20(t *testing.T) {
	weakPasswords := []string{
		"",
		"123",
		"123456",
		"1234567",
		"admin",
		"password",
		"12345678",       // numbers only
		"abcdefgh",       // letters only
		"11111111",       // all same
		"aaaaaaaa",       // all same
		"password123",    // common weak
		"admin123",       // common weak
		"qwerty123",      // common weak
	}

	for _, p := range weakPasswords {
		if err := ValidatePassword(p); err == nil {
			t.Errorf("Expected password %q to be rejected, but it was accepted", p)
		}
	}

	strongPasswords := []string{
		"P@ssword2026",
		"SecureAlpha99!",
		"pulseKey#88",
		"myStrongSecret12",
	}

	for _, p := range strongPasswords {
		if err := ValidatePassword(p); err != nil {
			t.Errorf("Expected password %q to be valid, but got error: %v", p, err)
		}
	}
}

