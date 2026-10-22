package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	"whatsapp-automation/pkg/securestore"
	"whatsapp-automation/pkg/security"
)

type ServerConfig struct {
	Host               string   `yaml:"host" json:"host"`
	Port               int      `yaml:"port" json:"port"`
	APIKey             string   `yaml:"api_key" json:"api_key"`
	AdminPassword      string   `yaml:"admin_password" json:"admin_password"`
	OperatorPassword   string   `yaml:"operator_password" json:"operator_password"`
	MustChangePassword bool     `yaml:"must_change_password" json:"must_change_password"`
	AllowedHosts       []string `yaml:"allowed_hosts" json:"allowed_hosts"`
	TrustedProxies     []string `yaml:"trusted_proxies" json:"trusted_proxies"`
}

// CloudAPIConfig holds this install's own connection to Meta's WhatsApp Business
// Platform (Cloud API). Every Pulse install, and every -session profile, uses the
// owner's own Meta app and number. The access token and app secret are stored sealed
// with the Windows account's key protection and are never sent to the browser.
type CloudAPIConfig struct {
	PhoneNumberID     string `yaml:"phone_number_id" json:"phone_number_id"`
	BusinessAccountID string `yaml:"business_account_id" json:"business_account_id"`
	AccessToken       string `yaml:"access_token" json:"-"`
	AppSecret         string `yaml:"app_secret" json:"-"`
	VerifyToken       string `yaml:"verify_token" json:"verify_token"`
	APIVersion        string `yaml:"api_version" json:"api_version"`
	// PublicURL is the https address of this computer's tunnel or reverse proxy; Meta
	// sends incoming messages to PublicURL + WebhookPath.
	PublicURL string `yaml:"public_url" json:"public_url"`
}

// DefaultGraphVersion is the Graph API version used when none is configured.
const DefaultGraphVersion = "v23.0"

// WebhookPath is where Meta delivers incoming messages and delivery updates.
const WebhookPath = "/cloud/webhook"

// Ready reports whether the number and access token are entered.
func (c CloudAPIConfig) Ready() bool { return c.PhoneNumberID != "" && c.AccessToken != "" }

// WebhookURL is the address to paste into Meta's webhook settings, or "" when the
// public address is not set yet.
func (c CloudAPIConfig) WebhookURL() string {
	if c.PublicURL == "" {
		return ""
	}
	return strings.TrimRight(c.PublicURL, "/") + WebhookPath
}

// StorageConfig controls where customer data lives and how long it is kept.
type StorageConfig struct {
	DataDir       string `yaml:"data_dir" json:"data_dir"`
	Encrypt       bool   `yaml:"encrypt" json:"encrypt"`
	RetentionDays int    `yaml:"retention_days" json:"retention_days"`
	MaxMessages   int    `yaml:"max_messages" json:"max_messages"`
	// AutoBackup saves an encrypted backup every day and keeps the last 7 (FT22).
	AutoBackup bool `yaml:"auto_backup" json:"auto_backup"`
	// BackupCopyDir optionally receives a second copy (a USB drive or a synced folder).
	BackupCopyDir string `yaml:"backup_copy_dir" json:"backup_copy_dir"`
}

// BusinessConfig holds the owner's real details. Default replies refer to these
// through {{business_*}} variables instead of shipping invented text.
type BusinessConfig struct {
	Name     string `yaml:"name" json:"name"`
	Hours    string `yaml:"hours" json:"hours"`
	Address  string `yaml:"address" json:"address"`
	Website  string `yaml:"website" json:"website"`
	Email    string `yaml:"email" json:"email"`
	Services string `yaml:"services" json:"services"`
	Prices   string `yaml:"prices" json:"prices"`
}

type AutomationConfig struct {
	Enabled            bool     `yaml:"enabled" json:"enabled"`
	AutoRead           bool     `yaml:"auto_read" json:"auto_read"`
	TypingSimulation   bool     `yaml:"typing_simulation" json:"typing_simulation"`
	TypingDurationMs   int      `yaml:"typing_duration_ms" json:"typing_duration_ms"`
	CooldownSeconds    int      `yaml:"cooldown_seconds" json:"cooldown_seconds"`
	IgnoreGroups       bool     `yaml:"ignore_groups" json:"ignore_groups"`
	IgnoreBroadcasts   bool     `yaml:"ignore_broadcasts" json:"ignore_broadcasts"`
	IgnoreSelf         bool     `yaml:"ignore_self" json:"ignore_self"`
	BlacklistPhones    []string `yaml:"blacklist_phones" json:"blacklist_phones"`
	DefaultReply       string   `yaml:"default_reply" json:"default_reply"`
	EnableDefaultReply bool     `yaml:"enable_default_reply" json:"enable_default_reply"`
	DailyLimit         int      `yaml:"daily_limit" json:"daily_limit"`
	WarmupMode         bool     `yaml:"warmup_mode" json:"warmup_mode"`

	Pace                   string `yaml:"pace" json:"pace"`
	BroadcastMinDelay      int    `yaml:"broadcast_min_delay" json:"broadcast_min_delay"`
	BroadcastMaxDelay      int    `yaml:"broadcast_max_delay" json:"broadcast_max_delay"`
	BatchSize              int    `yaml:"batch_size" json:"batch_size"`
	BatchPauseSeconds      int    `yaml:"batch_pause_seconds" json:"batch_pause_seconds"`
	MaxRepliesPerContactHr int    `yaml:"max_replies_per_contact_hour" json:"max_replies_per_contact_hour"`

	AllowListOnly bool     `yaml:"allow_list_only" json:"allow_list_only"`
	AllowList     []string `yaml:"allow_list" json:"allow_list"`
	DryRun        bool     `yaml:"dry_run" json:"dry_run"`

	RequireOptIn       bool   `yaml:"require_opt_in" json:"require_opt_in"`
	Enforce24hWindow   bool   `yaml:"enforce_24h_window" json:"enforce_24h_window"`
	HandoverPauseHours int    `yaml:"handover_pause_hours" json:"handover_pause_hours"`
	HandoverReply      string `yaml:"handover_reply" json:"handover_reply"`
	OptOutReply        string `yaml:"opt_out_reply" json:"opt_out_reply"`
	OptInReply         string `yaml:"opt_in_reply" json:"opt_in_reply"`
	// MissingDetailReply is sent when a rule matched but needs a business detail that is
	// still empty (for example prices), so the customer is never left without an answer.
	MissingDetailReply string `yaml:"missing_detail_reply" json:"missing_detail_reply"`
}

