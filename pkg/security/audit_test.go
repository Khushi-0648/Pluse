package security

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"whatsapp-automation/pkg/securestore"
)

// B11: an activity log that cannot be read is kept aside, not overwritten by the next save.
func TestUnreadableAuditLogIsSetAside(t *testing.T) {
	saved := audit
	audit = &auditLogger{}
	defer func() { audit = saved }()

	dir := t.TempDir()
	box, err := securestore.OpenBox(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit.json")
	damaged := []byte("PULSEENC1\n0123456789abcdefghijklmnopqrstuvwxyz")
	for _, p := range []string{path, path + ".bak"} {
		if err := os.WriteFile(p, damaged, 0600); err != nil {
			t.Fatal(err)
		}
	}
	OpenAuditLog(path, box)
	LogAudit("TEST", "local", "system", "after", "success")
	FlushAuditLog()

	entries, _ := os.ReadDir(dir)
	aside := 0
	for _, e := range entries {
		if strings.Contains(e.Name(), ".unreadable-") {
			aside++
			if b, _ := os.ReadFile(filepath.Join(dir, e.Name())); string(b) != string(damaged) {
				t.Fatalf("%s was changed", e.Name())
			}
		}
	}
	if aside != 2 {
		t.Fatalf("audit.json and its backup must both be kept aside, got %d files", aside)
	}
	data, err := box.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `"TEST"`) {
		t.Fatalf("a new log must be started: %q, %v", data, err)
	}
}

// A readable log is loaded and nothing is moved.
func TestReadableAuditLogIsKept(t *testing.T) {
	saved := audit
	audit = &auditLogger{}
	defer func() { audit = saved }()

	dir := t.TempDir()
	box, err := securestore.OpenBox(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "audit.json")
	if err := box.WriteFile(path, []byte(`[{"action":"OLD"}]`)); err != nil {
		t.Fatal(err)
	}
	OpenAuditLog(path, box)
	if ev := audit.events; len(ev) != 1 || ev[0].Action != "OLD" {
		t.Fatalf("events = %+v", ev)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".unreadable-") {
			t.Fatalf("a readable log must not be moved: %s", e.Name())
		}
	}
}
