package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/msgtemplate"
)

type fakeSender struct {
	mu   sync.Mutex
	sent map[string]string
	fail bool
	n    int
}

func (f *fakeSender) SendTemplate(ctx context.Context, to, name, lang string, parts cloudapi.TemplateParts) (string, error) {
	params := parts.Body
	f.mu.Lock()
	defer f.mu.Unlock()
	f.n++
	if f.sent == nil {
		f.sent = map[string]string{}
	}
	f.sent[to] = "TEMPLATE " + name + "/" + lang + ": " + strings.Join(params, ",")
	return "t" + to, nil
}

func (f *fakeSender) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return "", errors.New("not connected")
	}
	f.n++
	if f.sent == nil {
		f.sent = map[string]string{}
	}
	f.sent[to] = text
	return "m" + to, nil
}

// setup builds a runner. seed, when given, is a contact list written by Pulse 2.0, on
// disk before the store opens (see contacts.LegacyWAID).
func setup(t *testing.T, seed ...contacts.Contact) (*config.Config, *contacts.Store, *history.Store, *fakeSender, *Runner) {
	t.Helper()
	paceOverride = time.Millisecond
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	_ = cfg.Update(func(c *config.Config) error {
		c.Automation.WarmupMode = false
		c.Automation.DailyLimit = 100
		c.Business.Name = "Shop"
		return nil
	})
	cs := seededStore(t, filepath.Join(dir, "c.json"), seed...)
	hs, _ := history.NewStore(filepath.Join(dir, "h.json"), 1000)
	fs := &fakeSender{}
	r, err := NewRunner(filepath.Join(dir, "camp.json"), fs, cfg, cs, hs)
	if err != nil {
		t.Fatal(err)
	}
	// Pending saves are written before the folder is removed.
	t.Cleanup(func() { _ = cs.Flush(); _ = hs.Flush(); _ = r.Flush() })
	return cfg, cs, hs, fs, r
}

func waitStatus(t *testing.T, r *Runner, id, status string) Report {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, rep, _ := r.Get(id)
		if c.Status == status {
			return rep
		}
		time.Sleep(10 * time.Millisecond)
	}
	c, _, _ := r.Get(id)
	t.Fatalf("campaign did not reach %s (now %s: %s)", status, c.Status, c.StopReason)
	return Report{}
}

func TestPlanExplainsExclusions(t *testing.T) {
	cfg, cs, hs, _, _ := setup(t)
	_ = cs.Save(contacts.Contact{Phone: "14155550001", Name: "Ok", Tags: []string{"VIP"}, Consent: true, ConsentSource: "form"})
	_ = cs.Save(contacts.Contact{Phone: "14155550002", Name: "NoConsent", Tags: []string{"VIP"}})
	_ = cs.Save(contacts.Contact{Phone: "14155550003", Name: "Out", Tags: []string{"vip"}, Consent: true, ConsentSource: "form"})
	cs.SetDND("14155550003", true, "STOP")
	_ = cs.Save(contacts.Contact{Phone: "14155550004", Tags: []string{"VIP"}, Consent: true, ConsentSource: "form"})
	p, err := BuildPlan(cfg.Snapshot(), cs, hs, "Hi {{first_name}}, news from {{business_name}}. {{email}}", Audience{Tag: "tag: VIP"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Recipients) != 0 {
		t.Fatalf("contacts without an email value must be left out: %+v", p.Recipients)
	}
	reasons := map[string]int{}
	for _, x := range p.Excluded {
		reasons[strings.SplitN(x.Reason, " ", 2)[0]]++
	}
	if reasons["No"] < 2 || reasons["Opted"] != 1 {
		t.Fatalf("missing reasons (C20): %+v", p.Excluded)
	}
	p, _ = BuildPlan(cfg.Snapshot(), cs, hs, "Hi {{first_name}}", Audience{Tag: "VIP"})
	texts := map[string]string{}
	for _, s := range p.Samples {
		texts[s.Phone] = s.Text
	}
	if len(p.Recipients) != 2 || texts["14155550004"] != "Hi there" || texts["14155550001"] != "Hi Ok" {
		t.Fatalf("plan wrong: %+v", p)
	}
	if _, err := BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{Tag: "nobody"}); err == nil || !strings.Contains(strings.ToLower(err.Error()), "vip") {
		t.Fatalf("unknown tag should list the tags in use: %v", err)
	}
	if _, err := BuildPlan(cfg.Snapshot(), cs, hs, "Hi {{coupon}}", Audience{All: true}); err == nil {
		t.Fatal("unknown variables must be rejected")
	}
}