// DefaultMissingDetailReply is used when no missing-detail reply is configured.
const DefaultMissingDetailReply = "Thank you for asking. A member of our team will reply to you here shortly."

// DaySchedule is the opening window for one weekday ("09:00"-"18:00").
type DaySchedule struct {
	Day   string `yaml:"day" json:"day"`
	Open  bool   `yaml:"open" json:"open"`
	Start string `yaml:"start" json:"start"`
	End   string `yaml:"end" json:"end"`
}

type BusinessHoursConfig struct {
	Enabled   bool          `yaml:"enabled" json:"enabled"`
	Timezone  string        `yaml:"timezone" json:"timezone"`
	AwayReply string        `yaml:"away_reply" json:"away_reply"`
	Days      []DaySchedule `yaml:"days" json:"days"`
	Holidays  []string      `yaml:"holidays" json:"holidays"`

	LegacyStartHour int `yaml:"start_hour,omitempty" json:"-"`
	LegacyEndHour   int `yaml:"end_hour,omitempty" json:"-"`
}

type NotificationsConfig struct {
	Desktop bool `yaml:"desktop" json:"desktop"`
}

type WebhookConfig struct {
	Enabled        bool   `yaml:"enabled" json:"enabled"`
	URL            string `yaml:"url" json:"url"`
	Secret         string `yaml:"secret" json:"secret"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"timeout_seconds"`
	MaxRetries     int    `yaml:"max_retries" json:"max_retries"`
}

// SetupState tracks the first-run checklist.
type SetupState struct {
	RiskAcknowledgedAt *time.Time `yaml:"risk_acknowledged_at,omitempty" json:"risk_acknowledged_at,omitempty"`
	TestMessageSentAt  *time.Time `yaml:"test_message_sent_at,omitempty" json:"test_message_sent_at,omitempty"`
	// PasswordResetAt is when the reset tool last replaced the admin password; the
	// dashboard warns about it for 7 days so an unexpected reset is noticed.
	PasswordResetAt *time.Time `yaml:"password_reset_at,omitempty" json:"password_reset_at,omitempty"`
	// WebhookSeenAt is when Meta first reached the webhook, which proves incoming
	// messages can arrive.
	WebhookSeenAt *time.Time `yaml:"webhook_seen_at,omitempty" json:"webhook_seen_at,omitempty"`
}

// Rule is an automated keyword response. Action "handover" also pauses the bot and alerts staff.
type Rule struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"`
	Pattern     string `yaml:"pattern" json:"pattern"`
	Reply       string `yaml:"reply" json:"reply"`
	Description string `yaml:"description" json:"description"`
	Enabled     bool   `yaml:"enabled" json:"enabled"`
	Priority    int    `yaml:"priority" json:"priority"`
	Action      string `yaml:"action,omitempty" json:"action,omitempty"`
}

// Config is the full application configuration. Read it through Snapshot and
// change it through Update so the bot and the dashboard never race.
type Config struct {
	mu            *sync.RWMutex       `yaml:"-" json:"-"`
	FilePath      string              `yaml:"-" json:"-"`
	Server        ServerConfig        `yaml:"server" json:"server"`
	CloudAPI      CloudAPIConfig      `yaml:"cloud_api" json:"cloud_api"`
	Storage       StorageConfig       `yaml:"storage" json:"storage"`
	Business      BusinessConfig      `yaml:"business" json:"business"`
	Automation    AutomationConfig    `yaml:"automation" json:"automation"`
	BusinessHours BusinessHoursConfig `yaml:"business_hours" json:"business_hours"`
	Notifications NotificationsConfig `yaml:"notifications" json:"notifications"`
	Webhook       WebhookConfig       `yaml:"webhook" json:"webhook"`
	Setup         SetupState          `yaml:"setup" json:"setup"`
	Rules         []Rule              `yaml:"rules" json:"rules"`

	// Runtime override from the -port flag; never written to the file.
	RuntimePort int `yaml:"-" json:"-"`
}

// ListenPort is the dashboard port in use (flag override or configured).
func (c Config) ListenPort() int {
	if c.RuntimePort > 0 {
		return c.RuntimePort
	}
	return c.Server.Port
}

