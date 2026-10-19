package bot

import (
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
)

func TestEvaluateMessageBuiltins(t *testing.T) {
	rules := []config.Rule{}
	startTime := time.Now().Add(-1 * time.Minute)

	// Test !ping
	res := EvaluateMessage("!ping", "Alice", "14155552671", rules, startTime)
	if !res.Matched || !res.IsBuiltin || res.RuleName != "builtin:ping" {
		t.Fatalf("Expected !ping to match builtin, got %+v", res)
	}

	// Test !time
	res = EvaluateMessage("!time", "Alice", "14155552671", rules, startTime)
	if !res.Matched || !res.IsBuiltin || res.RuleName != "builtin:time" {
		t.Fatalf("Expected !time to match builtin, got %+v", res)
	}

	// Test !echo
	res = EvaluateMessage("!echo hello world", "Alice", "14155552671", rules, startTime)
	if !res.Matched || res.ReplyText != "📢 *Echo:* hello world" {
		t.Fatalf("Expected echo response, got %+v", res)
	}
}

func TestEvaluateMessageRules(t *testing.T) {
	rules := []config.Rule{
		{
			Name:     "greeting_multi",
			Type:     "exact",
			Pattern:  "hi, hello, hey",
			Reply:    "{{greeting}}, {{first_name}}!",
			Enabled:  true,
		},
		{
			Name:     "contains_support",
			Type:     "contains",
			Pattern:  "support, helpdesk",
			Reply:    "Support reached for {{phone}}",
			Enabled:  true,
		},
		{
			Name:     "prefix_cmd",
			Type:     "prefix",
			Pattern:  "/quote",
			Reply:    "Your quote is ready",
			Enabled:  true,
		},
		{
			Name:     "disabled_rule",
			Type:     "exact",
			Pattern:  "inactive",
			Reply:    "Should not match",
			Enabled:  false,
		},
	}
	startTime := time.Now()

	// Multi-keyword exact match: "hello"
	res := EvaluateMessage("hello", "Bob Jones", "14155552672", rules, startTime)
	if !res.Matched || !strings.Contains(res.ReplyText, "Bob") {
		t.Fatalf("Expected exact match with first_name Bob, got %+v", res)
	}

	// Multi-keyword exact match: "hey"
	res = EvaluateMessage("HEY", "Bob Jones", "14155552672", rules, startTime)
	if !res.Matched || !strings.Contains(res.ReplyText, "Bob") {
		t.Fatalf("Expected match with 'HEY', got %+v", res)
	}

	// Contains match: "helpdesk"
	res = EvaluateMessage("Please route to helpdesk please", "Bob Jones", "14155552672", rules, startTime)
	if !res.Matched || res.ReplyText != "Support reached for 14155552672" {
		t.Fatalf("Expected contains match for helpdesk, got %+v", res)
	}

	// Disabled rule
	res = EvaluateMessage("inactive", "Bob Jones", "14155552672", rules, startTime)
	if res.Matched {
		t.Fatalf("Disabled rule should not match, got %+v", res)
	}

	// No match
	res = EvaluateMessage("random gibberish", "Bob Jones", "14155552672", rules, startTime)
	if res.Matched {
		t.Fatalf("Expected no match, got %+v", res)
	}
}

func TestEvaluateMessage_StopAndStartCompliance(t *testing.T) {
	// C18: START and STOP must trigger opt-in and opt-out without colliding with greeting rules
	rules := []config.Rule{
		{
			Name:     "greeting",
			Type:     "exact",
			Pattern:  "hi, hello, menu",
			Reply:    "Hello!",
			Enabled:  true,
		},
	}
	startTime := time.Now()

	// 1. "STOP" keyword
	resStop := EvaluateMessage("STOP", "Alice", "+14155552671", rules, startTime)
	if !resStop.Matched || resStop.RuleName != "system:opt_out" {
		t.Errorf("Expected STOP to match system:opt_out, got %+v", resStop)
	}

	// 2. "start" keyword (resubscribe)
	resStart := EvaluateMessage("start", "Alice", "+14155552671", rules, startTime)
	if !resStart.Matched || resStart.RuleName != "system:opt_in" {
		t.Errorf("Expected start to match system:opt_in, got %+v", resStart)
	}
}

