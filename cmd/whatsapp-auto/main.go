package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	// Windows has no zone database Go can read: without this every named business-hours
	// time zone (Asia/Kolkata, Europe/London...) fails and Pulse falls back to the PC clock.
	_ "time/tzdata"

	"whatsapp-automation/pkg/bot"
	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/desktop"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/notify"
	"whatsapp-automation/pkg/scheduler"
	"whatsapp-automation/pkg/securestore"
	"whatsapp-automation/pkg/security"
	"whatsapp-automation/pkg/server"
	"whatsapp-automation/pkg/templates"
	"whatsapp-automation/pkg/version"
	"whatsapp-automation/pkg/webhook"

	"gopkg.in/yaml.v3"
)

func main() {
	cmd := "app"
	args := os.Args[1:]
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	cfgPath := fs.String("config", "config.yaml", "configuration file")
	session := fs.String("session", "", "separate profile for a second number, for example: sales")
	port := fs.Int("port", 0, "dashboard port (default 8085)")
	to := fs.String("to", "", "recipient phone number with country code (send)")
	msg := fs.String("msg", "", "message text (send)")
	keepRunning := fs.Bool("keep-running", false, "start again automatically after a crash (serve)")
	_ = fs.Parse(args)
	*cfgPath = resolveConfigPath(fs, *cfgPath)
	cliPort = *port // every command (stop, send, status...) talks to the copy on this port
	// "Start with Windows" must start this same profile, not always the main one.
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "session":
			desktop.Profile.Name = sessionRe.ReplaceAllString(f.Value.String(), "")
			desktop.Profile.Args = append(desktop.Profile.Args, "-session", desktop.Profile.Name)
		case "config":
			if abs, err := filepath.Abs(*cfgPath); err == nil {
				desktop.Profile.Args = append(desktop.Profile.Args, "-config", abs)
			}
		case "port":
			desktop.Profile.Args = append(desktop.Profile.Args, "-port", f.Value.String())
		}
	})

	switch cmd {
	case "app":
		run(*cfgPath, *session, *port, true)
	case "serve", "server":
		if *keepRunning {
			supervise(fs, *cfgPath)
			return
		}
		run(*cfgPath, *session, *port, false)
	case "send":
		if *to == "" || *msg == "" {
			fmt.Println("Usage: whatsapp-auto send -to +919876543210 -msg \"Hello\"")
			os.Exit(2)
		}
		runSend(*cfgPath, *session, *to, *msg)
	case "pair":
		fmt.Printf("%s now uses Meta's official WhatsApp Business API, so there is no phone to pair.\nEnter your own Business API details in the dashboard: Settings > WhatsApp connection.\n", version.Product)
		os.Exit(2)
	case "status":
		runStatus(*cfgPath, *session)
	case "disconnect", "unlink", "logout":
		runDisconnect(*cfgPath, *session)
	case "reset-password":
		runResetPassword(*cfgPath, *session)
	case "stop":
		runStop(*cfgPath, *session)
	case "shortcut":
		if err := desktop.EnsureDesktopShortcut(""); err != nil {
			fmt.Println("Could not create the shortcut:", err)
			os.Exit(1)
		}
		fmt.Println("Desktop shortcut created.")
	case "version", "-v", "--version":
		fmt.Printf("%s %s\n", version.Product, version.Version)
	case "help", "-h", "--help":
		printHelp()
	default:
		fmt.Printf("Unknown command %q. Run \"whatsapp-auto help\".\n", cmd)
		os.Exit(2)
	}
}

// configGiven is true when -config was passed explicitly; cliPort holds -port.
var (
	configGiven bool
	cliPort     int
)

// sessionConfig gives each extra number (-session sales) its own settings file next to
// the main one, so two running copies never overwrite each other's settings. The first
// time, it starts as a copy of the main settings (rules, business details, password);
// fresh reports that, so the copy's WhatsApp Business API details can be cleared: every
// profile connects with its own number and token.
func sessionConfig(mainPath, suffix string) (path string, fresh bool) {
	if suffix == "" || configGiven {
		return mainPath, false
	}
	p := filepath.Join(filepath.Dir(mainPath), "config"+suffix+".yaml")
	if _, err := os.Stat(p); os.IsNotExist(err) {
		if data, err := os.ReadFile(mainPath); err == nil {
			fresh = os.WriteFile(p, data, 0600) == nil
		}
	}
	return p, fresh
}

// programDir is where an older version kept config.yaml (and, before 1.4, plaintext data
// files). It is set only when the settings are not given with -config.
var programDir string

// allowNewSettings lets reset-password (which needs administrator rights) start over
// with new settings when the old ones are lost.
var allowNewSettings bool

// resolveConfigPath picks the settings file. Without -config it lives in the data folder
// (%APPDATA%\Pulse), so Pulse runs from any folder, including Program Files, and a new
// version unzipped into another folder keeps the password, settings and connection. A
// config.yaml that an older version left in the current folder or next to the program
// is moved there, unless that older version is still running.
func resolveConfigPath(fs *flag.FlagSet, p string) string {
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == "config" })
	configGiven = given
	if given {
		return p
	}
	home := defaultDataDir("")
	target := filepath.Join(home, "config.yaml")
	var legacy []string
	if abs, err := filepath.Abs(p); err == nil {
		legacy = append(legacy, abs)
	}
	if exe, err := os.Executable(); err == nil {
		programDir = filepath.Dir(exe)
		legacy = append(legacy, filepath.Join(programDir, "config.yaml"))
	}
	if _, err := os.Stat(target); err == nil {
		return target
	}
	// First start of this version (or on this computer): create the data folder and hold
	// the settings lock while looking for old settings, so two copies started together
	// never both move them, or create new settings while the other moves old ones (N6).
	if err := os.MkdirAll(home, 0700); err == nil {
		securestore.RestrictToUser(home)
	}
	unlock := lockConfigDir(home)
	defer unlock()
	if _, err := os.Stat(target); err == nil {
		return target // another copy moved or created them while this one waited
	}
	for _, old := range legacy {
		if strings.EqualFold(old, target) {
			continue
		}
		if _, err := os.Stat(old); err != nil {
			continue
		}
		// Only read here, never loaded or rewritten: an older version may still be
		// running and writing this file.
		// Every start of Pulse stores the admin password hash under server.admin_password.
		probe, ok := readSettingsProbe(old)
		if !ok || probe.Server.AdminPassword == "" {
			continue // another program's config.yaml: never load, rewrite or move it
		}
		programDir = filepath.Dir(old)
		if portInUse("127.0.0.1", probe.port()) {
			return old // the older version is running and still writes this file
		}
		if err := moveSettings(programDir, home); err != nil {
			fmt.Printf("[Settings] Could not move the settings to %s: %v. Using %s.\n", home, err, old)
			return old
		}
		fmt.Printf("[Settings] Moved the settings from %s to %s.\n", programDir, home)
		return target
	}
	return target
}

