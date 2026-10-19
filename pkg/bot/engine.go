package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatsapp-automation/pkg/ai"
	"whatsapp-automation/pkg/client"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/security"
)

// WebhookPayload represents the JSON sent to external webhooks
type WebhookPayload struct {
	Event     string    `json:"event"`
	MessageID string    `json:"message_id"`
	SenderJID string    `json:"sender_jid"`
	Phone     string    `json:"phone"`
	PushName  string    `json:"push_name"`
	ChatJID   string    `json:"chat_jid"`
	IsGroup   bool      `json:"is_group"`
	Text      string    `json:"text"`
	Timestamp time.Time `json:"timestamp"`
}

// Engine coordinates message events, rule evaluation, typing simulation, and auto-replies
type Engine struct {
	client        *client.WhatsAppClient
	cfg           *config.Config
	contactStore  *contacts.Store
	histStore     *history.Store
	aiEngine      *ai.Engine
	startTime     time.Time
	cooldowns     map[string]time.Time
	cooldownMu    sync.Mutex
	convMu        sync.RWMutex
	conversations map[string]*ConversationState
	httpClient    *http.Client
}

// ConversationState maintains conversational topic and state across multi-turn user messages (C8)
type ConversationState struct {
	LastRule      string    `json:"last_rule"`
	LastTopic     string    `json:"last_topic"`
	LastTimestamp time.Time `json:"last_timestamp"`
	Turns         int       `json:"turns"`
}

// NewEngine creates a new bot engine instance
func NewEngine(c *client.WhatsAppClient, cfg *config.Config, cs *contacts.Store, hs *history.Store, aiEng *ai.Engine) *Engine {
	return &Engine{
		client:        c,
		cfg:           cfg,
		contactStore:  cs,
		histStore:     hs,
		aiEngine:      aiEng,
		startTime:     time.Now(),
		cooldowns:     make(map[string]time.Time),
		conversations: make(map[string]*ConversationState),
		httpClient:    &http.Client{Timeout: 10 * time.Second},
	}
}

// Start registers the bot engine's message handler with the WhatsApp client
func (e *Engine) Start() {
	if e.client == nil {
		return
	}
	e.client.OnMessage(e.handleMessage)
	e.client.OnConnect(func() {
		e.syncWhatsAppContacts()
		if e.histStore != nil && e.client != nil {
			resolver := func(target string) string {
				return e.client.ResolveString(context.Background(), target)
			}
			nameResolver := func(target string) string {
				return e.client.GetContactName(context.Background(), target)
			}
			e.histStore.FixPhoneNumbers(resolver, nameResolver)
			if e.contactStore != nil {
				e.contactStore.FixPhoneNumbers(resolver, nameResolver)
			}
		}
	})
	e.client.OnHistorySync(e.handleHistorySync)
}

// syncWhatsAppContacts fetches all contacts from the connected WhatsApp account and saves them in contactStore
func (e *Engine) syncWhatsAppContacts() {
	if e.contactStore == nil || e.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	contactsMap, err := e.client.GetAllContacts(ctx)
	if err != nil {
		return
	}

	for jid, info := range contactsMap {
		phone := ""
		if jid.Server == types.DefaultUserServer && jid.User != "" {
			phone = contacts.CleanPhone(jid.User)
		} else if jid.Server == types.HiddenUserServer && jid.User != "" {
			resolved := e.client.ResolvePhoneNumber(ctx, jid)
			if resolved != "" && resolved != jid.User {
				phone = contacts.CleanPhone(resolved)
			}
		}

		if phone == "" || contacts.IsLID(phone) {
			continue
		}

		name := strings.TrimSpace(info.FullName)
		if name == "" {
			name = strings.TrimSpace(info.PushName)
		}
		if name == "" {
			name = strings.TrimSpace(info.BusinessName)
		}
		if name == "" {
			name = strings.TrimSpace(info.FirstName)
		}
		if contacts.IsLID(name) {
			name = ""
		}
		if name == "" {
			name = "+" + phone
		}

		_ = e.contactStore.Save(contacts.Contact{
			Phone:     phone,
			Name:      name,
			CreatedAt: time.Now(),
			Notes:     "WhatsApp Contact",
		})
	}
}

