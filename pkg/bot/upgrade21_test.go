package bot

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
)

// N1: Pulse 2.0 saved the Singapore number 6591234567 as 916591234567. After the upgrade
// a message from 6591234567 must find that contact (one contact, still opted out, its
// messages along) and get no reply.
func TestLegacyMisprefixedOptOutSurvivesUpgrade(t *testing.T) {
	stop := time.Date(2026, 10, 19, 11, 0, 0, 0, time.Local)
	r := newRig(t, contacts.Contact{Phone: "916591234567", Name: "Wei", Source: "whatsapp", IsDND: true, ConsentSource: "Replied STOP", ConsentAt: &stop, Tags: []string{"VIP"}, CreatedAt: stop.Add(-time.Hour)})
	r.hs.Record(history.MessageRecord{ID: "old.stop", Direction: history.DirectionIncoming, Type: history.TypeUserMessage, Phone: "916591234567", Text: "STOP", Timestamp: r.now.Add(-48 * time.Hour)})

	out := r.e.Process(Incoming{ID: "wamid.sg1", Phone: "6591234567", PushName: "Wei", Text: "hello", SentAt: r.now})
	if r.m.count() != 0 || out.Reply != "" && out.Skipped == "" {
		t.Fatalf("an opted-out customer must not be answered: %+v", out)
	}
	if r.cs.Count() != 1 {
		t.Fatalf("want one contact, got %d: %+v", r.cs.Count(), r.cs.List())
	}
	c, ok := r.cs.Get("6591234567")
	if !ok || c.Phone != "6591234567" || !c.IsDND || c.Consent || len(c.Tags) == 0 {
		t.Fatalf("the contact must move to the real number with its opt-out and tags: %+v", c)
	}
	if !r.cs.WasOptedOut("916591234567") {
		t.Fatal("the old number must stay opted out too, in case it really is India's")
	}
	if got := r.hs.List(10, "6591234567", ""); len(got) != 2 {
		t.Fatalf("the old messages must be found under the real number: %+v", got)
	}
}

// N1: an opt-out that 2.0 kept only in the suppression list (contact deleted) under the
// mis-prefixed number still applies to the real number.
func TestLegacySuppressionAppliesToRealNumber(t *testing.T) {
	r := newRig(t)
	r.cs.Upsert("916591234567", "", "whatsapp")
	r.cs.SetDND("916591234567", true, "Replied STOP")
	r.cs.Delete("916591234567")
	r.e.Process(Incoming{ID: "wamid.sg2", Phone: "6591234567", Text: "hello", SentAt: r.now})
	if r.m.count() != 0 {
		t.Fatal("an opted-out customer must not be answered")
	}
	if c, ok := r.cs.Get("6591234567"); !ok || !c.IsDND {
		t.Fatalf("the new contact must be do-not-message: %+v", c)
	}
	r.cs.Delete("6591234567")
	if !r.cs.WasOptedOut("6591234567") {
		t.Fatal("the opt-out must now be recorded under the real number")
	}
}

// N6: a message older than contacts.FreeTextWindow (23h30) gets no free-text reply.
func TestStaleCutoffIsFreeTextWindow(t *testing.T) {
	r := newRig(t)
	out := r.e.Process(Incoming{ID: "wamid.late", Phone: asha, Text: "hours", SentAt: r.now.Add(-23*time.Hour - 45*time.Minute)})
	if r.m.count() != 0 || out.Skipped == "" {
		t.Fatalf("a message 23h45 old must not be answered with free text: %+v", out)
	}
	out = r.e.Process(Incoming{ID: "wamid.ok", Phone: asha, Text: "hours", SentAt: r.now.Add(-23 * time.Hour)})
	if r.m.count() != 1 {
		t.Fatalf("a message 23h old is still answered: %+v", out)
	}
}

type probeMessenger struct {
	*fakeMessenger
	before func()
}

func (p probeMessenger) SendReply(ctx context.Context, phone, text, quotedID string) (string, error) {
	p.before()
	return p.fakeMessenger.SendReply(ctx, phone, text, quotedID)
}

