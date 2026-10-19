package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MessageDirection indicates whether a message was incoming or outgoing
type MessageDirection string

const (
	DirectionIncoming MessageDirection = "incoming"
	DirectionOutgoing MessageDirection = "outgoing"
)

// MessageType categorizes how the message was created
type MessageType string

const (
	TypeUserMessage  MessageType = "user_message"
	TypeAutoReply    MessageType = "auto_reply"
	TypeDirectSend   MessageType = "direct_send"
	TypeBroadcast    MessageType = "broadcast"
	TypeScheduled    MessageType = "scheduled"
	TypeAISmartReply MessageType = "ai_reply"
)

// MessageRecord stores full metadata and text of a chat interaction
type MessageRecord struct {
	ID            string           `json:"id"`
	Direction     MessageDirection `json:"direction"`
	Type          MessageType      `json:"type"`
	Timestamp     time.Time        `json:"timestamp"`
	SenderPhone   string           `json:"sender_phone"`
	SenderName    string           `json:"sender_name"`
	Recipient     string           `json:"recipient"`
	Text          string           `json:"text"`
	RuleTriggered string           `json:"rule_triggered,omitempty"`
	Status        string           `json:"status"` // "received", "delivered", "sent", "failed"
	Error         string           `json:"error,omitempty"`
}

// Store provides thread-safe access and persistence for message history
type Store struct {
	mu       sync.RWMutex
	filePath string
	messages []MessageRecord
	maxItems int
}

// NewStore initializes the message history store
func NewStore(filePath string, maxItems int) (*Store, error) {
	if maxItems <= 0 {
		maxItems = 1000
	}

	s := &Store{
		filePath: filePath,
		messages: make([]MessageRecord, 0),
		maxItems: maxItems,
	}

	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	return s, nil
}

// Record appends a new message record and flushes to disk
func (s *Store) Record(rec MessageRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}
	if rec.Status == "" {
		if rec.Direction == DirectionIncoming {
			rec.Status = "received"
		} else {
			rec.Status = "sent"
		}
	}

	s.messages = append(s.messages, rec)
	if len(s.messages) > s.maxItems {
		s.messages = s.messages[len(s.messages)-s.maxItems:]
	}

	_ = s.saveLocked()
}

// List returns recorded messages (newest first) matching optional phone or text filters
func (s *Store) List(limit int, phoneFilter string, search string) []MessageRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var filtered []MessageRecord
	phoneFilter = strings.TrimSpace(phoneFilter)
	search = strings.ToLower(strings.TrimSpace(search))

	// Iterate in reverse (newest first)
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]

		if phoneFilter != "" {
			if !strings.Contains(m.SenderPhone, phoneFilter) && !strings.Contains(m.Recipient, phoneFilter) {
				continue
			}
		}

		if search != "" {
			textMatch := strings.Contains(strings.ToLower(m.Text), search)
			nameMatch := strings.Contains(strings.ToLower(m.SenderName), search)
			phoneMatch := strings.Contains(m.SenderPhone, search) || strings.Contains(m.Recipient, search)
			if !textMatch && !nameMatch && !phoneMatch {
				continue
			}
		}

		filtered = append(filtered, m)
		if limit > 0 && len(filtered) >= limit {
			break
		}
	}

	return filtered
}

// TotalStats returns incoming and outgoing counts
func (s *Store) TotalStats() (incoming int, outgoing int) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, m := range s.messages {
		if m.Direction == DirectionIncoming {
			incoming++
		} else {
			outgoing++
		}
	}
	return incoming, outgoing
}

// Clear wipes all recorded messages
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.messages = make([]MessageRecord, 0)
	return s.saveLocked()
}

// ClearPhone removes all recorded messages for a specific phone number
func (s *Store) ClearPhone(phone string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	phone = strings.TrimSpace(phone)
	if phone == "" {
		return 0
	}
	cleanPhone := strings.TrimPrefix(phone, "+")

	var retained []MessageRecord
	deleted := 0

	for _, m := range s.messages {
		sClean := strings.TrimPrefix(m.SenderPhone, "+")
		rClean := strings.TrimPrefix(m.Recipient, "+")
		if strings.Contains(m.SenderPhone, phone) || strings.Contains(m.Recipient, phone) ||
			(cleanPhone != "" && (strings.Contains(sClean, cleanPhone) || strings.Contains(rClean, cleanPhone))) {
			deleted++
		} else {
			retained = append(retained, m)
		}
	}

	if deleted > 0 {
		s.messages = retained
		_ = s.saveLocked()
	}
	return deleted
}