func TestRunReportAndControls(t *testing.T) {
	cfg, cs, hs, fs, r := setup(t)
	for _, p := range []string{"14155550011", "14155550012", "14155550013"} {
		_ = cs.Save(contacts.Contact{Phone: p, Consent: true, ConsentSource: "test"})
		cs.RecordInbound(p, time.Now())
	}
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "Hello {{first_name}}", Audience{All: true})
	sum, err := r.Start("Test", "Hello {{first_name}}", "admin", plan)
	if err != nil {
		t.Fatal(err)
	}
	rep := waitStatus(t, r, sum.ID, CCompleted)
	if rep.Sent != 3 || fs.n != 3 || fs.sent["14155550011"] != "Hello there" {
		t.Fatalf("report wrong: %+v sent=%v", rep, fs.sent)
	}
	r.OnReceipt([]string{"m14155550011"}, RRead)
	r.OnReceipt([]string{"m14155550011"}, RDelivered)
	r.OnIncoming("14155550012", time.Now().Add(time.Second))
	_, rep, _ = r.Get(sum.ID)
	if rep.Read != 1 || rep.Delivered != 1 || rep.Replied != 1 {
		t.Fatalf("receipts or replies not tracked: %+v", rep)
	}
	if len(hs.Query(history.Query{Phone: "14155550013"})) != 1 {
		t.Fatal("campaign messages must be in the history")
	}

	_ = cfg.Update(func(c *config.Config) error { c.Automation.DailyLimit = 4; return nil })
	if _, err := r.Start("Too big", "Hi", "admin", plan); err == nil {
		t.Fatal("campaigns larger than today's remaining limit must be refused")
	}
}

func TestCircuitBreakerAndResume(t *testing.T) {
	cfg, cs, hs, fs, r := setup(t)
	for i := 0; i < 7; i++ {
		_ = cs.Save(contacts.Contact{Phone: "1415555010" + string(rune('0'+i)), Consent: true, ConsentSource: "t"})
		cs.RecordInbound("1415555010"+string(rune('0'+i)), time.Now())
	}
	fs.fail = true
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{All: true})
	sum, _ := r.Start("Fails", "Hi", "admin", plan)
	waitStatus(t, r, sum.ID, CPaused)
	c, rep, _ := r.Get(sum.ID)
	if rep.Failed != 5 || !strings.Contains(c.StopReason, "5 failed") {
		t.Fatalf("circuit breaker wrong: %+v %s", rep, c.StopReason)
	}
	if _, err := r.Start("Other", "Hi", "admin", plan); err == nil {
		t.Fatal("a new campaign must not start while one is paused")
	}
	fs.fail = false
	if err := r.Resume(sum.ID); err != nil {
		t.Fatal(err)
	}
	rep = waitStatus(t, r, sum.ID, CCompleted)
	if rep.Sent != 2 || rep.Failed != 5 {
		t.Fatalf("resume should send the remaining recipients only: %+v", rep)
	}
}

func TestRestartMarksRunningAsPaused(t *testing.T) {
	cfg, cs, hs, _, r := setup(t)
	_ = cs.Save(contacts.Contact{Phone: "14155550201", Consent: true, ConsentSource: "t"})
	paceOverride = time.Hour
	_ = cs.Save(contacts.Contact{Phone: "14155550202", Consent: true, ConsentSource: "t"})
	cs.RecordInbound("14155550201", time.Now())
	cs.RecordInbound("14155550202", time.Now())
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{All: true})
	sum, _ := r.Start("Slow", "Hi", "admin", plan)
	time.Sleep(50 * time.Millisecond)
	_ = r.Flush()
	r2, err := NewRunner(r.path, &fakeSender{}, cfg, cs, hs)
	if err != nil {
		t.Fatal(err)
	}
	c, _, _ := r2.Get(sum.ID)
	if c.Status != CPaused {
		t.Fatalf("after a restart a running campaign must wait for Resume, got %s", c.Status)
	}
	r.Cancel(sum.ID)
	paceOverride = time.Millisecond
}

