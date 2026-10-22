package history

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/securestore"
)

type MessageDirection string

const (
	DirectionIncoming MessageDirection = "incoming"
	DirectionOutgoing MessageDirection = "outgoing"
)

type MessageType string

const (
	TypeUserMessage MessageType = "user_message"
	TypeAutoReply   MessageType = "auto_reply"
	TypeDirectSend  MessageType = "direct_send"
	TypeBroadcast   MessageType = "broadcast"
	TypeScheduled   MessageType = "scheduled"
	TypeWebhook     MessageType = "webhook"
	TypeHistorySync MessageType = "history_sync"
)

// Status values, ordered by progress.
const (
	StatusReceived  = "received"
	StatusDryRun    = "dry_run"
	StatusFailed    = "failed"
	StatusSent      = "sent"
	StatusDelivered = "delivered"
	StatusRead      = "read"
)

var statusRank = map[string]int{StatusSent: 1, StatusDelivered: 2, StatusRead: 3}

// MessageRecord is one stored message. Phone is always the customer's number.
type MessageRecord struct {
	ID            string           `json:"id"`
	Direction     MessageDirection `json:"direction"`
	Type          MessageType      `json:"type"`
	Timestamp     time.Time        `json:"timestamp"`
	Phone         string           `json:"phone"`
	Name          string           `json:"name,omitempty"`
	Text          string           `json:"text"`
	Media         string           `json:"media,omitempty"`
	RuleTriggered string           `json:"rule_triggered,omitempty"`
	CampaignID    string           `json:"campaign_id,omitempty"`
	Status        string           `json:"status"`
	Error         string           `json:"error,omitempty"`
	Imported      bool             `json:"imported,omitempty"`
	SentBy        string           `json:"sent_by,omitempty"`
}

// legacyRecord reads files written by versions before 1.4.
type legacyRecord struct {
	MessageRecord
	SenderPhone string `json:"sender_phone"`
	SenderName  string `json:"sender_name"`
	Recipient   string `json:"recipient"`
}

// Store keeps message history in memory and persists it, encrypted, in the background.
type Store struct {
	mu       sync.RWMutex
	filePath string
	box      *securestore.Box
	messages []MessageRecord
	byID     map[string]int
	maxItems int
	saver    *securestore.Saver
	onRecord []func(MessageRecord)
}

// NewStore loads history from filePath. maxItems caps the number of stored messages.
func NewStore(filePath string, maxItems int) (*Store, error) {
	if maxItems <= 0 {
		maxItems = 50000
	}
	s := &Store{filePath: filePath, box: securestore.Default(), maxItems: maxItems, byID: map[string]int{}}
	s.saver = securestore.NewSaver(1500*time.Millisecond, s.persist)
	data, err := s.box.ReadFile(filePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cannot read message history %s: %w", filePath, err)
	}
	if len(strings.TrimSpace(string(data))) > 0 {
		var raw []legacyRecord
		if err := json.Unmarshal(data, &raw); err != nil {
			bak, bErr := s.box.ReadBackup(filePath)
			if bErr != nil || json.Unmarshal(bak, &raw) != nil {
				return nil, fmt.Errorf("message history %s is damaged and no usable backup was found: %w", filePath, err)
			}
		}
		for _, lr := range raw {
			s.messages = append(s.messages, migrate(lr))
		}
		sort.SliceStable(s.messages, func(i, j int) bool { return s.messages[i].Timestamp.Before(s.messages[j].Timestamp) })
		s.trimLocked()
		s.reindexLocked()
	}
	return s, nil
}

func migrate(lr legacyRecord) MessageRecord {
	m := lr.MessageRecord
	if m.Phone == "" {
		if m.Direction == DirectionIncoming {
			m.Phone = lr.SenderPhone
		} else {
			m.Phone = lr.Recipient
		}
	}
	m.Phone = digits(m.Phone)
	if m.Name == "" && m.Direction == DirectionIncoming {
		m.Name = lr.SenderName
	}
	if m.Type == TypeHistorySync {
		m.Imported = true
	}
	if m.Type == "ai_reply" {
		m.Type = TypeAutoReply
	}
	if m.Status == "" {
		if m.Direction == DirectionIncoming {
			m.Status = StatusReceived
		} else {
			m.Status = StatusSent
		}
	}
	return m
}

func digits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// OnRecord registers a callback invoked after each new message is stored.
func (s *Store) OnRecord(fn func(MessageRecord)) {
	s.mu.Lock()
	s.onRecord = append(s.onRecord, fn)
	s.mu.Unlock()
}

