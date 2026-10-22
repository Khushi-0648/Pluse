package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whatsapp-automation/pkg/contacts"
)

func TestChatAssignmentAndNote(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	w, out := h.api("POST", "/conversations/meta", map[string]any{"phone": "+91 98765 43210", "assigned_to": "Ravi", "notes": "Wants a call after 5 pm"})
	if w.Code != 200 || out["assigned_to"] != "Ravi" {
		t.Fatalf("assign failed: %d %v", w.Code, out)
	}
	c, _ := h.cs.Get("919876543210")
	if c.AssignedTo != "Ravi" || c.Notes != "Wants a call after 5 pm" {
		t.Fatalf("not stored: %+v", c)
	}
	// Saving the contact form must not drop the assignment.
	_ = h.cs.Save(contacts.Contact{Phone: c.Phone, Name: "Asha", Notes: c.Notes})
	if c2, _ := h.cs.Get(c.Phone); c2.AssignedTo != "Ravi" {
		t.Fatal("the contact form must keep who handles the chat")
	}
	h.api("POST", "/conversations/meta", map[string]any{"phone": c.Phone, "assigned_to": ""})
	if c3, _ := h.cs.Get(c.Phone); c3.AssignedTo != "" || c3.Notes == "" {
		t.Fatal("clearing the assignment must keep the note")
	}
}

func TestAutomaticBackupAndRestore(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	_ = h.cs.Save(contacts.Contact{Phone: "14155550900", Name: "Backed Up"})
	if w, _ := h.api("POST", "/backups/now", nil); w.Code != 200 {
		t.Fatalf("backup now failed: %d", w.Code)
	}
	_, list := h.api("GET", "/backups", nil)
	items, _ := list["backups"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected one backup, got %v", list)
	}
	name := items[0].(map[string]any)["name"].(string)
	h.cs.Delete("14155550900")
	if w, _ := h.api("POST", "/backups/restore", map[string]any{"name": name, "password": "wrong-pass1"}); w.Code != http.StatusForbidden {
		t.Fatal("restoring needs the admin password")
	}
	if w, _ := h.api("POST", "/backups/restore", map[string]any{"name": "../../config.yaml", "password": adminPass}); w.Code != http.StatusBadRequest {
		t.Fatal("only backups from the list may be restored")
	}
	if w, _ := h.api("POST", "/backups/restore", map[string]any{"name": name, "password": adminPass}); w.Code != 200 {
		t.Fatalf("restore failed: %d", w.Code)
	}
	if _, ok := h.cs.Get("14155550900"); !ok {
		t.Fatal("the contact must come back from the automatic backup")
	}
	for i := 0; i < 10; i++ { // only the last 7 days are kept
		_ = os.WriteFile(filepath.Join(h.s.backupDir(), "pulse-backup-2020-01-0"+string(rune('0'+i%10))+".json"), []byte("{}"), 0600)
	}
	pruneBackups(h.s.backupDir())
	if entries, _ := os.ReadDir(h.s.backupDir()); len(entries) != autoBackupKeep {
		t.Fatalf("expected %d backups kept, got %d", autoBackupKeep, len(entries))
	}
}

func TestDeleteUnencryptedOldCopies(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	dir := filepath.Join(h.s.oldCopiesDir(), "20261020-211002")
	_ = os.MkdirAll(dir, 0700)
	_ = os.WriteFile(filepath.Join(dir, "contacts.json"), []byte(`[{"phone":"14155550100"}]`), 0600)
	if _, out := h.api("GET", "/backups", nil); out["old_copies"] != float64(1) {
		t.Fatalf("the old copy must be reported: %v", out)
	}
	if w, _ := h.api("POST", "/privacy/delete-old-copies", map[string]any{"password": "nope1234x"}); w.Code != http.StatusForbidden {
		t.Fatal("deleting needs the admin password")
	}
	if w, _ := h.api("POST", "/privacy/delete-old-copies", map[string]any{"password": adminPass}); w.Code != 200 {
		t.Fatalf("delete failed: %d", w.Code)
	}
	if _, err := os.Stat(h.s.oldCopiesDir()); !os.IsNotExist(err) {
		t.Fatal("the unencrypted copies must be gone")
	}
}

func TestStopEndpointNeedsLocalToken(t *testing.T) {
	h := newHarness(t)
	quit := make(chan struct{}, 1)
	h.s.Quit, h.s.ControlToken = func() { quit <- struct{}{} }, "tok123"
	send := func(remote, token string) int {
		req := httptest.NewRequest("POST", "/control/quit", nil)
		req.Host, req.RemoteAddr = "127.0.0.1:8085", remote
		req.Header.Set("X-Pulse-Control", token)
		w := httptest.NewRecorder()
		h.s.Handler().ServeHTTP(w, req)
		return w.Code
	}
	if send("192.168.1.20:5000", "tok123") != http.StatusForbidden {
		t.Fatal("a request from another computer must never stop Pulse")
	}
	if send("127.0.0.1:5000", "wrong") != http.StatusForbidden {
		t.Fatal("the stop key is required")
	}
	if send("127.0.0.1:5000", "tok123") != http.StatusOK {
		t.Fatal("the stop command must work with the right key")
	}
	select {
	case <-quit:
	case <-time.After(2 * time.Second):
		t.Fatal("Quit was not called")
	}
}
