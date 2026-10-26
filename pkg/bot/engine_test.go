package bot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
)

type fakeMessenger struct {
	mu     sync.Mutex
	sent   []string
	quoted []string
	fail   bool
	reads  []string
	typing []string
}

func (f *fakeMessenger) SendReply(ctx context.Context, phone, text, quotedID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return "", errors.New("offline")
	}
	f.sent = append(f.sent, text)
	f.quoted = append(f.quoted, phone+">"+quotedID)
	return "out" + string(rune('A'+len(f.sent))), nil
}
func (f *fakeMessenger) MarkAsRead(ctx context.Context, id string) error {
	f.mu.Lock()
	f.reads = append(f.reads, id)
	f.mu.Unlock()
	return nil
}
func (f *fakeMessenger) ShowTyping(ctx context.Context, phone string) error {
	f.mu.Lock()
	f.typing = append(f.typing, phone)
	f.mu.Unlock()
	return nil
}

func (f *fakeMessenger) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

type fakeNotifier struct{ n int }

func (f *fakeNotifier) Notify(title, body string) { f.n++ }

type rig struct {
	e   *Engine
	m   *fakeMessenger
	cfg *config.Config
	cs  *contacts.Store
	hs  *history.Store
	n   *fakeNotifier
	now time.Time
	seq int
}

func newRig(t *testing.T) *rig {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	_ = cfg.Update(func(c *config.Config) error {
		c.Automation.Enabled = true
		c.Automation.TypingSimulation = false
		c.Automation.CooldownSeconds = 0
		c.Automation.MaxRepliesPerContactHr = 50
		c.Automation.WarmupMode = false
		c.Business = config.BusinessConfig{Name: "Verma Dental", Hours: "Mon-Sat 9-6", Address: "12 MG Road", Services: "Cleaning and fillings", Prices: ""}
		return nil
	})
	cs, _ := contacts.NewStore(filepath.Join(dir, "c.json"))
	hs, _ := history.NewStore(filepath.Join(dir, "h.json"), 1000)
	r := &rig{m: &fakeMessenger{}, cfg: cfg, cs: cs, hs: hs, n: &fakeNotifier{}, now: time.Date(2026, 10, 21, 11, 0, 0, 0, time.Local)}
	r.e = NewEngine(nil, cfg, cs, hs, nil, r.n)
	r.e.SetMessenger(r.m)
	r.e.SetClock(func() time.Time { r.now = r.now.Add(time.Second); return r.now })
	return r
}

func (r *rig) say(phone, name, text string) Outcome {
	r.seq++
	return r.e.Process(Incoming{ID: "in" + string(rune('a'+r.seq)), Phone: phone, PushName: name, Text: text})
}

const asha = "919876543210"

func TestAutomationOffStillRecords(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error { c.Automation.Enabled = false; return nil })
	out := r.say(asha, "Asha", "hi")
	if r.m.count() != 0 || out.Skipped == "" {
		t.Fatal("no reply expected when automation is off")
	}
	if len(r.hs.List(10, asha, "")) != 1 {
		t.Fatal("incoming message must still be stored")
	}
	if c, _ := r.cs.Get(asha); c.Name != "Asha" || c.LastInboundAt == nil {
		t.Fatal("contact not recorded")
	}
}

func TestOptOutRecordedWhileAutomationOff(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error { c.Automation.Enabled = false; return nil })
	out := r.say(asha, "Asha", "STOP")
	if c, _ := r.cs.Get(asha); !c.IsDND {
		t.Fatal("STOP must be recorded even while automatic replies are off")
	}
	if r.m.count() != 0 || out.Rule != "system:opt_out" {
		t.Fatalf("no confirmation should be sent while automation is off: %+v", out)
	}
	if out := r.say("14155550100", "Grp", "STOP"); out.Rule != "system:opt_out" {
		t.Fatal("second opt-out not handled")
	}
}

func TestMenuNumbersNeedAnActiveMenu(t *testing.T) {
	r := newRig(t)
	if out := r.say(asha, "Asha", "1"); out.Rule != "system:fallback" {
		t.Fatalf("a bare number without a menu should not trigger a menu rule (C8), got %+v", out)
	}
	out := r.say(asha, "Asha", "hello")
	if out.Rule != "greeting_menu" || !strings.Contains(out.Reply, "Verma Dental") || !strings.Contains(out.Reply, "Asha") {
		t.Fatalf("greeting wrong: %+v", out)
	}
	if out := r.say(asha, "Asha", "1"); out.Rule != "option_services" || !strings.Contains(out.Reply, "Cleaning") {
		t.Fatalf("menu option 1 wrong: %+v", out)
	}
}

