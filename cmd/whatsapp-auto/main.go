package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"

	"whatsapp-automation/pkg/ai"
	"whatsapp-automation/pkg/bot"
	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/client"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/desktop"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/scheduler"
	"whatsapp-automation/pkg/server"
	"whatsapp-automation/pkg/templates"
)

const version = "1.3.0"

func main() {
	if len(os.Args) < 2 {
		runApp("config.yaml", "", 0)
		return
	}

	command := os.Args[1]

	switch command {
	case "app":
		appFlags := flag.NewFlagSet("app", flag.ExitOnError)
		cfgPath := appFlags.String("config", "config.yaml", "Path to YAML configuration file")
		session := appFlags.String("session", "", "Multi-session account profile name (e.g. sales, support)")
		port := appFlags.Int("port", 0, "Custom HTTP server port (e.g. 8086)")
		_ = appFlags.Parse(os.Args[2:])
		runApp(*cfgPath, *session, *port)

	case "serve", "server":
		serveFlags := flag.NewFlagSet("serve", flag.ExitOnError)
		cfgPath := serveFlags.String("config", "config.yaml", "Path to YAML configuration file")
		session := serveFlags.String("session", "", "Multi-session account profile name (e.g. sales, support)")
		port := serveFlags.Int("port", 0, "Custom HTTP server port (e.g. 8086)")
		_ = serveFlags.Parse(os.Args[2:])
		runServe(*cfgPath, *session, *port)


	case "shortcut":
		if err := desktop.EnsureDesktopShortcut(""); err != nil {
			fmt.Printf("Error creating shortcut: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Desktop shortcut created successfully on your Windows Desktop.")

	case "send":
		sendFlags := flag.NewFlagSet("send", flag.ExitOnError)
		cfgPath := sendFlags.String("config", "config.yaml", "Path to configuration file")
		to := sendFlags.String("to", "", "Recipient phone number with country code (e.g. +14155552671)")
		msg := sendFlags.String("msg", "", "Message text content")
		_ = sendFlags.Parse(os.Args[2:])

		if *to == "" || *msg == "" {
			fmt.Println("Error: both -to and -msg flags are required.")
			sendFlags.Usage()
			os.Exit(1)
		}
		runSend(*cfgPath, *to, *msg)

	case "broadcast":
		bFlags := flag.NewFlagSet("broadcast", flag.ExitOnError)
		cfgPath := bFlags.String("config", "config.yaml", "Path to configuration file")
		csvFile := bFlags.String("file", "contacts.csv", "Path to contacts CSV file")
		template := bFlags.String("msg", "Hello {{name}}!", "Message template")
		minDelay := bFlags.Int("min-delay", 4, "Minimum randomized delay between sends (seconds)")
		maxDelay := bFlags.Int("max-delay", 10, "Maximum randomized delay between sends (seconds)")
		_ = bFlags.Parse(os.Args[2:])

		runBroadcast(*cfgPath, *csvFile, *template, *minDelay, *maxDelay)

	case "pair":
		pairFlags := flag.NewFlagSet("pair", flag.ExitOnError)
		cfgPath := pairFlags.String("config", "config.yaml", "Path to configuration file")
		phone := pairFlags.String("phone", "", "Phone number with country code (e.g. 14155552671)")
		_ = pairFlags.Parse(os.Args[2:])

		if *phone == "" {
			fmt.Println("Error: -phone flag is required.")
			pairFlags.Usage()
			os.Exit(1)
		}
		runPair(*cfgPath, *phone)

	case "status":
		cfgPath := "config.yaml"
		if len(os.Args) > 2 {
			cfgPath = os.Args[2]
		}
		runStatus(cfgPath)

	case "version", "-v", "--version":
		fmt.Printf("whatsapp-auto version %s\n", version)

	case "help", "-h", "--help":
		printHelp()

	default:
		fmt.Printf("Unknown command '%s'. Run 'whatsapp-auto help' for usage.\n", command)
		os.Exit(1)
	}
}

func printHelp() {
	fmt.Printf(`WhatsApp Automation Tool (Go) v%s

Usage:
  whatsapp-auto [command] [flags]

Commands:
  serve       Start the automation bot daemon, web dashboard & REST API (default)
  send        Send a one-off text message via CLI
  broadcast   Send bulk personalized messages from a CSV file with anti-ban delays
  pair        Generate an 8-digit WhatsApp pairing code for a phone number
  status      Inspect session store and connection status
  version     Show application version
  help        Show this help message

Flags for serve:
  -config string    Path to config.yaml (default "config.yaml")

Flags for send:
  -to string        Recipient phone number (e.g. +14155552671)
  -msg string       Text message body
  -config string    Path to config.yaml (default "config.yaml")

Flags for broadcast:
  -file string      Contacts CSV file path (default "contacts.csv")
  -msg string       Message template with {{name}}, {{phone}}, etc.
  -min-delay int    Min delay in seconds between messages (default 4)
  -max-delay int    Max delay in seconds between messages (default 10)
  -config string    Path to config.yaml (default "config.yaml")

Flags for pair:
  -phone string     Phone number with country code (e.g. 14155552671)
  -config string    Path to config.yaml (default "config.yaml")
`, version)
}

func runApp(cfgPath string, sessionName string, customPort int) {
	runApplication(cfgPath, sessionName, customPort, true)
}

func runServe(cfgPath string, sessionName string, customPort int) {
	runApplication(cfgPath, sessionName, customPort, false)
}

func runApplication(cfgPath string, sessionName string, customPort int, launchDesktopApp bool) {
	fmt.Println("==================================================")
	if launchDesktopApp {
		fmt.Printf("🚀 Starting Web Auto v%s\n", version)
	} else {
		fmt.Printf("🤖 Starting Web Auto Server v%s\n", version)
	}
	fmt.Println("==================================================")

	// Ensure Desktop shortcut exists when running in app mode
	if launchDesktopApp {
		_ = desktop.EnsureDesktopShortcut("")
	}

	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	// C15: Multi-session and environment overrides
	if sessionName == "" {
		sessionName = os.Getenv("WA_SESSION_NAME")
	}
	if customPort <= 0 {
		if envPort := os.Getenv("WA_PORT"); envPort != "" {
			if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
				customPort = p
			}
		}
	}
	if customPort > 0 {
		cfg.Server.Port = customPort
	}

	sessionSuffix := ""
	if sessionName != "" && sessionName != "default" {
		safeSession := regexp.MustCompile(`[^a-zA-Z0-9_\-]`).ReplaceAllString(sessionName, "")
		if safeSession != "" {
			sessionSuffix = "-" + safeSession
			cfg.WhatsApp.SessionName = safeSession
			cfg.WhatsApp.DBPath = fmt.Sprintf("whatsapp%s.db", sessionSuffix)
			fmt.Printf("📱 Multi-Session Profile: %s (DB: %s, Port: %d)\n", safeSession, cfg.WhatsApp.DBPath, cfg.Server.Port)
		}
	}

	contactsFile := fmt.Sprintf("contacts%s.json", sessionSuffix)
	templatesFile := fmt.Sprintf("templates%s.json", sessionSuffix)
	messagesFile := fmt.Sprintf("messages%s.json", sessionSuffix)
	scheduledFile := fmt.Sprintf("scheduled%s.json", sessionSuffix)

	genPass, isNew := cfg.EnsureAdminPassword()
	if isNew {
		fmt.Println()
		fmt.Println("==========================================================================")
		fmt.Println("  [SECURITY NOTICE] FIRST-TIME RUN: MASTER PASSWORD GENERATED")
		fmt.Println("==========================================================================")
		fmt.Println("  No master password was configured in config.yaml.")
		fmt.Println("  A secure random password has been generated for your first login:")
		fmt.Println()
		fmt.Printf("      Master Password:   %s\n", genPass)
		fmt.Println()
		fmt.Printf("  >> Web Dashboard:      http://%s:%d/login\n", cfg.Server.Host, cfg.Server.Port)
		fmt.Println("  >> You will be prompted to set your permanent password on first login.")
		fmt.Println("==========================================================================")
		fmt.Println()
	} else if cfg.Server.MustChangePassword {
		fmt.Println()
		fmt.Println("[SECURITY NOTICE] Master password change is required upon login.")
		fmt.Printf(">> Dashboard Login: http://%s:%d/login\n", cfg.Server.Host, cfg.Server.Port)
		fmt.Println()
	}

	waClient, err := client.NewWhatsAppClient(cfg)
	if err != nil {
		fmt.Printf("Error initializing WhatsApp client: %v\n", err)
		os.Exit(1)
	}

	// Initialize contact, template, and history stores
	contactStore, err := contacts.NewStore(contactsFile)
	if err != nil {
		fmt.Printf("Warning: contacts store error: %v\n", err)
	}

	templateStore, err := templates.NewStore(templatesFile)
	if err != nil {
		fmt.Printf("Warning: template store error: %v\n", err)
	}

	histStore, err := history.NewStore(messagesFile, 2000)
	if err != nil {
		fmt.Printf("Warning: history store error: %v\n", err)
	}

	// Initialize AI smart reply engine
	aiEngine := ai.NewEngine(ai.Config{
		Enabled:      cfg.AI.Enabled,
		APIKey:       cfg.AI.APIKey,
		Model:        cfg.AI.Model,
		SystemPrompt: cfg.AI.SystemPrompt,
	})

	// Initialize message scheduler
	msgScheduler, err := scheduler.NewScheduler(scheduledFile, waClient, contactStore, histStore)
	if err != nil {
		fmt.Printf("Warning: scheduler store error: %v\n", err)
	}

	// Context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if msgScheduler != nil {
		msgScheduler.Start(ctx)
	}

	// Initialize bot engine
	engine := bot.NewEngine(waClient, cfg, contactStore, histStore, aiEngine)
	engine.Start()

	// Initialize HTTP server
	httpSrv := server.NewServer(cfg, waClient, contactStore, templateStore, histStore, msgScheduler, aiEngine)
	httpSrv.SetBotEngine(engine)

	// Connect WhatsApp client
	if err := waClient.Start(ctx); err != nil {
		fmt.Printf("Error starting WhatsApp client: %v\n", err)
		os.Exit(1)
	}

	// Note: User contacts and CRM history are preserved across restarts and unlinked states


	// Background migration: resolve any historical messages or contacts with LIDs to real phone numbers
	go func() {
		time.Sleep(2 * time.Second)
		resolver := func(target string) string {
			return waClient.ResolveString(context.Background(), target)
		}
		nameResolver := func(target string) string {
			return waClient.GetContactName(context.Background(), target)
		}
		if histStore != nil {
			histStore.FixPhoneNumbers(resolver, nameResolver)
		}
		if contactStore != nil {
			contactStore.FixPhoneNumbers(resolver, nameResolver)
		}
	}()

	// Start HTTP server in background
	go func() {
		if err := httpSrv.Start(); err != nil {
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	// Listen for termination signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// If launchDesktopApp is requested, wait for server and open native standalone window
	if launchDesktopApp {
		go func() {
			appURL := fmt.Sprintf("http://127.0.0.1:%d", cfg.Server.Port)
			if desktop.WaitForServer(appURL+"/login", 10*time.Second) {
				fmt.Printf("[Desktop App] Opening application window: %s\n", appURL)
				_, err := desktop.LaunchAppWindow(appURL)
				if err != nil {
					fmt.Printf("[Desktop App] Note: %v. You can open %s directly in your browser.\n", err, appURL)
				}
			}
		}()
	}

	if launchDesktopApp {
		fmt.Println("[System] Application running. Press Ctrl+C in this console to stop.")
	} else {
		fmt.Println("[System] Automation server running. Press Ctrl+C in this console to stop.")
	}
	<-sigChan

	fmt.Println("\n[System] Graceful shutdown initiated...")
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if msgScheduler != nil {
		msgScheduler.Stop()
	}
	_ = httpSrv.Stop(shutdownCtx)
	waClient.Stop()
	fmt.Println("[System] Shutdown complete.")
}

func runSend(cfgPath, to, msg string) {
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	waClient, err := client.NewWhatsAppClient(cfg)
	if err != nil {
		fmt.Printf("Error initializing WhatsApp client: %v\n", err)
		os.Exit(1)
	}
	defer waClient.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := waClient.Start(ctx); err != nil {
		fmt.Printf("Error connecting: %v\n", err)
		os.Exit(1)
	}

	// Wait up to 5 seconds for connection
	for i := 0; i < 10; i++ {
		st := waClient.GetStatus()
		if st.Connected {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	msgID, err := waClient.SendTextMessage(ctx, to, msg)
	if err != nil {
		fmt.Printf("❌ Failed to send message to %s: %v\n", to, err)
		os.Exit(1)
	}

	fmt.Printf("✅ Message dispatched successfully to %s! ID: %s\n", to, msgID)
}

func runBroadcast(cfgPath, csvFile, template string, minDelay, maxDelay int) {
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	contacts, err := campaign.LoadContactsFromCSV(csvFile)
	if err != nil {
		fmt.Printf("Error loading contacts from %s: %v\n", csvFile, err)
		os.Exit(1)
	}

	fmt.Printf("Loaded %d contacts from %s\n", len(contacts), csvFile)

	waClient, err := client.NewWhatsAppClient(cfg)
	if err != nil {
		fmt.Printf("Error initializing WhatsApp client: %v\n", err)
		os.Exit(1)
	}
	defer waClient.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Println("\n[Broadcast] Cancellation received, stopping broadcast...")
		cancel()
	}()

	if err := waClient.Start(ctx); err != nil {
		fmt.Printf("Error connecting: %v\n", err)
		os.Exit(1)
	}

	// Wait for connection
	for i := 0; i < 10; i++ {
		st := waClient.GetStatus()
		if st.Connected {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	bCfg := campaign.BroadcastConfig{
		MinDelaySeconds: minDelay,
		MaxDelaySeconds: maxDelay,
		BatchSize:       20,
		BatchPauseSec:   30,
	}

	broadcaster := campaign.NewBroadcaster(waClient)
	result := broadcaster.RunBroadcast(ctx, contacts, template, bCfg, func(curr, total, succ, fail int, phone string, err error) {
		if err != nil {
			fmt.Printf("[%d/%d] ❌ %s: %v\n", curr, total, phone, err)
		} else {
			fmt.Printf("[%d/%d] ✅ %s\n", curr, total, phone)
		}
	})

	fmt.Println("\n================ Broadcast Summary ================")
	fmt.Printf("Total Contacts: %d\n", result.TotalContacts)
	fmt.Printf("Successfully Sent: %d\n", result.SuccessCount)
	fmt.Printf("Failed: %d\n", result.FailureCount)
	fmt.Printf("Duration: %v\n", result.Duration)
	fmt.Println("===================================================")
}

func runPair(cfgPath, phone string) {
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	cfg.WhatsApp.PairPhone = phone

	waClient, err := client.NewWhatsAppClient(cfg)
	if err != nil {
		fmt.Printf("Error initializing WhatsApp client: %v\n", err)
		os.Exit(1)
	}
	defer waClient.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := waClient.Start(ctx); err != nil {
		fmt.Printf("Error starting client: %v\n", err)
		os.Exit(1)
	}

	// Wait for pair code
	for i := 0; i < 30; i++ {
		st := waClient.GetStatus()
		if st.PairCode != "" {
			fmt.Printf("\n>>> Pairing Code: %s <<<\n", st.PairCode)
			break
		}
		time.Sleep(1 * time.Second)
	}
}

func runStatus(cfgPath string) {
	cfg, err := config.LoadConfig(cfgPath)
	if err != nil {
		fmt.Printf("Error loading config: %v\n", err)
		os.Exit(1)
	}

	waClient, err := client.NewWhatsAppClient(cfg)
	if err != nil {
		fmt.Printf("Error reading store: %v\n", err)
		os.Exit(1)
	}
	defer waClient.Stop()

	status := waClient.GetStatus()
	fmt.Println("WhatsApp Automation Session Status:")
	fmt.Printf("• Database Path: %s\n", cfg.WhatsApp.DBPath)
	fmt.Printf("• Logged In:     %t\n", status.LoggedIn)
	if status.JID != "" {
		fmt.Printf("• Account JID:   %s\n", status.JID)
		fmt.Printf("• Phone Number:  %s\n", status.Phone)
	}
	if status.PushName != "" {
		fmt.Printf("• Push Name:     %s\n", status.PushName)
	}
	fmt.Printf("• Rules Configured: %d\n", len(cfg.GetRules()))
}
