package server

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/security"
	"whatsapp-automation/pkg/templates"
	"whatsapp-automation/pkg/version"
)

func (s *Server) handleAnalytics(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || days < 0 {
		days = 30
	}
	var since time.Time
	if days > 0 {
		since = reportSince(days, time.Now())
	}
	var st history.Stats
	imported := 0
	if s.History != nil {
		st = s.History.Stats(since)
		imported = s.History.ImportedCount()
	}
	type kv struct {
		Key   string `json:"key"`
		Count int    `json:"count"`
	}
	rules := []kv{}
	for k, v := range st.Rules {
		rules = append(rules, kv{k, v})
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].Count > rules[j].Count })
	if len(rules) > 8 {
		rules = rules[:8]
	}
	series := []kv{}
	if days > 0 && days <= 120 {
		for i := days - 1; i >= 0; i-- {
			d := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
			series = append(series, kv{d, st.Daily[d]})
		}
	}
	rate := func(a, b int) any {
		if b == 0 {
			return nil
		}
		return float64(a) / float64(b)
	}
	resp := map[string]any{
		"days": days, "since": since, "first_message_at": st.FirstAt, "stats": st, "top_rules": rules, "series": series,
		"delivery_rate": rate(st.Delivered, st.Sent), "read_rate": rate(st.Read, st.Sent), "failure_rate": rate(st.Failed, st.Sent+st.Failed),
		"imported_excluded": imported, "opt_outs": security.CountAudit("OPT_OUT", since), "handovers": security.CountAudit("AGENT_HANDOVER", since),
	}
	if s.Campaigns != nil {
		list := s.Campaigns.List()
		if len(list) > 10 {
			list = list[:10]
		}
		resp["campaigns"] = list
	}
	writeJSON(w, http.StatusOK, resp)
}

// reportSince is where a report of the last days days starts: local midnight of
// today - days + 1, so "7 days" is today and the six days before it.
func reportSince(days int, now time.Time) time.Time {
	y, m, d := now.AddDate(0, 0, -days+1).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

func (s *Server) handleAnalyticsCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="messages-%s.csv"`, time.Now().Format("20060102")))
	// days limits the download to the range shown in Reports; 0 or missing means all.
	var since time.Time
	if days, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && days > 0 && days <= 3660 {
		since = reportSince(days, time.Now()) // the same days as the Reports screen
	}
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"time", "direction", "type", "phone", "name", "status", "rule", "campaign", "text"})
	if s.History != nil {
		f := false
		for _, m := range s.History.Query(history.Query{Imported: &f, Since: since}) {
			_ = cw.Write([]string{m.Timestamp.Format("2006-01-02 15:04:05"), string(m.Direction), string(m.Type), "+" + m.Phone, csvCell(m.Name), m.Status, m.RuleTriggered, m.CampaignID, csvCell(m.Text)})
		}
	}
	cw.Flush()
	security.LogAudit("REPORT_EXPORTED", clientIP(r), actor(r), "Message report downloaded", "success")
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	writeJSON(w, http.StatusOK, security.QueryAuditLog(limit, strings.ToUpper(r.URL.Query().Get("action")), time.Time{}))
}

func (s *Server) handleAuditCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="audit-log-%s.csv"`, time.Now().Format("20060102")))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"time", "action", "by", "from", "status", "details"})
	for _, e := range security.GetAuditLog(0) {
		_ = cw.Write([]string{e.Timestamp.Format("2006-01-02 15:04:05"), e.Action, csvCell(e.User), e.IP, e.Status, csvCell(e.Details)})
	}
	cw.Flush()
}

