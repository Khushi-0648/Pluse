package campaign

import (
	"os"
	"testing"
)

func TestLoadContactsFromCSV(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "contacts_*.csv")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	csvContent := `phone,name,company
+14155552671,Alice,Acme Corp
+14155552672,Bob,Tech Inc
`
	if _, err := tmpFile.WriteString(csvContent); err != nil {
		t.Fatalf("Failed to write temp csv: %v", err)
	}
	tmpFile.Close()

	contacts, err := LoadContactsFromCSV(tmpFile.Name())
	if err != nil {
		t.Fatalf("LoadContactsFromCSV failed: %v", err)
	}

	if len(contacts) != 2 {
		t.Fatalf("Expected 2 contacts, got %d", len(contacts))
	}

	if contacts[0].Name != "Alice" || contacts[0].Phone != "+14155552671" || contacts[0].Variables["company"] != "Acme Corp" {
		t.Fatalf("Contact 0 mismatch: %+v", contacts[0])
	}

	if contacts[1].Name != "Bob" || contacts[1].Phone != "+14155552672" || contacts[1].Variables["company"] != "Tech Inc" {
		t.Fatalf("Contact 1 mismatch: %+v", contacts[1])
	}
}