// A recipient cut off mid-send is failed without a message ID after a restart; Meta's
// delivery update with the campaign label corrects it, so Resume does not resend.
func TestInterruptedRecipientIsCorrectedByItsLabel(t *testing.T) {
	_, _, _, _, r := setup(t)
	c := &Campaign{ID: "camp1", Name: "Offer", Status: CPaused, Recipients: []Recipient{
		{Phone: "14155550401", Status: RFailed, Error: "Interrupted while sending; check whether it arrived before sending again."},
		{Phone: "14155550402", Status: RFailed, MessageID: "wamid.REAL", Error: "Message undeliverable"},
	}}
	r.mu.Lock()
	r.campaigns = append(r.campaigns, c)
	r.mu.Unlock()
	if r.Reconcile("pulse:s:job", "wamid.X", "delivered") {
		t.Fatal("a scheduler label is not a campaign's")
	}
	if !r.Reconcile(Label+"camp1:14155550401", "wamid.NEW", RDelivered) {
		t.Fatal("the campaign's own label must be recognised")
	}
	r.Reconcile(Label+"camp1:14155550402", "wamid.OTHER", RDelivered)
	got, _, _ := r.Get("camp1")
	if x := got.Recipients[0]; x.Status != RDelivered || x.MessageID != "wamid.NEW" || x.Error != "" || x.SentAt == nil {
		t.Fatalf("the interrupted recipient must show as delivered: %+v", x)
	}
	if x := got.Recipients[1]; x.Status != RFailed || x.MessageID != "wamid.REAL" {
		t.Fatalf("a real failure reported by Meta must stay failed: %+v", x)
	}
	// Later updates for it now find it by its message ID.
	r.OnReceipt([]string{"wamid.NEW"}, RRead)
	if got, _, _ := r.Get("camp1"); got.Recipients[0].Status != RRead {
		t.Fatal("later receipts must apply to the corrected recipient")
	}
}

func TestBatchPauseNeverShorterThanNormalDelay(t *testing.T) {
	old := paceOverride
	paceOverride = 0
	defer func() { paceOverride = old }()
	a := config.AutomationConfig{BroadcastMinDelay: 5, BroadcastMaxDelay: 5, BatchSize: 10, BatchPauseSeconds: 0}
	if d := Pace(a, 10); d < 5*time.Second {
		t.Fatalf("a batch pause of 0 must still wait the normal delay, got %v", d)
	}
	a.BatchPauseSeconds = 60
	if d := Pace(a, 10); d != time.Minute {
		t.Fatalf("batch pause not applied: %v", d)
	}
	if d := Pace(a, 3); d < 5*time.Second || d > 6*time.Second {
		t.Fatalf("normal delay wrong: %v", d)
	}
}

