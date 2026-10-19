package bot

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
)

// MatchResult stores match status and formatted reply text
type MatchResult struct {
	Matched   bool
	RuleName  string
	ReplyText string
	IsBuiltin bool
}

// isHandoverIntent checks whether the user is requesting live human support (C4)
func isHandoverIntent(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	cleanWord := strings.Trim(lower, "!?.#* \t\r\n")
	if cleanWord == "agent" || cleanWord == "!agent" || cleanWord == "human" || cleanWord == "support" || cleanWord == "representative" {
		return true
	}
	phrases := []string{
		"talk to agent", "talk to an agent", "talk to human", "talk to a human",
		"speak to agent", "speak to an agent", "speak to human", "speak to a human",
		"speak with agent", "speak with human", "human agent", "live agent",
		"customer care", "customer support", "need human", "connect to human", "connect to agent",
	}
	for _, p := range phrases {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// EvaluateMessage matches message text against configured rules and built-in commands
func EvaluateMessage(text string, senderName string, senderPhone string, rules []config.Rule, startTime time.Time) MatchResult {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)

	// 1. Check Built-in Compliance Commands (STOP / START opt-in & opt-out)
	cleanWord := strings.Trim(lower, "!?.#* \t\r\n")
	if cleanWord == "stop" || cleanWord == "unsubscribe" || cleanWord == "optout" || cleanWord == "opt-out" || cleanWord == "cancel" || cleanWord == "quit" {
		return MatchResult{
			Matched:   true,
			RuleName:  "system:opt_out",
			IsBuiltin: true,
			ReplyText: "🛑 You have been unsubscribed from automated messages. You will no longer receive broadcast notifications. Reply *START* anytime to resubscribe.",
		}
	}

	if cleanWord == "start" || cleanWord == "unstop" || cleanWord == "subscribe" {
		return MatchResult{
			Matched:   true,
			RuleName:  "system:opt_in",
			IsBuiltin: true,
			ReplyText: "✅ Welcome back! You have successfully resubscribed to automated updates. Reply *MENU* anytime to view services.",
		}
	}

	// Human Agent Handover (C4)
	if isHandoverIntent(text) {
		return MatchResult{
			Matched:   true,
			RuleName:  "builtin:agent_handover",
			IsBuiltin: true,
			ReplyText: "👨‍💼 *Live Agent Requested*\n\nYour request has been forwarded to a human representative. Automated replies have been paused for this chat. An agent will respond shortly.",
		}
	}

	// 2. Check Built-in Utility Commands
	if strings.HasPrefix(lower, "!ping") {
		return MatchResult{
			Matched:   true,
			RuleName:  "builtin:ping",
			IsBuiltin: true,
			ReplyText: fmt.Sprintf("🏓 *Pong!*\n⏰ Time: %s\n⏱️ Uptime: %s", time.Now().Format("15:04:05 MST"), time.Since(startTime).Round(time.Second)),
		}
	}

	if strings.HasPrefix(lower, "!time") {
		return MatchResult{
			Matched:   true,
			RuleName:  "builtin:time",
			IsBuiltin: true,
			ReplyText: fmt.Sprintf("🕒 *Server Time:*\n%s (%s)", time.Now().Format(time.RFC1123), time.Now().Location().String()),
		}
	}

	if strings.HasPrefix(lower, "!info") {
		return MatchResult{
			Matched:   true,
			RuleName:  "builtin:info",
			IsBuiltin: true,
			ReplyText: fmt.Sprintf("🤖 *WhatsApp Automation Bot*\n\n• Version: v1.3.0\n• Engine: Go + whatsmeow\n• Uptime: %s\n• Active Rules: %d", time.Since(startTime).Round(time.Second), len(rules)),
		}
	}


	if strings.HasPrefix(lower, "!echo") {
		payload := strings.TrimSpace(trimmed[len("!echo"):])
		if payload == "" {
			payload = "(empty text)"
		}
		return MatchResult{
			Matched:   true,
			RuleName:  "builtin:echo",
			IsBuiltin: true,
			ReplyText: fmt.Sprintf("📢 *Echo:* %s", payload),
		}
	}

	// 2. Evaluate Configured Rules
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}

		matched := matchRule(rule, trimmed, lower)
		if matched {
			reply := RenderTemplate(rule.Reply, senderName, senderPhone, trimmed)
			return MatchResult{
				Matched:   true,
				RuleName:  rule.Name,
				ReplyText: reply,
			}
		}
	}

	return MatchResult{Matched: false}
}

// matchRule checks if the text matches the rule, supporting comma-separated patterns
func matchRule(rule config.Rule, trimmed, lower string) bool {
	rawPatterns := strings.Split(rule.Pattern, ",")
	cleanTrimmed := strings.TrimRight(trimmed, "!?. \t\r\n")
	cleanLower := strings.ToLower(cleanTrimmed)

	for _, p := range rawPatterns {
		pat := strings.TrimSpace(p)
		if pat == "" {
			continue
		}
		patLower := strings.ToLower(pat)

		switch rule.Type {
		case "exact":
			if strings.EqualFold(trimmed, pat) || strings.EqualFold(cleanTrimmed, pat) {
				return true
			}
			// Match if message starts with the greeting keyword as a distinct word (e.g. "hii bro", "hello there")
			if strings.HasPrefix(cleanLower, patLower+" ") || strings.HasPrefix(cleanLower, patLower+",") {
				return true
			}
		case "contains":
			if strings.Contains(lower, patLower) {
				return true
			}
		case "prefix":
			if strings.HasPrefix(lower, patLower) {
				return true
			}
		case "regex":
			re, err := regexp.Compile(pat)
			if err == nil && re.MatchString(trimmed) {
				return true
			}
		}
	}

	return false
}

