package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/history"
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
	defer releaseInstanceLock() // setup with migrate takes the single-copy lock, as run does
	if instanceLock == nil {
		t.Fatal("setup for run must take the single-copy lock before migrating")
	}

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
	// F13: the profile's backup copy must not keep the main number's details either.
	bak, _ := os.ReadFile(s.FilePath + ".bak")
	if strings.Contains(string(bak), "111111") || strings.Contains(string(bak), "pulse-main") || strings.Contains(string(bak), "sealed1:") {
		t.Fatalf("config-sales.yaml.bak still holds the main profile's WhatsApp details:\n%s", bak)
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

// Settings an older version kept next to the program move into the data folder, so a
// new version unzipped anywhere (even Program Files) keeps the password and connection.
func TestSettingsMoveIntoDataFolder(t *testing.T) {
	home, oldDir := t.TempDir(), t.TempDir()
	t.Setenv("PULSE_DATA_DIR", home)
	defer func() { programDir, configGiven = "", false }()
	body := []byte("server:\n    port: 18995\n    admin_password: $2a$10$abcdefghijklmnopqrstuv\n")
	for _, name := range []string{"config.yaml", "config-sales.yaml", "config.yaml.bak"} {
		if err := os.WriteFile(filepath.Join(oldDir, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	wd, _ := os.Getwd()
	if err := os.Chdir(oldDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.String("config", "config.yaml", "")
	if got := resolveConfigPath(fs, "config.yaml"); got != filepath.Join(home, "config.yaml") {
		t.Fatalf("settings path: %s", got)
	}
	for _, name := range []string{"config.yaml", "config-sales.yaml", "config.yaml.bak"} {
		if _, err := os.Stat(filepath.Join(home, name)); err != nil {
			t.Errorf("%s not moved: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(oldDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s left in the old folder", name)
		}
	}
	if programDir != oldDir {
		t.Fatalf("older data files must still be looked for in %s, got %s", oldDir, programDir)
	}
	// Once moved, the data folder's copy wins over any copy left elsewhere.
	_ = os.WriteFile(filepath.Join(oldDir, "config.yaml"), []byte("stale"), 0600)
	if got := resolveConfigPath(fs, "config.yaml"); got != filepath.Join(home, "config.yaml") {
		t.Fatalf("second start: %s", got)
	}
}

// On a new computer the data folder does not exist yet; it is created for the settings.
func TestSettingsFolderCreatedOnFirstStart(t *testing.T) {
	home := filepath.Join(t.TempDir(), "AppData", "Pulse")
	t.Setenv("PULSE_DATA_DIR", home)
	defer func() { programDir, configGiven = "", false }()
	wd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.String("config", "config.yaml", "")
	p := resolveConfigPath(fs, "config.yaml")
	if _, err := config.LoadConfig(p); err != nil {
		t.Fatalf("first start on a new computer: %v", err)
	}
}

// Data without its settings file must not start fresh with a new one-time password.
func TestSettingsMissingWithExistingData(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PULSE_DATA_DIR", home)
	cfg := filepath.Join(home, "config.yaml")
	if settingsMissing(cfg) {
		t.Fatal("a first start with no data is fine")
	}
	_ = os.WriteFile(filepath.Join(home, "store.key"), []byte("k"), 0600)
	if !settingsMissing(cfg) {
		t.Fatal("data without settings must be refused")
	}
	_ = os.WriteFile(cfg, []byte("server: {}\n"), 0600)
	if settingsMissing(cfg) {
		t.Fatal("data with settings is fine")
	}
}

// No command may create an empty settings file next to existing data: the next start
// would then hand out a new one-time password. The test runs setup in a child process
// because fail exits.
func TestEveryCommandRefusesMissingSettings(t *testing.T) {
	if os.Getenv("PULSE_TEST_SETUP_CHILD") == "1" {
		setup(filepath.Join(os.Getenv("PULSE_DATA_DIR"), "config.yaml"), "", 18994, false)
		os.Exit(0)
	}
	home := t.TempDir()
	_ = os.WriteFile(filepath.Join(home, "store.key"), []byte("k"), 0600)
	cmd := exec.Command(os.Args[0], "-test.run=^TestEveryCommandRefusesMissingSettings$")
	cmd.Env = append(os.Environ(), "PULSE_TEST_SETUP_CHILD=1", "PULSE_DATA_DIR="+home)
	out, err := cmd.CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("settings file (config.yaml) is missing")) {
		t.Fatalf("setup must refuse: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(home, "config.yaml")); !os.IsNotExist(err) {
		t.Fatal("no settings file may be created")
	}
}

// Only Pulse's own settings are moved: never config.example.yaml or another program's file.
func TestOnlyPulseSettingsAreMoved(t *testing.T) {
	home, oldDir := t.TempDir(), t.TempDir()
	t.Setenv("PULSE_DATA_DIR", home)
	defer func() { programDir, configGiven = "", false }()
	foreign := []byte("server:\n    port: 3000\n") // another program's config.yaml
	_ = os.WriteFile(filepath.Join(oldDir, "config.yaml"), foreign, 0600)
	_ = os.WriteFile(filepath.Join(oldDir, "config.example.yaml"), []byte("x: 1\n"), 0600)
	wd, _ := os.Getwd()
	if err := os.Chdir(oldDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.String("config", "config.yaml", "")
	resolveConfigPath(fs, "config.yaml")
	for _, name := range []string{"config.yaml", "config.example.yaml"} {
		if _, err := os.Stat(filepath.Join(oldDir, name)); err != nil {
			t.Errorf("%s must be left where it was: %v", name, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(oldDir, "config.yaml")); string(data) != string(foreign) {
		t.Fatal("another program's config.yaml must not be rewritten")
	}
}

// D35: a byte-order mark, oversized fields and path-like ids from an old file.
func TestCleanLegacyJSON(t *testing.T) {
	in := append([]byte("\xef\xbb\xbf"), []byte(`[{"id":"welcome","name":"`+strings.Repeat("x", 5000)+`"},{"id":"../../evil","name":"bad"}]`)...)
	out, dropped, err := cleanLegacyJSON(in)
	if err != nil || dropped != 1 {
		t.Fatalf("err %v dropped %d", err, dropped)
	}
	var items []map[string]any
	if json.Unmarshal(out, &items) != nil || len(items) != 1 || len(items[0]["name"].(string)) != 200 {
		t.Fatalf("not cleaned: %s", out[:80])
	}
	if _, _, err := cleanLegacyJSON([]byte("not json")); err == nil {
		t.Fatal("a damaged file must not be imported")
	}
}

type fakeCampaigns struct {
	mu     sync.Mutex
	events []string
}

func (f *fakeCampaigns) OnFailed(id, reason string) {
	f.mu.Lock()
	f.events = append(f.events, "failed:"+id)
	f.mu.Unlock()
}

func (f *fakeCampaigns) OnReceipt(ids []string, status string) {
	f.mu.Lock()
	f.events = append(f.events, status+":"+ids[0])
	f.mu.Unlock()
}

func TestReceiptBeforeSendAnswerIsNotLost(t *testing.T) {
	hs, err := history.NewStore(filepath.Join(t.TempDir(), "messages.json"), 100)
	if err != nil {
		t.Fatal(err)
	}
	camps := &fakeCampaigns{}
	handle := receiptHandler(hs, camps, 20*time.Millisecond)
	// Meta's "failed" arrives before Pulse has recorded the message it sent.
	handle(cloudapi.Receipt{MessageID: "wamid.EARLY", Status: "failed", Error: "Not on WhatsApp"})
	hs.Record(history.MessageRecord{ID: "wamid.EARLY", Direction: history.DirectionOutgoing, Phone: "919800000002", Text: "hi", Status: history.StatusSent})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if m, _ := hs.Get("wamid.EARLY"); m.Status == history.StatusFailed && m.Error == "Not on WhatsApp" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("an early failure receipt must still mark the message as failed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	camps.mu.Lock()
	defer camps.mu.Unlock()
	if !slices.Equal(camps.events, []string{"failed:wamid.EARLY"}) {
		t.Fatalf("campaigns must get the receipt once: %v", camps.events)
	}
}

type fakeLabels struct {
	prefix string
	got    []string
}

func (f *fakeLabels) Reconcile(label, messageID, status string) bool {
	if !strings.HasPrefix(label, f.prefix) {
		return false
	}
	f.got = append(f.got, label+"|"+messageID+"|"+status)
	return true
}

// A labelled receipt reaches the owner of the label (and only it) before the history.
func TestLabelledReceiptReachesItsOwner(t *testing.T) {
	hs, _ := history.NewStore(filepath.Join(t.TempDir(), "messages.json"), 100)
	camps, sched := &fakeLabels{prefix: "pulse:c:"}, &fakeLabels{prefix: "pulse:s:"}
	handle := receiptHandler(hs, &fakeCampaigns{}, time.Millisecond, camps, sched)
	handle(cloudapi.Receipt{MessageID: "wamid.Q", Status: "delivered", CallbackData: "pulse:s:job7"})
	handle(cloudapi.Receipt{MessageID: "wamid.R", Status: "read"})
	if len(camps.got) != 0 || !slices.Equal(sched.got, []string{"pulse:s:job7|wamid.Q|delivered"}) {
		t.Fatalf("campaigns %v, scheduler %v", camps.got, sched.got)
	}
}