// Parallel senders (automatic replies, campaigns, schedules) must not overshoot the
// daily limit: each reserves before sending (F19).
func TestDailyReservationStopsOvershoot(t *testing.T) {
	cfg, _, hs, _, _ := setup(t)
	_ = cfg.Update(func(c *config.Config) error { c.Automation.DailyLimit = 3; return nil })
	snap := cfg.Snapshot()
	var wg sync.WaitGroup
	var mu sync.Mutex
	got := 0
	var releases []func()
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rel, ok := ReserveDaily(snap, hs, time.Now()); ok {
				mu.Lock()
				got++
				releases = append(releases, rel)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if got != 3 {
		t.Fatalf("expected exactly 3 reservations, got %d", got)
	}
	releases[0]()
	releases[0]() // releasing twice must not free two places
	if _, ok := ReserveDaily(snap, hs, time.Now()); !ok {
		t.Fatal("a released place should be available again")
	}
	if _, ok := ReserveDaily(snap, hs, time.Now()); ok {
		t.Fatal("double release freed an extra place")
	}
	for _, r := range releases[1:] {
		r()
	}
	inFlight.mu.Lock()
	inFlight.n = 0
	inFlight.mu.Unlock()
}

func TestForgetPhoneScrubsRunningCampaign(t *testing.T) {
	cfg, cs, hs, fs, r := setup(t)
	paceOverride = 300 * time.Millisecond
	defer func() { paceOverride = time.Millisecond }()
	for _, p := range []string{"14155550401", "14155550402", "14155550403"} {
		_ = cs.Save(contacts.Contact{Phone: p, Consent: true, ConsentSource: "t"})
		cs.RecordInbound(p, time.Now())
	}
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{All: true})
	sum, err := r.Start("Erase", "Hi", "admin", plan)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	r.ForgetPhone("14155550403")
	c, _, _ := r.Get(sum.ID)
	for _, x := range c.Recipients {
		if x.Phone == "14155550403" {
			t.Fatal("an erased customer must be removed from a running campaign too")
		}
	}
	waitStatus(t, r, sum.ID, CCompleted)
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if _, ok := fs.sent["14155550403"]; ok {
		t.Fatal("an erased customer must not be sent the campaign")
	}
}

// Customers who have not written in 24 hours can only get an approved template: a
// campaign without one is refused, and with one they get the template while customers
// inside the window get the normal text.
func TestTemplateForCustomersOutsideTheWindow(t *testing.T) {
	cfg, cs, hs, fs, r := setup(t)
	_ = cs.Save(contacts.Contact{Phone: "14155550501", Name: "Recent Ravi", Consent: true, ConsentSource: "t"})
	cs.RecordInbound("14155550501", time.Now())
	_ = cs.Save(contacts.Contact{Phone: "14155550502", Name: "Old Olga", Consent: true, ConsentSource: "t"})
	plan, err := BuildPlan(cfg.Snapshot(), cs, hs, "Hi {{first_name}}", Audience{All: true})
	if err != nil || plan.OutsideWindow != 1 || len(plan.Recipients) != 2 {
		t.Fatalf("plan %+v %v", plan, err)
	}
	if _, err := r.Start("No template", "Hi {{first_name}}", "admin", plan); err == nil || !strings.Contains(err.Error(), "template") {
		t.Fatalf("a campaign reaching customers outside the window needs a template: %v", err)
	}
	for _, bad := range []Template{{Name: "Order Update"}, {Name: "ok", Language: "english"}, {Name: "ok", Params: []string{"{{coupon}}"}}} {
		if _, err := bad.Clean(); err == nil {
			t.Errorf("template %+v should be refused", bad)
		}
	}
	// An empty {{2}} between filled values would move {{3}}'s value into {{2}}: refused.
	if _, err := (&Template{Name: "order_update", Params: []string{"{{first_name}}", " ", "{{business_name}}"}}).Clean(); err == nil {
		t.Fatal("an empty value in the middle must be refused, not shifted")
	}
	tpl, err := (&Template{Name: " order_update ", Language: "en_US", Params: []string{"{{first_name}}", "{{business_name}}", " "}}).Clean()
	if err != nil || tpl.Name != "order_update" || len(tpl.Params) != 2 {
		t.Fatalf("clean %+v %v", tpl, err)
	}
	plan.Template = tpl
	sum, err := r.Start("With template", "Hi {{first_name}}", "admin", plan)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, r, sum.ID, CCompleted)
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.sent["14155550501"] != "Hi Recent" || fs.sent["14155550502"] != "TEMPLATE order_update/en_US: Old,Shop" {
		t.Fatalf("sent %v", fs.sent)
	}
	if h := hs.Query(history.Query{Phone: "14155550502"}); len(h) != 1 || !strings.Contains(h[0].Text, "Template") {
		t.Fatalf("the template must be in the history: %+v", h)
	}
	r.OnFailed("t14155550502", "More than 24 hours")
	_, rep, _ := r.Get(sum.ID)
	if rep.Failed != 1 {
		t.Fatalf("a failed delivery must show in the report: %+v", rep)
	}
}

// blockingSender holds the send until released, like a slow Graph API.
type blockingSender struct {
	fakeSender
	started chan struct{}
	release chan struct{}
}