// settingsProbe is the part of a settings file Pulse reads without loading it (loading
// may rewrite the file).
type settingsProbe struct {
	Server struct {
		Port          int    `yaml:"port"`
		AdminPassword string `yaml:"admin_password"`
	} `yaml:"server"`
	Storage struct {
		DataDir string `yaml:"data_dir"`
	} `yaml:"storage"`
}

func (p settingsProbe) port() int {
	if p.Server.Port <= 0 || p.Server.Port > 65535 {
		return config.DefaultConfig().Server.Port
	}
	return p.Server.Port
}

// readSettingsProbe reads path without changing it. ok is false when the file cannot be
// read or is not YAML.
func readSettingsProbe(path string) (p settingsProbe, ok bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return p, false
	}
	return p, yaml.Unmarshal(data, &p) == nil
}

// moveSettings copies config.yaml, the -session profiles and their .bak copies from src
// into dst, checks each copy, and only then removes the originals.
func moveSettings(src, dst string) error {
	// Only Pulse's own files: config.yaml and the -session profiles (config-<name>.yaml),
	// never config.example.yaml or another program's config*.yaml in the same folder.
	var names []string
	for _, pattern := range []string{"config.yaml", "config-*.yaml", "config.yaml.bak", "config-*.yaml.bak"} {
		m, _ := filepath.Glob(filepath.Join(src, pattern))
		names = append(names, m...)
	}
	if err := os.MkdirAll(dst, 0700); err != nil {
		return err
	}
	securestore.RestrictToUser(dst)
	for _, from := range names {
		data, err := os.ReadFile(from)
		if err != nil {
			return err
		}
		to := filepath.Join(dst, filepath.Base(from))
		if _, err := os.Stat(to); err == nil {
			continue // never overwrite settings that are already there
		}
		if err := os.WriteFile(to, data, 0600); err != nil {
			return err
		}
		if back, err := os.ReadFile(to); err != nil || !bytes.Equal(back, data) {
			return fmt.Errorf("the copy of %s did not read back correctly", filepath.Base(from))
		}
	}
	for _, from := range names {
		_ = os.Remove(from) // a read-only program folder keeps a stale copy; it is ignored
	}
	return nil
}

// settingsMissing reports data from an earlier start without its settings file. Starting
// anyway would create a new one-time password and hand the old data to whoever runs it.
// This holds with -config too: a new settings file there would use the same data folder.
// An empty settings file without a usable backup (a power cut during a save, say) counts
// as missing too (B8).
func settingsMissing(cfgPath string) bool {
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) && !settingsEmpty(cfgPath) {
		return false
	}
	return config.HasDataKey(defaultDataDir(""))
}

// settingsEmpty reports a settings file that holds nothing, with no usable backup either.
func settingsEmpty(cfgPath string) bool {
	data, err := os.ReadFile(cfgPath)
	if err != nil || len(bytes.TrimSpace(data)) > 0 {
		return false
	}
	bak, err := os.ReadFile(cfgPath + ".bak")
	return err != nil || len(bytes.TrimSpace(bak)) == 0
}

// supervise runs "serve" as a child process and starts it again whenever it stops with
// an error (a crash or a forced stop). A normal stop ("whatsapp-auto stop") or Ctrl+C
// ends both. Task Scheduler cannot do this: its restart setting only covers a launch
// that fails, not a program that stops later.
func supervise(fs *flag.FlagSet, cfgPath string) {
	if settingsMissing(cfgPath) {
		setup(cfgPath, "", 0, false) // explains what to do and exits
	}
	exe, err := os.Executable()
	if err != nil {
		fail("%v", err)
	}
	args := []string{"serve"}
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "keep-running" {
			args = append(args, "-"+f.Name+"="+f.Value.String())
		}
	})
	var stopping atomic.Bool
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; stopping.Store(true) }()
	delay := 5 * time.Second
	quickFails := 0
	for {
		start := time.Now()
		cmd := exec.Command(exe, args...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if err == nil || stopping.Load() {
			return
		}
		if !errors.As(err, &exit) {
			fail("Could not start %s: %v", version.Product, err)
		}
		if time.Since(start) > 10*time.Minute {
			delay = 5 * time.Second
		}
		// Failing again and again right at start-up is a problem a restart cannot fix
		// (for example a broken settings file); the reason is in the output above.
		if time.Since(start) < 30*time.Second {
			quickFails++
		} else {
			quickFails = 0
		}
		if quickFails >= 5 {
			fail("%s stopped 5 times in a row right after starting. Fix the problem shown above, then start it again.", version.Product)
		}
		fmt.Printf("[Restart] %s stopped unexpectedly (%v). Starting it again in %s.\n", version.Product, err, delay)
		time.Sleep(delay)
		delay = min(delay*2, time.Minute)
	}
}

func printHelp() {
	fmt.Printf(`%s %s - %s

Usage: whatsapp-auto [command] [flags]

Commands:
  app        Start and open the dashboard window (default)
  serve      Start without opening a window (for running in the background)
  stop       Close a running Pulse normally (everything is saved first)
  status     Check the connection to the WhatsApp Business API
  disconnect Remove this computer's WhatsApp Business API details (token, app secret)
  send       Send one message from the command line
  reset-password  Forgot the admin password? Creates a new one-time password
  shortcut   Create a desktop shortcut
  version    Show the version

Flags:
  -config string    settings file (default: config.yaml in the data folder)
  -session string   separate profile for another number, e.g. -session sales
  -port int         dashboard port (default 8085)
  -keep-running     with serve: start again automatically after a crash
  -to, -msg         recipient and text for "send"

Data (messages, contacts) is stored, encrypted, in the folder shown at start-up.
The access token and app secret are stored sealed with your Windows account.
`, version.Product, version.Version, version.Tagline)
}

type env struct {
	cfg     *config.Config
	dataDir string
	box     *securestore.Box
	suffix  string
	// oldSession lists where versions before 2.0 kept the linked-device WhatsApp
	// session. Those files are no longer used; the owner can delete them in Settings.
	oldSession []string
	// otherCopy: another running copy holds this data folder's single-copy lock (only
	// set for run; the data key and data files were not opened).
	otherCopy bool
}

