package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/scheduler"
	"whatsapp-automation/pkg/templates"
	"whatsapp-automation/pkg/webhook"
)

// newSeededHarness is newHarness on a contact list that Pulse 2.0 wrote with list.
func newSeededHarness(t *testing.T, list ...contacts.Contact) *harness {
	t.Helper()
	h := newHarness(t)
	dir := t.TempDir()
	data, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "c.json")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	cs, err := contacts.NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	hs, _ := history.NewStore(filepath.Join(dir, "h.json"), 1000)
	ts, _ := templates.NewStore(filepath.Join(dir, "t.json"))
	runner, _ := campaign.NewRunner(filepath.Join(dir, "camp.json"), h.wa, h.cfg, cs, hs)
	sch, _ := scheduler.NewScheduler(filepath.Join(dir, "s.json"), h.wa, h.cfg, cs, hs)
	s := New(Deps{Config: h.cfg, Messenger: h.wa, Conn: h.wa, Contacts: cs, Templates: ts, History: hs, Scheduler: sch, Campaigns: runner, Hooks: webhook.New(h.cfg, ""), DataDir: dir})
	t.Cleanup(func() { s.sessions.Close() })
	h.s, h.cs, h.hs = s, cs, hs
	return h
}

// A1: the Singapore contact 6591234567, who has no messages, never shows or exports the
// Indian customer 916591234567's messages: in the privacy export, the contact view and
// the message list.
func TestStoredNumberNeverShowsLookAlikeMessages(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	h.cs.Upsert("6591234567", "Wei", "whatsapp")
	h.cs.Upsert("916591234567", "Ravi", "whatsapp")
	h.hs.Record(history.MessageRecord{ID: "r1", Direction: history.DirectionIncoming, Type: history.TypeUserMessage, Phone: "916591234567", Text: "Ravi's private order", Timestamp: time.Now()})
	for _, path := range []string{"/privacy/export?phone=6591234567", "/contacts/6591234567", "/messages?phone=6591234567"} {
		w, _ := h.api("GET", path, nil)
		if strings.Contains(w.Body.String(), "private order") {
			t.Fatalf("%s showed another customer's messages: %s", path, w.Body.String())
		}
	}
	if w, _ := h.api("GET", "/privacy/export?phone=916591234567", nil); !strings.Contains(w.Body.String(), "private order") {
		t.Fatalf("the Indian customer's own export: %d", w.Code)
	}
}

// A3: erasing a customer also removes the backup copy of the scheduled messages, which
// still held their messages.
func TestForgetRemovesScheduledBackup(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	at := time.Now().Add(48 * time.Hour)
	job, _, err := h.s.Scheduler.Add("+14155550777", "for the erased customer", at, "", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.s.Scheduler.Add("+14155550778", "for someone else", at, "", "test", nil); err != nil {
		t.Fatal(err)
	}
	if w, out := h.api("POST", "/privacy/forget-contact", map[string]any{"phone": "+14155550777", "password": adminPass}); w.Code != 200 {
		t.Fatalf("forget: %d %v", w.Code, out)
	}
	for _, j := range h.s.Scheduler.List() {
		if j.ID == job.ID {
			t.Fatal("the scheduled message is still listed")
		}
	}
	path := filepath.Join(h.s.DataDir, "s.json")
	for _, f := range []string{path, path + ".bak"} {
		if b, err := os.ReadFile(f); err == nil && (strings.Contains(string(b), job.ID) || strings.Contains(string(b), "14155550777")) {
			t.Fatalf("%s still holds the erased customer's scheduled message", filepath.Base(f))
		}
	}
}

// A4: erasing a foreign customer also erases the contact and messages Pulse 2.0 kept for
// them under the 91 form, when that contact is certainly 2.0's record of them.
func TestForgetErasesPulse20Record(t *testing.T) {
	before := time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)
	h := newSeededHarness(t, contacts.Contact{Phone: "916591234567", Name: "Wei", Source: "whatsapp", CreatedAt: before, LastInboundAt: &before, IsDND: true, ConsentAt: &before})
	h.login(adminPass)
	h.hs.Record(history.MessageRecord{ID: "old1", Direction: history.DirectionIncoming, Type: history.TypeUserMessage, Phone: "916591234567", Text: "old order", Timestamp: before})
	w, out := h.api("POST", "/privacy/forget-contact", map[string]any{"phone": "+65 9123 4567", "password": adminPass})
	if w.Code != 200 {
		t.Fatalf("forget: %d %v", w.Code, out)
	}
	if c, ok := h.cs.GetExact("916591234567"); ok {
		t.Fatalf("the Pulse 2.0 record of the customer was kept: %+v", c)
	}
	if got := h.hs.Query(history.Query{Phone: "916591234567"}); len(got) != 0 {
		t.Fatalf("the customer's messages under the 91 form were kept: %+v", got)
	}
	// The old form had opted out: it stays opted out in case it is someone else's too.
	if !h.cs.WasOptedOut("916591234567") {
		t.Fatal("the opt-out under the 91 form was lost")
	}
}