func TestRenderTemplate(t *testing.T) {
	tmpl := "Hello {{name}}! Welcome to our service. Recipient: {{ recipient }}. First name: {FIRST_NAME}. Phone: {phone}."
	rendered := RenderTemplate(tmpl, "Satya Sharma", "+1234567890", "")
	if !strings.Contains(rendered, "Hello Satya Sharma!") {
		t.Errorf("Expected 'Hello Satya Sharma!', got: %s", rendered)
	}
	if !strings.Contains(rendered, "Recipient: Satya Sharma.") {
		t.Errorf("Expected 'Recipient: Satya Sharma.', got: %s", rendered)
	}
	if !strings.Contains(rendered, "First name: Satya.") {
		t.Errorf("Expected 'First name: Satya.', got: %s", rendered)
	}
	if !strings.Contains(rendered, "Phone: +1234567890.") {
		t.Errorf("Expected 'Phone: +1234567890.', got: %s", rendered)
	}

	// D10: Test unknown variables are stripped cleanly
	tmplUnknown := "Order confirmed for {{name}}. Details: {unknown_field} {{missing_val}}."
	renderedUnknown := RenderTemplate(tmplUnknown, "John Doe", "+1234567890", "")
	if strings.Contains(renderedUnknown, "{unknown_field}") || strings.Contains(renderedUnknown, "{{missing_val}}") {
		t.Errorf("Expected unknown variables to be cleanly stripped, got: %s", renderedUnknown)
	}

	// Test custom variable interpolation (e.g. from CSV broadcast parameters)
	tmplCustom := "Hello {{name}}, your {{service}} balance at {{company}} is ${{amount}} (inv: {invoice_id})."
	customVars := map[string]string{
		"service":    "WhatsApp Bot",
		"company":    "Acme Corp",
		"amount":     "250",
		"invoice_id": "INV-1029",
	}
	renderedCustom := RenderTemplate(tmplCustom, "Jane Doe", "+1234567890", "", customVars)
	expectedSub := "Hello Jane Doe, your WhatsApp Bot balance at Acme Corp is $250 (inv: INV-1029)."
	if renderedCustom != expectedSub {
		t.Errorf("Expected custom variable substitution %q, got %q", expectedSub, renderedCustom)
	}
}

func TestEvaluateMessage_AgentHandover(t *testing.T) {
	rules := []config.Rule{}
	startTime := time.Now()

	keywords := []string{"agent", "!agent", "human", "support", "representative"}
	for _, kw := range keywords {
		res := EvaluateMessage(kw, "Alice", "+14155552671", rules, startTime)
		if !res.Matched || res.RuleName != "builtin:agent_handover" {
			t.Errorf("Expected keyword %q to match builtin:agent_handover, got %+v", kw, res)
		}
		if !strings.Contains(res.ReplyText, "Live Agent Requested") {
			t.Errorf("Expected agent handover reply for %q, got: %s", kw, res.ReplyText)
		}
	}
}

// TestConversationContextMemory_C8 verifies multi-turn conversation memory
func TestConversationContextMemory_C8(t *testing.T) {
	cfg := config.DefaultConfig()
	eng := NewEngine(nil, cfg, nil, nil, nil)

	phone := "14155559999"
	// Simulate Turn 1: User receives menu
	eng.conversations[phone] = &ConversationState{
		LastRule:      "greeting_menu",
		LastTopic:     "menu",
		LastTimestamp: time.Now(),
		Turns:         1,
	}

	state := eng.GetConversationState(phone)
	if state == nil || state.LastRule != "greeting_menu" {
		t.Fatalf("Expected initial state greeting_menu, got %+v", state)
	}

	// Verify multi-turn state tracks correctly
	eng.conversations[phone] = &ConversationState{
		LastRule:      "option_services",
		LastTopic:     "services",
		LastTimestamp: time.Now(),
		Turns:         2,
	}
	state2 := eng.GetConversationState(phone)
	if state2.Turns != 2 || state2.LastRule != "option_services" {
		t.Fatalf("Expected turn 2 state option_services, got %+v", state2)
	}
}

// TestRenderTemplate_LIDAndFallback_C2 verifies C2: raw LIDs, phone digits, or blank names fall back naturally to "there"
func TestRenderTemplate_LIDAndFallback_C2(t *testing.T) {
	tmpl := "Hello, *{{first_name}}*! Welcome to our service. Recipient: {{name}}."

	// Case 1: WhatsApp Linked Device ID (LID)
	outLID := RenderTemplate(tmpl, "11235785498817", "14155552671", "")
	if strings.Contains(outLID, "11235785498817") {
		t.Errorf("Expected raw LID to be replaced with natural fallback, got: %s", outLID)
	}
	if !strings.Contains(outLID, "Hello there!") {
		t.Errorf("Expected 'Hello there!', got: %s", outLID)
	}

	// Case 2: Pure phone digits as name
	outDigits := RenderTemplate(tmpl, "919876543210", "919876543210", "")
	if strings.Contains(outDigits, "919876543210!") {
		t.Errorf("Expected phone number name to fall back to 'there', got: %s", outDigits)
	}
	if !strings.Contains(outDigits, "Hello there!") {
		t.Errorf("Expected 'Hello there!', got: %s", outDigits)
	}

	// Case 3: Empty name
	outBlank := RenderTemplate(tmpl, "", "14155552671", "")
	if !strings.Contains(outBlank, "Hello there!") {
		t.Errorf("Expected 'Hello there!', got: %s", outBlank)
	}

	// Case 4: Real name
	outReal := RenderTemplate(tmpl, "Satya Sharma", "14155552671", "")
	if !strings.Contains(outReal, "*Satya*") {
		t.Errorf("Expected real name *Satya*, got: %s", outReal)
	}
}

// TestEvaluateMessage_HandoverPhrases_C4 verifies C4: natural agent handover phrases trigger human agent handover
func TestEvaluateMessage_HandoverPhrases_C4(t *testing.T) {
	phrases := []string{
		"Can I talk to an agent please?",
		"I want to speak to a human",
		"connect me to customer support",
		"live agent",
		"need human help",
	}

	for _, p := range phrases {
		res := EvaluateMessage(p, "Customer", "14155552671", nil, time.Now())
		if !res.Matched || res.RuleName != "builtin:agent_handover" {
			t.Errorf("Expected phrase %q to trigger builtin:agent_handover, got %+v", p, res)
		}
	}
}




