package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/security"
	"whatsapp-automation/pkg/version"
)

// FirstRunNoticeName is the file that holds the one-time password when no console is attached.
const FirstRunNoticeName = "FIRST-RUN-PASSWORD.txt"

func removeFirstRunNotice(dataDir string) {
	if dataDir != "" {
		_ = os.Remove(filepath.Join(dataDir, FirstRunNoticeName))
	}
}

type setupStep struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Done  bool   `json:"done"`
}

func (s *Server) setupSteps() []setupStep {
	snap := s.cfg.Snapshot()
	st := s.status()
	return []setupStep{
		{"password", "Choose your own admin password", !snap.Server.MustChangePassword},
		{"risk", "Read WhatsApp's messaging rules", snap.Setup.RiskAcknowledgedAt != nil},
		{"link", "Connect your WhatsApp Business API", st.Configured && st.Connected},
		{"webhook", "Receive messages: set up the webhook in Meta", snap.Setup.WebhookSeenAt != nil},
		{"business", "Enter your business name and opening hours", strings.TrimSpace(snap.Business.Name) != "" && strings.TrimSpace(snap.Business.Hours) != ""},
		{"test", "Send a test message to your own phone", snap.Setup.TestMessageSentAt != nil},
		{"automation", "Switch automatic replies on", snap.Automation.Enabled},
	}
}

// Health is the account health summary for the last 24 hours.
type Health struct {
	Level      string   `json:"level"`
	Sent       int      `json:"sent"`
	Failed     int      `json:"failed"`
	FailRate   float64  `json:"fail_rate"`
	OptOuts    int      `json:"opt_outs"`
	Handovers  int      `json:"handovers"`
	Loops      int      `json:"loops"`
	PeakHour   int      `json:"peak_hour"`
	DailyLimit int      `json:"daily_limit"`
	DailyLeft  int      `json:"daily_left"`
	Advice     []string `json:"advice"`
}

func (s *Server) health() Health {
	now := time.Now()
	since := now.Add(-24 * time.Hour)
	snap := s.cfg.Snapshot()
	h := Health{Level: "good", Advice: []string{}}
	h.DailyLimit = snap.Automation.DailyLimit
	if snap.Automation.WarmupMode && h.DailyLimit > 50 {
		h.DailyLimit = 50
	}
	h.DailyLeft = campaign.DailyRemaining(snap, s.History, now)
	if s.History != nil {
		st := s.History.Stats(since)
		h.Sent, h.Failed = st.Sent, st.Failed
		for _, n := range st.Hourly {
			if n > h.PeakHour {
				h.PeakHour = n
			}
		}
	}
	if total := h.Sent + h.Failed; total > 0 {
		h.FailRate = float64(h.Failed) / float64(total)
	}
	h.OptOuts = security.CountAudit("OPT_OUT", since)
	h.Handovers = security.CountAudit("AGENT_HANDOVER", since)
	h.Loops = security.CountAudit("LOOP_DETECTED", since) + security.CountAudit("REPLY_CAP", since)
	st := s.status()
	switch {
	case strings.EqualFold(st.QualityRating, "RED"):
		h.Level = "risk"
		h.Advice = append(h.Advice, "Meta rates this number's quality as low (red). Stop campaigns and reply only to customers who write first, or WhatsApp will lower your messaging limit.")
	case h.Failed >= 5 && h.FailRate >= 0.2:
		h.Level = "risk"
		h.Advice = append(h.Advice, fmt.Sprintf("%.0f%% of messages failed in the last 24 hours. Check the connection and pause campaigns.", h.FailRate*100))
	case h.OptOuts >= 5:
		h.Level = "risk"
		h.Advice = append(h.Advice, fmt.Sprintf("%d people opted out in the last 24 hours. Send fewer, more relevant messages.", h.OptOuts))
	}
	if h.Level == "good" {
		if h.OptOuts >= 2 || (h.Failed >= 3 && h.FailRate >= 0.1) || h.Loops > 0 {
			h.Level = "watch"
		}
		if h.OptOuts >= 2 {
			h.Advice = append(h.Advice, "Several people opted out today. Review your recent campaign before sending more.")
		}
		if h.Loops > 0 {
			h.Advice = append(h.Advice, "Some chats were paused because of very frequent messages. Check the Needs attention list.")
		}
	}
	if h.DailyLimit > 0 && h.DailyLeft*5 < h.DailyLimit {
		h.Advice = append(h.Advice, "You have used most of today's sending limit.")
	}
	if strings.EqualFold(st.QualityRating, "YELLOW") {
		if h.Level == "good" {
			h.Level = "watch"
		}
		h.Advice = append(h.Advice, "Meta rates this number's quality as medium (yellow). Send fewer, more relevant messages.")
	}
	if snap.Automation.WarmupMode {
		h.Advice = append(h.Advice, "Warm-up is on: at most 50 automated messages per day while the number builds a history.")
	}
	// Not "risk": that would block campaigns, and this is the owner's own choice.
	if !snap.Automation.RequireOptIn && !snap.Automation.Enforce24hWindow {
		if h.Level == "good" {
			h.Level = "watch"
		}
		h.Advice = append(h.Advice, "Opt-in and the 24-hour rule are both off (Settings > Safety): "+consentRulesWarning)
	}
	return h
}

