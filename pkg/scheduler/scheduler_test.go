package scheduler

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
)

type sender struct {
	mu   sync.Mutex
	sent []string
	fail bool
}

func (s *sender) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return "", errors.New("WhatsApp did not accept the message")
	}
	s.sent = append(s.sent, to+":"+text)
	return "id" + to, nil
}

func (s *sender) SendTemplate(ctx context.Context, to, name, lang string, params []string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, to+":TEMPLATE "+name+" "+strings.Join(params, ","))
	return "tpl" + to, nil
}

func init() {
	pace = func(config.AutomationConfig, int) time.Duration { return time.Millisecond }
}

func newTestScheduler(t *testing.T, edit func(*config.Config)) (*Scheduler, *sender, *contacts.Store) {
	t.Helper()
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	_ = cfg.Update(func(c *config.Config) error {
		c.Automation.WarmupMode = false
		if edit != nil {
			edit(c)
		}
		return nil
	})
	cs, _ := contacts.NewStore(filepath.Join(dir, "c.json"))
	hs, _ := history.NewStore(filepath.Join(dir, "h.json"), 100)
	snd := &sender{}
	s, err := NewScheduler(filepath.Join(dir, "s.json"), snd, cfg, cs, hs)
	if err != nil {
		t.Fatal(err)
	}
	return s, snd, cs
}