func TestEmptyBusinessDetailGetsHoldingReply(t *testing.T) {
	r := newRig(t)
	out := r.say(asha, "Asha", "prices")
	// The prices rule is not sent with a gap (C2/C23), but the customer is not left
	// in silence either (C27): a holding reply goes out and staff are alerted.
	if out.Rule != "system:missing_detail" || strings.Contains(out.Reply, "{{") || r.m.count() != 1 {
		t.Fatalf("expected the holding reply: %+v", out)
	}
	if c, _ := r.cs.Get(asha); !c.NeedsAttention || !strings.Contains(c.AttentionReason, "business_prices") {
		t.Fatalf("chat must be flagged for staff: %+v", c)
	}
	if r.n.n == 0 {
		t.Fatal("the owner must get an alert")
	}
}

func TestStopAndStart(t *testing.T) {
	r := newRig(t)
	if out := r.say(asha, "Asha", "STOP"); out.Rule != "system:opt_out" {
		t.Fatalf("stop not handled: %+v", out)
	}
	if c, _ := r.cs.Get(asha); !c.IsDND {
		t.Fatal("stop must set do-not-message")
	}
	if out := r.say(asha, "Asha", "hi"); out.Reply != "" {
		t.Fatal("no replies after opting out")
	}
	if out := r.say(asha, "Asha", "band karo"); out.Rule != "system:opt_out" {
		t.Fatal("Hinglish opt-out not recognised (C18)")
	}
	if out := r.say(asha, "Asha", "start"); out.Rule != "system:opt_in" {
		t.Fatal("start not handled")
	}
	if c, _ := r.cs.Get(asha); c.IsDND || !c.Consent {
		t.Fatal("start must record consent")
	}
}

func TestHandoverPausesAndAlerts(t *testing.T) {
	r := newRig(t)
	out := r.say(asha, "Asha", "can I talk to a person please")
	if !out.Handover || r.n.n != 1 {
		t.Fatalf("handover not detected or no alert (C4): %+v", out)
	}
	if !r.cs.IsBotPaused(asha) || len(r.cs.NeedsAttention()) != 1 {
		t.Fatal("chat must be paused and flagged")
	}
	if out := r.say(asha, "Asha", "hello?"); out.Reply != "" {
		t.Fatal("bot must stay silent during handover")
	}
	r.cs.ResumeBot(asha)
	r.say(asha, "Asha", "hi")
	if out := r.say(asha, "Asha", "4"); !out.Handover {
		t.Fatal("menu option 4 should hand over")
	}
}

func TestIncomingStoredBeforeReply(t *testing.T) {
	r := newRig(t)
	r.say(asha, "Asha", "hi")
	all := r.hs.ExportAll()
	if len(all) != 2 || all[0].Direction != history.DirectionIncoming || !all[0].Timestamp.Before(all[1].Timestamp) {
		t.Fatalf("incoming must be stored before the reply (D23): %+v", all)
	}
}

func TestMaskedNameGreetsThere(t *testing.T) {
	r := newRig(t)
	out := r.say("919811112237", "+91∙∙∙∙∙∙∙∙37", "hi")
	// No masked digits, and no stiff "Good morning there,": the name is simply left out.
	if !strings.HasPrefix(out.Reply, "Good morning, thank you") || strings.Contains(out.Reply, "∙") {
		t.Fatalf("masked number used as a name (C22): %q", out.Reply)
	}
}

