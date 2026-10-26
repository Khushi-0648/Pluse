package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/securestore"
)

// ContactVariables are available when a template is sent to a saved contact.
var ContactVariables = []string{"email", "notes"}

// Template is a reusable message.
type Template struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Content     string    `json:"content"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Store persists templates.
type Store struct {
	mu        sync.RWMutex
	writeMu   sync.Mutex // one save at a time, so an older save never overwrites a newer one
	filePath  string
	box       *securestore.Box
	templates map[string]Template
}

var idRe = regexp.MustCompile(`[^a-z0-9_\-]+`)

// SlugID turns a name into a template ID.
func SlugID(name string) string {
	id := strings.Trim(idRe.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "_"), "_")
	if len(id) > 48 {
		id = id[:48]
	}
	return id
}

// NewStore loads templates from filePath, creating the defaults on first run.
func NewStore(filePath string) (*Store, error) {
	if filePath == "" {
		filePath = "templates.json"
	}
	s := &Store{filePath: filePath, box: securestore.Default(), templates: map[string]Template{}}
	data, err := s.box.ReadFile(filePath)
	if errors.Is(err, os.ErrNotExist) {
		s.populateDefaults()
		return s, s.persist()
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read templates %s: %w", filePath, err)
	}
	var list []Template
	if len(data) > 0 {
		if err := json.Unmarshal(data, &list); err != nil {
			// Fall back to the previous copy instead of refusing to start.
			bak, bErr := s.box.ReadBackup(filePath)
			if bErr != nil || json.Unmarshal(bak, &list) != nil {
				return nil, fmt.Errorf("templates file %s is damaged: %w", filePath, err)
			}
			fmt.Printf("[Data] %s was damaged; loaded the backup copy.\n", filePath)
		}
	}
	for _, t := range list {
		if t.ID == "promo_discount" && strings.Contains(t.Content, "SAVE20") {
			continue // invented promo code shipped by older versions
		}
		if t.ID == "order_update" && strings.Contains(t.Content, "{{tracking_url}}") {
			continue // needed order data the app never has
		}
		s.templates[t.ID] = t
	}
	if len(list) != len(s.templates) {
		_ = s.persist()
	}
	return s, nil
}

func (s *Store) populateDefaults() {
	now := time.Now()
	add := func(id, name, cat, content, desc string) {
		s.templates[id] = Template{ID: id, Name: name, Category: cat, Content: content, Description: desc, CreatedAt: now, UpdatedAt: now}
	}
	add("welcome", "Welcome", "Onboarding",
		"Hello {{first_name}}, thank you for choosing {{business_name}}. Reply here any time if you need help.",
		"For new customers who agreed to hear from you")
	add("appointment_reminder", "Appointment reminder", "Reminders",
		"Hello {{first_name}}, this is a reminder about your upcoming appointment with {{business_name}}. Reply YES to confirm or let us know if you need to change it.",
		"Send the day before an appointment")
	add("follow_up", "Follow-up", "Support",
		"Hello {{first_name}}, we are following up on your recent message to {{business_name}}. Is there anything else we can help with?",
		"After a support conversation")
	add("hours_update", "Opening hours update", "Updates",
		"Hello {{first_name}}, our opening hours are now: {{business_hours}}. Thank you, {{business_name}}.",
		"When your hours change")
}

func (s *Store) persist() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.RLock()
	list := make([]Template, 0, len(s.templates))
	for _, t := range s.templates {
		list = append(list, t)
	}
	s.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return s.box.WriteFile(s.filePath, data)
}

// List returns templates sorted by category then name.
func (s *Store) List() []Template {
	s.mu.RLock()
	out := make([]Template, 0, len(s.templates))
	for _, t := range s.templates {
		out = append(out, t)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return out[i].Category < out[j].Category
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out
}

// Get returns a template by ID.
func (s *Store) Get(id string) (*Template, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.templates[id]
	if !ok {
		return nil, false
	}
	return &t, true
}

// Save validates and stores a template. Unknown variables are rejected.
func (s *Store) Save(t Template) (Template, error) {
	t.Name = strings.TrimSpace(t.Name)
	t.Content = strings.TrimSpace(strings.ReplaceAll(t.Content, "\r\n", "\n"))
	t.Category = strings.TrimSpace(t.Category)
	t.Description = strings.TrimSpace(t.Description)
	if t.Name == "" {
		return t, errors.New("Give the template a name.")
	}
	if t.Content == "" {
		return t, errors.New("Write the message text.")
	}
	if len(t.Content) > 4000 || len(t.Name) > 80 || len(t.Category) > 40 || len(t.Description) > 200 {
		return t, errors.New("The template is too long.")
	}
	if err := msgtemplate.ValidateOutgoing(t.Content, ContactVariables...); err != nil {
		return t, err
	}
	t.ID = SlugID(t.ID)
	isNew := t.ID == ""
	if isNew {
		t.ID = SlugID(t.Name)
	}
	if t.ID == "" {
		return t, errors.New("The template name must contain letters or numbers.")
	}
	if t.Category == "" {
		t.Category = "General"
	}
	now := time.Now()
	s.mu.Lock()
	if isNew {
		// A new template never replaces an existing one with a similar name.
		base := t.ID
		for n := 2; ; n++ {
			if _, taken := s.templates[t.ID]; !taken {
				break
			}
			t.ID = fmt.Sprintf("%s_%d", base, n)
		}
	}
	if ex, ok := s.templates[t.ID]; ok {
		t.CreatedAt = ex.CreatedAt
	} else {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	s.templates[t.ID] = t
	s.mu.Unlock()
	return t, s.persist()
}

// Delete removes a template.
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	_, ok := s.templates[id]
	delete(s.templates, id)
	s.mu.Unlock()
	if ok {
		_ = s.persist()
	}
	return ok
}
