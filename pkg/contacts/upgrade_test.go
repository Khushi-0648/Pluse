package contacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"whatsapp-automation/pkg/security"
)

// writeStore writes a contact list and its opt-out list as an earlier version left them.
func writeStore(t *testing.T, list []Contact, sup map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "contacts.json")
	data, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	if sup != nil {
		data, err := json.Marshal(sup)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "contacts-suppressed.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

// withStarts makes the activity log hold the given "started" entries.
func withStarts(t *testing.T, details ...any) {
	t.Helper()
	var ev []security.AuditEvent
	for i := 0; i+1 < len(details); i += 2 {
		ev = append(ev, security.AuditEvent{Timestamp: details[i].(time.Time), Action: "APP_STARTED", Details: details[i+1].(string)})
	}
	oldEvents, oldStart := startEvents, processStart
	startEvents = func() []security.AuditEvent { return ev }
	processStart = time.Now()
	t.Cleanup(func() { startEvents, processStart = oldEvents, oldStart })
}

// A timeline like the owner's PC: Pulse 2.0, then 2.1.0 for a few hours, then 2.1.1,
// which recorded its own first start as upgraded_at.
func timeline() (start20, start210, start211 time.Time) {
	t0 := time.Now().Add(-72 * time.Hour).Truncate(time.Minute)
	return t0, t0.Add(5 * time.Hour), t0.Add(10 * time.Hour)
}

// A2: a real Indian customer added while Pulse 2.1.0 ran is not taken for a contact
// Pulse 2.0 saved under a wrong number when a foreign number writes. 2.1.0 kept a
// Singapore sender's number exactly (2.0 never stored such a number), which shows that
// 2.1 was running since then.
func TestContactFromPulse210IsNotMoved(t *testing.T) {
	_, start210, start211 := timeline()
	sg, ravi := start210.Add(time.Minute), start210.Add(30*time.Minute)
	p := writeStore(t, []Contact{
		{Phone: "6598765432", Source: "whatsapp", CreatedAt: sg, LastInboundAt: &sg},
		{Phone: "916591234567", Name: "Ravi", Source: "whatsapp", CreatedAt: ravi, LastInboundAt: &ravi, Consent: true, ConsentSource: "form", ConsentAt: &ravi, Tags: []string{"VIP"}},
	}, map[string]any{"salt": "s", "numbers": map[string]string{}, "upgraded_at": start211})
	s, err := NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Upsert("6591234567", "Wei", "whatsapp")
	if c.Name != "Wei" || c.Consent || c.IsDND || len(c.Tags) != 0 {
		t.Fatalf("the Singapore sender is a new contact: %+v", c)
	}
	if in, ok := s.GetExact("916591234567"); !ok || in.Name != "Ravi" || !in.Consent {
		t.Fatalf("the Indian customer added under 2.1.0 was moved: %+v", in)
	}
	// The corrected time is kept: after a restart the contact is still not taken.
	_ = s.Flush()
	again, err := NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Upsert("6591234567", "", "whatsapp"); got.Name != "Wei" {
		t.Fatalf("after a restart: %+v", got)
	}
	if _, ok := again.GetExact("916591234567"); !ok {
		t.Fatal("the Indian customer was moved after a restart")
	}
}

// A2: the activity log names each version that started, so the first start of 2.1.0 is
// the boundary: a contact Pulse 2.0 wrote still moves, one added under 2.1.0 does not.
func TestUpgradeTimeFromActivityLog(t *testing.T) {
	start20, start210, start211 := timeline()
	withStarts(t, start20, "Pulse 2.0.0 started", start210, "Pulse 2.1.0 started", start211, "Pulse 2.1.1 started")
	ravi, mei := start210.Add(30*time.Minute), start20.Add(time.Hour)
	p := writeStore(t, []Contact{
		{Phone: "916591234567", Name: "Ravi", Source: "whatsapp", CreatedAt: ravi, LastInboundAt: &ravi},
		{Phone: "916591234568", Name: "Mei", Source: "whatsapp", CreatedAt: mei, LastInboundAt: &mei},
	}, map[string]any{"salt": "s", "numbers": map[string]string{}, "upgraded_at": start211})
	s, err := NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if c := s.Upsert("6591234567", "Wei", "whatsapp"); c.Name != "Wei" {
		t.Fatalf("the Singapore sender is a new contact: %+v", c)
	}
	if in, ok := s.GetExact("916591234567"); !ok || in.Name != "Ravi" {
		t.Fatalf("the Indian customer added under 2.1.0 was moved: %+v", in)
	}
	if c := s.Upsert("6591234568", "", "whatsapp"); c.Name != "Mei" {
		t.Fatalf("the contact Pulse 2.0 saved under 91 must move: %+v", c)
	}
	if _, ok := s.GetExact("916591234568"); ok {
		t.Fatal("the 2.0 record stayed under the wrong number")
	}
}

// A2: when the first start of 2.1 is not known (the log keeps only the newest entries),
// a contact that may be from either version is never moved; its opt-out still applies to
// the number that writes (the safe side), its consent does not.
func TestUnknownUpgradeTimeMovesNothing(t *testing.T) {
	start20, _, start211 := timeline()
	withStarts(t, start211, "Pulse 2.1.1 started")
	asha := start20.Add(time.Hour)
	p := writeStore(t, []Contact{
		{Phone: "916591234569", Name: "Asha", Source: "whatsapp", CreatedAt: asha, LastInboundAt: &asha, IsDND: true, ConsentAt: &asha},
		{Phone: "916591234570", Name: "Bala", Source: "whatsapp", CreatedAt: asha, LastInboundAt: &asha, Consent: true, ConsentSource: "form", ConsentAt: &asha},
	}, map[string]any{"salt": "s", "numbers": map[string]string{}, "upgraded_at": start211})
	s, err := NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	c := s.Upsert("6591234569", "", "whatsapp")
	if !c.IsDND || c.Name == "Asha" {
		t.Fatalf("the opt-out applies, the contact does not move: %+v", c)
	}
	if in, ok := s.GetExact("916591234569"); !ok || !in.IsDND || in.Name != "Asha" {
		t.Fatalf("the contact under 91 must stay as it was: %+v", in)
	}
	if c := s.Upsert("6591234570", "", "whatsapp"); c.Consent || c.Name == "Bala" {
		t.Fatalf("consent never moves: %+v", c)
	}
	if lc, sure := s.LegacyContact("6591234569"); lc == nil || sure {
		t.Fatalf("the contact is only possibly 2.0's record: %+v %v", lc, sure)
	}
}

// A5: a typed number is never matched to a contact by its last digits; a bare 10-digit
// Indian mobile means the Indian number, and another bare short number needs its code.
func TestTypedNumberIsNeverMatchedBySuffix(t *testing.T) {
	s, _ := newStore(t)
	s.Upsert("449876543210", "UK", "whatsapp")
	s.Upsert("12025550123", "US", "whatsapp")
	if c, ok := s.Get("9876543210"); ok {
		t.Fatalf("a bare Indian mobile found another country's contact: %+v", c)
	}
	if c, ok := s.Get("2025550123"); ok {
		t.Fatalf("a bare number found a contact by its last digits: %+v", c)
	}
	if p, err := s.Resolve("9876543210"); err != nil || p != "919876543210" {
		t.Fatalf("a bare Indian mobile reads as +91: %q %v", p, err)
	}
	if _, err := s.Resolve("2025550123"); err != ErrNeedsCountryCode {
		t.Fatalf("a bare number with no known country must be refused: %v", err)
	}
	if p, err := s.Resolve("+1 202 555 0123"); err != nil || p != "12025550123" {
		t.Fatalf("with its country code: %q %v", p, err)
	}
	// A stored number that is in the history but no contact keeps its country.
	if p, _ := s.Resolve("6591234567", func(p string) bool { return p == "6591234567" }); p != "6591234567" {
		t.Fatalf("a stored number must stay exact: %q", p)
	}
	// Formatted without + and stored both ways: refused instead of guessed.
	s.Upsert("6591234567", "SG", "whatsapp")
	s.Upsert("916591234567", "IN", "whatsapp")
	if _, err := s.Resolve("65 9123 4567"); err != ErrAmbiguousNumber {
		t.Fatalf("an ambiguous number must be refused: %v", err)
	}
	if p, _ := s.Resolve("6591234567"); p != "6591234567" {
		t.Fatalf("a stored number is exact: %q", p)
	}
}