func (s *Server) handlePrivacyExport(w http.ResponseWriter, r *http.Request) {
	typed := strings.TrimSpace(r.URL.Query().Get("phone"))
	// Resolved once (resolvePhone), then everything is matched exactly: the contact, the
	// messages. A stored number never exports another customer's data.
	phone, ok := s.requestPhone(w, typed)
	if !ok {
		return
	}
	bundle := map[string]any{"product": version.Product, "version": version.Version, "exported_at": time.Now().Format(time.RFC3339)}
	name := "all"
	if typed != "" {
		// A number was asked for: never fall back to everyone's data.
		if len(phone) < 7 || len(phone) > 15 {
			writeError(w, http.StatusBadRequest, "Enter a valid phone number with country code, for example +919876543210.")
			return
		}
		var c *contacts.Contact
		var msgs []history.MessageRecord
		if s.Contacts != nil {
			c, _ = s.Contacts.GetExact(phone)
		}
		if s.History != nil {
			msgs = s.History.Query(history.Query{Phone: phone})
		}
		if c == nil && len(msgs) == 0 {
			writeError(w, http.StatusBadRequest, "Pulse has no contact or messages for that number.")
			return
		}
		name = phone
		if c != nil {
			bundle["contact"] = c
		}
		bundle["messages"] = msgs
	} else {
		if s.Contacts != nil {
			bundle["contacts"] = s.Contacts.List()
		}
		if s.History != nil {
			bundle["messages"] = s.History.ExportAll()
		}
		bundle["audit_log"] = security.GetAuditLog(0)
	}
	security.LogAudit("DATA_EXPORTED", clientIP(r), actor(r), "Personal data export ("+name+")", "success")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pulse-data-%s-%s.json"`, name, time.Now().Format("20060102")))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(bundle)
}

