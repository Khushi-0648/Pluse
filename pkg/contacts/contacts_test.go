package contacts

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/securestore"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "contacts.json")
	s, err := NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	return s, p
}

func TestSaveKeepsSystemFieldsAndCleansNames(t *testing.T) {
	s, _ := newStore(t)
	if err := s.Save(Contact{Phone: "+91 98765 43210", Name: "+919876543210"}); err != nil {
		t.Fatal(err)
	}
	c, ok := s.Get("9876543210")
	if !ok || c.Phone != "919876543210" || c.Name != "" {
		t.Fatalf("phone normalisation or placeholder name wrong: %+v", c)
	}
	s.RecordInbound(c.Phone, time.Now())
	_ = s.Save(Contact{Phone: c.Phone, Name: "Asha", Tags: []string{"VIP", "vip", " "}})
	c, _ = s.Get(c.Phone)
	if c.LastInboundAt == nil || c.Name != "Asha" || len(c.Tags) != 1 {
		t.Fatalf("save lost data: %+v", c)
	}
	if !c.InServiceWindow(time.Now()) {
		t.Error("recent inbound should open the 24-hour window")
	}
	if s.Save(Contact{Phone: "123"}) == nil {
		t.Error("short numbers must be rejected")
	}
}

func TestOptOutWithdrawsConsent(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Save(Contact{Phone: "14155550100", Consent: true, ConsentSource: "form"})
	c, _ := s.Get("14155550100")
	if !c.Consent || c.ConsentAt == nil {
		t.Fatal("consent not recorded")
	}
	s.SetDND(c.Phone, true, "Replied STOP")
	c, _ = s.Get(c.Phone)
	if !c.IsDND || c.Consent {
		t.Error("opting out must withdraw consent")
	}
	s.SetConsent(c.Phone, true, "Replied START")
	c, _ = s.Get(c.Phone)
	if c.IsDND || !c.Consent || c.ConsentSource != "Replied START" {
		t.Error("opting in must clear do-not-message")
	}
}

func TestPauseAndAttention(t *testing.T) {
	s, _ := newStore(t)
	s.PauseBot("14155550101", time.Hour, "asked for a person")
	if !s.IsBotPaused("14155550101") || len(s.NeedsAttention()) != 1 {
		t.Fatal("pause should create the contact and flag it")
	}
	s.ResumeBot("14155550101")
	if s.IsBotPaused("14155550101") || len(s.NeedsAttention()) != 0 {
		t.Fatal("resume should clear the pause and flag")
	}
}

