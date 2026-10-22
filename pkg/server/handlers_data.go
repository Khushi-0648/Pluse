package server

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"whatsapp-automation/pkg/campaign"
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
		y, m, d := time.Now().AddDate(0, 0, -days+1).Date()
		since = time.Date(y, m, d, 0, 0, 0, 0, time.Local)
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

func (s *Server) handleAnalyticsCSV(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="messages-%s.csv"`, time.Now().Format("20060102")))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"time", "direction", "type", "phone", "name", "status", "rule", "campaign", "text"})
	if s.History != nil {
		f := false
		for _, m := range s.History.Query(history.Query{Imported: &f}) {
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
	phone := contacts.CleanPhone(r.URL.Query().Get("phone"))
	bundle := map[string]any{"product": version.Product, "version": version.Version, "exported_at": time.Now().Format(time.RFC3339)}
	name := "all"
	if phone != "" {
		name = phone
		if s.Contacts != nil {
			if c, ok := s.Contacts.Get(phone); ok {
				bundle["contact"] = c
			}
		}
		if s.History != nil {
			bundle["messages"] = s.History.Query(history.Query{Phone: phone})
		}
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
		s.deleteAllCustomerData()
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
	phone := contacts.CleanPhone(req.Phone)
	if phone == "" {
		writeError(w, http.StatusBadRequest, "Enter the phone number.")
		return
	}
	msgs := 0
	if s.History != nil {
		msgs = s.History.ClearPhone(phone)
	}
	if s.Contacts != nil {
		s.Contacts.Delete(phone)
	}
	if s.Scheduler != nil {
		for _, j := range s.Scheduler.List() {
			if j.Recipient == phone {
				_ = s.Scheduler.Delete(j.ID)
			}
		}
	}
	if s.Campaigns != nil {
		s.Campaigns.ForgetPhone(phone)
	}
	if s.Hooks != nil {
		s.Hooks.Forget("+" + phone)
	}
	security.LogAudit("CONTACT_FORGOTTEN", clientIP(r), actor(r), fmt.Sprintf("All data for +%s deleted (%d messages)", phone, msgs), "success")
	writeOK(w, map[string]any{"message": fmt.Sprintf("Deleted the contact and %d messages.", msgs)})
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
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "Body too large.")
		return
	}
	snap := s.cfg.Snapshot()
	secret := snap.Webhook.Secret
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
		security.LogAudit("WEBHOOK_REJECTED", clientIP(r), "unknown", "Inbound webhook without a valid secret", "failed")
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
	}
	if err := jsonUnmarshal(body, &p); err != nil {
		writeError(w, http.StatusBadRequest, "The body is not valid JSON.")
		return
	}
	to := p.To
	if to == "" {
		to = p.Phone
	}
	phone := contacts.CleanPhone(to)
	if len(phone) < 7 || len(phone) > 15 {
		writeError(w, http.StatusBadRequest, "Field 'to' must be a phone number with country code.")
		return
	}
	text := strings.TrimSpace(p.Message)
	if text == "" && p.TemplateID != "" && s.Templates != nil {
		if t, ok := s.Templates.Get(p.TemplateID); ok {
			text = t.Content
		}
	}
	if text == "" {
		writeError(w, http.StatusBadRequest, "Field 'message' (or a valid 'template_id') is required.")
		return
	}
	extraKeys := []string{"email", "notes"}
	for k := range p.Variables {
		extraKeys = append(extraKeys, k)
	}
	if err := msgtemplate.Validate(text, extraKeys...); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.Contacts != nil {
		if _, ok := s.Contacts.Get(phone); !ok {
			_ = s.Contacts.Save(contacts.Contact{Phone: phone, Name: p.Name, Email: p.Email, Tags: p.Tags, Source: "webhook"})
		}
		// "consent" in the request is only the other system's claim. It is not recorded and
		// does not open the 24-hour window: only an opt-in recorded in Pulse counts.
	}
	var ct contacts.Contact
	if s.Contacts != nil {
		if c, ok := s.Contacts.Get(phone); ok {
			ct = *c
		}
	}
	a := snap.Automation
	switch {
	case ct.IsDND:
		writeError(w, http.StatusConflict, "The customer opted out of messages.")
		return
	case inPhoneList(a.BlacklistPhones, phone):
		writeError(w, http.StatusConflict, "This number is on the blocked list.")
		return
	case a.AllowListOnly && !inPhoneList(a.AllowList, phone):
		writeError(w, http.StatusConflict, "Test mode is on and this number is not on the allow-list.")
		return
	case campaign.NeedsTemplate(&ct, time.Now()):
		writeError(w, http.StatusConflict, campaign.OutsideWindowMessage)
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
	rendered, missing := msgtemplate.Render(text, msgtemplate.Vars{Name: name, Phone: phone, Business: snap.Business, Extra: vars})
	if len(missing) > 0 {
		writeError(w, http.StatusBadRequest, msgtemplate.MissingError(missing).Error())
		return
	}
	rec := history.MessageRecord{Direction: history.DirectionOutgoing, Type: history.TypeWebhook, Phone: phone, Text: rendered, SentBy: "webhook"}
	if a.DryRun {
		rec.ID, rec.Status = "dry_"+security.RandomToken(6), history.StatusDryRun
		if s.History != nil {
			s.History.Record(rec)
		}
		writeOK(w, map[string]any{"dry_run": true, "text": rendered})
		return
	}
	if s.Messenger == nil || !s.status().Connected {
		writeError(w, http.StatusServiceUnavailable, "WhatsApp is not connected.")
		return
	}
	id, err := s.Messenger.SendTextMessage(r.Context(), phone, rendered)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	rec.ID, rec.Status = id, history.StatusSent
	if s.History != nil {
		s.History.Record(rec)
	}
	if s.Contacts != nil {
		s.Contacts.UpdateLastContacted(phone)
	}
	writeOK(w, map[string]any{"message_id": id, "to": "+" + phone})
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
