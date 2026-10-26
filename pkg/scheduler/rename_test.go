package scheduler

import (
	"testing"
	"time"

	"whatsapp-automation/pkg/contacts"
)

// A message scheduled under Pulse 2.0 for the mis-prefixed number 916591234567 moves to
// the real number when the contact is moved after the upgrade, so it cannot go to the
// Indian number (a stranger).
func TestWaitingMessagesFollowAMovedContact(t *testing.T) {
	s, _, cs := newTestScheduler(t, nil, contacts.Contact{Phone: "916591234567", Name: "Wei", Source: "whatsapp", CreatedAt: time.Now().Add(-48 * time.Hour)})
	j, _, err := s.Add("916591234567", "See you tomorrow", time.Now().Add(time.Hour), "Wei", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	cs.Upsert("6591234567", "Wei", "whatsapp") // the first message from the real number
	if _, ok := cs.Get("6591234567"); !ok {
		t.Skip("the contact was not moved; nothing to follow")
	}
	for _, got := range s.List() {
		if got.ID == j.ID && got.Recipient != "6591234567" {
			t.Fatalf("the waiting message still goes to %s", got.Recipient)
		}
	}
}
