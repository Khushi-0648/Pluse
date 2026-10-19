package config

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"
)

// ServerConfig defines HTTP server settings
type ServerConfig struct {
	Host               string `yaml:"host" json:"host"`
	Port               int    `yaml:"port" json:"port"`
	APIKey             string `yaml:"api_key" json:"api_key"`
	AdminPassword      string `yaml:"admin_password" json:"admin_password"`
	MustChangePassword bool   `yaml:"must_change_password" json:"must_change_password"`
}

// WhatsAppConfig holds WhatsApp connection and store settings
type WhatsAppConfig struct {
	DBPath      string `yaml:"db_path" json:"db_path"`
	SessionName string `yaml:"session_name" json:"session_name"`
	LogLevel    string `yaml:"log_level" json:"log_level"`
	PairPhone   string `yaml:"pair_phone" json:"pair_phone"`
}

// AutomationConfig defines auto-reply and anti-spam behaviors
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
}

// BusinessHoursConfig defines working hours and outside-hours away replies
type BusinessHoursConfig struct {
	Enabled   bool   `yaml:"enabled" json:"enabled"`
	StartHour int    `yaml:"start_hour" json:"start_hour"` // e.g. 9
	EndHour   int    `yaml:"end_hour" json:"end_hour"`     // e.g. 18
	Timezone  string `yaml:"timezone" json:"timezone"`     // e.g. "Local", "UTC", "America/New_York", "Asia/Kolkata"
	AwayReply string `yaml:"away_reply" json:"away_reply"`
}

// AIConfig defines Google Gemini smart auto-response settings
type AIConfig struct {
	Enabled      bool   `yaml:"enabled" json:"enabled"`
	APIKey       string `yaml:"api_key" json:"api_key"`
	Model        string `yaml:"model" json:"model"`
	SystemPrompt string `yaml:"system_prompt" json:"system_prompt"`
}

// Rule defines an automated response rule
type Rule struct {
	Name        string `yaml:"name" json:"name"`
	Type        string `yaml:"type" json:"type"` // "exact", "contains", "prefix", "regex"
	Pattern     string `yaml:"pattern" json:"pattern"`
	Reply       string `yaml:"reply" json:"reply"`
	Description string `yaml:"description" json:"description"`
	Enabled     bool   `yaml:"enabled" json:"enabled"`
}

// WebhookConfig defines outbound webhook dispatching for messages
type WebhookConfig struct {
	Enabled        bool   `yaml:"enabled" json:"enabled"`
	URL            string `yaml:"url" json:"url"`
	Secret         string `yaml:"secret" json:"secret"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"timeout_seconds"`
}

// Config represents the complete application configuration
type Config struct {
	mu            sync.RWMutex        `yaml:"-" json:"-"`
	FilePath      string              `yaml:"-" json:"-"`
	Server        ServerConfig        `yaml:"server" json:"server"`
	WhatsApp      WhatsAppConfig      `yaml:"whatsapp" json:"whatsapp"`
	Automation    AutomationConfig    `yaml:"automation" json:"automation"`
	BusinessHours BusinessHoursConfig `yaml:"business_hours" json:"business_hours"`
	AI            AIConfig            `yaml:"ai" json:"ai"`
	Webhook       WebhookConfig       `yaml:"webhook" json:"webhook"`
	Rules         []Rule              `yaml:"rules" json:"rules"`
}