// SetRuntime applies the command-line port without saving it.
func (c *Config) SetRuntime(port int) {
	c.mu.Lock()
	c.RuntimePort = port
	c.mu.Unlock()
}

// CloudToken returns the Cloud API access token, unsealed.
func (c *Config) CloudToken() (string, error) {
	c.mu.RLock()
	v := c.CloudAPI.AccessToken
	c.mu.RUnlock()
	return securestore.OpenString(v)
}

// CloudAppSecret returns the Meta app secret used to check webhook signatures, unsealed.
func (c *Config) CloudAppSecret() (string, error) {
	c.mu.RLock()
	v := c.CloudAPI.AppSecret
	c.mu.RUnlock()
	return securestore.OpenString(v)
}

var Weekdays = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

// Pace presets: careful suits new numbers, fast suits numbers with a long clean history.
type PacePreset struct {
	Name, Label, Explain                      string
	DailyLimit, Cooldown, TypingMs            int
	MinDelay, MaxDelay, BatchSize, BatchPause int
	RepliesPerContactHour                     int
	Warmup                                    bool
}

var PacePresets = []PacePreset{
	{"careful", "Careful", "For new numbers or numbers connected less than two weeks ago.", 100, 10, 2500, 8, 20, 10, 120, 6, true},
	{"balanced", "Balanced", "For numbers with a few weeks of normal two-way conversations.", 300, 5, 1500, 5, 12, 20, 60, 10, false},
	{"fast", "Fast", "Only for numbers with months of history and almost no blocks or reports.", 800, 3, 1000, 3, 7, 30, 45, 15, false},
}

// PresetByName returns the preset with name, or false.
func PresetByName(name string) (PacePreset, bool) {
	for _, p := range PacePresets {
		if p.Name == name {
			return p, true
		}
	}
	return PacePreset{}, false
}

// ApplyPace copies a preset's values into the automation settings.
func (a *AutomationConfig) ApplyPace(p PacePreset) {
	a.Pace = p.Name
	a.DailyLimit = p.DailyLimit
	a.CooldownSeconds = p.Cooldown
	a.TypingDurationMs = p.TypingMs
	a.BroadcastMinDelay = p.MinDelay
	a.BroadcastMaxDelay = p.MaxDelay
	a.BatchSize = p.BatchSize
	a.BatchPauseSeconds = p.BatchPause
	a.MaxRepliesPerContactHr = p.RepliesPerContactHour
	a.WarmupMode = p.Warmup
}

func defaultDays() []DaySchedule {
	days := make([]DaySchedule, 0, 7)
	for _, d := range Weekdays {
		days = append(days, DaySchedule{Day: d, Open: d != "sun", Start: "09:00", End: "18:00"})
	}
	return days
}

// DefaultRules are generic and refer to the business profile; nothing is invented.
func DefaultRules() []Rule {
	return []Rule{
		{Name: "greeting_menu", Type: "exact", Priority: 10, Enabled: true,
			Pattern:     "hi, hii, hello, hey, hlo, namaste, menu, good morning, good afternoon, good evening",
			Reply:       "{{greeting}} {{first_name}}, thank you for contacting {{business_name}}. Reply with a number:\n1. Services\n2. Prices\n3. Opening hours and address\n4. Talk to a person\n\nReply MENU at any time to see these options again.",
			Description: "Greeting and main menu"},
		{Name: "option_services", Type: "exact", Priority: 5, Enabled: true,
			Pattern:     "1, services, service",
			Reply:       "{{business_services}}\n\nReply 2 for prices or 4 to talk to a person.",
			Description: "Menu option 1: services"},
		{Name: "option_prices", Type: "exact", Priority: 5, Enabled: true,
			Pattern:     "2, prices, price, pricing, rates",
			Reply:       "{{business_prices}}\n\nReply 4 to talk to a person.",
			Description: "Menu option 2: prices"},
		{Name: "option_hours", Type: "exact", Priority: 5, Enabled: true,
			Pattern:     "3, hours, timing, timings, address, location",
			Reply:       "Opening hours: {{business_hours}}\nAddress: {{business_address}}\n\nReply MENU to see the options again.",
			Description: "Menu option 3: opening hours and address"},
		{Name: "option_agent", Type: "exact", Priority: 5, Enabled: true, Action: "handover",
			Pattern:     "4",
			Reply:       "Thank you, {{first_name}}. A member of our team will reply here as soon as possible.",
			Description: "Menu option 4: hand the chat to a person"},
		{Name: "thanks", Type: "exact", Priority: 1, Enabled: true,
			Pattern:     "thanks, thank you, thx, ty, dhanyavad, shukriya",
			Reply:       "You are welcome, {{first_name}}.",
			Description: "Reply to thanks"},
		{Name: "goodbye", Type: "exact", Priority: 1, Enabled: true,
			Pattern:     "bye, goodbye, see you",
			Reply:       "Goodbye {{first_name}}, have a good day.",
			Description: "Reply to goodbye"},
		{Name: "help", Type: "exact", Priority: 1, Enabled: true,
			Pattern:     "help, !help",
			Reply:       "You can reply:\nMENU to see the options\nAGENT to talk to a person\nSTOP to stop receiving messages",
			Description: "Lists the keywords customers can use"},
	}
}

