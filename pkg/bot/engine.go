package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/notify"
	"whatsapp-automation/pkg/security"
	"whatsapp-automation/pkg/webhook"
)

// Messenger is the part of the WhatsApp connection the engine needs.
type Messenger interface {
	// SendReply sends text to phone, shown as a reply to quotedID when it is set.
	SendReply(ctx context.Context, phone, text, quotedID string) (string, error)
	// MarkAsRead shows the customer's message as read.
	MarkAsRead(ctx context.Context, messageID string) error
	// ShowTyping shows "typing..." in the customer's chat until the reply arrives.
	ShowTyping(ctx context.Context, phone string) error
}

// Incoming is a customer's WhatsApp message reduced to what the engine needs. The
// official API only delivers one-to-one chats, so there are no groups here.
type Incoming struct {
	ID       string // WhatsApp's message ID
	Phone    string // the customer's number, digits only
	PushName string // the customer's WhatsApp profile name
	Text     string
	Media    string // "Photo", "Document"... for messages that are not plain text
	SentAt   time.Time
}

// Outcome describes what the engine did with a message (used by tests and the rule tester).
type Outcome struct {
	Reply    string `json:"reply"`
	Rule     string `json:"rule"`
	Skipped  string `json:"skipped,omitempty"`
	Handover bool   `json:"handover"`
	DryRun   bool   `json:"dry_run"`
	SendErr  string `json:"send_error,omitempty"`
}

const (
	menuSessionTTL  = 15 * time.Minute
	awayRepeatAfter = 4 * time.Hour
	fallbackRepeat  = 30 * time.Minute
	loopWindow      = 10 * time.Minute
)

// Engine runs the automation pipeline for incoming messages.
type Engine struct {
	msg      Messenger
	cfg      *config.Config
	contacts *contacts.Store
	hist     *history.Store
	hooks    *webhook.Dispatcher
	notifier notify.Notifier
	now      func() time.Time

	mu         sync.Mutex
	menus      map[string]time.Time
	lastAway   map[string]time.Time
	lastFall   map[string]time.Time
	capFlagged map[string]time.Time
	limitNoted time.Time
	queues     map[string]chan Incoming
	stopping   bool           // Stop was called: new messages are only recorded
	stopCh     chan struct{}  // closed when Stop is called to wake idle workers
	inflight   sync.WaitGroup // queued messages not yet processed
}

// NewEngine creates the engine. m, hooks and notifier may be nil.
func NewEngine(m Messenger, cfg *config.Config, cs *contacts.Store, hs *history.Store, hooks *webhook.Dispatcher, n notify.Notifier) *Engine {
	e := &Engine{msg: m, cfg: cfg, contacts: cs, hist: hs, hooks: hooks, notifier: n, now: time.Now, stopCh: make(chan struct{})}
	e.Reset()
	return e
}

// SetMessenger replaces the sender (tests).
func (e *Engine) SetMessenger(m Messenger) { e.msg = m }

// SetClock replaces the clock (tests).
func (e *Engine) SetClock(now func() time.Time) { e.now = now }

// Stop stops automatic replies and waits, at most until ctx ends, for the messages
// already queued to be processed. Messages that arrive afterwards are still recorded
// and their opt-outs applied, but never answered. Call it before the final save.
func (e *Engine) Stop(ctx context.Context) bool {
	e.mu.Lock()
	if !e.stopping {
		e.stopping = true
		if e.stopCh != nil {
			close(e.stopCh)
		}
	}
	e.mu.Unlock()
	done := make(chan struct{})
	go func() {
		e.inflight.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		fmt.Println("[Bot] Some received messages were still being handled at shutdown.")
		return false
	}
}

// Reset clears in-memory conversation state.
func (e *Engine) Reset() {
	e.mu.Lock()
	e.menus, e.lastAway, e.lastFall, e.capFlagged = map[string]time.Time{}, map[string]time.Time{}, map[string]time.Time{}, map[string]time.Time{}
	if e.queues == nil {
		e.queues = map[string]chan Incoming{}
	}
	e.mu.Unlock()
}

