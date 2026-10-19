package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/history"
)

func TestHandleMessagesGetAndClear(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "srv_messages_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	hs, err := history.NewStore(tmpFile.Name(), 100)
	if err != nil {
		t.Fatalf("Failed to create history store: %v", err)
	}

	// Insert test records
	hs.Record(history.MessageRecord{
		ID:          "m1",
		Direction:   history.DirectionIncoming,
		Type:        history.TypeUserMessage,
		SenderPhone: "+14155551111",
		SenderName:  "Charlie",
		Text:        "First msg",
		Timestamp:   time.Now(),
	})
	hs.Record(history.MessageRecord{
		ID:          "m2",
		Direction:   history.DirectionOutgoing,
		Type:        history.TypeUserMessage,
		Recipient:   "+14155551111",
		Text:        "Reply to Charlie",
		Timestamp:   time.Now(),
	})
	hs.Record(history.MessageRecord{
		ID:          "m3",
		Direction:   history.DirectionIncoming,
		Type:        history.TypeUserMessage,
		SenderPhone: "+14155552222",
		SenderName:  "David",
		Text:        "Hello from David",
		Timestamp:   time.Now(),
	})

	srv := &Server{
		cfg:       config.DefaultConfig(),
		histStore: hs,
	}

	// 1. GET /api/v1/messages
	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages", nil)
	w := httptest.NewRecorder()
	srv.handleMessages(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/messages failed with code %d", w.Code)
	}
	var msgs []history.MessageRecord
	if err := json.Unmarshal(w.Body.Bytes(), &msgs); err != nil {
		t.Fatalf("Failed to parse json: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("Expected 3 messages, got %d", len(msgs))
	}

	// 2. DELETE /api/v1/messages?phone=+14155551111 (Clear chat with Charlie)
	delReq := httptest.NewRequest(http.MethodDelete, "/api/v1/messages?phone=%2B14155551111", nil)
	delW := httptest.NewRecorder()
	srv.handleMessages(delW, delReq)

	if delW.Code != http.StatusOK {
		t.Fatalf("DELETE with phone failed with code %d", delW.Code)
	}
	var delResp map[string]any
	if err := json.Unmarshal(delW.Body.Bytes(), &delResp); err != nil {
		t.Fatalf("Failed to parse delete response: %v", err)
	}
	if delResp["deleted"] != float64(2) {
		t.Fatalf("Expected 2 messages deleted, got %v", delResp["deleted"])
	}

	// Verify only David remains
	remaining := hs.List(10, "", "")
	if len(remaining) != 1 || remaining[0].ID != "m3" {
		t.Fatalf("Expected only David's message remaining, got %d messages", len(remaining))
	}

	// 3. DELETE /api/v1/messages (Clear entire inbox)
	delAllReq := httptest.NewRequest(http.MethodDelete, "/api/v1/messages", nil)
	delAllW := httptest.NewRecorder()
	srv.handleMessages(delAllW, delAllReq)

	if delAllW.Code != http.StatusOK {
		t.Fatalf("DELETE all failed with code %d", delAllW.Code)
	}

	// Verify inbox is completely empty
	if len(hs.List(10, "", "")) != 0 {
		t.Fatalf("Expected 0 messages remaining after full clear")
	}
}
