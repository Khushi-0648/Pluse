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

// A restore must never undo an opt-out or an erasure request, even from a backup made
// before them (daily backups keep such copies).
func TestRestoreHonoursOptOutsAndErasure(t *testing.T) {
	s, _ := newStore(t)
	consentAt := time.Now().Add(-48 * time.Hour)
	old := Contact{Phone: "14155550901", Name: "Asha", Consent: true, ConsentSource: "form", ConsentAt: &consentAt}
	gone := Contact{Phone: "14155550902", Name: "Ravi", Consent: true, ConsentSource: "form", ConsentAt: &consentAt}
	_ = s.Save(old)
	_ = s.Save(gone)
	s.SetDND(old.Phone, true, "Replied STOP")
	s.Delete(old.Phone)
	s.Forget(gone.Phone)
	_ = s.Restore(old) // the copy from the backup still has consent and no opt-out
	_ = s.Restore(gone)
	if c, ok := s.Get(old.Phone); !ok || !c.IsDND || c.Consent {
		t.Fatalf("a contact deleted after opting out must come back only as do-not-message: %+v", c)
	}
	if _, ok := s.Get(gone.Phone); ok {
		t.Fatal("a customer who asked to be forgotten must not come back from a backup")
	}
	// The list survives a restart, and "delete all" keeps the opt-outs too.
	s.Flush()
	again, err := NewStore(s.filePath)
	if err != nil {
		t.Fatal(err)
	}
	_ = again.Save(Contact{Phone: "14155550903"})
	again.SetDND("14155550903", true, "STOP")
	_ = again.Clear()
	_ = again.Restore(gone)
	_ = again.Restore(Contact{Phone: "14155550903", Consent: true, ConsentAt: &consentAt})
	if _, ok := again.Get(gone.Phone); ok {
		t.Fatal("the erasure must be remembered after a restart")
	}
	if c, ok := again.Get("14155550903"); !ok || !c.IsDND || c.Consent {
		t.Fatalf("delete all must keep opt-outs for later restores: %+v", c)
	}
	// A contact deleted without an opt-out can still be restored (undo a mistake).
	_ = again.Save(Contact{Phone: "14155550904", Name: "Meena"})
	again.Delete("14155550904")
	_ = again.Restore(Contact{Phone: "14155550904", Name: "Meena"})
	if _, ok := again.Get("14155550904"); !ok {
		t.Fatal("restoring an ordinary deleted contact must still work")
	}
}