// Receive queues a customer's message for the automation pipeline. Messages without a
// number or without any content are ignored.
func (e *Engine) Receive(in Incoming) {
	in.Phone = contacts.CleanPhone(in.Phone)
	in.PushName, in.Text = strings.TrimSpace(in.PushName), strings.TrimSpace(in.Text)
	if in.Phone == "" || in.ID == "" || (in.Text == "" && in.Media == "") {
		return
	}
	e.enqueue(in)
}

// enqueue processes messages from the same chat strictly in order, while different
// customers are handled in parallel.
func (e *Engine) enqueue(in Incoming) {
	key := in.Phone
	// The send happens under the lock so an idle worker cannot remove the queue in between.
	e.mu.Lock()
	if e.stopping {
		e.mu.Unlock()
		e.process(in, true) // shutting down: record it and apply STOP/START, no reply
		return
	}
	q, ok := e.queues[key]
	if !ok {
		q = make(chan Incoming, 64)
		e.queues[key] = q
		go e.worker(key, q)
	}
	queued := true
	e.inflight.Add(1)
	select {
	case q <- in:
	default:
		queued = false
		e.inflight.Done()
	}
	e.mu.Unlock()
	if !queued {
		fmt.Printf("[Bot] Queue full for %s; message %s recorded without automation\n", key, in.ID)
		e.record(in, e.cfg.Snapshot())
		// An opt-out must never be lost, even when the chat is flooding.
		if IsOptOut(in.Text) && e.contacts != nil {
			e.contacts.SetDND(in.Phone, true, "Replied "+strings.ToUpper(Normalize(in.Text))+" on "+e.now().Format("2006-01-02"))
		}
	}
}

func (e *Engine) worker(key string, q chan Incoming) {
	idle := time.NewTimer(5 * time.Minute)
	defer idle.Stop()
	for {
		select {
		case in := <-q:
			func() {
				defer e.inflight.Done()
				defer func() {
					if r := recover(); r != nil {
						fmt.Printf("[Bot] Recovered from error handling message %s: %v\n", in.ID, r)
					}
				}()
				e.Process(in)
			}()
			idle.Reset(5 * time.Minute)
		case <-idle.C:
			e.mu.Lock()
			if len(q) == 0 {
				delete(e.queues, key)
				e.mu.Unlock()
				return
			}
			e.mu.Unlock()
			idle.Reset(time.Minute)
		case <-e.stopCh:
			for {
				select {
				case in := <-q:
					func() {
						defer e.inflight.Done()
						defer func() {
							if r := recover(); r != nil {
								fmt.Printf("[Bot] Recovered from error handling message %s: %v\n", in.ID, r)
							}
						}()
						e.Process(in)
					}()
				default:
					return
				}
			}
		}
	}
}

func (e *Engine) record(in Incoming, cfg config.Config) {
	if e.hist == nil {
		return
	}
	e.hist.Record(history.MessageRecord{ID: in.ID, Timestamp: e.now(), Phone: in.Phone, Text: in.Text, Media: in.Media,
		Direction: history.DirectionIncoming, Type: history.TypeUserMessage, Name: in.PushName})
}

// Process runs the full pipeline for one message and reports what happened.
func (e *Engine) Process(in Incoming) Outcome { return e.process(in, false) }

