package security

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/securestore"
)

// AuditEvent is one administrative or security-relevant action.
type AuditEvent struct {
	Timestamp time.Time `json:"timestamp"`
	Action    string    `json:"action"`
	IP        string    `json:"ip"`
	User      string    `json:"user"`
	Details   string    `json:"details"`
	Status    string    `json:"status"`
}

const auditMax = 2000

type auditLogger struct {
	mu     sync.RWMutex
	events []AuditEvent
	path   string
	box    *securestore.Box
	saver  *securestore.Saver
}

var audit = &auditLogger{}

// OpenAuditLog loads persisted audit events from path and keeps them saved there.
func OpenAuditLog(path string, box *securestore.Box) {
	audit.mu.Lock()
	defer audit.mu.Unlock()
	audit.path, audit.box = path, box
	data, err := box.ReadFile(path) // falls back to audit.json.bak by itself
	if err == nil && len(data) > 0 {
		var ev []AuditEvent
		if err = json.Unmarshal(data, &ev); err != nil {
			if bak, bErr := box.ReadBackup(path); bErr == nil && json.Unmarshal(bak, &ev) == nil {
				err = nil
			}
		}
		if err == nil {
			audit.events = append(ev, audit.events...)
		}
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		setAsideUnreadable(path, err)
	}
	audit.saver = securestore.NewSaver(2*time.Second, audit.save)
}

// setAsideUnreadable moves an activity log that neither it nor its backup could be read
// (for example a damaged file, or one from another Windows account) out of the way, so
// the next save does not overwrite the only copy of its entries.
func setAsideUnreadable(path string, why error) {
	stamp := time.Now().Format("20060102-150405")
	for _, p := range []string{path, path + ".bak"} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		aside := fmt.Sprintf("%s.unreadable-%s", p, stamp)
		if err := os.Rename(p, aside); err == nil {
			fmt.Printf("[Activity log] %s could not be read (%v); it was kept as %s and a new log was started.\n", filepath.Base(p), why, aside)
		} else {
			fmt.Printf("[Activity log] %s could not be read (%v) and could not be moved aside: %v\n", filepath.Base(p), why, err)
		}
	}
}

func (a *auditLogger) save() error {
	a.mu.RLock()
	data, err := json.Marshal(a.events)
	path, box := a.path, a.box
	a.mu.RUnlock()
	if err != nil || path == "" {
		return err
	}
	return box.WriteFile(path, data)
}

// FlushAuditLog writes pending audit events to disk.
func FlushAuditLog() {
	audit.mu.RLock()
	s := audit.saver
	audit.mu.RUnlock()
	if s != nil {
		_ = s.Flush()
	}
}

// LogAudit records an action in the audit trail.
func LogAudit(action, ip, user, details, status string) {
	audit.mu.Lock()
	audit.events = append(audit.events, AuditEvent{Timestamp: time.Now(), Action: action, IP: ip, User: user, Details: details, Status: status})
	if len(audit.events) > auditMax {
		audit.events = audit.events[len(audit.events)-auditMax:]
	}
	s := audit.saver
	audit.mu.Unlock()
	if s != nil {
		s.Trigger()
	}
}

// MaskPhone shows only the last four digits of a number, for records kept after erasure.
func MaskPhone(phone string) string {
	if len(phone) <= 4 {
		return "+••••"
	}
	return "+••••" + phone[len(phone)-4:]
}

// RedactAudit removes a customer from the activity log after an erasure request: their
// number is masked and, where the entry was about them, their name is removed. The
// entries stay, so the log still shows what happened. It returns how many changed.
func RedactAudit(phone string) int {
	if len(phone) < 7 {
		return 0
	}
	masked := MaskPhone(phone)
	audit.mu.Lock()
	n := 0
	for i := range audit.events {
		e := &audit.events[i]
		hit := false
		if e.IP == "+"+phone || e.IP == phone {
			e.IP, hit = masked, true
			if e.User != "" && e.User != "system" && e.User != "local" {
				e.User = "customer" // the customer's own name, recorded on opt-outs and hand-overs
			}
		}
		if strings.Contains(e.Details, phone) {
			e.Details, hit = strings.ReplaceAll(strings.ReplaceAll(e.Details, "+"+phone, masked), phone, masked), true
		}
		if hit {
			n++
		}
	}
	audit.mu.Unlock()
	if n > 0 {
		saveRedacted()
	}
	return n
}

// saveRedacted writes the masked log at once and then deletes audit.json.bak: the save
// copies the previous, unmasked file there, which would keep the erased numbers.
func saveRedacted() {
	audit.mu.RLock()
	s, path := audit.saver, audit.path
	audit.mu.RUnlock()
	if s == nil {
		return
	}
	s.Trigger()
	if err := s.Flush(); err != nil {
		fmt.Printf("[Activity log] The masked log could not be saved yet (%v); retrying.\n", err)
		return
	}
	if path != "" {
		securestore.RemoveBackup(path)
	}
}

var customerNumber = regexp.MustCompile(`\+?\d{7,15}`)

// RedactAllCustomers masks every customer number in the activity log, for "Delete
// everything". Entries about a customer also lose the customer's name. It returns how
// many entries changed.
func RedactAllCustomers() int {
	audit.mu.Lock()
	n := 0
	for i := range audit.events {
		e := &audit.events[i]
		hit := false
		if p := strings.TrimPrefix(e.IP, "+"); net.ParseIP(e.IP) == nil && customerNumber.MatchString(e.IP) {
			e.IP, hit = MaskPhone(customerNumber.FindString(p)), true
			if e.User != "" && e.User != "system" && e.User != "local" {
				e.User = "customer"
			}
		}
		if d := customerNumber.ReplaceAllStringFunc(e.Details, func(m string) string { return MaskPhone(strings.TrimPrefix(m, "+")) }); d != e.Details {
			e.Details, hit = d, true
		}
		if hit {
			n++
		}
	}
	audit.mu.Unlock()
	if n > 0 {
		saveRedacted()
	}
	return n
}

// GetAuditLog returns up to limit events, newest first. limit <= 0 returns all.
func GetAuditLog(limit int) []AuditEvent {
	return QueryAuditLog(limit, "", time.Time{})
}

// QueryAuditLog returns events newest first, optionally filtered by action prefix and start time.
func QueryAuditLog(limit int, actionPrefix string, since time.Time) []AuditEvent {
	audit.mu.RLock()
	defer audit.mu.RUnlock()
	out := make([]AuditEvent, 0)
	for i := len(audit.events) - 1; i >= 0; i-- {
		e := audit.events[i]
		if !since.IsZero() && e.Timestamp.Before(since) {
			break
		}
		if actionPrefix != "" && !strings.HasPrefix(e.Action, actionPrefix) {
			continue
		}
		out = append(out, e)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// CountAudit counts events with the given action since t.
func CountAudit(action string, since time.Time) int {
	audit.mu.RLock()
	defer audit.mu.RUnlock()
	n := 0
	for i := len(audit.events) - 1; i >= 0; i-- {
		e := audit.events[i]
		if e.Timestamp.Before(since) {
			break
		}
		if e.Action == action {
			n++
		}
	}
	return n
}
