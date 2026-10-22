package security

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidatePassword(t *testing.T) {
	bad := []string{"123456", "12345678", "password1", "Admin1234", "abcdefgh1", "aaaa1111", "MyPulse2026", "qwerty99x",
		"abcd1234", "qwer1234", "asdf4321", "hello2024", "india123", "test12345", "zyxw9876"} // S24
	for _, p := range bad {
		if ValidatePassword(p) == nil {
			t.Errorf("password %q should be rejected", p)
		}
	}
	for _, p := range []string{"river7Lamp", "Kx9-tiger-moon", "blue42Harbor", "harbor42Blue", "Sunita@Dental9"} {
		if err := ValidatePassword(p); err != nil {
			t.Errorf("password %q should be accepted: %v", p, err)
		}
	}
}

func TestValidateWebhookURLBlocksLocalTargets(t *testing.T) {
	blocked := []string{
		"http://127.0.0.1/x", "http://localhost:8085/", "http://169.254.169.254/latest", "http://10.0.0.5/",
		"http://192.168.1.10/", "http://0.0.0.0:8085/", "http://[::]:8085/", "http://[::1]/", "http://[::ffff:127.0.0.1]/",
		"http://100.64.1.1/", "ftp://example.com/", "http://user:pass@example.com/", "http://printer.local/",
	}
	for _, u := range blocked {
		if ValidateWebhookURL(u) == nil {
			t.Errorf("%s should be blocked", u)
		}
	}
	if err := ValidateWebhookURL("https://93.184.216.34/hook"); err != nil {
		t.Errorf("public IP should be allowed: %v", err)
	}
}

func TestSafeHTTPClientRefusesLoopbackAtConnectTime(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	_, err := SafeHTTPClient(2 * time.Second).Get(ts.URL)
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected the dialer to block loopback, got %v", err)
	}
}

func TestValidateAttachmentAllowList(t *testing.T) {
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 32)...)
	jpg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 32)...)
	pdf := []byte("%PDF-1.7\n...")
	cases := []struct {
		name  string
		data  []byte
		valid bool
		clean string
	}{
		{"photo.png", png, true, "photo.png"},
		{"photo.png", jpg, true, "photo.jpg"},
		{"invoice.pdf", pdf, true, "invoice.pdf"},
		{"evil.png", append([]byte("MZ"), make([]byte, 64)...), false, ""},
		{"page.html", []byte("<!doctype html><script>alert(1)</script>"), false, ""},
		{"notes.txt", []byte("hello"), false, ""},
		{`..\..\x.png`, png, true, "x.png"},
	}
	for _, c := range cases {
		r := ValidateAttachment(c.name, c.data, MaxAttachmentBytes)
		if r.Valid != c.valid {
			t.Errorf("%s: valid=%v want %v (%s)", c.name, r.Valid, c.valid, r.Error)
		}
		if c.valid && r.CleanName != c.clean {
			t.Errorf("%s: clean name %q want %q", c.name, r.CleanName, c.clean)
		}
	}
	big := append(append([]byte{}, png...), bytes.Repeat([]byte{0}, MaxAttachmentBytes)...)
	if ValidateAttachment("big.png", big, MaxAttachmentBytes).Valid {
		t.Error("files over the limit must be rejected")
	}
}

func TestHostAndOriginChecks(t *testing.T) {
	for _, h := range []string{"127.0.0.1:8085", "localhost:8085", "[::1]:8085", "localhost"} {
		if !ValidateHostHeader(h) {
			t.Errorf("%s should be allowed", h)
		}
	}
	for _, h := range []string{"evil.com", "127.0.0.1.nip.io:8085", ""} {
		if ValidateHostHeader(h) {
			t.Errorf("%s should be rejected", h)
		}
	}
	if !ValidateHostHeader("pulse.example.com", "pulse.example.com") {
		t.Error("configured extra host should be allowed")
	}
	if !IsAllowedOrigin("http://127.0.0.1:8085", "127.0.0.1:8085") || IsAllowedOrigin("https://evil.com", "127.0.0.1:8085") {
		t.Error("origin check is wrong")
	}
}

func TestSessionsAndRoles(t *testing.T) {
	sm := NewSessionManager(time.Hour)
	defer sm.Close()
	tok, csrf := sm.CreateSessionWithRole("operator")
	if !sm.ValidateSession(tok) || sm.GetSessionRole(tok) != "operator" || sm.GetCSRFToken(tok) != csrf {
		t.Fatal("session not stored correctly")
	}
	if sm.GetSessionRole("unknown") != "" {
		t.Error("unknown sessions must have no role")
	}
	other, _ := sm.CreateSessionWithRole("admin")
	sm.DestroyAll(tok)
	if sm.ValidateSession(other) || !sm.ValidateSession(tok) {
		t.Error("DestroyAll should keep only the excepted session")
	}
}

func TestRateLimiterAndClientIP(t *testing.T) {
	rl := NewRateLimiter()
	for i := 0; i < 5; i++ {
		rl.RecordFailedLogin("1.2.3.4:5")
	}
	if !rl.IsLoginLocked("1.2.3.4:9") {
		t.Error("five failures should lock the IP")
	}
	ResetTrustedProxies()
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Forwarded-For", "8.8.8.8")
	if GetClientIP(r) != "127.0.0.1" {
		t.Error("forwarded headers from untrusted peers must be ignored")
	}
	_ = AddTrustedProxy("127.0.0.1/32")
	defer ResetTrustedProxies()
	if GetClientIP(r) != "8.8.8.8" {
		t.Error("forwarded headers from a trusted proxy should be used")
	}
}

func TestCSPHasNoUnsafeInline(t *testing.T) {
	if strings.Contains(ContentSecurityPolicy, "unsafe-inline") || strings.Contains(ContentSecurityPolicy, "unsafe-eval") {
		t.Fatal("CSP must not allow inline script or style")
	}
}

func TestIsLocalRequestRefusesProxiedRequests(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	if !IsLocalRequest(r) {
		t.Fatal("a direct loopback request is local")
	}
	for _, h := range []string{"X-Forwarded-For", "Forwarded", "X-Real-IP"} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = "127.0.0.1:5000"
		r.Header.Set(h, "203.0.113.9")
		if IsLocalRequest(r) {
			t.Errorf("a request with %s came through a proxy and is not local", h)
		}
	}
}