// SetMaxItems changes the size cap.
func (s *Store) SetMaxItems(n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	s.maxItems = n
	trimmed := s.trimLocked()
	if trimmed {
		s.reindexLocked()
	}
	s.mu.Unlock()
	if trimmed {
		s.saver.Trigger()
	}
}

func (s *Store) trimLocked() bool {
	if len(s.messages) <= s.maxItems {
		return false
	}
	s.messages = append([]MessageRecord(nil), s.messages[len(s.messages)-s.maxItems:]...)
	return true
}

func (s *Store) reindexLocked() {
	s.byID = make(map[string]int, len(s.messages))
	for i, m := range s.messages {
		if m.ID != "" {
			s.byID[m.ID] = i
		}
	}
}

// Flush writes pending changes to disk.
func (s *Store) Flush() error { return s.saver.Flush() }

func (s *Store) persist() error {
	s.mu.RLock()
	data, err := json.Marshal(s.messages)
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	return s.box.WriteFile(s.filePath, data)
}

// Record stores a message. Records with an ID that is already stored are ignored.
func (s *Store) Record(rec MessageRecord) {
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now()
	}
	rec.Phone = digits(rec.Phone)
	if rec.Status == "" {
		if rec.Direction == DirectionIncoming {
			rec.Status = StatusReceived
		} else {
			rec.Status = StatusSent
		}
	}
	if rec.Type == TypeHistorySync {
		rec.Imported = true
	}
	s.mu.Lock()
	if rec.ID != "" {
		if _, dup := s.byID[rec.ID]; dup {
			s.mu.Unlock()
			return
		}
	}
	n := len(s.messages)
	if n > 0 && rec.Timestamp.Before(s.messages[n-1].Timestamp) && rec.Imported {
		i := sort.Search(n, func(i int) bool { return s.messages[i].Timestamp.After(rec.Timestamp) })
		s.messages = append(s.messages, MessageRecord{})
		copy(s.messages[i+1:], s.messages[i:])
		s.messages[i] = rec
		s.trimLocked()
		s.reindexLocked()
	} else {
		s.messages = append(s.messages, rec)
		if s.trimLocked() {
			s.reindexLocked()
		} else if rec.ID != "" {
			s.byID[rec.ID] = len(s.messages) - 1
		}
	}
	hooks := append([]func(MessageRecord){}, s.onRecord...)
	s.mu.Unlock()
	s.saver.Trigger()
	for _, h := range hooks {
		h(rec)
	}
}

// UpdateStatus advances a sent message to delivered or read. It returns the updated record.
func (s *Store) UpdateStatus(id, status string) (MessageRecord, bool) {
	s.mu.Lock()
	i, ok := s.byID[id]
	if !ok || i >= len(s.messages) || s.messages[i].ID != id {
		s.mu.Unlock()
		return MessageRecord{}, false
	}
	m := &s.messages[i]
	if m.Direction != DirectionOutgoing || statusRank[status] <= statusRank[m.Status] {
		rec := *m
		s.mu.Unlock()
		return rec, false
	}
	m.Status = status
	rec := *m
	s.mu.Unlock()
	s.saver.Trigger()
	return rec, true
}

// MarkFailed records that WhatsApp could not deliver a sent message, with the reason.
func (s *Store) MarkFailed(id, reason string) bool {
	s.mu.Lock()
	i, ok := s.byID[id]
	if !ok || i >= len(s.messages) || s.messages[i].ID != id || s.messages[i].Direction != DirectionOutgoing {
		s.mu.Unlock()
		return false
	}
	s.messages[i].Status, s.messages[i].Error = StatusFailed, reason
	s.mu.Unlock()
	s.saver.Trigger()
	return true
}

// Get returns a message by ID.
func (s *Store) Get(id string) (MessageRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if i, ok := s.byID[id]; ok && i < len(s.messages) {
		return s.messages[i], true
	}
	return MessageRecord{}, false
}

// normPhone applies the same rules as contact numbers (a bare 10-digit Indian mobile gets 91).
func normPhone(p string) string {
	if strings.TrimSpace(p) == "" {
		return ""
	}
	return contacts.CleanPhone(p)
}

func phoneMatch(stored, filter string) bool {
	if filter == "" {
		return true
	}
	return stored == filter
}

// List returns up to limit messages, newest first, filtered by phone and text search.
func (s *Store) List(limit int, phone, search string) []MessageRecord {
	return s.Query(Query{Limit: limit, Phone: phone, Search: search})
}