// oldSessionFiles returns the places a linked-device version may have left its session
// database for this profile: the data folder and, from older versions still, the folder
// of the settings file. Nothing here is opened or changed automatically.
func oldSessionFiles(dataDir, cfgDir, suffix string) []string {
	var out []string
	for _, dir := range []string{dataDir, cfgDir} {
		base := filepath.Join(dir, "whatsapp"+suffix+".db")
		for _, ext := range []string{"", "-wal", "-shm", ".sealed", ".sealed.bak", ".tmp"} {
			out = append(out, base+ext)
		}
	}
	return out
}

var sessionRe = regexp.MustCompile(`[^a-zA-Z0-9_\-]`)

func defaultDataDir(suffix string) string {
	if d := os.Getenv("PULSE_DATA_DIR"); d != "" {
		return d
	}
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = "."
	}
	return filepath.Join(base, version.Product+suffix)
}

// dataDirFor is the data folder for the storage.data_dir setting and a -session suffix.
func dataDirFor(configured, suffix string) string {
	if configured == "" {
		return defaultDataDir(suffix)
	}
	if suffix != "" {
		return filepath.Join(configured, suffix[1:])
	}
	return configured
}

func filepathEqual(a, b string) bool {
	if aa, err := filepath.Abs(a); err == nil {
		a = aa
	}
	if bb, err := filepath.Abs(b); err == nil {
		b = bb
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// takeInstanceLock creates dataDir and takes its single-copy lock for this process. It
// reports true when another running copy holds it.
func takeInstanceLock(dataDir string) (otherCopy bool) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		fail("Could not create the data folder %s: %v", dataDir, err)
	}
	securestore.RestrictToUser(dataDir)
	switch err := acquireInstanceLock(dataDir); {
	case errors.Is(err, errInstanceLocked):
		return true
	case err != nil:
		fmt.Printf("[Data] Could not take the single-copy lock in %s (%v). Make sure %s is not already running.\n", dataDir, err, version.Product)
	default:
		atFail(releaseInstanceLock)
	}
	return false
}

// setup loads the configuration and opens the data folder. Data files from older
// versions are looked for next to the configuration file (never the current directory)
// and are only moved when migrate is true and no other copy of the app is running.
func setup(cfgPath, session string, port int, migrate bool) env {
	// Every command checks this, not only the server: any of them would otherwise create
	// an empty settings file, and the next start would hand out a new one-time password.
	if settingsMissing(cfgPath) && !allowNewSettings {
		fail("%s data was found in %s, but its settings file (config.yaml) is missing or empty.\n"+
			"If you moved %s to a new folder: close it, copy config.yaml from the old %s folder into\n  %s\nand start again.\n"+
			"If the settings are lost: run Reset-Password.bat and choose \"Yes\" when Windows asks for permission.",
			version.Product, defaultDataDir(""), version.Product, version.Product, programDir)
	}
	absCfg, err := filepath.Abs(cfgPath)
	if err != nil {
		fail("Invalid configuration path %s: %v", cfgPath, err)
	}
	baseDir := filepath.Dir(absCfg)
	if programDir != "" {
		baseDir = programDir // older versions kept their files next to the program
	}
	if session == "" {
		session = os.Getenv("WA_SESSION_NAME")
	}
	if port <= 0 {
		port = cliPort
	}
	if port <= 0 {
		port, _ = strconv.Atoi(os.Getenv("WA_PORT"))
	}
	suffix := ""
	if s := sessionRe.ReplaceAllString(session, ""); s != "" && s != "default" {
		suffix = "-" + s
	}
	// The settings file may be created, copied for a -session profile, set aside or
	// rewritten (old readable secrets) below. That happens only while holding the settings
	// lock, so two copies started at the same moment never both create one, each with its
	// own one-time password. "run" also takes the single-copy lock first (N6): the data
	// folder is read from the settings file without loading (and so without changing) it.
	unlockCfg := lockConfigDir(filepath.Dir(absCfg))
	defer unlockCfg()
	otherCopy, lockedDir := false, ""
	if migrate {
		probePath := absCfg
		if sp := filepath.Join(filepath.Dir(absCfg), "config"+suffix+".yaml"); suffix != "" && !configGiven {
			if _, err := os.Stat(sp); err == nil {
				probePath = sp // otherwise it starts as a copy of the main settings
			}
		}
		probe, _ := readSettingsProbe(probePath)
		lockedDir = dataDirFor(probe.Storage.DataDir, suffix)
		otherCopy = takeInstanceLock(lockedDir)
		migrate = !otherCopy
	}
	absCfg, fresh := sessionConfig(absCfg, suffix)
	cfg, err := config.LoadConfig(absCfg)
	if errors.Is(err, config.ErrEmptySettings) && allowNewSettings {
		// reset-password, run as administrator, may start over with new settings: the
		// empty file is kept aside rather than overwritten.
		aside := absCfg + ".empty-" + time.Now().Format("20060102-150405")
		if rerr := os.Rename(absCfg, aside); rerr != nil {
			fail("Could not move the empty settings file %s aside: %v", absCfg, rerr)
		}
		cfg, err = config.LoadConfig(absCfg)
	}
	if errors.Is(err, config.ErrEmptySettings) {
		fail("%v\nIf the settings are lost: run Reset-Password.bat and choose \"Yes\" when Windows asks for permission.", err)
	}
	if err != nil {
		fail("Could not load the configuration: %v", err)
	}
	if fresh {
		_ = cfg.Update(func(c *config.Config) error {
			c.CloudAPI = config.CloudAPIConfig{}
			c.Automation.Enabled = false
			c.Setup.WebhookSeenAt, c.Setup.TestMessageSentAt = nil, nil
			return nil
		})
		// That save copied the main profile's file, with its sealed token, to
		// config-<name>.yaml.bak; save once more so the backup copy is cleared too (F13).
		_ = cfg.Save()
	}
	snap := cfg.Snapshot()
	dataDir := dataDirFor(snap.Storage.DataDir, suffix)
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		fail("Could not create the data folder %s: %v", dataDir, err)
	}
	securestore.RestrictToUser(dataDir)

	// Only one copy may use a data folder: two would overwrite each other's files and
	// process the same incoming messages twice (B1). "run" (the only caller with migrate)
	// took the lock above, before the settings or any data file were read, moved or
	// replayed. Should the loaded settings name another folder than the one read without
	// loading them (a backup copy of the settings was used, say), that folder is locked.
	if migrate && !filepathEqual(dataDir, lockedDir) {
		releaseInstanceLock()
		otherCopy = takeInstanceLock(dataDir)
		migrate = !otherCopy
	}
	unlockCfg()

	effPort := snap.Server.Port
	if port > 0 {
		effPort = port
	}
	if migrate && portInUse("127.0.0.1", effPort) {
		migrate = false // another copy (possibly an older version) is using these files
	}
	backupDir := filepath.Join(dataDir, "old-version-backup", time.Now().Format("20060102-150405"))
	cfg.SetRuntime(effPort)

	e := env{cfg: cfg, dataDir: dataDir, suffix: suffix, oldSession: oldSessionFiles(dataDir, baseDir, suffix), otherCopy: otherCopy}
	if otherCopy {
		return e // run only shows the running copy's window; nothing else is opened
	}
	if snap.Storage.Encrypt {
		e.box, err = securestore.OpenBox(dataDir)
		if err != nil {
			fail("%v", err)
		}
	}
	securestore.SetDefault(e.box)
	if migrate {
		for _, name := range []string{"contacts", "messages", "scheduled", "templates"} {
			e.migrateJSON(filepath.Join(baseDir, name+suffix+".json"), e.path(name), backupDir)
		}
	}
	return e
}

