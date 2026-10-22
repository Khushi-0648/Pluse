package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"whatsapp-automation/pkg/bot"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/desktop"
	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/security"
)

type settingsView struct {
	config.Config
	PacePresets      []config.PacePreset `json:"pace_presets"`
	AutostartOK      bool                `json:"autostart_supported"`
	Autostart        bool                `json:"autostart"`
	SetupMissing     []string            `json:"setup_missing"`
	HasAPIKey        bool                `json:"has_api_key"`
	HasOperator      bool                `json:"has_operator_password"`
	HasWebhookSecret bool                `json:"has_webhook_secret"`
	DataDir          string              `json:"data_dir"`
	Encrypted        bool                `json:"encrypted"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	raw := s.cfg.Snapshot()
	safe := s.cfg.SafeClone()
	safe.Server.AdminPassword, safe.Server.OperatorPassword, safe.Server.APIKey = "", "", ""
	writeJSON(w, http.StatusOK, settingsView{
		Config: safe, PacePresets: config.PacePresets, AutostartOK: desktop.AutostartSupported(), Autostart: desktop.AutostartEnabled(),
		SetupMissing: config.MissingSetup(raw), HasAPIKey: raw.Server.APIKey != "", HasOperator: raw.Server.OperatorPassword != "",
		HasWebhookSecret: raw.Webhook.Secret != "", DataDir: s.DataDir, Encrypted: s.Encrypted,
	})
}

func fieldError(section, msg string) error { return fmt.Errorf("%s: %s", section, msg) }

func validateBusiness(b *config.BusinessConfig) error {
	trim := func(p *string, max int, label string) error {
		*p = strings.TrimSpace(*p)
		if len(*p) > max {
			return fieldError("Business details", fmt.Sprintf("%s can be at most %d characters.", label, max))
		}
		return nil
	}
	for _, f := range []struct {
		p     *string
		max   int
		label string
	}{{&b.Name, 80, "Name"}, {&b.Hours, 200, "Opening hours"}, {&b.Address, 300, "Address"}, {&b.Website, 200, "Website"}, {&b.Email, 120, "Email"}, {&b.Services, 1500, "Services"}, {&b.Prices, 1500, "Prices"}} {
		if err := trim(f.p, f.max, f.label); err != nil {
			return err
		}
	}
	// Replies use these two; saving them empty would silently stop those replies.
	if b.Name == "" {
		return fieldError("Business details", "Enter the business name.")
	}
	if b.Hours == "" {
		return fieldError("Business details", "Enter your opening hours.")
	}
	if b.Email != "" {
		if _, err := mail.ParseAddress(b.Email); err != nil {
			return fieldError("Business details", "The email address is not valid.")
		}
	}
	if b.Website != "" {
		u, err := url.Parse(b.Website)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fieldError("Business details", "The website must start with https:// or http://.")
		}
	}
	for _, t := range []string{b.Name, b.Hours, b.Address, b.Website, b.Email, b.Services, b.Prices} {
		if strings.Contains(t, "{{") {
			return fieldError("Business details", "Business details cannot contain {{variables}}.")
		}
	}
	return nil
}

var pacingKeys = []string{"daily_limit", "cooldown_seconds", "typing_duration_ms", "broadcast_min_delay", "broadcast_max_delay", "batch_size", "batch_pause_seconds", "max_replies_per_contact_hour", "warmup_mode"}

func between(v, lo, hi int, label string) error {
	if v < lo || v > hi {
		return fieldError("Sending pace", fmt.Sprintf("%s must be between %d and %d.", label, lo, hi))
	}
	return nil
}

func validateAutomation(a *config.AutomationConfig) error {
	checks := []error{
		between(a.DailyLimit, 1, 5000, "Daily limit"),
		between(a.CooldownSeconds, 0, 3600, "Cooldown"),
		between(a.TypingDurationMs, 0, 8000, "Typing time"),
		between(a.BroadcastMinDelay, 2, 600, "Shortest gap between campaign messages"),
		between(a.BroadcastMaxDelay, a.BroadcastMinDelay, 900, "Longest gap between campaign messages"),
		between(a.BatchSize, 0, 500, "Batch size"),
		between(a.BatchPauseSeconds, 0, 3600, "Batch pause"),
		between(a.MaxRepliesPerContactHr, 1, 100, "Replies per customer per hour"),
		between(a.HandoverPauseHours, 1, 720, "Hand-over pause"),
	}
	for _, err := range checks {
		if err != nil {
			return err
		}
	}
	for label, text := range map[string]string{"Fallback reply": a.DefaultReply, "Hand-over reply": a.HandoverReply, "Opt-out reply": a.OptOutReply, "Opt-in reply": a.OptInReply, "Reply when a detail is missing": a.MissingDetailReply} {
		if len(text) > 1000 {
			return fieldError("Replies", label+" can be at most 1000 characters.")
		}
		if err := msgtemplate.Validate(text); err != nil {
			return fieldError(label, err.Error())
		}
	}
	if strings.TrimSpace(a.OptOutReply) == "" || strings.TrimSpace(a.HandoverReply) == "" {
		return fieldError("Replies", "The opt-out and hand-over replies cannot be empty.")
	}
	clean := func(list []string, label string) ([]string, error) {
		out := []string{}
		for _, p := range list {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			d := strings.TrimLeft(p, "+")
			if len(d) < 7 || len(d) > 15 || strings.Trim(d, "0123456789 -") != "" {
				return nil, fieldError(label, fmt.Sprintf("%q is not a valid phone number.", p))
			}
			out = append(out, p)
		}
		return out, nil
	}
	var err error
	if a.AllowList, err = clean(a.AllowList, "Test numbers"); err != nil {
		return err
	}
	if a.BlacklistPhones, err = clean(a.BlacklistPhones, "Blocked numbers"); err != nil {
		return err
	}
	if a.AllowListOnly && len(a.AllowList) == 0 {
		return fieldError("Test mode", "Add at least one test number before turning test mode on.")
	}
	return nil
}

func validateHours(bh *config.BusinessHoursConfig) error {
	if len(bh.Days) != 7 {
		return fieldError("Opening hours", "All seven days are required.")
	}
	for _, d := range bh.Days {
		if d.Open && (!bot.ValidClock(d.Start) || !bot.ValidClock(d.End)) {
			return fieldError("Opening hours", fmt.Sprintf("Use HH:MM times for %s.", d.Day))
		}
	}
	hol := []string{}
	for _, h := range bh.Holidays {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", h); err != nil {
			return fieldError("Holidays", fmt.Sprintf("%q is not a date. Write it as year-month-day, for example 2026-12-25.", h))
		}
		hol = append(hol, h)
	}
	sort.Strings(hol)
	bh.Holidays = hol
	if tz := strings.TrimSpace(bh.Timezone); tz != "" && !strings.EqualFold(tz, "local") {
		if _, err := time.LoadLocation(tz); err != nil {
			return fieldError("Opening hours", fmt.Sprintf("Unknown time zone %q. Use a name like Asia/Kolkata.", tz))
		}
	}
	if len(bh.AwayReply) > 1000 {
		return fieldError("Away reply", "The away reply can be at most 1000 characters.")
	}
	if err := msgtemplate.Validate(bh.AwayReply); err != nil {
		return fieldError("Away reply", err.Error())
	}
	return nil
}

// handlePatchSettings merges a partial update into the saved settings: fields that are
// not sent keep their current values (D20).
func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var raw map[string]json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return
	}
	var sections []string
	var maxMessages int
	err := s.cfg.Update(func(c *configT) error {
		wasEnabled := c.Automation.Enabled
		for key, msg := range raw {
			sections = append(sections, key)
			switch key {
			case "business":
				b := c.Business
				if err := json.Unmarshal(msg, &b); err != nil {
					return fieldError("Business details", "could not be read. Reload the page and try again.")
				}
				if err := validateBusiness(&b); err != nil {
					return err
				}
				c.Business = b
			case "automation":
				var probe map[string]json.RawMessage
				_ = json.Unmarshal(msg, &probe)
				a := c.Automation
				if err := json.Unmarshal(msg, &a); err != nil {
					return fieldError("Automation", "could not be read. Reload the page and try again.")
				}
				if v, ok := probe["daily_limit"]; ok && string(v) == "0" {
					return fieldError("Sending pace", "The daily limit cannot be 0. Switch automatic replies off instead.")
				}
				manual := false
				for _, k := range pacingKeys {
					if _, ok := probe[k]; ok {
						manual = true
					}
				}
				if _, ok := probe["pace"]; ok && !manual {
					if p, ok := config.PresetByName(a.Pace); ok {
						a.ApplyPace(p)
					} else if a.Pace != "custom" {
						return fieldError("Sending pace", "Unknown pace preset.")
					}
				} else if manual {
					a.Pace = "custom"
				}
				if err := validateAutomation(&a); err != nil {
					return err
				}
				c.Automation = a
			case "business_hours":
				bh := c.BusinessHours
				if err := json.Unmarshal(msg, &bh); err != nil {
					return fieldError("Opening hours", "could not be read. Reload the page and try again.")
				}
				if err := validateHours(&bh); err != nil {
					return err
				}
				c.BusinessHours = bh
			case "notifications":
				n := c.Notifications
				if err := json.Unmarshal(msg, &n); err != nil {
					return fieldError("Notifications", "could not be read. Reload the page and try again.")
				}
				c.Notifications = n
			case "webhook":
				wh := c.Webhook
				if err := json.Unmarshal(msg, &wh); err != nil {
					return fieldError("Integrations", "could not be read. Reload the page and try again.")
				}
				if config.IsMasked(wh.Secret) {
					wh.Secret = c.Webhook.Secret
				}
				wh.URL, wh.Secret = strings.TrimSpace(wh.URL), strings.TrimSpace(wh.Secret)
				if wh.URL != "" {
					if err := security.ValidateWebhookURL(wh.URL); err != nil {
						return fieldError("Integrations", err.Error())
					}
				}
				if wh.Enabled && wh.URL == "" {
					return fieldError("Integrations", "Enter the address of the other system before switching this on.")
				}
				if wh.Secret != "" && len(wh.Secret) < 16 {
					return fieldError("Integrations", "The security key must be at least 16 characters. Press Generate to create one.")
				}
				if wh.TimeoutSeconds < 1 || wh.TimeoutSeconds > 30 {
					return fieldError("Integrations", "The waiting time must be between 1 and 30 seconds.")
				}
				if wh.MaxRetries < 1 || wh.MaxRetries > 10 {
					return fieldError("Integrations", "Tries must be between 1 and 10.")
				}
				c.Webhook = wh
			case "storage":
				var st struct {
					RetentionDays *int    `json:"retention_days"`
					MaxMessages   *int    `json:"max_messages"`
					AutoBackup    *bool   `json:"auto_backup"`
					BackupCopyDir *string `json:"backup_copy_dir"`
				}
				if err := json.Unmarshal(msg, &st); err != nil {
					return fieldError("Storage", "could not be read. Reload the page and try again.")
				}
				if st.RetentionDays != nil {
					if *st.RetentionDays < 0 || *st.RetentionDays > 3650 {
						return fieldError("Storage", "Keep messages for 0 (forever) to 3650 days.")
					}
					c.Storage.RetentionDays = *st.RetentionDays
				}
				if st.MaxMessages != nil {
					if *st.MaxMessages < 1000 || *st.MaxMessages > 1000000 {
						return fieldError("Storage", "The message limit must be between 1,000 and 1,000,000.")
					}
					c.Storage.MaxMessages = *st.MaxMessages
					maxMessages = *st.MaxMessages
				}
				if st.AutoBackup != nil {
					c.Storage.AutoBackup = *st.AutoBackup
				}
				if st.BackupCopyDir != nil {
					dir := strings.TrimSpace(*st.BackupCopyDir)
					if dir != "" {
						// A network share (\\server\share) would make Windows send this
						// computer's sign-in details to that server; only local folders are allowed.
						if strings.HasPrefix(dir, `\\`) || strings.HasPrefix(dir, "//") {
							return fieldError("Backups", `Choose a folder on this computer, for example D:\Backups.`)
						}
						if !filepath.IsAbs(dir) {
							return fieldError("Backups", "Enter the full folder path, for example E:\\Pulse backups.")
						}
						if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
							return fieldError("Backups", "That folder does not exist. Create it first, or plug in the drive.")
						}
					}
					c.Storage.BackupCopyDir = dir
				}
			default:
				return fmt.Errorf("Unknown settings section %q.", key)
			}
		}
		if c.Automation.Enabled && !wasEnabled {
			if missing := config.MissingSetup(*c); len(missing) > 0 {
				return errors.New("Finish setup before switching automatic replies on: " + strings.Join(missing, " "))
			}
		}
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if maxMessages > 0 && s.History != nil {
		s.History.SetMaxItems(maxMessages)
	}
	sort.Strings(sections)
	security.LogAudit("SETTINGS_SAVED", clientIP(r), actor(r), "Updated "+strings.Join(sections, ", "), "success")
	writeOK(w, nil)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Current) {
		return
	}
	if err := s.cfg.SetPassword(req.New); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.sessions.DestroyAll(authOf(r).session)
	security.LogAudit("PASSWORD_CHANGED", clientIP(r), actor(r), "Admin password changed; other sessions signed out", "success")
	writeOK(w, map[string]any{"message": "Password changed. Other devices were signed out."})
}

func (s *Server) handleOperatorPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Current) {
		return
	}
	if err := s.cfg.SetOperatorPassword(req.New); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.sessions.DestroyRole("operator")
	msg := "Staff password set. Staff can use the inbox and contacts but cannot change settings."
	if strings.TrimSpace(req.New) == "" {
		msg = "Staff sign-in switched off."
	}
	security.LogAudit("OPERATOR_PASSWORD", clientIP(r), actor(r), msg, "success")
	writeOK(w, map[string]any{"message": msg})
}

func (s *Server) handleAPIKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		Action   string `json:"action"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password) {
		return
	}
	key := ""
	switch req.Action {
	case "generate":
		key = "pk_" + security.RandomToken(24)
	case "revoke":
	default:
		writeError(w, http.StatusBadRequest, "Unknown action.")
		return
	}
	stored := "" // only a hash is kept; the key itself is shown once, below
	if key != "" {
		stored = config.HashAPIKey(key)
	}
	if err := s.cfg.Update(func(c *configT) error { c.Server.APIKey = stored; return nil }); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	security.LogAudit("API_KEY_"+strings.ToUpper(req.Action), clientIP(r), actor(r), "API key "+req.Action+"d", "success")
	writeOK(w, map[string]any{"api_key": key})
}

