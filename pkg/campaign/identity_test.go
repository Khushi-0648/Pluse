package campaign

import (
	"context"
	"sync"
	"testing"
	"time"

	"whatsapp-automation/pkg/contacts"
)

// F3: a recipient deleted after opting out is skipped at send time, never taken for the
// consented Indian contact 916591234567 whose last digits match.
func TestDeletedRecipientIsNotResolvedToLookalike(t *testing.T) {
	_, cs, _, fs, r := setup(t)
	_ = cs.Save(contacts.Contact{Phone: "+91 65912 34567", Name: "Ravi", Consent: true, ConsentSource: "form"})
	cs.Upsert("6591234567", "Wei", "whatsapp")
	cs.SetDND("6591234567", true, "Replied STOP")
	cs.Delete("6591234567")
	cs.RecordInbound("916591234567", time.Now())
	r.mu.Lock()
	r.campaigns = append(r.campaigns, &Campaign{ID: "c-sg", Name: "Offer", Message: "Hi", Status: CPaused,
		Recipients: []Recipient{{Phone: "6591234567", Status: RPending}}})
	r.mu.Unlock()
	if err := r.Resume("c-sg"); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, r, "c-sg", CCompleted)
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.n != 0 {
		t.Fatalf("the deleted, opted-out number was messaged: %v", fs.sent)
	}
}

type holdSender struct {
	*fakeSender
	mu      sync.Mutex
	started int
	first   chan struct{}
	release chan struct{}
}

func (h *holdSender) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	h.mu.Lock()
	h.started++
	n := h.started
	h.mu.Unlock()
	if n == 1 {
		close(h.first)
		<-h.release
	}
	return h.fakeSender.SendTextMessage(ctx, to, text)
}

func (h *holdSender) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.started
}

// F16: Resume while the previous run's message is still on its way waits for it before
// the next recipient is sent.
func TestResumeWaitsForInFlightSend(t *testing.T) {
	_, cs, _, fs, r := setup(t)
	for _, p := range []string{"14155550801", "14155550802"} {
		_ = cs.Save(contacts.Contact{Phone: p, Consent: true, ConsentSource: "form"})
		cs.RecordInbound(p, time.Now())
	}
	hs := &holdSender{fakeSender: fs, first: make(chan struct{}), release: make(chan struct{})}
	r.sender = hs
	r.mu.Lock()
	r.campaigns = append(r.campaigns, &Campaign{ID: "c-two", Name: "Offer", Message: "Hi", Status: CPaused,
		Recipients: []Recipient{{Phone: "14155550801", Status: RPending}, {Phone: "14155550802", Status: RPending}}})
	r.mu.Unlock()
	if err := r.Resume("c-two"); err != nil {
		t.Fatal(err)
	}
	<-hs.first
	r.Pause("test")
	if err := r.Resume("c-two"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if n := hs.count(); n != 1 {
		close(hs.release)
		t.Fatalf("the next message went out while the first was still on its way (%d sends)", n)
	}
	close(hs.release)
	waitStatus(t, r, "c-two", CCompleted)
	if hs.count() != 2 {
		t.Fatalf("both recipients are sent once: %d", hs.count())
	}
}