// process handles one message; when quiet it is only recorded (with STOP/START
// applied) and never answered.
func (e *Engine) process(in Incoming, quiet bool) Outcome {
	cfg := e.cfg.Snapshot()
	a := cfg.Automation
	now := e.now()

	// The 24-hour window starts when the customer wrote, not when Pulse came back online
	// and received the message.
	sentAt := now
	if !in.SentAt.IsZero() && in.SentAt.Before(now) {
		sentAt = in.SentAt
	}
	stale := now.Sub(sentAt) > 24*time.Hour
	var contact contacts.Contact
	if e.contacts != nil {
		contact = e.contacts.Upsert(in.Phone, in.PushName, "whatsapp")
		e.contacts.RecordInbound(in.Phone, sentAt)
		if c, ok := e.contacts.Get(in.Phone); ok {
			contact = *c
		}
	}
	// Recorded with the local arrival time before any reply, so the inbox order is always right.
	e.record(in, cfg)
	e.hooks.Send("message.received", map[string]any{
		"message_id": in.ID, "phone": "+" + in.Phone, "name": contact.Name, "text": in.Text, "media": in.Media,
		"is_group": false, "received_at": now.UTC(),
	})

	if in.Text == "" {
		return Outcome{Skipped: "no text"}
	}

	name := contact.Name
	if name == "" {
		name = in.PushName
	}
	render := func(text string) (string, []string) {
		return msgtemplate.Render(text, msgtemplate.Vars{Name: name, Phone: in.Phone, Message: in.Text, Business: cfg.Business, Now: now, Location: Location(cfg.BusinessHours.Timezone)})
	}

	blocked := contains(a.BlacklistPhones, in.Phone)
	outsideTest := a.AllowListOnly && !contains(a.AllowList, in.Phone)
	mayReply := a.Enabled && !blocked && !outsideTest && !stale && !quiet

	// Opt-out and opt-in are always recorded, even while automation is off or the chat is
	// paused; the confirmation is only sent when automatic replies may be sent, and only
	// when the status actually changed (repeating STOP/START must not trigger replies).
	if IsOptOut(in.Text) {
		if e.contacts != nil {
			e.contacts.SetDND(in.Phone, true, "Replied "+strings.ToUpper(Normalize(in.Text))+" on "+now.Format("2006-01-02"))
		}
		security.LogAudit("OPT_OUT", "+"+in.Phone, name, "Customer opted out with \""+in.Text+"\"", "success")
		e.hooks.Send("contact.opted_out", map[string]any{"phone": "+" + in.Phone, "name": name})
		if !mayReply || contact.IsDND {
			return Outcome{Rule: "system:opt_out", Skipped: "opt-out recorded; no confirmation sent"}
		}
		reply, _ := render(a.OptOutReply)
		return e.reply(in, cfg, reply, "system:opt_out", true)
	}
	if IsOptIn(in.Text) {
		already := contact.Consent && !contact.IsDND
		if e.contacts != nil {
			e.contacts.SetConsent(in.Phone, true, "Replied "+strings.ToUpper(Normalize(in.Text))+" on "+now.Format("2006-01-02"))
		}
		security.LogAudit("OPT_IN", "+"+in.Phone, name, "Customer opted in with \""+in.Text+"\"", "success")
		e.hooks.Send("contact.opted_in", map[string]any{"phone": "+" + in.Phone, "name": name})
		if !mayReply || already {
			return Outcome{Rule: "system:opt_in", Skipped: "opt-in recorded; no confirmation sent"}
		}
		reply, _ := render(a.OptInReply)
		return e.reply(in, cfg, reply, "system:opt_in", true)
	}

	switch {
	case quiet:
		return Outcome{Skipped: "replies are off"}
	case !a.Enabled:
		return Outcome{Skipped: "automation is switched off"}
	case stale:
		return Outcome{Skipped: "message is more than 24 hours old"}
	case contact.IsDND:
		return Outcome{Skipped: "customer opted out"}
	case blocked:
		return Outcome{Skipped: "number is blocked"}
	case outsideTest:
		return Outcome{Skipped: "test mode is on and this is not one of the test numbers"}
	case contact.BotPaused(now):
		return Outcome{Skipped: "chat is with a person"}
	}

	// The limits apply to the hand-over reply too; the chat is still handed over (and
	// shown under Needs attention) when the reply itself is held back.
	limited := e.overLimits(in, name, cfg, now)
	if IsHandoverRequest(in.Text) {
		e.handover(in, name, "Customer asked for a person: \""+truncate(in.Text, 80)+"\"", cfg)
		if limited != "" {
			return Outcome{Rule: "system:handover", Skipped: limited, Handover: true}
		}
		reply, _ := render(a.HandoverReply)
		out := e.reply(in, cfg, reply, "system:handover", true)
		out.Handover = true
		return out
	}

	if limited != "" {
		return Outcome{Skipped: limited}
	}

	if cfg.BusinessHours.Enabled && !IsOpen(cfg.BusinessHours, now) {
		e.mu.Lock()
		last := e.lastAway[in.Phone]
		e.mu.Unlock()
		if now.Sub(last) > awayRepeatAfter {
			reply, missing := render(cfg.BusinessHours.AwayReply)
			if len(missing) == 0 && reply != "" {
				e.mu.Lock()
				e.lastAway[in.Phone] = now
				e.mu.Unlock()
				return e.reply(in, cfg, reply, "system:away", false)
			}
		}
	}

	e.mu.Lock()
	menuActive := now.Sub(e.menus[in.Phone]) < menuSessionTTL
	e.mu.Unlock()

	rules := e.cfg.GetRules()
	var unanswered []string // rules that matched but need a detail that is still empty
	var missingAll []string
	for len(rules) > 0 {
		rule, ok := FindRule(rules, in.Text, menuActive)
		if !ok {
			break
		}
		reply, missing := render(rule.Reply)
		if len(missing) > 0 || reply == "" {
			security.LogAudit("RULE_SKIPPED", "local", "system", fmt.Sprintf("Rule %q was not sent because %s is empty", rule.Name, strings.Join(missing, ", ")), "warning")
			unanswered = append(unanswered, rule.Name)
			missingAll = append(missingAll, missing...)
			rules = without(rules, rule.Name)
			continue
		}
		if rule.Action == "handover" {
			e.handover(in, name, "Customer chose \""+truncate(in.Text, 40)+"\" (rule "+rule.Name+")", cfg)
			out := e.reply(in, cfg, reply, rule.Name, true)
			out.Handover = true
			return out
		}
		out := e.reply(in, cfg, reply, rule.Name, false)
		if out.SendErr == "" && out.Skipped == "" {
			e.mu.Lock()
			if HasMenu(reply) {
				e.menus[in.Phone] = now
			}
			e.mu.Unlock()
		}
		return out
	}

	// A rule matched but its answer needs a detail the owner has not entered yet
	// (C27/FT19): the customer gets a holding reply and the chat is handed over to staff.
	if len(unanswered) > 0 {
		reply, miss := render(a.MissingDetailReply)
		if len(miss) > 0 || strings.TrimSpace(reply) == "" {
			reply = config.DefaultMissingDetailReply
		}
		reason := fmt.Sprintf("Asked \"%s\" but %s is empty in Settings > Business details", truncate(in.Text, 40), strings.Join(dedupe(missingAll), ", "))
		e.handover(in, name, reason, cfg)
		out := e.reply(in, cfg, reply, "system:missing_detail", true)
		out.Handover = true
		return out
	}

	if a.EnableDefaultReply && strings.TrimSpace(a.DefaultReply) != "" {
		e.mu.Lock()
		last := e.lastFall[in.Phone]
		e.mu.Unlock()
		if now.Sub(last) < fallbackRepeat {
			return Outcome{Skipped: "fallback reply already sent recently"}
		}
		reply, missing := render(a.DefaultReply)
		if len(missing) == 0 && reply != "" {
			e.mu.Lock()
			e.lastFall[in.Phone] = now
			e.mu.Unlock()
			return e.reply(in, cfg, reply, "system:fallback", false)
		}
	}
	return Outcome{Skipped: "no rule matched"}
}

