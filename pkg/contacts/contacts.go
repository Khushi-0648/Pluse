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
	"strconv"
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
	// UpgradedAt is when Pulse 2.1 first ran on this contact list (see settleUpgradeLocked).
	// Only contacts created before it can have been saved by Pulse 2.0 under a wrong number
	// (see LegacyWAID); a customer added since is never mistaken for one.
	UpgradedAt *time.Time `json:"upgraded_at,omitempty"`
	// LegacyBefore is set when the first start of 2.1 is not known exactly: contacts
	// created before it surely come from Pulse 2.0; one created between it and UpgradedAt
	// only may. Such a contact is never moved or erased with the real number, but its
	// opt-out still counts for the real number (the safe side). nil: same as UpgradedAt.
	LegacyBefore *time.Time `json:"legacy_before,omitempty"`
	// LegacyAwareAt is when Pulse first looked for Pulse 2.0's numbers on this list (2.1.0
	// did not): a contact created before it may have missed an opt-out kept under the old
	// form, which is then applied once (applyLegacyOptOutLocked). nil: UpgradedAt.
	LegacyAwareAt *time.Time `json:"legacy_aware_at,omitempty"`
	// UpgradeChecked records that UpgradedAt was worked out from the evidence on this PC.
	// 2.1.1 set UpgradedAt to its own first start, even on lists Pulse 2.1.0 had been
	// using for hours, so those contacts looked older than they are.
	UpgradeChecked bool `json:"upgrade_checked,omitempty"`
	// LegacyDone lists (hashed) numbers whose Pulse 2.0 form was already folded in, so
	// an opt-out taken over from it is applied once and an opt-in afterwards stays.
	LegacyDone map[string]bool `json:"legacy_done,omitempty"`
}

const (
	suppressOptedOut  = "opted_out"
	suppressForgotten = "forgotten"
	// suppressForgottenOptedOut marks the Pulse 2.0 form of an erased number that had
	// itself opted out: it is never restored, and still counts as opted out in case it
	// really is a different (Indian) customer.
	suppressForgottenOptedOut = "forgotten_opted_out"
)

// isOptOutMark reports whether a suppression entry means "opted out".
func isOptOutMark(v string) bool { return v == suppressOptedOut || v == suppressForgottenOptedOut }

// isForgottenMark reports whether a suppression entry means "erased: never restore".
func isForgottenMark(v string) bool { return v == suppressForgotten || v == suppressForgottenOptedOut }