// DefaultConfig returns safe and production-grade defaults for WhatsApp automation
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host:               "127.0.0.1",
			Port:               8085,
			APIKey:             "",
			AdminPassword:      "",
			MustChangePassword: true,
		},
		WhatsApp: WhatsAppConfig{
			DBPath:      "whatsapp.db",
			SessionName: "default",
			LogLevel:    "INFO",
			PairPhone:   "",
		},
		Automation: AutomationConfig{
			Enabled:            true,
			AutoRead:           true,
			TypingSimulation:   true,
			TypingDurationMs:   1500,
			CooldownSeconds:    5,
			IgnoreGroups:       true,
			IgnoreBroadcasts:   true,
			IgnoreSelf:         true,
			BlacklistPhones:    []string{},
			EnableDefaultReply: false,
			DefaultReply:       "Thanks for reaching out! We have received your message and will get back to you shortly.",
			DailyLimit:         500,
			WarmupMode:         false,
		},
		BusinessHours: BusinessHoursConfig{
			Enabled:   false,
			StartHour: 9,
			EndHour:   18,
			Timezone:  "Local",
			AwayReply: "Hello {{first_name}}! 🌙 Our offices are currently closed. Our hours are 9:00 AM - 6:00 PM. We will get back to you as soon as our team is back online!",
		},
		AI: AIConfig{
			Enabled:      false,
			APIKey:       "",
			Model:        "gemini-3.8-flash",
			SystemPrompt: "You are a professional, helpful customer service assistant for our business on WhatsApp. Answer questions clearly, accurately, and politely in 1-3 concise sentences. If unsure, invite the customer to speak with a human specialist.",
		},
		Webhook: WebhookConfig{
			Enabled:        false,
			URL:            "",
			Secret:         "",
			TimeoutSeconds: 5,
		},
		Rules: []Rule{
			{
				Name:        "greeting_menu",
				Type:        "exact",
				Pattern:     "hi, hello, hey, start, menu, good morning, good afternoon, good evening",
				Reply:       "👋 {{greeting}}, *{{first_name}}*! Welcome to our automated assistant. 🤖\n\nHow can we help you today? Please reply with a number or keyword:\n1️⃣ *Services* - View our offerings\n2️⃣ *Pricing* - View plans & pricing\n3️⃣ *Hours* - Office hours & location\n4️⃣ *Agent* - Speak with a human\n\n💡 You can also type *!help* for commands.",
				Description: "Conversational greeting and interactive main menu",
				Enabled:     true,
			},
			{
				Name:        "option_services",
				Type:        "exact",
				Pattern:     "1, services, service",
				Reply:       "💼 *Our Services:*\n\n1. WhatsApp Automation & AI Assistant\n2. Real-Time CRM & Webhook Integration\n3. Scheduled Broadcasts & Campaigns\n\nReply *2* to see pricing, or *4* for live support!",
				Description: "Menu Option 1: Services",
				Enabled:     true,
			},
			{
				Name:        "option_pricing",
				Type:        "exact",
				Pattern:     "2, pricing, price, plans",
				Reply:       "💰 *Pricing & Packages:*\n\n• *Starter:* Basic automated responses & up to 1,000 messages\n• *Professional:* Full auto-reply rules, AI bot & broadcasts\n• *Enterprise:* High-volume dedicated deployment\n\nReply *4* to connect with our sales team!",
				Description: "Menu Option 2: Pricing",
				Enabled:     true,
			},
			{
				Name:        "option_hours",
				Type:        "exact",
				Pattern:     "3, hours, location, address, timing",
				Reply:       "📍 *Hours & Location:*\n\n⏰ *Operating Hours:* Monday - Friday, 9:00 AM - 6:00 PM\n🏢 *Location:* Available upon scheduled appointment\n🌐 *Online Support:* Available 24/7\n\nReply *menu* to return to the main options.",
				Description: "Menu Option 3: Hours & Location",
				Enabled:     true,
			},
			{
				Name:        "option_agent",
				Type:        "exact",
				Pattern:     "4, agent, human, support, representative, help me",
				Reply:       "🧑‍💼 *Human Support Requested*\n\nThank you, {{first_name}}! We have notified our customer service team. An agent will review this conversation and take over shortly.",
				Description: "Menu Option 4: Human agent transfer",
				Enabled:     true,
			},
			{
				Name:        "gratitude",
				Type:        "exact",
				Pattern:     "thanks, thank you, thx, appreciated",
				Reply:       "😊 You are very welcome, {{first_name}}! Happy to help. Feel free to reach out anytime!",
				Description: "Friendly response to thank you",
				Enabled:     true,
			},
			{
				Name:        "farewell",
				Type:        "exact",
				Pattern:     "bye, goodbye, see you, cya",
				Reply:       "👋 Goodbye {{first_name}}! Have a wonderful day ahead!",
				Description: "Friendly farewell message",
				Enabled:     true,
			},
			{
				Name:        "ping",
				Type:        "prefix",
				Pattern:     "!ping",
				Reply:       "🏓 Pong! WhatsApp Automation engine is active and operational at {{time}}.",
				Description: "Ping command to check bot health",
				Enabled:     true,
			},
			{
				Name:        "help",
				Type:        "prefix",
				Pattern:     "!help",
				Reply:       "🤖 *WhatsApp Automation Menu*\n\nAvailable commands:\n• *!ping* - Check bot status\n• *!time* - Current server time\n• *!info* - System information\n• *!echo <text>* - Echo back text\n• *menu* - Main menu\n\nNeed human support? Type *4* or *support*.",
				Description: "Shows command menu",
				Enabled:     true,
			},
		},
	}
}

