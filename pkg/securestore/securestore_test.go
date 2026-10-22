package securestore

import (
	"runtime"
	"strings"
	"testing"
)

func TestSealStringRoundTripAndRefusesPlainText(t *testing.T) {
	sealed, err := SealString("EAAG-secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealedString(sealed) || (runtime.GOOS == "windows" && strings.Contains(sealed, "secret-token")) {
		t.Fatalf("not sealed: %q", sealed)
	}
	if got, err := OpenString(sealed); err != nil || got != "EAAG-secret-token" {
		t.Fatalf("open = %q, %v", got, err)
	}
	if _, err := OpenString("EAAG-plain"); err == nil {
		t.Fatal("a plain value must not be accepted as a sealed secret")
	}
	if s, err := SealString(""); err != nil || s != "" {
		t.Fatal("an empty secret stays empty")
	}
}