// healthWatch pauses a running campaign when account health turns risky.
func (s *Server) healthWatch() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stopHealth:
			return
		case <-t.C:
		}
		if s.Campaigns == nil {
			continue
		}
		if h := s.health(); h.Level == "risk" {
			if active, ok := s.Campaigns.Active(); ok && active.Status == campaign.CRunning {
				reason := "Paused automatically because account health is at risk: " + strings.Join(h.Advice, " ")
				if s.Campaigns.Pause(reason) {
					security.LogAudit("BROADCAST_AUTO_PAUSE", "local", "system", reason, "warning")
				}
			}
		}
	}
}

type attentionItem struct {
	Phone  string     `json:"phone"`
	Name   string     `json:"name"`
	Reason string     `json:"reason"`
	Since  *time.Time `json:"since,omitempty"`
	Until  *time.Time `json:"paused_until,omitempty"`
}

func (s *Server) attention(limit int) []attentionItem {
	out := []attentionItem{}
	if s.Contacts == nil {
		return out
	}
	for _, c := range s.Contacts.NeedsAttention() {
		out = append(out, attentionItem{Phone: c.Phone, Name: c.DisplayName(), Reason: c.AttentionReason, Since: c.AttentionAt, Until: c.BotPausedUntil})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot()
	now := time.Now()
	y, m, d := now.Date()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	var today history.Stats
	unread := 0
	if s.History != nil {
		today = s.History.Stats(midnight)
		for _, c := range s.History.Conversations() {
			var lastRead *time.Time
			if s.Contacts != nil {
				if ct, ok := s.Contacts.Get(c.Phone); ok {
					lastRead = ct.LastReadAt
				}
			}
			if c.UnreadSince(lastRead) > 0 {
				unread++
			}
		}
	}
	steps := s.setupSteps()
	done := 0
	for _, st := range steps {
		if st.Done {
			done++
		}
	}
	att := s.attention(0)
	resp := map[string]any{
		"product": version.Product, "version": version.Version,
		"whatsapp": s.status(),
		"setup":    map[string]any{"steps": steps, "done": done, "total": len(steps), "missing": s.cfg.SetupMissing()},
		"automation": map[string]any{
			"enabled": snap.Automation.Enabled, "dry_run": snap.Automation.DryRun, "allow_list_only": snap.Automation.AllowListOnly,
			"pace": snap.Automation.Pace, "business_hours": snap.BusinessHours.Enabled,
		},
		"today":  map[string]int{"received": today.Incoming, "sent": today.Sent, "auto_replies": today.AutoReplies, "failed": today.Failed, "customers": today.Customers},
		"counts": map[string]int{"needs_attention": len(att), "unread_chats": unread},
		"health": s.health(),
		"attention": func() []attentionItem {
			if len(att) > 5 {
				return att[:5]
			}
			return att
		}(),
		// The list is oldest first and cut at 5; the dashboard alert needs the newest request.
		"latest_attention": func() *attentionItem {
			var latest *attentionItem
			for i := range att {
				if a := &att[i]; latest == nil || a.Since != nil && (latest.Since == nil || a.Since.After(*latest.Since)) {
					latest = a
				}
			}
			return latest
		}(),
		"risk_acknowledged": snap.Setup.RiskAcknowledgedAt != nil,
	}
	if t := snap.Setup.PasswordResetAt; t != nil && time.Since(*t) < 7*24*time.Hour && authOf(r).role == "admin" {
		resp["password_reset_at"] = t
	}
	counts := resp["counts"].(map[string]int)
	if s.Contacts != nil {
		counts["contacts"] = s.Contacts.Count()
	}
	if s.Scheduler != nil {
		counts["scheduled"] = s.Scheduler.PendingCount()
	}
	// Campaigns are admin-only (their message, audience and results), so staff do not get
	// the running one here either.
	if s.Campaigns != nil && authOf(r).role == "admin" {
		if a, ok := s.Campaigns.Active(); ok {
			resp["campaign"] = a
		}
	}
	if s.History != nil {
		resp["imported_messages"] = s.History.ImportedCount()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"whatsapp": s.status(), "version": version.Version, "uptime": time.Since(s.startTime).Round(time.Second).String()})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.health())
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	snap := s.cfg.Snapshot()
	pending := 0
	if s.Hooks != nil {
		pending = s.Hooks.PendingCount()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"product": version.Product, "version": version.Version, "go": runtime.Version(), "os": runtime.GOOS,
		"uptime": time.Since(s.startTime).Round(time.Second).String(), "memory_mb": m.Alloc / (1 << 20), "goroutines": runtime.NumGoroutine(),
		"data_dir": s.DataDir, "encrypted": s.Encrypted, "config_file": snap.FilePath, "webhook_queue": pending,
	})
}

