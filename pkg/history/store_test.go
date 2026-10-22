package history

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
