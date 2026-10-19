package campaign

import (
	"context"
	"encoding/csv"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/bot"
	"whatsapp-automation/pkg/client"
	"whatsapp-automation/pkg/history"
)

// Contact represents a broadcast recipient with personalized fields
type Contact struct {
	Phone     string            `json:"phone"`
	Name      string            `json:"name"`
	Variables map[string]string `json:"variables,omitempty"`
}

// BroadcastConfig configures the pacing and anti-ban safeguards
type BroadcastConfig struct {
	MinDelaySeconds int `json:"min_delay_seconds"`
	MaxDelaySeconds int `json:"max_delay_seconds"`
	BatchSize       int `json:"batch_size"`        // Pause every N messages
	BatchPauseSec   int `json:"batch_pause_sec"`   // Pause duration between batches
}

// DefaultBroadcastConfig returns safe pacing parameters
func DefaultBroadcastConfig() BroadcastConfig {
	return BroadcastConfig{
		MinDelaySeconds: 4,
		MaxDelaySeconds: 10,
		BatchSize:       25,
		BatchPauseSec:   30,
	}
}

// BroadcastResult details the outcome of a bulk messaging run
type BroadcastResult struct {
	TotalContacts int           `json:"total_contacts"`
	SuccessCount  int           `json:"success_count"`
	FailureCount  int           `json:"failure_count"`
	Duration      time.Duration `json:"duration"`
	Failures      []string      `json:"failures,omitempty"`
}

// Broadcaster manages safe bulk message delivery
type Broadcaster struct {
	client    *client.WhatsAppClient
	histStore *history.Store
	cancelMu  sync.Mutex
	cancelFn  context.CancelFunc
}

// NewBroadcaster creates a new Broadcaster instance
func NewBroadcaster(c *client.WhatsAppClient) *Broadcaster {
	return &Broadcaster{client: c}
}

// CancelBroadcast aborts any active broadcast campaign
func (b *Broadcaster) CancelBroadcast() {
	b.cancelMu.Lock()
	defer b.cancelMu.Unlock()
	if b.cancelFn != nil {
		b.cancelFn()
		b.cancelFn = nil
	}
}

// SetHistoryStore sets optional message history audit store
func (b *Broadcaster) SetHistoryStore(hs *history.Store) {
	b.histStore = hs
}

// LoadContactsFromCSV parses contacts from a CSV file (phone, name, [var1, var2...])
func LoadContactsFromCSV(filePath string) ([]Contact, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open contacts csv: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("failed to parse contacts csv: %w", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("contacts file is empty")
	}

	var contacts []Contact
	header := records[0]

	// Determine column indices
	phoneIdx := -1
	nameIdx := -1
	for i, col := range header {
		clean := strings.ToLower(strings.TrimSpace(col))
		if clean == "phone" || clean == "number" || clean == "mobile" {
			phoneIdx = i
		} else if clean == "name" || clean == "fullname" {
			nameIdx = i
		}
	}

	startRow := 0
	if phoneIdx != -1 {
		startRow = 1 // Header detected
	} else {
		// No explicit header, default 0: phone, 1: name
		phoneIdx = 0
		nameIdx = 1
	}

	for i := startRow; i < len(records); i++ {
		row := records[i]
		if len(row) <= phoneIdx || strings.TrimSpace(row[phoneIdx]) == "" {
			continue
		}

		phone := strings.TrimSpace(row[phoneIdx])
		name := ""
		if nameIdx != -1 && len(row) > nameIdx {
			name = strings.TrimSpace(row[nameIdx])
		}

		vars := make(map[string]string)
		for colIdx, val := range row {
			if colIdx < len(header) {
				key := strings.ToLower(strings.TrimSpace(header[colIdx]))
				vars[key] = strings.TrimSpace(val)
			}
		}

		contacts = append(contacts, Contact{
			Phone:     phone,
			Name:      name,
			Variables: vars,
		})
	}

	return contacts, nil
}