func (s *Server) handlePrivacyPurge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
		Days     int    `json:"days"`
		All      bool   `json:"all"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password) {
		return
	}
	if req.All {
		if err := s.deleteAllCustomerData(); err != nil {
			security.LogAudit("DATA_DELETED_ALL", clientIP(r), actor(r), "Delete all data: not every file could be saved yet ("+err.Error()+"); retrying", "failed")
			writeError(w, http.StatusInternalServerError, "The data is deleted in Pulse, but not every file could be saved yet ("+err.Error()+"). Pulse keeps trying; close programs that may hold the files (antivirus, backup tools) and check again in a minute.")
			return
		}
		security.LogAudit("DATA_DELETED_ALL", clientIP(r), actor(r), "All messages, contacts and scheduled messages deleted", "warning")
		writeOK(w, map[string]any{"message": "All customer data was deleted."})
		return
	}
	if req.Days < 1 {
		writeError(w, http.StatusBadRequest, "Enter a number of days (1 or more).")
		return
	}
	n := 0
	if s.History != nil {
		n = s.History.PurgeOlderThan(req.Days)
	}
	security.LogAudit("DATA_PURGED", clientIP(r), actor(r), fmt.Sprintf("Deleted %d messages older than %d days", n, req.Days), "success")
	writeOK(w, map[string]any{"deleted": n, "message": fmt.Sprintf("Deleted %d messages older than %d days.", n, req.Days)})
}

func (s *Server) handleDeleteImported(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password) {
		return
	}
	msgs, people := 0, 0
	if s.History != nil {
		msgs = s.History.DeleteHistorySync()
	}
	if s.Contacts != nil {
		for _, c := range s.Contacts.List() {
			if (c.Source == "imported history" || c.Source == "phone address book") && c.LastInboundAt == nil && c.LastContactedAt == nil && !c.Consent && len(c.Tags) == 0 && c.Notes == "" {
				if s.Contacts.Delete(c.Phone) {
					people++
				}
			}
		}
	}
	security.LogAudit("IMPORTED_DELETED", clientIP(r), actor(r), fmt.Sprintf("Deleted %d imported messages and %d imported contacts", msgs, people), "success")
	writeOK(w, map[string]any{"messages": msgs, "contacts": people, "message": fmt.Sprintf("Deleted %d imported messages and %d contacts that only came from the import.", msgs, people)})
}

func (s *Server) handleForgetContact(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone    string `json:"phone"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password) {
		return
	}
	// Resolved once, then every store is matched exactly: an erasure never removes a
	// customer whose number only looks alike.
	phone, ok := s.requestPhone(w, req.Phone)
	if !ok {
		return
	}
	if phone == "" {
		writeError(w, http.StatusBadRequest, "Enter the phone number.")
		return
	}
	// Pulse 2.0 kept a foreign number such as 6591234567 as 916591234567. That record is
	// erased too when it is certainly this customer's (written by 2.0, from WhatsApp);
	// otherwise it may be someone in India, so it stays and the admin is told.
	erase := []string{phone}
	legacy := contacts.LegacyWAID(phone)
	kept := ""
	if legacy != "" {
		lc, sure := (*contacts.Contact)(nil), false
		if s.Contacts != nil {
			lc, sure = s.Contacts.LegacyContact(phone)
		}
		switch {
		case lc != nil && sure && s.Contacts.ForgetLegacy(phone):
			erase = append(erase, legacy)
		case lc != nil:
			kept = fmt.Sprintf(" A contact under +%s also exists. Pulse 2.0 may have saved this customer under that number, but it can also be a different customer in India, so it was not erased; if it is the same person, erase +%s as well.", legacy, legacy)
		case s.History != nil && s.History.HasPhone(legacy):
			kept = fmt.Sprintf(" Messages under +%s also exist. Pulse 2.0 may have saved this customer's messages under that number, but it can also be a different customer in India, so they were not erased; if it is the same person, erase +%s as well.", legacy, legacy)
		}
	}
	msgs := 0
	for _, p := range erase {
		if s.History != nil {
			msgs += s.History.ClearPhone(p)
		}
		if s.Campaigns != nil {
			s.Campaigns.ForgetPhone(p)
		}
		if s.Hooks != nil {
			s.Hooks.Forget("+" + p)
		}
	}
	if s.Contacts != nil {
		s.Contacts.Forget(phone) // also keeps a later "Restore backup" from bringing them back
	}
	if s.Scheduler != nil {
		// Also deletes scheduled.json.bak, which still held their messages.
		if _, err := s.Scheduler.ForgetPhone(erase...); err != nil {
			fmt.Printf("[Privacy] The scheduled messages could not be saved yet: %v\n", err)
		}
	}
	// The activity log keeps the entries (what happened, and that an erasure was done)
	// but no longer names the customer.
	security.RedactAudit(phone)
	if legacy != "" {
		// Pulse 2.0 logged a foreign number such as 6591234567 as 916591234567.
		security.RedactAudit(legacy)
	}
	for _, p := range erase {
		s.forgetInBackups(p)
	}
	security.LogAudit("CONTACT_FORGOTTEN", clientIP(r), actor(r), fmt.Sprintf("All data for %s deleted (%d messages)", security.MaskPhone(phone), msgs), "success")
	if kept != "" {
		security.LogAudit("CONTACT_FORGOTTEN", clientIP(r), actor(r), fmt.Sprintf("Data under %s (the form Pulse 2.0 used) was not erased: it may be a different customer", security.MaskPhone(legacy)), "warning")
	}
	if s.History != nil {
		if err := s.History.Flush(); err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("The contact and %d messages are deleted in Pulse, but the message file could not be saved yet (%v). Pulse keeps trying; check again in a minute.", msgs, err))
			return
		}
	}
	msg := fmt.Sprintf("Deleted the contact and %d messages.", msgs) + kept
	if s.DataDir != "" && s.oldCopies() > 0 {
		// Those files are not edited here; the owner removes them in one step.
		msg += " Unencrypted copies from a version before 1.4 still exist: delete them under Settings > Privacy and data > Old copies."
	}
	resp := map[string]any{"message": msg}
	if kept != "" {
		resp["not_erased"] = "+" + legacy
	}
	writeOK(w, resp)
}

type backupBundle struct {
	Product       string                      `json:"product"`
	Version       string                      `json:"version"`
	CreatedAt     time.Time                   `json:"created_at"`
	Business      config.BusinessConfig       `json:"business"`
	Automation    *config.AutomationConfig    `json:"automation,omitempty"`
	BusinessHours *config.BusinessHoursConfig `json:"business_hours,omitempty"`
	Rules         []config.Rule               `json:"rules"`
	Templates     []templates.Template        `json:"templates"`
	Contacts      []contacts.Contact          `json:"contacts"`
}

