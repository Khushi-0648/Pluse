package templates

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// Template represents a reusable message template with variable placeholders
type Template struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Category    string    `json:"category"`
	Content     string    `json:"content"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Store provides thread-safe persistent CRUD for message templates
type Store struct {
	mu        sync.RWMutex
	filePath  string
	templates map[string]Template
}

// NewStore initializes the template store from a JSON file
func NewStore(filePath string) (*Store, error) {
	if filePath == "" {
		filePath = "templates.json"
	}

	s := &Store{
		filePath:  filePath,
		templates: make(map[string]Template),
	}

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		s.populateDefaults()
		_ = s.persist()
		return s, nil
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read templates file %s: %w", filePath, err)
	}

	if len(data) > 0 {
		var list []Template
		if err := json.Unmarshal(data, &list); err == nil {
			for _, t := range list {
				s.templates[t.ID] = t
			}
		}
	}

	return s, nil
}

func (s *Store) populateDefaults() {
	now := time.Now()
	s.templates["welcome"] = Template{
		ID:          "welcome",
		Name:        "Welcome Onboarding",
		Category:    "Onboarding",
		Content:     "👋 Hello {{name}}! Welcome to our service. We are delighted to have you onboard. Let us know if you need any assistance!",
		Description: "Sent to new signups or contacts",
		CreatedAt:   now,
	}

	s.templates["reminder"] = Template{
		ID:          "reminder",
		Name:        "Appointment Reminder",
		Category:    "Reminders",
		Content:     "⏰ Hi {{name}}, this is a friendly reminder for your upcoming appointment scheduled for {{date}} at {{time}}. Please reply *YES* to confirm.",
		Description: "Appointment confirmation reminder",
		CreatedAt:   now,
	}

	s.templates["order_update"] = Template{
		ID:          "order_update",
		Name:        "Order Status Update",
		Category:    "Transactional",
		Content:     "📦 Great news {{name}}! Your order #{{order_id}} has been shipped and is on its way. Track your package here: {{tracking_url}}",
		Description: "Sent when order ships",
		CreatedAt:   now,
	}

	s.templates["support_followup"] = Template{
		ID:          "support_followup",
		Name:        "Support Ticket Follow-Up",
		Category:    "Support",
		Content:     "🛠️ Hello {{name}}, following up on your recent support inquiry. Did our solution resolve your issue? Reply with any additional questions!",
		Description: "Customer service follow up",
		CreatedAt:   now,
	}

	s.templates["promo_discount"] = Template{
		ID:          "promo_discount",
		Name:        "Special Discount Offer",
		Category:    "Marketing",
		Content:     "🎉 Exclusive for you, {{name}}! Enjoy 20% off your next purchase using code *SAVE20* at checkout today only: {{promo_link}}",
		Description: "Marketing promotional discount",
		CreatedAt:   now,
	}
}

// List returns all templates
func (s *Store) List() []Template {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]Template, 0, len(s.templates))
	for _, t := range s.templates {
		result = append(result, t)
	}
	return result
}

// Get finds a template by its ID
func (s *Store) Get(id string) (*Template, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.templates[id]
	if !ok {
		return nil, false
	}
	return &t, true
}

// Save creates or updates a template
func (s *Store) Save(t Template) error {
	t.ID = strings.TrimSpace(t.ID)
	if t.ID == "" {
		return errors.New("template ID is required")
	}
	if t.Name == "" {
		t.Name = t.ID
	}
	if t.Category == "" {
		t.Category = "General"
	}
	if strings.TrimSpace(t.Content) == "" {
		return errors.New("template content cannot be empty")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.templates[t.ID]
	if exists {
		t.CreatedAt = existing.CreatedAt
	} else {
		t.CreatedAt = time.Now()
	}

	s.templates[t.ID] = t
	return s.persist()
}

// Delete removes a template
func (s *Store) Delete(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.templates[id]; exists {
		delete(s.templates, id)
		_ = s.persist()
		return true
	}
	return false
}

// Render interpolates variables into the template content
func (s *Store) Render(id string, vars map[string]string) (string, error) {
	t, ok := s.Get(id)
	if !ok {
		return "", fmt.Errorf("template '%s' not found", id)
	}

	content := t.Content
	for k, v := range vars {
		content = strings.ReplaceAll(content, "{{"+k+"}}", v)
	}
	return content, nil
}

func (s *Store) persist() error {
	list := make([]Template, 0, len(s.templates))
	for _, t := range s.templates {
		list = append(list, t)
	}

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(s.filePath, data, 0644)
}