// RunBroadcast sends personalized messages to all contacts with anti-ban delays
func (b *Broadcaster) RunBroadcast(ctx context.Context, contacts []Contact, template string, cfg BroadcastConfig, onProgress func(current, total, success, failed int, phone string, err error)) BroadcastResult {
	runCtx, cancel := context.WithCancel(ctx)
	b.cancelMu.Lock()
	b.cancelFn = cancel
	b.cancelMu.Unlock()
	defer func() {
		b.cancelMu.Lock()
		b.cancelFn = nil
		b.cancelMu.Unlock()
		cancel()
	}()

	start := time.Now()
	result := BroadcastResult{
		TotalContacts: len(contacts),
	}

	if cfg.MinDelaySeconds <= 0 {
		cfg.MinDelaySeconds = 3
	}
	if cfg.MaxDelaySeconds < cfg.MinDelaySeconds {
		cfg.MaxDelaySeconds = cfg.MinDelaySeconds + 4
	}

	consecutiveErrors := 0
	const maxConsecutiveErrors = 5

	for i, c := range contacts {
		select {
		case <-runCtx.Done():
			result.Duration = time.Since(start)
			return result
		default:
		}

		// Personalize message with full token rendering ({{name}}, {{first_name}}, {{greeting}}, and custom CSV variables)
		body := bot.RenderTemplate(template, c.Name, c.Phone, template, c.Variables)

		// Send message
		msgID, err := b.client.SendTextMessage(runCtx, c.Phone, body)
		if err != nil {
			result.FailureCount++
			consecutiveErrors++
			errMsg := fmt.Sprintf("%s: %v", c.Phone, err)
			result.Failures = append(result.Failures, errMsg)
			if onProgress != nil {
				onProgress(i+1, len(contacts), result.SuccessCount, result.FailureCount, c.Phone, err)
			}

			// Circuit breaker: abort if 5 consecutive messages fail to prevent account banning
			if consecutiveErrors >= maxConsecutiveErrors {
				abortMsg := fmt.Sprintf("Circuit breaker triggered: %d consecutive failures. Broadcast halted immediately to safeguard WhatsApp account from policy ban.", consecutiveErrors)
				result.Failures = append(result.Failures, abortMsg)
				if onProgress != nil {
					onProgress(i+1, len(contacts), result.SuccessCount, result.FailureCount, c.Phone, fmt.Errorf("%s", abortMsg))
				}
				result.Duration = time.Since(start)
				return result
			}
		} else {
			consecutiveErrors = 0
			result.SuccessCount++
			if onProgress != nil {
				onProgress(i+1, len(contacts), result.SuccessCount, result.FailureCount, c.Phone, nil)
			}
		}

		if b.histStore != nil {
			status := "sent"
			errMsg := ""
			if err != nil {
				status = "failed"
				errMsg = err.Error()
			}
			b.histStore.Record(history.MessageRecord{
				ID:          msgID,
				Direction:   history.DirectionOutgoing,
				Type:        history.TypeBroadcast,
				Timestamp:   time.Now(),
				SenderPhone: "Me",
				SenderName:  "WhatsApp Campaign",
				Recipient:   c.Phone,
				Text:        body,
				Status:      status,
				Error:       errMsg,
			})
		}

		// Pause between batches
		if cfg.BatchSize > 0 && (i+1)%cfg.BatchSize == 0 && (i+1) < len(contacts) {
			pauseDuration := time.Duration(cfg.BatchPauseSec) * time.Second
			if pauseDuration <= 0 {
				pauseDuration = 30 * time.Second
			}
			fmt.Printf("\n[Broadcast] Reached batch size of %d. Pausing for %v to cool down...\n", cfg.BatchSize, pauseDuration)
			select {
			case <-time.After(pauseDuration):
			case <-ctx.Done():
				result.Duration = time.Since(start)
				return result
			}
		} else if (i + 1) < len(contacts) {
			// Anti-ban randomized jitter delay
			diff := cfg.MaxDelaySeconds - cfg.MinDelaySeconds
			delaySec := cfg.MinDelaySeconds
			if diff > 0 {
				delaySec += rand.Intn(diff + 1)
			}
			delay := time.Duration(delaySec)*time.Second + time.Duration(rand.Intn(999))*time.Millisecond
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				result.Duration = time.Since(start)
				return result
			}
		}
	}

	result.Duration = time.Since(start)
	return result
}