func (b *blockingSender) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	b.started <- struct{}{}
	<-b.release
	return b.fakeSender.SendTextMessage(ctx, to, text)
}

// If Pulse dies while a message is on its way, the claim must already be on disk, so the
// restart marks it interrupted instead of sending it again on Resume.
func TestClaimIsSavedBeforeSending(t *testing.T) {
	cfg, cs, hs, _, r := setup(t)
	bs := &blockingSender{started: make(chan struct{}, 1), release: make(chan struct{})}
	r.sender = bs
	_ = cs.Save(contacts.Contact{Phone: "14155550301", Consent: true, ConsentSource: "t"})
	cs.RecordInbound("14155550301", time.Now())
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{All: true})
	sum, err := r.Start("Crash", "Hi", "admin", plan)
	if err != nil {
		t.Fatal(err)
	}
	<-bs.started // the message is now "on its way"; nothing else has been saved
	after, err := NewRunner(r.path, &fakeSender{}, cfg, cs, hs)
	if err != nil {
		t.Fatal(err)
	}
	c, _, _ := after.Get(sum.ID)
	if len(c.Recipients) != 1 || c.Recipients[0].Status != RFailed || !strings.Contains(c.Recipients[0].Error, "Interrupted") {
		t.Fatalf("a crash mid-send must not leave the recipient pending: %+v", c.Recipients)
	}
	close(bs.release)
	waitStatus(t, r, sum.ID, CCompleted)
}

// C12: while a campaign message is on its way the history already holds a labelled
// "sending" record, so a crash at that moment still counts towards the daily limit.
func TestSendingPlaceholderCountsTowardsTheLimit(t *testing.T) {
	cfg, cs, hs, _, r := setup(t)
	bs := &blockingSender{started: make(chan struct{}, 1), release: make(chan struct{})}
	r.sender = bs
	_ = cs.Save(contacts.Contact{Phone: "14155550311", Consent: true, ConsentSource: "t"})
	cs.RecordInbound("14155550311", time.Now())
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{All: true})
	sum, err := r.Start("Placeholder", "Hi", "admin", plan)
	if err != nil {
		t.Fatal(err)
	}
	<-bs.started
	h := hs.Query(history.Query{Phone: "14155550311"})
	if len(h) != 1 || h[0].Status != history.StatusSending || h[0].Label != Label+sum.ID+":14155550311" {
		t.Fatalf("a labelled placeholder must be recorded before the send: %+v", h)
	}
	if hs.SentSince(time.Now().Add(-time.Hour)) != 1 {
		t.Fatal("the message on its way must count towards the daily limit")
	}
	close(bs.release)
	waitStatus(t, r, sum.ID, CCompleted)
	if h := hs.Query(history.Query{Phone: "14155550311"}); len(h) != 1 || h[0].Status != history.StatusSent || h[0].ID != "m14155550311" {
		t.Fatalf("the placeholder must take the real ID: %+v", h)
	}
}

// marketingSender refuses every text like Meta does for a customer who stopped marketing
// messages, and lets the test deliver receipts like the WhatsApp connection.
type marketingSender struct {
	fakeSender
	receipts []func(cloudapi.Receipt)
}

func (m *marketingSender) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	return "", &cloudapi.APIError{HTTPStatus: 400, Code: cloudapi.CodeMarketingOptOut, Plain: "stopped marketing"}
}

func (m *marketingSender) OnReceipt(h func(cloudapi.Receipt)) { m.receipts = append(m.receipts, h) }