// PurgeOlderThan removes messages older than the specified number of days
func (s *Store) PurgeOlderThan(days int) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	if days <= 0 {
		count := len(s.messages)
		s.messages = make([]MessageRecord, 0)
		_ = s.saveLocked()
		return count
	}

	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	var retained []MessageRecord
	purged := 0

	for _, m := range s.messages {
		if m.Timestamp.After(cutoff) {
			retained = append(retained, m)
		} else {
			purged++
		}
	}

	if purged > 0 {
		s.messages = retained
		_ = s.saveLocked()
	}
	return purged
}

// ExportAll returns a thread-safe copy of all history records
func (s *Store) ExportAll() []MessageRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()

	res := make([]MessageRecord, len(s.messages))
	copy(res, s.messages)
	return res
}

func (s *Store) load() error {
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
		s.messages = make([]MessageRecord, 0)
		return nil
	}
	if err := json.Unmarshal(data, &s.messages); err != nil {
		bakPath := s.filePath + ".bak"
		if bakData, bErr := os.ReadFile(bakPath); bErr == nil && len(bakData) > 0 {
			return json.Unmarshal(bakData, &s.messages)
		}
		return err
	}
	return nil
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.filePath)
	if dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0700)
	}

	data, err := json.MarshalIndent(s.messages, "", "  ")
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

// FixPhoneNumbers replaces any LIDs with their resolved real phone numbers across all stored messages
func (s *Store) FixPhoneNumbers(resolver func(string) string, nameResolver ...func(string) string) {
	if resolver == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var getName func(string) string
	if len(nameResolver) > 0 && nameResolver[0] != nil {
		getName = nameResolver[0]
	}

	isLID := func(str string) bool {
		clean := strings.TrimSpace(strings.TrimPrefix(str, "+"))
		if len(clean) < 13 || len(clean) > 18 {
			return false
		}
		for _, r := range clean {
			if r < '0' || r > '9' {
				return false
			}
		}
		return true
	}

	modified := false
	for i := range s.messages {
		if s.messages[i].SenderPhone != "" && s.messages[i].SenderPhone != "Me" && s.messages[i].SenderPhone != "CRM/Webhook" {
			realPhone := resolver(s.messages[i].SenderPhone)
			if realPhone != "" && realPhone != s.messages[i].SenderPhone {
				s.messages[i].SenderPhone = realPhone
				modified = true
			}
		}
		if s.messages[i].Recipient != "" && s.messages[i].Recipient != "Me" {
			realPhone := resolver(s.messages[i].Recipient)
			if realPhone != "" && realPhone != s.messages[i].Recipient {
				s.messages[i].Recipient = realPhone
				modified = true
			}
		}

		// Identify the counter-party phone
		contactPhone := s.messages[i].SenderPhone
		if contactPhone == "Me" || contactPhone == "" {
			contactPhone = s.messages[i].Recipient
		}

		// If SenderName is an LID or invalid, resolve to real name or formatted phone number
		if isLID(s.messages[i].SenderName) || s.messages[i].SenderName == "" || s.messages[i].SenderName == "-" {
			resolvedName := ""
			if getName != nil && contactPhone != "" && contactPhone != "Me" {
				resolvedName = getName(contactPhone)
			}
			if resolvedName != "" && !isLID(resolvedName) {
				s.messages[i].SenderName = resolvedName
				modified = true
			} else if contactPhone != "" && contactPhone != "Me" && !isLID(contactPhone) {
				cleanP := strings.TrimPrefix(contactPhone, "+")
				s.messages[i].SenderName = "+" + cleanP
				modified = true
			}
		}
	}
	if modified {
		_ = s.saveLocked()
	}
}