func TestCSVImportMergesAndReports(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Save(Contact{Phone: "14155550102", Name: "Old Name", Tags: []string{"VIP"}, Email: "old@example.com"})
	csv := "phone,name,email,tags,notes,consent\n" +
		"+1 415 555 0102,,,Lead,,\n" +
		"14155550103,Bob,bob@example.com,Customer;Lead,note,yes\n" +
		"badnumber,X,,,,\n" +
		"14155550103,,,,second note,\n"
	res, err := s.ImportCSV(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Updated != 2 || res.Skipped != 1 || len(res.Warnings) != 1 {
		t.Fatalf("unexpected result %+v", res)
	}
	a, _ := s.Get("14155550102")
	if a.Name != "Old Name" || a.Email != "old@example.com" || len(a.Tags) != 2 {
		t.Errorf("import must merge, not blank fields (C19): %+v", a)
	}
	b, _ := s.Get("14155550103")
	if !b.Consent || b.Notes != "second note" || len(b.Tags) != 2 {
		t.Errorf("consent column or duplicate merge wrong: %+v", b)
	}
	p, err := PreviewCSV(strings.NewReader("Mobile,Full Name\n9876543210,Asha\n"))
	if err != nil || p.Mapping.Phone != 0 || p.Mapping.Name != 1 || !p.Mapping.Header || p.Total != 1 {
		t.Errorf("preview mapping wrong: %+v %v", p, err)
	}
	var out bytes.Buffer
	_ = s.Save(Contact{Phone: "14155550104", Name: "=HYPERLINK(\"x\")"})
	_ = s.ExportCSV(&out)
	if strings.Contains(out.String(), ",=HYPERLINK") {
		t.Error("export must neutralise spreadsheet formulas")
	}
}

func TestEncryptedPersistence(t *testing.T) {
	dir := t.TempDir()
	box, err := securestore.OpenBox(dir)
	if err != nil {
		t.Fatal(err)
	}
	securestore.SetDefault(box)
	defer securestore.SetDefault(nil)
	p := filepath.Join(dir, "contacts.json")
	s, _ := NewStore(p)
	_ = s.Save(Contact{Phone: "14155550199", Name: "Secret Person"})
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if bytes.Contains(raw, []byte("Secret Person")) || !securestore.IsEncrypted(raw) {
		t.Fatal("contacts file must be encrypted at rest (S9)")
	}
	s2, err := NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := s2.Get("14155550199"); !ok || c.Name != "Secret Person" {
		t.Fatal("encrypted file did not load back")
	}
}

func TestNumbersMatchExactly(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Save(contacts447())
	if _, ok := s.Get("+917700900123"); ok {
		t.Fatal("an Indian number must not match a UK contact with the same last 10 digits")
	}
	if c, ok := s.Get("447700900123"); !ok || c.Name != "UK" {
		t.Fatal("exact match failed")
	}
}

func contacts447() Contact { return Contact{Phone: "447700900123", Name: "UK"} }

func TestRestoreNeverUndoesOptOut(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Save(Contact{Phone: "14155550500", Name: "Out"})
	s.SetDND("14155550500", true, "STOP")
	_ = s.Restore(Contact{Phone: "14155550500", Name: "Out", IsDND: false, Consent: true, Tags: []string{"VIP"}})
	c, _ := s.Get("14155550500")
	if !c.IsDND || c.Consent || len(c.Tags) != 2 {
		t.Fatalf("restore changed the opt-out or consent: %+v", c)
	}
	_ = s.Restore(Contact{Phone: "14155550501", Consent: true})
	if c, _ := s.Get("14155550501"); c.Consent {
		t.Fatal("restore must not invent consent without a recorded date")
	}
}

func TestImportKeepsOptOutsAndRejectsExcelNumbers(t *testing.T) {
	s, _ := newStore(t)
	csv := "phone,name,consent,consent_source,opted_out\n" +
		"14155550600,Out,yes,Shop form,yes\n" +
		"14155550601,In,yes,Shop form,no\n" +
		"9.19877E+11,Broken,,,\n"
	res, err := s.ImportCSV(strings.NewReader(csv))
	if err != nil || res.Added != 2 || res.Skipped != 1 {
		t.Fatalf("unexpected result %+v %v", res, err)
	}
	if c, _ := s.Get("14155550600"); !c.IsDND || c.Consent {
		t.Error("an opted_out column must be honoured on import")
	}
	if c, _ := s.Get("14155550601"); !c.Consent || c.ConsentSource != "Shop form" {
		t.Errorf("consent evidence from the file must be kept: %+v", c)
	}
}

func TestOptOutAlwaysWithdrawsConsentOnSave(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Save(Contact{Phone: "14155550700", Consent: true, IsDND: true})
	if c, _ := s.Get("14155550700"); c.Consent {
		t.Fatal("a do-not-message contact cannot also be opted in")
	}
}

func TestLongCountryNumbersAreNotInternalIDs(t *testing.T) {
	for _, p := range []string{"5511987654321", "8613812345678", "4915112345678"} {
		if IsLID(p) {
			t.Errorf("%s is a real phone number", p)
		}
	}
	if !IsLID("123456789012345@lid") || !IsLID("203780453318713") {
		t.Error("linked-device IDs must still be recognised")
	}
}

func TestCleanPhone(t *testing.T) {
	cases := map[string]string{"+91 98765-43210": "919876543210", "09876543210": "919876543210", "9876543210": "919876543210", "0044 20 7946 0958": "442079460958", "14155550100@s.whatsapp.net": "14155550100"}
	for in, want := range cases {
		if got := CleanPhone(in); got != want {
			t.Errorf("CleanPhone(%q)=%q want %q", in, got, want)
		}
	}
}