// A4: a contact under the 91 form that may be a different (Indian) customer is not
// erased with the foreign number; the admin is told it exists.
func TestForgetReportsPossibleIndianRecord(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	h.cs.Upsert("916591234568", "Priya", "whatsapp") // added since the upgrade
	h.hs.Record(history.MessageRecord{ID: "p1", Direction: history.DirectionIncoming, Type: history.TypeUserMessage, Phone: "916591234568", Text: "Priya's order", Timestamp: time.Now()})
	w, out := h.api("POST", "/privacy/forget-contact", map[string]any{"phone": "+65 9123 4568", "password": adminPass})
	if w.Code != 200 {
		t.Fatalf("forget: %d %v", w.Code, out)
	}
	if c, ok := h.cs.GetExact("916591234568"); !ok || c.Name != "Priya" {
		t.Fatal("a possibly different customer was erased")
	}
	if len(h.hs.Query(history.Query{Phone: "916591234568"})) != 1 {
		t.Fatal("that customer's messages were erased")
	}
	if !strings.Contains(fmt.Sprint(out["message"]), "+916591234568") || out["not_erased"] != "+916591234568" {
		t.Fatalf("the admin must be told about the record under the 91 form: %v", out)
	}
}

// A5: a typed bare number never reaches a contact by its last 10 digits: not for a send,
// a scheduled message or an erasure.
func TestTypedNumberNeverMatchesBySuffix(t *testing.T) {
	h := newHarness(t)
	h.login(adminPass)
	h.cs.Upsert("449876543210", "UK", "whatsapp")
	h.cs.RecordInbound("449876543210", time.Now())
	h.cs.Upsert("12025550123", "US", "whatsapp")
	h.cs.RecordInbound("12025550123", time.Now())
	for _, to := range []string{"9876543210", "2025550123"} {
		h.api("POST", "/messages/send", map[string]any{"to": to, "message": "hello"})
		h.api("POST", "/scheduled", map[string]any{"recipient": to, "message": "later", "scheduled_at": time.Now().Add(48 * time.Hour).Format(time.RFC3339)})
	}
	for _, s := range h.wa.sent {
		if strings.HasPrefix(s, "449876543210:") || strings.HasPrefix(s, "12025550123:") {
			t.Fatalf("a typed bare number was sent to another country's contact: %v", h.wa.sent)
		}
	}
	for _, j := range h.s.Scheduler.List() {
		if j.Recipient == "449876543210" || j.Recipient == "12025550123" {
			t.Fatalf("a typed bare number was scheduled for another country's contact: %+v", j)
		}
	}
	if w, _ := h.api("POST", "/privacy/forget-contact", map[string]any{"phone": "9876543210", "password": adminPass}); w.Code != 200 {
		t.Fatalf("forget: %d", w.Code)
	}
	if _, ok := h.cs.GetExact("449876543210"); !ok {
		t.Fatal("erasing a bare Indian mobile erased the UK contact")
	}
	if w, _ := h.api("POST", "/messages/send", map[string]any{"to": "2025550123", "message": "hello"}); w.Code != http.StatusBadRequest {
		t.Fatalf("a bare number with no known country must be refused: %d", w.Code)
	}
}