func (e env) path(name string) string { return filepath.Join(e.dataDir, name+".json") }

// migrateJSON copies a plaintext data file from an older version into the data folder,
// encrypted. Once the copy reads back correctly, the plaintext originals are moved into
// backupDir (inside the protected data folder) rather than deleted.
func (e env) migrateJSON(legacy, target, backupDir string) {
	data, err := os.ReadFile(legacy)
	if err != nil || len(bytes.TrimSpace(data)) == 0 {
		return
	}
	if _, err := os.Stat(target); err == nil {
		fmt.Printf("[Data] %s was not imported because the data folder already has newer data. It was left where it is.\n", legacy)
		return
	}
	data, dropped, err := cleanLegacyJSON(data)
	if err != nil {
		// Importing a file Pulse cannot read would stop every later start (D35).
		fmt.Printf("[Data] %s could not be read (%v), so it was not imported. The original was kept.\n", legacy, err)
		return
	}
	if dropped > 0 {
		fmt.Printf("[Data] %d entries in %s had an invalid id and were skipped.\n", dropped, filepath.Base(legacy))
	}
	if err := e.box.WriteFile(target, data); err != nil {
		fmt.Printf("[Data] Could not import %s: %v. The original was kept.\n", legacy, err)
		return
	}
	back, err := e.box.ReadFile(target)
	if err != nil || !bytes.Equal(back, data) {
		_ = os.Remove(target)
		fmt.Printf("[Data] Import check for %s failed. The original was kept.\n", legacy)
		return
	}
	_ = os.Remove(target + ".bak")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		fmt.Printf("[Data] Imported %s; the original stays in place (%v).\n", legacy, err)
		return
	}
	for _, p := range []string{legacy, legacy + ".bak"} {
		if _, err := os.Stat(p); err == nil {
			dst := filepath.Join(backupDir, filepath.Base(p))
			if err := moveFile(p, dst); err != nil {
				fmt.Printf("[Data] Could not move %s into the backup folder: %v\n", p, err)
			}
		}
	}
	fmt.Printf("[Data] Imported %s. The unencrypted original is in %s; delete it once you have checked your data.\n", filepath.Base(legacy), backupDir)
}

var legacyID = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// cleanLegacyJSON prepares a data file from an older version for import (D35): it removes
// a byte-order mark (which Windows editors add), checks that the file is a JSON list,
// shortens text fields to sane lengths, and skips entries whose id could be read as a
// path. It returns the cleaned file and how many entries were skipped.
func cleanLegacyJSON(data []byte) ([]byte, int, error) {
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	var items []map[string]any
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, 0, errors.New("not a valid data file")
	}
	kept, dropped := make([]map[string]any, 0, len(items)), 0
	for _, it := range items {
		if id, ok := it["id"].(string); ok && (!legacyID.MatchString(id) || strings.Contains(id, "..")) {
			dropped++
			continue
		}
		for k, v := range it {
			str, ok := v.(string)
			if !ok {
				continue
			}
			limit := 10000 // message text, notes, template content
			if k == "name" || k == "push_name" || k == "category" || k == "email" {
				limit = 200
			}
			if r := []rune(str); len(r) > limit {
				it[k] = string(r[:limit])
			}
		}
		kept = append(kept, it)
	}
	out, err := json.Marshal(kept)
	return out, dropped, err
}

// moveFile renames src to dst, falling back to a verified copy across volumes.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		// A rename keeps the old folder's permissions, which may let other accounts read
		// the file; it now lives in the data folder, so only this user may.
		securestore.RestrictToUser(dst)
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0600); err != nil {
		return err
	}
	securestore.RestrictToUser(dst)
	if back, err := os.ReadFile(dst); err != nil || !bytes.Equal(back, data) {
		return errors.New("copy could not be verified; original kept")
	}
	return os.Remove(src)
}

// failCleanups run, newest first, before fail exits, because os.Exit skips deferred
// calls: the data is saved first (F10).
var (
	failMu       sync.Mutex
	failCleanups []func()
)

func atFail(f func()) {
	failMu.Lock()
	failCleanups = append(failCleanups, f)
	failMu.Unlock()
}

func runFailCleanups() {
	failMu.Lock()
	fs := failCleanups
	failCleanups = nil
	failMu.Unlock()
	for i := len(fs) - 1; i >= 0; i-- {
		fs[i]()
	}
}

func fail(format string, a ...any) {
	runFailCleanups()
	fmt.Printf("Error: "+format+"\n", a...)
	if runtime.GOOS == "windows" && isTerminal() {
		fmt.Println("Press Enter to close.")
		_, _ = fmt.Scanln()
	}
	os.Exit(1)
}

