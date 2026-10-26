package bot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A message Meta was told it received must survive a crash before it was processed, and
// one that was processed just before the crash must not be answered a second time.
func TestJournalReplaysUnprocessedMessagesOnce(t *testing.T) {
	r := newRig(t)
	path := filepath.Join(t.TempDir(), "inbound-pending.json")
	now := r.now
	lost := Incoming{ID: "wamid.lost", Phone: asha, PushName: "Asha", Text: "hi", SentAt: now.Add(-time.Minute)}
	handled := Incoming{ID: "wamid.handled", Phone: "919800000005", PushName: "Ravi", Text: "hi", SentAt: now.Add(-2 * time.Minute)}
	r.e.Process(handled) // answered before the "crash"
	answered := r.m.count()
	data, _ := json.Marshal([]Incoming{lost, handled})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}

	// Restart: a new engine finds both in the journal.
	r.e = NewEngine(nil, r.cfg, r.cs, r.hs, nil, r.n)
	r.e.SetMessenger(r.m)
	r.e.SetClock(func() time.Time { r.now = r.now.Add(time.Second); return r.now })
	pending := r.e.UseJournal(path)
	if len(pending) != 2 || pending[0].ID != handled.ID {
		t.Fatalf("want both messages back, oldest first: %+v", pending)
	}
	for _, in := range pending {
		r.e.Receive(in)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !r.e.Stop(ctx) {
		t.Fatal("replayed messages were not processed")
	}
	if got := r.m.count() - answered; got != 1 {
		t.Fatalf("only the unprocessed message may be answered, got %d new replies", got)
	}
	if _, ok := r.hs.Get(lost.ID); !ok {
		t.Fatal("the replayed message must be in the history")
	}
	if err := r.hs.Flush(); err != nil {
		t.Fatal(err)
	}
	_, left := openJournal(path, nil)
	if len(left) != 0 {
		t.Fatalf("processed messages must leave the journal once the history is saved: %+v", left)
	}
}

// A message stays in the journal until it is processed AND the history holding it is
// on disk: a crash in between (history saves are batched) must still replay it.
func TestJournalHoldsMessageUntilHistoryIsSaved(t *testing.T) {
	r := newRig(t)
	path := filepath.Join(t.TempDir(), "inbound-pending.json")
	r.e.UseJournal(path)
	in := Incoming{ID: "wamid.x", Phone: asha, PushName: "Asha", Text: "hi", SentAt: r.now}
	if err := r.e.jnl.add(in); err != nil {
		t.Fatal(err)
	}
	if _, saved := openJournal(path, nil); len(saved) != 1 {
		t.Fatalf("the message must be on disk before it is processed: %+v", saved)
	}
	r.e.Process(in)
	r.e.jnl.done(in.ID)
	if _, saved := openJournal(path, nil); len(saved) != 1 {
		t.Fatalf("handled but the history is not saved yet: it must still be listed: %+v", saved)
	}
	if err := r.hs.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, saved := openJournal(path, nil); len(saved) != 0 {
		t.Fatalf("once the history is saved it must leave the journal: %+v", saved)
	}
}

// A journal that cannot be read is moved aside, never overwritten by the next message.
func TestUnreadableJournalIsKeptAside(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inbound-pending.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	j, got := openJournal(path, nil)
	if len(got) != 0 {
		t.Fatalf("nothing can be replayed from a broken file: %+v", got)
	}
	_ = j.add(Incoming{ID: "wamid.n", Phone: asha, Text: "hi"})
	aside, _ := filepath.Glob(path + ".unreadable-*")
	if len(aside) != 1 {
		t.Fatalf("the broken file must be kept aside: %v", aside)
	}
	if data, _ := os.ReadFile(aside[0]); string(data) != "{not json" {
		t.Fatal("the kept file must be unchanged")
	}
}

// Two messages from one chat with the same timestamp are replayed in arrival order.
func TestJournalKeepsArrivalOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbound-pending.json")
	j, _ := openJournal(path, nil)
	at := time.Date(2026, 10, 21, 11, 0, 0, 0, time.UTC)
	for _, id := range []string{"c", "a", "b"} {
		_ = j.add(Incoming{ID: id, Phone: asha, Text: id, SentAt: at})
	}
	_, got := openJournal(path, nil)
	if len(got) != 3 || got[0].ID != "c" || got[1].ID != "a" || got[2].ID != "b" {
		t.Fatalf("want arrival order c, a, b: %+v", got)
	}
}
