package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

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
	_ = fs.Parse(args)
	*cfgPath = resolveConfigPath(fs, *cfgPath)
	cliPort = *port // every command (stop, send, status...) talks to the copy on this port

	switch cmd {
	case "app":
		run(*cfgPath, *session, *port, true)
	case "serve", "server":
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

// resolveConfigPath uses the config.yaml next to the program when none was given and
// the current folder has none, so commands run from another folder (for example
// reset-password) change the real settings instead of creating a new, unused file.
func resolveConfigPath(fs *flag.FlagSet, p string) string {
	given := false
	fs.Visit(func(f *flag.Flag) { given = given || f.Name == "config" })
	configGiven = given
	if given {
		return p
	}
	if _, err := os.Stat(p); err == nil {
		return p
	}
	if exe, err := os.Executable(); err == nil {
		if next := filepath.Join(filepath.Dir(exe), "config.yaml"); next != p {
			if _, err := os.Stat(next); err == nil {
				return next
			}
		}
	}
	return p
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
  -config string    configuration file (default "config.yaml")
  -session string   separate profile for another number, e.g. -session sales
  -port int         dashboard port (default 8085)
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

// setup loads the configuration and opens the data folder. Data files from older
// versions are looked for next to the configuration file (never the current directory)
// and are only moved when migrate is true and no other copy of the app is running.
func setup(cfgPath, session string, port int, migrate bool) env {
	absCfg, err := filepath.Abs(cfgPath)
	if err != nil {
		fail("Invalid configuration path %s: %v", cfgPath, err)
	}
	baseDir := filepath.Dir(absCfg)
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
	absCfg, fresh := sessionConfig(absCfg, suffix)
	cfg, err := config.LoadConfig(absCfg)
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
	}
	snap := cfg.Snapshot()
	dataDir := snap.Storage.DataDir
	if dataDir == "" {
		dataDir = defaultDataDir(suffix)
	} else if suffix != "" {
		dataDir = filepath.Join(dataDir, suffix[1:])
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		fail("Could not create the data folder %s: %v", dataDir, err)
	}
	securestore.RestrictToUser(dataDir)

	effPort := snap.Server.Port
	if port > 0 {
		effPort = port
	}
	if migrate && portInUse("127.0.0.1", effPort) {
		migrate = false // another copy (possibly an older version) is using these files
	}
	backupDir := filepath.Join(dataDir, "old-version-backup", time.Now().Format("20060102-150405"))
	cfg.SetRuntime(effPort)

	var box *securestore.Box
	if snap.Storage.Encrypt {
		box, err = securestore.OpenBox(dataDir)
		if err != nil {
			fail("%v", err)
		}
	}
	securestore.SetDefault(box)
	e := env{cfg: cfg, dataDir: dataDir, box: box, suffix: suffix, oldSession: oldSessionFiles(dataDir, baseDir, suffix)}
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

// moveFile renames src to dst, falling back to a verified copy across volumes.
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, data, 0600); err != nil {
		return err
	}
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

func run(cfgPath, session string, port int, window bool) {
	fmt.Printf("%s %s starting...\n", version.Product, version.Version)
	e := setup(cfgPath, session, port, true)
	snap := e.cfg.Snapshot()
	url := fmt.Sprintf("http://127.0.0.1:%d", snap.ListenPort())
	if portInUse("127.0.0.1", snap.ListenPort()) {
		fmt.Printf("%s is already running at %s.\n", version.Product, url)
		if window {
			_, _ = desktop.LaunchAppWindow(url)
		}
		return
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

	wa.OnReceipt(func(r cloudapi.Receipt) {
		if r.Status == "failed" {
			hs.MarkFailed(r.MessageID, r.Error)
			runner.OnFailed(r.MessageID, r.Error)
			return
		}
		hs.UpdateStatus(r.MessageID, r.Status)
		runner.OnReceipt([]string{r.MessageID}, r.Status)
	})
	hs.OnRecord(func(m history.MessageRecord) {
		if m.Direction == history.DirectionIncoming && !m.Imported {
			runner.OnIncoming(m.Phone, m.Timestamp)
		}
	})
	engine := bot.NewEngine(wa, e.cfg, cs, hs, hooks, notifier)
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
	srv := server.New(server.Deps{Config: e.cfg, Client: wa, Contacts: cs, Templates: ts, History: hs, Scheduler: sched, Campaigns: runner, Engine: engine, Hooks: hooks, Alerts: notifier,
		Quit: func() { quitOnce.Do(func() { close(quitCh) }) }, ControlToken: control, DataDir: e.dataDir, Encrypted: e.box != nil, OldSessionFiles: e.oldSession})

	go retention(ctx, e.cfg, hs)
	go srv.RunAutoBackups(ctx)

	wa.Start(ctx)
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
	select {
	case <-quitCh:
		reason = "Stopped with the stop command"
	case s := <-sig:
		// Ctrl+C, closing this window, signing out of Windows or shutting down.
		reason = "Stopped by Windows or the user (" + s.String() + ")"
	case err := <-errCh:
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
	fmt.Println("Stopped.")
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

func runSend(cfgPath, session, to, text string) {
	e := setup(cfgPath, session, 0, false)
	snap := e.cfg.Snapshot()
	if portInUse("127.0.0.1", snap.ListenPort()) {
		fail("%s is running. Send the message from the dashboard instead (the data files can only be used by one program).", version.Product)
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
	phone := contacts.CleanPhone(to)
	if len(phone) < 7 || len(phone) > 15 {
		fail("Enter a valid phone number with country code, for example +919876543210.")
	}
	// The same rules as every other way of sending: opt-out, blocked numbers, test mode,
	// the 24-hour window, the daily limit and practice mode.
	var ct contacts.Contact
	if c, ok := cs.Get(phone); ok {
		ct, phone = *c, c.Phone
	}
	a := snap.Automation
	switch {
	case ct.IsDND:
		fail("This customer opted out of messages.")
	case containsPhone(a.BlacklistPhones, phone):
		fail("This number is on the blocked list (Settings > Safety).")
	case a.AllowListOnly && !containsPhone(a.AllowList, phone):
		fail("Test mode is on: messages can only go to the test numbers in Settings > Safety.")
	case campaign.NeedsTemplate(&ct, time.Now()):
		fail("%s", campaign.OutsideWindowMessage)
	case campaign.DailyRemaining(snap, hs, time.Now()) <= 0:
		fail("Today's sending limit has been reached.")
	}
	rendered, missing := msgtemplate.Render(text, msgtemplate.Vars{Name: ct.Name, Phone: phone, Business: snap.Business, Extra: map[string]string{"email": ct.Email, "notes": ct.Notes}})
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
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id, err := newCloud(e.cfg).SendTextMessage(ctx, phone, rendered)
	if err != nil {
		fail("%v", err)
	}
	// Recorded so it shows in the inbox and counts towards the daily limit.
	rec.ID, rec.Status, rec.Phone = id, history.StatusSent, phone
	hs.Record(rec)
	cs.Upsert(phone, "", "manual")
	cs.UpdateLastContacted(phone)
	fmt.Println("Sent. Message ID:", id)
}

func containsPhone(list []string, phone string) bool {
	for _, p := range list {
		if c := contacts.CleanPhone(p); c != "" && c == phone {
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

const controlFile = "control.token"

// runStop asks a running Pulse to close normally, so its data is saved and the
// session file encrypted (used by the stop/uninstall scripts instead of killing it).
func runStop(cfgPath, session string) {
	e := setup(cfgPath, session, 0, false)
	port := e.cfg.Snapshot().ListenPort()
	if !portInUse("127.0.0.1", port) {
		fmt.Printf("%s is not running.\n", version.Product)
		return
	}
	token, err := os.ReadFile(filepath.Join(e.dataDir, controlFile))
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
	e := setup(cfgPath, session, 0, false)
	if portInUse("127.0.0.1", e.cfg.Snapshot().ListenPort()) {
		fail("%s is running. Close it first (Ctrl+C in its window), then run this again.", version.Product)
	}
	if !isTerminal() {
		fail("Run this from Reset-Password.bat or a command window.")
	}
	security.OpenAuditLog(e.path("audit"), e.box)
	// Being signed in to Windows is not enough to take over Pulse: the reset must run
	// with Windows administrator rights. (The business number cannot send a code to
	// itself through the official API, so there is no phone check any more.)
	if !isElevated() {
		fail("Resetting the admin password needs Windows administrator rights.\nClose this window, right-click Reset-Password.bat and choose \"Run as administrator\".")
	}
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
	if portInUse("127.0.0.1", e.cfg.Snapshot().ListenPort()) {
		fail("%s is running. Disconnect from the dashboard (Settings > WhatsApp connection) instead.", version.Product)
	}
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