// deleteAllCustomerData deletes every customer's data. It reports what could not be
// saved yet (usually a file held by antivirus; it is retried in the background), so the
// owner is never told "deleted" when the disk still holds the data.
func (s *Server) deleteAllCustomerData() error {
	var errs []error
	if s.History != nil {
		errs = append(errs, s.History.Clear())
	}
	if s.Contacts != nil {
		errs = append(errs, s.Contacts.Clear())
	}
	if s.Scheduler != nil {
		errs = append(errs, s.Scheduler.Clear(true))
	}
	if s.Campaigns != nil {
		s.Campaigns.DeleteAll()
	}
	if s.Hooks != nil {
		s.Hooks.Forget("")
	}
	// The daily backups hold the same contacts, rules and details: "delete all" that
	// left them would not delete all.
	s.deleteAutoBackups()
	// The unencrypted files of versions before 1.4 hold customer data too.
	if s.DataDir != "" && s.oldCopies() > 0 {
		errs = append(errs, os.RemoveAll(s.oldCopiesDir()))
	}
	// The activity log keeps what happened, but no longer names any customer (S38).
	security.RedactAllCustomers()
	return errors.Join(errs...)
}

func (s *Server) handleAcknowledgeRisk(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	if err := s.cfg.Update(func(c *configT) error { c.Setup.RiskAcknowledgedAt = &now; return nil }); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	security.LogAudit("RISK_ACKNOWLEDGED", clientIP(r), actor(r), "Account safety notice accepted", "success")
	writeOK(w, nil)
}

