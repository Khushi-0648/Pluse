package server

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"testing"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
)

func TestFirstRunPasswordGenerationAndEnforcement(t *testing.T) {
	// 1. Setup clean config with blank password (shipped state)
	cfg := config.DefaultConfig()
	cfg.Server.Host = "127.0.0.1"
	cfg.Server.Port = 8085
	cfg.Server.AdminPassword = ""

	tmpContacts, err := os.CreateTemp("", "setup_contacts_*.json")
	if err != nil {
		t.Fatal(err)
	}
	tmpContacts.Close()
	defer os.Remove(tmpContacts.Name())

	cs, err := contacts.NewStore(tmpContacts.Name())
	if err != nil {
		t.Fatal(err)
	}

	tmpHist, err := os.CreateTemp("", "setup_hist_*.json")
	if err != nil {
		t.Fatal(err)
	}
	tmpHist.Close()
	defer os.Remove(tmpHist.Name())

	hs, err := history.NewStore(tmpHist.Name(), 100)
	if err != nil {
		t.Fatal(err)
	}

	// 2. EnsureAdminPassword generates a random temporary password
	genPass, isNew := cfg.EnsureAdminPassword()
	if !isNew || genPass == "" {
		t.Fatalf("Expected new password to be generated, got isNew=%v, pass=%s", isNew, genPass)
	}
	if !cfg.Server.MustChangePassword {
		t.Fatalf("Expected MustChangePassword to be true after initial generation")
	}

	srv := NewServer(cfg, nil, cs, nil, hs, nil, nil)
	memTransport := &testMemoryTransport{handler: srv.httpServer.Handler}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	client := &http.Client{
		Jar:       jar,
		Transport: memTransport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	baseURL := "http://127.0.0.1:8085"

	// 3. Attempt login with empty password -> must fail
	formEmpty := url.Values{"password": {""}}
	respEmpty, err := client.PostForm(baseURL+"/login", formEmpty)
	if err != nil {
		t.Fatal(err)
	}
	respEmpty.Body.Close()
	if respEmpty.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized for empty password, got %d", respEmpty.StatusCode)
	}

	// 4. Attempt login with "admin" -> must fail (we didn't ship admin)
	formAdmin := url.Values{"password": {"admin"}}
	respAdmin, err := client.PostForm(baseURL+"/login", formAdmin)
	if err != nil {
		t.Fatal(err)
	}
	respAdmin.Body.Close()
	if respAdmin.StatusCode != http.StatusUnauthorized {
		t.Errorf("Expected 401 Unauthorized for default 'admin', got %d", respAdmin.StatusCode)
	}

	// 5. Login with the generated temporary password -> must succeed and redirect to /setup-password
	formGen := url.Values{"password": {genPass}}
	respGen, err := client.PostForm(baseURL+"/login", formGen)
	if err != nil {
		t.Fatal(err)
	}
	respGen.Body.Close()
	if respGen.StatusCode != http.StatusSeeOther {
		t.Errorf("Expected 303 redirect on login success, got %d", respGen.StatusCode)
	}
	if loc := respGen.Header.Get("Location"); loc != "/setup-password" {
		t.Errorf("Expected redirect to /setup-password, got %s", loc)
	}

	// 6. Attempt to access dashboard / before setting password -> must be forced back to /setup-password
	respDash, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	respDash.Body.Close()
	if respDash.StatusCode != http.StatusSeeOther || respDash.Header.Get("Location") != "/setup-password" {
		t.Errorf("Expected redirect to /setup-password when accessing /, got code=%d loc=%s", respDash.StatusCode, respDash.Header.Get("Location"))
	}

	// 7. Attempt to access API before setting password -> must return 403 Forbidden
	respAPI, err := client.Get(baseURL + "/api/v1/contacts")
	if err != nil {
		t.Fatal(err)
	}
	respAPI.Body.Close()
	if respAPI.StatusCode != http.StatusForbidden {
		t.Errorf("Expected 403 Forbidden when calling API before password change, got %d", respAPI.StatusCode)
	}

	// 8. Submit setup form with mismatched passwords -> 400 Bad Request
	formMismatch := url.Values{
		"new_password":     {"MySecurePass123"},
		"confirm_password": {"DifferentPass456"},
	}
	respMismatch, err := client.PostForm(baseURL+"/setup-password", formMismatch)
	if err != nil {
		t.Fatal(err)
	}
	respMismatch.Body.Close()
	if respMismatch.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for mismatched passwords, got %d", respMismatch.StatusCode)
	}

	// 8b. Submit setup form with weak password (123456) -> 400 Bad Request (S20)
	formWeak := url.Values{
		"new_password":     {"123456"},
		"confirm_password": {"123456"},
	}
	respWeak, err := client.PostForm(baseURL+"/setup-password", formWeak)
	if err != nil {
		t.Fatal(err)
	}
	respWeak.Body.Close()
	if respWeak.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected 400 Bad Request for weak password 123456, got %d", respWeak.StatusCode)
	}

	// 9. Submit setup form with valid permanent password -> 303 Redirect to /
	formValid := url.Values{
		"new_password":     {"MyPermanentSecretPass2026!"},
		"confirm_password": {"MyPermanentSecretPass2026!"},
	}
	respValid, err := client.PostForm(baseURL+"/setup-password", formValid)
	if err != nil {
		t.Fatal(err)
	}
	respValid.Body.Close()
	if respValid.StatusCode != http.StatusSeeOther || respValid.Header.Get("Location") != "/" {
		t.Errorf("Expected 303 redirect to / after setting password, got code=%d loc=%s", respValid.StatusCode, respValid.Header.Get("Location"))
	}

	// 10. Verify MustChangePassword is now false and new password verifies
	if cfg.Server.MustChangePassword {
		t.Errorf("Expected MustChangePassword to be false after password setup")
	}
	if !cfg.VerifyPassword("MyPermanentSecretPass2026!") {
		t.Errorf("Expected new permanent password to verify successfully")
	}

	// 11. Now accessing dashboard / succeeds with 200 OK
	respFinalDash, err := client.Get(baseURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	respFinalDash.Body.Close()
	if respFinalDash.StatusCode != http.StatusOK {
		t.Errorf("Expected 200 OK on / after password initialized, got %d", respFinalDash.StatusCode)
	}
}
