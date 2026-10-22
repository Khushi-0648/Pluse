package campaign

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
)

type fakeSender struct {
	mu   sync.Mutex
	sent map[string]string
	fail bool
	n    int
}

func (f *fakeSender) SendTemplate(ctx context.Context, to, name, lang string, params []string) (string, error) {
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

func setup(t *testing.T) (*config.Config, *contacts.Store, *history.Store, *fakeSender, *Runner) {
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
	cs, _ := contacts.NewStore(filepath.Join(dir, "c.json"))
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
	tpl, err := (&Template{Name: " order_update ", Language: "en_US", Params: []string{"{{first_name}}", " ", "{{business_name}}"}}).Clean()
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
