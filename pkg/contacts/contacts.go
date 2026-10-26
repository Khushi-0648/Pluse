package contacts

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/securestore"
	"whatsapp-automation/pkg/security"
)

// Contact is a stored customer profile.
type Contact struct {
	Phone           string     `json:"phone"`
	Name            string     `json:"name"`
	Email           string     `json:"email,omitempty"`
	Tags            []string   `json:"tags,omitempty"`
	Notes           string     `json:"notes,omitempty"`
	Source          string     `json:"source,omitempty"`
	IsDND           bool       `json:"is_dnd,omitempty"`
	Consent         bool       `json:"consent,omitempty"`
	ConsentSource   string     `json:"consent_source,omitempty"`
	AssignedTo      string     `json:"assigned_to,omitempty"` // staff member handling the chat (FT21)
	ConsentAt       *time.Time `json:"consent_at,omitempty"`
	BotPausedUntil  *time.Time `json:"bot_paused_until,omitempty"`
	NeedsAttention  bool       `json:"needs_attention,omitempty"`
	AttentionReason string     `json:"attention_reason,omitempty"`
	AttentionAt     *time.Time `json:"attention_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	LastContactedAt *time.Time `json:"last_contacted_at,omitempty"`
	LastInboundAt   *time.Time `json:"last_inbound_at,omitempty"`
	LastReadAt      *time.Time `json:"last_read_at,omitempty"`
	// OptInAt is when the customer sent their latest START (their own opt-in, not one
	// recorded by staff), so an older STOP that arrives after it is not applied.
	OptInAt *time.Time `json:"opt_in_at,omitempty"`
	// MarketingOptOut is set when Meta refused a message because this customer stopped
	// marketing messages from businesses in WhatsApp (error 131050). Campaigns leave them
	// out; replies to their own messages still go.
	MarketingOptOut   bool       `json:"marketing_opt_out,omitempty"`
	MarketingOptOutAt *time.Time `json:"marketing_opt_out_at,omitempty"`
}

// DisplayName returns the saved name or the formatted phone number.
func (c Contact) DisplayName() string {
	if strings.TrimSpace(c.Name) != "" {
		return c.Name
	}
	return "+" + c.Phone
}

// BotPaused reports whether automated replies are paused for this contact.
func (c Contact) BotPaused(now time.Time) bool {
	return c.BotPausedUntil != nil && now.Before(*c.BotPausedUntil)
}

// InServiceWindow reports whether the customer wrote to us in the last 24 hours.
func (c Contact) InServiceWindow(now time.Time) bool {
	return c.LastInboundAt != nil && now.Sub(*c.LastInboundAt) < 24*time.Hour
}

// FreeTextWindow is how long after the customer's last message Pulse still sends free
// text. WhatsApp's window is 24 hours, but a message queued, paced or retried near the
// end could reach Meta after it closed, so Pulse switches to the template half an hour
// early.
const FreeTextWindow = 23*time.Hour + 30*time.Minute

// CanSendFreeText reports whether free text (not only an approved template) may be sent
// to the customer now (see FreeTextWindow).
func (c Contact) CanSendFreeText(now time.Time) bool {
	return c.LastInboundAt != nil && now.Sub(*c.LastInboundAt) < FreeTextWindow
}

// Store is a thread-safe, persisted contact list.
type Store struct {
	mu       sync.RWMutex
	filePath string
	box      *securestore.Box
	contacts map[string]Contact
	saver    *securestore.Saver
	supPath  string      // the suppression list, next to the contacts file
	sup      suppression // guarded by mu
	// supSaveMu runs one suppression-list save at a time (see saveSuppression).
	supSaveMu sync.Mutex
	// onRenamed runs after a contact saved by Pulse 2.0 under a wrong number moved to the
	// number Meta sends (see LegacyWAID), so other stores can follow. Guarded by mu.
	onRenamed []func(oldPhone, newPhone string)
}

// suppression remembers numbers that must not come back through "Restore backup": a
// contact deleted after opting out (restored as do-not-message, never with consent) and
// a customer who asked to be forgotten (not restored at all). Daily backups keep older
// copies of the contact list, so without this a restore would undo an opt-out or an
// erasure. Only a salted hash of each number is kept, not the number.
type suppression struct {
	Salt    string            `json:"salt"`
	Numbers map[string]string `json:"numbers"` // hash -> suppressOptedOut | suppressForgotten
	// OptedOutAt keeps when an opted-out number last opted out, so a contact created for
	// it again keeps that time and a START written before it still cannot subscribe them.
	OptedOutAt map[string]time.Time `json:"opted_out_at,omitempty"`
}

const (
	suppressOptedOut  = "opted_out"
	suppressForgotten = "forgotten"
)

// NewStore loads contacts from filePath using the process-wide encryption box.
func NewStore(filePath string) (*Store, error) {
	if filePath == "" {
		filePath = "contacts.json"
	}
	s := &Store{filePath: filePath, box: securestore.Default(), contacts: make(map[string]Contact)}
	s.saver = securestore.NewSaver(750*time.Millisecond, s.persist)
	ext := filepath.Ext(filePath)
	s.supPath = strings.TrimSuffix(filePath, ext) + "-suppressed" + ext
	if data, err := s.box.ReadFile(s.supPath); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &s.sup); err != nil {
			return nil, fmt.Errorf("the opt-out list %s is damaged: %w", s.supPath, err)
		}
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cannot read the opt-out list %s: %w", s.supPath, err)
	}
	if s.sup.Salt == "" {
		s.sup.Salt = security.RandomToken(16)
	}
	if s.sup.Numbers == nil {
		s.sup.Numbers = map[string]string{}
	}

	data, err := s.box.ReadFile(filePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cannot read contacts file %s: %w", filePath, err)
	}
	if len(data) > 0 {
		var list []Contact
		if err := json.Unmarshal(data, &list); err != nil {
			bak, bErr := s.box.ReadBackup(filePath)
			if bErr != nil || json.Unmarshal(bak, &list) != nil {
				return nil, fmt.Errorf("contacts file %s is damaged and no usable backup was found: %w", filePath, err)
			}
		}
		for _, c := range list {
			c.Phone = NormalizePhone(c.Phone) // stored numbers are international already
			if len(c.Phone) < 7 {
				continue
			}
			c.Name = cleanName(c.Name, c.Phone)
			if ex, dup := s.contacts[c.Phone]; dup {
				c = mergeContacts(ex, c) // two old spellings of one number: keep both records' details
			}
			s.contacts[c.Phone] = c
		}
	}
	return s, nil
}

func cleanName(name, phone string) string {
	n := strings.TrimSpace(name)
	digits := nonDigits.ReplaceAllString(n, "")
	if n == "" || n == "-" || IsLID(n) || (digits != "" && (digits == phone || strings.HasSuffix(phone, digits)) && len(digits) >= 7) {
		return ""
	}
	if strings.ContainsAny(n, "∙•") {
		return ""
	}
	return n
}

// Flush writes pending changes to disk.
func (s *Store) Flush() error { return s.saver.Flush() }

func (s *Store) changed(critical bool) {
	if critical {
		_ = s.saver.Flush()
		return
	}
	s.saver.Trigger()
}

func (s *Store) persist() error {
	s.mu.RLock()
	list := make([]Contact, 0, len(s.contacts))
	for _, c := range s.contacts {
		list = append(list, c)
	}
	s.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Phone < list[j].Phone })
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return s.box.WriteFile(s.filePath, data)
}

// Clear deletes every contact. Opt-outs are kept in the suppression list (as hashes), so
// restoring a downloaded backup later cannot bring those customers back with consent.
func (s *Store) Clear() error {
	s.mu.Lock()
	n := len(s.contacts)
	sup := false
	for phone, c := range s.contacts {
		if c.IsDND && s.suppressLocked(phone, suppressOptedOut) {
			sup = true
		}
		if c.IsDND && s.noteOptOutTimeLocked(phone, c) {
			sup = true
		}
	}
	s.contacts = make(map[string]Contact)
	s.mu.Unlock()
	if sup {
		if err := s.saveSuppression(); err != nil {
			fmt.Printf("[Contacts] The opt-out list could not be saved: %v\n", err)
		}
	}
	s.saver.Trigger()
	err := s.saver.Flush()
	securestore.RemoveBackup(s.filePath)
	security.LogAudit("CONTACTS_PURGED", "local", "system", fmt.Sprintf("%d contacts deleted", n), "warning")
	return err
}

// Count returns the number of contacts.
func (s *Store) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.contacts)
}

// List returns all contacts sorted by most recent activity, then name.
func (s *Store) List() []Contact {
	s.mu.RLock()
	out := make([]Contact, 0, len(s.contacts))
	for _, c := range s.contacts {
		out = append(out, c)
	}
	s.mu.RUnlock()
	sortContacts(out)
	return out
}

func lastActivity(c Contact) time.Time {
	t := c.CreatedAt
	if c.LastInboundAt != nil && c.LastInboundAt.After(t) {
		t = *c.LastInboundAt
	}
	if c.LastContactedAt != nil && c.LastContactedAt.After(t) {
		t = *c.LastContactedAt
	}
	return t
}

func sortContacts(list []Contact) {
	sort.SliceStable(list, func(i, j int) bool {
		ai, aj := lastActivity(list[i]), lastActivity(list[j])
		if !ai.Equal(aj) {
			return ai.After(aj)
		}
		return strings.ToLower(list[i].DisplayName()) < strings.ToLower(list[j].DisplayName())
	})
}

// Get finds a contact by phone, tolerating formatting differences.
func (s *Store) Get(phone string) (*Contact, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, ok := s.keyLocked(phone)
	if !ok {
		return nil, false
	}
	c := s.contacts[key]
	return &c, true
}

// keyLocked finds the stored key for phone. An international number (bare digits, as
// stored or as Meta sends it) matches exactly first, so the Singapore number 6591234567
// finds its own contact, not the Indian 916591234567; then the number is read as typed
// by a person (CleanPhone).
func (s *Store) keyLocked(phone string) (string, bool) {
	for _, k := range []string{NormalizePhone(phone), CleanPhone(phone), CleanWAID(phone)} {
		if _, ok := s.contacts[k]; ok && k != "" {
			return k, true
		}
	}
	raw := CleanWAID(phone)
	// A bare 10-digit number without a country code may match one saved contact by its
	// last digits; numbers with a country code must match exactly.
	if len(raw) == 10 && !strings.HasPrefix(strings.TrimSpace(phone), "+") {
		found := ""
		for k := range s.contacts {
			if len(k) > 10 && strings.HasSuffix(k, raw) {
				if found != "" {
					return "", false
				}
				found = k
			}
		}
		if found != "" {
			return found, true
		}
	}
	return "", false
}

// Save creates or replaces a contact's editable fields; system timestamps are preserved.
func (s *Store) Save(c Contact) error {
	typed := c.Phone
	c.Phone = CleanPhone(typed)
	if exact := NormalizePhone(typed); exact != c.Phone {
		// The form sends a saved contact's number back as stored digits: an existing
		// contact keeps its number (6591234567 is Singapore, not India's 916591234567).
		s.mu.RLock()
		if _, ok := s.contacts[exact]; ok {
			c.Phone = exact
		}
		s.mu.RUnlock()
	}
	if len(c.Phone) < 7 || len(c.Phone) > 15 {
		return errors.New("Enter a valid phone number with country code, for example +919876543210.")
	}
	c.Name = cleanName(c.Name, c.Phone)
	c.Email = strings.TrimSpace(c.Email)
	c.Notes = strings.TrimSpace(c.Notes)
	c.Tags = normalizeTags(c.Tags)
	if len(c.Name) > 120 || len(c.Email) > 200 || len(c.Notes) > 2000 {
		return errors.New("Name, email or notes are too long.")
	}
	if c.IsDND {
		c.Consent = false // an opt-out always withdraws consent
	}

	s.mu.Lock()
	critical := false
	if key, ok := s.keyLocked(c.Phone); ok {
		ex := s.contacts[key]
		if key != c.Phone {
			delete(s.contacts, key)
		}
		c.CreatedAt = ex.CreatedAt
		c.LastContactedAt, c.LastInboundAt, c.LastReadAt = ex.LastContactedAt, ex.LastInboundAt, ex.LastReadAt
		c.BotPausedUntil, c.NeedsAttention, c.AttentionReason, c.AttentionAt = ex.BotPausedUntil, ex.NeedsAttention, ex.AttentionReason, ex.AttentionAt
		c.OptInAt, c.MarketingOptOut, c.MarketingOptOutAt = ex.OptInAt, ex.MarketingOptOut, ex.MarketingOptOutAt
		if c.Source == "" {
			c.Source = ex.Source
		}
		if c.AssignedTo == "" {
			c.AssignedTo = ex.AssignedTo // the contact form does not edit the assignment
		}
		if c.Consent == ex.Consent {
			// The date stays; a corrected "how did they agree" note is kept.
			c.ConsentAt = ex.ConsentAt
			if strings.TrimSpace(c.ConsentSource) == "" || !c.Consent {
				c.ConsentSource = ex.ConsentSource
			} else if c.ConsentSource != ex.ConsentSource {
				critical = true
			}
		} else {
			critical = true
			if c.Consent {
				now := time.Now()
				c.ConsentAt = &now
				if c.ConsentSource == "" {
					c.ConsentSource = "Recorded manually"
				}
			}
		}
		critical = critical || c.IsDND != ex.IsDND
		if c.IsDND && !ex.IsDND {
			// Like a STOP, the opt-out keeps its time, so an older START that arrives
			// later cannot undo it.
			now := time.Now()
			c.ConsentAt = &now
			c.OptInAt = nil
		} else if c.IsDND {
			c.ConsentAt = ex.ConsentAt
		}
	} else {
		if c.CreatedAt.IsZero() {
			c.CreatedAt = time.Now()
		}
		if c.Source == "" {
			c.Source = "manual"
		}
		// System timestamps are never taken from the form: a made-up "last message" time
		// would open the 24-hour window, and a back-dated consent date would be false evidence.
		c.LastContactedAt, c.LastInboundAt, c.LastReadAt = nil, nil, nil
		c.BotPausedUntil, c.NeedsAttention, c.AttentionReason, c.AttentionAt = nil, false, "", nil
		c.ConsentAt, c.OptInAt, c.MarketingOptOut, c.MarketingOptOutAt = nil, nil, false, nil
		if s.optedOutBeforeLocked(c.Phone) {
			// Opted out, then deleted: adding the number again must not undo the opt-out.
			c.IsDND, c.Consent = true, false
			c.ConsentAt = s.optedOutAtLocked(c.Phone)
		}
		if c.IsDND && c.ConsentAt == nil {
			now := time.Now()
			c.ConsentAt = &now // when the opt-out was recorded
		}
		if c.Consent {
			now := time.Now()
			c.ConsentAt = &now
			if c.ConsentSource == "" {
				c.ConsentSource = "Recorded manually"
			}
		}
	}
	s.contacts[c.Phone] = c
	s.mu.Unlock()
	s.changed(critical)
	return nil
}

// Restore merges a contact from a backup. Existing contacts only gain missing details and
// tags; their opt-out and consent state is never changed. New contacts keep the backup's
// opt-out flag, and consent only when the backup recorded when it was given.
func (s *Store) Restore(c Contact) error {
	c.Phone = NormalizePhone(c.Phone)
	if len(c.Phone) < 7 || len(c.Phone) > 15 {
		return errors.New("invalid phone number")
	}
	c.Name = cleanName(c.Name, c.Phone)
	s.mu.Lock()
	if key, ok := s.keyLocked(c.Phone); ok {
		ex := s.contacts[key]
		if ex.Name == "" {
			ex.Name = c.Name
		}
		if ex.Email == "" {
			ex.Email = strings.TrimSpace(c.Email)
		}
		if ex.Notes == "" {
			ex.Notes = strings.TrimSpace(c.Notes)
		}
		ex.Tags = normalizeTags(append(ex.Tags, c.Tags...))
		s.contacts[key] = ex
	} else {
		switch s.sup.Numbers[s.suppressKeyLocked(c.Phone)] {
		case suppressForgotten:
			s.mu.Unlock()
			return nil // they asked to be erased after this backup was made
		case suppressOptedOut:
			c.IsDND = true // opted out after this backup was made: never undone by a restore
		}
		n := Contact{Phone: c.Phone, Name: c.Name, Email: strings.TrimSpace(c.Email), Notes: strings.TrimSpace(c.Notes), Tags: normalizeTags(c.Tags), Source: "backup", IsDND: c.IsDND, CreatedAt: c.CreatedAt}
		if n.CreatedAt.IsZero() {
			n.CreatedAt = time.Now()
		}
		if c.Consent && !c.IsDND && c.ConsentAt != nil {
			n.Consent, n.ConsentSource, n.ConsentAt = true, c.ConsentSource, c.ConsentAt
		}
		if n.IsDND {
			// The opt-out keeps its time, so an older START arriving later cannot undo it.
			n.ConsentSource, n.ConsentAt = c.ConsentSource, c.ConsentAt
			if t := s.optedOutAtLocked(c.Phone); t != nil {
				n.ConsentAt = t
			}
		}
		s.contacts[c.Phone] = n
	}
	s.mu.Unlock()
	s.saver.Trigger()
	return nil
}

// Upsert records a contact seen in WhatsApp without overwriting edited details.
// phone is an international number (as Meta sends it, or after CleanPhone) and must
// match a contact exactly: a sender from Singapore (6591234567) is never taken for the
// Indian contact 916591234567. It returns the stored contact.
//
// The one exception is a contact Pulse 2.0 saved under the wrong number (LegacyWAID):
// when no contact has phone itself, that contact moves to phone with everything it had
// (opt-out, consent, times, tags, notes), so an upgrade never loses an opt-out.
func (s *Store) Upsert(phone, name, source string) Contact {
	phone = NormalizePhone(phone)
	name = cleanName(name, phone)
	s.mu.Lock()
	if c, ok := s.contacts[phone]; ok {
		if c.Name == "" && name != "" {
			c.Name = name
			s.contacts[phone] = c
			s.saver.Trigger()
		}
		s.mu.Unlock()
		return c
	}
	legacy := LegacyWAID(phone)
	supChanged := s.carryLegacySuppressionLocked(legacy, phone)
	if c, ok := s.contacts[legacy]; ok && legacy != "" && (c.Source == "whatsapp" || c.LastInboundAt != nil || c.IsDND) {
		// Only a contact that came from WhatsApp, wrote, or opted out moves: one typed in
		// by staff for the Indian number is left alone. An opt-out also stays on the old
		// number, so if it really was India's 91 number, that customer stays opted out.
		if c.IsDND {
			supChanged = s.suppressLocked(legacy, suppressOptedOut) || supChanged
			supChanged = s.noteOptOutTimeLocked(legacy, c) || supChanged
		}
		delete(s.contacts, legacy)
		c.Phone = phone
		if c.Name == "" {
			c.Name = name
		}
		s.contacts[phone] = c
		hooks := append([]func(string, string){}, s.onRenamed...)
		s.mu.Unlock()
		if supChanged {
			if err := s.saveSuppression(); err != nil {
				fmt.Printf("[Contacts] The opt-out list could not be saved: %v\n", err)
			}
		}
		s.changed(true)
		security.LogAudit("CONTACT_NUMBER_CORRECTED", "local", "system", fmt.Sprintf("Contact %s moved to %s, the number WhatsApp reports (an earlier version added 91 to it)", security.MaskPhone(legacy), security.MaskPhone(phone)), "success")
		for _, h := range hooks {
			h(legacy, phone)
		}
		return c
	}
	c := Contact{Phone: phone, Name: name, Source: source, CreatedAt: time.Now(), IsDND: s.optedOutBeforeLocked(phone)}
	if c.IsDND {
		c.ConsentAt = s.optedOutAtLocked(phone) // keeps the guard against an older START
	}
	if len(phone) >= 7 {
		s.contacts[phone] = c
		s.saver.Trigger()
	}
	s.mu.Unlock()
	if supChanged {
		if err := s.saveSuppression(); err != nil {
			fmt.Printf("[Contacts] The opt-out list could not be saved: %v\n", err)
		}
	}
	return c
}

// OnRenamed registers f to run when a contact moves from the number Pulse 2.0 saved to
// the number Meta sends (see Upsert), so the message history can follow it.
func (s *Store) OnRenamed(f func(oldPhone, newPhone string)) {
	s.mu.Lock()
	s.onRenamed = append(s.onRenamed, f)
	s.mu.Unlock()
}

// LegacyWAID returns the number Pulse 2.0 stored for the number waid that Meta sent.
// 2.0 read every number as if a person had typed it, so a 10-digit number starting 6-9
// (Singapore's 6591234567) was saved with India's 91 in front: 916591234567. It returns
// "" for numbers 2.0 stored as they were.
func LegacyWAID(waid string) string {
	d := CleanWAID(waid)
	if len(d) == 10 && d[0] >= '6' && d[0] <= '9' {
		return "91" + d
	}
	return ""
}

// carryLegacySuppressionLocked re-records an opt-out or erasure that Pulse 2.0 kept
// under legacy (LegacyWAID) for phone too. It reports a change. Callers hold s.mu.
func (s *Store) carryLegacySuppressionLocked(legacy, phone string) bool {
	if legacy == "" {
		return false
	}
	lk := s.suppressKeyLocked(legacy)
	changed := false
	if reason, ok := s.sup.Numbers[lk]; ok && s.suppressLocked(phone, reason) {
		changed = true
	}
	if t, ok := s.sup.OptedOutAt[lk]; ok {
		k := s.suppressKeyLocked(phone)
		if prev, had := s.sup.OptedOutAt[k]; !had || t.After(prev) {
			s.sup.OptedOutAt[k] = t
			changed = true
		}
	}
	return changed
}

func (s *Store) update(phone string, critical bool, fn func(*Contact)) bool {
	s.mu.Lock()
	key, ok := s.keyLocked(phone)
	if !ok {
		s.mu.Unlock()
		return false
	}
	c := s.contacts[key]
	fn(&c)
	s.contacts[key] = c
	s.mu.Unlock()
	s.changed(critical)
	return true
}

// SetTag adds or removes one tag under the store lock, so it never overwrites an
// opt-out that arrives at the same moment.
func (s *Store) SetTag(phone, tag string, on bool) bool {
	return s.update(phone, false, func(c *Contact) {
		if on {
			c.Tags = addTag(c.Tags, tag)
		} else {
			c.Tags = removeTag(c.Tags, tag)
		}
	})
}

// Delete removes a contact.
func (s *Store) Delete(phone string) bool {
	return s.remove(phone, "")
}

// Forget deletes a contact because they asked to be erased: a later "Restore backup"
// must not bring them back, even from a backup made before the request.
func (s *Store) Forget(phone string) bool {
	return s.remove(phone, suppressForgotten)
}

// remove deletes a contact. A contact who had opted out is remembered as such (a restore
// brings them back only as do-not-message); reason "forgotten" blocks any restore.
func (s *Store) remove(phone, reason string) bool {
	s.mu.Lock()
	key, ok := s.keyLocked(phone)
	timeNoted := false
	if ok {
		if s.contacts[key].IsDND && reason == "" {
			reason = suppressOptedOut
			timeNoted = s.noteOptOutTimeLocked(key, s.contacts[key])
		}
		delete(s.contacts, key)
	} else {
		key = NormalizePhone(phone)
	}
	if reason == suppressForgotten && s.sup.OptedOutAt != nil {
		delete(s.sup.OptedOutAt, s.suppressKeyLocked(key)) // erased: keep nothing about them
	}
	sup := reason != "" && key != "" && s.suppressLocked(key, reason)
	sup = sup || timeNoted
	s.mu.Unlock()
	if sup {
		if err := s.saveSuppression(); err != nil {
			fmt.Printf("[Contacts] The opt-out list could not be saved: %v\n", err)
		}
	}
	if ok {
		s.changed(true)
		securestore.RemoveBackup(s.filePath)
	}
	return ok
}

func (s *Store) suppressKeyLocked(phone string) string {
	sum := sha256.Sum256([]byte(s.sup.Salt + ":" + phone))
	return hex.EncodeToString(sum[:])
}

// optedOutBeforeLocked reports whether a number that is not a contact now opted out
// before it was deleted. Callers hold s.mu.
func (s *Store) optedOutBeforeLocked(phone string) bool {
	phone = NormalizePhone(phone)
	if s.sup.Numbers[s.suppressKeyLocked(phone)] == suppressOptedOut {
		return true
	}
	// Pulse 2.0 kept a Meta number such as 6591234567 as 916591234567 (LegacyWAID).
	legacy := LegacyWAID(phone)
	return legacy != "" && s.sup.Numbers[s.suppressKeyLocked(legacy)] == suppressOptedOut
}

// noteOptOutTimeLocked keeps when c (being removed) opted out. It reports a change.
func (s *Store) noteOptOutTimeLocked(phone string, c Contact) bool {
	if c.ConsentAt == nil {
		return false
	}
	k := s.suppressKeyLocked(NormalizePhone(phone))
	if s.sup.OptedOutAt == nil {
		s.sup.OptedOutAt = map[string]time.Time{}
	}
	if prev, ok := s.sup.OptedOutAt[k]; ok && !c.ConsentAt.After(prev) {
		return false
	}
	s.sup.OptedOutAt[k] = *c.ConsentAt
	return true
}

// optedOutAtLocked returns when a deleted opted-out number opted out, if known.
func (s *Store) optedOutAtLocked(phone string) *time.Time {
	if t, ok := s.sup.OptedOutAt[s.suppressKeyLocked(NormalizePhone(phone))]; ok {
		return &t
	}
	return nil
}

// WasOptedOut reports whether phone opted out and was then deleted.
func (s *Store) WasOptedOut(phone string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.optedOutBeforeLocked(phone)
}

// unsuppress forgets an earlier opt-out after the customer opted in again, so deleting
// and re-adding them later does not mark them do-not-message. "Forgotten" stays.
func (s *Store) unsuppress(phone string) {
	s.mu.Lock()
	k := s.suppressKeyLocked(NormalizePhone(phone))
	changed := s.sup.Numbers[k] == suppressOptedOut
	if changed {
		delete(s.sup.Numbers, k)
		delete(s.sup.OptedOutAt, k)
	}
	s.mu.Unlock()
	if changed {
		if err := s.saveSuppression(); err != nil {
			fmt.Printf("[Contacts] The opt-out list could not be saved: %v\n", err)
		}
	}
}

// suppressLocked records phone; "forgotten" is never weakened to "opted out".
func (s *Store) suppressLocked(phone, reason string) bool {
	k := s.suppressKeyLocked(phone)
	if s.sup.Numbers[k] == reason || s.sup.Numbers[k] == suppressForgotten {
		return false
	}
	s.sup.Numbers[k] = reason
	return true
}

// saveSuppression writes the list at once: it is small and changes rarely, and losing an
// entry would let a restore undo an opt-out.
func (s *Store) saveSuppression() error {
	// One save at a time, each taking its copy inside the lock: otherwise an older copy
	// written last could drop an opt-out that a parallel save had just added.
	s.supSaveMu.Lock()
	defer s.supSaveMu.Unlock()
	s.mu.RLock()
	data, err := json.Marshal(s.sup)
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	return s.box.WriteFile(s.supPath, data)
}

// UpdateLastContacted stamps an outgoing message.
func (s *Store) UpdateLastContacted(phone string) {
	now := time.Now()
	s.update(phone, false, func(c *Contact) { c.LastContactedAt = &now })
}

// RecordInbound stamps an incoming customer message (opens the 24-hour service window).
func (s *Store) RecordInbound(phone string, at time.Time) {
	s.update(phone, false, func(c *Contact) { c.LastInboundAt = &at })
}

// MarkRead records that staff opened the conversation.
func (s *Store) MarkRead(phone string) {
	now := time.Now()
	s.update(phone, false, func(c *Contact) { c.LastReadAt = &now })
}

// SetDND sets the opt-out flag. Opting out also withdraws marketing consent. at is when
// the customer sent the opt-out (default now), so a later START can be told apart from
// an older one that arrived late.
func (s *Store) SetDND(phone string, dnd bool, reason string, at ...time.Time) bool {
	if !dnd {
		defer s.unsuppress(phone)
	}
	return s.update(phone, true, func(c *Contact) {
		already := c.IsDND
		sentAt := len(at) > 0 && !at[0].IsZero()
		if dnd && sentAt && !already && c.OptInAt != nil && at[0].Before(*c.OptInAt) {
			// A STOP written before the customer's newer START arrived late (Meta does not
			// keep the order of deliveries): their latest choice was START.
			return
		}
		c.IsDND = dnd
		if dnd {
			c.Consent = false
			c.OptInAt = nil
			when := time.Now()
			if sentAt {
				when = at[0]
			}
			// An older STOP that arrives late must not move the opt-out back in time, or
			// a START written between the two would wrongly count as newer.
			if !already || c.ConsentAt == nil || when.After(*c.ConsentAt) {
				c.ConsentSource = reason
				c.ConsentAt = &when
			}
			c.Tags = removeTag(c.Tags, "Subscribed")
			c.Tags = addTag(c.Tags, "Opted out")
		} else {
			c.Tags = removeTag(c.Tags, "Opted out")
		}
	})
}

// ToggleDND flips the opt-out flag.
func (s *Store) ToggleDND(phone string) (bool, error) {
	var now bool
	ok := s.update(phone, true, func(c *Contact) {
		c.IsDND = !c.IsDND
		now = c.IsDND
		if c.IsDND {
			// The opt-out keeps its time like a STOP, so an older START arriving later
			// cannot undo it.
			at := time.Now()
			c.Consent, c.ConsentAt, c.OptInAt = false, &at, nil
			c.ConsentSource = "Marked do-not-message on " + at.Format("2006-01-02")
			c.Tags = removeTag(c.Tags, "Subscribed")
			c.Tags = addTag(c.Tags, "Opted out")
		} else {
			c.Tags = removeTag(c.Tags, "Opted out")
		}
	})
	if !ok {
		return false, errors.New("contact not found")
	}
	if !now {
		s.unsuppress(phone)
	}
	return now, nil
}

// SetConsent records or withdraws opt-in consent with its source. at, when given, is
// when the customer sent their own opt-in (START): it is kept as the consent time and as
// OptInAt, so an older STOP that arrives after it is not applied.
func (s *Store) SetConsent(phone string, consent bool, source string, at ...time.Time) bool {
	if consent {
		defer s.unsuppress(phone)
	}
	return s.update(phone, true, func(c *Contact) {
		c.Consent = consent
		c.ConsentSource = source
		now := time.Now()
		c.ConsentAt, c.OptInAt = &now, nil
		if consent && len(at) > 0 && !at[0].IsZero() {
			sent := at[0]
			c.ConsentAt, c.OptInAt = &sent, &sent
		}
		if consent {
			c.IsDND = false
			c.Tags = removeTag(c.Tags, "Opted out")
			// They asked for messages again; if WhatsApp still blocks marketing, Meta's
			// next refusal marks it again.
			c.MarketingOptOut, c.MarketingOptOutAt = false, nil
		}
	})
}

// SetMarketingOptOut records that Meta refused a message because the customer stopped
// marketing messages from businesses in WhatsApp (error 131050). Campaigns leave such a
// customer out; replies to their own messages still go. It reports whether the contact
// exists.
func (s *Store) SetMarketingOptOut(phone string, at time.Time) bool {
	if at.IsZero() {
		at = time.Now()
	}
	return s.update(phone, true, func(c *Contact) {
		if !c.MarketingOptOut {
			c.MarketingOptOut, c.MarketingOptOutAt = true, &at
		}
	})
}

// PauseBot stops automated replies for duration and flags the chat for staff.
func (s *Store) PauseBot(phone string, duration time.Duration, reason ...string) {
	phone = NormalizePhone(phone)
	until := time.Now().Add(duration)
	now := time.Now()
	r := "Customer asked for a person"
	if len(reason) > 0 && reason[0] != "" {
		r = reason[0]
	}
	if !s.update(phone, true, func(c *Contact) {
		c.BotPausedUntil, c.NeedsAttention, c.AttentionReason, c.AttentionAt = &until, true, r, &now
	}) && len(phone) >= 7 {
		s.mu.Lock()
		s.contacts[phone] = Contact{Phone: phone, CreatedAt: now, Source: "whatsapp", BotPausedUntil: &until, NeedsAttention: true, AttentionReason: r, AttentionAt: &now}
		s.mu.Unlock()
		s.changed(true)
	}
}

// ResumeBot re-enables automated replies and clears the attention flag.
func (s *Store) ResumeBot(phone string) bool {
	return s.update(phone, true, func(c *Contact) {
		c.BotPausedUntil, c.NeedsAttention, c.AttentionReason, c.AttentionAt = nil, false, "", nil
	})
}

// SetChatMeta changes who handles a chat and its internal note. A nil value is left as is.
func (s *Store) SetChatMeta(phone string, assignedTo, notes *string) error {
	phone = NormalizePhone(phone)
	if len(phone) < 7 {
		return errors.New("Enter a valid phone number.")
	}
	if assignedTo != nil && len(strings.TrimSpace(*assignedTo)) > 60 {
		return errors.New("The name can be at most 60 characters.")
	}
	if notes != nil && len(strings.TrimSpace(*notes)) > 2000 {
		return errors.New("The note can be at most 2000 characters.")
	}
	s.Upsert(phone, "", "whatsapp")
	s.update(phone, false, func(c *Contact) {
		if assignedTo != nil {
			c.AssignedTo = strings.TrimSpace(*assignedTo)
		}
		if notes != nil {
			c.Notes = strings.TrimSpace(*notes)
		}
	})
	return nil
}

// FlagAttention puts a chat on the needs-attention list without pausing automatic replies.
func (s *Store) FlagAttention(phone, reason string) {
	now := time.Now()
	s.update(phone, true, func(c *Contact) {
		if !c.NeedsAttention {
			c.NeedsAttention, c.AttentionReason, c.AttentionAt = true, reason, &now
		}
	})
}

// ResumeAttentionOnly clears the needs-attention flag but keeps automated replies paused.
func (s *Store) ResumeAttentionOnly(phone string) bool {
	return s.update(phone, true, func(c *Contact) { c.NeedsAttention, c.AttentionReason, c.AttentionAt = false, "", nil })
}

// IsBotPaused reports whether automated replies are paused.
func (s *Store) IsBotPaused(phone string) bool {
	c, ok := s.Get(phone)
	return ok && c.BotPaused(time.Now())
}

// NeedsAttention returns contacts waiting for a person, oldest request first.
func (s *Store) NeedsAttention() []Contact {
	var out []Contact
	for _, c := range s.List() {
		if c.NeedsAttention {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AttentionAt == nil || out[j].AttentionAt == nil {
			return out[i].AttentionAt != nil
		}
		return out[i].AttentionAt.Before(*out[j].AttentionAt)
	})
	return out
}

// GetByTag returns contacts carrying tag (case-insensitive).
func (s *Store) GetByTag(tag string) []Contact {
	tag = strings.ToLower(strings.TrimSpace(tag))
	var out []Contact
	for _, c := range s.List() {
		for _, t := range c.Tags {
			if strings.ToLower(strings.TrimSpace(t)) == tag {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// GetAllTags returns the distinct tags in use, sorted.
func (s *Store) GetAllTags() []string {
	s.mu.RLock()
	seen := map[string]string{}
	for _, c := range s.contacts {
		for _, t := range c.Tags {
			if k := strings.ToLower(t); k != "" {
				if _, ok := seen[k]; !ok {
					seen[k] = t
				}
			}
		}
	}
	s.mu.RUnlock()
	out := make([]string, 0, len(seen))
	for _, t := range seen {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

// Search matches name, phone, email, notes or tags.
func (s *Store) Search(query string) []Contact {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return s.List()
	}
	qd := nonDigits.ReplaceAllString(q, "")
	var out []Contact
	for _, c := range s.List() {
		hay := strings.ToLower(c.Name + " " + c.Email + " " + c.Notes + " " + strings.Join(c.Tags, " "))
		if strings.Contains(hay, q) || (qd != "" && strings.Contains(c.Phone, qd)) {
			out = append(out, c)
		}
	}
	return out
}

func mergeContacts(a, b Contact) Contact {
	if a.Name == "" {
		a.Name = b.Name
	}
	if a.Email == "" {
		a.Email = b.Email
	}
	if a.Notes == "" {
		a.Notes = b.Notes
	}
	a.Tags = normalizeTags(append(a.Tags, b.Tags...))
	a.IsDND = a.IsDND || b.IsDND
	if b.MarketingOptOut && !a.MarketingOptOut {
		a.MarketingOptOut, a.MarketingOptOutAt = true, b.MarketingOptOutAt
	}
	later := func(x, y *time.Time) *time.Time {
		if x == nil || (y != nil && y.After(*x)) {
			return y
		}
		return x
	}
	// Keep the most recent activity, a pending hand-over and the consent record.
	a.LastInboundAt = later(a.LastInboundAt, b.LastInboundAt)
	a.LastContactedAt = later(a.LastContactedAt, b.LastContactedAt)
	a.BotPausedUntil = later(a.BotPausedUntil, b.BotPausedUntil)
	if b.NeedsAttention && !a.NeedsAttention {
		a.NeedsAttention, a.AttentionReason, a.AttentionAt = true, b.AttentionReason, b.AttentionAt
	}
	if !a.Consent && b.Consent {
		a.Consent, a.ConsentSource, a.ConsentAt = true, b.ConsentSource, b.ConsentAt
	}
	if a.IsDND {
		a.Consent = false
	}
	return a
}

func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" || len(t) > 40 || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func addTag(tags []string, tag string) []string { return normalizeTags(append(tags, tag)) }

func removeTag(tags []string, tag string) []string {
	out := tags[:0:0]
	for _, t := range tags {
		if !strings.EqualFold(t, tag) {
			out = append(out, t)
		}
	}
	return normalizeTags(out)
}

// CSVMapping gives the zero-based column index for each field, or -1 when absent.
type CSVMapping struct {
	Phone   int  `json:"phone"`
	Name    int  `json:"name"`
	Email   int  `json:"email"`
	Tags    int  `json:"tags"`
	Notes   int  `json:"notes"`
	Consent int  `json:"consent"`
	Header  bool `json:"header"`
}

// CSVPreview is returned before an import so the user can confirm the column mapping.
type CSVPreview struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
	Total   int        `json:"total"`
	Mapping CSVMapping `json:"mapping"`
}

// CSVImportResult details a completed import.
type CSVImportResult struct {
	Total      int      `json:"total"`
	Added      int      `json:"added"`
	Updated    int      `json:"updated"`
	Skipped    int      `json:"skipped"`
	Warnings   []string `json:"warnings"`
	Overwrites []string `json:"overwrites"`
}

func readCSV(r io.Reader) ([][]string, error) {
	data, err := io.ReadAll(io.LimitReader(r, 20<<20))
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	cr := csv.NewReader(strings.NewReader(string(data)))
	cr.FieldsPerRecord = -1
	cr.TrimLeadingSpace = true
	cr.LazyQuotes = true
	records, err := cr.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("The file is not a valid CSV: %w", err)
	}
	if len(records) == 0 {
		return nil, errors.New("The CSV file is empty.")
	}
	return records, nil
}

// DetectMapping guesses columns from a header row.
func DetectMapping(header []string) CSVMapping {
	m := CSVMapping{Phone: -1, Name: -1, Email: -1, Tags: -1, Notes: -1, Consent: -1}
	for i, col := range header {
		switch strings.ToLower(strings.TrimSpace(col)) {
		case "phone", "number", "mobile", "phone number", "whatsapp", "whatsapp number", "contact number":
			m.Phone = i
		case "name", "full name", "fullname", "contact", "customer", "first name":
			if m.Name == -1 {
				m.Name = i
			}
		case "email", "e-mail", "email address":
			m.Email = i
		case "tags", "tag", "labels", "group":
			m.Tags = i
		case "notes", "note", "comments", "remarks":
			m.Notes = i
		case "consent", "opt-in", "opt in", "optin", "subscribed", "opted in":
			m.Consent = i
		}
	}
	if m.Phone >= 0 {
		m.Header = true
	} else {
		m.Phone, m.Name = 0, 1
		if len(header) < 2 {
			m.Name = -1
		}
	}
	return m
}

// PreviewCSV parses the file and returns the first rows with a guessed mapping.
func PreviewCSV(r io.Reader) (CSVPreview, error) {
	records, err := readCSV(r)
	if err != nil {
		return CSVPreview{}, err
	}
	m := DetectMapping(records[0])
	p := CSVPreview{Mapping: m}
	rows := records
	if m.Header {
		p.Columns = records[0]
		rows = records[1:]
	} else {
		for i := range records[0] {
			p.Columns = append(p.Columns, fmt.Sprintf("Column %d", i+1))
		}
	}
	p.Total = len(rows)
	if len(rows) > 5 {
		rows = rows[:5]
	}
	p.Rows = rows
	return p, nil
}

func cell(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return csvUnsafe(strings.TrimSpace(row[idx]))
}

// csvUnsafe undoes csvSafe: the ' that the export puts before a cell starting with = + -
// or @ is not part of the value, so export followed by import changes nothing.
func csvUnsafe(v string) string {
	if len(v) >= 2 && v[0] == '\'' && strings.ContainsRune("=+-@\t\r", rune(v[1])) {
		return v[1:]
	}
	return v
}

// consentDate reads a consent date written by Pulse's export (or typed as 2026-01-31).
// A date in the future or one that cannot be read is ignored.
func consentDate(v string, now time.Time) *time.Time {
	for _, layout := range []string{"2006-01-02", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, v, time.Local); err == nil && !t.After(now) {
			return &t
		}
	}
	return nil
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "y", "yes", "true", "opted in", "opt-in", "subscribed", "haan", "ha":
		return true
	}
	return false
}

// ImportCSV imports contacts. A nil mapping auto-detects columns. Existing contacts are
// merged: non-empty cells update fields, tags are added, nothing is blanked.
func (s *Store) ImportCSV(r io.Reader, mapping ...*CSVMapping) (CSVImportResult, error) {
	res := CSVImportResult{Warnings: []string{}, Overwrites: []string{}}
	records, err := readCSV(r)
	if err != nil {
		return res, err
	}
	m := DetectMapping(records[0])
	if len(mapping) > 0 && mapping[0] != nil {
		m = *mapping[0]
	}
	if m.Phone < 0 {
		return res, errors.New("Choose which column holds the phone number.")
	}
	start := 0
	if m.Header {
		start = 1
	}
	stamp := time.Now()
	consentSource := "CSV import " + stamp.Format("2006-01-02")
	// Columns written by Pulse's own export, so export + import keeps opt-outs and
	// the original consent evidence.
	optOutCol, sourceCol, dateCol := -1, -1, -1
	if m.Header {
		for i, col := range records[0] {
			switch strings.ToLower(strings.TrimSpace(col)) {
			case "opted_out", "opted out", "do not message", "dnd", "unsubscribed":
				optOutCol = i
			case "consent_source", "consent source", "how they agreed":
				sourceCol = i
			case "consent_date", "consent date", "consented on", "opt-in date":
				dateCol = i
			}
		}
	}

	s.mu.Lock()
	for i := start; i < len(records); i++ {
		row, rowNum := records[i], i+1
		empty := true
		for _, v := range row {
			if strings.TrimSpace(v) != "" {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		res.Total++
		raw := cell(row, m.Phone)
		if lr := strings.ToLower(raw); strings.Contains(lr, "e+") || strings.ContainsAny(raw, ".,") {
			// Excel turns long numbers into 9.19877E+11, losing digits.
			res.Skipped++
			res.Warnings = append(res.Warnings, fmt.Sprintf("Row %d: the phone %q was changed by Excel. Format the phone column as Text, type the numbers again and save.", rowNum, raw))
			continue
		}
		phone := CleanPhone(raw)
		if len(phone) < 7 || len(phone) > 15 {
			res.Skipped++
			res.Warnings = append(res.Warnings, fmt.Sprintf("Row %d: invalid phone %q", rowNum, raw))
			continue
		}
		name, email, notes := cleanName(cell(row, m.Name), phone), cell(row, m.Email), cell(row, m.Notes)
		var tags []string
		if t := cell(row, m.Tags); t != "" {
			tags = strings.FieldsFunc(t, func(r rune) bool { return r == ';' || r == ',' || r == '|' })
		}
		consentCell := cell(row, m.Consent)
		optedOut := truthy(cell(row, optOutCol))
		rowSource := consentSource
		if src := cell(row, sourceCol); src != "" {
			rowSource = src
		}
		// The date the person agreed is consent evidence: keep the one in the file.
		rowDate := &stamp
		if d := consentDate(cell(row, dateCol), stamp); d != nil {
			rowDate = d
		}

		key, exists := s.keyLocked(phone)
		if exists {
			c := s.contacts[key]
			changes := []string{}
			if name != "" && name != c.Name {
				c.Name = name
				changes = append(changes, "name")
			}
			if email != "" && email != c.Email {
				c.Email = email
				changes = append(changes, "email")
			}
			if notes != "" && notes != c.Notes {
				c.Notes = notes
				changes = append(changes, "notes")
			}
			if len(tags) > 0 {
				before := len(c.Tags)
				c.Tags = normalizeTags(append(c.Tags, tags...))
				if len(c.Tags) != before {
					changes = append(changes, "tags")
				}
			}
			if optedOut && !c.IsDND {
				c.IsDND, c.Consent = true, false // an opt-out in the file is always honoured
				// Kept like a STOP's time, so an older START that arrives later cannot undo it.
				c.ConsentAt, c.OptInAt = &stamp, nil
				c.ConsentSource = "Opted out in " + consentSource
				changes = append(changes, "opted out")
			}
			if m.Consent >= 0 && consentCell != "" && truthy(consentCell) && !c.Consent && !c.IsDND {
				c.Consent, c.ConsentSource, c.ConsentAt = true, rowSource, rowDate
				changes = append(changes, "consent")
			}
			s.contacts[key] = c
			res.Updated++
			if len(changes) > 0 {
				res.Overwrites = append(res.Overwrites, fmt.Sprintf("Row %d: +%s updated (%s)", rowNum, key, strings.Join(changes, ", ")))
			}
			continue
		}
		c := Contact{Phone: phone, Name: name, Email: email, Notes: notes, Tags: normalizeTags(tags), Source: "csv", CreatedAt: stamp}
		if s.optedOutBeforeLocked(phone) {
			c.IsDND = true // opted out and deleted earlier: an older file cannot undo that
			c.ConsentAt = s.optedOutAtLocked(phone)
			if c.ConsentAt == nil {
				c.ConsentAt = &stamp
			}
			res.Warnings = append(res.Warnings, fmt.Sprintf("Row %d: +%s opted out earlier and was added as do-not-message.", rowNum, phone))
		} else if optedOut {
			c.IsDND, c.ConsentAt = true, rowDate // when the opt-out was recorded (the file's date if it has one)
			c.ConsentSource = "Opted out in " + consentSource
			if cell(row, sourceCol) != "" {
				c.ConsentSource = rowSource
			}
		} else if m.Consent >= 0 && truthy(consentCell) {
			c.Consent, c.ConsentSource, c.ConsentAt = true, rowSource, rowDate
		}
		s.contacts[phone] = c
		res.Added++
	}
	s.mu.Unlock()
	if res.Total == 0 {
		return res, errors.New("The CSV file has no data rows.")
	}
	s.changed(true)
	return res, nil
}

// ExportCSV writes all contacts as CSV.
func (s *Store) ExportCSV(w io.Writer) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"phone", "name", "email", "tags", "notes", "consent", "consent_source", "consent_date", "opted_out"}); err != nil {
		return err
	}
	for _, c := range s.List() {
		cd := ""
		if c.ConsentAt != nil {
			cd = c.ConsentAt.Format("2006-01-02")
		}
		row := []string{"+" + c.Phone, csvSafe(c.Name), csvSafe(c.Email), csvSafe(strings.Join(c.Tags, ";")), csvSafe(c.Notes), yesNo(c.Consent), csvSafe(c.ConsentSource), cd, yesNo(c.IsDND)}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// csvSafe neutralises spreadsheet formula injection.
func csvSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

var nonDigits = regexp.MustCompile(`[^0-9]`)

// longNumberCountries are calling codes whose real mobile numbers reach 13-14 digits
// (Brazil, China, Germany, Indonesia, Nigeria, Bangladesh, Austria, Italy, Vietnam).
var longNumberCountries = []string{"55", "86", "49", "62", "234", "880", "43", "39", "84"}

// IsLID reports whether s looks like a WhatsApp linked-device ID rather than a phone number.
func IsLID(str string) bool {
	s := strings.TrimSpace(str)
	if strings.HasSuffix(s, "@lid") {
		return true
	}
	clean := strings.TrimPrefix(s, "+")
	if len(clean) < 13 || len(clean) > 18 {
		return false
	}
	for _, r := range clean {
		if r < '0' || r > '9' {
			return false
		}
	}
	if len(clean) <= 14 {
		for _, cc := range longNumberCountries {
			if strings.HasPrefix(clean, cc) {
				return false // a real phone number, not an internal ID
			}
		}
	}
	return true
}

// CleanWAID normalises a number Meta sent (the webhook's from, wa_id and recipient_id):
// those are always the full international number, digits only, so no country code is
// ever guessed. A Singapore number 6591234567 stays 6591234567 instead of becoming the
// Indian 916591234567 that CleanPhone would make of it.
func CleanWAID(id string) string {
	p := strings.TrimSpace(id)
	if i := strings.Index(p, "@"); i >= 0 {
		p = p[:i]
	}
	if i := strings.Index(p, ":"); i >= 0 {
		p = p[:i]
	}
	return nonDigits.ReplaceAllString(p, "")
}

// NormalizePhone normalises a number that is already international: a stored contact
// number, a Meta wa_id, or one that went through CleanPhone. A bare run of digits is
// kept as it is; anything typed with a + or other formatting goes through CleanPhone.
// Use CleanPhone for what a person typed (forms, CSV files, the API's "to").
func NormalizePhone(phone string) string {
	p := strings.TrimSpace(phone)
	if p != "" && nonDigits.FindStringIndex(p) == nil {
		return p
	}
	if i := strings.IndexAny(p, "@:"); i >= 0 && nonDigits.FindStringIndex(p[:i]) == nil {
		return CleanWAID(p) // a JID such as 6591234567@s.whatsapp.net
	}
	return CleanPhone(p)
}

// MatchesTyped reports whether a number a person typed (a blocked number, a test number)
// is phone, an international number. It matches with and without the Indian default, so
// "6591234567" typed without a + still matches the Singapore number 6591234567.
func MatchesTyped(typed, phone string) bool {
	if phone == "" {
		return false
	}
	if c := CleanPhone(typed); c != "" && c == phone {
		return true
	}
	return CleanWAID(typed) == phone
}

// AllowListed reports whether phone is on a list of test numbers a person typed. Unlike
// MatchesTyped (right for the blocked list, where matching too much is the safe side), a
// typed entry that is exactly a saved contact's number means that contact only: the
// test number "6591234567" of a Singapore contact does not also let messages go to
// India's 916591234567. s may be nil.
func (s *Store) AllowListed(list []string, phone string) bool {
	for _, p := range list {
		if raw := CleanWAID(p); raw != "" && raw != phone && !strings.HasPrefix(strings.TrimSpace(p), "+") && s != nil {
			s.mu.RLock()
			_, exact := s.contacts[raw]
			s.mu.RUnlock()
			if exact {
				continue // the entry names another saved contact exactly
			}
		}
		if MatchesTyped(p, phone) {
			return true
		}
	}
	return false
}

// CleanPhone normalises a phone number a person typed to international digits. A
// 10-digit number starting 6-9 without a + is treated as an Indian mobile number, so it
// must not be used for numbers Meta sent (CleanWAID) or already stored (NormalizePhone).
func CleanPhone(phone string) string {
	p := strings.TrimSpace(phone)
	if i := strings.Index(p, "@"); i >= 0 {
		p = p[:i]
	}
	if i := strings.Index(p, ":"); i >= 0 {
		p = p[:i]
	}
	hadPlus := strings.HasPrefix(p, "+")
	clean := nonDigits.ReplaceAllString(p, "")
	if strings.HasPrefix(clean, "00") && len(clean) > 10 {
		clean = clean[2:]
		hadPlus = true
	}
	if strings.HasPrefix(clean, "0") && len(clean) == 11 {
		clean = clean[1:]
	}
	if !hadPlus && len(clean) == 10 && clean[0] >= '6' && clean[0] <= '9' {
		clean = "91" + clean
	}
	return clean
}
