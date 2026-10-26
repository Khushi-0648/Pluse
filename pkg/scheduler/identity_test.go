package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
)

func noOptIn(c *config.Config) { c.Automation.RequireOptIn = false }

// F3: a recipient deleted after opting out is never taken for the consented Indian
// contact 916591234567 at send time.
func TestDeletedOptedOutRecipientIsNotResolvedToLookalike(t *testing.T) {
	s, snd, cs := newTestScheduler(t, noOptIn)
	_ = cs.Save(contacts.Contact{Phone: "+91 65912 34567", Name: "Ravi", Consent: true, ConsentSource: "form"})
	cs.Upsert("6591234567", "Wei", "whatsapp")
	cs.RecordInbound("6591234567", time.Now())
	j, _, err := s.Add("6591234567", "See you", time.Now(), "", "admin", nil)
	if err != nil || j.Recipient != "6591234567" {
		t.Fatalf("%+v %v", j, err)
	}
	cs.SetDND("6591234567", true, "Replied STOP")
	cs.Delete("6591234567")
	cs.RecordInbound("916591234567", time.Now())
	s.processDue(context.Background())
	if len(snd.sent) != 0 {
		t.Fatalf("an opted-out, deleted recipient was messaged: %v", snd.sent)
	}
	if got := s.List()[0]; got.Status != StatusSkipped || !strings.Contains(got.Error, "opted out") {
		t.Fatalf("must be skipped as opted out: %+v", got)
	}
}

// F12: without an opt-in a scheduled message follows the same 23h30 window as campaigns.
func TestSchedulerUsesFreeTextWindow(t *testing.T) {
	s, snd, cs := newTestScheduler(t, noOptIn)
	cs.Upsert("14155550701", "Ann", "whatsapp")
	cs.RecordInbound("14155550701", time.Now().Add(-23*time.Hour-45*time.Minute))
	tpl := &campaign.Template{Name: "reminder", Language: "en"}
	if _, _, err := s.Add("14155550701", "Hi", time.Now(), "", "admin", tpl); err != nil {
		t.Fatal(err)
	}
	s.processDue(context.Background())
	if len(snd.sent) != 0 {
		t.Fatalf("no opt-in and last message 23h45 ago: nothing may be sent: %v", snd.sent)
	}
}

type maybeSender struct{ *sender }

func (m maybeSender) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	return "", &cloudapi.APIError{HTTPStatus: 500, Plain: "WhatsApp had a problem and may or may not have sent this message."}
}

// F11: a message being sent cannot be retried, and one that may have reached the
// customer is only sent again when confirmed.
func TestRetryRefusesSendingAndNeedsConfirmWhenUncertain(t *testing.T) {
	s, snd, cs := newTestScheduler(t, noOptIn)
	cs.Upsert("14155550702", "Ben", "whatsapp")
	cs.RecordInbound("14155550702", time.Now())
	j, _, err := s.Add("14155550702", "Hi", time.Now(), "", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.jobs[0].Status = StatusSending
	s.mu.Unlock()
	if _, err := s.Retry(j.ID, true); err == nil {
		t.Fatal("a message being sent must not be retried")
	}
	s.mu.Lock()
	s.jobs[0].Status = StatusPending
	s.sender = maybeSender{snd}
	s.mu.Unlock()
	s.processDue(context.Background())
	if got := s.List()[0]; got.Status != StatusFailed || !got.MaybeSent {
		t.Fatalf("an uncertain send must be marked: %+v", got)
	}
	if _, err := s.Retry(j.ID, false); !errors.Is(err, ErrRetryNeedsConfirm) {
		t.Fatalf("retrying a message that may have been sent needs a confirmation: %v", err)
	}
	if got := s.List()[0]; got.Status != StatusFailed {
		t.Fatalf("a refused retry changes nothing: %+v", got)
	}
	if _, err := s.Retry(j.ID, true); err != nil {
		t.Fatal(err)
	}
	if got := s.List()[0]; got.Status != StatusPending || got.MaybeSent {
		t.Fatalf("a confirmed retry is pending again: %+v", got)
	}
}
