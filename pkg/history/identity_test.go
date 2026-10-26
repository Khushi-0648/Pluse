package history

import (
	"path/filepath"
	"testing"
	"time"
)

// A1: a phone filter is a stored number, matched exactly. The Singapore number
// 6591234567 without messages of its own never shows India's 916591234567, which a
// typed reading (CleanPhone) would make of it.
func TestQueryStoredNumberIsExact(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "m.json"), 100)
	s.Record(MessageRecord{ID: "in1", Direction: DirectionIncoming, Phone: "916591234567", Text: "Ravi's order", Timestamp: time.Now()})
	for _, q := range []string{"6591234567", "+6591234567"} {
		if got := s.Query(Query{Phone: q}); len(got) != 0 {
			t.Fatalf("%s showed another customer's messages: %+v", q, got)
		}
	}
	if got := s.Query(Query{Phone: "916591234567"}); len(got) != 1 {
		t.Fatalf("the stored number's own messages: %+v", got)
	}
}