func TestSafetyLimits(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error { c.Automation.MaxRepliesPerContactHr = 2; return nil })
	r.say(asha, "Asha", "hi")
	r.say(asha, "Asha", "thanks")
	if out := r.say(asha, "Asha", "bye"); out.Reply != "" || !r.cs.IsBotPaused(asha) {
		t.Fatalf("per-contact cap not enforced: %+v", out)
	}

	r2 := newRig(t)
	_ = r2.cfg.Update(func(c *config.Config) error {
		c.Automation.AllowListOnly, c.Automation.AllowList = true, []string{"+14155550100"}
		return nil
	})
	if out := r2.say(asha, "Asha", "hi"); out.Reply != "" {
		t.Fatal("test mode must ignore numbers outside the allow-list")
	}
	if out := r2.say("14155550100", "Me", "hi"); out.Reply == "" {
		t.Fatal("allow-listed number should get replies")
	}

	r3 := newRig(t)
	_ = r3.cfg.Update(func(c *config.Config) error { c.Automation.DryRun = true; return nil })
	if out := r3.say(asha, "Asha", "hi"); !out.DryRun || r3.m.count() != 0 {
		t.Fatal("dry run must not send")
	}
	if all := r3.hs.ExportAll(); all[len(all)-1].Status != history.StatusDryRun {
		t.Fatal("dry run reply must be recorded as such")
	}

	r4 := newRig(t)
	_ = r4.cfg.Update(func(c *config.Config) error { c.Automation.DailyLimit = 1; return nil })
	r4.say(asha, "Asha", "hi")
	if out := r4.say("14155550101", "Bob", "hi"); out.Reply != "" {
		t.Fatal("daily limit must stop automatic replies")
	}
}

func TestLoopDetection(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 5; i++ {
		r.say(asha, "Asha", "hi")
	}
	if !r.cs.IsBotPaused(asha) {
		t.Fatal("identical repeated messages with replies should pause the chat (P10/FT12)")
	}
}

func TestAwayReplyOncePerVisit(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error {
		c.BusinessHours.Enabled = true
		c.BusinessHours.Timezone = "Local"
		for i := range c.BusinessHours.Days {
			c.BusinessHours.Days[i].Open = false
		}
		return nil
	})
	if out := r.say(asha, "Asha", "hi"); out.Rule != "system:away" || !strings.Contains(out.Reply, "Mon-Sat") {
		t.Fatalf("away reply expected: %+v", out)
	}
	if out := r.say(asha, "Asha", "hi"); out.Rule != "greeting_menu" {
		t.Fatalf("after the away reply, rules should still answer: %+v", out)
	}
	// A restart (a new engine on the same saved history) must not send it again, and the
	// menu shown just before the restart still accepts a number choice.
	r.e = NewEngine(nil, r.cfg, r.cs, r.hs, nil, r.n)
	r.e.SetMessenger(r.m)
	r.e.SetClock(func() time.Time { r.now = r.now.Add(time.Second); return r.now })
	if out := r.say(asha, "Asha", "1"); out.Rule == "system:away" || out.Rule == "" {
		t.Fatalf("after a restart the away reply must not repeat and the menu must still work: %+v", out)
	}
}

// While sending fails, the fallback reply is not retried for every message, only after
// a pause.
func TestFailedFallbackIsNotRetriedEveryMessage(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error {
		c.Automation.EnableDefaultReply, c.Automation.DefaultReply = true, "Sorry, I did not understand. Reply MENU."
		return nil
	})
	r.m.fail = true
	if out := r.say(asha, "Asha", "qwerty zxcv"); out.Rule != "system:fallback" || out.SendErr == "" {
		t.Fatalf("the first try must be made and fail: %+v", out)
	}
	if out := r.say(asha, "Asha", "asdf ghjk"); out.Rule == "system:fallback" {
		t.Fatalf("a failing fallback must not be retried at once: %+v", out)
	}
	r.now = r.now.Add(failedRetryAfter + time.Minute)
	r.m.fail = false
	if out := r.say(asha, "Asha", "zzzz yyyy"); out.Rule != "system:fallback" || out.SendErr != "" {
		t.Fatalf("after the pause it is tried again: %+v", out)
	}
}

// The fallback limit (once per 30 minutes) survives a restart too.
func TestFallbackLimitSurvivesRestart(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error {
		c.Automation.EnableDefaultReply, c.Automation.DefaultReply = true, "Sorry, I did not understand. Reply MENU."
		return nil
	})
	if out := r.say(asha, "Asha", "qwerty zxcv"); out.Rule != "system:fallback" {
		t.Fatalf("fallback expected: %+v", out)
	}
	r.e = NewEngine(nil, r.cfg, r.cs, r.hs, nil, r.n)
	r.e.SetMessenger(r.m)
	r.e.SetClock(func() time.Time { r.now = r.now.Add(time.Second); return r.now })
	if out := r.say(asha, "Asha", "asdf ghjk"); out.Rule == "system:fallback" {
		t.Fatalf("a restart must not reset the fallback limit: %+v", out)
	}
}