var (
	reGreeting         = regexp.MustCompile(`(?i)\{\{?\s*greeting\s*\}?\}`)
	reName             = regexp.MustCompile(`(?i)\{\{?\s*name\s*\}?\}`)
	reFirstName        = regexp.MustCompile(`(?i)\{\{?\s*(first_name|firstname)\s*\}?\}`)
	reSender           = regexp.MustCompile(`(?i)\{\{?\s*sender\s*\}?\}`)
	reRecipient        = regexp.MustCompile(`(?i)\{\{?\s*recipient\s*\}?\}`)
	rePhone            = regexp.MustCompile(`(?i)\{\{?\s*phone\s*\}?\}`)
	reTime             = regexp.MustCompile(`(?i)\{\{?\s*time\s*\}?\}`)
	reDate             = regexp.MustCompile(`(?i)\{\{?\s*date\s*\}?\}`)
	reMessage          = regexp.MustCompile(`(?i)\{\{?\s*message\s*\}?\}`)
	reRemainingUnknown = regexp.MustCompile(`\{\{[^}]+\}\}|\{[a-zA-Z0-9_]+\}`)
)

// RenderTemplate substitutes {{greeting}}, {{name}}, {{first_name}}, {{sender}}, {{recipient}}, {{phone}}, {{time}}, {{date}}, {{message}}, and any custom variables
func RenderTemplate(template string, senderName, senderPhone, originalMsg string, customVars ...map[string]string) string {
	res := template

	// Substitute any custom key-value variables first (e.g. from CSV columns or CRM payloads)
	for _, vars := range customVars {
		for k, v := range vars {
			res = strings.ReplaceAll(res, "{{"+k+"}}", v)
			res = strings.ReplaceAll(res, "{{ "+k+" }}", v)
			res = strings.ReplaceAll(res, "{"+k+"}", v)
			// Case-insensitive token replacement
			lowerK := strings.ToLower(k)
			if lowerK != k {
				res = strings.ReplaceAll(res, "{{"+lowerK+"}}", v)
				res = strings.ReplaceAll(res, "{{ "+lowerK+" }}", v)
				res = strings.ReplaceAll(res, "{"+lowerK+"}", v)
			}
		}
	}

	displayName := strings.TrimSpace(senderName)
	cleanPhoneDigits := regexp.MustCompile(`[^0-9]`).ReplaceAllString(senderPhone, "")
	cleanNameDigits := regexp.MustCompile(`[^0-9]`).ReplaceAllString(displayName, "")

	// C2: If name is missing, a placeholder, raw phone digits, WhatsApp LID, or non-name, use natural "there" fallback
	isPhoneOrBlank := displayName == "" || displayName == "-" || displayName == senderPhone || displayName == "+"+senderPhone
	if !isPhoneOrBlank {
		lowerName := strings.ToLower(displayName)
		if (len(cleanNameDigits) >= 7 && cleanNameDigits == cleanPhoneDigits) ||
			contacts.IsLID(displayName) || contacts.IsLID(cleanNameDigits) ||
			strings.HasSuffix(lowerName, "@lid") ||
			(len(cleanNameDigits) >= 7 && cleanNameDigits == displayName) ||
			lowerName == "there" || lowerName == "customer" || lowerName == "unknown" || lowerName == "null" || lowerName == "undefined" {
			isPhoneOrBlank = true
		}
	}

	firstName := "there"
	if !isPhoneOrBlank {
		parts := strings.Fields(displayName)
		if len(parts) > 0 {
			firstName = parts[0]
		} else {
			firstName = displayName
		}
	} else {
		displayName = "there"
	}

	hour := time.Now().Hour()
	greeting := "Hello"
	if hour >= 5 && hour < 12 {
		greeting = "Good morning"
	} else if hour >= 12 && hour < 17 {
		greeting = "Good afternoon"
	} else if hour >= 17 && hour < 22 {
		greeting = "Good evening"
	}

	res = reGreeting.ReplaceAllString(res, greeting)
	res = reName.ReplaceAllString(res, displayName)
	res = reFirstName.ReplaceAllString(res, firstName)
	res = reSender.ReplaceAllString(res, displayName)
	res = reRecipient.ReplaceAllString(res, displayName)
	res = rePhone.ReplaceAllString(res, senderPhone)
	res = reTime.ReplaceAllString(res, time.Now().Format("15:04:05"))
	res = reDate.ReplaceAllString(res, time.Now().Format("2006-01-02"))
	res = reMessage.ReplaceAllString(res, originalMsg)

	// Clean up formatting artifacts if firstName or name resolved to "there"
	if isPhoneOrBlank {
		res = strings.ReplaceAll(res, "*there*", "there")
		res = strings.ReplaceAll(res, "_there_", "there")
		res = strings.ReplaceAll(res, ", there", " there")
		res = strings.ReplaceAll(res, ",  there", " there")
	}

	// Clean any unrecognized template tokens (D10, C2)
	res = reRemainingUnknown.ReplaceAllString(res, "")
	return res
}