// handleHistorySync processes historical conversation sync blobs from WhatsApp
func (e *Engine) handleHistorySync(evt *events.HistorySync) {
	if evt == nil || evt.Data == nil {
		return
	}
	for _, conv := range evt.Data.GetConversations() {
		if conv == nil {
			continue
		}
		rawID := conv.GetID()
		if rawID == "" || strings.HasSuffix(rawID, "@g.us") || strings.HasSuffix(rawID, "@newsletter") {
			continue
		}

		// Resolve phone number from rawID (handles both @s.whatsapp.net and @lid)
		phone := strings.TrimSuffix(strings.TrimSuffix(rawID, "@s.whatsapp.net"), "@lid")
		phone = contacts.CleanPhone(phone)
		if phone == "" {
			continue
		}

		// If this is an LID, resolve to actual phone number
		if e.client != nil {
			resolved := e.client.ResolveString(context.Background(), rawID)
			if resolved != "" && resolved != rawID && resolved != phone {
				phone = contacts.CleanPhone(resolved)
			}
		}

		if phone == "" || contacts.IsLID(phone) {
			// Skip unresolvable internal device IDs
			continue
		}

		name := strings.TrimSpace(conv.GetDisplayName())
		if name == "" {
			name = strings.TrimSpace(conv.GetName())
		}
		if contacts.IsLID(name) {
			name = ""
		}
		if name == "" && e.client != nil {
			name = e.client.GetContactName(context.Background(), phone)
		}
		if name == "" {
			name = "+" + phone
		}

		if e.contactStore != nil {
			_ = e.contactStore.Save(contacts.Contact{
				Phone:     phone,
				Name:      name,
				CreatedAt: time.Now(),
				Notes:     "WhatsApp Contact",
			})
		}

		if e.histStore != nil {
			for _, hMsg := range conv.GetMessages() {
				if hMsg == nil {
					continue
				}
				webMsg := hMsg.GetMessage()
				if webMsg == nil || webMsg.GetKey() == nil {
					continue
				}
				text := ExtractMessageText(webMsg.GetMessage())
				if text == "" {
					continue
				}
				isFromMe := webMsg.GetKey().GetFromMe()
				ts := time.Unix(int64(webMsg.GetMessageTimestamp()), 0)
				if ts.IsZero() {
					ts = time.Now()
				}
				direction := history.DirectionIncoming
				senderPhone := phone
				recipient := "Me"
				if isFromMe {
					direction = history.DirectionOutgoing
					senderPhone = "Me"
					recipient = phone
				}
				e.histStore.Record(history.MessageRecord{
					ID:          webMsg.GetKey().GetID(),
					Direction:   direction,
					Type:        history.TypeUserMessage,
					SenderPhone: senderPhone,
					SenderName:  name,
					Recipient:   recipient,
					Text:        text,
					Timestamp:   ts,
				})
			}
		}
	}
}

// ExtractMessageText pulls clean text content from various WhatsApp message types
func ExtractMessageText(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}

	if msg.Conversation != nil && *msg.Conversation != "" {
		return *msg.Conversation
	}
	if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil {
		return *msg.ExtendedTextMessage.Text
	}
	if msg.ImageMessage != nil && msg.ImageMessage.Caption != nil {
		return *msg.ImageMessage.Caption
	}
	if msg.VideoMessage != nil && msg.VideoMessage.Caption != nil {
		return *msg.VideoMessage.Caption
	}
	if msg.DocumentMessage != nil && msg.DocumentMessage.Caption != nil {
		return *msg.DocumentMessage.Caption
	}

	return ""
}

