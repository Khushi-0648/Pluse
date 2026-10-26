package contacts

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seeded opens a store whose file was written by Pulse 2.0 with list, so those contacts
// count as being from before the upgrade.
func seeded(t *testing.T, list ...Contact) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "contacts.json")
	data, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

var before = time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)

// F1: saving "+65 9123 4567" never replaces the Indian contact 916591234567 or its opt-out.
func TestSavingForeignNumberLeavesIndianContactAlone(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Save(Contact{Phone: "+91 65912 34567", Name: "Ravi"}); err != nil {
		t.Fatal(err)
	}
	s.SetDND("916591234567", true, "Replied STOP")
	for _, c := range []Contact{{Phone: "+65 9123 4567", Name: "SG"}, {Phone: "0065 9123 4567", Name: "SG"}} {
		if err := s.Save(c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SaveExact(Contact{Phone: "6591234567", Name: "SG"}); err != nil {
		t.Fatal(err)
	}
	in, ok := s.GetExact("916591234567")
	if !ok || in.Name != "Ravi" || !in.IsDND {
		t.Fatalf("the Indian contact and its opt-out must stay: %+v", in)
	}
	if sg, ok := s.GetExact("6591234567"); !ok || sg.Name != "SG" || s.Count() != 2 {
		t.Fatalf("the Singapore number is its own contact: %+v (count %d)", sg, s.Count())
	}
	// A 10-digit number without a country code that matches nothing exactly is not
	// matched to another country's contact by its last digits either.
	if err := s.Save(Contact{Phone: "+1 202 555 0123", Name: "US"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveExact(Contact{Phone: "2025550123", Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	if us, _ := s.GetExact("12025550123"); us == nil || us.Name != "US" {
		t.Fatalf("saving another number replaced the US contact: %+v", us)
	}
}

// F3: a stored number that is no contact (deleted after opting out) never resolves to a
// look-alike consented contact; a restore of it comes back as do-not-message on its own.
func TestStoredNumberLookupIsExact(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Save(Contact{Phone: "+91 65912 34567", Name: "Ravi", Consent: true, ConsentSource: "form"})
	_ = s.Save(Contact{Phone: "+65 9123 4567", Name: "Wei"})
	s.SetDND("6591234567", true, "Replied STOP")
	s.Delete("6591234567")
	if c, ok := s.GetExact("6591234567"); ok {
		t.Fatalf("a deleted number resolved to %+v", c)
	}
	if !s.WasOptedOut("6591234567") {
		t.Fatal("the deleted number opted out")
	}
	at := before
	if err := s.Restore(Contact{Phone: "6591234567", Name: "Wei", Notes: "from backup", Consent: true, ConsentAt: &at}); err != nil {
		t.Fatal(err)
	}
	if in, _ := s.GetExact("916591234567"); in == nil || in.Notes != "" {
		t.Fatalf("the restore was merged into the Indian contact: %+v", in)
	}
	if sg, ok := s.GetExact("6591234567"); !ok || !sg.IsDND || sg.Consent {
		t.Fatalf("the restored contact must be do-not-message: %+v", sg)
	}
	// STOP, START and consent changes go to exactly that number.
	s.Delete("6591234567")
	if s.SetConsent("6591234567", true, "staff") || s.SetDND("2025550123", true, "x") {
		t.Fatal("an update must not land on a look-alike contact")
	}
	if in, _ := s.GetExact("916591234567"); in.IsDND {
		t.Fatal("the Indian contact was changed")
	}
}

// F4: only a contact from before the upgrade moves; a real Indian customer added since,
// who wrote and opted out, keeps their own number and opt-out (also after a restart).
func TestOnlyPreUpgradeContactsMove(t *testing.T) {
	s, p := newStore(t)
	s.Upsert("916591234567", "Ravi", "whatsapp")
	s.RecordInbound("916591234567", time.Now())
	s.SetDND("916591234567", true, "Replied STOP")
	_ = s.Flush()
	again, err := NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range []*Store{s, again} {
		c := st.Upsert("6591234567", "Wei", "whatsapp")
		if c.IsDND || c.Name != "Wei" {
			t.Fatalf("the Singapore sender is a new contact: %+v", c)
		}
		if in, ok := st.GetExact("916591234567"); !ok || !in.IsDND || in.Name != "Ravi" {
			t.Fatalf("the Indian customer was moved: %+v", in)
		}
	}
	// A contact from Pulse 2.0 moves with its opt-out but not its consent.
	old, _ := seeded(t, Contact{Phone: "916591234568", Name: "Mei", Source: "whatsapp", Consent: true, ConsentSource: "form", ConsentAt: &before, Tags: []string{"VIP"}, CreatedAt: before})
	c := old.Upsert("6591234568", "", "whatsapp")
	if c.Phone != "6591234568" || c.Name != "Mei" || c.Consent || c.ConsentAt != nil || len(c.Tags) != 1 || old.Count() != 1 {
		t.Fatalf("moved contact: %+v (count %d)", c, old.Count())
	}
}

// F5: when the real number already has a contact from before the upgrade, an opt-out
// Pulse 2.0 kept under the 91 form still applies (once), and WasOptedOut sees a live
// opted-out contact under that form.
func TestLegacyOptOutAppliesBesideExistingContact(t *testing.T) {
	// Pulse 2.0 stored every number through CleanPhone, so the contact under the real
	// number was added later, by 2.1.0 (which did not look for the 91 form).
	s, _ := seeded(t,
		Contact{Phone: "6591234567", Name: "Wei", Source: "manual", CreatedAt: before.Add(time.Hour)},
		Contact{Phone: "916591234567", Source: "whatsapp", IsDND: true, ConsentAt: &before, CreatedAt: before},
		Contact{Phone: "916591234568", Source: "whatsapp", IsDND: true, ConsentAt: &before, CreatedAt: before},
	)
	if !s.WasOptedOut("6591234568") {
		t.Fatal("an opted-out contact under the 91 form must count")
	}
	if c := s.Upsert("6591234567", "", "whatsapp"); !c.IsDND || !c.ConsentAt.Equal(before) {
		t.Fatalf("the opt-out under the old form must apply: %+v", c)
	}
	if in, ok := s.GetExact("916591234567"); !ok || !in.IsDND {
		t.Fatal("the old form stays opted out")
	}
	// Staff opt them back in: the old opt-out is not applied again.
	s.SetDND("6591234567", false, "")
	if c := s.Upsert("6591234567", "", "whatsapp"); c.IsDND {
		t.Fatal("an opt-in after the old opt-out was applied must stay")
	}
}

// F6: opting in a number that is not a contact does not lift its earlier opt-out.
func TestOptInOfMissingContactKeepsSuppression(t *testing.T) {
	s, _ := newStore(t)
	s.Upsert("14155550100", "", "whatsapp")
	s.SetDND("14155550100", true, "Replied STOP")
	s.Delete("14155550100")
	if s.SetDND("14155550100", false, "") || s.SetConsent("14155550100", true, "staff") {
		t.Fatal("no contact: nothing changes")
	}
	if !s.WasOptedOut("14155550100") {
		t.Fatal("the opt-out of a deleted contact was lifted by a failed update")
	}
}

// F8: erasing a foreign number also blocks its Pulse 2.0 form in a restore, and the
// other way round; an opt-out recorded under that form is not lost.
func TestForgetCoversLegacyForm(t *testing.T) {
	s, _ := newStore(t)
	s.Upsert("916591234567", "", "whatsapp")
	s.SetDND("916591234567", true, "Replied STOP")
	s.Delete("916591234567")
	s.Upsert("6591234567", "Wei", "whatsapp")
	s.Forget("6591234567")
	if err := s.Restore(Contact{Phone: "916591234567", Name: "Wei", CreatedAt: before}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetExact("916591234567"); ok {
		t.Fatal("the erased customer came back under the 91 form")
	}
	if !s.WasOptedOut("916591234567") {
		t.Fatal("the 91 form had opted out and must stay opted out")
	}
	// An erasure recorded under the 91 form blocks the real number too.
	s2, _ := newStore(t)
	s2.Upsert("916591234569", "", "whatsapp")
	s2.Forget("916591234569")
	_ = s2.Restore(Contact{Phone: "6591234569", Name: "Lim", CreatedAt: before})
	if _, ok := s2.GetExact("6591234569"); ok {
		t.Fatal("restore must check the 91 form of the number")
	}
}

// F15: an older message that arrives late does not move the last-message time back.
func TestRecordInboundKeepsLatest(t *testing.T) {
	s, _ := newStore(t)
	s.Upsert("14155550101", "", "whatsapp")
	now := time.Now()
	s.RecordInbound("14155550101", now)
	s.RecordInbound("14155550101", now.Add(-20*time.Hour))
	if c, _ := s.GetExact("14155550101"); c.LastInboundAt == nil || !c.LastInboundAt.Equal(now) {
		t.Fatalf("last inbound moved back: %v", c.LastInboundAt)
	}
}
