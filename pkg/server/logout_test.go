package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	"whatsapp-automation/pkg/client"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/security"
)

func TestLogoutAndRelinkClearsUserData(t *testing.T) {
	// Temporary history store
	tmpHist, err := os.CreateTemp("", "logout_hist_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpHist.Name())
	tmpHist.Close()

	hs, err := history.NewStore(tmpHist.Name(), 100)
	if err != nil {
		t.Fatalf("Failed to create history store: %v", err)
	}

	// Temporary contacts store
	tmpContacts, err := os.CreateTemp("", "logout_contacts_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpContacts.Name())
	tmpContacts.Close()

	cs, err := contacts.NewStore(tmpContacts.Name())
	if err != nil {
		t.Fatalf("Failed to create contacts store: %v", err)
	}

	// Populate data
	_ = cs.Save(contacts.Contact{Phone: "14155551234", Name: "Test Contact", CreatedAt: time.Now()})
	hs.Record(history.MessageRecord{
		ID:          "m1",
		Direction:   history.DirectionIncoming,
		Type:        history.TypeUserMessage,
		SenderPhone: "14155551234",
		SenderName:  "Test Contact",
		Text:        "Hello there",
		Timestamp:   time.Now(),
	})

	if len(cs.List()) != 1 {
		t.Fatalf("Expected 1 contact before logout, got %d", len(cs.List()))
	}
	if len(hs.List(10, "", "")) != 1 {
		t.Fatalf("Expected 1 message before logout, got %d", len(hs.List(10, "", "")))
	}

	srv := &Server{
		cfg:          config.DefaultConfig(),
		sessionMgr:   security.NewSessionManager(1 * time.Hour),
		contactStore: cs,
		histStore:    hs,
	}

	// Test handleLogout
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	w := httptest.NewRecorder()
	srv.handleLogout(w, req)

	if w.Code != http.StatusSeeOther {
		t.Fatalf("Expected redirect status 303, got %d", w.Code)
	}

	// Verify contacts and messages are safely purged across logout as requested
	if len(cs.List()) != 0 {
		t.Fatalf("Expected 0 contacts after logout, got %d", len(cs.List()))
	}
	if len(hs.List(10, "", "")) != 0 {
		t.Fatalf("Expected 0 messages after logout, got %d", len(hs.List(10, "", "")))
	}

	// Verify session cookie was expired
	foundCookie := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "wa_session" {
			foundCookie = true
			if c.MaxAge != -1 {
				t.Fatalf("Expected wa_session cookie MaxAge to be -1, got %d", c.MaxAge)
			}
		}
	}
	if !foundCookie {
		t.Fatal("Expected wa_session cookie in logout response")
	}
}

func TestWhatsAppRelinkPurgesSessionData(t *testing.T) {
	// Temporary history store
	tmpHist, err := os.CreateTemp("", "relink_hist_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpHist.Name())
	tmpHist.Close()

	hs, err := history.NewStore(tmpHist.Name(), 100)
	if err != nil {
		t.Fatalf("Failed to create history store: %v", err)
	}

	// Temporary contacts store
	tmpContacts, err := os.CreateTemp("", "relink_contacts_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpContacts.Name())
	tmpContacts.Close()

	cs, err := contacts.NewStore(tmpContacts.Name())
	if err != nil {
		t.Fatalf("Failed to create contacts store: %v", err)
	}

	// Populate data
	_ = cs.Save(contacts.Contact{Phone: "14155559999", Name: "Purge Me", CreatedAt: time.Now()})
	hs.Record(history.MessageRecord{
		ID:          "m-purge",
		Direction:   history.DirectionIncoming,
		Type:        history.TypeUserMessage,
		SenderPhone: "14155559999",
		SenderName:  "Purge Me",
		Text:        "Sensitive WhatsApp message",
		Timestamp:   time.Now(),
	})

	if len(cs.List()) != 1 || len(hs.List(10, "", "")) != 1 {
		t.Fatal("Data setup failed")
	}

	srv := &Server{
		cfg:          config.DefaultConfig(),
		sessionMgr:   security.NewSessionManager(1 * time.Hour),
		contactStore: cs,
		histStore:    hs,
	}

	// Call handleWhatsAppLogout from localhost
	req := httptest.NewRequest(http.MethodPost, "/api/v1/whatsapp/logout", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	w := httptest.NewRecorder()
	srv.handleWhatsAppLogout(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200 OK, got %d (body: %s)", w.Code, w.Body.String())
	}

	// Verify that contacts and message history are completely cleared upon WhatsApp logout
	if len(cs.List()) != 0 {
		t.Fatalf("Expected 0 contacts after WhatsApp logout, got %d", len(cs.List()))
	}
	if len(hs.List(10, "", "")) != 0 {
		t.Fatalf("Expected 0 messages after WhatsApp logout, got %d", len(hs.List(10, "", "")))
	}
}

func TestClearUserDataPurgesAllSessionData(t *testing.T) {
	tmpHist, err := os.CreateTemp("", "clear_hist_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpHist.Name())
	tmpHist.Close()

	hs, err := history.NewStore(tmpHist.Name(), 100)
	if err != nil {
		t.Fatalf("Failed to create history store: %v", err)
	}

	tmpContacts, err := os.CreateTemp("", "clear_contacts_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpContacts.Name())
	tmpContacts.Close()

	cs, err := contacts.NewStore(tmpContacts.Name())
	if err != nil {
		t.Fatalf("Failed to create contacts store: %v", err)
	}

	_ = cs.Save(contacts.Contact{Phone: "14155558888", Name: "Session User", CreatedAt: time.Now()})
	hs.Record(history.MessageRecord{
		ID:          "m-session",
		Direction:   history.DirectionIncoming,
		Type:        history.TypeUserMessage,
		SenderPhone: "14155558888",
		SenderName:  "Session User",
		Text:        "Session Message",
		Timestamp:   time.Now(),
	})

	srv := &Server{
		contactStore: cs,
		histStore:    hs,
	}

	srv.ClearUserData()

	if len(cs.List()) != 0 {
		t.Fatalf("Expected 0 contacts after ClearUserData, got %d", len(cs.List()))
	}
	if len(hs.List(10, "", "")) != 0 {
		t.Fatalf("Expected 0 messages after ClearUserData, got %d", len(hs.List(10, "", "")))
	}
}

func TestContactsNotVisibleWhenWhatsAppLoggedOut(t *testing.T) {
	tmpContacts, err := os.CreateTemp("", "test_hide_contacts_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpContacts.Name())
	tmpContacts.Close()

	cs, err := contacts.NewStore(tmpContacts.Name())
	if err != nil {
		t.Fatal(err)
	}

	_ = cs.Save(contacts.Contact{Phone: "14155557777", Name: "Ghost User", CreatedAt: time.Now()})
	if len(cs.List()) != 1 {
		t.Fatal("Contact not saved")
	}

	srv := &Server{
		contactStore: cs,
		client:       &client.WhatsAppClient{},
	}

	// Trigger WhatsApp logout / session clear
	srv.ClearUserData()

	// Test GET /api/v1/contacts
	req := httptest.NewRequest(http.MethodGet, "/api/v1/contacts", nil)
	w := httptest.NewRecorder()
	srv.handleContacts(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	var returnedContacts []contacts.Contact
	if err := json.NewDecoder(w.Body).Decode(&returnedContacts); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Contacts MUST NOT be visible when device is logged out!
	if len(returnedContacts) != 0 {
		t.Fatalf("Expected 0 contacts visible when logged out, got %d", len(returnedContacts))
	}
}