// handleMessage processes an incoming message event
func (e *Engine) handleMessage(evt *events.Message) {
	// 1. Guard Clauses & Filters
	info := evt.Info
	if info.IsFromMe && e.cfg.Automation.IgnoreSelf {
		return
	}

	if info.Chat == types.StatusBroadcastJID && e.cfg.Automation.IgnoreBroadcasts {
		return
	}

	if info.Chat.Server == "newsletter" || info.Sender.Server == "newsletter" {
		return
	}

	if info.IsGroup && e.cfg.Automation.IgnoreGroups {
		return
	}

	text := ExtractMessageText(evt.Message)
	if text == "" {
		return
	}

	// 1. Resolve REAL user phone number (resolving WhatsApp Multi-Device LID to actual phone number)
	senderPhone := ""

	// Check SenderAlt (whatsmeow provides the real phone number here when Sender is a LID)
	if info.SenderAlt.Server == types.DefaultUserServer && info.SenderAlt.User != "" {
		senderPhone = info.SenderAlt.User
	}

	// For 1-on-1 direct messages, info.Chat may be the real phone number
	if senderPhone == "" && !info.IsGroup && info.Chat.Server == types.DefaultUserServer && info.Chat.User != "" {
		senderPhone = info.Chat.User
	}

	// Resolve using whatsmeow LID map store
	if senderPhone == "" && e.client != nil {
		senderPhone = e.client.ResolvePhoneNumber(context.Background(), info.Sender)
	}

	// Final fallback to info.Sender.User
	if senderPhone == "" {
		senderPhone = info.Sender.User
	}

	// If info.Sender was a LID and we resolved the phone, ensure mapping is stored
	if info.Sender.Server == types.HiddenUserServer && senderPhone != "" && senderPhone != info.Sender.User && e.client != nil {
		e.client.StoreLIDPNMapping(context.Background(), info.Sender, types.NewJID(senderPhone, types.DefaultUserServer))
	}

	pushName := strings.TrimSpace(info.PushName)
	var contact *contacts.Contact
	if e.contactStore != nil {
		if c, found := e.contactStore.Get(senderPhone); found {
			contact = c
			if strings.TrimSpace(c.Name) != "" && c.Name != c.Phone && c.Name != "+"+c.Phone && c.Name != "-" {
				pushName = c.Name
			}
		} else {
			nameToSave := pushName
			if nameToSave == "" || nameToSave == senderPhone || nameToSave == "+"+senderPhone || nameToSave == "-" {
				nameToSave = "+" + senderPhone
			}
			_ = e.contactStore.Save(contacts.Contact{
				Phone: senderPhone,
				Name:  nameToSave,
				Tags:  []string{"Incoming"},
				Notes: "WhatsApp Contact",
			})
		}
		e.contactStore.UpdateLastContacted(senderPhone)
	}
	if pushName == "" || pushName == senderPhone || pushName == "-" {
		pushName = "+" + senderPhone
	}

	// 2. Audit Trail: Record incoming message in history store
	if e.histStore != nil {
		e.histStore.Record(history.MessageRecord{
			ID:          info.ID,
			Direction:   history.DirectionIncoming,
			Type:        history.TypeUserMessage,
			Timestamp:   info.Timestamp,
			SenderPhone: senderPhone,
			SenderName:  pushName,
			Recipient:   "Me",
			Text:        text,
			Status:      "received",
		})
	}

	// Check master automation toggle
	if !e.cfg.Automation.Enabled {
		return
	}

	var replyText string
	var ruleName string

	// 3. Smart STOP / Unsubscribe Anti-Ban Compliance (and START to resubscribe)
	trimmedLower := strings.TrimSpace(strings.ToLower(text))
	cleanOptWord := strings.Trim(trimmedLower, "!?.#* \t\r\n")

	isOptOut := cleanOptWord == "stop" || cleanOptWord == "unsubscribe" || cleanOptWord == "optout" || cleanOptWord == "opt-out" || cleanOptWord == "cancel" || cleanOptWord == "quit"
	isOptIn := cleanOptWord == "start" || cleanOptWord == "unstop" || cleanOptWord == "subscribe"

	if isOptOut {
		if e.contactStore != nil {
			c, found := e.contactStore.Get(senderPhone)
			if !found {
				c = &contacts.Contact{
					Phone: senderPhone,
					Name:  pushName,
				}
			}
			c.IsDND = true
			hasTag := false
			for _, t := range c.Tags {
				if strings.EqualFold(t, "Opted-Out") {
					hasTag = true
					break
				}
			}
			if !hasTag {
				c.Tags = append(c.Tags, "Opted-Out")
			}
			c.Notes = "Customer opted out via STOP"
			_ = e.contactStore.Save(*c)
		}

		replyText = "🛑 You have been unsubscribed from automated messages. You will no longer receive broadcast notifications. Reply *START* anytime to resubscribe."
		ruleName = "system:opt_out"
	} else if isOptIn {
		if e.contactStore != nil {
			c, found := e.contactStore.Get(senderPhone)
			if !found {
				c = &contacts.Contact{
					Phone: senderPhone,
					Name:  pushName,
				}
			}
			c.IsDND = false
			newTags := make([]string, 0, len(c.Tags))
			for _, t := range c.Tags {
				if !strings.EqualFold(t, "Opted-Out") {
					newTags = append(newTags, t)
				}
			}
			newTags = append(newTags, "Subscribed")
			c.Tags = newTags
			c.Notes = "Customer resubscribed via START"
			_ = e.contactStore.Save(*c)
		}

		replyText = "✅ Welcome back! You have successfully resubscribed to automated updates. Reply *MENU* anytime to view services."
		ruleName = "system:opt_in"
	}

	// 4. Do Not Disturb (DND) / Blacklist / Human Agent Handover Check
	if replyText == "" {
		if contact != nil && contact.IsDND {
			return
		}
		if e.contactStore != nil && e.contactStore.IsBotPaused(senderPhone) {
			return // Auto-replies paused: customer requested live human agent
		}
		for _, bl := range e.cfg.Automation.BlacklistPhones {
			if bl == senderPhone {
				return
			}
		}
	}

	// 4. Outbound Webhook dispatch (if configured)
	if e.cfg.Webhook.Enabled && e.cfg.Webhook.URL != "" {
		go e.dispatchWebhook(evt, text, senderPhone)
	}

	// 5. Rate limiting / Cooldown check
	senderKey := info.Sender.String()
	cooldownSec := e.cfg.Automation.CooldownSeconds
	if cooldownSec > 0 {
		e.cooldownMu.Lock()
		lastTime, exists := e.cooldowns[senderKey]
		if exists && time.Since(lastTime) < time.Duration(cooldownSec)*time.Second {
			e.cooldownMu.Unlock()
			return // skip reply to prevent spamming
		}
		e.cooldowns[senderKey] = time.Now()
		e.cooldownMu.Unlock()
	}

	// 6. Business Hours Check (Away Message with Timezone support)
	if e.cfg.BusinessHours.Enabled {
		loc := time.Local
		if tz := strings.TrimSpace(e.cfg.BusinessHours.Timezone); tz != "" && !strings.EqualFold(tz, "Local") {
			if l, err := time.LoadLocation(tz); err == nil {
				loc = l
			}
		}
		currentHour := time.Now().In(loc).Hour()
		if currentHour < e.cfg.BusinessHours.StartHour || currentHour >= e.cfg.BusinessHours.EndHour {
			if e.cfg.BusinessHours.AwayReply != "" {
				replyText = RenderTemplate(e.cfg.BusinessHours.AwayReply, pushName, senderPhone, text)
				ruleName = "business_hours_away"
			}
		}
	}

	// 6.5. Conversation Context Memory (C8)
	var prevTurns int
	e.convMu.RLock()
	convState, hasConv := e.conversations[senderPhone]
	if hasConv && time.Since(convState.LastTimestamp) < 15*time.Minute {
		prevTurns = convState.Turns
		switch cleanOptWord {
		case "more", "details", "explain", "yes", "sure", "tell me more":
			if convState.LastRule == "option_services" || convState.LastTopic == "services" {
				replyText = "Our Services In Detail:\n\n1. WhatsApp Automation & AI Assistant: 24/7 intelligent answering\n2. Real-Time CRM & Webhook: Sync contacts with Zapier/Make\n3. Scheduled Broadcasts: Targeted campaigns with zero ban risk\n\nReply 2 for pricing, or 4 for live support!"
				ruleName = "context:services_details"
			} else if convState.LastRule == "option_pricing" || convState.LastTopic == "pricing" {
				replyText = "Pricing Details:\n\n• Starter: $29/mo (up to 1,000 automated replies)\n• Professional: $79/mo (unlimited rules + AI smart bot)\n• Enterprise: $199/mo (dedicated server + SLA)\n\nReply 4 to connect with our sales team!"
				ruleName = "context:pricing_details"
			}
		case "1":
			if convState.LastRule == "greeting_menu" || convState.LastRule == "welcome_greeting" || convState.LastRule == "menu" {
				replyText = "Our Services:\n\n1. WhatsApp Automation & AI Assistant\n2. Real-Time CRM & Webhook Integration\n3. Scheduled Broadcasts & Campaigns\n\nReply 2 to see pricing, or 4 for live support!"
				ruleName = "option_services"
			}
		case "2":
			if convState.LastRule == "greeting_menu" || convState.LastRule == "welcome_greeting" || convState.LastRule == "option_services" {
				replyText = "Pricing & Packages:\n\n• Starter: Basic automated responses & up to 1,000 messages\n• Professional: Full auto-reply rules, AI bot & broadcasts\n• Enterprise: High-volume dedicated deployment\n\nReply 4 to connect with our sales team!"
				ruleName = "option_pricing"
			}
		case "3":
			if convState.LastRule == "greeting_menu" || convState.LastRule == "welcome_greeting" {
				replyText = "Office Hours & Location:\n\n• Monday - Friday: 9:00 AM - 6:00 PM\n• Support: 24/7 online ticketing\n• Location: Headquarters & Remote Global Team\n\nReply 4 to speak with an agent!"
				ruleName = "option_hours"
			}
		case "4":
			if convState.LastRule == "greeting_menu" || convState.LastRule == "welcome_greeting" || convState.LastRule == "option_services" || convState.LastRule == "option_pricing" {
				ruleName = "option_agent"
			}
		case "back", "menu", "main menu":
			replyText = "Welcome to our automated assistant.\n\nHow can we help you today? Please reply with a number or keyword:\n1 Services - View our offerings\n2 Pricing - View plans & pricing\n3 Hours - Office hours & location\n4 Agent - Speak with a human"
			ruleName = "greeting_menu"
		}
	}
	e.convMu.RUnlock()

	// 7. Rule Evaluation (if not already handled by context or business hours)
	if replyText == "" {
		rules := e.cfg.GetRules()
		result := EvaluateMessage(text, pushName, senderPhone, rules, e.startTime)
		if result.Matched {
			replyText = result.ReplyText
			ruleName = result.RuleName
		}
	}

	// Check if human agent transfer was triggered (C4)
	if ruleName == "option_agent" || strings.Contains(strings.ToLower(ruleName), "agent") || cleanOptWord == "agent" || cleanOptWord == "human" || cleanOptWord == "support" {
		if e.contactStore != nil {
			e.contactStore.PauseBot(senderPhone, 24*time.Hour)
			security.LogAudit("AGENT_HANDOVER", senderPhone, pushName, "Customer requested human support; bot auto-replies paused for 24 hours", "success")
		}
	}

	// 8. AI Smart Reply with multi-turn conversation memory (C8)
	if replyText == "" && e.aiEngine != nil && e.aiEngine.IsEnabled() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		var histSummary string
		if e.histStore != nil {
			msgs := e.histStore.List(4, senderPhone, "")
			if len(msgs) > 0 {
				var sb strings.Builder
				for i := len(msgs) - 1; i >= 0; i-- {
					m := msgs[i]
					role := "Customer"
					if m.Direction == history.DirectionOutgoing {
						role = "Assistant"
					}
					sb.WriteString(fmt.Sprintf("%s: %s\n", role, m.Text))
				}
				histSummary = sb.String()
			}
		}
		aiReply, err := e.aiEngine.GenerateReply(ctx, pushName, text, histSummary)
		cancel()
		if err == nil && aiReply != "" {
			replyText = aiReply
			ruleName = "ai_smart_reply"
		}
	}

	// 9. Default Fallback Reply
	if replyText == "" && e.cfg.Automation.EnableDefaultReply && e.cfg.Automation.DefaultReply != "" {
		replyText = RenderTemplate(e.cfg.Automation.DefaultReply, pushName, senderPhone, text)
		ruleName = "default_reply"
	}

	if replyText == "" {
		return
	}

	// Save conversational turn in memory (C8)
	e.convMu.Lock()
	e.conversations[senderPhone] = &ConversationState{
		LastRule:      ruleName,
		LastTopic:     cleanOptWord,
		LastTimestamp: time.Now(),
		Turns:         prevTurns + 1,
	}
	e.convMu.Unlock()

	ctx := context.Background()

	// 10. Auto-read receipt
	if e.cfg.Automation.AutoRead {
		_ = e.client.MarkAsRead(ctx, info.Chat, info.Sender, []types.MessageID{info.ID})
	}

	// 11. Typing simulation for natural human feel
	if e.cfg.Automation.TypingSimulation {
		durMs := e.cfg.Automation.TypingDurationMs
		if durMs <= 0 {
			durMs = 1500
		}
		_ = e.client.SimulateTyping(ctx, info.Chat.String(), time.Duration(durMs)*time.Millisecond)
	}

	// 12. Dispatch quoted response
	participant := ""
	if info.IsGroup {
		participant = info.Sender.String()
	}

	msgID, err := e.client.SendReply(ctx, info.Chat.String(), replyText, info.ID, participant, evt.Message)
	if err != nil {
		fmt.Printf("[Bot] Failed to send automated reply to %s: %v\n", info.Chat.String(), err)
	} else {
		fmt.Printf("[Bot] Auto-replied to %s (%s) triggered by rule: %s\n", pushName, senderPhone, ruleName)
	}

	// 13. Audit Trail: Record outgoing auto-reply in history
	if e.histStore != nil {
		status := "sent"
		errMsg := ""
		if err != nil {
			status = "failed"
			errMsg = err.Error()
		}
		e.histStore.Record(history.MessageRecord{
			ID:            msgID,
			Direction:     history.DirectionOutgoing,
			Type:          history.TypeAutoReply,
			Timestamp:     time.Now(),
			SenderPhone:   "Me",
			SenderName:    "WhatsApp Automation",
			Recipient:     senderPhone,
			Text:          replyText,
			RuleTriggered: ruleName,
			Status:        status,
			Error:         errMsg,
		})
	}
}