func TestSendFailureIsRecorded(t *testing.T) {
	r := newRig(t)
	r.m.fail = true
	if out := r.say(asha, "Asha", "hi"); out.SendErr == "" {
		t.Fatal("send error not reported")
	}
	all := r.hs.ExportAll()
	if all[len(all)-1].Status != history.StatusFailed {
		t.Fatal("failed replies must be recorded as failed")
	}
}

func TestIsOpen(t *testing.T) {
	bh := config.DefaultConfig().BusinessHours
	bh.Enabled = true
	bh.Timezone = "UTC"
	mon := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC) // Monday
	if !IsOpen(bh, mon) || IsOpen(bh, mon.Add(9*time.Hour)) {
		t.Fatal("weekday schedule wrong")
	}
	if IsOpen(bh, time.Date(2026, 10, 25, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("closed on Sunday by default")
	}
	bh.Holidays = []string{"2026-10-19"}
	if IsOpen(bh, mon) {
		t.Fatal("holidays must be closed")
	}
	bh.Holidays = nil
	bh.Days[4] = config.DaySchedule{Day: "fri", Open: true, Start: "20:00", End: "02:00"}
	if !IsOpen(bh, time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)) {
		t.Fatal("overnight window should run past midnight into Saturday")
	}
	bh.Holidays = []string{"2026-10-23"}
	if IsOpen(bh, time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)) {
		t.Fatal("a late shift that started on a holiday must stay closed after midnight")
	}
}

func TestContainsMatchesWholeWords(t *testing.T) {
	r := config.Rule{Name: "greet", Type: "contains", Pattern: "hi, price", Enabled: true}
	for _, yes := range []string{"hi there", "ok hi", "what is the price?", "Hi!"} {
		if !MatchRule(r, yes, false) {
			t.Errorf("%q should match", yes)
		}
	}
	for _, no := range []string{"is this shipped?", "which plan", "surprice"} {
		if MatchRule(r, no, false) {
			t.Errorf("%q must not match part of a word", no)
		}
	}
}

func TestRuleConflictsAndMatching(t *testing.T) {
	rules := []config.Rule{
		{Name: "a", Type: "exact", Pattern: "price, hi", Enabled: true},
		{Name: "b", Type: "exact", Pattern: "Price", Enabled: true},
	}
	if c := RuleConflicts(rules); len(c) != 1 || !strings.Contains(c[0], "price") {
		t.Fatalf("conflict not found: %v", c)
	}
	if !MatchRule(rules[0], "Hi there!", false) || MatchRule(rules[0], "hiring", false) {
		t.Fatal("exact match should accept the first word only")
	}
	if !HasMenu("Pick:\n1. A\n2. B") || HasMenu("Call 1. now") {
		t.Fatal("menu detection wrong")
	}
	if !IsHandoverRequest("mujhe kisi se baat karni hai") || IsHandoverRequest("thank you") {
		t.Fatal("handover detection wrong")
	}
}

// A hand-over request obeys the daily limit like any other reply, but the chat is
// still handed over so staff see it (F19).
func TestHandoverReplyRespectsDailyLimit(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error { c.Automation.DailyLimit = 1; return nil })
	r.say("14155550101", "Bob", "hi") // uses up today's only message
	sent := r.m.count()
	out := r.say(asha, "Asha", "can I talk to a person please")
	if !out.Handover || !strings.Contains(out.Skipped, "daily") || r.m.count() != sent {
		t.Fatalf("hand-over reply must be held back at the daily limit: %+v", out)
	}
	if !r.cs.IsBotPaused(asha) || len(r.cs.NeedsAttention()) != 1 || r.n.n != 1 {
		t.Fatal("the chat must still be handed over and flagged")
	}
}