// LoadConfig reads config from the given YAML file path or creates a default one
func LoadConfig(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := DefaultConfig()
		cfg.FilePath = path
		if err := cfg.Save(); err != nil {
			return nil, fmt.Errorf("failed to write initial config to %s: %w", path, err)
		}
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		// Attempt recovery from .bak file if available (D15)
		bakPath := path + ".bak"
		if bakData, bErr := os.ReadFile(bakPath); bErr == nil && len(bakData) > 0 {
			data = bakData
		} else if err != nil {
			return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
		}
	}

	cfg := DefaultConfig()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		// Attempt recovery from .bak if parse fails
		bakPath := path + ".bak"
		if bakData, bErr := os.ReadFile(bakPath); bErr == nil && len(bakData) > 0 {
			if bErr := yaml.Unmarshal(bakData, cfg); bErr == nil {
				data = bakData
			} else {
				return nil, fmt.Errorf("failed to parse yaml config %s: %w", path, err)
			}
		} else {
			return nil, fmt.Errorf("failed to parse yaml config %s: %w", path, err)
		}
	}
	cfg.FilePath = path

	// If host is empty or set to insecure 0.0.0.0, advise or default to localhost
	if cfg.Server.Host == "" {
		cfg.Server.Host = "127.0.0.1"
	}

	return cfg, nil
}

// Save writes current config back to disk with atomic replacement and backup (S9, D15)
func (c *Config) Save() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.FilePath == "" {
		c.FilePath = "config.yaml"
	}

	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	// Create backup of existing valid file if present
	if _, err := os.Stat(c.FilePath); err == nil {
		bakFile := c.FilePath + ".bak"
		existingData, rErr := os.ReadFile(c.FilePath)
		if rErr == nil && len(existingData) > 0 {
			_ = os.WriteFile(bakFile, existingData, 0600)
		}
	}

	tmpFile := c.FilePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return os.WriteFile(c.FilePath, data, 0600)
	}

	_ = os.Remove(c.FilePath)
	if err := os.Rename(tmpFile, c.FilePath); err != nil {
		return os.WriteFile(c.FilePath, data, 0600)
	}
	return nil
}

// SafeClone returns a thread-safe copy of Config with masked secrets and a fresh mutex
func (c *Config) SafeClone() *Config {
	c.mu.RLock()
	defer c.mu.RUnlock()

	clone := &Config{
		FilePath:      c.FilePath,
		Server:        c.Server,
		WhatsApp:      c.WhatsApp,
		Automation:    c.Automation,
		BusinessHours: c.BusinessHours,
		AI:            c.AI,
		Webhook:       c.Webhook,
	}
	if len(c.Rules) > 0 {
		clone.Rules = make([]Rule, len(c.Rules))
		copy(clone.Rules, c.Rules)
	}
	clone.Server.AdminPassword = "********"
	if clone.Server.APIKey != "" {
		clone.Server.APIKey = "********"
	}
	if clone.AI.APIKey != "" {
		clone.AI.APIKey = "********"
	}
	if clone.Webhook.Secret != "" {
		clone.Webhook.Secret = "********"
	}
	return clone
}

// GenerateRandomPassword generates a secure, human-readable random password
func GenerateRandomPassword(length int) (string, error) {
	const charset = "23456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"
	b := make([]byte, length)
	for i := range b {
		idx, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			return "", err
		}
		b[i] = charset[idx.Int64()]
	}
	return string(b), nil
}

// EnsureAdminPassword verifies if a master password exists; if empty or default 'admin', generates a random one and sets MustChangePassword = true
func (c *Config) EnsureAdminPassword() (generatedPass string, isNew bool) {
	c.mu.Lock()
	pass := strings.TrimSpace(c.Server.AdminPassword)

	// If already configured with non-default password and not pending change
	if pass != "" && pass != "admin" && !c.Server.MustChangePassword {
		c.mu.Unlock()
		return "", false
	}

	// If empty or default 'admin', generate fresh random password
	var randomPass string
	if pass == "" || pass == "admin" {
		var err error
		randomPass, err = GenerateRandomPassword(12)
		if err != nil {
			randomPass = fmt.Sprintf("SecPass%d", time.Now().UnixNano()%1000000)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(randomPass), bcrypt.DefaultCost)
		if err == nil {
			c.Server.AdminPassword = string(hash)
		} else {
			c.Server.AdminPassword = randomPass
		}
		c.Server.MustChangePassword = true
		c.mu.Unlock()
		_ = c.Save()
		return randomPass, true
	}

	c.mu.Unlock()
	return "", false
}

// VerifyPassword validates the provided password against bcrypt hash or plaintext with auto-upgrade
func (c *Config) VerifyPassword(input string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	expected := c.Server.AdminPassword
	if expected == "" {
		return false
	}

	if strings.HasPrefix(expected, "$2a$") || strings.HasPrefix(expected, "$2b$") {
		return bcrypt.CompareHashAndPassword([]byte(expected), []byte(input)) == nil
	}

	if input == expected {
		// Automatically upgrade plaintext password to bcrypt hash
		if hash, err := bcrypt.GenerateFromPassword([]byte(input), bcrypt.DefaultCost); err == nil {
			c.Server.AdminPassword = string(hash)
			// Trigger asynchronous save without blocking verification
			go func() { _ = c.Save() }()
		}
		return true
	}

	return false
}

