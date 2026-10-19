package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultModel is the latest recommended Google Gemini Flash model
const DefaultModel = "gemini-3.8-flash"

// NormalizeModel updates deprecated models (e.g., gemini-2.5, gemini-1.5) to the latest stable model
func NormalizeModel(model string) string {
	model = strings.TrimSpace(model)
	model = strings.TrimPrefix(model, "models/")
	if model == "" || strings.HasPrefix(model, "gemini-2.") || strings.HasPrefix(model, "gemini-1.") {
		return DefaultModel
	}
	return model
}

// Config defines AI smart-reply settings
type Config struct {
	Enabled      bool   `json:"enabled" yaml:"enabled"`
	APIKey       string `json:"api_key" yaml:"api_key"`
	Model        string `json:"model" yaml:"model"` // defaults to "gemini-3.8-flash"
	SystemPrompt string `json:"system_prompt" yaml:"system_prompt"`
	MaxTokens    int    `json:"max_tokens" yaml:"max_tokens"`
}

// Engine handles AI conversational generation
type Engine struct {
	mu         sync.RWMutex
	cfg        Config
	httpClient *http.Client
}

// NewEngine creates a new AI smart-reply engine
func NewEngine(cfg Config) *Engine {
	cfg.Model = NormalizeModel(cfg.Model)
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = "You are a friendly, helpful, and concise WhatsApp assistant. Answer concisely (1-3 sentences maximum) suitable for mobile chat. Use emojis occasionally. Never write long essays."
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 250
	}

	return &Engine{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

// UpdateConfig updates the engine configuration live
func (e *Engine) UpdateConfig(cfg Config) {
	e.mu.Lock()
	defer e.mu.Unlock()

	cfg.Model = NormalizeModel(cfg.Model)
	if cfg.SystemPrompt == "" {
		cfg.SystemPrompt = e.cfg.SystemPrompt
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 250
	}
	e.cfg = cfg
}

// GetConfig returns a copy of current AI config
func (e *Engine) GetConfig() Config {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg
}

// IsEnabled checks if AI reply is turned on
func (e *Engine) IsEnabled() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.cfg.Enabled
}

// TestConnection directly tests the Gemini API key and model against the official API
func (e *Engine) TestConnection(ctx context.Context, apiKey, model string) (string, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return "", fmt.Errorf("API key is required")
	}
	model = NormalizeModel(model)
	testCfg := Config{
		Enabled:      true,
		APIKey:       apiKey,
		Model:        model,
		SystemPrompt: "You are an automated validator. Keep response to 5 words or fewer.",
		MaxTokens:    50,
	}
	return e.callGemini(ctx, testCfg, "Tester", "Respond with 'Connected successfully!'")
}

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent  `json:"system_instruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	GenerationConfig  map[string]any  `json:"generationConfig,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// GenerateReply calls Gemini API or falls back to smart intent heuristic with multi-turn conversation context (C8)
func (e *Engine) GenerateReply(ctx context.Context, senderName, incomingText string, conversationHistory ...string) (string, error) {
	e.mu.RLock()
	cfg := e.cfg
	e.mu.RUnlock()

	if !cfg.Enabled {
		return "", nil
	}

	// If API key is provided, use Google Gemini
	if cfg.APIKey != "" {
		reply, err := e.callGemini(ctx, cfg, senderName, incomingText, conversationHistory...)
		if err == nil && reply != "" {
			return reply, nil
		}
		fmt.Printf("[AI] Gemini API returned error (%v), falling back to heuristic reply.\n", err)
	}

	// Heuristic smart fallback
	return e.heuristicFallback(senderName, incomingText), nil
}

func (e *Engine) callGemini(ctx context.Context, cfg Config, senderName, incomingText string, conversationHistory ...string) (string, error) {
	model := NormalizeModel(cfg.Model)
	var promptBuilder strings.Builder
	if len(conversationHistory) > 0 && strings.TrimSpace(conversationHistory[0]) != "" {
		promptBuilder.WriteString("Recent Conversation Context:\n")
		promptBuilder.WriteString(conversationHistory[0])
		promptBuilder.WriteString("\n\n")
	}
	promptBuilder.WriteString(fmt.Sprintf("Message from %s: \"%s\"\nProvide a helpful, friendly, natural WhatsApp reply:", senderName, incomingText))
	userPrompt := promptBuilder.String()

	// 1. Try modern Interactions API first (recommended by Google for Gemini 3.x)
	reply, errInteractions := e.callInteractionsAPI(ctx, cfg.APIKey, model, cfg.SystemPrompt, userPrompt)
	if errInteractions == nil && strings.TrimSpace(reply) != "" {
		return reply, nil
	}

	// 2. Fallback to generateContent
	reply, errGen := e.callGenerateContent(ctx, cfg.APIKey, model, cfg.SystemPrompt, userPrompt, cfg.MaxTokens)
	if errGen == nil && strings.TrimSpace(reply) != "" {
		return reply, nil
	}

	if errInteractions != nil {
		return "", fmt.Errorf("gemini API error: %v", errInteractions)
	}
	return "", fmt.Errorf("gemini API error: %v", errGen)
}