// C7: error 131050 marks the customer, whether Meta says it at once or in a receipt, and
// later campaigns leave them out.
func TestMarketingOptOutIsLeftOutOfCampaigns(t *testing.T) {
	cfg, cs, hs, _, _ := setup(t)
	ms := &marketingSender{}
	r, err := NewRunner("", ms, cfg, cs, hs)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"14155550321", "14155550322"} {
		_ = cs.Save(contacts.Contact{Phone: p, Consent: true, ConsentSource: "t"})
		cs.RecordInbound(p, time.Now())
	}
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{Phones: []string{"14155550321"}})
	sum, err := r.Start("Marketing", "Hi", "admin", plan)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, r, sum.ID, CCompleted)
	if c, _ := cs.Get("14155550321"); !c.MarketingOptOut {
		t.Fatal("a 131050 refusal must mark the contact")
	}
	if len(ms.receipts) != 1 {
		t.Fatal("the runner must listen for Meta's delivery updates")
	}
	ms.receipts[0](cloudapi.Receipt{MessageID: "x", Phone: "14155550322", Status: "failed", Code: cloudapi.CodeMarketingOptOut})
	if c, _ := cs.Get("14155550322"); !c.MarketingOptOut {
		t.Fatal("a 131050 delivery update must mark the contact")
	}
	plan, _ = BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{All: true})
	if len(plan.Recipients) != 0 || len(plan.Excluded) != 2 || !strings.Contains(plan.Excluded[0].Reason, "marketing") {
		t.Fatalf("customers who stopped marketing messages must be left out: %+v", plan)
	}
}

// C6: WhatsApp refuses template values with line breaks, tabs or long runs of spaces.
func TestTemplateValuesAreFlattened(t *testing.T) {
	tpl := Template{Name: "note", Params: []string{"{{notes}}"}}
	parts, missing := tpl.Render(msgtemplate.Vars{Extra: map[string]string{"notes": "Line one\r\nLine two\tend      x"}})
	if len(missing) != 0 || parts.Body[0] != "Line one Line two end x" {
		t.Fatalf("value not flattened: %q %v", parts.Body, missing)
	}
}

// C11, C15, C18: free text stops before the window ends, {{time}} follows the business
// time zone, and the daily limit counts the last 24 hours.
func TestWindowTimeZoneAndRollingLimit(t *testing.T) {
	now := time.Now()
	late := now.Add(-23*time.Hour - 40*time.Minute)
	if !NeedsTemplate(&contacts.Contact{LastInboundAt: &late}, now) {
		t.Fatal("23h40m after the customer wrote, only a template may go")
	}
	cfg, cs, hs, fs, r := setup(t)
	_ = cfg.Update(func(c *config.Config) error {
		c.BusinessHours.Timezone = "Asia/Kolkata"
		c.Automation.DailyLimit = 3
		return nil
	})
	// One message 25 hours ago (no longer counted) and one 23 hours ago (still counted,
	// even though it was "yesterday").
	hs.Record(history.MessageRecord{ID: "old", Direction: history.DirectionOutgoing, Type: history.TypeDirectSend, Phone: "1", Status: history.StatusSent, Timestamp: now.Add(-25 * time.Hour)})
	hs.Record(history.MessageRecord{ID: "recent", Direction: history.DirectionOutgoing, Type: history.TypeDirectSend, Phone: "1", Status: history.StatusSent, Timestamp: now.Add(-23 * time.Hour)})
	_ = cs.Save(contacts.Contact{Phone: "14155550331", Consent: true, ConsentSource: "t"})
	cs.RecordInbound("14155550331", now)
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "At {{time}}", Audience{All: true})
	sum, err := r.Start("Zone", "At {{time}}", "admin", plan)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, r, sum.ID, CCompleted)
	ist, _ := time.LoadLocation("Asia/Kolkata")
	fs.mu.Lock()
	got := fs.sent["14155550331"]
	fs.mu.Unlock()
	if want1, want2 := "At "+time.Now().In(ist).Format("15:04"), "At "+now.In(ist).Format("15:04"); got != want1 && got != want2 {
		t.Fatalf("{{time}} must use the business time zone: %q", got)
	}
	if left := DailyRemaining(cfg.Snapshot(), hs, time.Now()); left != 1 {
		t.Fatalf("the limit counts the last 24 hours: %d left", left)
	}
}

// seededStore opens a contact store at path. seed, when given, is written there first as
// a contact list from Pulse 2.0, so those contacts count as being from before the upgrade.
func seededStore(t *testing.T, path string, seed ...contacts.Contact) *contacts.Store {
	t.Helper()
	if len(seed) > 0 {
		data, err := json.Marshal(seed)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cs, err := contacts.NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}