// DefaultConfig returns safe defaults for a brand-new install. Automation stays off
// until the setup checklist is complete.
func DefaultConfig() *Config {
	c := &Config{
		mu:       &sync.RWMutex{},
		Server:   ServerConfig{Host: "127.0.0.1", Port: 8085, MustChangePassword: true},
		CloudAPI: CloudAPIConfig{APIVersion: DefaultGraphVersion},
		Storage:  StorageConfig{Encrypt: true, RetentionDays: 180, MaxMessages: 50000, AutoBackup: true},
		Automation: AutomationConfig{
			AutoRead: true, TypingSimulation: true,
			IgnoreGroups: true, IgnoreBroadcasts: true, IgnoreSelf: true,
			BlacklistPhones:    []string{},
			AllowList:          []string{},
			EnableDefaultReply: true,
			DefaultReply:       "Sorry, I did not understand that. Reply MENU to see the options, or AGENT to talk to a person.",
			RequireOptIn:       true,
			Enforce24hWindow:   true,
			HandoverPauseHours: 24,
			HandoverReply:      "Thank you, {{first_name}}. A member of our team will reply here as soon as possible.",
			OptOutReply:        "You will not receive any more automated messages from us. Reply START if you change your mind.",
			OptInReply:         "You are subscribed again. Reply MENU to see the options.",
			MissingDetailReply: DefaultMissingDetailReply,
		},
		BusinessHours: BusinessHoursConfig{
			Timezone:  "Local",
			AwayReply: "Thank you for your message. We are closed right now. Our opening hours are {{business_hours}}. We will reply when we open.",
			Days:      defaultDays(),
			Holidays:  []string{},
		},
		Notifications: NotificationsConfig{Desktop: true},
		Webhook:       WebhookConfig{TimeoutSeconds: 8, MaxRetries: 5},
		Rules:         DefaultRules(),
	}
	careful, _ := PresetByName("careful")
	c.Automation.ApplyPace(careful)
	return c
}

// LoadConfig reads path, creating it with defaults when missing.
func LoadConfig(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := DefaultConfig()
		cfg.FilePath = path
		if err := cfg.Save(); err != nil {
			return nil, fmt.Errorf("cannot create %s: %w", path, err)
		}
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		// An empty file (for example after a power cut) is damage, not a fresh install:
		// use the backup so rules, business details and the password survive.
		if bak, bErr := os.ReadFile(path + ".bak"); bErr == nil && len(strings.TrimSpace(string(bak))) > 0 {
			fmt.Printf("[Config] %s was empty; restored the settings from its backup.\n", path)
			data = bak
		}
	}
	cfg := loadBase()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		bak, bErr := os.ReadFile(path + ".bak")
		if bErr != nil {
			return nil, fmt.Errorf("cannot parse %s: %w", path, err)
		}
		cfg = loadBase()
		if err2 := yaml.Unmarshal(bak, cfg); err2 != nil {
			return nil, fmt.Errorf("cannot parse %s or its backup: %w", path, err)
		}
	}
	if cfg.Rules == nil {
		cfg.Rules = DefaultRules()
	}
	cfg.FilePath = path
	// Files from older versions have no pace preset: keep their numbers, fill the gaps.
	if p, ok := PresetByName(cfg.Automation.Pace); ok {
		cfg.Automation.fillPace(p)
	} else if cfg.Automation.Pace == "" {
		balanced, _ := PresetByName("balanced")
		cfg.Automation.fillPace(balanced)
		cfg.Automation.Pace = "custom"
	}
	migrated := cfg.hashPlaintextSecrets()
	cfg.normalize()
	if migrated {
		// Older files kept the passwords or API key as readable text. Save twice so the
		// backup copy (config.yaml.bak) no longer holds the readable version either.
		if err := cfg.Save(); err == nil {
			_ = cfg.Save()
			fmt.Println("[Config] Passwords, keys and tokens in the settings file are now stored scrambled.")
		}
	}
	return cfg, nil
}

const apiKeyHashPrefix = "sha256:"

// HashAPIKey returns the stored form of an API key. Pulse only ever compares the key,
// so the settings file keeps a SHA-256 hash; the key itself is shown once, when created.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return apiKeyHashPrefix + hex.EncodeToString(sum[:])
}

// CheckAPIKey reports whether got is the configured API key.
func (c *Config) CheckAPIKey(got string) bool {
	c.mu.RLock()
	stored := c.Server.APIKey
	c.mu.RUnlock()
	if stored == "" || got == "" {
		return false
	}
	return security.ConstantTimeEqual(HashAPIKey(got), stored)
}

// hashPlaintextSecrets replaces readable passwords and API keys (from older files or
// hand edits) with their hashes. It reports whether anything changed. The literal
// default "admin" is left for EnsureAdminPassword, which replaces it on start.
func (c *Config) hashPlaintextSecrets() bool {
	changed := false
	if k := c.Server.APIKey; k != "" && !strings.HasPrefix(k, apiKeyHashPrefix) {
		c.Server.APIKey, changed = HashAPIKey(k), true
	}
	// A token or app secret typed into the file by hand is sealed at once.
	for _, p := range []*string{&c.CloudAPI.AccessToken, &c.CloudAPI.AppSecret} {
		if v := strings.TrimSpace(*p); v != "" && !securestore.IsSealedString(v) {
			if sealed, err := securestore.SealString(v); err == nil {
				*p, changed = sealed, true
			}
		}
	}
	for _, p := range []*string{&c.Server.AdminPassword, &c.Server.OperatorPassword} {
		v := *p
		if strings.TrimSpace(v) == "" || isHash(v) || (p == &c.Server.AdminPassword && v == "admin") {
			continue
		}
		if h, err := bcrypt.GenerateFromPassword([]byte(v), bcrypt.DefaultCost); err == nil {
			*p, changed = string(h), true
		}
	}
	return changed
}

