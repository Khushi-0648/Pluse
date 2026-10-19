package contacts

import (
	"bytes"
	"os"
	"testing"
	"time"
)

func TestContactsStore(t *testing.T) {
	tempFile := "test_contacts_" + time.Now().Format("150405") + ".json"
	defer os.Remove(tempFile)

	store, err := NewStore(tempFile)
	if err != nil {
		t.Fatalf("Failed to create contacts store: %v", err)
	}

	c := Contact{
		Phone: "+14155552671",
		Name:  "Test Alice",
		Email: "alice@test.com",
		Tags:  []string{"VIP", "Beta"},
	}

	if err := store.Save(c); err != nil {
		t.Fatalf("Failed to save contact: %v", err)
	}

	retrieved, found := store.Get("14155552671")
	if !found {
		t.Fatalf("Contact not found with clean phone lookup")
	}
	if retrieved.Name != "Test Alice" {
		t.Errorf("Expected name 'Test Alice', got '%s'", retrieved.Name)
	}

	// Test Bot Pause (C4)
	store.PauseBot("14155552671", 1*time.Hour)
	if !store.IsBotPaused("14155552671") {
		t.Errorf("Expected bot to be paused for contact")
	}

	store.ResumeBot("14155552671")
	if store.IsBotPaused("14155552671") {
		t.Errorf("Expected bot to be resumed after ResumeBot")
	}

	// Test ExportCSV (C13)
	var buf bytes.Buffer
	if err := store.ExportCSV(&buf); err != nil {
		t.Fatalf("Failed to export CSV: %v", err)
	}
	if !bytes.Contains(buf.Bytes(), []byte("14155552671")) {
		t.Errorf("Expected CSV output to contain phone 14155552671")
	}
}

func TestContactsLIDResolution(t *testing.T) {
	tempFile := "test_contacts_lid_" + time.Now().Format("150405.000") + ".json"
	defer os.Remove(tempFile)

	store, err := NewStore(tempFile)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// 1. Test IsLID helper
	if !IsLID("245058100052033") {
		t.Errorf("Expected 245058100052033 to be recognized as LID")
	}
	if !IsLID("+172361668141288") {
		t.Errorf("Expected +172361668141288 to be recognized as LID")
	}
	if IsLID("919971731137") {
		t.Errorf("Expected standard 12-digit Indian number not to be LID")
	}
	if IsLID("Alice Smith") {
		t.Errorf("Expected text name not to be LID")
	}

	// 2. Save an LID contact
	_ = store.Save(Contact{
		Phone: "245058100052033",
		Name:  "+245058100052033",
	})

	// 3. Run FixPhoneNumbers with resolver
	resolver := func(target string) string {
		if target == "245058100052033" {
			return "919971731137"
		}
		return target
	}
	nameResolver := func(target string) string {
		if target == "919971731137" {
			return "Sparsh Gupta Techpros"
		}
		return ""
	}

	store.FixPhoneNumbers(resolver, nameResolver)

	// Verify LID is deleted
	if _, found := store.Get("245058100052033"); found {
		t.Errorf("Expected raw LID phone 245058100052033 to be removed from store")
	}

	// Verify resolved contact exists with real name
	c, found := store.Get("919971731137")
	if !found {
		t.Fatalf("Expected resolved contact 919971731137 to exist in store")
	}
	if c.Name != "Sparsh Gupta Techpros" {
		t.Errorf("Expected contact name 'Sparsh Gupta Techpros', got '%s'", c.Name)
	}
}

func TestImportCSV_GranularMetrics_C19(t *testing.T) {
	tempFile := "test_contacts_csv_" + time.Now().Format("150405.000") + ".json"
	defer os.Remove(tempFile)

	store, err := NewStore(tempFile)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// 1. Initial pre-existing contact
	_ = store.Save(Contact{
		Phone: "14155551111",
		Name:  "Original Name",
	})

	// CSV content with:
	// - Header
	// - 1 new valid contact (Added)
	// - 1 existing duplicate contact (Updated)
	// - 1 invalid phone (<7 digits) (Skipped)
	// - 1 empty row (Skipped)
	csvContent := `phone,name,email,tags,notes
+14155552222,New Contact,new@example.com,VIP;Lead,A new lead
+14155551111,Updated Name,updated@example.com,Customer,Updated note
123,Invalid Phone,bad@example.com,,
,,,`

	res, err := store.ImportCSV(bytes.NewBufferString(csvContent))
	if err != nil {
		t.Fatalf("ImportCSV failed: %v", err)
	}

	if res.Added != 1 {
		t.Errorf("Expected Added=1, got %d", res.Added)
	}
	if res.Updated != 1 {
		t.Errorf("Expected Updated=1, got %d", res.Updated)
	}
	if res.Skipped != 2 {
		t.Errorf("Expected Skipped=2, got %d", res.Skipped)
	}
	if res.Total != 4 {
		t.Errorf("Expected Total=4, got %d", res.Total)
	}
	if len(res.Warnings) != 2 {
		t.Errorf("Expected 2 warnings, got %d: %v", len(res.Warnings), res.Warnings)
	}

	// Verify the updated contact has the new name
	c, found := store.Get("14155551111")
	if !found {
		t.Fatalf("Expected contact 14155551111 to exist")
	}
	if c.Name != "Updated Name" {
		t.Errorf("Expected name 'Updated Name', got %q", c.Name)
	}
}