func isTerminal() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func announcePassword(e env, pass string) {
	notice := filepath.Join(e.dataDir, server.FirstRunNoticeName)
	snap := e.cfg.Snapshot()
	body := fmt.Sprintf("%s one-time password: %s\r\n\r\nOpen http://127.0.0.1:%d and sign in with it. You will then choose your own password and this file is deleted.\r\n", version.Product, pass, snap.ListenPort())
	_ = os.WriteFile(notice, []byte(body), 0600)
	fmt.Println()
	fmt.Println("==================================================================")
	fmt.Println(" FIRST START: sign in with a one-time password")
	if isTerminal() {
		fmt.Printf(" One-time password:  %s\n", pass)
	}
	fmt.Printf(" It is also saved in: %s\n", notice)
	fmt.Printf(" Dashboard:          http://127.0.0.1:%d\n", snap.ListenPort())
	fmt.Println("==================================================================")
	fmt.Println()
}

func portInUse(host string, port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 500*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// A receipt for a message not in the history yet is tried again receiptRetries times,
// receiptRetryAfter apart: 80 seconds, longer than the longest send (60 seconds for
// campaigns and scheduled messages), so the answer of a slow send is always waited for,
// while a receipt for a message sent from the phone (never in the history) is still
// dropped soon.
const (
	receiptRetries    = 8
	receiptRetryAfter = 10 * time.Second
)

// campaignReceipts is the part of the campaign runner that tracks delivery.
type campaignReceipts interface {
	OnFailed(id, reason string)
	OnReceipt(ids []string, status string)
}

// sendLabels is implemented by the campaign runner and the scheduler: a delivery update
// carrying one of their labels corrects a send that was cut off (cloudapi.WithCallbackData).
type sendLabels interface {
	Reconcile(label, messageID, status string) bool
}

// receiptHandler applies Meta's delivery updates to the history and campaigns. Meta
// often reports "sent", "delivered" or "failed" through the webhook before its answer
// to the send itself reaches Pulse, so the message is not in the history yet: such a
// receipt is applied again a little later instead of being lost.
func receiptHandler(hs *history.Store, runner campaignReceipts, wait time.Duration, labels ...sendLabels) func(cloudapi.Receipt) {
	var apply func(r cloudapi.Receipt, retries int)
	apply = func(r cloudapi.Receipt, retries int) {
		if _, known := hs.Get(r.MessageID); !known && retries > 0 {
			time.AfterFunc(wait, func() { apply(r, retries-1) })
			return
		}
		if r.Status == "failed" {
			hs.MarkFailed(r.MessageID, r.Error)
			runner.OnFailed(r.MessageID, r.Error)
			return
		}
		hs.UpdateStatus(r.MessageID, r.Status)
		runner.OnReceipt([]string{r.MessageID}, r.Status)
	}
	return func(r cloudapi.Receipt) {
		if r.CallbackData != "" {
			// The history record of a send whose answer was lost takes the real ID first,
			// so the update below (and later ones) apply to it.
			hs.ResolveLabel(r.CallbackData, r.MessageID, r.Status)
			for _, l := range labels {
				if l.Reconcile(r.CallbackData, r.MessageID, r.Status) {
					break
				}
			}
		}
		apply(r, receiptRetries)
	}
}

func run(cfgPath, session string, port int, window bool) {
	fmt.Printf("%s %s starting...\n", version.Product, version.Version)
	e := setup(cfgPath, session, port, true)
	snap := e.cfg.Snapshot()
	url := fmt.Sprintf("http://127.0.0.1:%d", snap.ListenPort())
	if e.otherCopy {
		// Another copy holds this data folder. It may have been started with another
		// -port, or still be starting: show its window once its dashboard answers.
		if p, ok := runningPort(e); ok {
			url = fmt.Sprintf("http://127.0.0.1:%d", p)
		}
		fmt.Printf("%s is already running at %s.\n", version.Product, url)
		if window && desktop.WaitForServer(url+"/login", 10*time.Second) {
			_, _ = desktop.LaunchAppWindow(url)
		}
		return
	}
	defer releaseInstanceLock()
	if portInUse("127.0.0.1", snap.ListenPort()) {
		fmt.Printf("%s is already running at %s.\n", version.Product, url)
		if window {
			_, _ = desktop.LaunchAppWindow(url)
		}
		return
	}
	if !securestore.SecretsSealed() {
		fmt.Println("[Security] On this system the access token, app secret and data key are NOT sealed by the operating system: they are protected only by the permissions of the data folder. Keep this account and its backups private.")
	}
	if _, legacy := e.cfg.InboundSecret(); legacy {
		fmt.Println("[Integrations] The inbound integration endpoint accepts the outbound signing key (webhook.secret) because no separate webhook.inbound_secret is set. Set inbound_secret in config.yaml (16+ characters) so the system that receives Pulse's events cannot also send messages through Pulse.")
	}
	if snap.Server.Host != "127.0.0.1" && snap.Server.Host != "localhost" && snap.Server.Host != "::1" {
		fmt.Printf("[Security] The dashboard listens on %s, so other devices on the network can reach the sign-in page. Use 127.0.0.1 unless you need remote access.\n", snap.Server.Host)
	}
	fmt.Printf("[Data] Folder: %s (encrypted: %v)\n", e.dataDir, e.box != nil)

	security.OpenAuditLog(e.path("audit"), e.box)
	// A crash (rather than a normal stop) writes its details here, so the cause of an
	// unexplained stop can be found (D26).
	if f, err := os.OpenFile(filepath.Join(e.dataDir, "crash.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
		_ = debug.SetCrashOutput(f, debug.CrashOptions{})
		f.Close()
	}
	security.LogAudit("APP_STARTED", "local", "system", version.Product+" "+version.Version+" started", "success")
	if pass, isNew := e.cfg.EnsureAdminPassword(); isNew {
		announcePassword(e, pass)
	}

	wa := newCloud(e.cfg)
	atFail(wa.Stop)
	cs, err := contacts.NewStore(e.path("contacts"))
	if err != nil {
		fail("%v", err)
	}
	ts, err := templates.NewStore(e.path("templates"))
	if err != nil {
		fail("%v", err)
	}
	hs, err := history.NewStore(e.path("messages"), snap.Storage.MaxMessages)
	if err != nil {
		fail("%v", err)
	}
	hooks := webhook.New(e.cfg, e.path("webhook-failed"))
	hooks.Start()
	notifier := notify.NewDesktop(func() bool { return e.cfg.Snapshot().Notifications.Desktop })

	runner, err := campaign.NewRunner(e.path("campaigns"), wa, e.cfg, cs, hs)
	if err != nil {
		fail("%v", err)
	}
	sched, err := scheduler.NewScheduler(e.path("scheduled"), wa, e.cfg, cs, hs)
	if err != nil {
		fail("%v", err)
	}

	wa.OnReceipt(receiptHandler(hs, runner, receiptRetryAfter, runner, sched))
	hs.OnRecord(func(m history.MessageRecord) {
		if m.Direction == history.DirectionIncoming && !m.Imported {
			runner.OnIncoming(m.Phone, m.Timestamp)
		}
	})
	engine := bot.NewEngine(wa, e.cfg, cs, hs, hooks, notifier)
	unprocessed := engine.UseJournal(e.path("inbound-pending"))
	wa.OnMessage(func(m cloudapi.Message) {
		engine.Receive(bot.Incoming{ID: m.ID, Phone: m.From, PushName: m.Name, Text: m.Text, Media: m.Media, SentAt: m.Timestamp})
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sched.Start(ctx)

	quitCh := make(chan struct{})
	var quitOnce sync.Once
	control := security.RandomToken(24)
	if err := os.WriteFile(filepath.Join(e.dataDir, controlFile), []byte(control), 0600); err != nil {
		fmt.Printf("[Data] The stop command will not work: %v\n", err)
	}
	defer os.Remove(filepath.Join(e.dataDir, controlFile))
	// The port in use, so reset-password, disconnect and stop also find a copy that was
	// started with -port (otherwise its next save would undo their change).
	if err := os.WriteFile(filepath.Join(e.dataDir, portFile), []byte(strconv.Itoa(snap.ListenPort())), 0600); err == nil {
		defer os.Remove(filepath.Join(e.dataDir, portFile))
	}
	srv := server.New(server.Deps{Config: e.cfg, Client: wa, Contacts: cs, Templates: ts, History: hs, Scheduler: sched, Campaigns: runner, Engine: engine, Hooks: hooks, Alerts: notifier,
		Quit: func() { quitOnce.Do(func() { close(quitCh) }) }, ControlToken: control, DataDir: e.dataDir, Profile: strings.TrimPrefix(e.suffix, "-"), Encrypted: e.box != nil, OldSessionFiles: e.oldSession})

	go retention(ctx, e.cfg, hs)
	go srv.RunAutoBackups(ctx)

	wa.Start(ctx)
	if len(unprocessed) > 0 {
		// Received before an unexpected stop but never processed: handle them now.
		fmt.Printf("[Bot] Processing %d message(s) received before Pulse last stopped.\n", len(unprocessed))
		for _, in := range unprocessed {
			engine.Receive(in)
		}
	}
	if snap.CloudAPI.Ready() {
		fmt.Printf("[WhatsApp] Business API number ID %s. Incoming messages arrive at %s\n", snap.CloudAPI.PhoneNumberID, orNotSet(snap.CloudAPI.WebhookURL()))
	} else {
		fmt.Println("[WhatsApp] Not connected yet: enter your WhatsApp Business API details in Settings > WhatsApp connection.")
	}

	errCh := make(chan error, 1)
	go func() {
		// If the dashboard stops on its own after running fine (D26), start it again
		// instead of shutting Pulse down; only a failure at start-up ends the app.
		for attempt := 0; ; attempt++ {
			started := time.Now()
			err := srv.Start()
			if err == nil || time.Since(started) < 10*time.Second || attempt >= 5 {
				errCh <- err
				return
			}
			security.LogAudit("DASHBOARD_RESTARTED", "local", "system", "The dashboard stopped unexpectedly and was restarted: "+err.Error(), "warning")
			time.Sleep(2 * time.Second)
		}
	}()
	fmt.Printf("[Dashboard] %s\n", url)
	if window {
		go func() {
			if desktop.WaitForServer(url+"/login", 10*time.Second) {
				if _, err := desktop.LaunchAppWindow(url); err != nil {
					fmt.Printf("Open %s in your browser.\n", url)
				}
			}
		}()
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	fmt.Println("Running. Press Ctrl+C to stop.")
	reason := ""
	failed := false // the dashboard died: exit with an error so -keep-running restarts Pulse
	select {
	case <-quitCh:
		reason = "Stopped with the stop command"
	case s := <-sig:
		// Ctrl+C, closing this window, signing out of Windows or shutting down.
		reason = "Stopped by Windows or the user (" + s.String() + ")"
	case err := <-errCh:
		failed = true
		reason = "The dashboard stopped"
		if err != nil {
			var opErr *net.OpError
			if errors.As(err, &opErr) {
				fmt.Printf("The dashboard could not start on port %d: %v\n", snap.ListenPort(), err)
			} else {
				fmt.Printf("Server error: %v\n", err)
			}
			reason += ": " + err.Error()
		}
	}
	// Recorded so an unexpected stop can be explained afterwards (Settings > Account > Activity log).
	security.LogAudit("APP_STOPPED", "local", "system", reason, "success")

	fmt.Println("Stopping...")
	shut, done := context.WithTimeout(context.Background(), 8*time.Second)
	defer done()
	_ = srv.Stop(shut)
	sched.Stop()
	runner.CancelAll("Pulse was stopped. Press Resume to continue.")
	// Messages already received are handled (at least stored, with opt-outs applied)
	// before the final save (F16); the webhook stopped with the dashboard above.
	drain, drained := context.WithTimeout(context.Background(), 5*time.Second)
	engine.Stop(drain)
	drained()
	wa.Stop()
	hooks.Stop()
	cancel()
	for _, f := range []func() error{cs.Flush, hs.Flush, runner.Flush} {
		if err := f(); err != nil {
			fmt.Printf("[Data] Save failed: %v\n", err)
		}
	}
	security.FlushAuditLog()
	// A normal stop leaves no crash details: drop the empty crash.log so its presence
	// alone means something went wrong.
	_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
	if crash := filepath.Join(e.dataDir, "crash.log"); fileEmpty(crash) {
		_ = os.Remove(crash)
	}
	fmt.Println("Stopped.")
	if failed {
		releaseInstanceLock() // os.Exit skips the deferred release
		os.Exit(1)            // everything is saved above; the supervisor treats exit code 1 as a crash
	}
}

func fileEmpty(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() == 0
}

func orNotSet(s string) string {
	if s == "" {
		return "(not set: add the public address in Settings > WhatsApp connection)"
	}
	return s
}

// retention deletes old messages once a day according to the storage settings.
func retention(ctx context.Context, cfg *config.Config, hs *history.Store) {
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st := cfg.Snapshot().Storage
		hs.SetMaxItems(st.MaxMessages)
		if st.RetentionDays > 0 {
			if n := hs.PurgeOlderThan(st.RetentionDays); n > 0 {
				security.LogAudit("RETENTION", "local", "system", fmt.Sprintf("Deleted %d messages older than %d days", n, st.RetentionDays), "success")
			}
		}
		t.Reset(24 * time.Hour)
	}
}

// sendContact returns the contact the send command checks before messaging phone. It
// uses the exact number that will be messaged, never a guess, so a number cannot borrow
// another contact's consent or skip the opt-out of a deleted contact (F3).
func sendContact(cs *contacts.Store, phone string) contacts.Contact {
	if c, ok := cs.GetExact(phone); ok {
		return *c
	}
	return contacts.Contact{Phone: phone, IsDND: cs.WasOptedOut(phone)}
}

func runSend(cfgPath, session, to, text string) {
	e := setup(cfgPath, session, 0, false)
	snap := e.cfg.Snapshot()
	// Held while this command writes the data files, so Pulse cannot start meanwhile.
	lockErr := acquireInstanceLock(e.dataDir)
	if errors.Is(lockErr, errInstanceLocked) || portInUse("127.0.0.1", snap.ListenPort()) {
		fail("%s is running. Send the message from the dashboard instead (the data files can only be used by one program).", version.Product)
	}
	if lockErr == nil {
		atFail(releaseInstanceLock)
		defer releaseInstanceLock()
	}
	cs, err := contacts.NewStore(e.path("contacts"))
	if err != nil {
		fail("%v", err)
	}
	hs, err := history.NewStore(e.path("messages"), snap.Storage.MaxMessages)
	if err != nil {
		fail("%v", err)
	}
	security.OpenAuditLog(e.path("audit"), e.box)
	save := func() {
		for _, f := range []func() error{cs.Flush, hs.Flush} {
			if err := f(); err != nil {
				fmt.Printf("[Data] Save failed: %v\n", err)
			}
		}
		security.FlushAuditLog()
	}
	atFail(save)
	// A saved contact is found as written first, so a number that came from WhatsApp keeps
	// its country; only an unknown number is read as typed (a 10-digit Indian mobile gets 91).
	phone := contacts.CleanPhone(to)
	if c, ok := cs.Get(to); ok {
		phone = c.Phone
	}
	if len(phone) < 7 || len(phone) > 15 {
		fail("Enter a valid phone number with country code, for example +919876543210.")
	}
	// The same rules as every other way of sending: opt-out, blocked numbers, test mode,
	// the 24-hour window, the daily limit and practice mode.
	ct := sendContact(cs, phone)
	a := snap.Automation
	switch {
	case ct.IsDND:
		fail("This customer opted out of messages.")
	case containsPhone(a.BlacklistPhones, phone):
		fail("This number is on the blocked list (Settings > Safety).")
	case a.AllowListOnly && !cs.AllowListed(a.AllowList, phone):
		fail("Test mode is on: messages can only go to the test numbers in Settings > Safety.")
	case campaign.NeedsTemplate(&ct, time.Now()):
		fail("%s", campaign.OutsideWindowMessage)
	case campaign.DailyRemaining(snap, hs, time.Now()) <= 0:
		fail("Today's sending limit has been reached.")
	}
	rendered, missing := msgtemplate.Render(text, msgtemplate.Vars{Name: ct.Name, Phone: phone, Business: snap.Business, Extra: map[string]string{"email": ct.Email, "notes": ct.Notes}, Now: time.Now(), Location: msgtemplate.Location(snap.BusinessHours.Timezone)})
	if len(missing) > 0 {
		fail("%v", msgtemplate.MissingError(missing))
	}
	rec := history.MessageRecord{Direction: history.DirectionOutgoing, Type: history.TypeDirectSend, Phone: phone, Text: rendered, SentBy: "command line"}
	if a.DryRun {
		rec.ID, rec.Status = "dry_"+security.RandomToken(6), history.StatusDryRun
		hs.Record(rec)
		save()
		fmt.Println("Practice mode is on: the message was recorded but not sent.")
		return
	}
	defer save()
	// Recorded ("sending", labelled) before it goes, like the dashboard: a send whose
	// answer is lost shows in the inbox as "may have been sent" and counts towards the
	// daily limit.
	rec.Label = server.DirectLabel + security.RandomToken(8)
	cloud := newCloud(e.cfg)
	id, err := campaign.SendRecorded(context.Background(), hs, rec, 60*time.Second, func(ctx context.Context) (string, error) {
		return cloud.SendTextMessage(ctx, phone, rendered)
	})
	if cloudapi.MaybeSent(err) {
		fail("%s (%v)", campaign.MaybeSentMessage, err)
	}
	if err != nil {
		fail("%v", err)
	}
	cs.Upsert(phone, "", "manual")
	cs.UpdateLastContacted(phone)
	fmt.Println("Sent. Message ID:", id)
}

// containsPhone reports whether phone (a stored number) is on a list of numbers a person
// typed, matched like the server does (contacts.MatchesTyped): the blocked number
// "6591234567" also blocks the Singapore contact 6591234567.
func containsPhone(list []string, phone string) bool {
	for _, p := range list {
		if contacts.MatchesTyped(p, phone) {
			return true
		}
	}
	return false
}

func runStatus(cfgPath, session string) {
	e := setup(cfgPath, session, 0, false)
	snap := e.cfg.Snapshot()
	ca := snap.CloudAPI
	fmt.Printf("%s %s\nData folder: %s\n", version.Product, version.Version, e.dataDir)
	if !ca.Ready() {
		fmt.Println("WhatsApp Business API: not set up. Enter your details in Settings > WhatsApp connection.")
	} else {
		fmt.Printf("Phone number ID: %s\nIncoming messages arrive at: %s\n", ca.PhoneNumberID, orNotSet(ca.WebhookURL()))
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		st, err := newCloud(e.cfg).Check(ctx)
		cancel()
		if err != nil {
			fmt.Println("Connection check: " + err.Error())
		} else {
			fmt.Printf("Connected: %s (%s), quality rating %s\n", st.DisplayPhone, st.VerifiedName, st.QualityRating)
		}
	}
	fmt.Printf("Auto-reply rules: %d\n", len(e.cfg.GetRules()))
}

const (
	controlFile = "control.token"
	portFile    = "control.port"
)

// runningPort reports the port of a running Pulse that uses this data folder. The port
// that copy recorded comes first: the configured port may be used by another program or
// by a Pulse with a different data folder. Older versions record nothing, so the
// configured port is checked after it.
func runningPort(e env) (int, bool) {
	if b, err := os.ReadFile(filepath.Join(e.dataDir, portFile)); err == nil {
		if p, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && p > 0 && portInUse("127.0.0.1", p) {
			return p, true
		}
	}
	if p := e.cfg.Snapshot().ListenPort(); portInUse("127.0.0.1", p) {
		return p, true
	}
	return 0, false
}

// claimDataFolder stops a command that changes settings or data when a running copy uses
// e's data folder (it holds the single-copy lock, or its dashboard answers), and
// otherwise holds the lock itself until the command ends, so Pulse cannot start in the
// middle of the change. running is a message with one %s for the product name.
func claimDataFolder(e env, running string) {
	err := acquireInstanceLock(e.dataDir)
	if _, answering := runningPort(e); errors.Is(err, errInstanceLocked) || answering {
		fail(running, version.Product)
	}
	if err == nil {
		atFail(releaseInstanceLock)
	}
}

// runStop asks a running Pulse to close normally, so its data is saved and the
// session file encrypted (used by the stop/uninstall scripts instead of killing it).
func runStop(cfgPath, session string) {
	e := setup(cfgPath, session, 0, false)
	port, running := runningPort(e)
	if !running {
		if instanceLockHeld(e.dataDir) {
			fail("%s is running but its dashboard does not answer yet (it may still be starting). Try again in a moment, or close its window.", version.Product)
		}
		fmt.Printf("%s is not running.\n", version.Product)
		return
	}
	token, err := os.ReadFile(filepath.Join(e.dataDir, controlFile))
	if errors.Is(err, os.ErrNotExist) {
		// A running Pulse always has its stop key: the port belongs to another program.
		fmt.Printf("%s is not running (port %d is used by another program).\n", version.Product, port)
		return
	}
	if err != nil {
		fail("Could not find the running copy's stop key (%v). Close its window instead.", err)
	}
	req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/control/quit", port), nil)
	req.Header.Set("X-Pulse-Control", strings.TrimSpace(string(token)))
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		fail("Could not reach %s: %v", version.Product, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fail("%s refused the stop request (HTTP %d). Close its window instead.", version.Product, resp.StatusCode)
	}
	for i := 0; i < 30 && portInUse("127.0.0.1", port); i++ {
		time.Sleep(500 * time.Millisecond)
	}
	time.Sleep(2 * time.Second) // final save and encryption run after the dashboard closes
	fmt.Printf("%s was closed normally. Everything was saved.\n", version.Product)
}

func runResetPassword(cfgPath, session string) {
	if !isTerminal() {
		fail("Run this from Reset-Password.bat or a command window.")
	}
	// Administrator rights are a hurdle against a casual takeover, not a hard barrier:
	// whoever uses this Windows account can also edit config.yaml. (The business number
	// cannot send a code to itself through the official API, so there is no phone check.)
	// Checked before setup, which may create new settings when the old ones are lost.
	if !isElevated() {
		fail("Resetting the admin password needs Windows administrator rights.\nClose this window, run Reset-Password.bat and choose \"Yes\" when Windows asks for permission.")
	}
	allowNewSettings = true
	e := setup(cfgPath, session, 0, false)
	claimDataFolder(e, "%s is running. Close it first (Ctrl+C in its window), then run this again.")
	defer releaseInstanceLock()
	// Elevating with another account's password runs this as that account, whose
	// %APPDATA% holds no Pulse data; the audit entry and password file would go there.
	if _, err := os.Stat(e.path("audit")); err != nil {
		fail("No Pulse data was found in %s.\nIf Windows asked for another account's password, this ran as that account.\n"+
			"Pulse's data can only be opened by the Windows account that created it, so the reset must run as that account:\n"+
			"an administrator gives that account administrator rights for a moment (Settings > Accounts > Other users),\n"+
			"you sign in as that account and run Reset-Password.bat, then the rights are taken back.\n"+
			"See \"Forgot the password on a standard account\" in HOW-TO-RUN.txt.", e.dataDir)
	}
	security.OpenAuditLog(e.path("audit"), e.box)
	proof := "run as Windows administrator"
	pass, err := e.cfg.ResetAdminPassword()
	if err != nil {
		fail("%v", err)
	}
	security.LogAudit("PASSWORD_RESET", "local", "admin", "Admin password reset with the reset tool ("+proof+")", "warning")
	security.FlushAuditLog()
	announcePassword(e, pass)
	fmt.Println("Messages, contacts and the WhatsApp connection were not changed. Start the app and sign in with this password.")
}

// runDisconnect removes this computer's WhatsApp Business API details (number ID,
// access token, app secret) after the owner confirms. Messages and contacts are kept.
func runDisconnect(cfgPath, session string) {
	e := setup(cfgPath, session, 0, false)
	claimDataFolder(e, "%s is running. Disconnect from the dashboard (Settings > WhatsApp connection) instead.")
	defer releaseInstanceLock()
	if !e.cfg.Snapshot().CloudAPI.Ready() {
		fmt.Println("No WhatsApp Business API details are saved. Nothing to do.")
		return
	}
	if !isTerminal() {
		fail("Run this from Run-Unlink.bat or a command window, so you can confirm it.")
	}
	fmt.Print("This removes the access token and app secret from this computer. Pulse stops sending and receiving until you enter them again.\nType YES and press Enter to continue: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	if strings.TrimSpace(line) != "YES" {
		fmt.Println("Nothing was changed.")
		return
	}
	if err := e.cfg.ClearCloudCredentials(); err != nil {
		fail("%v", err)
	}
	security.OpenAuditLog(e.path("audit"), e.box)
	security.LogAudit("DISCONNECT", "local", "admin", "WhatsApp Business API details removed with the disconnect command", "success")
	security.FlushAuditLog()
	fmt.Println("Disconnected. Messages and contacts were kept; delete them in the dashboard if you need to.")
}

// newCloud creates the WhatsApp Business API client. PULSE_GRAPH_BASE_URL points it at
// a stand-in Graph API on this computer for automated tests; only loopback addresses
// are accepted, so the access token can never be sent anywhere but Meta or this PC.
func newCloud(cfg *config.Config) *cloudapi.Client {
	c := cloudapi.New(cfg)
	if u := os.Getenv("PULSE_GRAPH_BASE_URL"); u != "" {
		if loopbackURL(u) {
			c.BaseURL = u
		} else {
			fmt.Println("[WhatsApp] PULSE_GRAPH_BASE_URL ignored: only http://127.0.0.1 addresses are allowed.")
		}
	}
	return c
}

func loopbackURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}