// loadBase is the starting point for reading a file: defaults, but with the pacing
// values, schedule and rules cleared so values from older files are detected and kept.
func loadBase() *Config {
	c := DefaultConfig()
	c.Rules = nil
	c.BusinessHours.Days = nil
	a := &c.Automation
	a.Pace, a.DailyLimit, a.CooldownSeconds, a.TypingDurationMs = "", 0, 0, 0
	a.BroadcastMinDelay, a.BroadcastMaxDelay, a.BatchSize, a.BatchPauseSeconds, a.MaxRepliesPerContactHr = 0, 0, 0, 0, 0
	a.WarmupMode = false
	return c
}

// fillPace sets any pacing value that is missing from preset p.
func (a *AutomationConfig) fillPace(p PacePreset) {
	if a.DailyLimit <= 0 {
		a.DailyLimit = p.DailyLimit
	}
	if a.CooldownSeconds <= 0 {
		a.CooldownSeconds = p.Cooldown
	}
	if a.TypingDurationMs <= 0 {
		a.TypingDurationMs = p.TypingMs
	}
	if a.BroadcastMinDelay <= 0 {
		a.BroadcastMinDelay = p.MinDelay
	}
	if a.BroadcastMaxDelay <= 0 {
		a.BroadcastMaxDelay = p.MaxDelay
	}
	if a.BatchSize <= 0 {
		a.BatchSize = p.BatchSize
	}
	if a.BatchPauseSeconds <= 0 {
		a.BatchPauseSeconds = p.BatchPause
	}
	if a.MaxRepliesPerContactHr <= 0 {
		a.MaxRepliesPerContactHr = p.RepliesPerContactHour
	}
}

// normalize repairs missing or out-of-range values, including configs from older versions.
func (c *Config) normalize() {
	c.hashPlaintextSecrets()
	if c.Server.Host == "" {
		c.Server.Host = "127.0.0.1"
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		c.Server.Port = 8085
	}
	ca := &c.CloudAPI
	ca.PhoneNumberID, ca.BusinessAccountID = strings.TrimSpace(ca.PhoneNumberID), strings.TrimSpace(ca.BusinessAccountID)
	ca.PublicURL = strings.TrimRight(strings.TrimSpace(ca.PublicURL), "/")
	if !ValidGraphVersion(ca.APIVersion) {
		ca.APIVersion = DefaultGraphVersion
	}
	if c.Storage.RetentionDays < 0 {
		c.Storage.RetentionDays = 0
	}
	if c.Storage.MaxMessages <= 0 {
		c.Storage.MaxMessages = 50000
	}
	a := &c.Automation
	if a.DailyLimit <= 0 {
		a.DailyLimit = 100
	}
	if strings.TrimSpace(a.MissingDetailReply) == "" {
		a.MissingDetailReply = DefaultMissingDetailReply
	}
	if a.BroadcastMinDelay < 2 {
		a.BroadcastMinDelay = 2
	}
	if a.BroadcastMaxDelay < a.BroadcastMinDelay {
		a.BroadcastMaxDelay = a.BroadcastMinDelay + 5
	}
	if a.MaxRepliesPerContactHr <= 0 {
		a.MaxRepliesPerContactHr = 10
	}
	if a.HandoverPauseHours <= 0 {
		a.HandoverPauseHours = 24
	}
	if a.BlacklistPhones == nil {
		a.BlacklistPhones = []string{}
	}
	if a.AllowList == nil {
		a.AllowList = []string{}
	}
	d := DefaultConfig()
	if strings.TrimSpace(a.HandoverReply) == "" {
		a.HandoverReply = d.Automation.HandoverReply
	}
	if strings.TrimSpace(a.OptOutReply) == "" {
		a.OptOutReply = d.Automation.OptOutReply
	}
	if strings.TrimSpace(a.OptInReply) == "" {
		a.OptInReply = d.Automation.OptInReply
	}
	bh := &c.BusinessHours
	if len(bh.Days) != 7 {
		days := defaultDays()
		if bh.LegacyStartHour > 0 || bh.LegacyEndHour > 0 {
			for i := range days {
				days[i].Open = true
				days[i].Start = fmt.Sprintf("%02d:00", clamp(bh.LegacyStartHour, 0, 23))
				days[i].End = fmt.Sprintf("%02d:00", clamp(bh.LegacyEndHour, 1, 24)%24)
				if bh.LegacyEndHour >= 24 {
					days[i].End = "23:59"
				}
			}
		}
		bh.Days = days
	}
	bh.LegacyStartHour, bh.LegacyEndHour = 0, 0
	if bh.Holidays == nil {
		bh.Holidays = []string{}
	}
	if bh.Timezone == "" {
		bh.Timezone = "Local"
	}
	if c.Webhook.TimeoutSeconds <= 0 {
		c.Webhook.TimeoutSeconds = 8
	}
	if c.Webhook.MaxRetries <= 0 {
		c.Webhook.MaxRetries = 5
	}
	for i := range c.Rules {
		if c.Rules[i].Type == "" {
			c.Rules[i].Type = "exact"
		}
	}
}