// Adding a deleted opted-out number again (form, CSV or an incoming message) must not undo
// the opt-out; opting in again clears it.
func TestReAddingNeverUndoesOptOut(t *testing.T) {
	s, _ := newStore(t)
	for _, p := range []string{"14155550911", "14155550912", "14155550913", "14155550914"} {
		_ = s.Save(Contact{Phone: p, Consent: true, ConsentSource: "form"})
		s.SetDND(p, true, "Replied STOP")
		s.Delete(p)
	}
	if !s.WasOptedOut("+1 415 555 0911") {
		t.Fatal("a deleted opted-out number must be remembered")
	}
	if err := s.Save(Contact{Phone: "14155550911", Consent: true, ConsentSource: "old list"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Get("14155550911"); !c.IsDND || c.Consent {
		t.Fatalf("saving the number again must keep it do-not-message: %+v", c)
	}
	res, err := s.ImportCSV(strings.NewReader("phone,consent\n+14155550912,yes\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Get("14155550912"); !c.IsDND || c.Consent || len(res.Warnings) == 0 {
		t.Fatalf("an import must not undo an opt-out and must say so: %+v %+v", c, res)
	}
	if c := s.Upsert("14155550913", "Asha", "whatsapp"); !c.IsDND {
		t.Fatalf("a new message from an opted-out number must not make it messageable: %+v", c)
	}
	// Opting in again (START) clears the old opt-out for good.
	s.Upsert("14155550914", "", "whatsapp")
	s.SetConsent("14155550914", true, "Replied START")
	s.Delete("14155550914")
	if s.WasOptedOut("14155550914") {
		t.Fatal("after opting in again the old opt-out must be forgotten")
	}
}

// A late, older STOP keeps the newer opt-out time, so a START between the two cannot
// count as newer than the customer's last STOP.
func TestOlderStopKeepsNewerOptOutTime(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Save(Contact{Phone: "14155550930"})
	t1, t3 := time.Now().Add(-30*time.Minute), time.Now().Add(-10*time.Minute)
	s.SetDND("14155550930", true, "STOP at t3", t3)
	s.SetDND("14155550930", true, "STOP at t1", t1)
	if c, _ := s.Get("14155550930"); c.ConsentAt == nil || !c.ConsentAt.Equal(t3) {
		t.Fatalf("the opt-out time must stay at the newest STOP: %+v", c.ConsentAt)
	}
}

// A deleted opted-out customer who writes again comes back with the time they opted
// out, so the guard against an older START still works.
func TestRecreatedContactKeepsOptOutTime(t *testing.T) {
	s, _ := newStore(t)
	stopAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	_ = s.Save(Contact{Phone: "14155550940"})
	s.SetDND("14155550940", true, "Replied STOP", stopAt)
	s.Delete("14155550940")
	c := s.Upsert("14155550940", "Asha", "whatsapp")
	if !c.IsDND || c.ConsentAt == nil || !c.ConsentAt.Equal(stopAt) {
		t.Fatalf("the re-created contact must keep the opt-out time: %+v", c)
	}
	// The time survives a restart.
	s.Delete("14155550940")
	s.Flush()
	again, err := NewStore(s.filePath)
	if err != nil {
		t.Fatal(err)
	}
	if c := again.Upsert("14155550940", "", "whatsapp"); c.ConsentAt == nil || !c.ConsentAt.Equal(stopAt) {
		t.Fatalf("the opt-out time must survive a restart: %+v", c)
	}
}

// System timestamps on a new contact never come from the form.
func TestSaveIgnoresClientTimestampsOnNewContacts(t *testing.T) {
	s, _ := newStore(t)
	now, past := time.Now(), time.Now().Add(-90*24*time.Hour)
	if err := s.Save(Contact{Phone: "14155550920", LastInboundAt: &now, ConsentAt: &past, Consent: true, ConsentSource: "form", NeedsAttention: true}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Get("14155550920")
	if c.LastInboundAt != nil || c.NeedsAttention {
		t.Fatalf("a made-up last message time would open the 24-hour window: %+v", c)
	}
	if c.ConsentAt == nil || c.ConsentAt.Before(now.Add(-time.Minute)) {
		t.Fatalf("the consent date must be when it was recorded, not a back-dated value: %+v", c.ConsentAt)
	}
}

// Export then import must give back the same data.
func TestCSVRoundTripKeepsValuesAndConsentDate(t *testing.T) {
	s, _ := newStore(t)
	consentAt := time.Date(2020, 1, 15, 0, 0, 0, 0, time.Local)
	// A consent date from the past can only come from an import (Save stamps the time).
	if _, err := s.ImportCSV(strings.NewReader("phone,name,notes,consent,consent_source,consent_date\n+14155551001,-Bob,=1+1,yes,shop form,2020-01-15\n")); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := s.ExportCSV(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "'-Bob") {
		t.Fatalf("the export must guard formula cells: %s", buf.String())
	}
	t2, _ := newStore(t)
	if _, err := t2.ImportCSV(strings.NewReader(buf.String())); err != nil {
		t.Fatal(err)
	}
	c, ok := t2.Get("14155551001")
	if !ok || c.Name != "-Bob" || c.Notes != "=1+1" || !c.Consent || c.ConsentAt == nil || !c.ConsentAt.Equal(consentAt) {
		t.Fatalf("round trip changed the contact: %+v", c)
	}
}

// Meta's numbers are full international digits: a Singapore wa_id must not get India's
// 91, while numbers people type still do.
func TestMetaNumbersKeepTheirCountry(t *testing.T) {
	if got := CleanWAID("6591234567"); got != "6591234567" {
		t.Fatalf("a Meta wa_id must never get a country code: %q", got)
	}
	if got := NormalizePhone("6591234567"); got != "6591234567" {
		t.Fatalf("a stored number must stay as it is: %q", got)
	}
	if got := NormalizePhone("+65 9123 4567"); got != "6591234567" {
		t.Fatalf("a typed international number: %q", got)
	}
	if got := CleanPhone("9876543210"); got != "919876543210" {
		t.Fatalf("a typed Indian mobile still gets 91: %q", got)
	}
	s, p := newStore(t)
	if err := s.Save(Contact{Phone: "+65 9123 4567", Name: "Wei"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(Contact{Phone: "98765 43210", Name: "Asha"}); err != nil {
		t.Fatal(err)
	}
	// A message from Meta finds the Singapore contact; no Indian look-alike is created.
	if c := s.Upsert(CleanWAID("6591234567"), "Wei L", "whatsapp"); c.Phone != "6591234567" || c.Name != "Wei" {
		t.Fatalf("the Singapore sender must match the saved contact: %+v", c)
	}
	if _, ok := s.Get("916591234567"); ok {
		t.Fatal("an Indian copy of the Singapore number was created")
	}
	if c := s.Upsert(CleanWAID("919876543210"), "", "whatsapp"); c.Name != "Asha" {
		t.Fatalf("the Indian sender must match the saved contact: %+v", c)
	}
	// Saving the form with the stored digits keeps the number, and a restart keeps it too.
	if err := s.Save(Contact{Phone: "6591234567", Name: "Wei Lin"}); err != nil {
		t.Fatal(err)
	}
	if c, ok := s.Get("6591234567"); !ok || c.Name != "Wei Lin" || s.Count() != 2 {
		t.Fatalf("editing the contact changed its number: %+v (count %d)", c, s.Count())
	}
	_ = s.Flush()
	again, err := NewStore(p)
	if err != nil {
		t.Fatal(err)
	}
	if c, ok := again.Get("6591234567"); !ok || c.Phone != "6591234567" {
		t.Fatalf("reloading must not add 91 to the stored Singapore number: %+v", c)
	}
	// An Indian contact whose last digits are the Singapore number is not taken for it.
	_ = s.Save(Contact{Phone: "+91 65912 34568", Name: "Ravi"})
	if c := s.Upsert("6591234568", "", "whatsapp"); c.Name == "Ravi" {
		t.Fatal("a Singapore sender matched an Indian contact")
	}
	if !MatchesTyped("6591234567", "6591234567") || !MatchesTyped("9876543210", "919876543210") {
		t.Fatal("typed blocked numbers must match with and without the Indian default")
	}
}

// C9: a STOP written before the customer's newer START, delivered late, is not applied.
func TestOlderStopAfterNewerStartIsIgnored(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Save(Contact{Phone: "14155552001"})
	now := time.Now()
	s.SetDND("14155552001", true, "Replied STOP", now.Add(-time.Hour))
	s.SetConsent("14155552001", true, "Replied START", now.Add(-10*time.Minute))
	c, _ := s.Get("14155552001")
	if c.OptInAt == nil || !c.OptInAt.Equal(now.Add(-10*time.Minute)) || !c.ConsentAt.Equal(*c.OptInAt) {
		t.Fatalf("the START's own time must be kept: %+v", c)
	}
	s.SetDND("14155552001", true, "Replied STOP", now.Add(-20*time.Minute))
	if c, _ := s.Get("14155552001"); c.IsDND || !c.Consent {
		t.Fatal("an older STOP arriving after a newer START must not opt them out")
	}
	s.SetDND("14155552001", true, "Replied STOP", now.Add(-time.Minute))
	if c, _ := s.Get("14155552001"); !c.IsDND || c.OptInAt != nil {
		t.Fatal("a newer STOP must opt them out")
	}
}

// C10: every way to mark do-not-message keeps when it happened.
func TestEveryOptOutRouteRecordsItsTime(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Save(Contact{Phone: "14155552101"})
	if on, _ := s.ToggleDND("14155552101"); !on {
		t.Fatal("toggle must opt out")
	}
	if c, _ := s.Get("14155552101"); c.ConsentAt == nil {
		t.Fatal("ToggleDND must record the opt-out time")
	}
	res, err := s.ImportCSV(strings.NewReader("phone,opted_out\n+14155552102,yes\n+14155552103,no\n"))
	if err != nil || res.Added != 2 {
		t.Fatalf("import: %+v %v", res, err)
	}
	if c, _ := s.Get("14155552102"); !c.IsDND || c.ConsentAt == nil {
		t.Fatalf("a CSV opt-out must record the time: %+v", c)
	}
	if _, err := s.ImportCSV(strings.NewReader("phone,opted_out\n+14155552103,yes\n")); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Get("14155552103"); !c.IsDND || c.ConsentAt == nil {
		t.Fatalf("a CSV opt-out of an existing contact must record the time: %+v", c)
	}
	_ = s.Save(Contact{Phone: "14155552104"})
	_ = s.Save(Contact{Phone: "14155552104", IsDND: true})
	if c, _ := s.Get("14155552104"); c.ConsentAt == nil {
		t.Fatal("marking do-not-message in the form must record the time")
	}
}

// C7: Meta's "stopped marketing messages" is kept on the contact until they opt in again.
func TestMarketingOptOut(t *testing.T) {
	s, _ := newStore(t)
	_ = s.Save(Contact{Phone: "14155552201", Consent: true, ConsentSource: "form"})
	if !s.SetMarketingOptOut("14155552201", time.Time{}) {
		t.Fatal("contact not found")
	}
	if c, _ := s.Get("14155552201"); !c.MarketingOptOut || c.MarketingOptOutAt == nil || !c.Consent {
		t.Fatalf("marketing opt-out not recorded: %+v", c)
	}
	_ = s.Save(Contact{Phone: "14155552201", Name: "Edited", Consent: true})
	if c, _ := s.Get("14155552201"); !c.MarketingOptOut {
		t.Fatal("editing the contact must not clear Meta's marketing opt-out")
	}
	s.SetConsent("14155552201", true, "Replied START", time.Now())
	if c, _ := s.Get("14155552201"); c.MarketingOptOut {
		t.Fatal("a new opt-in clears the marketing opt-out")
	}
}

// C11: free text stops half an hour before WhatsApp's 24 hours end.
func TestFreeTextWindowClosesEarly(t *testing.T) {
	now := time.Now()
	at := now.Add(-23*time.Hour - 45*time.Minute)
	c := Contact{LastInboundAt: &at}
	if !c.InServiceWindow(now) || c.CanSendFreeText(now) {
		t.Fatal("23h45m after the last message: still in WhatsApp's window, but no more free text")
	}
	at2 := now.Add(-23 * time.Hour)
	if !(Contact{LastInboundAt: &at2}).CanSendFreeText(now) {
		t.Fatal("23 hours after: free text is fine")
	}
}
