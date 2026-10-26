package bot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/notify"
	"whatsapp-automation/pkg/securestore"
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

	// late marks a message that arrived after Stop: it goes through its chat's queue, so
	// it is handled after the ones before it, but is only recorded (STOP/START applied).
	late bool
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
	capFlagged map[string]time.Time
	lastPrune  time.Time // when the per-customer maps above were last cleared of old entries
	limitNoted time.Time
	queues     map[string]chan Incoming
	stopping   bool           // Stop was called: new messages are only recorded
	inflight   sync.WaitGroup // messages queued before Stop and not yet processed
	jnl        *journal       // received messages not yet processed, on disk (nil: off)
}

// UseJournal keeps received messages in path until they are processed, so none is lost
// when Pulse stops unexpectedly. It returns the messages a previous run received but did
// not process; pass them to Receive once sending is ready.
func (e *Engine) UseJournal(path string) []Incoming {
	j, pending := openJournal(path, securestore.Default())
	if e.hist != nil {
		// Entries leave the journal once their history is on disk, and the contacts too:
		// a STOP or START applied to a contact that was not saved yet must be replayed
		// after a crash, so a contacts save that fails keeps the entries.
		e.hist.OnSaved(func(snapshotAt time.Time) {
			if e.contacts != nil {
				if err := e.contacts.Flush(); err != nil {
					fmt.Printf("[Bot] The contacts could not be saved (%v); received messages stay listed until they are.\n", err)
					return
				}
			}
			j.compact(snapshotAt)
		})
	} else {
		j.immediate = true
	}
	e.jnl = j
	return pending
}

// NewEngine creates the engine. m, hooks and notifier may be nil.
func NewEngine(m Messenger, cfg *config.Config, cs *contacts.Store, hs *history.Store, hooks *webhook.Dispatcher, n notify.Notifier) *Engine {
	e := &Engine{msg: m, cfg: cfg, contacts: cs, hist: hs, hooks: hooks, notifier: n, now: time.Now}
	if cs != nil && hs != nil {
		// A contact Pulse 2.0 saved under a wrong number takes its messages along when it
		// moves to the number Meta sends (contacts.LegacyWAID).
		cs.OnRenamed(func(oldPhone, newPhone string) { hs.RenamePhone(oldPhone, newPhone) })
	}
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
	e.stopping = true
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
	e.menus, e.capFlagged = map[string]time.Time{}, map[string]time.Time{}
	if e.queues == nil {
		e.queues = map[string]chan Incoming{}
	}
	e.mu.Unlock()
}

// Receive queues a customer's message for the automation pipeline. Messages without a
// number or without any content are ignored.
func (e *Engine) Receive(in Incoming) {
	in.Phone = contacts.NormalizePhone(in.Phone) // Meta's number: no country code is guessed
	in.PushName, in.Text = strings.TrimSpace(in.PushName), strings.TrimSpace(in.Text)
	if in.Phone == "" || in.ID == "" || (in.Text == "" && in.Media == "") {
		return
	}
	if err := e.jnl.add(in); err != nil {
		// Meta still gets its answer (refusing would only make it repeat the delivery,
		// which hits the same disk problem); the message is processed right away and only
		// a crash before the next history save could lose it.
		fmt.Printf("[Bot] Message %s could not be saved before processing: %v\n", in.ID, err)
	}
	e.enqueue(in)
}

// enqueue processes messages from the same chat strictly in order, while different
// customers are handled in parallel.
func (e *Engine) enqueue(in Incoming) {
	key := in.Phone
	// The send happens under the lock so an idle worker cannot remove the queue in between.
	e.mu.Lock()
	// After Stop a message is still handled through its chat's queue, after the ones
	// before it (a STOP then START must apply in that order), but only recorded with
	// STOP/START applied, never answered. Stop does not wait for it.
	in.late = e.stopping
	q, ok := e.queues[key]
	if !ok {
		q = make(chan Incoming, 64)
		e.queues[key] = q
		go e.worker(key, q)
	}
	queued := true
	if !in.late {
		e.inflight.Add(1)
	}
	select {
	case q <- in:
	default:
		queued = false
		if !in.late {
			e.inflight.Done()
		}
	}
	e.mu.Unlock()
	if !queued {
		fmt.Printf("[Bot] Queue full for %s; message %s recorded without automation\n", key, in.ID)
		e.record(in, e.cfg.Snapshot())
		// An opt-out must never be lost, even when the chat is flooding.
		if IsOptOut(in.Text) && e.contacts != nil {
			now := e.now()
			// As in the normal path: a send time in the future (a wrong clock) is capped
			// at now, or the opt-out would outrank a later START.
			sentAt := now
			if !in.SentAt.IsZero() && in.SentAt.Before(now) {
				sentAt = in.SentAt
			}
			e.contacts.Upsert(in.Phone, in.PushName, "whatsapp") // the exact number, as Meta sent it
			e.contacts.SetDND(in.Phone, true, "Replied "+strings.ToUpper(Normalize(in.Text))+" on "+now.Format("2006-01-02"), sentAt)
		}
		e.jnl.done(in.ID)
	}
}