// "Require opt-in" applies to scheduled messages as it does to campaigns (F17).
func TestScheduledMessagesRequireOptIn(t *testing.T) {
	s, snd, cs := newTestScheduler(t, func(c *config.Config) {
		c.Automation.RequireOptIn = true
		c.Automation.Enforce24hWindow = false
	})
	_ = cs.Save(contacts.Contact{Phone: "14155550311", Name: "Cold"}) // no opt-in, has not written
	j, _, err := s.Add("14155550311", "Hello", time.Now(), "", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.processDue(context.Background())
	if got := s.List()[0]; got.ID != j.ID || got.Status != StatusSkipped || !strings.Contains(got.Error, "opt-in") {
		t.Fatalf("message without opt-in must be skipped: %+v", got)
	}
	if len(snd.sent) != 0 {
		t.Fatal("nothing should have been sent")
	}
}

// After 5 failures in a row, scheduled sending pauses instead of trying every due
// message (F18).
func TestScheduledSendingPausesAfterRepeatedFailures(t *testing.T) {
	s, snd, cs := newTestScheduler(t, nil)
	snd.fail = true
	for i := 0; i < 7; i++ {
		p := fmt.Sprintf("1415555032%d", i)
		_ = cs.Save(contacts.Contact{Phone: p, Consent: true, ConsentSource: "form"})
		cs.RecordInbound(p, time.Now())
		if _, _, err := s.Add(p, "Hi", time.Now(), "", "admin", nil); err != nil {
			t.Fatal(err)
		}
	}
	s.processDue(context.Background())
	failed := 0
	for _, j := range s.List() {
		if j.Status == StatusFailed {
			failed++
		}
	}
	if failed != 5 || s.PendingCount() != 2 {
		t.Fatalf("expected 5 failures and 2 still pending, got %d failed, %d pending", failed, s.PendingCount())
	}
	s.processDue(context.Background())
	if s.PendingCount() != 2 {
		t.Fatal("sending must stay paused after 5 failures in a row")
	}
}

func TestScheduledMessagesFollowPolicy(t *testing.T) {
	dir := t.TempDir()
	cfg := config.DefaultConfig()
	_ = cfg.Update(func(c *config.Config) error { c.Automation.WarmupMode = false; return nil })
	cs, _ := contacts.NewStore(filepath.Join(dir, "c.json"))
	hs, _ := history.NewStore(filepath.Join(dir, "h.json"), 100)
	snd := &sender{}
	s, err := NewScheduler(filepath.Join(dir, "s.json"), snd, cfg, cs, hs)
	if err != nil {
		t.Fatal(err)
	}
	_ = cs.Save(contacts.Contact{Phone: "14155550301", Name: "Ok", Consent: true, ConsentSource: "form"})
	cs.RecordInbound("14155550301", time.Now())
	_ = cs.Save(contacts.Contact{Phone: "14155550302", Name: "Cold"})
	_ = cs.Save(contacts.Contact{Phone: "14155550303", Name: "Out"})
	cs.SetDND("14155550303", true, "STOP")

	if _, _, err := s.Add("14155550303", "Hi", time.Now().Add(time.Hour), "", "admin", nil); err == nil {
		t.Fatal("scheduling to an opted-out customer must be refused")
	}
	if _, _, err := s.Add("14155550301", "Hi {{coupon}}", time.Now().Add(time.Hour), "", "admin", nil); err == nil {
		t.Fatal("unknown variables must be refused")
	}
	if _, _, err := s.Add("14155550301", "Hi", time.Now().Add(-time.Hour), "", "admin", nil); err == nil {
		t.Fatal("times in the past must be refused")
	}
	ok, warn, err := s.Add("14155550301", "Hello {{first_name}}", time.Now(), "", "admin", nil)
	if err != nil || len(warn) != 0 {
		t.Fatalf("opted-in customer: %v %v", err, warn)
	}
	cold, warn, _ := s.Add("14155550302", "Hello", time.Now(), "", "admin", nil)
	if len(warn) == 0 {
		t.Fatal("a warning is expected for customers without opt-in")
	}
	s.processDue(context.Background())
	byID := map[string]ScheduledJob{}
	for _, j := range s.List() {
		byID[j.ID] = j
	}
	if byID[ok.ID].Status != StatusSent || len(snd.sent) != 1 || !strings.Contains(snd.sent[0], "Hello Ok") {
		t.Fatalf("opted-in message not sent: %+v %v", byID[ok.ID], snd.sent)
	}
	if byID[cold.ID].Status != StatusSkipped {
		t.Fatalf("message outside the 24-hour window must be skipped (P6): %+v", byID[cold.ID])
	}
	cs.RecordInbound("14155550302", time.Now())
	if _, err := s.Retry(cold.ID); err != nil {
		t.Fatal(err)
	}
	s.processDue(context.Background())
	if len(snd.sent) != 2 {
		t.Fatal("after the customer wrote, the retried message should be sent")
	}
	if s.PendingCount() != 0 {
		t.Fatal("nothing should be pending")
	}
}

// Outside the 24-hour window a scheduled message is sent as its approved template, or
// skipped with a plain reason when it has none.
func TestScheduledTemplateOutsideTheWindow(t *testing.T) {
	s, snd, cs := newTestScheduler(t, nil)
	_ = cs.Save(contacts.Contact{Phone: "14155550601", Name: "Old Olga", Consent: true, ConsentSource: "form"})
	_ = cs.Save(contacts.Contact{Phone: "14155550602", Name: "Old Omar", Consent: true, ConsentSource: "form"})
	if _, _, err := s.Add("14155550601", "Hi", time.Now(), "", "admin", &campaign.Template{Name: "Bad Name"}); err == nil {
		t.Fatal("a badly written template name must be refused")
	}
	withT, warn, err := s.Add("14155550601", "Hi {{first_name}}", time.Now(), "", "admin", &campaign.Template{Name: "reminder", Language: "en", Params: []string{"{{first_name}}"}})
	if err != nil || len(warn) != 0 {
		t.Fatalf("%v %v", err, warn)
	}
	without, warn, _ := s.Add("14155550602", "Hi", time.Now(), "", "admin", nil)
	if len(warn) == 0 {
		t.Fatal("scheduling free text to someone outside the window must warn")
	}
	s.processDue(context.Background())
	byID := map[string]ScheduledJob{}
	for _, j := range s.List() {
		byID[j.ID] = j
	}
	if byID[withT.ID].Status != StatusSent || len(snd.sent) != 1 || snd.sent[0] != "14155550601:TEMPLATE reminder Old" {
		t.Fatalf("template not sent: %+v %v", byID[withT.ID], snd.sent)
	}
	if j := byID[without.ID]; j.Status != StatusSkipped || !strings.Contains(j.Error, "24 hours") {
		t.Fatalf("free text outside the window must be skipped: %+v", j)
	}
}
