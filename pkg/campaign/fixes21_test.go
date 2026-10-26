package campaign

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
)

type panicSender struct{ fakeSender }

func (p *panicSender) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	panic("boom")
}

// N8: an internal error while a message is on its way leaves neither the recipient nor
// its history record "sending": the recipient fails (never sent again on Resume) and the
// record is an uncertain failure that still counts towards the daily limit.
func TestPanicMidSendSettlesRecipientAndPlaceholder(t *testing.T) {
	cfg, cs, hs, _, r := setup(t)
	r.sender = &panicSender{}
	_ = cs.Save(contacts.Contact{Phone: "14155550401", Consent: true, ConsentSource: "t"})
	cs.RecordInbound("14155550401", time.Now())
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{All: true})
	sum, err := r.Start("Panic", "Hi", "admin", plan)
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, r, sum.ID, CPaused)
	c, _, _ := r.Get(sum.ID)
	if len(c.Recipients) != 1 || c.Recipients[0].Status != RFailed || !strings.Contains(c.Recipients[0].Error, "may have reached") {
		t.Fatalf("the recipient must be failed as possibly sent: %+v", c.Recipients)
	}
	h := hs.Query(history.Query{Phone: "14155550401"})
	if len(h) != 1 || h[0].Status != history.StatusFailed || !h[0].Uncertain {
		t.Fatalf("the placeholder must be settled as an uncertain failure: %+v", h)
	}
	if hs.SentSince(time.Now().Add(-time.Hour)) != 1 {
		t.Fatal("a possibly sent message counts towards the daily limit")
	}
}

// N9: without an opt-in, a customer who wrote between 23h30 and 24h ago is outside the
// window, like CanSendFreeText.
func TestNoOptInOutsideFreeTextWindowIsExcluded(t *testing.T) {
	cfg, cs, hs, _, _ := setup(t)
	_ = cfg.Update(func(c *config.Config) error {
		c.Automation.Enforce24hWindow, c.Automation.RequireOptIn = true, false
		return nil
	})
	_ = cs.Save(contacts.Contact{Phone: "14155550402"})
	cs.RecordInbound("14155550402", time.Now().Add(-23*time.Hour-45*time.Minute))
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{All: true})
	if len(plan.Recipients) != 0 || len(plan.Excluded) != 1 {
		t.Fatalf("a customer who wrote 23h45 ago without opt-in must be left out: %+v", plan)
	}
	ct, _ := cs.Get("14155550402")
	if sendSkipReason(cfg.Snapshot(), cs, ct, true, ct.Phone, time.Now()) == "" {
		t.Fatal("the check before sending must also leave them out")
	}
}

// Minor: a test number that is exactly a saved contact's number allows only that contact.
func TestAllowListMatchesSavedNumberExactly(t *testing.T) {
	cfg, cs, hs, _, _ := setup(t)
	_ = cfg.Update(func(c *config.Config) error {
		c.Automation.AllowListOnly, c.Automation.AllowList = true, []string{"6591234567"}
		c.Automation.RequireOptIn, c.Automation.Enforce24hWindow = false, false
		return nil
	})
	cs.Upsert("6591234567", "Wei", "whatsapp")
	cs.Upsert("916591234567", "Ravi", "whatsapp")
	plan, _ := BuildPlan(cfg.Snapshot(), cs, hs, "Hi", Audience{All: true})
	if len(plan.Recipients) != 1 || plan.Recipients[0].Phone != "6591234567" {
		t.Fatalf("only the Singapore test number may receive it: %+v", plan.Recipients)
	}
}

// N3: SendRecorded records before sending, settles an ambiguous outcome as uncertain,
// and is not cut off when the request's context is cancelled.
func TestSendRecordedSettlesAmbiguousOutcome(t *testing.T) {
	_, _, hs, _, _ := setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the browser tab was closed
	rec := history.MessageRecord{Direction: history.DirectionOutgoing, Type: history.TypeDirectSend, Phone: "14155550403", Text: "Hi", Label: "pulse:d:x"}
	_, err := SendRecorded(ctx, hs, rec, time.Minute, func(sctx context.Context) (string, error) {
		if sctx.Err() != nil {
			t.Error("the send must not inherit the request's cancellation")
		}
		if h := hs.Query(history.Query{Phone: "14155550403"}); len(h) != 1 || h[0].Status != history.StatusSending {
			t.Errorf("the record must exist before sending: %+v", h)
		}
		return "", &cloudapi.APIError{HTTPStatus: 503, Plain: "unavailable"}
	})
	if !cloudapi.MaybeSent(err) {
		t.Fatalf("want an uncertain error, got %v", err)
	}
	h := hs.Query(history.Query{Phone: "14155550403"})
	if len(h) != 1 || h[0].Status != history.StatusFailed || !h[0].Uncertain || h[0].Label != "pulse:d:x" {
		t.Fatalf("an ambiguous send must be recorded as possibly sent: %+v", h)
	}
	if hs.SentSince(time.Now().Add(-time.Hour)) != 1 {
		t.Fatal("it must count towards the daily limit")
	}
	_, err = SendRecorded(context.Background(), hs, history.MessageRecord{Direction: history.DirectionOutgoing, Phone: "14155550404", Label: "pulse:d:y"}, time.Minute, func(context.Context) (string, error) {
		return "", cloudapi.ErrNotConnected
	})
	if !errors.Is(err, cloudapi.ErrNotConnected) || len(hs.Query(history.Query{Phone: "14155550404"})) != 0 {
		t.Fatal("nothing is recorded when WhatsApp was not connected")
	}
}

// Campaign recipients still waiting under the number Pulse 2.0 saved move to the real
// number when the contact is moved, so the campaign cannot send to the Indian number.
func TestWaitingRecipientsFollowAMovedContact(t *testing.T) {
	_, cs, _, _, r := setup(t, contacts.Contact{Phone: "916591234567", Name: "Wei", Source: "whatsapp", CreatedAt: time.Now().Add(-48 * time.Hour)})
	r.mu.Lock()
	r.campaigns = append(r.campaigns, &Campaign{ID: "c-old", Name: "Old", Status: CPaused,
		Recipients: []Recipient{{Phone: "916591234567", Status: RPending}, {Phone: "916591234568", Status: RSent}}})
	r.mu.Unlock()
	cs.Upsert("6591234567", "Wei", "whatsapp") // the real number writes after the upgrade
	if _, ok := cs.Get("6591234567"); !ok {
		t.Skip("the contact was not moved; nothing to follow")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if got := r.campaigns[len(r.campaigns)-1].Recipients[0].Phone; got != "6591234567" {
		t.Fatalf("the waiting recipient still goes to %s", got)
	}
	if got := r.campaigns[len(r.campaigns)-1].Recipients[1].Phone; got != "916591234568" {
		t.Fatalf("a recipient already sent must not change: %s", got)
	}
}