func (s *Server) buildBackup() backupBundle {
	snap := s.cfg.Snapshot()
	b := backupBundle{Product: version.Product, Version: version.Version, CreatedAt: time.Now(), Business: snap.Business, Automation: &snap.Automation, BusinessHours: &snap.BusinessHours, Rules: snap.Rules}
	if s.Templates != nil {
		b.Templates = s.Templates.List()
	}
	if s.Contacts != nil {
		b.Contacts = s.Contacts.List()
	}
	return b
}

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	b := s.buildBackup()
	security.LogAudit("BACKUP_DOWNLOADED", clientIP(r), actor(r), fmt.Sprintf("Backup with %d contacts", len(b.Contacts)), "success")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="pulse-backup-%s.json"`, time.Now().Format("20060102-1504")))
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(b)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string       `json:"password"`
		Bundle   backupBundle `json:"bundle"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "The backup file is larger than 50 MB.")
		return
	}
	if err := jsonUnmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "This is not a valid backup file.")
		return
	}
	if !s.reauth(w, r, req.Password) {
		return
	}
	b := req.Bundle
	if b.Product != version.Product && b.Product != "" {
		writeError(w, http.StatusBadRequest, "This backup was not made by "+version.Product+".")
		return
	}
	nc, nt, nr := s.restoreBundle(b)
	security.LogAudit("BACKUP_RESTORED", clientIP(r), actor(r), fmt.Sprintf("Restored %d contacts, %d templates, %d rules", nc, nt, nr), "success")
	writeOK(w, map[string]any{"message": fmt.Sprintf("Restored %d contacts, %d templates and %d rules.", nc, nt, nr)})
}

// restoreBundle adds or updates contacts, templates, rules, business details and
// opening hours from a backup. Nothing is deleted and opt-outs are never undone.
func (s *Server) restoreBundle(b backupBundle) (nc, nt, nr int) {
	if s.Contacts != nil {
		for _, c := range b.Contacts {
			if s.Contacts.Restore(c) == nil {
				nc++
			}
		}
		_ = s.Contacts.Flush()
	}
	if s.Templates != nil {
		for _, t := range b.Templates {
			if _, err := s.Templates.Save(t); err == nil {
				nt++
			}
		}
	}
	for _, rule := range b.Rules {
		if msgtemplate.Validate(rule.Reply) == nil && s.cfg.AddOrUpdateRule(rule) == nil {
			nr++
		}
	}
	_ = s.cfg.Update(func(c *configT) error {
		bb := b.Business
		if validateBusiness(&bb) == nil && bb.Name != "" {
			c.Business = bb
		}
		if b.BusinessHours != nil {
			bh := *b.BusinessHours
			if validateHours(&bh) == nil {
				c.BusinessHours = bh
			}
		}
		return nil
	})
	return nc, nt, nr
}

