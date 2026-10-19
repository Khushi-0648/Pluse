package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/bot"
	"whatsapp-automation/pkg/client"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
)

// JobStatus describes the current lifecycle state of a scheduled message
type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusSent      JobStatus = "sent"
	StatusFailed    JobStatus = "failed"
	StatusCancelled JobStatus = "cancelled"
)

// ScheduledJob stores a single scheduled message task
type ScheduledJob struct {
	ID            string    `json:"id"`
	Recipient     string    `json:"recipient"`
	RecipientName string    `json:"recipient_name,omitempty"`
	Message       string    `json:"message"`
	ScheduledAt   time.Time `json:"scheduled_at"`
	Status        JobStatus `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
	SentAt        time.Time `json:"sent_at,omitempty"`
	MessageID     string    `json:"message_id,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// Scheduler manages scheduled messages in background
type Scheduler struct {
	mu           sync.RWMutex
	filePath     string
	jobs         []ScheduledJob
	client       *client.WhatsAppClient
	contactStore *contacts.Store
	histStore    *history.Store
	cancelFn     context.CancelFunc
	ticker       *time.Ticker
}

// NewScheduler creates a new message scheduler instance
func NewScheduler(filePath string, c *client.WhatsAppClient, cs *contacts.Store, hs *history.Store) (*Scheduler, error) {
	s := &Scheduler{
		filePath:     filePath,
		jobs:         make([]ScheduledJob, 0),
		client:       c,
		contactStore: cs,
		histStore:    hs,
	}

	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return s, nil
}

// Start launches the background polling loop
func (s *Scheduler) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancelFn = cancel
	s.ticker = time.NewTicker(5 * time.Second)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.ticker.C:
				s.processPendingJobs(ctx)
			}
		}
	}()
}

// Stop terminates the scheduler background worker
func (s *Scheduler) Stop() {
	if s.cancelFn != nil {
		s.cancelFn()
	}
	if s.ticker != nil {
		s.ticker.Stop()
	}
}

// Add creates a new scheduled message
func (s *Scheduler) Add(recipient, message string, scheduledAt time.Time, recipientName ...string) (*ScheduledJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	recipient = contacts.CleanPhone(recipient)
	message = strings.TrimSpace(message)

	if recipient == "" {
		return nil, errors.New("recipient phone is required")
	}
	if message == "" {
		return nil, errors.New("message text is required")
	}
	// Allow 2-minute grace period for slight clock offsets
	if scheduledAt.Before(time.Now().Add(-2 * time.Minute)) {
		return nil, errors.New("scheduled time must be in the future")
	}

	name := ""
	if len(recipientName) > 0 {
		name = strings.TrimSpace(recipientName[0])
	}

	idBytes := make([]byte, 4)
	_, _ = rand.Read(idBytes)
	id := fmt.Sprintf("sch_%s", hex.EncodeToString(idBytes))

	job := ScheduledJob{
		ID:            id,
		Recipient:     recipient,
		RecipientName: name,
		Message:       message,
		ScheduledAt:   scheduledAt,
		Status:        StatusPending,
		CreatedAt:     time.Now(),
	}

	s.jobs = append(s.jobs, job)
	_ = s.saveLocked()

	return &job, nil
}

// List returns all scheduled jobs, newest first
func (s *Scheduler) List() []ScheduledJob {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]ScheduledJob, len(s.jobs))
	for i, j := 0, len(s.jobs)-1; j >= 0; i, j = i+1, j-1 {
		result[i] = s.jobs[j]
	}
	return result
}

// Cancel marks a pending scheduled job as cancelled
func (s *Scheduler) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.jobs {
		if s.jobs[i].ID == id {
			if s.jobs[i].Status != StatusPending {
				return fmt.Errorf("job is already %s", s.jobs[i].Status)
			}
			s.jobs[i].Status = StatusCancelled
			return s.saveLocked()
		}
	}

	return errors.New("job not found")
}

// Retry resets a failed or cancelled job to pending for immediate dispatch
func (s *Scheduler) Retry(id string) (*ScheduledJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i := range s.jobs {
		if s.jobs[i].ID == id {
			s.jobs[i].Status = StatusPending
			s.jobs[i].ScheduledAt = time.Now()
			s.jobs[i].Error = ""
			_ = s.saveLocked()
			return &s.jobs[i], nil
		}
	}
	return nil, errors.New("job not found")
}

// Delete permanently removes a scheduled job by ID
func (s *Scheduler) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	found := false
	remaining := make([]ScheduledJob, 0)
	for _, j := range s.jobs {
		if j.ID == id {
			found = true
			continue
		}
		remaining = append(remaining, j)
	}
	if !found {
		return errors.New("job not found")
	}
	s.jobs = remaining
	return s.saveLocked()
}