// callInteractionsAPI calls the Google Gemini Interactions API (POST /v1beta/interactions)
func (e *Engine) callInteractionsAPI(ctx context.Context, apiKey, model, systemPrompt, input string) (string, error) {
	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/interactions?key=%s", url.QueryEscape(apiKey))

	reqBody := map[string]any{
		"model":              model,
		"input":              input,
		"system_instruction": systemPrompt,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var res struct {
		OutputText string `json:"output_text"`
		Steps      []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"steps"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error,omitempty"`
	}

	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return "", err
	}

	if res.Error != nil && res.Error.Message != "" {
		return "", fmt.Errorf("%s", res.Error.Message)
	}

	if strings.TrimSpace(res.OutputText) != "" {
		return strings.TrimSpace(res.OutputText), nil
	}

	for _, step := range res.Steps {
		for _, c := range step.Content {
			if strings.TrimSpace(c.Text) != "" {
				return strings.TrimSpace(c.Text), nil
			}
		}
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return "", fmt.Errorf("no output text in interactions response")
}

// callGenerateContent calls the standard generateContent endpoint
func (e *Engine) callGenerateContent(ctx context.Context, apiKey, model, systemPrompt, userPrompt string, maxTokens int) (string, error) {
	apiURL := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", model, url.QueryEscape(apiKey))

	if maxTokens <= 0 {
		maxTokens = 250
	}

	reqBody := geminiRequest{
		SystemInstruction: &geminiContent{
			Parts: []geminiPart{{Text: systemPrompt}},
		},
		Contents: []geminiContent{
			{
				Role:  "user",
				Parts: []geminiPart{{Text: userPrompt}},
			},
		},
		GenerationConfig: map[string]any{
			"maxOutputTokens": maxTokens,
			"temperature":     0.7,
			"thinkingConfig": map[string]any{
				"thinkingLevel": "low",
			},
		},
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", apiKey)

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var geminiResp geminiResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return "", err
	}

	if geminiResp.Error != nil && geminiResp.Error.Message != "" {
		return "", fmt.Errorf("%s", geminiResp.Error.Message)
	}

	if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
		return strings.TrimSpace(geminiResp.Candidates[0].Content.Parts[0].Text), nil
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return "", fmt.Errorf("no reply text generated by Gemini")
}

// heuristicFallback provides an intelligent contextual response without external API calls
func (e *Engine) heuristicFallback(senderName, incomingText string) string {
	lower := strings.ToLower(incomingText)
	displayName := senderName
	if displayName == "" {
		displayName = "there"
	}

	if strings.Contains(lower, "price") || strings.Contains(lower, "cost") || strings.Contains(lower, "rate") {
		return fmt.Sprintf("Hello %s! 💰 Thanks for your interest. You can type *2* or *pricing* to view our current plans, or I'll respond personally soon!", displayName)
	}

	if strings.Contains(lower, "?") || strings.HasPrefix(lower, "how") || strings.HasPrefix(lower, "what") || strings.HasPrefix(lower, "why") || strings.HasPrefix(lower, "can you") {
		return fmt.Sprintf("Hi %s! 👋 Thanks for asking. I've received your query: \"%s\". I'll review and get back to you shortly! (Type *menu* for quick options)", displayName, incomingText)
	}

	if strings.Contains(lower, "help") || strings.Contains(lower, "support") {
		return fmt.Sprintf("Hi %s! 🤝 How can I assist you today? Type *menu* to explore services, or reply *4* to connect with support.", displayName)
	}

	return fmt.Sprintf("Hi %s! 👋 Thanks for reaching out. I'm currently stepping away but I'll reply to your message soon! (Type *menu* for instant self-service options)", displayName)
}