// handle processes one queued message and then takes it off the journal.
func (e *Engine) handle(in Incoming) {
	if !in.late {
		defer e.inflight.Done()
	}
	defer e.jnl.done(in.ID) // also after a recovered panic, so such a message is not retried at every start
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("[Bot] Recovered from error handling message %s: %v\n", in.ID, r)
		}
	}()
	e.process(in, in.late) // after Stop: record it and apply STOP/START, no reply
}

// worker handles one chat's queue until it has been idle for a while. It keeps running
// after Stop, so messages that arrive during shutdown keep their order.
func (e *Engine) worker(key string, q chan Incoming) {
	idle := time.NewTimer(5 * time.Minute)
	defer idle.Stop()
	for {
		select {
		case in := <-q:
			e.handle(in)
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
	in.Text, in.PushName = CleanDisplayText(in.Text, true), CleanDisplayText(in.PushName, false)
	cfg := e.cfg.Snapshot()
	a := cfg.Automation
	now := e.now()
	e.pruneState(now)

	// Meta can deliver a message more than once, and the client only remembers deliveries
	// since Pulse last started: a message already in the history was handled before, so
	// it must not be answered again (a restart, or a replayed delivery).
	if e.hist != nil {
		if _, done := e.hist.Get(in.ID); done {
			return Outcome{Skipped: "message was already handled"}
		}
		// The reply's record is saved before the reply is sent (see reply): a message
		// replayed after a crash that came after the reply went out is not answered again.
		since := time.Time{}
		if !in.SentAt.IsZero() {
			since = in.SentAt.Add(-time.Hour) // a reply is never older than the message (with clock slack)
		}
		if e.hist.HasLabel(ReplyLabel+in.ID, since) {
			return Outcome{Skipped: "message was already answered"}
		}
	}

	// The 24-hour window starts when the customer wrote, not when Pulse came back online
	// and received the message.
	sentAt := now
	if !in.SentAt.IsZero() && in.SentAt.Before(now) {
		sentAt = in.SentAt
	}
	// Free text may only answer within WhatsApp's 24-hour window; like every other sender,
	// Pulse stops half an hour early (contacts.FreeTextWindow), so a reply that is paced or
	// retried cannot reach Meta after the window closed.
	stale := now.Sub(sentAt) >= contacts.FreeTextWindow
	var contact contacts.Contact
	if e.contacts != nil {
		contact = e.contacts.Upsert(in.Phone, in.PushName, "whatsapp")
		e.contacts.RecordInbound(in.Phone, sentAt)
		if c, ok := e.contacts.GetExact(in.Phone); ok {
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
		// Pulse cannot read a voice note, a photo without a caption or a document, so a
		// person has to look; otherwise the customer would get silence.
		if e.needsPersonForMedia(in, contact, cfg, now, stale) {
			reason := "Sent a " + strings.ToLower(in.Media) + " that Pulse cannot read"
			e.contacts.FlagAttention(in.Phone, reason)
			security.LogAudit("MEDIA_NEEDS_PERSON", "+"+in.Phone, contact.Name, reason, "success")
			if e.notifier != nil {
				e.notifier.Notify("Chat needs attention", displayName(contact.Name, in.Phone)+": "+reason)
			}
			return Outcome{Skipped: "no text; chat flagged for a person"}
		}
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
	outsideTest := a.AllowListOnly && !e.contacts.AllowListed(a.AllowList, in.Phone)
	mayReply := a.Enabled && !blocked && !outsideTest && !stale && !quiet

	// Opt-out and opt-in are always recorded, even while automation is off or the chat is
	// paused; the confirmation is only sent when automatic replies may be sent, and only
	// when the status actually changed (repeating STOP/START must not trigger replies).
	if IsOptOut(in.Text) && !contact.IsDND && contact.OptInAt != nil && sentAt.Before(*contact.OptInAt) {
		// Meta does not guarantee the order of deliveries: a STOP written before the
		// customer's newer START must not opt them out again.
		security.LogAudit("OPT_OUT_IGNORED", "+"+in.Phone, name, "An older \""+in.Text+"\" arrived after the customer opted in again; the opt-in stays", "warning")
		return Outcome{Rule: "system:opt_out", Skipped: "older than the customer's opt-in; still opted in"}
	}
	if IsOptOut(in.Text) {
		if e.contacts != nil {
			e.contacts.SetDND(in.Phone, true, "Replied "+strings.ToUpper(Normalize(in.Text))+" on "+now.Format("2006-01-02"), sentAt)
		}
		security.LogAudit("OPT_OUT", "+"+in.Phone, name, "Customer opted out with \""+in.Text+"\"", "success")
		e.hooks.Send("contact.opted_out", map[string]any{"phone": "+" + in.Phone, "name": name})
		if !mayReply || contact.IsDND {
			return Outcome{Rule: "system:opt_out", Skipped: "opt-out recorded; no confirmation sent"}
		}
		if e.optConfirmsCapped(in.Phone, now) {
			return Outcome{Rule: "system:opt_out", Skipped: "opt-out recorded; confirmation limit reached"}
		}
		reply := systemReply(render, a.OptOutReply, defaults.OptOutReply)
		return e.reply(in, cfg, reply, "system:opt_out", true)
	}
	if IsOptIn(in.Text) && contact.IsDND && contact.ConsentAt != nil && sentAt.Before(*contact.ConsentAt) {
		// Meta does not guarantee the order of deliveries: a START written before the
		// customer's STOP (or delivered again later) must not subscribe them again.
		security.LogAudit("OPT_IN_IGNORED", "+"+in.Phone, name, "An older \""+in.Text+"\" arrived after the customer opted out; the opt-out stays", "warning")
		return Outcome{Rule: "system:opt_in", Skipped: "older than the customer's opt-out; still opted out"}
	}
	// "start", "resume" or "chalu" alone only re-subscribes a customer who opted out;
	// from anyone else it is an ordinary message ("resume" my order) and records no
	// consent, which needs a clear opt-in.
	if IsOptIn(in.Text) && contact.IsDND {
		if e.contacts != nil {
			// The time the customer wrote it, so an older STOP arriving later cannot undo it.
			e.contacts.SetConsent(in.Phone, true, "Replied "+strings.ToUpper(Normalize(in.Text))+" on "+now.Format("2006-01-02"), sentAt)
		}
		security.LogAudit("OPT_IN", "+"+in.Phone, name, "Customer opted in with \""+in.Text+"\"", "success")
		e.hooks.Send("contact.opted_in", map[string]any{"phone": "+" + in.Phone, "name": name})
		if !mayReply {
			return Outcome{Rule: "system:opt_in", Skipped: "opt-in recorded; no confirmation sent"}
		}
		if e.optConfirmsCapped(in.Phone, now) {
			return Outcome{Rule: "system:opt_in", Skipped: "opt-in recorded; confirmation limit reached"}
		}
		reply := systemReply(render, a.OptInReply, defaults.OptInReply)
		return e.reply(in, cfg, reply, "system:opt_in", true)
	}

	switch {
	case quiet:
		return Outcome{Skipped: "replies are off"}
	case !a.Enabled:
		return Outcome{Skipped: "automation is switched off"}
	case stale:
		return Outcome{Skipped: "message is more than 23 hours 30 minutes old (WhatsApp's 24-hour window)"}
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
		reply := systemReply(render, a.HandoverReply, defaults.HandoverReply)
		out := e.reply(in, cfg, reply, "system:handover", true)
		out.Handover = true
		return out
	}

	if limited != "" {
		return Outcome{Skipped: limited}
	}

	if cfg.BusinessHours.Enabled && !IsOpen(cfg.BusinessHours, now) && !e.repliedRecently(in.Phone, now.Add(-awayRepeatAfter), "system:away") {
		reply, missing := render(cfg.BusinessHours.AwayReply)
		if len(missing) == 0 && reply != "" {
			return e.reply(in, cfg, reply, "system:away", false)
		}
	}

	menuActive := e.menuActive(in.Phone, now)

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
		if e.repliedRecently(in.Phone, now.Add(-fallbackRepeat), "system:fallback") {
			return Outcome{Skipped: "fallback reply already sent recently"}
		}
		reply, missing := render(a.DefaultReply)
		if len(missing) == 0 && reply != "" {
			return e.reply(in, cfg, reply, "system:fallback", false)
		}
	}
	return Outcome{Skipped: "no rule matched"}
}

// failedRetryAfter is how long a reply that could not be sent holds back the next try,
// so failing sends (an expired token, Meta's rate limit) are not repeated for every
// message, while a short outage does not suppress the reply for the whole window.
const failedRetryAfter = 15 * time.Minute

// repliedRecently reports whether one of rules answered phone since t. It reads the
// saved history rather than memory, so a restart does not reset the away and fallback
// limits. A reply that failed to send counts only for failedRetryAfter.
func (e *Engine) repliedRecently(phone string, since time.Time, rules ...string) bool {
	if e.hist == nil {
		return false
	}
	if e.hist.RuleRepliesTo(phone, since, rules...) > 0 {
		return true
	}
	failedSince := e.now().Add(-failedRetryAfter)
	if failedSince.Before(since) {
		failedSince = since
	}
	return e.hist.RuleFailuresTo(phone, failedSince, rules...) > 0
}

// menuActive reports whether a menu was shown to phone recently, so number choices
// ("1", "2") match. After a restart the last automatic reply in the history tells.
func (e *Engine) menuActive(phone string, now time.Time) bool {
	e.mu.Lock()
	at, ok := e.menus[phone]
	e.mu.Unlock()
	if ok {
		return now.Sub(at) < menuSessionTTL
	}
	if e.hist != nil {
		if last, found := e.hist.LastAutoReply(phone, now.Add(-menuSessionTTL)); found && now.Sub(last.Timestamp) < menuSessionTTL && HasMenu(last.Text) {
			return true
		}
	}
	return false
}

// pruneState drops per-customer entries that no longer matter (older than the window
// each one is used for), at most every 10 minutes, so the maps do not grow with every
// customer who ever wrote while Pulse keeps running for months.
func (e *Engine) pruneState(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if now.Sub(e.lastPrune) < 10*time.Minute {
		return
	}
	e.lastPrune = now
	for _, m := range []struct {
		entries map[string]time.Time
		keep    time.Duration
	}{{e.menus, menuSessionTTL}, {e.capFlagged, time.Hour}} {
		for phone, at := range m.entries {
			if now.Sub(at) > m.keep {
				delete(m.entries, phone)
			}
		}
	}
}

// optConfirmPerHour caps STOP/START confirmations per customer. They skip the cooldown
// and daily limit so an opt-out is always confirmed, but a customer (or another bot)
// alternating STOP and START must not keep Pulse replying. The opt-out itself is always
// recorded; only the confirmation is held back.
const optConfirmPerHour = 3

func (e *Engine) optConfirmsCapped(phone string, now time.Time) bool {
	return e.hist != nil && e.hist.RuleRepliesTo(phone, now.Add(-time.Hour), "system:opt_out", "system:opt_in") >= optConfirmPerHour
}

// needsPersonForMedia reports whether a message without text should go on the Needs
// attention list: only while automatic replies run (otherwise staff read every chat),
// never for stickers, opted-out, blocked or test-excluded numbers, or a chat a person
// already handles.
func (e *Engine) needsPersonForMedia(in Incoming, ct contacts.Contact, cfg config.Config, now time.Time, stale bool) bool {
	a := cfg.Automation
	switch {
	case e.contacts == nil, !a.Enabled, stale, in.Media == "", in.Media == "Sticker":
		return false
	case ct.IsDND, contains(a.BlacklistPhones, in.Phone), a.AllowListOnly && !e.contacts.AllowListed(a.AllowList, in.Phone):
		return false
	case ct.BotPaused(now), ct.NeedsAttention:
		return false
	}
	return true
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
		if contacts.MatchesTyped(p, phone) {
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
	if last, ok := e.hist.LastAutoReply(in.Phone, now.Add(-time.Duration(a.CooldownSeconds)*time.Second)); ok && a.CooldownSeconds > 0 && now.Sub(last.Timestamp) < time.Duration(a.CooldownSeconds)*time.Second {
		return "cooldown: replied less than " + fmt.Sprint(a.CooldownSeconds) + " seconds ago"
	}
	if n := e.hist.AutoRepliesTo(in.Phone, now.Add(-time.Hour)); a.MaxRepliesPerContactHr > 0 && n >= a.MaxRepliesPerContactHr {
		e.flagOnce(in.Phone, name, fmt.Sprintf("Reached %d automatic replies in one hour", n), cfg)
		return "per-contact hourly reply cap reached"
	}
	// Three identical messages within the window, after two automatic replies, look like
	// another bot answering Pulse (D37); a person rarely sends the same text three times.
	recent := e.hist.RecentIncoming(in.Phone, 3, now.Add(-loopWindow))
	if len(recent) == 3 && now.Sub(recent[2].Timestamp) < loopWindow {
		same := true
		for _, m := range recent[1:] {
			if Normalize(m.Text) != Normalize(recent[0].Text) {
				same = false
				break
			}
		}
		if same && e.hist.AutoRepliesTo(in.Phone, now.Add(-loopWindow)) >= 2 {
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
	if limit > 0 && e.hist.SentSince(campaign.DailyWindowStart(now)) >= limit {
		e.mu.Lock()
		note := now.Sub(e.limitNoted) > time.Hour
		if note {
			e.limitNoted = now
		}
		e.mu.Unlock()
		if note {
			security.LogAudit("DAILY_LIMIT_REACHED", "local", "system", fmt.Sprintf("Daily sending limit of %d reached in the last 24 hours; automatic replies paused until earlier messages are more than 24 hours old", limit), "warning")
		}
		return "daily sending limit reached"
	}
	return ""
}

func (e *Engine) flagOnce(phone, name, reason string, cfg config.Config) {
	e.mu.Lock()
	last, now := e.capFlagged[phone], e.now()
	if now.Sub(last) < time.Hour {
		e.mu.Unlock()
		return
	}
	e.capFlagged[phone] = now
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
	// WhatsApp only shows "typing..." together with a read receipt, so with "mark as
	// read" off there is no typing indicator either: the customer sees no blue ticks.
	if a.AutoRead {
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
		} else {
			_ = e.msg.MarkAsRead(ctx, in.ID)
		}
	}
	// Labelled like campaign and scheduled messages: a reply whose answer was lost (a
	// timeout) is recorded as failed and corrected when Meta reports it (ResolveLabel).
	rec.Label = ReplyLabel + in.ID
	rec.Timestamp = e.now()
	placeholder := ""
	if e.hist != nil {
		// The reply is recorded ("sending", with its label) before it goes out, and with
		// the journal on it is saved to disk first: if Pulse stops after the reply left
		// but before the next save, the replayed message is recognised as answered
		// (process) instead of being answered twice.
		placeholder = e.hist.Placeholder(rec)
		if e.jnl != nil {
			if ferr := e.hist.Flush(); ferr != nil {
				fmt.Printf("[Bot] The message history could not be saved before replying: %v\n", ferr)
			}
		}
	}
	id, err := e.msg.SendReply(cloudapi.WithCallbackData(ctx, rec.Label), in.Phone, text, in.ID)
	if err != nil {
		out.SendErr = err.Error()
	}
	if e.hist != nil {
		if err != nil {
			// May have gone out (a timeout): counts towards the limit, corrected by its label.
			e.hist.Settle(placeholder, "fail_"+security.RandomToken(6), history.StatusFailed, err.Error(), cloudapi.MaybeSent(err))
		} else {
			e.hist.Settle(placeholder, id, history.StatusSent, "", false)
		}
	}
	if e.contacts != nil && err == nil {
		e.contacts.UpdateLastContacted(in.Phone)
	}
	return out
}

// ReplyLabel prefixes the label every automatic reply is sent with
// (biz_opaque_callback_data): ReplyLabel + the customer's message ID.
const ReplyLabel = "pulse:r:"

// defaults holds the built-in confirmation texts; they only use variables that always
// have a value ({{first_name}} falls back to "there").
var defaults = config.DefaultConfig().Automation

// systemReply renders an opt-out, opt-in or hand-over confirmation. These are always
// sent, so when the owner's text needs a detail that is empty, the built-in text goes
// out instead of a reply with a gap in it.
func systemReply(render func(string) (string, []string), text, builtIn string) string {
	if r, missing := render(text); len(missing) == 0 {
		return r
	}
	r, _ := render(builtIn)
	return r
}

// Simulate shows which reply a message would get, without sending anything.
func (e *Engine) Simulate(text, name string, menuActive bool) Outcome {
	cfg := e.cfg.Snapshot()
	now := e.now()
	vars := msgtemplate.Vars{Name: name, Phone: "919800000000", Message: text, Business: cfg.Business, Now: now, Location: Location(cfg.BusinessHours.Timezone)}
	render := func(t string) (string, []string) { return msgtemplate.Render(t, vars) }
	switch {
	case IsOptOut(text):
		return Outcome{Reply: systemReply(render, cfg.Automation.OptOutReply, defaults.OptOutReply), Rule: "system:opt_out"}
	case IsOptIn(text):
		return Outcome{Reply: systemReply(render, cfg.Automation.OptInReply, defaults.OptInReply), Rule: "system:opt_in"}
	case IsHandoverRequest(text):
		return Outcome{Reply: systemReply(render, cfg.Automation.HandoverReply, defaults.HandoverReply), Rule: "system:handover", Handover: true}
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