// testRecipient checks the owner's own phone number for a test message. The business
// number cannot message itself, so tests go to another phone the owner has.
func (s *Server) testRecipient(w http.ResponseWriter, raw string) (string, bool) {
	st := s.status()
	if !st.Connected || s.Messenger == nil {
		writeError(w, http.StatusConflict, "Connect your WhatsApp Business API first (Settings > WhatsApp connection).")
		return "", false
	}
	phone := contacts.CleanPhone(raw)
	if len(phone) < 7 || len(phone) > 15 {
		writeError(w, http.StatusBadRequest, "Enter your own mobile number with country code, for example +919876543210.")
		return "", false
	}
	if phone == st.Phone {
		writeError(w, http.StatusBadRequest, "The business number cannot message itself. Enter another phone you have, such as your personal mobile.")
		return "", false
	}
	return phone, true
}

// testHint is added when Meta refuses a test because that phone has not written first.
const testHint = " Send any message from that phone to your business number first, then try again within 24 hours."

// sendTest sends a test message with the same checks as a message typed in the inbox:
// opt-out, blocked numbers, test mode, practice mode and the daily limit. In practice
// mode it is only recorded (dryRun). ok is false when an error was written.
func (s *Server) sendTest(w http.ResponseWriter, r *http.Request, phone, text string) (dryRun, ok bool) {
	snap := s.cfg.Snapshot()
	a := snap.Automation
	if s.Contacts != nil {
		c, found := s.Contacts.GetExact(phone)
		if (found && c.IsDND) || (!found && s.Contacts.WasOptedOut(phone)) {
			writeError(w, http.StatusConflict, "This number opted out of messages, so no test is sent to it. Use another phone, or ask them to reply START first.")
			return false, false
		}
	}
	if inPhoneList(a.BlacklistPhones, phone) {
		writeError(w, http.StatusConflict, "This number is on your blocked list (Settings > Safety).")
		return false, false
	}
	if a.AllowListOnly && !s.Contacts.AllowListed(a.AllowList, phone) {
		writeError(w, http.StatusConflict, "Test mode is on: messages can only go to the test numbers in Settings > Safety.")
		return false, false
	}
	rec := history.MessageRecord{Direction: history.DirectionOutgoing, Type: history.TypeDirectSend, Phone: phone, Text: text, SentBy: actor(r)}
	if a.DryRun {
		rec.ID, rec.Status = "dry_"+security.RandomToken(6), history.StatusDryRun
		if s.History != nil {
			s.History.Record(rec)
		}
		return true, true
	}
	releaseDaily, reserved := campaign.ReserveDaily(snap, s.History, time.Now())
	if !reserved {
		writeError(w, http.StatusTooManyRequests, "Today's sending limit has been reached, so the test was not sent. Raise the limit in Settings > Safety, or try again later.")
		return false, false
	}
	defer releaseDaily()
	// Recorded and labelled before it goes (see campaign.SendRecorded).
	rec.Label = DirectLabel + security.RandomToken(8)
	_, err := campaign.SendRecorded(r.Context(), s.History, rec, directSendTimeout, func(ctx context.Context) (string, error) {
		return s.Messenger.SendTextMessage(ctx, phone, text)
	})
	if err != nil {
		writeSendError(w, err, testHint)
		return false, false
	}
	return false, true
}

// practiceNote answers a test that practice mode only recorded.
const practiceNote = "Practice mode is on, so the test was not sent; it is shown in the inbox as a practice message. Turn practice mode off in Settings > Safety to send it."

func (s *Server) handleTestMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	phone, ok := s.testRecipient(w, req.Phone)
	if !ok {
		return
	}
	snap := s.cfg.Snapshot()
	name := snap.Business.Name
	if name == "" {
		name = version.Product
	}
	text := fmt.Sprintf("Test message from %s. If you can read this, sending works. Sent at %s.", name, time.Now().Format("15:04"))
	dryRun, ok := s.sendTest(w, r, phone, text)
	if !ok {
		return
	}
	if dryRun {
		writeOK(w, map[string]any{"dry_run": true, "message": practiceNote})
		return
	}
	now := time.Now()
	_ = s.cfg.Update(func(c *configT) error { c.Setup.TestMessageSentAt = &now; return nil })
	writeOK(w, map[string]any{"message": "Test message sent to +" + phone + ". Check WhatsApp on that phone."})
}