// Clear removes scheduled tasks. If onlyProcessed is true, it preserves pending tasks and deletes sent/failed/cancelled.
func (s *Scheduler) Clear(onlyProcessed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if onlyProcessed {
		remaining := make([]ScheduledJob, 0)
		for _, j := range s.jobs {
			if j.Status == StatusPending {
				remaining = append(remaining, j)
			}
		}
		s.jobs = remaining
	} else {
		s.jobs = make([]ScheduledJob, 0)
	}

	return s.saveLocked()
}

// processPendingJobs checks for due tasks and dispatches them
func (s *Scheduler) processPendingJobs(ctx context.Context) {
	s.mu.Lock()
	now := time.Now()
	type task struct {
		id            string
		recipient     string
		recipientName string
		message       string
	}
	var due []task

	for i := range s.jobs {
		if s.jobs[i].Status == StatusPending && !now.Before(s.jobs[i].ScheduledAt) {
			due = append(due, task{
				id:            s.jobs[i].ID,
				recipient:     s.jobs[i].Recipient,
				recipientName: s.jobs[i].RecipientName,
				message:       s.jobs[i].Message,
			})
		}
	}
	s.mu.Unlock()

	if len(due) == 0 {
		return
	}

	for _, t := range due {
		// Resolve recipient name: from job or CRM contacts store
		contactName := t.recipientName
		if s.contactStore != nil {
			if c, found := s.contactStore.Get(t.recipient); found {
				if c.IsDND {
					s.mu.Lock()
					for i := range s.jobs {
						if s.jobs[i].ID == t.id {
							s.jobs[i].Status = StatusCancelled
							s.jobs[i].Error = "Skipped: recipient opted out (DND active)"
							break
						}
					}
					_ = s.saveLocked()
					s.mu.Unlock()
					continue
				}
				if contactName == "" {
					contactName = c.Name
				}
			}
		}

		// Interpolate all dynamic template tokens ({{name}}, {{first_name}}, {{phone}}, {{greeting}}, {{date}}, {{time}}, etc.)
		renderedMsg := bot.RenderTemplate(t.message, contactName, t.recipient, t.message)

		msgID, err := s.client.SendTextMessage(ctx, t.recipient, renderedMsg)

		s.mu.Lock()
		for i := range s.jobs {
			if s.jobs[i].ID == t.id {
				if err != nil {
					s.jobs[i].Status = StatusFailed
					s.jobs[i].Error = err.Error()
					fmt.Printf("[Scheduler] Job %s failed: %v\n", t.id, err)
				} else {
					s.jobs[i].Status = StatusSent
					s.jobs[i].SentAt = time.Now()
					s.jobs[i].MessageID = msgID
					s.jobs[i].Error = ""
					fmt.Printf("[Scheduler] Job %s successfully sent to %s (MsgID: %s)\n", t.id, t.recipient, msgID)
				}
				break
			}
		}
		_ = s.saveLocked()
		s.mu.Unlock()

		if s.histStore != nil {
			status := "sent"
			errMsg := ""
			if err != nil {
				status = "failed"
				errMsg = err.Error()
			}
			s.histStore.Record(history.MessageRecord{
				ID:          msgID,
				Direction:   history.DirectionOutgoing,
				Type:        history.TypeScheduled,
				Timestamp:   time.Now(),
				SenderPhone: "Me",
				Recipient:   t.recipient,
				SenderName:  "Scheduled Reminder",
				Text:        renderedMsg,
				Status:      status,
				Error:       errMsg,
			})
		}
	}
}

func (s *Scheduler) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil || len(strings.TrimSpace(string(data))) == 0 {
		// Attempt recovery from backup if main file is missing or empty (D15)
		bakPath := s.filePath + ".bak"
		if bakData, bErr := os.ReadFile(bakPath); bErr == nil && len(strings.TrimSpace(string(bakData))) > 0 {
			data = bakData
		} else if err != nil {
			return err
		}
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		s.jobs = make([]ScheduledJob, 0)
		return nil
	}
	if err := json.Unmarshal(data, &s.jobs); err != nil {
		bakPath := s.filePath + ".bak"
		if bakData, bErr := os.ReadFile(bakPath); bErr == nil && len(bakData) > 0 {
			return json.Unmarshal(bakData, &s.jobs)
		}
		return err
	}
	return nil
}

func (s *Scheduler) saveLocked() error {
	dir := filepath.Dir(s.filePath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0700)
	}

	data, err := json.MarshalIndent(s.jobs, "", "  ")
	if err != nil {
		return err
	}

	// Create backup of existing file if present (D15)
	if _, err := os.Stat(s.filePath); err == nil {
		bakPath := s.filePath + ".bak"
		if existing, rErr := os.ReadFile(s.filePath); rErr == nil && len(existing) > 0 {
			_ = os.WriteFile(bakPath, existing, 0600)
		}
	}

	// Atomic write via .tmp file with strict 0600 permissions (S9)
	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return os.WriteFile(s.filePath, data, 0600)
	}

	_ = os.Remove(s.filePath)
	if err := os.Rename(tmpFile, s.filePath); err != nil {
		return os.WriteFile(s.filePath, data, 0600)
	}
	return nil
}
