package security

import (
	"encoding/json"
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
	if data, err := box.ReadFile(path); err == nil && len(data) > 0 {
		var ev []AuditEvent
		if json.Unmarshal(data, &ev) == nil {
			audit.events = append(ev, audit.events...)
		}
	}
	audit.saver = securestore.NewSaver(2*time.Second, audit.save)
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
