package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/securestore"
	"whatsapp-automation/pkg/security"
)

type JobStatus string

const (
	StatusPending   JobStatus = "pending"
	StatusSent      JobStatus = "sent"
	StatusFailed    JobStatus = "failed"
	StatusSkipped   JobStatus = "skipped"
	StatusCancelled JobStatus = "cancelled"
	StatusSending   JobStatus = "sending"
)

// ScheduledJob is one message to send later.
type ScheduledJob struct {
	ID            string     `json:"id"`
	Recipient     string     `json:"recipient"`
	RecipientName string     `json:"recipient_name,omitempty"`
	Message       string     `json:"message"`
	ScheduledAt   time.Time  `json:"scheduled_at"`
	Status        JobStatus  `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	SentAt        *time.Time `json:"sent_at,omitempty"`
	MessageID     string     `json:"message_id,omitempty"`
	Error         string     `json:"error,omitempty"`
	CreatedBy     string     `json:"created_by,omitempty"`
	// Template is sent instead of Message when the customer has not written in the
	// 24 hours before the scheduled time.
	Template *campaign.Template `json:"template,omitempty"`
}

// Scheduler sends due messages in the background.
type Scheduler struct {
	mu       sync.Mutex
	filePath string
	box      *securestore.Box
	jobs     []ScheduledJob
	sender   campaign.Sender
	cfg      *config.Config
	contacts *contacts.Store
	hist     *history.Store
	cancel   context.CancelFunc
	sending  bool
	fails    int       // failed sends in a row
	pausedTo time.Time // sending waits until then after too many failures in a row
}

// failPause is how long scheduled sending waits after 5 failures in a row.
const failPause = 15 * time.Minute

// pace is the wait after each sent message (replaced in tests).
var pace = campaign.Pace

// NewScheduler loads jobs from filePath.
func NewScheduler(filePath string, s campaign.Sender, cfg *config.Config, cs *contacts.Store, hs *history.Store) (*Scheduler, error) {
	sc := &Scheduler{filePath: filePath, box: securestore.Default(), sender: s, cfg: cfg, contacts: cs, hist: hs}
	data, err := sc.box.ReadFile(filePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &sc.jobs); err != nil {
			return nil, fmt.Errorf("scheduled messages file %s is damaged: %w", filePath, err)
		}
	}
	for i := range sc.jobs {
		if sc.jobs[i].Status == StatusSending {
			sc.jobs[i].Status = StatusFailed
			sc.jobs[i].Error = "Pulse stopped while sending. Check the chat before retrying."
		}
	}
	return sc, nil
}

func (s *Scheduler) saveLocked() error {
	data, err := json.Marshal(s.jobs)
	if err != nil {
		return err
	}
	return s.box.WriteFile(s.filePath, data)
}

// Start begins checking for due messages every few seconds.
func (s *Scheduler) Start(ctx context.Context) {
	ctx, s.cancel = context.WithCancel(ctx)
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.processDue(ctx)
			}
		}
	}()
}

// Stop ends the background loop.
func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// Validate checks a message before it is scheduled and returns warnings about policy.
func (s *Scheduler) Validate(recipient, message string, at time.Time, tmpl *campaign.Template) (string, []string, error) {
	phone := contacts.CleanPhone(recipient)
	if len(phone) < 7 || len(phone) > 15 {
		return "", nil, errors.New("Enter a valid phone number with country code.")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return "", nil, errors.New("Write the message.")
	}
	if len(message) > 4000 {
		return "", nil, errors.New("The message is too long (4000 characters at most).")
	}
	if err := msgtemplate.Validate(message, "email", "notes"); err != nil {
		return "", nil, err
	}
	if at.Before(time.Now().Add(-time.Minute)) {
		return "", nil, errors.New("Choose a time in the future.")
	}
	if at.After(time.Now().AddDate(1, 0, 0)) {
		return "", nil, errors.New("Messages can be scheduled up to one year ahead.")
	}
	var warn []string
	if s.contacts != nil {
		if c, ok := s.contacts.Get(phone); ok {
			if c.IsDND {
				return "", nil, errors.New("This customer opted out. Messages to them are blocked.")
			}
			if !c.Consent {
				warn = append(warn, "This customer has no opt-in recorded. Unless they write to you in the 24 hours before the scheduled time, the message will be skipped.")
			} else if tmpl == nil && (c.LastInboundAt == nil || at.Sub(*c.LastInboundAt) >= 24*time.Hour) {
				warn = append(warn, "Unless this customer writes to you in the 24 hours before the scheduled time, WhatsApp only allows an approved template, and the message will be skipped. Add an official WhatsApp template to be sure it goes out.")
			}
		} else {
			warn = append(warn, "This number is not in your contacts and has no opt-in recorded. The message will only be sent if they write to you in the 24 hours before.")
		}
	}
	return phone, warn, nil
}

// Add schedules a message. tmpl is optional: it is sent instead of the message when the
// customer has not written in the 24 hours before the scheduled time.
func (s *Scheduler) Add(recipient, message string, at time.Time, name, createdBy string, tmpl *campaign.Template) (ScheduledJob, []string, error) {
	tmpl, err := tmpl.Clean()
	if err != nil {
		return ScheduledJob{}, nil, err
	}
	phone, warn, err := s.Validate(recipient, message, at, tmpl)
	if err != nil {
		return ScheduledJob{}, nil, err
	}
	job := ScheduledJob{ID: "sch_" + security.RandomToken(5), Recipient: phone, RecipientName: strings.TrimSpace(name), Message: strings.TrimSpace(message), ScheduledAt: at, Status: StatusPending, CreatedAt: time.Now(), CreatedBy: createdBy, Template: tmpl}
	s.mu.Lock()
	s.jobs = append(s.jobs, job)
	err = s.saveLocked()
	s.mu.Unlock()
	return job, warn, err
}

// List returns jobs: pending first by due time, then the rest newest first.
func (s *Scheduler) List() []ScheduledJob {
	s.mu.Lock()
	out := append(make([]ScheduledJob, 0, len(s.jobs)), s.jobs...)
	s.mu.Unlock()
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := out[i].Status == StatusPending, out[j].Status == StatusPending
		if pi != pj {
			return pi
		}
		if pi {
			return out[i].ScheduledAt.Before(out[j].ScheduledAt)
		}
		return out[i].ScheduledAt.After(out[j].ScheduledAt)
	})
	return out
}

// PendingCount returns how many messages are waiting.
func (s *Scheduler) PendingCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, j := range s.jobs {
		if j.Status == StatusPending {
			n++
		}
	}
	return n
}

// Cancel marks a pending job cancelled.
func (s *Scheduler) Cancel(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			if s.jobs[i].Status != StatusPending {
				return fmt.Errorf("This message is already %s.", s.jobs[i].Status)
			}
			s.jobs[i].Status = StatusCancelled
			return s.saveLocked()
		}
	}
	return errors.New("Scheduled message not found.")
}

// Retry sends a failed or skipped job again now.
func (s *Scheduler) Retry(id string) (ScheduledJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			if s.jobs[i].Status == StatusSent {
				return ScheduledJob{}, errors.New("This message was already sent.")
			}
			s.jobs[i].Status, s.jobs[i].ScheduledAt, s.jobs[i].Error = StatusPending, time.Now(), ""
			s.pausedTo = time.Time{} // the owner asked to try now
			return s.jobs[i], s.saveLocked()
		}
	}
	return ScheduledJob{}, errors.New("Scheduled message not found.")
}

// Delete removes a job.
func (s *Scheduler) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			s.jobs = append(s.jobs[:i], s.jobs[i+1:]...)
			return s.saveLocked()
		}
	}
	return errors.New("Scheduled message not found.")
}

// Clear removes finished jobs, or every job when all is true.
func (s *Scheduler) Clear(all bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.jobs[:0]
	for _, j := range s.jobs {
		if !all && j.Status == StatusPending {
			kept = append(kept, j)
		}
	}
	s.jobs = append([]ScheduledJob(nil), kept...)
	err := s.saveLocked()
	securestore.RemoveBackup(s.filePath)
	return err
}

func (s *Scheduler) finish(id string, st JobStatus, msgID, errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == id {
			if s.jobs[i].Status != StatusPending && s.jobs[i].Status != StatusSending {
				return // cancelled or changed while this batch was running
			}
			s.jobs[i].Status, s.jobs[i].MessageID, s.jobs[i].Error = st, msgID, errText
			if st == StatusSent {
				now := time.Now()
				s.jobs[i].SentAt = &now
			}
			_ = s.saveLocked()
			return
		}
	}
}

func (s *Scheduler) processDue(ctx context.Context) {
	s.mu.Lock()
	if s.sending {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	if now.Before(s.pausedTo) {
		s.mu.Unlock()
		return
	}
	var due []ScheduledJob
	for _, j := range s.jobs {
		if j.Status == StatusPending && !now.Before(j.ScheduledAt) {
			due = append(due, j)
		}
	}
	s.sending = len(due) > 0
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.sending = false
		s.mu.Unlock()
	}()

	// Paced like campaigns: a random delay within the configured pace, a longer pause
	// after each batch, and a stop after 5 failures in a row to protect the number.
	sent := 0
	for _, j := range due {
		if ctx.Err() != nil {
			return
		}
		if !s.claim(j.ID) {
			continue // cancelled or deleted since the batch started
		}
		cfg := s.cfg.Snapshot()
		a := cfg.Automation
		if cc, ok := s.sender.(interface{ Connected() bool }); ok && !a.DryRun && !cc.Connected() {
			s.unclaim(j.ID) // WhatsApp is offline: keep the job and try on the next tick
			return
		}
		name, vars := j.RecipientName, map[string]string{}
		var ct *contacts.Contact
		if s.contacts != nil {
			if c, ok := s.contacts.Get(j.Recipient); ok {
				ct = c
				if name == "" {
					name = c.Name
				}
				vars["email"], vars["notes"] = c.Email, c.Notes
			}
		}
		switch {
		case ct != nil && ct.IsDND:
			s.finish(j.ID, StatusSkipped, "", "Not sent: the customer opted out.")
			continue
		case inList(a.BlacklistPhones, j.Recipient):
			s.finish(j.ID, StatusSkipped, "", "Not sent: the number is on the blocked list.")
			continue
		case a.AllowListOnly && !inList(a.AllowList, j.Recipient):
			s.finish(j.ID, StatusSkipped, "", "Not sent: test mode is on and this is not one of the test numbers.")
			continue
		// "Require opt-in" applies as for messages sent from the dashboard: without a
		// recorded opt-in, only customers who wrote in the last 24 hours can be messaged.
		case (a.Enforce24hWindow || a.RequireOptIn) && (ct == nil || (!ct.Consent && !ct.InServiceWindow(time.Now()))):
			s.finish(j.ID, StatusSkipped, "", "Not sent: no opt-in recorded and the customer has not written in the last 24 hours.")
			continue
		}
		releaseDaily, ok := campaign.ReserveDaily(cfg, s.hist, time.Now())
		if !ok {
			s.unclaim(j.ID) // try again after midnight
			continue
		}
		mv := msgtemplate.Vars{Name: name, Phone: j.Recipient, Business: cfg.Business, Extra: vars}
		text, missing := msgtemplate.Render(j.Message, mv)
		// Outside the 24-hour window WhatsApp only delivers an approved template.
		useTemplate := campaign.NeedsTemplate(ct, time.Now())
		var params []string
		if useTemplate {
			if j.Template == nil {
				releaseDaily()
				s.finish(j.ID, StatusSkipped, "", "Not sent: "+campaign.OutsideWindowMessage)
				continue
			}
			params, missing = j.Template.Render(mv)
			text = j.Template.Describe(params)
		}
		if len(missing) > 0 {
			releaseDaily()
			s.finish(j.ID, StatusFailed, "", msgtemplate.MissingError(missing).Error())
			continue
		}
		rec := history.MessageRecord{Direction: history.DirectionOutgoing, Type: history.TypeScheduled, Phone: j.Recipient, Text: text, SentBy: j.CreatedBy}
		if a.DryRun {
			rec.ID, rec.Status = "dry_"+security.RandomToken(6), history.StatusDryRun
			s.finish(j.ID, StatusSkipped, "", "Practice mode: shown in the inbox but not sent.")
		} else {
			sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
			var id string
			var err error
			if useTemplate {
				id, err = campaign.SendTemplate(sctx, s.sender, j.Recipient, *j.Template, params)
			} else {
				id, err = s.sender.SendTextMessage(sctx, j.Recipient, text)
			}
			cancel()
			if cloudapi.IsNotConnected(err) {
				releaseDaily()
				s.unclaim(j.ID) // connection dropped: retry on the next tick instead of failing
				return
			}
			if err != nil {
				rec.ID, rec.Status, rec.Error = "fail_"+security.RandomToken(6), history.StatusFailed, err.Error()
				s.finish(j.ID, StatusFailed, "", err.Error())
				s.mu.Lock()
				s.fails++
				s.mu.Unlock()
			} else {
				rec.ID, rec.Status = id, history.StatusSent
				s.finish(j.ID, StatusSent, id, "")
				if s.contacts != nil {
					s.contacts.UpdateLastContacted(j.Recipient)
				}
				s.mu.Lock()
				s.fails = 0
				s.mu.Unlock()
				sent++
			}
		}
		if s.hist != nil {
			s.hist.Record(rec)
		}
		releaseDaily() // the history counts it from here on
		if a.DryRun {
			continue // nothing was sent, no need to wait
		}
		s.mu.Lock()
		stop := s.fails >= 5
		if stop {
			s.fails, s.pausedTo = 0, time.Now().Add(failPause)
		}
		s.mu.Unlock()
		if stop {
			// The rest stay pending and are tried again after the pause.
			security.LogAudit("SCHEDULE_PAUSED", "local", "system", fmt.Sprintf("Scheduled messages paused for %d minutes after 5 failed messages in a row, to protect the number. Check the WhatsApp connection.", int(failPause.Minutes())), "warning")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(pace(a, sent)):
		}
	}
}

func inList(list []string, phone string) bool {
	for _, p := range list {
		if contacts.CleanPhone(p) == phone {
			return true
		}
	}
	return false
}

func (s *Scheduler) unclaim(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == id && s.jobs[i].Status == StatusSending {
			s.jobs[i].Status = StatusPending
			_ = s.saveLocked()
		}
	}
}

// claim marks a pending job as being sent; it fails if the job was cancelled or removed.
func (s *Scheduler) claim(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.jobs {
		if s.jobs[i].ID == id && s.jobs[i].Status == StatusPending {
			s.jobs[i].Status = StatusSending
			_ = s.saveLocked()
			return true
		}
	}
	return false
}