var graphVersionRe = regexp.MustCompile(`^v\d{1,2}\.\d$`)

// ValidGraphVersion reports whether v looks like a Graph API version such as v23.0.
func ValidGraphVersion(v string) bool { return graphVersionRe.MatchString(v) }

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

var saveMu sync.Mutex

// Save writes the config atomically. A config without a file path (tests) is not written.
func (c *Config) Save() error {
	saveMu.Lock()
	defer saveMu.Unlock()
	c.mu.RLock()
	path := c.FilePath
	data, err := yaml.Marshal(c)
	c.mu.RUnlock()
	if path == "" {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cannot serialize config: %w", err)
	}
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		if old, err := os.ReadFile(path); err == nil {
			if os.WriteFile(path+".bak", old, 0600) == nil {
				securestore.RestrictToUser(path + ".bak")
			}
		}
	}
	tmp := fmt.Sprintf("%s.%d.tmp", path, time.Now().UnixNano())
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	serr := f.Sync() // on disk before the rename, so a power cut cannot leave an empty file
	cerr := f.Close()
	if werr != nil || serr != nil || cerr != nil {
		_ = os.Remove(tmp)
		return errors.Join(werr, serr, cerr)
	}
	// Only this Windows user may read the file: it holds the webhook secret and the
	// password hashes. Done before the rename so the final file is never left open.
	securestore.RestrictToUser(tmp)
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Snapshot returns a deep copy that callers may read freely.
func (c *Config) Snapshot() Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.copyLocked()
}

func (c *Config) copyLocked() Config {
	s := Config{
		mu: c.mu, FilePath: c.FilePath, Server: c.Server, CloudAPI: c.CloudAPI, Storage: c.Storage,
		Business: c.Business, Automation: c.Automation, BusinessHours: c.BusinessHours,
		Notifications: c.Notifications, Webhook: c.Webhook, Setup: c.Setup,
		RuntimePort: c.RuntimePort,
	}
	s.Server.AllowedHosts = append([]string(nil), c.Server.AllowedHosts...)
	s.Server.TrustedProxies = append([]string(nil), c.Server.TrustedProxies...)
	s.Automation.BlacklistPhones = append([]string{}, c.Automation.BlacklistPhones...)
	s.Automation.AllowList = append([]string{}, c.Automation.AllowList...)
	s.BusinessHours.Days = append([]DaySchedule(nil), c.BusinessHours.Days...)
	s.BusinessHours.Holidays = append([]string{}, c.BusinessHours.Holidays...)
	s.Rules = append([]Rule{}, c.Rules...)
	return s
}

// Update applies fn under the write lock and saves the result.
func (c *Config) Update(fn func(*Config) error) error {
	c.mu.Lock()
	before := c.copyLocked()
	if err := fn(c); err != nil {
		c.restoreLocked(before)
		c.mu.Unlock()
		return err
	}
	c.normalize()
	c.mu.Unlock()
	if err := c.Save(); err != nil {
		// Not saved: undo it in memory too, so the app never acts on a setting
		// (such as a shorter retention) that the owner was told failed.
		c.mu.Lock()
		c.restoreLocked(before)
		c.mu.Unlock()
		return fmt.Errorf("The settings could not be saved (%v). Nothing was changed.", err)
	}
	return nil
}

func (c *Config) restoreLocked(s Config) {
	c.Server, c.CloudAPI, c.Storage, c.Business = s.Server, s.CloudAPI, s.Storage, s.Business
	c.Automation, c.BusinessHours, c.Notifications = s.Automation, s.BusinessHours, s.Notifications
	c.Webhook, c.Setup, c.Rules = s.Webhook, s.Setup, s.Rules
}

const mask = "********"

// SafeClone returns a snapshot with every secret masked, for the settings API.
func (c *Config) SafeClone() Config {
	s := c.Snapshot()
	s.Server.AdminPassword = mask
	if s.Server.OperatorPassword != "" {
		s.Server.OperatorPassword = mask
	}
	if s.Server.APIKey != "" {
		s.Server.APIKey = mask
	}
	if s.Webhook.Secret != "" {
		s.Webhook.Secret = mask
	}
	s.CloudAPI.AccessToken, s.CloudAPI.AppSecret = "", "" // never leave Pulse, not even masked
	return s
}

// IsMasked reports whether v is the placeholder returned for secrets.
func IsMasked(v string) bool { return v == mask }

// GenerateRandomPassword returns a readable random password that satisfies the policy.
func GenerateRandomPassword(length int) (string, error) {
	const letters = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz"
	const digits = "23456789"
	const all = letters + digits
	for {
		b := make([]byte, length)
		for i := range b {
			n, err := rand.Int(rand.Reader, big.NewInt(int64(len(all))))
			if err != nil {
				return "", err
			}
			b[i] = all[n.Int64()]
		}
		if security.ValidatePassword(string(b)) == nil {
			return string(b), nil
		}
	}
}