func dedupe(list []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range list {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func without(rules []config.Rule, name string) []config.Rule {
	out := make([]config.Rule, 0, len(rules))
	for _, r := range rules {
		if r.Name != name {
			out = append(out, r)
		}
	}
	return out
}

func contains(list []string, phone string) bool {
	for _, p := range list {
		if c := contacts.CleanPhone(p); c != "" && c == phone {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// overLimits applies the cooldown, per-contact reply cap, loop detection and daily limit.
func (e *Engine) overLimits(in Incoming, name string, cfg config.Config, now time.Time) string {
	a := cfg.Automation
	if e.hist == nil {
		return ""
	}
	if last, ok := e.hist.LastAutoReply(in.Phone); ok && a.CooldownSeconds > 0 && now.Sub(last.Timestamp) < time.Duration(a.CooldownSeconds)*time.Second {
		return "cooldown: replied less than " + fmt.Sprint(a.CooldownSeconds) + " seconds ago"
	}
	if n := e.hist.AutoRepliesTo(in.Phone, now.Add(-time.Hour)); a.MaxRepliesPerContactHr > 0 && n >= a.MaxRepliesPerContactHr {
		e.flagOnce(in.Phone, name, fmt.Sprintf("Reached %d automatic replies in one hour", n), cfg)
		return "per-contact hourly reply cap reached"
	}
	recent := e.hist.RecentIncoming(in.Phone, 4)
	if len(recent) == 4 && now.Sub(recent[3].Timestamp) < loopWindow {
		same := true
		for _, m := range recent[1:] {
			if Normalize(m.Text) != Normalize(recent[0].Text) {
				same = false
				break
			}
		}
		if same && e.hist.AutoRepliesTo(in.Phone, now.Add(-loopWindow)) >= 3 {
			if e.contacts != nil {
				e.contacts.PauseBot(in.Phone, time.Hour, "Paused for 1 hour: the same message kept arriving (possible bot loop)")
			}
			security.LogAudit("LOOP_DETECTED", "+"+in.Phone, name, "Repeated identical messages; automation paused for 1 hour", "warning")
			return "possible bot loop; chat paused"
		}
	}
	limit := a.DailyLimit
	if a.WarmupMode && limit > 50 {
		limit = 50
	}
	y, m, d := now.Date()
	if limit > 0 && e.hist.SentSince(time.Date(y, m, d, 0, 0, 0, 0, now.Location())) >= limit {
		e.mu.Lock()
		note := now.Sub(e.limitNoted) > time.Hour
		if note {
			e.limitNoted = now
		}
		e.mu.Unlock()
		if note {
			security.LogAudit("DAILY_LIMIT_REACHED", "local", "system", fmt.Sprintf("Daily sending limit of %d reached; automatic replies paused until midnight", limit), "warning")
		}
		return "daily sending limit reached"
	}
	return ""
}

func (e *Engine) flagOnce(phone, name, reason string, cfg config.Config) {
	e.mu.Lock()
	last := e.capFlagged[phone]
	if time.Since(last) < time.Hour {
		e.mu.Unlock()
		return
	}
	e.capFlagged[phone] = time.Now()
	e.mu.Unlock()
	if e.contacts != nil {
		e.contacts.PauseBot(phone, time.Hour, reason)
	}
	security.LogAudit("REPLY_CAP", "+"+phone, name, reason+"; automation paused for 1 hour", "warning")
	if e.notifier != nil {
		e.notifier.Notify("Chat needs attention", displayName(name, phone)+": "+reason)
	}
}

func displayName(name, phone string) string {
	if msgtemplate.IsPlaceholderName(name) {
		return "+" + phone
	}
	return name
}

func (e *Engine) handover(in Incoming, name, reason string, cfg config.Config) {
	hours := cfg.Automation.HandoverPauseHours
	if e.contacts != nil {
		e.contacts.PauseBot(in.Phone, time.Duration(hours)*time.Hour, reason)
	}
	security.LogAudit("AGENT_HANDOVER", "+"+in.Phone, name, fmt.Sprintf("%s. Automatic replies paused for %d hours.", reason, hours), "success")
	if e.notifier != nil {
		e.notifier.Notify("Customer wants a person", displayName(name, in.Phone)+": "+truncate(in.Text, 120))
	}
	e.hooks.Send("handover.requested", map[string]any{"phone": "+" + in.Phone, "name": name, "text": in.Text, "reason": reason, "paused_hours": hours})
}

// reply sends (or, in dry-run mode, only records) an automatic reply.
func (e *Engine) reply(in Incoming, cfg config.Config, text, rule string, essential bool) Outcome {
	a := cfg.Automation
	out := Outcome{Reply: text, Rule: rule}
	if strings.TrimSpace(text) == "" {
		out.Skipped = "empty reply"
		return out
	}
	rec := history.MessageRecord{Direction: history.DirectionOutgoing, Type: history.TypeAutoReply, Phone: in.Phone, Text: text, RuleTriggered: rule}
	if a.DryRun {
		out.DryRun = true
		rec.ID = "dry_" + security.RandomToken(6)
		rec.Status = history.StatusDryRun
		rec.Timestamp = e.now()
		if e.hist != nil {
			e.hist.Record(rec)
		}
		return out
	}
	if e.msg == nil {
		out.SendErr = "not connected"
		return out
	}
	// Opt-out and opt-in confirmations always go out; every other reply takes a place
	// in today's limit first, so parallel chats and campaigns cannot overshoot it.
	if rule != "system:opt_out" && rule != "system:opt_in" {
		release, ok := campaign.ReserveDaily(cfg, e.hist, e.now())
		if !ok {
			out.Skipped = "daily sending limit reached"
			return out
		}
		defer release() // runs after the reply is recorded below
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// Read receipts and "typing..." are courtesies: they never hold up or fail a reply.
	if a.AutoRead && !a.TypingSimulation {
		_ = e.msg.MarkAsRead(ctx, in.ID)
	}
	if a.TypingSimulation && !essential {
		d := time.Duration(a.TypingDurationMs) * time.Millisecond
		if d <= 0 {
			d = 1500 * time.Millisecond
		}
		if d > 8*time.Second {
			d = 8 * time.Second
		}
		_ = e.msg.ShowTyping(ctx, in.Phone) // also marks the message as read
		select {
		case <-time.After(d):
		case <-ctx.Done():
		}
	} else if a.TypingSimulation && a.AutoRead {
		_ = e.msg.MarkAsRead(ctx, in.ID)
	}
	id, err := e.msg.SendReply(ctx, in.Phone, text, in.ID)
	rec.ID, rec.Timestamp, rec.Status = id, e.now(), history.StatusSent
	if err != nil {
		rec.ID = "fail_" + security.RandomToken(6)
		rec.Status, rec.Error = history.StatusFailed, err.Error()
		out.SendErr = err.Error()
	}
	if e.hist != nil {
		e.hist.Record(rec)
	}
	if e.contacts != nil && err == nil {
		e.contacts.UpdateLastContacted(in.Phone)
	}
	return out
}

// Simulate shows which reply a message would get, without sending anything.
func (e *Engine) Simulate(text, name string, menuActive bool) Outcome {
	cfg := e.cfg.Snapshot()
	now := e.now()
	vars := msgtemplate.Vars{Name: name, Phone: "919800000000", Message: text, Business: cfg.Business, Now: now, Location: Location(cfg.BusinessHours.Timezone)}
	switch {
	case IsOptOut(text):
		r, _ := msgtemplate.Render(cfg.Automation.OptOutReply, vars)
		return Outcome{Reply: r, Rule: "system:opt_out"}
	case IsOptIn(text):
		r, _ := msgtemplate.Render(cfg.Automation.OptInReply, vars)
		return Outcome{Reply: r, Rule: "system:opt_in"}
	case IsHandoverRequest(text):
		r, _ := msgtemplate.Render(cfg.Automation.HandoverReply, vars)
		return Outcome{Reply: r, Rule: "system:handover", Handover: true}
	}
	// Like Process, an away reply that needs an empty detail is not sent at all.
	if cfg.BusinessHours.Enabled && !IsOpen(cfg.BusinessHours, now) {
		if r, missing := msgtemplate.Render(cfg.BusinessHours.AwayReply, vars); len(missing) == 0 && r != "" {
			return Outcome{Reply: r, Rule: "system:away", Skipped: "closed now: the first message of a visit gets the away reply"}
		}
	}
	// Same order as Process: a rule that needs an empty detail is skipped and the next
	// matching rule is tried; only when none can answer does the holding reply go out.
	rules := e.cfg.GetRules()
	var skipped, missingAll []string
	for len(rules) > 0 {
		rule, ok := FindRule(rules, text, menuActive)
		if !ok {
			break
		}
		r, missing := msgtemplate.Render(rule.Reply, vars)
		if len(missing) > 0 || r == "" {
			skipped = append(skipped, rule.Name)
			missingAll = append(missingAll, missing...)
			rules = without(rules, rule.Name)
			continue
		}
		return Outcome{Reply: r, Rule: rule.Name, Handover: rule.Action == "handover"}
	}
	if len(skipped) > 0 {
		hold, miss := msgtemplate.Render(cfg.Automation.MissingDetailReply, vars)
		if len(miss) > 0 || hold == "" {
			hold = config.DefaultMissingDetailReply
		}
		return Outcome{Reply: hold, Rule: "system:missing_detail", Skipped: "Rule \"" + strings.Join(skipped, "\", \"") + "\" matched, but " + strings.Join(dedupe(missingAll), ", ") + " is empty in Settings > Business details. The customer gets this holding reply and the chat appears under Needs attention."}
	}
	if cfg.Automation.EnableDefaultReply && strings.TrimSpace(cfg.Automation.DefaultReply) != "" {
		if r, missing := msgtemplate.Render(cfg.Automation.DefaultReply, vars); len(missing) == 0 && r != "" {
			return Outcome{Reply: r, Rule: "system:fallback"}
		}
	}
	return Outcome{Skipped: "no rule matched and the fallback reply is off"}
}