// Query filters messages.
type Query struct {
	Limit    int
	Phone    string
	Search   string
	Before   time.Time
	Since    time.Time
	Imported *bool
}

// Query returns matching messages, newest first.
func (s *Store) Query(q Query) []MessageRecord {
	phone := normPhone(q.Phone)
	search := strings.ToLower(strings.TrimSpace(q.Search))
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]MessageRecord, 0)
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if !q.Before.IsZero() && !m.Timestamp.Before(q.Before) {
			continue
		}
		if !q.Since.IsZero() && m.Timestamp.Before(q.Since) {
			if !m.Imported {
				break
			}
			continue
		}
		if q.Imported != nil && m.Imported != *q.Imported {
			continue
		}
		if !phoneMatch(m.Phone, phone) {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(m.Text), search) && !strings.Contains(strings.ToLower(m.Name), search) && !strings.Contains(m.Phone, search) {
			continue
		}
		out = append(out, m)
		if q.Limit > 0 && len(out) >= q.Limit {
			break
		}
	}
	return out
}

// Conversation summarises one customer's thread for the inbox.
type Conversation struct {
	Phone         string           `json:"phone"`
	Name          string           `json:"name"`
	LastText      string           `json:"last_text"`
	LastAt        time.Time        `json:"last_at"`
	LastDirection MessageDirection `json:"last_direction"`
	LastStatus    string           `json:"last_status"`
	Messages      int              `json:"messages"`
	incomingTimes []time.Time
}

// UnreadSince counts incoming messages after t.
func (c Conversation) UnreadSince(t *time.Time) int {
	n := 0
	for _, it := range c.incomingTimes {
		if t == nil || it.After(*t) {
			n++
		}
	}
	return n
}

// Conversations returns one entry per customer, most recent first.
func (s *Store) Conversations() []Conversation {
	s.mu.RLock()
	idx := map[string]int{}
	var out []Conversation
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if m.Phone == "" {
			continue
		}
		j, ok := idx[m.Phone]
		if !ok {
			text := m.Text
			if text == "" && m.Media != "" {
				text = "[" + m.Media + "]"
			}
			out = append(out, Conversation{Phone: m.Phone, Name: m.Name, LastText: text, LastAt: m.Timestamp, LastDirection: m.Direction, LastStatus: m.Status})
			j = len(out) - 1
			idx[m.Phone] = j
		}
		c := &out[j]
		c.Messages++
		if c.Name == "" && m.Direction == DirectionIncoming {
			c.Name = m.Name
		}
		// Chats imported from the phone were already read there; they are not "unread".
		if m.Direction == DirectionIncoming && !m.Imported && len(c.incomingTimes) < 200 {
			c.incomingTimes = append(c.incomingTimes, m.Timestamp)
		}
	}
	s.mu.RUnlock()
	return out
}

// Stats summarises activity the app handled since a point in time. Imported history is excluded.
type Stats struct {
	Since       time.Time      `json:"since"`
	Incoming    int            `json:"incoming"`
	Outgoing    int            `json:"outgoing"`
	AutoReplies int            `json:"auto_replies"`
	Broadcasts  int            `json:"broadcasts"`
	Scheduled   int            `json:"scheduled"`
	DirectSends int            `json:"direct_sends"`
	Webhook     int            `json:"webhook"`
	Sent        int            `json:"sent"`
	Delivered   int            `json:"delivered"`
	Read        int            `json:"read"`
	Failed      int            `json:"failed"`
	DryRun      int            `json:"dry_run"`
	Hourly      [24]int        `json:"hourly"`
	Daily       map[string]int `json:"daily"`
	Rules       map[string]int `json:"rules"`
	Customers   int            `json:"customers"`
	FirstAt     *time.Time     `json:"first_at,omitempty"`
}

// Stats computes activity since t (zero = all time).
func (s *Store) Stats(since time.Time) Stats {
	st := Stats{Since: since, Daily: map[string]int{}, Rules: map[string]int{}}
	people := map[string]bool{}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.messages {
		if m.Imported || (!since.IsZero() && m.Timestamp.Before(since)) {
			continue
		}
		if st.FirstAt == nil {
			t := m.Timestamp
			st.FirstAt = &t
		}
		st.Hourly[m.Timestamp.Hour()]++
		st.Daily[m.Timestamp.Format("2006-01-02")]++
		if m.Phone != "" {
			people[m.Phone] = true
		}
		if m.Direction == DirectionIncoming {
			st.Incoming++
			continue
		}
		st.Outgoing++
		switch m.Type {
		case TypeAutoReply:
			st.AutoReplies++
		case TypeBroadcast:
			st.Broadcasts++
		case TypeScheduled:
			st.Scheduled++
		case TypeDirectSend:
			st.DirectSends++
		case TypeWebhook:
			st.Webhook++
		}
		switch m.Status {
		case StatusFailed:
			st.Failed++
		case StatusDryRun:
			st.DryRun++
		case StatusRead:
			st.Read++
			st.Delivered++
			st.Sent++
		case StatusDelivered:
			st.Delivered++
			st.Sent++
		default:
			st.Sent++
		}
		if m.RuleTriggered != "" {
			st.Rules[m.RuleTriggered]++
		}
	}
	st.Customers = len(people)
	return st
}