func (s *Server) handleAutostart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if !security.IsLocalRequest(r) {
		writeError(w, http.StatusForbidden, "This can only be changed on the computer running "+"Pulse.")
		return
	}
	if err := desktop.SetAutostart(req.Enabled); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	security.LogAudit("AUTOSTART", clientIP(r), actor(r), fmt.Sprintf("Start with Windows: %v", req.Enabled), "success")
	writeOK(w, nil)
}

func (s *Server) handleTestAlert(w http.ResponseWriter, r *http.Request) {
	if s.Alerts != nil {
		_ = s.Alerts.Test()
	}
	writeOK(w, map[string]any{"message": "Alert test played. Handover alerts trigger an audible system chime without displaying customer messages on the desktop."})
}

func (s *Server) handleLaunchWindow(w http.ResponseWriter, r *http.Request) {
	if !security.IsLocalRequest(r) {
		writeError(w, http.StatusForbidden, "Only available on the computer running the app.")
		return
	}
	port := s.cfg.Snapshot().ListenPort()
	if _, err := desktop.LaunchAppWindow(fmt.Sprintf("http://127.0.0.1:%d", port)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, nil)
}

func (s *Server) handleCreateShortcut(w http.ResponseWriter, r *http.Request) {
	if !security.IsLocalRequest(r) {
		writeError(w, http.StatusForbidden, "Only available on the computer running the app.")
		return
	}
	if err := desktop.EnsureDesktopShortcut(""); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]any{"message": "Desktop shortcut created."})
}