// SetPassword updates and bcrypt-hashes the admin master password, clearing MustChangePassword (S20)
func (c *Config) SetPassword(newPass string) error {
	p := strings.TrimSpace(newPass)
	if len(p) < 8 {
		return fmt.Errorf("password must be at least 8 characters long")
	}
	if len(p) > 128 {
		return fmt.Errorf("password cannot exceed 128 characters")
	}
	var hasLetter, hasDigit bool
	for _, r := range p {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			hasLetter = true
		} else if r >= '0' && r <= '9' {
			hasDigit = true
		}
	}
	if !hasLetter || !hasDigit {
		return fmt.Errorf("password must contain both letters and numbers")
	}
	lower := strings.ToLower(p)
	commonWeak := []string{
		"password", "password1", "password123", "admin123", "administrator",
		"12345678", "123456789", "1234567890", "87654321", "qwerty123",
		"letmein1", "welcome1", "changeme1", "admin", "admin1234",
	}
	for _, weak := range commonWeak {
		if lower == weak {
			return fmt.Errorf("password is too common or easily guessable")
		}
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("failed to hash password: %w", err)
	}

	c.mu.Lock()
	c.Server.AdminPassword = string(hash)
	c.Server.MustChangePassword = false
	c.mu.Unlock()

	return c.Save()
}

// IsDefaultPassword returns true if password is unconfigured, requires first-run change, or is 'admin'
func (c *Config) IsDefaultPassword() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.Server.MustChangePassword {
		return true
	}

	pass := c.Server.AdminPassword
	if pass == "" || pass == "admin" {
		return true
	}
	if strings.HasPrefix(pass, "$2a$") || strings.HasPrefix(pass, "$2b$") {
		return bcrypt.CompareHashAndPassword([]byte(pass), []byte("admin")) == nil
	}
	return false
}

// GetRules returns a thread-safe copy of rules
func (c *Config) GetRules() []Rule {
	c.mu.RLock()
	defer c.mu.RUnlock()
	res := make([]Rule, len(c.Rules))
	copy(res, c.Rules)
	return res
}

const (
	// Rule engine limits (D8)
	MaxRules          = 100
	MaxRuleNameLen    = 64
	MaxRulePatternLen = 500
	MaxRuleReplyLen   = 4000
	MaxRuleDescLen    = 256
)

// AddOrUpdateRule updates an existing rule or appends a new rule with strict validation (D8)
func (c *Config) AddOrUpdateRule(rule Rule) error {
	rule.Name = strings.TrimSpace(rule.Name)
	if rule.Name == "" {
		return fmt.Errorf("rule name is required")
	}
	if len(rule.Name) > MaxRuleNameLen {
		return fmt.Errorf("rule name cannot exceed %d characters", MaxRuleNameLen)
	}
	if len(rule.Pattern) > MaxRulePatternLen {
		return fmt.Errorf("rule pattern cannot exceed %d characters", MaxRulePatternLen)
	}
	if len(rule.Reply) > MaxRuleReplyLen {
		return fmt.Errorf("rule reply text cannot exceed %d characters", MaxRuleReplyLen)
	}
	if len(rule.Description) > MaxRuleDescLen {
		return fmt.Errorf("rule description cannot exceed %d characters", MaxRuleDescLen)
	}

	validTypes := map[string]bool{"exact": true, "contains": true, "prefix": true, "regex": true}
	if rule.Type == "" {
		rule.Type = "exact"
	}
	if !validTypes[rule.Type] {
		return fmt.Errorf("invalid rule type %q: must be exact, contains, prefix, or regex", rule.Type)
	}

	if rule.Type == "regex" {
		patterns := strings.Split(rule.Pattern, ",")
		for _, pat := range patterns {
			p := strings.TrimSpace(pat)
			if p != "" {
				if _, err := regexp.Compile(p); err != nil {
					return fmt.Errorf("invalid regular expression %q: %w", p, err)
				}
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	found := false
	for i, r := range c.Rules {
		if r.Name == rule.Name {
			c.Rules[i] = rule
			found = true
			break
		}
	}
	if !found {
		if len(c.Rules) >= MaxRules {
			return fmt.Errorf("maximum limit of %d rules reached", MaxRules)
		}
		c.Rules = append(c.Rules, rule)
	}
	return nil
}

// DeleteRule removes a rule by name
func (c *Config) DeleteRule(name string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for i, r := range c.Rules {
		if r.Name == name {
			c.Rules = append(c.Rules[:i], c.Rules[i+1:]...)
			return true
		}
	}
	return false
}
