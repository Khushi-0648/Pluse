package securestore

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
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

// Two copies starting at the same moment must end up with the same data key (B5).
func TestOpenBoxConcurrentCreationSharesOneKey(t *testing.T) {
	dir := t.TempDir()
	const n = 8
	boxes := make([]*Box, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			boxes[i], errs[i] = OpenBox(dir)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("OpenBox %d: %v", i, err)
		}
	}
	path := filepath.Join(dir, "contacts.json")
	if err := boxes[0].WriteFile(path, []byte(`[1]`)); err != nil {
		t.Fatal(err)
	}
	for i, b := range boxes {
		if got, err := b.ReadFile(path); err != nil || string(got) != `[1]` {
			t.Fatalf("box %d cannot read box 0's file: %q, %v", i, got, err)
		}
	}
	if later, err := OpenBox(dir); err != nil {
		t.Fatal(err)
	} else if got, err := later.ReadFile(path); err != nil || string(got) != `[1]` {
		t.Fatalf("reopened box: %q, %v", got, err)
	}
}

// N8: an empty store.key left by a crash is replaced when no encrypted data exists, and
// refused with instructions when it does (a new key could never open that data).
func TestStaleEmptyKeyReplacedOnlyWithoutData(t *testing.T) {
	old := time.Now().Add(-time.Minute)

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "store.key")
	if err := os.WriteFile(keyPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(keyPath, old, old); err != nil {
		t.Fatal(err)
	}
	// Plain (unencrypted) files do not count as data a new key would lose.
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server: {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := OpenBox(dir)
	if err != nil {
		t.Fatalf("a stale empty key without data must be recreated: %v", err)
	}
	if fi, err := os.Stat(keyPath); err != nil || fi.Size() < 32 {
		t.Fatalf("new key file: %v, %v", fi, err)
	}
	if left, _ := filepath.Glob(keyPath + ".empty-*"); len(left) != 0 {
		t.Fatalf("the stale file must be removed: %v", left)
	}
	p := filepath.Join(dir, "contacts.json")
	if err := b.WriteFile(p, []byte(`[2]`)); err != nil {
		t.Fatal(err)
	}
	if again, err := OpenBox(dir); err != nil {
		t.Fatal(err)
	} else if got, err := again.ReadFile(p); err != nil || string(got) != `[2]` {
		t.Fatalf("reopened: %q, %v", got, err)
	}

	// With encrypted data (here in a subfolder) the empty key is kept and start-up stops.
	dir2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir2, "backups"), 0700); err != nil {
		t.Fatal(err)
	}
	enc, _ := os.ReadFile(p)
	if err := os.WriteFile(filepath.Join(dir2, "backups", "contacts.json"), enc, 0600); err != nil {
		t.Fatal(err)
	}
	key2 := filepath.Join(dir2, "store.key")
	if err := os.WriteFile(key2, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(key2, old, old); err != nil {
		t.Fatal(err)
	}
	_, err = OpenBox(dir2)
	if err == nil || !strings.Contains(err.Error(), "restore store.key from a backup") {
		t.Fatalf("expected a restore-from-backup error, got %v", err)
	}
	if fi, err := os.Stat(key2); err != nil || fi.Size() != 0 {
		t.Fatalf("the empty key must be left in place: %v, %v", fi, err)
	}
}

// New files are bound to their name; files written the old way still open (B17).
func TestFileNameBoundAndOldFormatStillOpens(t *testing.T) {
	dir := t.TempDir()
	b, err := OpenBox(dir)
	if err != nil {
		t.Fatal(err)
	}
	a, c := filepath.Join(dir, "contacts.json"), filepath.Join(dir, "messages.json")
	if err := b.WriteFile(a, []byte(`"contacts"`)); err != nil {
		t.Fatal(err)
	}
	// Swapping one file for another must not be accepted.
	raw, _ := os.ReadFile(a)
	if err := os.WriteFile(c, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReadFile(c); err == nil {
		t.Fatal("a file copied to another name must not open")
	}
	// The .bak copy carries the name of the file it backs up.
	if err := b.WriteFile(a, []byte(`"second"`)); err != nil {
		t.Fatal(err)
	}
	if got, err := b.ReadBackup(a); err != nil || string(got) != `"contacts"` {
		t.Fatalf("backup = %q, %v", got, err)
	}
	// A file sealed by an older version (additional data = header only).
	nonce := make([]byte, b.aead.NonceSize())
	old := append(append([]byte(nil), magic...), nonce...)
	old = b.aead.Seal(old, nonce, []byte(`"old"`), magic)
	oldPath := filepath.Join(dir, "templates.json")
	if err := os.WriteFile(oldPath, old, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := b.ReadFile(oldPath); err != nil || string(got) != `"old"` {
		t.Fatalf("old-format file = %q, %v", got, err)
	}
	// Damaged data is still refused.
	old[len(old)-1] ^= 1
	_ = os.WriteFile(oldPath, old, 0600)
	if _, err := b.readOne(oldPath, "templates.json"); err == nil {
		t.Fatal("a damaged file must not open")
	}
}
