package history

import (
	"os"
	"testing"
	"time"
)

func TestHistoryStore(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "messages_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	store, err := NewStore(tmpFile.Name(), 10)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Record incoming
	store.Record(MessageRecord{
		ID:          "msg_1",
		Direction:   DirectionIncoming,
		Type:        TypeUserMessage,
		SenderPhone: "14155552671",
		SenderName:  "Alice",
		Text:        "hii",
		Timestamp:   time.Now(),
	})

	// Record outgoing
	store.Record(MessageRecord{
		ID:          "msg_2",
		Direction:   DirectionOutgoing,
		Type:        TypeAutoReply,
		SenderPhone: "Me",
		Recipient:   "14155552671",
		Text:        "Hello! How are you doing?",
		Timestamp:   time.Now(),
	})

	list := store.List(10, "", "")
	if len(list) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(list))
	}

	in, out := store.TotalStats()
	if in != 1 || out != 1 {
		t.Fatalf("Expected 1 in, 1 out, got %d in, %d out", in, out)
	}

	// Test search filter
	filtered := store.List(10, "", "Hello")
	if len(filtered) != 1 || filtered[0].ID != "msg_2" {
		t.Fatalf("Expected 1 filtered result msg_2, got %d", len(filtered))
	}

	// Record another message for Bob
	store.Record(MessageRecord{
		ID:          "msg_3",
		Direction:   DirectionIncoming,
		Type:        TypeUserMessage,
		SenderPhone: "+19998887777",
		SenderName:  "Bob",
		Text:        "Hey there",
		Timestamp:   time.Now(),
	})

	if len(store.List(10, "", "")) != 3 {
		t.Fatalf("Expected 3 messages before ClearPhone, got %d", len(store.List(10, "", "")))
	}

	// Test ClearPhone for Alice
	deletedAlice := store.ClearPhone("+14155552671")
	if deletedAlice != 2 {
		t.Fatalf("Expected 2 deleted messages for Alice, got %d", deletedAlice)
	}

	remaining := store.List(10, "", "")
	if len(remaining) != 1 || remaining[0].ID != "msg_3" {
		t.Fatalf("Expected only Bob's message to remain, got %d messages", len(remaining))
	}

	// Test clear all
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if len(store.List(10, "", "")) != 0 {
		t.Fatal("Expected empty store after clear")
	}
}

func TestHistoryLIDResolution(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "messages_lid_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	store, err := NewStore(tmpFile.Name(), 10)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Record message with LID in sender_phone and sender_name
	store.Record(MessageRecord{
		ID:          "lid_msg_1",
		Direction:   DirectionIncoming,
		Type:        TypeUserMessage,
		SenderPhone: "245058100052033",
		SenderName:  "+245058100052033",
		Recipient:   "Me",
		Text:        "Hello there",
		Timestamp:   time.Now(),
	})

	// Record outgoing message with LID in recipient and sender_name
	store.Record(MessageRecord{
		ID:          "lid_msg_2",
		Direction:   DirectionOutgoing,
		Type:        TypeUserMessage,
		SenderPhone: "Me",
		SenderName:  "+245058100052033",
		Recipient:   "245058100052033",
		Text:        "Hi, how can I help?",
		Timestamp:   time.Now(),
	})

	resolver := func(target string) string {
		if target == "245058100052033" {
			return "919971731137"
		}
		return target
	}
	nameResolver := func(target string) string {
		if target == "919971731137" {
			return "Sparsh Gupta Techpros"
		}
		return ""
	}

	store.FixPhoneNumbers(resolver, nameResolver)

	msgs := store.List(10, "", "")
	if len(msgs) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(msgs))
	}

	for _, m := range msgs {
		if m.Direction == DirectionIncoming {
			if m.SenderPhone != "919971731137" {
				t.Errorf("Expected incoming SenderPhone to be 919971731137, got %s", m.SenderPhone)
			}
			if m.SenderName != "Sparsh Gupta Techpros" {
				t.Errorf("Expected incoming SenderName to be 'Sparsh Gupta Techpros', got %s", m.SenderName)
			}
		} else {
			if m.Recipient != "919971731137" {
				t.Errorf("Expected outgoing Recipient to be 919971731137, got %s", m.Recipient)
			}
			if m.SenderName != "Sparsh Gupta Techpros" {
				t.Errorf("Expected outgoing SenderName to be 'Sparsh Gupta Techpros', got %s", m.SenderName)
			}
		}
	}
}