// Stop waits for queued messages, and messages arriving afterwards are still recorded
// with their opt-outs applied, but not answered (F16).
func TestStopDrainsQueuedMessages(t *testing.T) {
	r := newRig(t)
	in := func(phone, text string) Incoming {
		r.seq++
		return Incoming{ID: "q" + string(rune('a'+r.seq)), Phone: phone, Text: text}
	}
	r.e.enqueue(in(asha, "hi"))
	r.e.enqueue(in(asha, "thanks"))
	r.e.enqueue(in("14155550101", "hello"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !r.e.Stop(ctx) {
		t.Fatal("queued messages were not processed before the deadline")
	}
	incoming := 0
	for _, m := range r.hs.ExportAll() {
		if m.Direction == history.DirectionIncoming {
			incoming++
		}
	}
	if incoming != 3 {
		t.Fatalf("all queued messages must be stored before shutdown, got %d", incoming)
	}
	sent := r.m.count()
	// Handled through the chat's queue (C17), after that chat's earlier messages.
	r.e.enqueue(in(asha, "STOP"))
	r.e.enqueue(in(asha, "START"))
	r.e.enqueue(in("14155550102", "STOP"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		c, ok := r.cs.Get("14155550102")
		a, _ := r.cs.Get(asha)
		if ok && c.IsDND && a != nil && a.Consent && !a.IsDND {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("opt-outs arriving during shutdown must still be applied, in order")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if r.m.count() != sent {
		t.Fatal("no replies may be sent after Stop")
	}
}

// The rule tester shows what Process would really do: an away or fallback reply that
// needs an empty business detail is not sent, so it is not shown either.
func TestSimulateSkipsRepliesWithEmptyDetails(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error {
		c.BusinessHours.Enabled = true
		c.BusinessHours.Timezone = "Local"
		c.BusinessHours.AwayReply = "We are closed. Prices: {{prices}}"
		for i := range c.BusinessHours.Days {
			c.BusinessHours.Days[i].Open = false
		}
		c.Automation.EnableDefaultReply = true
		c.Automation.DefaultReply = "Our prices: {{prices}}"
		return nil
	})
	out := r.e.Simulate("zzqx unrelated words", "Asha", false)
	if out.Rule == "system:away" || out.Rule == "system:fallback" || strings.Contains(out.Reply, "{{") {
		t.Fatalf("replies with empty details must not be shown: %+v", out)
	}
	if real := r.say(asha, "Asha", "zzqx unrelated words"); real.Reply != out.Reply {
		t.Fatalf("Simulate (%q) and Process (%q) disagree", out.Reply, real.Reply)
	}
}

// Replies quote the customer's message; read receipts and "typing..." use the
// customer's message and number. Messages without a number or content are ignored.
func TestReplyQuotesReadsAndTyping(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error {
		c.Automation.TypingSimulation = true
		c.Automation.TypingDurationMs = 1
		c.Automation.AutoRead = true
		return nil
	})
	out := r.e.Process(Incoming{ID: "wamid.1", Phone: asha, PushName: "Asha", Text: "hi"})
	if out.Rule != "greeting_menu" || len(r.m.quoted) != 1 || r.m.quoted[0] != asha+">wamid.1" {
		t.Fatalf("reply must go to the customer quoting their message: %+v %v", out, r.m.quoted)
	}
	if len(r.m.typing) != 1 || r.m.typing[0] != asha {
		t.Fatalf("typing indicator expected: %v", r.m.typing)
	}
	_ = r.cfg.Update(func(c *config.Config) error { c.Automation.TypingSimulation = false; return nil })
	r.e.Process(Incoming{ID: "wamid.2", Phone: asha, Text: "thanks"})
	if len(r.m.reads) != 1 || r.m.reads[0] != "wamid.2" {
		t.Fatalf("read receipt expected: %v", r.m.reads)
	}
	before := len(r.hs.ExportAll())
	r.e.Receive(Incoming{ID: "x", Phone: "", Text: "hi"})
	r.e.Receive(Incoming{ID: "y", Phone: asha})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r.e.Stop(ctx)
	if len(r.hs.ExportAll()) != before {
		t.Fatal("messages without a number or content must be ignored")
	}
}

// Meta can deliver a message twice; after a restart the client no longer remembers it.
func TestRedeliveredMessageIsNotAnsweredTwice(t *testing.T) {
	r := newRig(t)
	in := Incoming{ID: "wamid.SAME", Phone: asha, PushName: "Asha", Text: "hi"}
	r.e.Process(in)
	if out := r.e.Process(in); out.Skipped == "" || r.m.count() != 1 {
		t.Fatalf("a message already in the history must not be answered again: %d replies, %+v", r.m.count(), out)
	}
}

// STOP/START skip the reply limits, so their confirmations have their own cap.
func TestOptOutOptInConfirmationsAreCapped(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 20; i++ {
		text := "STOP"
		if i%2 == 1 {
			text = "START"
		}
		r.say(asha, "Asha", text)
	}
	if n := r.m.count(); n != optConfirmPerHour {
		t.Fatalf("want %d confirmations per hour, got %d", optConfirmPerHour, n)
	}
	if c, _ := r.cs.Get(asha); !c.Consent || c.IsDND {
		t.Fatal("every STOP and START must still be recorded; the last one was START")
	}
	r.say(asha, "Asha", "STOP")
	if c, _ := r.cs.Get(asha); !c.IsDND {
		t.Fatal("an opt-out past the confirmation cap must still be recorded")
	}
}

// An opt-out confirmation the owner edited to use an empty detail must not go out with a
// gap: the built-in text is sent instead.
func TestOptOutConfirmationNeverHasGaps(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error {
		c.Business.Name = ""
		c.Automation.OptOutReply = "You will hear nothing more from {{business_name}}."
		return nil
	})
	r.say(asha, "Asha", "STOP")
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	if len(r.m.sent) != 1 || r.m.sent[0] != config.DefaultConfig().Automation.OptOutReply {
		t.Fatalf("want the built-in opt-out text, got %q", r.m.sent)
	}
}

// Meta may deliver STOP and START out of order: a START written before the STOP must not
// subscribe the customer again, but a START written after it does.
func TestOlderStartDoesNotUndoStop(t *testing.T) {
	r := newRig(t)
	now := r.e.now()
	r.e.Process(Incoming{ID: "wamid.stop", Phone: asha, PushName: "Asha", Text: "STOP", SentAt: now.Add(-10 * time.Minute)})
	r.e.Process(Incoming{ID: "wamid.oldstart", Phone: asha, PushName: "Asha", Text: "START", SentAt: now.Add(-20 * time.Minute)})
	if c, _ := r.cs.Get(asha); !c.IsDND || c.Consent {
		t.Fatalf("a START older than the STOP must not subscribe again: %+v", c)
	}
	r.e.Process(Incoming{ID: "wamid.newstart", Phone: asha, PushName: "Asha", Text: "START", SentAt: now.Add(-time.Minute)})
	if c, _ := r.cs.Get(asha); c.IsDND || !c.Consent {
		t.Fatalf("a START written after the STOP must subscribe again: %+v", c)
	}
}

// C2: "start" or "resume" from a customer who never opted out is an ordinary message:
// no consent is recorded and the normal rules answer it.
func TestStartWithoutOptOutIsNotConsent(t *testing.T) {
	r := newRig(t)
	out := r.say(asha, "Asha", "resume")
	if out.Rule == "system:opt_in" {
		t.Fatalf("resume from a subscribed customer is not an opt-in: %+v", out)
	}
	if c, _ := r.cs.Get(asha); c.Consent || c.ConsentAt != nil {
		t.Fatalf("no consent may be recorded: %+v", c)
	}
}

// C9: a STOP written before the customer's newer START, delivered after it, is ignored;
// the START keeps the time the customer sent it.
func TestOlderStopDoesNotUndoNewerStart(t *testing.T) {
	r := newRig(t)
	now := r.e.now()
	r.e.Process(Incoming{ID: "wamid.s1", Phone: asha, PushName: "Asha", Text: "STOP", SentAt: now.Add(-30 * time.Minute)})
	r.e.Process(Incoming{ID: "wamid.go", Phone: asha, PushName: "Asha", Text: "START", SentAt: now.Add(-5 * time.Minute)})
	if c, _ := r.cs.Get(asha); c.IsDND || c.OptInAt == nil || !c.OptInAt.Equal(now.Add(-5*time.Minute)) {
		t.Fatalf("the START must be recorded with the time it was sent: %+v", c)
	}
	out := r.e.Process(Incoming{ID: "wamid.s0", Phone: asha, PushName: "Asha", Text: "STOP", SentAt: now.Add(-10 * time.Minute)})
	if c, _ := r.cs.Get(asha); c.IsDND || !c.Consent || out.Reply != "" {
		t.Fatalf("an older STOP arriving late must not opt them out again: %+v %+v", c, out)
	}
}

// C8: Indian customers say DND or "stop na".
func TestDNDAndStopNaAreOptOuts(t *testing.T) {
	for _, s := range []string{"DND", "dnd please", "do not disturb", "Please do not disturb me", "stop na", "Stop na!"} {
		if !IsOptOut(s) {
			t.Errorf("%q must opt out", s)
		}
	}
	if IsOptOut("don't stop") {
		t.Error("a negated stop is not an opt-out")
	}
}

// C5: automatic replies carry a label, so a reply recorded as failed (its answer was
// lost) is corrected when Meta reports it delivered.
func TestFailedReplyIsCorrectedByItsLabel(t *testing.T) {
	r := newRig(t)
	r.m.fail = true
	r.e.Process(Incoming{ID: "wamid.L", Phone: asha, PushName: "Asha", Text: "hello"})
	var rec history.MessageRecord
	for _, m := range r.hs.ExportAll() {
		if m.Direction == history.DirectionOutgoing {
			rec = m
		}
	}
	if rec.Status != history.StatusFailed || rec.Label != ReplyLabel+"wamid.L" {
		t.Fatalf("the failed reply must carry its label: %+v", rec)
	}
	if !r.hs.ResolveLabel(rec.Label, "wamid.OUT", history.StatusDelivered) {
		t.Fatal("Meta's report must correct the reply")
	}
}

// C1: a sender from Singapore (wa_id 6591234567) is their own contact, not India's
// 916591234567.
func TestSingaporeSenderKeepsTheirNumber(t *testing.T) {
	r := newRig(t)
	_ = r.cs.Save(contacts.Contact{Phone: "+65 9123 4567", Name: "Wei"})
	r.e.Process(Incoming{ID: "wamid.SG", Phone: "6591234567", PushName: "Wei L", Text: "hello"})
	if _, ok := r.cs.Get("916591234567"); ok || r.cs.Count() != 1 {
		t.Fatal("the Singapore sender was taken for an Indian number")
	}
	if c, _ := r.cs.Get("6591234567"); c.LastInboundAt == nil {
		t.Fatalf("the message must be recorded on the Singapore contact: %+v", c)
	}
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	if len(r.m.quoted) != 1 || !strings.HasPrefix(r.m.quoted[0], "6591234567>") {
		t.Fatalf("the reply must go to the Singapore number: %v", r.m.quoted)
	}
}

// C14: a received message leaves the journal only once the contacts (its STOP) are on
// disk as well as the history.
func TestJournalWaitsForContactsSave(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	cs, err := contacts.NewStore(filepath.Join(dir, "c.json"))
	if err != nil {
		t.Fatal(err)
	}
	r.cs = cs
	r.e = NewEngine(r.m, r.cfg, cs, r.hs, nil, r.n)
	path := filepath.Join(dir, "inbound-pending.json")
	r.e.UseJournal(path)
	// A folder where the contacts file belongs: saving the contacts fails.
	if err := os.Mkdir(filepath.Join(dir, "c.json"), 0700); err != nil {
		t.Fatal(err)
	}
	in := Incoming{ID: "wamid.j", Phone: asha, PushName: "Asha", Text: "STOP", SentAt: time.Now()}
	if err := r.e.jnl.add(in); err != nil {
		t.Fatal(err)
	}
	r.e.Process(in)
	r.e.jnl.done(in.ID)
	if err := r.hs.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, saved := openJournal(path, nil); len(saved) != 1 {
		t.Fatalf("the contacts are not saved: the entry must stay to be replayed: %+v", saved)
	}
	if err := os.Remove(filepath.Join(dir, "c.json")); err != nil {
		t.Fatal(err)
	}
	r.hs.Record(history.MessageRecord{ID: "next", Direction: history.DirectionIncoming, Phone: "1", Text: "x"})
	if err := r.hs.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, saved := openJournal(path, nil); len(saved) != 0 {
		t.Fatalf("history and contacts saved: the entry must be gone: %+v", saved)
	}
	again, _ := contacts.NewStore(filepath.Join(dir, "c.json"))
	if c, ok := again.Get(asha); !ok || !c.IsDND {
		t.Fatal("the STOP must be on disk before the journal entry is removed")
	}
}

// A voice note or a photo without a caption cannot be answered: a person must look.
func TestMediaWithoutTextNeedsAPerson(t *testing.T) {
	r := newRig(t)
	r.e.Process(Incoming{ID: "wamid.V", Phone: asha, PushName: "Asha", Media: "Voice message"})
	c, _ := r.cs.Get(asha)
	if r.m.count() != 0 || !c.NeedsAttention || !strings.Contains(c.AttentionReason, "voice message") || r.n.n != 1 {
		t.Fatalf("a voice note must be flagged for a person: replies=%d attention=%v %q notified=%d", r.m.count(), c.NeedsAttention, c.AttentionReason, r.n.n)
	}
	r.e.Process(Incoming{ID: "wamid.S", Phone: "919800000002", PushName: "Ravi", Media: "Sticker"})
	if c, _ := r.cs.Get("919800000002"); c.NeedsAttention {
		t.Fatal("a sticker needs no person")
	}
}

// "typing..." is sent with a read receipt, so it must not appear with "mark as read" off.
func TestNoReadReceiptWhenAutoReadIsOff(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error {
		c.Automation.AutoRead, c.Automation.TypingSimulation, c.Automation.TypingDurationMs = false, true, 1
		return nil
	})
	r.say(asha, "Asha", "hi")
	if len(r.m.typing) != 0 || len(r.m.reads) != 0 || r.m.count() != 1 {
		t.Fatalf("with auto read off: typing=%d reads=%d replies=%d", len(r.m.typing), len(r.m.reads), r.m.count())
	}
	_ = r.cfg.Update(func(c *config.Config) error { c.Automation.AutoRead = true; return nil })
	r.say(asha, "Asha", "thanks")
	if len(r.m.typing) != 1 {
		t.Fatal("with auto read on, typing is shown")
	}
}

// The opt-out button on Meta's marketing templates arrives as "Stop promotions".
func TestOptOutButtonAndLookalikes(t *testing.T) {
	for _, s := range []string{"Stop promotions", "Unsubscribe me", "ＳＴＯＰ", "STOP\u200b", "please stop"} {
		if !IsOptOut(s) {
			t.Errorf("%q must opt out", s)
		}
	}
	if IsOptOut("stop by the shop tomorrow") {
		t.Error("a sentence that merely contains stop is not an opt-out")
	}
}

// P4 and P5: customers ask in their own words, and look-alike letters do not hide STOP.
func TestOptOutAndHandoverInOwnWords(t *testing.T) {
	for _, s := range []string{"cancel", "QUIT", "STOP please", "stop it", "please stop messaging me",
		"dont message me again", "Stop sending me these", "ЅТОР", "Ѕтор", "remove me from your list", "how do I unsubscribe?"} {
		if !IsOptOut(s) {
			t.Errorf("%q must opt out", s)
		}
	}
	for _, s := range []string{"don't stop", "cancel my order please", "when does the bus stop", "stop by the shop tomorrow", "hi", "price"} {
		if IsOptOut(s) {
			t.Errorf("%q is not an opt-out", s)
		}
	}
	for _, s := range []string{"AGENT please", "I want a human being", "can I talk to a real person", "аgent"} {
		if !IsHandoverRequest(s) {
			t.Errorf("%q must hand over", s)
		}
	}
	for _, s := range []string{"thank you", "what are your hours", "привет"} {
		if IsHandoverRequest(s) {
			t.Errorf("%q is not a hand-over", s)
		}
	}
}

// D37: another bot repeating the same text must not keep Pulse replying.
func TestRepeatedIdenticalMessagesStopReplies(t *testing.T) {
	r := newRig(t)
	for i := 0; i < 8; i++ {
		r.say(asha, "Asha", "hi")
	}
	if n := r.m.count(); n == 0 || n > 2 {
		t.Fatalf("8 identical messages got %d replies, want 1 or 2", n)
	}
	if c, _ := r.cs.Get(asha); c.BotPausedUntil == nil {
		t.Fatal("the chat must be paused as a possible bot loop")
	}
}

// S43: hidden control and direction characters are removed before storing.
func TestCleanDisplayText(t *testing.T) {
	if got := CleanDisplayText("Inv\u202efdp.exe\x00 line1\r\nline2", true); got != "Invfdp.exe line1\nline2" {
		t.Errorf("message: %q", got)
	}
	if got := CleanDisplayText("Asha\nVerma\u2066", false); got != "Asha Verma" {
		t.Errorf("name: %q", got)
	}
	if got := CleanDisplayText("👨\u200d👩\u200d👧", false); got != "👨\u200d👩\u200d👧" {
		t.Errorf("emoji joiners must stay: %q", got)
	}
}
