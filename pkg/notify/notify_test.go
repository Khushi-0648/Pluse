package notify

import (
	"testing"
)

func TestDesktopNotifier(t *testing.T) {
	enabled := true
	d := NewDesktop(func() bool { return enabled })

	// Test when enabled
	d.Notify("Test Title", "Test Body")

	// Test when disabled
	enabled = false
	d.Notify("Test Title", "Test Body")

	// Test explicit test
	if err := d.Test(); err != nil {
		t.Fatalf("Test() returned error: %v", err)
	}
}