// EnsureAdminPassword generates a one-time password when none is configured.
func (c *Config) EnsureAdminPassword() (string, bool) {
	c.mu.Lock()
	pass := strings.TrimSpace(c.Server.AdminPassword)
	if pass != "" && pass != "admin" {
		c.mu.Unlock()
		return "", false
	}
	gen, err := GenerateRandomPassword(14)
	if err != nil {
		c.mu.Unlock()
		return "", false
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(gen), bcrypt.DefaultCost)
	if err != nil {
		c.mu.Unlock()
		return "", false
	}
	c.Server.AdminPassword = string(hash)
	c.Server.MustChangePassword = true
	c.mu.Unlock()
	_ = c.Save()
	return gen, true
}

// ResetAdminPassword replaces a forgotten admin password with a new one-time password.
func (c *Config) ResetAdminPassword() (string, error) {
	c.mu.Lock()
	c.Server.AdminPassword = ""
	now := time.Now()
	c.Setup.PasswordResetAt = &now
	c.mu.Unlock()
	pass, ok := c.EnsureAdminPassword()
	if !ok {
		return "", errors.New("could not create a new password")
	}
	return pass, nil
}

type UserRole string

const (
	RoleAdmin    UserRole = "admin"
	RoleOperator UserRole = "operator"
)

func isHash(s string) bool {
	return strings.HasPrefix(s, "$2a$") || strings.HasPrefix(s, "$2b$") || strings.HasPrefix(s, "$2y$")
}

// verify checks input against a stored bcrypt hash. Readable passwords are hashed when
// the file is loaded, so a stored value that is not a hash never matches.
func verify(expected, input string) bool {
	if input == "" || !isHash(expected) {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(expected), []byte(input)) == nil
}

// Authenticate returns the role whose password matches input.
func (c *Config) Authenticate(input string) (UserRole, bool) {
	c.mu.RLock()
	admin, op := c.Server.AdminPassword, c.Server.OperatorPassword
	c.mu.RUnlock()
	if verify(admin, input) {
		return RoleAdmin, true
	}
	if verify(op, input) {
		return RoleOperator, true
	}
	return "", false
}

// VerifyPassword checks the admin password.
func (c *Config) VerifyPassword(input string) bool {
	c.mu.RLock()
	admin := c.Server.AdminPassword
	c.mu.RUnlock()
	return verify(admin, input)
}

// SetPassword validates and stores a new admin password.
func (c *Config) SetPassword(newPass string) error {
	p := strings.TrimSpace(newPass)
	if err := security.ValidatePassword(p); err != nil {
		return err
	}
	c.mu.RLock()
	same := verify(c.Server.OperatorPassword, p)
	c.mu.RUnlock()
	if same {
		return errors.New("The admin password must be different from the staff password.")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	// Update undoes the change if it cannot be saved, so the old password keeps working.
	return c.Update(func(x *Config) error {
		x.Server.AdminPassword = string(hash)
		x.Server.MustChangePassword = false
		return nil
	})
}

// SetOperatorPassword sets or (with "") disables the operator login.
func (c *Config) SetOperatorPassword(newPass string) error {
	p := strings.TrimSpace(newPass)
	var stored string
	if p != "" {
		if err := security.ValidatePassword(p); err != nil {
			return err
		}
		c.mu.RLock()
		same := verify(c.Server.AdminPassword, p)
		c.mu.RUnlock()
		if same {
			return errors.New("The staff password must be different from the admin password.")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		stored = string(hash)
	}
	return c.Update(func(x *Config) error { x.Server.OperatorPassword = stored; return nil })
}

// MustChange reports whether the first-login password change is still pending.
func (c *Config) MustChange() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Server.MustChangePassword
}

// IsDefaultPassword reports whether the admin password still needs to be chosen.
func (c *Config) IsDefaultPassword() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	p := c.Server.AdminPassword
	return c.Server.MustChangePassword || p == "" || p == "admin" || (isHash(p) && bcrypt.CompareHashAndPassword([]byte(p), []byte("admin")) == nil)
}

// GetRules returns rules ordered by priority (highest first), keeping file order for ties.
func (c *Config) GetRules() []Rule {
	c.mu.RLock()
	res := append([]Rule{}, c.Rules...)
	c.mu.RUnlock()
	sort.SliceStable(res, func(i, j int) bool { return res[i].Priority > res[j].Priority })
	return res
}

const (
	MaxRules          = 200
	MaxRuleNameLen    = 64
	MaxRulePatternLen = 1000
	MaxRuleReplyLen   = 4000
	MaxRuleDescLen    = 256
)

var ruleNameRe = regexp.MustCompile(`^[A-Za-z0-9 _.\-]+$`)

