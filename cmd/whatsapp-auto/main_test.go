package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/securestore"
)

func writeLegacy(t *testing.T, dir string) {
	t.Helper()
	files := map[string]string{
		"contacts.json":     `[{"phone":"919876543210","name":"Asha"}]`,
		"contacts.json.bak": `[{"phone":"919876543210","name":"Asha (backup)"}]`,
		"messages.json":     `[{"id":"a","direction":"incoming","sender_phone":"919876543210","text":"hello"}]`,
		"whatsapp.db":       "sqlite-session",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMigrationKeepsOriginalsAndIgnoresCurrentDirectory(t *testing.T) {
	appDir, dataDir, otherDir := t.TempDir(), t.TempDir(), t.TempDir()
	writeLegacy(t, appDir)
	writeLegacy(t, otherDir)
	t.Setenv("PULSE_DATA_DIR", dataDir)
	defer securestore.SetDefault(nil)

	wd, _ := os.Getwd()
	if err := os.Chdir(otherDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	e := setup(filepath.Join(appDir, "config.yaml"), "", 18999, true)

	for _, name := range []string{"contacts.json", "contacts.json.bak", "messages.json", "whatsapp.db"} {
		if _, err := os.Stat(filepath.Join(otherDir, name)); err != nil {
			t.Fatalf("%s in the current directory must not be touched: %v", name, err)
		}
	}
	data, err := e.box.ReadFile(e.path("contacts"))
	if err != nil || !bytes.Contains(data, []byte(`"Asha"`)) {
		t.Fatalf("contacts not imported: %v %s", err, data)
	}
	raw, _ := os.ReadFile(e.path("contacts"))
	if !securestore.IsEncrypted(raw) {
		t.Fatal("imported contacts must be encrypted")
	}
	backups, _ := filepath.Glob(filepath.Join(dataDir, "old-version-backup", "*", "contacts.json*"))
	if len(backups) != 2 {
		t.Fatalf("originals must be moved to the backup folder, found %v", backups)
	}
	if b, _ := os.ReadFile(filepath.Join(filepath.Dir(backups[0]), "contacts.json")); string(b) != `[{"phone":"919876543210","name":"Asha"}]` {
		t.Fatalf("backup content changed: %s", b)
	}
	if _, err := os.Stat(filepath.Join(appDir, "contacts.json")); !os.IsNotExist(err) {
		t.Fatal("plaintext original should have left the program folder")
	}
	// The old linked-device session is not used any more: it is left where it is and
	// only offered for deletion in Settings > Privacy.
	if b, _ := os.ReadFile(filepath.Join(appDir, "whatsapp.db")); string(b) != "sqlite-session" {
		t.Fatal("the old session file must not be touched automatically")
	}
	if !slices.Contains(e.oldSession, filepath.Join(appDir, "whatsapp.db")) || !slices.Contains(e.oldSession, filepath.Join(dataDir, "whatsapp.db.sealed")) {
		t.Fatalf("old session files not listed: %v", e.oldSession)
	}
	cfgText, _ := os.ReadFile(filepath.Join(appDir, "config.yaml"))
	if bytes.Contains(cfgText, []byte("18999")) {
		t.Fatal("the -port override must not be saved into config.yaml")
	}
}

func TestNoMigrationWhenDisabled(t *testing.T) {
	appDir, dataDir := t.TempDir(), t.TempDir()
	writeLegacy(t, appDir)
	t.Setenv("PULSE_DATA_DIR", dataDir)
	defer securestore.SetDefault(nil)

	e := setup(filepath.Join(appDir, "config.yaml"), "", 18998, false)
	if _, err := os.Stat(filepath.Join(appDir, "contacts.json")); err != nil {
		t.Fatal("nothing may move when migration is off")
	}
	if e.cfg.Snapshot().ListenPort() != 18998 {
		t.Fatal("the -port override must apply")
	}
}

// A second profile (-session sales) starts as a copy of the main settings, but never
// with the main number's WhatsApp Business API details: each connects its own.
func TestSessionProfileStartsWithoutCloudCredentials(t *testing.T) {
	appDir, dataDir := t.TempDir(), t.TempDir()
	t.Setenv("PULSE_DATA_DIR", dataDir)
	defer securestore.SetDefault(nil)
	main := filepath.Join(appDir, "config.yaml")
	cfg, err := config.LoadConfig(main)
	if err != nil {
		t.Fatal(err)
	}
	_ = cfg.Update(func(c *config.Config) error {
		c.Business.Name = "Asha Stores"
		c.CloudAPI.PhoneNumberID, c.CloudAPI.AccessToken, c.CloudAPI.VerifyToken = "111111", "EAAG-main", "pulse-main"
		return nil
	})
	e := setup(main, "sales", 18997, false)
	s := e.cfg.Snapshot()
	if s.Business.Name != "Asha Stores" || s.CloudAPI.Ready() || s.CloudAPI.VerifyToken != "" || filepath.Base(s.FilePath) != "config-sales.yaml" {
		t.Fatalf("profile copy wrong: %+v %s", s.CloudAPI, s.FilePath)
	}
	if m, _ := config.LoadConfig(main); !m.Snapshot().CloudAPI.Ready() {
		t.Fatal("the main profile must keep its details")
	}
}

// fail exits without running deferred calls, so cleanups registered with atFail run
// first, newest first.
func TestFailCleanupsRunNewestFirst(t *testing.T) {
	var order []string
	atFail(func() { order = append(order, "seal") })
	atFail(func() { order = append(order, "save") })
	atFail(func() { order = append(order, "stop") })
	runFailCleanups()
	if len(order) != 3 || order[0] != "stop" || order[2] != "seal" {
		t.Fatalf("wrong cleanup order: %v", order)
	}
	runFailCleanups()
	if len(order) != 3 {
		t.Fatal("cleanups must run only once")
	}
}

func TestGraphOverrideOnlyForLoopback(t *testing.T) {
	for u, want := range map[string]bool{"http://127.0.0.1:9999": true, "http://[::1]:80": true, "https://graph.example.com": false, "http://10.0.0.5": false, "ftp://127.0.0.1": false} {
		if loopbackURL(u) != want {
			t.Errorf("%s: want %v", u, want)
		}
	}
	t.Setenv("PULSE_GRAPH_BASE_URL", "https://evil.example.com")
	if c := newCloud(config.DefaultConfig()); c.BaseURL != "https://graph.facebook.com" {
		t.Fatal("a non-loopback override must be ignored")
	}
}