// SentSince counts messages the app actually sent since t (dry runs and failures excluded).
func (s *Store) SentSince(t time.Time) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if m.Timestamp.Before(t) {
			if !m.Imported {
				break
			}
			continue
		}
		// Messages the owner typed on the phone itself do not use up Pulse's daily limit.
		if m.Direction == DirectionOutgoing && !m.Imported && m.SentBy != "phone" && statusRank[m.Status] > 0 {
			n++
		}
	}
	return n
}

// AutoRepliesTo counts automated replies to phone since t.
func (s *Store) AutoRepliesTo(phone string, t time.Time) int {
	phone = digits(phone)
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if m.Timestamp.Before(t) {
			if !m.Imported {
				break
			}
			continue
		}
		if m.Type == TypeAutoReply && phoneMatch(m.Phone, phone) && m.Status != StatusFailed {
			n++
		}
	}
	return n
}

// LastAutoReply returns the most recent automated reply to phone.
func (s *Store) LastAutoReply(phone string) (MessageRecord, bool) {
	phone = digits(phone)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.messages) - 1; i >= 0; i-- {
		m := s.messages[i]
		if m.Type == TypeAutoReply && phoneMatch(m.Phone, phone) && m.Status != StatusFailed {
			return m, true
		}
	}
	return MessageRecord{}, false
}

// RecentIncoming returns the last n incoming texts from phone, newest first.
func (s *Store) RecentIncoming(phone string, n int) []MessageRecord {
	phone = digits(phone)
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []MessageRecord
	for i := len(s.messages) - 1; i >= 0 && len(out) < n; i-- {
		m := s.messages[i]
		if m.Direction == DirectionIncoming && !m.Imported && phoneMatch(m.Phone, phone) {
			out = append(out, m)
		}
	}
	return out
}

// TotalStats returns all-time incoming and outgoing counts excluding imported history.
func (s *Store) TotalStats() (int, int) {
	st := s.Stats(time.Time{})
	return st.Incoming, st.Outgoing
}

// ImportedCount returns the number of messages that came from history sync.
func (s *Store) ImportedCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, m := range s.messages {
		if m.Imported {
			n++
		}
	}
	return n
}

func (s *Store) removeWhere(pred func(MessageRecord) bool) int {
	s.mu.Lock()
	kept := s.messages[:0]
	removed := 0
	for _, m := range s.messages {
		if pred(m) {
			removed++
			continue
		}
		kept = append(kept, m)
	}
	s.messages = append([]MessageRecord(nil), kept...)
	if removed > 0 {
		s.reindexLocked()
	}
	s.mu.Unlock()
	if removed > 0 {
		s.saver.Trigger()
		_ = s.saver.Flush()
		securestore.RemoveBackup(s.filePath) // deleted messages must not survive in the backup copy
	}
	return removed
}

// DeleteHistorySync removes every imported message.
func (s *Store) DeleteHistorySync() int {
	return s.removeWhere(func(m MessageRecord) bool { return m.Imported })
}

// Clear deletes all messages.
func (s *Store) Clear() error {
	s.removeWhere(func(MessageRecord) bool { return true })
	return s.saver.Flush()
}

// ClearPhone deletes one customer's messages.
func (s *Store) ClearPhone(phone string) int {
	p := digits(phone)
	if p == "" {
		return 0
	}
	return s.removeWhere(func(m MessageRecord) bool { return phoneMatch(m.Phone, p) })
}

// PurgeOlderThan deletes messages older than days. days <= 0 deletes nothing.
func (s *Store) PurgeOlderThan(days int) int {
	if days <= 0 {
		return 0
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	return s.removeWhere(func(m MessageRecord) bool { return m.Timestamp.Before(cutoff) })
}

// ExportAll returns a copy of all messages, oldest first.
func (s *Store) ExportAll() []MessageRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]MessageRecord(nil), s.messages...)
}