// ValidateRule normalises and checks a rule.
func ValidateRule(rule *Rule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	rule.Reply = strings.TrimSpace(strings.ReplaceAll(rule.Reply, "\r\n", "\n"))
	rule.Description = strings.TrimSpace(rule.Description)
	switch {
	case rule.Name == "":
		return errors.New("Give the rule a name.")
	case len(rule.Name) > MaxRuleNameLen:
		return fmt.Errorf("The rule name can be at most %d characters.", MaxRuleNameLen)
	case !ruleNameRe.MatchString(rule.Name):
		return errors.New("The rule name can only use letters, numbers, spaces, dots, dashes and underscores.")
	case rule.Pattern == "":
		return errors.New("Add at least one keyword.")
	case len(rule.Pattern) > MaxRulePatternLen:
		return fmt.Errorf("Keywords can be at most %d characters in total.", MaxRulePatternLen)
	case rule.Reply == "":
		return errors.New("Write the reply text.")
	case len(rule.Reply) > MaxRuleReplyLen:
		return fmt.Errorf("The reply can be at most %d characters.", MaxRuleReplyLen)
	case len(rule.Description) > MaxRuleDescLen:
		return fmt.Errorf("The description can be at most %d characters.", MaxRuleDescLen)
	}
	if rule.Type == "" {
		rule.Type = "exact"
	}
	switch rule.Type {
	case "exact", "contains", "prefix":
	case "regex":
		for _, p := range strings.Split(rule.Pattern, ",") {
			if p = strings.TrimSpace(p); p != "" {
				if _, err := regexp.Compile(p); err != nil {
					return fmt.Errorf("The pattern %q is not a valid regular expression: %v", p, err)
				}
			}
		}
	default:
		return fmt.Errorf("Unknown match type %q. Use exact, contains, prefix or regex.", rule.Type)
	}
	if rule.Action != "" && rule.Action != "reply" && rule.Action != "handover" {
		return fmt.Errorf("Unknown action %q.", rule.Action)
	}
	if rule.Action == "reply" {
		rule.Action = ""
	}
	if rule.Priority < -100 || rule.Priority > 100 {
		return errors.New("Priority must be between -100 and 100.")
	}
	return nil
}

// AddOrUpdateRule inserts or replaces a rule by name (case-insensitive) and saves.
// When originalName is set and differs, the rule is renamed.
func (c *Config) AddOrUpdateRule(rule Rule, originalName ...string) error {
	if err := ValidateRule(&rule); err != nil {
		return err
	}
	orig := rule.Name
	if len(originalName) > 0 && strings.TrimSpace(originalName[0]) != "" {
		orig = strings.TrimSpace(originalName[0])
	}
	return c.Update(func(cfg *Config) error {
		idx := -1
		for i, r := range cfg.Rules {
			if strings.EqualFold(r.Name, orig) {
				idx = i
			} else if strings.EqualFold(r.Name, rule.Name) {
				return fmt.Errorf("A rule named %q already exists.", rule.Name)
			}
		}
		if idx >= 0 {
			rules := append([]Rule{}, cfg.Rules...)
			rules[idx] = rule
			cfg.Rules = rules
			return nil
		}
		if len(cfg.Rules) >= MaxRules {
			return fmt.Errorf("You can have at most %d rules.", MaxRules)
		}
		cfg.Rules = append(append([]Rule{}, cfg.Rules...), rule)
		return nil
	})
}

// SetRuleEnabled switches a rule on or off.
func (c *Config) SetRuleEnabled(name string, enabled bool) error {
	return c.Update(func(cfg *Config) error {
		rules := append([]Rule{}, cfg.Rules...)
		for i := range rules {
			if strings.EqualFold(rules[i].Name, name) {
				rules[i].Enabled = enabled
				cfg.Rules = rules
				return nil
			}
		}
		return ErrRuleNotFound
	})
}

// ReorderRules puts the named rules first, in the given order, followed by the rest in
// their current order, and renumbers every priority to match.
func (c *Config) ReorderRules(names []string) error {
	return c.Update(func(cfg *Config) error {
		current := append([]Rule{}, cfg.Rules...)
		sort.SliceStable(current, func(i, j int) bool { return current[i].Priority > current[j].Priority })
		used := map[int]bool{}
		ordered := make([]Rule, 0, len(current))
		for _, n := range names {
			for i, r := range current {
				if !used[i] && strings.EqualFold(r.Name, n) {
					used[i] = true
					ordered = append(ordered, r)
					break
				}
			}
		}
		for i, r := range current {
			if !used[i] {
				ordered = append(ordered, r)
			}
		}
		for i := range ordered {
			ordered[i].Priority = clamp(100-i, -100, 100)
		}
		cfg.Rules = ordered
		return nil
	})
}

var ErrRuleNotFound = errors.New("rule not found")

// DeleteRule removes a rule by name and saves.
func (c *Config) DeleteRule(name string) bool {
	found := false
	err := c.Update(func(cfg *Config) error {
		rules := make([]Rule, 0, len(cfg.Rules))
		for _, r := range cfg.Rules {
			if strings.EqualFold(r.Name, name) {
				found = true
				continue
			}
			rules = append(rules, r)
		}
		if !found {
			return ErrRuleNotFound
		}
		cfg.Rules = rules
		return nil
	})
	return found && err == nil // false when the deletion could not be saved
}

// SetupMissing lists the checklist items that block switching automation on.
func (c *Config) SetupMissing() []string { return MissingSetup(c.Snapshot()) }

// MissingSetup evaluates the checklist on a config value.
func MissingSetup(s Config) []string {
	missing := []string{}
	if s.Server.MustChangePassword {
		missing = append(missing, "Choose your own admin password.")
	}
	if strings.TrimSpace(s.Business.Name) == "" {
		missing = append(missing, "Enter your business name in Settings > Business details.")
	}
	if strings.TrimSpace(s.Business.Hours) == "" {
		missing = append(missing, "Enter your opening hours in Settings > Business details.")
	}
	if !s.CloudAPI.Ready() {
		missing = append(missing, "Connect your WhatsApp Business API in Settings > WhatsApp connection.")
	}
	if s.Setup.RiskAcknowledgedAt == nil {
		missing = append(missing, "Read and accept WhatsApp's messaging rules.")
	}
	return missing
}
