package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/scheduler"
)

func TestBroadcastCampaignEndpoints(t *testing.T) {
	srv := &Server{
		cfg:              config.DefaultConfig(),
		isBroadcasting:   true,
		broadcastCurrent: 5,
		broadcastTotal:   10,
		broadcastSuccess: 4,
		broadcastFailed:  1,
		broadcastStatus:  "running",
	}

	// 1. GET /api/v1/campaigns/status
	req := httptest.NewRequest(http.MethodGet, "/api/v1/campaigns/status", nil)
	w := httptest.NewRecorder()
	srv.handleCampaignStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}
	var st map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("Failed to parse status response: %v", err)
	}
	if st["is_broadcasting"] != true || st["status"] != "running" {
		t.Fatalf("Unexpected status payload: %v", st)
	}
	if st["current"] != float64(5) || st["total"] != float64(10) {
		t.Fatalf("Expected current=5, total=10, got %v", st)
	}

	// 2. POST /api/v1/campaigns/cancel
	cReq := httptest.NewRequest(http.MethodPost, "/api/v1/campaigns/cancel", nil)
	cW := httptest.NewRecorder()
	srv.handleCancelBroadcast(cW, cReq)

	if cW.Code != http.StatusOK {
		t.Fatalf("Expected 200 on cancel, got %d", cW.Code)
	}
	if srv.isBroadcasting != false || srv.broadcastStatus != "cancelled" {
		t.Fatalf("Expected broadcast to be cancelled, got isBroadcasting=%v, status=%s", srv.isBroadcasting, srv.broadcastStatus)
	}
}

func TestScheduledRetryEndpoint(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "srv_sch_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	sch, err := scheduler.NewScheduler(tmpFile.Name(), nil, nil, nil)
	if err != nil {
		t.Fatalf("Failed to init scheduler: %v", err)
	}

	job, err := sch.Add("+14155551234", "Test reminder", time.Now().Add(10*time.Minute), "Alice")
	if err != nil {
		t.Fatalf("Failed to add scheduled job: %v", err)
	}
	if err := sch.Cancel(job.ID); err != nil {
		t.Fatalf("Failed to cancel job: %v", err)
	}

	srv := &Server{
		cfg:       config.DefaultConfig(),
		scheduler: sch,
	}

	// POST /api/v1/scheduled/{id}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scheduled/"+job.ID, nil)
	w := httptest.NewRecorder()
	srv.handleScheduledActions(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var foundJob *scheduler.ScheduledJob
	for _, j := range sch.List() {
		if j.ID == job.ID {
			foundJob = &j
			break
		}
	}
	if foundJob == nil {
		t.Fatalf("Job %s not found in scheduler", job.ID)
	}
	if foundJob.Status != scheduler.StatusPending {
		t.Fatalf("Expected retried job status to be pending, got %s", foundJob.Status)
	}
}

func TestHandleStatusMetrics(t *testing.T) {
	tmpHist, err := os.CreateTemp("", "status_hist_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpHist.Name())
	tmpHist.Close()

	hs, err := history.NewStore(tmpHist.Name(), 100)
	if err != nil {
		t.Fatal(err)
	}
	hs.Record(history.MessageRecord{
		ID:          "m1",
		Direction:   history.DirectionIncoming,
		Type:        history.TypeUserMessage,
		Text:        "Help me",
		Timestamp:   time.Now(),
		SenderPhone: "+14155551234",
	})
	hs.Record(history.MessageRecord{
		ID:          "m2",
		Direction:   history.DirectionOutgoing,
		Type:        history.TypeAutoReply,
		Text:        "Auto reply",
		Timestamp:   time.Now(),
		Recipient:   "+14155551234",
	})

	tmpContacts, err := os.CreateTemp("", "status_contacts_*.json")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpContacts.Name())
	tmpContacts.Close()

	cs, err := contacts.NewStore(tmpContacts.Name())
	if err != nil {
		t.Fatal(err)
	}
	_ = cs.Save(contacts.Contact{
		Phone:          "14155551234",
		Name:           "Test Attention",
		NeedsAttention: true,
	})

	srv := &Server{
		cfg:          config.DefaultConfig(),
		histStore:    hs,
		contactStore: cs,
		startTime:    time.Now().Add(-5 * time.Minute),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	srv.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200, got %d", w.Code)
	}

	var d map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatalf("Failed to parse status response: %v", err)
	}

	if d["attention_count"] != float64(1) {
		t.Fatalf("Expected attention_count=1, got %v", d["attention_count"])
	}
	if d["auto_replies"] != float64(1) {
		t.Fatalf("Expected auto_replies=1, got %v", d["auto_replies"])
	}
	if d["messages_in"] != float64(1) || d["messages_out"] != float64(1) {
		t.Fatalf("Expected in=1, out=1, got in=%v, out=%v", d["messages_in"], d["messages_out"])
	}
}

func TestUIModalsAndBadgesRendered(t *testing.T) {
	cfg := config.DefaultConfig()
	data := map[string]any{
		"Status":            "Connected",
		"ClientStatus":      nil,
		"Uptime":            "10m",
		"Rules":             cfg.Rules,
		"RulesCount":        len(cfg.Rules),
		"Contacts":          []any{},
		"ContactsCount":     0,
		"Tags":              []string{"VIP"},
		"Templates":         []any{},
		"TemplatesCount":    0,
		"Scheduled":         []any{},
		"ScheduledCount":    0,
		"MessagesIn":        5,
		"MessagesOut":       8,
		"IsBroadcasting":    false,
		"BusinessHours":     cfg.BusinessHours,
		"AI":                cfg.AI,
		"Automation":        cfg.Automation,
		"Webhook":           cfg.Webhook,
		"ServerPort":        8085,
		"MemUsageMB":        "15 MB",
		"Goroutines":        12,
		"IsDefaultPassword": false,
		"WhatsApp": map[string]any{
			"LoggedIn": true,
			"PushName": "Business Bot",
			"Phone":    "919876543210",
			"JID":      "919876543210@s.whatsapp.net",
		},
	}

	var buf bytes.Buffer
	err := dashboardTmpl.Execute(&buf, data)
	if err != nil {
		t.Fatalf("Failed to execute dashboard template: %v", err)
	}

	html := buf.String()
	elements := []string{
		"id=\"scheduleModal\"",
		"id=\"contactModal\"",
		"id=\"templateModal\"",
		"id=\"ruleModal\"",
		"id=\"inboxFileBadge\"",
		"id=\"bcCancelBtn\"",
		"id=\"navAttentionBadge\"",
	}

	for _, el := range elements {
		if !strings.Contains(html, el) {
			t.Errorf("Expected dashboard HTML to contain %s", el)
		}
	}
}