// N7: the reply's record (with its label) is on disk before the reply is sent, and a
// replayed message whose reply label exists is not answered again.
func TestReplyRecordedOnDiskBeforeSending(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	hpath := filepath.Join(dir, "h.json")
	hs, err := history.NewStore(hpath, 1000)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine(nil, r.cfg, r.cs, hs, nil, r.n)
	e.SetClock(func() time.Time { r.now = r.now.Add(time.Second); return r.now })
	e.UseJournal(filepath.Join(dir, "inbound-pending.json"))
	checked := false
	e.SetMessenger(probeMessenger{fakeMessenger: r.m, before: func() {
		onDisk, err := history.NewStore(hpath, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if !onDisk.HasLabel(ReplyLabel+"wamid.crash", time.Time{}) {
			t.Error("the reply must be recorded on disk before it is sent")
		}
		checked = true
	}})
	e.Process(Incoming{ID: "wamid.crash", Phone: asha, Text: "hours", SentAt: r.now})
	if !checked || r.m.count() != 1 {
		t.Fatalf("the reply was not sent (checked %v, sent %d)", checked, r.m.count())
	}

	// After a crash only the reply's record made it to disk: the replay is skipped.
	r2 := newRig(t)
	r2.hs.Placeholder(history.MessageRecord{Direction: history.DirectionOutgoing, Type: history.TypeAutoReply, Phone: asha, Text: "x", Label: ReplyLabel + "wamid.z", Timestamp: r2.now})
	out := r2.e.Process(Incoming{ID: "wamid.z", Phone: asha, Text: "hours", SentAt: r2.now.Add(-time.Minute)})
	if r2.m.count() != 0 || out.Skipped == "" {
		t.Fatalf("a message whose reply was already sent must not be answered again: %+v", out)
	}
}

// Minor: a test number typed as a saved contact's exact number allows only that contact.
func TestAllowListExactForSavedNumber(t *testing.T) {
	r := newRig(t)
	_ = r.cfg.Update(func(c *config.Config) error {
		c.Automation.AllowListOnly = true
		c.Automation.AllowList = []string{"6591234567"}
		return nil
	})
	r.cs.Upsert("6591234567", "Wei", "whatsapp")
	r.e.Process(Incoming{ID: "wamid.in1", Phone: "916591234567", Text: "hours", SentAt: r.now})
	if r.m.count() != 0 {
		t.Fatal("India's 916591234567 is not the Singapore test number")
	}
	r.e.Process(Incoming{ID: "wamid.in2", Phone: "6591234567", Text: "hours", SentAt: r.now})
	if r.m.count() != 1 {
		t.Fatal("the Singapore test number must be answered")
	}
}

// F14: a STOP recorded while the chat's queue is full keeps a send time in the future
// (a wrong clock) capped at now, like the normal path, so a later START still works.
func TestQueueFullStopCapsFutureSendTime(t *testing.T) {
	r := newRig(t)
	phone := "14155550199"
	q := make(chan Incoming, 64)
	for i := 0; i < cap(q); i++ {
		q <- Incoming{ID: "busy", Phone: phone, Text: "hi"}
	}
	r.e.mu.Lock()
	r.e.queues[phone] = q // no worker: the queue stays full
	r.e.mu.Unlock()
	future := r.now.Add(48 * time.Hour)
	r.e.enqueue(Incoming{ID: "wamid.stop", Phone: phone, Text: "STOP", SentAt: future})
	c, ok := r.cs.GetExact(phone)
	if !ok || !c.IsDND || c.ConsentAt == nil || !c.ConsentAt.Before(future) {
		t.Fatalf("the opt-out must be applied with its time capped at now: %+v", c)
	}
	r.e.Process(Incoming{ID: "wamid.start", Phone: phone, Text: "START", SentAt: r.now.Add(time.Hour)})
	if c, _ := r.cs.GetExact(phone); c.IsDND {
		t.Fatal("a START sent after the STOP must opt the customer back in")
	}
}
