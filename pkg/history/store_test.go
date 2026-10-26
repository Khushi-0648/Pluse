package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A send recorded as failed (its answer was lost) takes the real ID and status when Meta
// reports its label delivered; a real failure Meta reported (no label match) stays failed.
func TestResolveLabelCorrectsLostAnswer(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "m.json"), 100)
	if err != nil {
		t.Fatal(err)
	}
	s.Record(MessageRecord{ID: "fail_x1", Direction: DirectionOutgoing, Type: TypeScheduled, Phone: "14155550500", Text: "hi", Status: StatusFailed, Error: "took too long", Label: "pulse:s:job9"})
	s.Record(MessageRecord{ID: "wamid.REAL", Direction: DirectionOutgoing, Type: TypeBroadcast, Phone: "14155550501", Text: "hi", Status: StatusFailed, Error: "Not on WhatsApp", Label: "pulse:c:c1:14155550501"})
	if s.ResolveLabel("pulse:s:job9", "wamid.NEW", "failed") {
		t.Fatal("a failed status must never correct a record")
	}
	if !s.ResolveLabel("pulse:s:job9", "wamid.NEW", StatusDelivered) {
		t.Fatal("the lost answer must be corrected")
	}
	if _, old := s.Get("fail_x1"); old {
		t.Fatal("the placeholder ID must be gone")
	}
	if m, ok := s.Get("wamid.NEW"); !ok || m.Status != StatusDelivered || m.Error != "" {
		t.Fatalf("the record must carry the real ID and status: %+v", m)
	}
	if s.ResolveLabel("pulse:c:c1:14155550501", "wamid.REAL", StatusDelivered) {
		t.Fatal("a record already stored under the real ID is not changed here")
	}
}

// C12: a placeholder written before a send counts towards the daily limit; after a
// crash it is loaded as an uncertain failure that still counts and that its label fixes.
func TestPlaceholderCountsAndSurvivesACrash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.json")
	s, err := NewStore(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Hour)
	done := s.Placeholder(MessageRecord{Direction: DirectionOutgoing, Type: TypeBroadcast, Phone: "6591234567", Text: "a", Label: "pulse:c:c1:6591234567"})
	cut := s.Placeholder(MessageRecord{Direction: DirectionOutgoing, Type: TypeScheduled, Phone: "14155550600", Text: "b", Label: "pulse:s:job1"})
	failed := s.Placeholder(MessageRecord{Direction: DirectionOutgoing, Type: TypeScheduled, Phone: "14155550601", Text: "c", Label: "pulse:s:job2"})
	if n := s.SentSince(since); n != 3 {
		t.Fatalf("sends on their way must count: %d", n)
	}
	if !s.Settle(done, "wamid.OK", StatusSent, "", false) || !s.Settle(failed, "fail_1", StatusFailed, "refused", false) {
		t.Fatal("placeholders not found")
	}
	if m, ok := s.Get("wamid.OK"); !ok || m.Status != StatusSent || m.Label == "" {
		t.Fatalf("the placeholder must take the real ID: %+v", m)
	}
	if n := s.SentSince(since); n != 2 {
		t.Fatalf("a definite failure no longer counts: %d", n)
	}
	if got := s.List(10, "6591234567", ""); len(got) != 1 {
		t.Fatalf("a Singapore number must find its own messages: %+v", got)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	again, err := NewStore(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := again.Get(cut); !ok || m.Status != StatusFailed || !m.Uncertain || m.Error == "" {
		t.Fatalf("an interrupted send must load as an uncertain failure: %+v", m)
	}
	if n := again.SentSince(since); n != 2 {
		t.Fatalf("the interrupted send still counts towards the limit: %d", n)
	}
	if !again.ResolveLabel("pulse:s:job1", "wamid.LATE", StatusDelivered) {
		t.Fatal("Meta's report must correct the interrupted send")
	}
	if m, _ := again.Get("wamid.LATE"); m.Uncertain || m.Status != StatusDelivered {
		t.Fatalf("corrected: %+v", m)
	}
	// A placeholder that a delivery update corrected first keeps that correction.
	ph := again.Placeholder(MessageRecord{Direction: DirectionOutgoing, Type: TypeAutoReply, Phone: "14155550602", Text: "d", Label: "pulse:r:x"})
	again.ResolveLabel("pulse:r:x", "wamid.EARLY", StatusDelivered)
	again.Settle(ph, "wamid.EARLY", StatusSent, "", false)
	if m, _ := again.Get("wamid.EARLY"); m.Status != StatusDelivered {
		t.Fatalf("settling must not undo an earlier delivery update: %+v", m)
	}
}

func TestStatsExcludeImportedHistory(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "m.json"), 100)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for i := 0; i < 5; i++ {
		s.Record(MessageRecord{ID: "imp" + string(rune('a'+i)), Direction: DirectionIncoming, Type: TypeHistorySync, Phone: "1415", Timestamp: old})
	}
	s.Record(MessageRecord{ID: "live1", Direction: DirectionIncoming, Type: TypeUserMessage, Phone: "1415"})
	s.Record(MessageRecord{ID: "live2", Direction: DirectionOutgoing, Type: TypeAutoReply, Phone: "1415", Status: StatusSent})
	st := s.Stats(time.Time{})
	if st.Incoming != 1 || st.Outgoing != 1 || st.AutoReplies != 1 {
		t.Fatalf("imported messages counted (C16): %+v", st)
	}
	if s.ImportedCount() != 5 || s.DeleteHistorySync() != 5 || s.ImportedCount() != 0 {
		t.Fatal("imported messages not tracked or deleted")
	}
	s.Record(MessageRecord{ID: "live1", Direction: DirectionIncoming, Phone: "1415"})
	if len(s.ExportAll()) != 2 {
		t.Error("duplicate message IDs must be ignored")
	}
}

