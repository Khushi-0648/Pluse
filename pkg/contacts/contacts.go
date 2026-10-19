package contacts

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/security"
)

// Contact represents a stored recipient profile
type Contact struct {
	Phone           string     `json:"phone"`
	Name            string     `json:"name"`
	Email           string     `json:"email,omitempty"`
	Tags            []string   `json:"tags,omitempty"`
	Notes           string     `json:"notes,omitempty"`
	IsDND           bool       `json:"is_dnd,omitempty"` // Do Not Disturb / Blacklist flag
	BotPausedUntil  *time.Time `json:"bot_paused_until,omitempty"`
	NeedsAttention  bool       `json:"needs_attention,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	LastContactedAt *time.Time `json:"last_contacted_at,omitempty"`
}

// Store provides thread-safe persistent CRUD for contacts
type Store struct {
	mu       sync.RWMutex
	filePath string
	contacts map[string]Contact
}

// NewStore initializes the contact store from a JSON persistence file with backup recovery (D15)
func NewStore(filePath string) (*Store, error) {
	if filePath == "" {
		filePath = "contacts.json"
	}

	s := &Store{
		filePath: filePath,
		contacts: make(map[string]Contact),
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		_ = s.persist()
		return s, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil || len(data) == 0 {
		// Attempt recovery from backup if main file cannot be read or is empty (D15)
		bakPath := filePath + ".bak"
		if bakData, bErr := os.ReadFile(bakPath); bErr == nil && len(bakData) > 0 {
			data = bakData
		} else if err != nil {
			return nil, fmt.Errorf("failed to read contacts file %s: %w", filePath, err)
		}
	}

	if len(data) > 0 {
		var list []Contact
		if err := json.Unmarshal(data, &list); err != nil {
			// Attempt recovery from backup if main JSON corrupted
			bakPath := filePath + ".bak"
			if bakData, bErr := os.ReadFile(bakPath); bErr == nil && len(bakData) > 0 {
				_ = json.Unmarshal(bakData, &list)
			}
		}
		for _, c := range list {
			cleanedPhone := cleanPhone(c.Phone)
			if cleanedPhone != "" {
				c.Phone = cleanedPhone
				if strings.TrimSpace(c.Name) == "" || c.Name == "-" || c.Name == cleanedPhone {
					c.Name = "+" + cleanedPhone
				}
				s.contacts[cleanedPhone] = c
			}
		}
	}

	return s, nil
}

// Clear wipes all stored contacts and persists the empty list with an audit trail (D17)
func (s *Store) Clear() error {
	s.mu.Lock()
	count := len(s.contacts)
	s.contacts = make(map[string]Contact)
	err := s.persist()
	s.mu.Unlock()

	security.LogAudit("CONTACTS_PURGED", "internal", "system", fmt.Sprintf("All contacts purged from store (%d contacts removed)", count), "warning")
	return err
}

func (s *Store) populateDefaults() {
	now := time.Now()
	s.contacts["14155552671"] = Contact{
		Phone:     "14155552671",
		Name:      "Alice Smith",
		Email:     "alice@example.com",
		Tags:      []string{"VIP", "Customer"},
		Notes:     "Interested in enterprise plans",
		CreatedAt: now,
	}
	s.contacts["14155552672"] = Contact{
		Phone:     "14155552672",
		Name:      "Bob Jones",
		Email:     "bob@example.com",
		Tags:      []string{"Lead"},
		Notes:     "Requested pricing details",
		CreatedAt: now,
	}
}

// List returns all contacts sorted by name
func (s *Store) List() []Contact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Contact, 0, len(s.contacts))
	for _, c := range s.contacts {
		result = append(result, c)
	}
	return result
}

// Get finds a contact by phone number
func (s *Store) Get(phone string) (*Contact, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	cleaned := cleanPhone(phone)
	if c, ok := s.contacts[cleaned]; ok {
		return &c, true
	}

	// Fallback 1: check raw digits
	rawDigits := regexp.MustCompile(`[^0-9]`).ReplaceAllString(phone, "")
	if c, ok := s.contacts[rawDigits]; ok {
		return &c, true
	}

	// Fallback 2: match by last 10 digits
	if len(rawDigits) >= 10 {
		suffix := rawDigits[len(rawDigits)-10:]
		for _, c := range s.contacts {
			if strings.HasSuffix(c.Phone, suffix) {
				return &c, true
			}
		}
	}

	return nil, false
}

// Save creates or updates a contact
func (s *Store) Save(c Contact) error {
	cleaned := cleanPhone(c.Phone)
	if len(cleaned) < 7 {
		return errors.New("invalid phone number (must contain at least 7 digits)")
	}
	c.Phone = cleaned

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.contacts[cleaned]
	if exists {
		c.CreatedAt = existing.CreatedAt
		if c.LastContactedAt == nil {
			c.LastContactedAt = existing.LastContactedAt
		}
		if strings.TrimSpace(c.Name) == "" || c.Name == c.Phone || c.Name == "+"+c.Phone || c.Name == "-" {
			if strings.TrimSpace(existing.Name) != "" && existing.Name != existing.Phone && existing.Name != "+"+existing.Phone && existing.Name != "-" {
				c.Name = existing.Name
			}
		}
	} else {
		c.CreatedAt = time.Now()
	}

	if strings.TrimSpace(c.Name) == "" || c.Name == "-" {
		c.Name = "+" + c.Phone
	}

	s.contacts[cleaned] = c
	return s.persist()
}

// Delete removes a contact by phone
func (s *Store) Delete(phone string) bool {
	cleaned := cleanPhone(phone)

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.contacts[cleaned]; exists {
		delete(s.contacts, cleaned)
		_ = s.persist()
		return true
	}
	return false
}

// UpdateLastContacted updates the interaction timestamp
func (s *Store) UpdateLastContacted(phone string) {
	cleaned := cleanPhone(phone)

	s.mu.Lock()
	defer s.mu.Unlock()

	if c, exists := s.contacts[cleaned]; exists {
		now := time.Now()
		c.LastContactedAt = &now
		s.contacts[cleaned] = c
		_ = s.persist()
	}
}

// ToggleDND flips the Do Not Disturb status for a contact
func (s *Store) ToggleDND(phone string) (bool, error) {
	cleaned := cleanPhone(phone)

	s.mu.Lock()
	defer s.mu.Unlock()

	c, exists := s.contacts[cleaned]
	if !exists {
		return false, errors.New("contact not found")
	}

	c.IsDND = !c.IsDND
	s.contacts[cleaned] = c
	_ = s.persist()
	return c.IsDND, nil
}

// PauseBot pauses automated replies for this contact for human handover
func (s *Store) PauseBot(phone string, duration time.Duration) {
	cleaned := cleanPhone(phone)
	s.mu.Lock()
	defer s.mu.Unlock()

	c, exists := s.contacts[cleaned]
	if !exists {
		c = Contact{
			Phone:     cleaned,
			Name:      cleaned,
			CreatedAt: time.Now(),
		}
	}
	until := time.Now().Add(duration)
	c.BotPausedUntil = &until
	c.NeedsAttention = true
	s.contacts[cleaned] = c
	_ = s.persist()
}

// ResumeBot re-enables automated replies for this contact
func (s *Store) ResumeBot(phone string) {
	cleaned := cleanPhone(phone)
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, exists := s.contacts[cleaned]; exists {
		c.BotPausedUntil = nil
		c.NeedsAttention = false
		s.contacts[cleaned] = c
		_ = s.persist()
	}
}

// IsBotPaused checks if auto-replies are paused for this contact
func (s *Store) IsBotPaused(phone string) bool {
	cleaned := cleanPhone(phone)
	s.mu.RLock()
	defer s.mu.RUnlock()

	if c, exists := s.contacts[cleaned]; exists {
		if c.BotPausedUntil != nil && time.Now().Before(*c.BotPausedUntil) {
			return true
		}
	}
	return false
}

// IsLID reports whether a string represents a 13-18 digit WhatsApp Linked Device ID (LID)
func IsLID(str string) bool {
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

// FixPhoneNumbers iterates over stored contacts and migrates any LID phone keys to real phone numbers
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

	updated := false
	for phone, c := range s.contacts {
		resolved := resolver(phone)
		if resolved != "" && resolved != phone {
			delete(s.contacts, phone)
			c.Phone = resolved
			if c.Name == phone || c.Name == "+"+phone || IsLID(c.Name) || c.Name == "" || c.Name == "-" {
				if getName != nil {
					realName := getName(resolved)
					if realName != "" && !IsLID(realName) {
						c.Name = realName
					} else {
						c.Name = "+" + resolved
					}
				} else {
					c.Name = "+" + resolved
				}
			}
			s.contacts[resolved] = c
			updated = true
		} else {
			// Real phone number, check if its Name is an LID or invalid
			if IsLID(c.Name) || c.Name == "" || c.Name == "-" {
				if getName != nil {
					realName := getName(c.Phone)
					if realName != "" && !IsLID(realName) {
						c.Name = realName
						s.contacts[phone] = c
						updated = true
					} else if c.Name != "+"+c.Phone {
						c.Name = "+" + c.Phone
						s.contacts[phone] = c
						updated = true
					}
				} else if c.Name != "+"+c.Phone {
					c.Name = "+" + c.Phone
					s.contacts[phone] = c
					updated = true
				}
			}
		}
	}
	if updated {
		_ = s.persist()
	}
}

// GetByTag filters contacts belonging to a specific tag
func (s *Store) GetByTag(tag string) []Contact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tag = strings.TrimSpace(strings.ToLower(tag))
	var result []Contact
	for _, c := range s.contacts {
		for _, t := range c.Tags {
			if strings.ToLower(t) == tag {
				result = append(result, c)
				break
			}
		}
	}
	return result
}

// GetAllTags retrieves distinct list of tags used across contacts
func (s *Store) GetAllTags() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tagMap := make(map[string]struct{})
	for _, c := range s.contacts {
		for _, t := range c.Tags {
			t = strings.TrimSpace(t)
			if t != "" {
				tagMap[t] = struct{}{}
			}
		}
	}

	tags := make([]string, 0, len(tagMap))
	for t := range tagMap {
		tags = append(tags, t)
	}
	return tags
}

// Search queries contacts by phone, name, email, or notes
func (s *Store) Search(query string) []Contact {
	s.mu.RLock()
	defer s.mu.RUnlock()

	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return s.List()
	}

	var matched []Contact
	for _, c := range s.contacts {
		if strings.Contains(strings.ToLower(c.Name), query) ||
			strings.Contains(c.Phone, query) ||
			strings.Contains(strings.ToLower(c.Email), query) ||
			strings.Contains(strings.ToLower(c.Notes), query) {
			matched = append(matched, c)
		}
	}
	return matched
}

// CSVImportResult details the outcome of a bulk CSV contact import (C19)
type CSVImportResult struct {
	Total      int      `json:"total"`
	Added      int      `json:"added"`
	Updated    int      `json:"updated"`
	Skipped    int      `json:"skipped"`
	Warnings   []string `json:"warnings,omitempty"`
	Overwrites []string `json:"overwrites,omitempty"`
}

// ImportCSV parses and stores contacts from CSV reader (phone, name, email, tags, notes)
func (s *Store) ImportCSV(r io.Reader) (CSVImportResult, error) {
	var result CSVImportResult
	result.Warnings = make([]string, 0)
	result.Overwrites = make([]string, 0)

	reader := csv.NewReader(r)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return result, fmt.Errorf("failed to parse CSV: %w", err)
	}


	if len(records) == 0 {
		return result, errors.New("empty CSV file")
	}

	header := records[0]
	phoneIdx := -1
	nameIdx := -1
	emailIdx := -1
	tagsIdx := -1
	notesIdx := -1

	for i, col := range header {
		lower := strings.ToLower(strings.TrimSpace(col))
		switch lower {
		case "phone", "number", "mobile":
			phoneIdx = i
		case "name", "fullname", "contact":
			nameIdx = i
		case "email":
			emailIdx = i
		case "tags", "tag":
			tagsIdx = i
		case "notes", "note":
			notesIdx = i
		}
	}

	startRow := 0
	if phoneIdx != -1 {
		startRow = 1
	} else {
		phoneIdx = 0
		nameIdx = 1
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for i := startRow; i < len(records); i++ {
		row := records[i]
		rowNum := i + 1

		// Check if entire row is empty
		allEmpty := true
		for _, cell := range row {
			if strings.TrimSpace(cell) != "" {
				allEmpty = false
				break
			}
		}
		if allEmpty {
			result.Skipped++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Row %d: skipped empty row", rowNum))
			continue
		}

		if len(row) <= phoneIdx {
			result.Skipped++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Row %d: missing phone number column", rowNum))
			continue
		}

		rawPhone := row[phoneIdx]
		phone := cleanPhone(rawPhone)
		if len(phone) < 7 {
			result.Skipped++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Row %d: invalid phone %q (must contain at least 7 digits)", rowNum, rawPhone))
			continue
		}

		name := ""
		if nameIdx != -1 && len(row) > nameIdx && strings.TrimSpace(row[nameIdx]) != "" {
			name = strings.TrimSpace(row[nameIdx])
		}
		if name == "" || name == "-" || name == phone {
			name = "+" + phone
		}

		email := ""
		if emailIdx != -1 && len(row) > emailIdx {
			email = strings.TrimSpace(row[emailIdx])
		}

		notes := ""
		if notesIdx != -1 && len(row) > notesIdx {
			notes = strings.TrimSpace(row[notesIdx])
		}

		var tags []string
		if tagsIdx != -1 && len(row) > tagsIdx && strings.TrimSpace(row[tagsIdx]) != "" {
			tagStr := strings.ReplaceAll(row[tagsIdx], ",", ";")
			parts := strings.Split(tagStr, ";")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					tags = append(tags, p)
				}
			}
		}

		existing, exists := s.contacts[phone]
		if exists {
			result.Updated++
			result.Overwrites = append(result.Overwrites, fmt.Sprintf("Row %d: updated existing contact +%s (%s)", rowNum, phone, existing.Name))
			// Preserve created timestamp and bot pauses
			c := Contact{
				Phone:           phone,
				Name:            name,
				Email:           email,
				Tags:            tags,
				Notes:           notes,
				IsDND:           existing.IsDND,
				BotPausedUntil:  existing.BotPausedUntil,
				NeedsAttention:  existing.NeedsAttention,
				CreatedAt:       existing.CreatedAt,
				LastContactedAt: existing.LastContactedAt,
			}
			s.contacts[phone] = c
		} else {
			result.Added++
			c := Contact{
				Phone:     phone,
				Name:      name,
				Email:     email,
				Tags:      tags,
				Notes:     notes,
				CreatedAt: time.Now(),
			}
			s.contacts[phone] = c
		}
	}

	result.Total = result.Added + result.Updated + result.Skipped
	_ = s.persist()
	return result, nil
}


// ExportCSV writes all contacts as CSV to the provided writer
func (s *Store) ExportCSV(w io.Writer) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	writer := csv.NewWriter(w)
	defer writer.Flush()

	if err := writer.Write([]string{"phone", "name", "email", "tags", "notes"}); err != nil {
		return err
	}

	for _, c := range s.contacts {
		tagsStr := strings.Join(c.Tags, ";")
		row := []string{c.Phone, c.Name, c.Email, tagsStr, c.Notes}
		if err := writer.Write(row); err != nil {
			return err
		}
	}

	return nil
}

func (s *Store) persist() error {
	list := make([]Contact, 0, len(s.contacts))
	for _, c := range s.contacts {
		list = append(list, c)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	// Create backup of existing file if present (D15)
	if _, err := os.Stat(s.filePath); err == nil {
		bakFile := s.filePath + ".bak"
		existingData, rErr := os.ReadFile(s.filePath)
		if rErr == nil && len(existingData) > 0 {
			_ = os.WriteFile(bakFile, existingData, 0600)
		}
	}

	// D15: Atomic file replacement to prevent data corruption during abrupt shutdowns
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

// CleanPhone normalizes phone strings into standard international digits
func CleanPhone(phone string) string {
	return cleanPhone(phone)
}

func cleanPhone(phone string) string {
	hadPlus := strings.HasPrefix(strings.TrimSpace(phone), "+")
	reg := regexp.MustCompile(`[^0-9]`)
	clean := reg.ReplaceAllString(phone, "")
	// If starts with 0 and has 11 digits (e.g. 09876543210), strip the trunk 0
	if strings.HasPrefix(clean, "0") && len(clean) == 11 {
		clean = clean[1:]
	}
	// If no explicit '+' country code was specified, 10 digits starting with 6, 7, 8, or 9 (standard Indian mobile) defaults to 91
	if !hadPlus && len(clean) == 10 && (clean[0] == '6' || clean[0] == '7' || clean[0] == '8' || clean[0] == '9') {
		clean = "91" + clean
	}
	return clean
}
