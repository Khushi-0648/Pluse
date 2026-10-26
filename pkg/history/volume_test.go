package history

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestVolume checks the dashboard queries stay fast with 50,000 stored messages (D7).
func TestVolume(t *testing.T) {
	if testing.Short() {
		t.Skip("volume test")
	}
	s, _ := NewStore(filepath.Join(t.TempDir(), "m.json"), 50000)
	start := time.Now().Add(-20 * 24 * time.Hour)
	for i := 0; i < 50000; i++ {
		dir := DirectionIncoming
		if i%2 == 1 {
			dir = DirectionOutgoing
		}
		s.Record(MessageRecord{ID: fmt.Sprint("m", i), Direction: dir, Type: TypeAutoReply, Phone: fmt.Sprint(int64(919800000000) + int64(i%2000)), Text: "hello there", Timestamp: start.Add(time.Duration(i) * time.Minute / 2)})
	}
	t0 := time.Now()
	convs := s.Conversations()
	st := s.Stats(time.Now().AddDate(0, 0, -7))
	_ = s.Query(Query{Search: "there", Limit: 2000})
	_ = s.SentSince(time.Now().Add(-24 * time.Hour))
	if d := time.Since(t0); d > 2*time.Second {
		t.Fatalf("dashboard queries too slow at 50k messages: %v", d)
	}
	if len(convs) != 2000 || st.Incoming == 0 {
		t.Fatalf("unexpected results: %d conversations", len(convs))
	}
	t1 := time.Now()
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(t1); d > 5*time.Second {
		t.Fatalf("saving 50k messages too slow: %v", d)
	}
}