func (s *Server) handleWebhookTest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string `json:"url"`
		Secret string `json:"secret"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	snap := s.cfg.Snapshot().Webhook
	url := strings.TrimSpace(req.URL)
	if url == "" {
		url = snap.URL
	}
	secret := strings.TrimSpace(req.Secret)
	if secret == "" || config.IsMasked(secret) {
		secret = snap.Secret
	}
	if url == "" {
		writeError(w, http.StatusBadRequest, "Enter the address of the other system.")
		return
	}
	if s.Hooks == nil {
		writeError(w, http.StatusServiceUnavailable, "Integrations are not available.")
		return
	}
	code, dur, err := s.Hooks.Test(url, secret, time.Duration(snap.TimeoutSeconds)*time.Second)
	security.LogAudit("WEBHOOK_TEST", clientIP(r), actor(r), fmt.Sprintf("Test ping to %s: %v", url, err == nil), "success")
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "status_code": code, "duration": dur.String(), "error": err.Error()})
		return
	}
	writeOK(w, map[string]any{"status_code": code, "duration": dur.String()})
}

func (s *Server) handleWebhookFailed(w http.ResponseWriter, r *http.Request) {
	if s.Hooks == nil {
		writeJSON(w, http.StatusOK, map[string]any{"failed": []any{}, "pending": 0})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"failed": s.Hooks.Failed(), "pending": s.Hooks.PendingCount()})
}

func (s *Server) handleWebhookReplay(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if s.Hooks == nil {
		writeError(w, http.StatusServiceUnavailable, "Integrations are not available.")
		return
	}
	writeOK(w, map[string]any{"queued": s.Hooks.Replay(req.ID)})
}

func (s *Server) handleWebhookClearFailed(w http.ResponseWriter, r *http.Request) {
	if s.Hooks != nil {
		s.Hooks.ClearFailed()
	}
	writeOK(w, nil)
}

// handleInboundWebhook lets a CRM or shop system send a WhatsApp message. It requires the
// webhook secret (header) or an HMAC signature, or the API key.
func (s *Server) handleInboundWebhook(w http.ResponseWriter, r *http.Request) {
	// Other systems call this server to server and send no Origin. A browser always
	// does, so a web page probing this address is refused before anything is logged.
	if origin := r.Header.Get("Origin"); origin != "" && !security.IsAllowedOrigin(origin, r.Host) {
		writeError(w, http.StatusForbidden, "Cross-site request refused.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "Body too large.")
		return
	}
	snap := s.cfg.Snapshot()
	secret, _ := s.cfg.InboundSecret() // the sending key; falls back to the older shared one
	authorized, replayed := false, false
	if a, ok := s.authenticate(r); ok && a.apiKey {
		authorized = true
	} else if secret != "" {
		if security.ConstantTimeEqual(r.Header.Get("X-Webhook-Secret"), secret) {
			authorized = true
		} else if sig := r.Header.Get("X-Signature-SHA256"); sig != "" {
			// The signature covers timestamp + "." + body, and the timestamp must be recent,
			// so a captured request cannot be replayed later.
			ts := r.Header.Get("X-Webhook-Timestamp")
			if sec, err := strconv.ParseInt(ts, 10, 64); err == nil && math.Abs(float64(time.Now().Unix()-sec)) <= 300 &&
				security.VerifyWebhookHMAC(secret, append([]byte(ts+"."), body...), sig) {
				authorized, replayed = true, !s.firstUseOfSignature(sig)
			}
		}
	}
	if !authorized {
		s.logRejected("WEBHOOK_REJECTED", clientIP(r), "Inbound webhook without a valid secret")
		writeError(w, http.StatusUnauthorized, "Missing or wrong webhook secret.")
		return
	}
	if replayed {
		security.LogAudit("WEBHOOK_REPLAY", clientIP(r), "webhook", "A signed request was sent a second time and was refused", "failed")
		writeError(w, http.StatusConflict, "This signed request was already received. Sign each request with a new timestamp.")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Send JSON with Content-Type: application/json.")
		return
	}
	var p struct {
		To            string            `json:"to"`
		Phone         string            `json:"phone"`
		Message       string            `json:"message"`
		TemplateID    string            `json:"template_id"`
		Name          string            `json:"name"`
		Email         string            `json:"email"`
		Tags          []string          `json:"tags"`
		Variables     map[string]string `json:"variables"`
		Consent       bool              `json:"consent"`        // accepted from older integrations, ignored
		ConsentSource string            `json:"consent_source"` // accepted from older integrations, ignored
		// WhatsAppTemplate is a Meta-approved template, sent when the customer has not
		// written in the last 24 hours (or when there is no message).
		WhatsAppTemplate *campaign.Template `json:"whatsapp_template"`
		// IdempotencyKey, when given, makes a repeat of the same request within 24 hours
		// refused instead of sent twice (for example a retry after a timeout).
		IdempotencyKey string `json:"idempotency_key"`
	}
	if err := jsonUnmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "The body is not valid JSON.")
		return
	}
	if p.IdempotencyKey = strings.TrimSpace(p.IdempotencyKey); len(p.IdempotencyKey) > 200 {
		writeError(w, http.StatusBadRequest, "Field 'idempotency_key' can be at most 200 characters.")
		return
	}
	to := p.To
	if to == "" {
		to = p.Phone
	}
	// Resolved once (resolvePhone): a stored number keeps its country, so the Singapore
	// 6591234567 is never India's 916591234567; only another bare mobile gets 91.
	phone, ok := s.requestPhone(w, to)
	if !ok {
		return
	}
	if len(phone) < 7 || len(phone) > 15 {
		writeError(w, http.StatusBadRequest, "Field 'to' must be a phone number with country code.")
		return
	}
	tpl, err := p.WhatsAppTemplate.Clean()
	if err != nil {
		writeError(w, http.StatusBadRequest, "whatsapp_template: "+err.Error())
		return
	}
	text := strings.TrimSpace(p.Message)
	if text == "" && p.TemplateID != "" && s.Templates != nil {
		if t, ok := s.Templates.Get(p.TemplateID); ok {
			text = t.Content
		}
	}
	if text == "" && tpl == nil {
		writeError(w, http.StatusBadRequest, "Field 'message' (or a valid 'template_id', or 'whatsapp_template') is required.")
		return
	}
	extraKeys := []string{"email", "notes"}
	for k := range p.Variables {
		extraKeys = append(extraKeys, k)
	}
	if text != "" {
		if err := msgtemplate.ValidateOutgoing(text, extraKeys...); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// A new number is only saved once a message to it went out (or was recorded in
	// practice mode): a refused request must not fill the contact list.
	var ct contacts.Contact
	known := false
	if s.Contacts != nil {
		// Exactly the number resolved above: this decides who gets the message.
		if c, ok := s.Contacts.GetExact(phone); ok {
			ct, phone, known = *c, c.Phone, true
		}
	}
	if !known {
		ct = contacts.Contact{Phone: phone, Name: p.Name, Email: p.Email, Tags: p.Tags, Source: "webhook"}
		// Opted out and then deleted: still do-not-message (checked below like any opt-out).
		ct.IsDND = s.Contacts != nil && s.Contacts.WasOptedOut(phone)
	}
	saveNew := func() {
		if !known && s.Contacts != nil {
			// Saved under exactly the number the message went to: Save would read
			// 6591234567 as India's 916591234567 and overwrite that customer.
			_ = s.Contacts.SaveExact(ct)
		}
	}
	// "consent" in the request is only the other system's claim. It is not recorded and
	// does not open the 24-hour window: only an opt-in recorded in Pulse counts.
	a := snap.Automation
	useTemplate := text == "" || campaign.NeedsTemplate(&ct, time.Now())
	switch {
	case ct.IsDND:
		writeError(w, http.StatusConflict, "The customer opted out of messages.")
		return
	case inPhoneList(a.BlacklistPhones, phone):
		writeError(w, http.StatusConflict, "This number is on the blocked list.")
		return
	case a.AllowListOnly && !s.Contacts.AllowListed(a.AllowList, phone):
		writeError(w, http.StatusConflict, "Test mode is on and this number is not on the allow-list.")
		return
	case useTemplate && tpl == nil:
		writeError(w, http.StatusConflict, campaign.OutsideWindowMessage+" An integration can send one with 'whatsapp_template'.")
		return
	case useTemplate && a.RequireOptIn && !ct.Consent:
		// The same rule as campaigns: WhatsApp requires opt-in before a business starts a chat.
		writeError(w, http.StatusConflict, "No opt-in is recorded in Pulse for this customer, so no template is sent. Record their consent on the contact first.")
		return
	}
	releaseDaily, ok := campaign.ReserveDaily(snap, s.History, time.Now())
	if !ok {
		writeError(w, http.StatusTooManyRequests, "Today's sending limit has been reached.")
		return
	}
	defer releaseDaily()
	vars := map[string]string{"email": ct.Email, "notes": ct.Notes}
	for k, v := range p.Variables {
		vars[k] = v
	}
	name := p.Name
	if name == "" {
		name = ct.Name
	}
	mv := msgtemplate.Vars{Name: name, Phone: phone, Business: snap.Business, Extra: vars, Now: time.Now(), Location: msgtemplate.Location(snap.BusinessHours.Timezone)}
	var rendered string
	var missing []string
	var parts cloudapi.TemplateParts
	if useTemplate {
		parts, missing = tpl.Render(mv)
		rendered = tpl.Describe(parts)
	} else {
		rendered, missing = msgtemplate.Render(text, mv)
	}
	if len(missing) > 0 {
		writeError(w, http.StatusBadRequest, msgtemplate.MissingError(missing).Error())
		return
	}
	if !useTemplate {
		if err := msgtemplate.LengthError(rendered, false); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	// Labelled, so a send whose answer is lost is corrected when Meta reports it. With an
	// idempotency_key the label is made from it, so the history remembers the key across
	// restarts too.
	rec := history.MessageRecord{Direction: history.DirectionOutgoing, Type: history.TypeWebhook, Phone: phone, Text: rendered, SentBy: "webhook", Label: IntegrationLabel + security.RandomToken(8)}
	if p.IdempotencyKey != "" {
		rec.Label = idempotencyLabel(p.IdempotencyKey)
		switch integrationKeys.claim(p.IdempotencyKey, time.Now()) {
		case claimBusy:
			writeError(w, http.StatusConflict, "A request with this idempotency_key is still in progress — retry later.")
			return
		case claimUsed:
			writeError(w, http.StatusConflict, idempotencyUsed)
			return
		}
		defer integrationKeys.finish(p.IdempotencyKey)
		if s.History != nil && s.History.HasLabel(rec.Label, time.Now().Add(-idempotencyTTL)) {
			writeError(w, http.StatusConflict, idempotencyUsed) // received before Pulse restarted
			return
		}
	}
	if a.DryRun {
		rec.ID, rec.Status = "dry_"+security.RandomToken(6), history.StatusDryRun
		if s.History != nil {
			s.History.Record(rec)
		}
		saveNew()
		writeOK(w, map[string]any{"dry_run": true, "text": rendered, "template": useTemplate})
		return
	}
	if s.Messenger == nil || !s.status().Connected {
		integrationKeys.release(p.IdempotencyKey) // nothing was sent: the same key may be tried again
		writeError(w, http.StatusServiceUnavailable, "WhatsApp is not connected.")
		return
	}
	// Recorded as "sending" first (see campaign.SendRecorded), and not cut off when the
	// caller hangs up.
	id, err := campaign.SendRecorded(r.Context(), s.History, rec, directSendTimeout, func(sctx context.Context) (string, error) {
		if useTemplate {
			return campaign.SendTemplate(sctx, s.Messenger, phone, *tpl, parts)
		}
		return s.Messenger.SendTextMessage(sctx, phone, rendered)
	})
	if err != nil && cloudapi.MaybeSent(err) {
		// Meta may have the message: recorded as failed with its label (corrected when
		// Meta reports it) and counted towards the daily limit. The caller must not retry.
		saveNew()
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "unknown", "message": "may have been sent; do not retry", "to": "+" + phone, "error": err.Error()})
		return
	}
	if err != nil {
		integrationKeys.release(p.IdempotencyKey) // refused by WhatsApp: nothing was sent
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	saveNew()
	if s.Contacts != nil {
		s.Contacts.UpdateLastContacted(phone)
	}
	writeOK(w, map[string]any{"message_id": id, "to": "+" + phone, "template": useTemplate})
}

// IntegrationLabel prefixes the label of messages sent through the integration endpoint
// (biz_opaque_callback_data), so a send whose answer was lost is corrected later.
const IntegrationLabel = "pulse:w:"

// Idempotency keys of integration requests are remembered for 24 hours, at most
// idempotencyMax of them (the oldest are forgotten first).
const (
	idempotencyTTL = 24 * time.Hour
	idempotencyMax = 10000
)

// idempotencyUsed answers a request whose idempotency_key was already used.
const idempotencyUsed = "A request with this idempotency_key was already received in the last 24 hours, so it was not sent again."

// idempotencyBusyFor is the longest a request with a key can still be in progress (the
// send timeout with room to spare); after that its key simply counts as used.
const idempotencyBusyFor = 2 * directSendTimeout

// idempotencyLabel is the label of a message sent for an idempotency_key: a hash of the
// key, so the history remembers it across restarts without storing or sending the key.
func idempotencyLabel(key string) string {
	sum := sha256.Sum256([]byte("pulse-idempotency:" + key))
	return IntegrationLabel + "k:" + hex.EncodeToString(sum[:16])
}

// idempotencyKeys remembers integration requests by their idempotency_key, in memory;
// the history keeps them across restarts (idempotencyLabel).
type idempotencyKeys struct {
	mu   sync.Mutex
	seen map[string]time.Time
	busy map[string]bool // the first request with the key is still being handled
}

var integrationKeys = &idempotencyKeys{}

type claimResult int

const (
	claimNew  claimResult = iota // not used in the last 24 hours: go ahead
	claimBusy                    // the first request with this key is still in progress
	claimUsed                    // already received
)

// claim records key and reports whether it is new, still in progress or already used.
func (k *idempotencyKeys) claim(key string, now time.Time) claimResult {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.seen == nil {
		k.seen, k.busy = map[string]time.Time{}, map[string]bool{}
	}
	if at, dup := k.seen[key]; dup && now.Sub(at) < idempotencyTTL {
		if k.busy[key] && now.Sub(at) < idempotencyBusyFor {
			return claimBusy
		}
		return claimUsed
	}
	if len(k.seen) >= idempotencyMax {
		for x, at := range k.seen {
			if now.Sub(at) >= idempotencyTTL {
				delete(k.seen, x)
			}
		}
		for len(k.seen) >= idempotencyMax {
			oldest, first := "", true
			for x, at := range k.seen {
				if first || at.Before(k.seen[oldest]) {
					oldest, first = x, false
				}
			}
			delete(k.seen, oldest)
		}
		for x := range k.busy {
			if _, ok := k.seen[x]; !ok {
				delete(k.busy, x)
			}
		}
	}
	k.seen[key] = now
	k.busy[key] = true
	return claimNew
}

// finish marks the request with key as done; the key stays used for 24 hours.
func (k *idempotencyKeys) finish(key string) {
	if key == "" {
		return
	}
	k.mu.Lock()
	delete(k.busy, key)
	k.mu.Unlock()
}

// release forgets key after a request that certainly sent nothing, so it can be retried.
func (k *idempotencyKeys) release(key string) {
	if key == "" {
		return
	}
	k.mu.Lock()
	delete(k.seen, key)
	delete(k.busy, key)
	k.mu.Unlock()
}

// replayWindow is how long a used webhook signature is remembered. Timestamps are
// accepted up to 5 minutes either side of now, so 10 minutes covers every valid reuse.
const replayWindow = 10 * time.Minute

// firstUseOfSignature records a verified webhook signature and reports whether it is new.
// Old entries are dropped on each insert, so the map stays small.
func (s *Server) firstUseOfSignature(sig string) bool {
	sig = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(sig), "sha256="))
	now := time.Now()
	s.sigMu.Lock()
	defer s.sigMu.Unlock()
	if s.seenSigs == nil {
		s.seenSigs = map[string]time.Time{}
	}
	for k, at := range s.seenSigs {
		if now.Sub(at) > replayWindow {
			delete(s.seenSigs, k)
		}
	}
	if _, dup := s.seenSigs[sig]; dup {
		return false
	}
	s.seenSigs[sig] = now
	return true
}