// dispatchWebhook posts the message event to an external HTTP webhook endpoint with SSRF check, HMAC-SHA256, and retry
func (e *Engine) dispatchWebhook(evt *events.Message, text string, realPhone string) {
	destURL := strings.TrimSpace(e.cfg.Webhook.URL)
	if destURL == "" || strings.Contains(destURL, "/api/v1/webhook/send") {
		return // Skip outbound dispatch if unconfigured or pointing to self-inbound send endpoint
	}

	// S8: SSRF validation to prevent internal network scanning
	if err := security.ValidateWebhookURL(destURL); err != nil {
		fmt.Printf("[Webhook] Outbound webhook blocked by SSRF protection (%s): %v\n", destURL, err)
		return
	}

	phone := realPhone
	if phone == "" {
		phone = evt.Info.Sender.User
	}
	payload := WebhookPayload{
		Event:     "message",
		MessageID: evt.Info.ID,
		SenderJID: evt.Info.Sender.String(),
		Phone:     phone,
		PushName:  evt.Info.PushName,
		ChatJID:   evt.Info.Chat.String(),
		IsGroup:   evt.Info.IsGroup,
		Text:      text,
		Timestamp: evt.Info.Timestamp,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return
	}

	timeout := time.Duration(e.cfg.Webhook.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	// D11: HMAC-SHA256 signature and retry logic
	sig := ""
	if e.cfg.Webhook.Secret != "" {
		sig = security.SignWebhookHMAC(e.cfg.Webhook.Secret, data)
	}

	for attempt := 0; attempt < 3; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		req, err := http.NewRequestWithContext(ctx, "POST", destURL, bytes.NewBuffer(data))
		if err != nil {
			cancel()
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if e.cfg.Webhook.Secret != "" {
			req.Header.Set("X-Webhook-Secret", e.cfg.Webhook.Secret)
			req.Header.Set("X-Signature-SHA256", "sha256="+sig)
		}

		resp, err := e.httpClient.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return // Successful delivery or 4xx client response
			}
		}

		if attempt < 2 {
			time.Sleep(time.Duration(1<<attempt) * 500 * time.Millisecond)
		}
	}
}

// Reset clears all in-memory conversation states and message cooldowns
func (e *Engine) Reset() {
	if e == nil {
		return
	}
	e.cooldownMu.Lock()
	e.cooldowns = make(map[string]time.Time)
	e.cooldownMu.Unlock()

	e.convMu.Lock()
	e.conversations = make(map[string]*ConversationState)
	e.convMu.Unlock()
}

// GetConversationState returns a copy of the conversation state for a phone number (C8)
func (e *Engine) GetConversationState(phone string) *ConversationState {
	if e == nil {
		return nil
	}
	e.convMu.RLock()
	defer e.convMu.RUnlock()
	clean := contacts.CleanPhone(phone)
	if s, ok := e.conversations[clean]; ok {
		copyState := *s
		return &copyState
	}
	return nil
}