// NewStore loads contacts from filePath using the process-wide encryption box.
func NewStore(filePath string) (*Store, error) {
	if filePath == "" {
		filePath = "contacts.json"
	}
	s := &Store{filePath: filePath, box: securestore.Default(), contacts: make(map[string]Contact)}
	s.saver = securestore.NewSaver(750*time.Millisecond, s.persist)
	ext := filepath.Ext(filePath)
	s.supPath = strings.TrimSuffix(filePath, ext) + "-suppressed" + ext
	supExisted := false
	if data, err := s.box.ReadFile(s.supPath); err == nil && len(data) > 0 {
		if err := json.Unmarshal(data, &s.sup); err != nil {
			return nil, fmt.Errorf("the opt-out list %s is damaged: %w", s.supPath, err)
		}
		supExisted = true
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
	if s.sup.UpgradedAt == nil || !s.sup.UpgradeChecked {
		// Recorded at once, so a contact added from now on is never taken for one from
		// Pulse 2.0.
		s.settleUpgradeLocked(supExisted)
		if err := s.saveSuppression(); err != nil {
			fmt.Printf("[Contacts] The opt-out list could not be saved: %v\n", err)
		}
	}
	return s, nil
}

// processStart is when this program started: the activity log's "started" entry for
// this run is not evidence of an earlier version.
var processStart = time.Now()

// startEvents returns the activity log's APP_STARTED entries ("Pulse 2.1.0 started").
// A variable so tests can supply a log.
var startEvents = func() []security.AuditEvent {
	return security.QueryAuditLog(0, "APP_STARTED", time.Time{})
}

var startedVersion = regexp.MustCompile(`(\d+)\.(\d+)\.\d+\S* started$`)

// startedVersion21 reads an APP_STARTED entry: ok when it names a version, is21 when
// that version is 2.1 or later.
func startedVersion21(details string) (is21, ok bool) {
	m := startedVersion.FindStringSubmatch(strings.TrimSpace(details))
	if m == nil {
		return false, false
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 2 || (major == 2 && minor >= 1), true
}

// settleUpgradeLocked works out, once, when Pulse 2.1 first ran on this list, so that
// only contacts Pulse 2.0 wrote are treated as possibly saved under a wrong number.
// Pulse 2.1.0 did not record it, and 2.1.1 recorded its own first start instead, so a
// customer added under 2.1.0 looked like a 2.0 contact. The evidence, all of it from this
// PC:
//
//   - the activity log's APP_STARTED entries name each version that started: the first
//     2.1 start is exact when a 2.0 (or older) start is still logged before it (the log
//     keeps the newest 2,000 entries);
//   - Pulse 2.0 stored every number through CleanPhone, so a contact under a bare
//     10-digit number starting 6-9 (6591234567) was written by 2.1, when it was created;
//   - Pulse 2.0 kept no opt-out list file, and 2.1.1 recorded UpgradedAt.
//
// With no sign of 2.1 at all this is the first start of 2.1, and every contact is from
// an earlier version. Otherwise UpgradedAt is the earliest sign of 2.1, and when the
// first start is not known exactly, LegacyBefore marks the contacts that surely predate
// it (created before a 2.0 start that is logged, else none): the others may be from
// either version, so they are never moved (see legacyContactLocked). Callers hold s.mu
// or own s.
func (s *Store) settleUpgradeLocked(supExisted bool) {
	now := time.Now()
	aware := now
	if s.sup.UpgradedAt != nil && s.sup.UpgradedAt.Before(now) {
		aware = *s.sup.UpgradedAt // 2.1.1 looked for 2.0's numbers from then on
	}
	var upper *time.Time
	note := func(t time.Time) {
		if !t.IsZero() && (upper == nil || t.Before(*upper)) {
			upper = &t
		}
	}
	ran21 := supExisted
	if s.sup.UpgradedAt != nil {
		ran21 = true
		note(*s.sup.UpgradedAt)
	}
	for _, c := range s.contacts {
		if LegacyWAID(c.Phone) != "" {
			ran21 = true
			note(c.CreatedAt)
		}
	}
	var first21 *time.Time
	var starts20 []time.Time
	for _, e := range startEvents() {
		if !e.Timestamp.Before(processStart) {
			continue
		}
		is21, ok := startedVersion21(e.Details)
		switch {
		case !ok:
		case is21:
			if first21 == nil || e.Timestamp.Before(*first21) {
				t := e.Timestamp
				first21 = &t
			}
		default:
			starts20 = append(starts20, e.Timestamp)
		}
	}
	var last20 *time.Time
	for _, t := range starts20 {
		if (first21 == nil || t.Before(*first21)) && (last20 == nil || t.After(*last20)) {
			t := t
			last20 = &t
		}
	}
	if first21 != nil {
		ran21 = true
		note(*first21)
	}
	if !ran21 {
		s.sup.UpgradedAt, s.sup.LegacyBefore = &now, nil
		aware = now
	} else {
		up := now
		if upper != nil {
			up = *upper
		}
		var lower time.Time // nothing surely from Pulse 2.0
		switch {
		case first21 != nil && last20 != nil:
			lower = *first21 // the first start of 2.1 is in the log
		case last20 != nil:
			lower = *last20 // 2.0 still ran then
		}
		if lower.After(up) {
			lower = up
		}
		s.sup.UpgradedAt = &up
		s.sup.LegacyBefore = nil
		if !lower.Equal(up) {
			s.sup.LegacyBefore = &lower
		}
	}
	s.sup.LegacyAwareAt = &aware
	s.sup.UpgradeChecked = true
}

// preUpgradeLocked reports whether c may have been written by Pulse 2.0 (created before
// UpgradedAt), so it may be saved under a wrong number. Callers hold s.mu.
func (s *Store) preUpgradeLocked(c Contact) bool {
	return s.sup.UpgradedAt != nil && c.CreatedAt.Before(*s.sup.UpgradedAt)
}

// surelyPreUpgradeLocked reports whether c was certainly written by Pulse 2.0 (see
// LegacyBefore). Callers hold s.mu.
func (s *Store) surelyPreUpgradeLocked(c Contact) bool {
	if s.sup.LegacyBefore != nil {
		return c.CreatedAt.Before(*s.sup.LegacyBefore)
	}
	return s.preUpgradeLocked(c)
}

// legacyContactLocked returns the contact Pulse 2.0 may have saved for phone under
// LegacyWAID(phone): one written by Pulse 2.0 that came from WhatsApp, wrote or opted
// out (one typed in by staff for the Indian number is not taken). sure reports that it
// was certainly written by Pulse 2.0 (surelyPreUpgradeLocked): only then may it be moved
// to phone or erased with it. Either way its opt-out counts for phone. Callers hold s.mu.
func (s *Store) legacyContactLocked(phone string) (c Contact, sure, ok bool) {
	legacy := LegacyWAID(phone)
	if legacy == "" {
		return Contact{}, false, false
	}
	c, ok = s.contacts[legacy]
	if !ok || !s.preUpgradeLocked(c) || !(c.Source == "whatsapp" || c.LastInboundAt != nil || c.IsDND) {
		return Contact{}, false, false
	}
	return c, s.surelyPreUpgradeLocked(c), true
}

// LegacyContact returns the contact stored under Pulse 2.0's form of phone (LegacyWAID:
// 916591234567 for 6591234567), if there is one. sure reports that it is certainly
// Pulse 2.0's record of phone's customer (see legacyContactLocked); otherwise it may be a
// different, Indian customer and must be left alone.
func (s *Store) LegacyContact(phone string) (c *Contact, sure bool) {
	phone = NormalizePhone(phone)
	s.mu.RLock()
	defer s.mu.RUnlock()
	legacy := LegacyWAID(phone)
	ex, ok := s.contacts[legacy]
	if legacy == "" || !ok {
		return nil, false
	}
	_, sure, _ = s.legacyContactLocked(phone)
	return &ex, sure
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

// Get finds a contact by a number a person typed (see ResolveTyped): a stored number
// matches exactly, a number typed with its country code means that number, and only a
// bare number that is no stored number gets India's 91 (CleanPhone). No number is ever
// matched by its last digits. Code holding a stored number uses GetExact.
func (s *Store) Get(phone string) (*Contact, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, err := ResolveTyped(phone, s.hasLocked)
	if err != nil {
		return nil, false
	}
	c, ok := s.contacts[key]
	if !ok || key == "" {
		return nil, false
	}
	return &c, true
}

// Resolve turns a number a person typed into the stored number it means (ResolveTyped).
// A number is known when it is a contact or when one of known says so (for example the
// message history), so a stored number that is no longer a contact keeps its country.
func (s *Store) Resolve(typed string, known ...func(string) bool) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return ResolveTyped(typed, func(p string) bool {
		if s.hasLocked(p) {
			return true
		}
		for _, k := range known {
			if k != nil && k(p) {
				return true
			}
		}
		return false
	})
}

// hasLocked reports whether phone is a contact's stored number. Callers hold s.mu.
func (s *Store) hasLocked(phone string) bool {
	_, ok := s.contacts[phone]
	return ok && phone != ""
}

// ErrAmbiguousNumber is returned for a typed number that can mean two stored numbers.
var ErrAmbiguousNumber = errors.New("That number matches two customers (one with India's +91 in front). Type it with its country code, for example +65 9123 4567 or +91 65912 34567.")

// ResolveTyped turns a number a person typed (a form, a search for one customer, the
// "send to" field, an integration's "to") into the stored number it means. This is the
// only place where India's 91 is guessed (CleanPhone), and only for typed input:
//
//   - a number typed with + or 00 has its country code: nothing is guessed;
//   - a bare run of digits that is a stored number (known) is that number exactly, so
//     the Singapore 6591234567 is never India's 916591234567;
//   - other digits get the reading of a person typing a local number: a 10-digit mobile
//     starting 6-9 is Indian (CleanPhone); a longer number is taken as international;
//     one of 10 digits or fewer with no such reading has no known country:
//     ErrNeedsCountryCode;
//   - formatted digits without a + ("65 9123 4567") that are a stored number as they are
//     and also with 91 in front name two customers: ErrAmbiguousNumber.
//
// No number is ever matched by its last digits. Code that already holds a stored number
// (from a contact, the history, a scheduled message or Meta) uses it as it is.
func ResolveTyped(typed string, known func(string) bool) (string, error) {
	t := strings.TrimSpace(typed)
	if t == "" {
		return "", nil
	}
	guess := CleanPhone(t)
	raw := CleanWAID(t)
	if strings.Contains(t, "@") {
		return raw, nil // a WhatsApp ID (6591234567@s.whatsapp.net) is international
	}
	if strings.HasPrefix(t, "+") || strings.HasPrefix(raw, "00") {
		return guess, nil
	}
	if known == nil {
		known = func(string) bool { return false }
	}
	if raw != "" && known(raw) {
		if raw != guess && nonDigits.FindStringIndex(t) != nil && known(guess) {
			return "", ErrAmbiguousNumber
		}
		return raw, nil
	}
	if guess == raw && len(raw) > 0 && len(raw) <= 10 {
		// Not a stored number and no reading applies (not an Indian mobile): a local
		// number without its country code. Never matched by its last digits.
		return "", ErrNeedsCountryCode
	}
	return guess, nil
}

// ErrNeedsCountryCode is returned for a typed number of 10 digits or fewer that is not a
// stored number and not an Indian mobile (6-9...), so its country is unknown.
var ErrNeedsCountryCode = errors.New("Type the number with its country code, for example +1 202 555 0123 or +91 98765 43210.")

// GetExact finds the contact stored under phone, an international number (a stored
// contact number, a Meta wa_id, or one typed with its +country code). No country code is
// guessed and no other number is matched by its last digits, so a number that is not a
// contact (for example one deleted after opting out) never resolves to someone else.
func (s *Store) GetExact(phone string) (*Contact, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key, ok := s.exactKeyLocked(phone)
	if !ok {
		return nil, false
	}
	c := s.contacts[key]
	return &c, true
}

// exactKeyLocked finds the stored key for an international number, exactly.
func (s *Store) exactKeyLocked(phone string) (string, bool) {
	k := NormalizePhone(phone)
	if k == "" {
		return "", false
	}
	_, ok := s.contacts[k]
	return k, ok
}

// Save creates or replaces a contact's editable fields; system timestamps are preserved.
// c.Phone is the number as a person typed it, or a saved contact's stored number, read by
// ResolveTyped: a saved contact keeps its number (6591234567 is Singapore, not India's
// 916591234567). Only the contact under that exact number is changed: saving one number
// never replaces or deletes another number's contact or opt-out.
func (s *Store) Save(c Contact) error {
	phone, err := s.Resolve(c.Phone)
	if err != nil {
		return err
	}
	c.Phone = phone
	return s.save(c)
}

// SaveExact is Save for a number that is already international digits (for example the
// number a message was just sent to): no country code is guessed.
func (s *Store) SaveExact(c Contact) error {
	c.Phone = NormalizePhone(c.Phone)
	return s.save(c)
}

func (s *Store) save(c Contact) error {
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
	if ex, ok := s.contacts[c.Phone]; ok {
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
	// The backup's numbers are stored numbers: matched exactly, never by a guessed
	// country code or their last digits, so a contact deleted after opting out is not
	// merged into someone else (and comes back only as do-not-message, below).
	if key, ok := s.exactKeyLocked(c.Phone); ok {
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
		if s.forgottenLocked(c) {
			s.mu.Unlock()
			return nil // they asked to be erased after this backup was made
		}
		if s.optedOutBeforeLocked(c.Phone) {
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
// The one exception is a contact Pulse 2.0 saved under the wrong number (LegacyWAID)
// before this list was upgraded (see suppression.UpgradedAt): when no contact has phone
// itself, that contact moves to phone with its opt-out, times, tags and notes, so an
// upgrade never loses an opt-out. Its consent does not move: it was recorded for the
// other number. When phone already has a contact from before the upgrade, an opt-out
// recorded under the old form is applied to it once.
func (s *Store) Upsert(phone, name, source string) Contact {
	phone = NormalizePhone(phone)
	name = cleanName(name, phone)
	s.mu.Lock()
	if c, ok := s.contacts[phone]; ok {
		changed := false
		if c.Name == "" && name != "" {
			c.Name = name
			changed = true
		}
		optedOut, supChanged := s.applyLegacyOptOutLocked(&c)
		if changed || optedOut {
			s.contacts[phone] = c
		}
		s.mu.Unlock()
		if supChanged {
			if err := s.saveSuppression(); err != nil {
				fmt.Printf("[Contacts] The opt-out list could not be saved: %v\n", err)
			}
		}
		if optedOut {
			s.changed(true)
			security.LogAudit("CONTACT_NUMBER_CORRECTED", "local", "system", fmt.Sprintf("Contact %s marked do-not-message: it opted out under %s, the number an earlier version saved for it", security.MaskPhone(phone), security.MaskPhone(LegacyWAID(phone))), "success")
		} else if changed {
			s.saver.Trigger()
		}
		return c
	}
	legacy := LegacyWAID(phone)
	supChanged := s.carryLegacySuppressionLocked(legacy, phone)
	if c, sure, ok := s.legacyContactLocked(phone); ok && sure {
		// Only a contact certainly written by Pulse 2.0 that came from WhatsApp, wrote, or
		// opted out moves: one typed in by staff for the Indian number, or any contact that
		// may have been added since (a real Indian customer), is left alone; its opt-out
		// still applies to the new contact below. An opt-out also stays on the old number,
		// so if it really was India's 91 number, that customer stays opted out.
		if c.IsDND {
			// The list is saved below in any case (markLegacyDoneLocked changes it too).
			s.suppressLocked(legacy, suppressOptedOut)
			s.noteOptOutTimeLocked(legacy, c)
		} else {
			c.ConsentSource, c.ConsentAt = "", nil
		}
		// Only the opt-out moves, never consent: it was recorded for the other number.
		c.Consent, c.OptInAt = false, nil
		delete(s.contacts, legacy)
		c.Phone = phone
		if c.Name == "" {
			c.Name = name
		}
		s.markLegacyDoneLocked(phone)
		supChanged = true
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

// markLegacyDoneLocked records that phone's Pulse 2.0 form was folded in.
func (s *Store) markLegacyDoneLocked(phone string) {
	if s.sup.LegacyDone == nil {
		s.sup.LegacyDone = map[string]bool{}
	}
	s.sup.LegacyDone[s.suppressKeyLocked(phone)] = true
}

// beforeLegacyAwareLocked reports whether c was created before Pulse looked for Pulse
// 2.0's numbers (LegacyAwareAt; Pulse 2.1.0 created contacts under the real number
// without doing so). Callers hold s.mu.
func (s *Store) beforeLegacyAwareLocked(c Contact) bool {
	if s.sup.LegacyAwareAt != nil {
		return c.CreatedAt.Before(*s.sup.LegacyAwareAt)
	}
	return s.preUpgradeLocked(c)
}

// applyLegacyOptOutLocked applies to c, a contact created before Pulse looked for Pulse
// 2.0's numbers (beforeLegacyAwareLocked), an opt-out that Pulse 2.0 recorded under its
// wrong form of the number (LegacyWAID): a live contact there that opted out, or one
// deleted after opting out. It is done once per number (an opt-in afterwards stays), and
// not when the customer's own START is newer or the opt-out was made since the upgrade.
// It reports whether c changed and whether the suppression list changed. Callers hold
// s.mu.
func (s *Store) applyLegacyOptOutLocked(c *Contact) (optedOut, supChanged bool) {
	legacy := LegacyWAID(c.Phone)
	if legacy == "" || c.IsDND || !s.beforeLegacyAwareLocked(*c) || s.sup.LegacyDone[s.suppressKeyLocked(c.Phone)] {
		return false, false
	}
	var at *time.Time
	found := false
	if lc, _, ok := s.legacyContactLocked(c.Phone); ok && lc.IsDND {
		found, at = true, lc.ConsentAt
	} else if lk := s.suppressKeyLocked(legacy); isOptOutMark(s.sup.Numbers[lk]) {
		found = true
		if t, ok := s.sup.OptedOutAt[lk]; ok {
			at = &t
		}
	}
	if !found || (at != nil && !at.Before(*s.sup.UpgradedAt)) {
		// Nothing there, or an opt-out made since the upgrade: Meta reports this customer
		// under their real number now, so that one came from whoever has the 91 number.
		return false, false
	}
	s.markLegacyDoneLocked(c.Phone)
	if c.OptInAt != nil && at != nil && c.OptInAt.After(*at) {
		return false, true // the customer's START is newer than that opt-out
	}
	when := time.Now()
	if at != nil {
		when = *at
	}
	c.IsDND, c.Consent, c.OptInAt = true, false, nil
	c.ConsentAt = &when
	c.ConsentSource = "Opted out under +" + legacy + ", the number an earlier version of Pulse saved"
	c.Tags = removeTag(c.Tags, "Subscribed")
	c.Tags = addTag(c.Tags, "Opted out")
	return true, true
}

// forgottenLocked reports whether c (from a backup) belongs to a customer who asked to be
// erased, under its own number or the other form Pulse 2.0 used for it. Callers hold s.mu.
func (s *Store) forgottenLocked(c Contact) bool {
	if isForgottenMark(s.sup.Numbers[s.suppressKeyLocked(c.Phone)]) {
		return true
	}
	if legacy := LegacyWAID(c.Phone); legacy != "" && isForgottenMark(s.sup.Numbers[s.suppressKeyLocked(legacy)]) {
		return true
	}
	// A contact from before the upgrade under 91 + a 10-digit number may be how Pulse 2.0
	// kept a foreign number (91 6591234567 for Singapore's 6591234567) that was erased.
	if real := realFromLegacy(c.Phone); real != "" && s.preUpgradeLocked(c) && isForgottenMark(s.sup.Numbers[s.suppressKeyLocked(real)]) {
		return true
	}
	return false
}

// realFromLegacy undoes LegacyWAID: 916591234567 -> 6591234567, or "".
func realFromLegacy(phone string) string {
	if len(phone) == 12 && strings.HasPrefix(phone, "91") && phone[2] >= '6' && phone[2] <= '9' && nonDigits.FindStringIndex(phone) == nil {
		return phone[2:]
	}
	return ""
}

// forgetLegacyLocked records the Pulse 2.0 form of an erased number, so an old backup
// cannot bring the customer back under it. If that form had opted out itself (it may
// really be an Indian customer), it stays opted out too. It reports a change.
func (s *Store) forgetLegacyLocked(legacy string) bool {
	k := s.suppressKeyLocked(legacy)
	switch v := s.sup.Numbers[k]; {
	case isForgottenMark(v):
		return false
	case v == suppressOptedOut:
		s.sup.Numbers[k] = suppressForgottenOptedOut
	default:
		s.sup.Numbers[k] = suppressForgotten
	}
	return true
}

func (s *Store) update(phone string, critical bool, fn func(*Contact)) bool {
	s.mu.Lock()
	// Every change goes to the contact stored under exactly this number: a STOP, a START
	// or a consent change must never land on another customer whose number looks alike.
	key, ok := s.exactKeyLocked(phone)
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

// ForgetLegacy erases, for an erasure request for phone, the contact Pulse 2.0 kept for
// that customer under LegacyWAID(phone) (916591234567 for 6591234567), but only when it
// is certainly that customer's record (LegacyContact reports sure); a contact that may
// belong to someone in India is left alone. Like Forget, the old form is never restored
// from a backup; if it had opted out it also stays opted out, in case that number
// belongs to someone else too. Call it before Forget(phone). It reports whether a contact
// was erased.
func (s *Store) ForgetLegacy(phone string) bool {
	phone = NormalizePhone(phone)
	s.mu.Lock()
	c, sure, ok := s.legacyContactLocked(phone)
	if !ok || !sure {
		s.mu.Unlock()
		return false
	}
	legacy := LegacyWAID(phone)
	delete(s.contacts, legacy)
	if c.IsDND {
		s.sup.Numbers[s.suppressKeyLocked(legacy)] = suppressForgottenOptedOut
	} else {
		s.forgetLegacyLocked(legacy)
	}
	s.mu.Unlock()
	if err := s.saveSuppression(); err != nil {
		fmt.Printf("[Contacts] The opt-out list could not be saved: %v\n", err)
	}
	s.changed(true)
	securestore.RemoveBackup(s.filePath)
	return true
}

// remove deletes a contact. A contact who had opted out is remembered as such (a restore
// brings them back only as do-not-message); reason "forgotten" blocks any restore.
func (s *Store) remove(phone, reason string) bool {
	s.mu.Lock()
	// Stored numbers only: deleting one number never removes a look-alike contact.
	key, ok := s.exactKeyLocked(phone)
	timeNoted := false
	if ok {
		if s.contacts[key].IsDND && reason == "" {
			reason = suppressOptedOut
			timeNoted = s.noteOptOutTimeLocked(key, s.contacts[key])
		}
		delete(s.contacts, key)
	}
	if reason == suppressForgotten && s.sup.OptedOutAt != nil {
		delete(s.sup.OptedOutAt, s.suppressKeyLocked(key)) // erased: keep nothing about them
	}
	sup := reason != "" && key != "" && s.suppressLocked(key, reason)
	if reason == suppressForgotten {
		if legacy := LegacyWAID(key); legacy != "" {
			// Pulse 2.0 may have kept this foreign number as 91 + the number, so a backup
			// made before 2.1 may hold the customer under that form.
			sup = s.forgetLegacyLocked(legacy) || sup
		}
	}
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
	if isOptOutMark(s.sup.Numbers[s.suppressKeyLocked(phone)]) {
		return true
	}
	// Pulse 2.0 kept a Meta number such as 6591234567 as 916591234567 (LegacyWAID): an
	// opt-out recorded there, deleted or on a contact from before the upgrade, applies.
	legacy := LegacyWAID(phone)
	if legacy == "" {
		return false
	}
	if isOptOutMark(s.sup.Numbers[s.suppressKeyLocked(legacy)]) {
		return true
	}
	lc, _, ok := s.legacyContactLocked(phone)
	return ok && lc.IsDND
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
	phone = NormalizePhone(phone)
	if t, ok := s.sup.OptedOutAt[s.suppressKeyLocked(phone)]; ok {
		return &t
	}
	if legacy := LegacyWAID(phone); legacy != "" {
		if t, ok := s.sup.OptedOutAt[s.suppressKeyLocked(legacy)]; ok {
			return &t
		}
		if lc, _, ok := s.legacyContactLocked(phone); ok && lc.IsDND && lc.ConsentAt != nil {
			t := *lc.ConsentAt
			return &t
		}
	}
	return nil
}

// WasOptedOut reports whether phone, a number that is not a contact, opted out: it was
// deleted after opting out, or Pulse 2.0 recorded the opt-out under its old form of the
// number (LegacyWAID), on a contact from before the upgrade or one deleted since.
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
	changed := isOptOutMark(s.sup.Numbers[k])
	if changed {
		if s.sup.Numbers[k] == suppressForgottenOptedOut {
			s.sup.Numbers[k] = suppressForgotten
		} else {
			delete(s.sup.Numbers, k)
		}
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
	if v := s.sup.Numbers[k]; v == reason || v == suppressForgotten || (v == suppressForgottenOptedOut && reason == suppressOptedOut) {
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
// Meta does not keep the order of deliveries: an older message arriving late does not
// move the time back.
func (s *Store) RecordInbound(phone string, at time.Time) {
	s.update(phone, false, func(c *Contact) {
		if c.LastInboundAt == nil || at.After(*c.LastInboundAt) {
			c.LastInboundAt = &at
		}
	})
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
	ok := s.update(phone, true, func(c *Contact) {
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
	if ok && !dnd {
		// Only once the contact was found and opted in: a number that is not a contact
		// keeps its earlier opt-out.
		s.unsuppress(phone)
	}
	return ok
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
	ok := s.update(phone, true, func(c *Contact) {
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
	if ok && consent {
		s.unsuppress(phone) // only for a contact that was found and opted in
	}
	return ok
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
		c := Contact{Phone: phone, CreatedAt: now, Source: "whatsapp", BotPausedUntil: &until, NeedsAttention: true, AttentionReason: r, AttentionAt: &now}
		if s.optedOutBeforeLocked(phone) {
			c.IsDND, c.ConsentAt = true, s.optedOutAtLocked(phone) // opted out, then deleted
		}
		if _, exists := s.contacts[phone]; !exists {
			s.contacts[phone] = c
		}
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
	c, ok := s.GetExact(phone)
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
		// Read like a typed number: a saved contact's number exported as digits keeps its
		// country (6591234567 is Singapore, not India's 916591234567).
		phone, rerr := ResolveTyped(raw, s.hasLocked)
		if rerr != nil {
			res.Skipped++
			res.Warnings = append(res.Warnings, fmt.Sprintf("Row %d: %q matches two contacts; write it with its country code (+...).", rowNum, raw))
			continue
		}
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

		// Only the contact under exactly this number is updated: an opt-out or consent in
		// the file never lands on a different customer whose number looks alike.
		key := phone
		if _, exists := s.contacts[key]; exists {
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