func TestStatusOnlyMovesForward(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "m.json"), 100)
	s.Record(MessageRecord{ID: "x", Direction: DirectionOutgoing, Phone: "1", Status: StatusSent})
	s.UpdateStatus("x", StatusRead)
	s.UpdateStatus("x", StatusDelivered)
	if m, _ := s.Get("x"); m.Status != StatusRead {
		t.Fatalf("status went backwards: %s", m.Status)
	}
	if s.SentSince(time.Now().Add(-time.Minute)) != 1 {
		t.Error("read messages count as sent")
	}
}

func TestQueriesAndConversations(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "m.json"), 100)
	base := time.Now().Add(-time.Hour)
	s.Record(MessageRecord{ID: "1", Direction: DirectionIncoming, Phone: "919876543210", Name: "Asha", Text: "hello", Timestamp: base})
	s.Record(MessageRecord{ID: "2", Direction: DirectionOutgoing, Type: TypeAutoReply, Phone: "919876543210", Text: "hi there", Timestamp: base.Add(time.Second)})
	s.Record(MessageRecord{ID: "3", Direction: DirectionIncoming, Phone: "14155550100", Text: "price?", Timestamp: base.Add(2 * time.Second)})
	if got := s.List(10, "9876543210", ""); len(got) != 2 || got[0].ID != "2" {
		t.Fatalf("phone filter or order wrong: %+v", got)
	}
	if got := s.List(10, "", "PRICE"); len(got) != 1 {
		t.Fatal("search should be case-insensitive")
	}
	convs := s.Conversations()
	if len(convs) != 2 || convs[0].Phone != "14155550100" || convs[1].Name != "Asha" {
		t.Fatalf("conversations wrong: %+v", convs)
	}
	read := base.Add(500 * time.Millisecond)
	if convs[1].UnreadSince(&read) != 0 || convs[1].UnreadSince(nil) != 1 {
		t.Error("unread counting wrong")
	}
	if s.AutoRepliesTo("919876543210", base.Add(-time.Minute)) != 1 {
		t.Error("auto replies per contact wrong")
	}
	if n := s.PurgeOlderThan(1); n != 0 {
		t.Error("recent messages must not be purged")
	}
}

func TestLegacyFileMigrates(t *testing.T) {
	p := filepath.Join(t.TempDir(), "messages.json")
	legacy := `[{"id":"a","direction":"incoming","type":"history_sync","timestamp":"2026-01-01T10:00:00Z","sender_phone":"919876543210","sender_name":"Asha","recipient":"Me","text":"old"},
{"id":"b","direction":"outgoing","type":"ai_reply","timestamp":"2026-01-01T10:01:00Z","sender_phone":"Me","recipient":"919876543210","text":"reply"}]`
	if err := os.WriteFile(p, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(p, 100)
	if err != nil {
		t.Fatal(err)
	}
	all := s.ExportAll()
	if len(all) != 2 || all[0].Phone != "919876543210" || !all[0].Imported || all[1].Phone != "919876543210" || all[1].Type != TypeAutoReply {
		t.Fatalf("legacy records not migrated: %+v", all)
	}
}

func TestCapKeepsNewest(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "m.json"), 3)
	for i := 0; i < 5; i++ {
		s.Record(MessageRecord{ID: string(rune('a' + i)), Direction: DirectionIncoming, Phone: "1"})
	}
	all := s.ExportAll()
	if len(all) != 3 || all[0].ID != "c" {
		t.Fatalf("cap kept the wrong messages: %+v", all)
	}
	s.UpdateStatus("e", StatusRead)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
}

func TestMarkFailedRecordsReason(t *testing.T) {
	s, err := NewStore(filepath.Join(t.TempDir(), "h.json"), 100)
	if err != nil {
		t.Fatal(err)
	}
	s.Record(MessageRecord{ID: "out1", Direction: DirectionOutgoing, Phone: "14155550001", Text: "hi", Status: StatusSent})
	s.Record(MessageRecord{ID: "in1", Direction: DirectionIncoming, Phone: "14155550001", Text: "hello"})
	if !s.MarkFailed("out1", "More than 24 hours") || s.MarkFailed("in1", "x") || s.MarkFailed("nope", "x") {
		t.Fatal("only known outgoing messages can be marked failed")
	}
	if m, _ := s.Get("out1"); m.Status != StatusFailed || m.Error != "More than 24 hours" {
		t.Fatalf("got %+v", m)
	}
	_ = s.Flush()
}
