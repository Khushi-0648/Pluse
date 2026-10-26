package msgtemplate

import (
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
)

func TestNameFallback(t *testing.T) {
	for _, name := range []string{"", "+91∙∙∙∙∙∙∙∙37", "+919876543210", "919876543210", "123456789012345@lid", "-"} {
		// No usable name: the name is left out before punctuation, never a number (C22).
		out, _ := Render("Hello {{first_name}}!", Vars{Name: name, Phone: "919876543210"})
		if out != "Hello!" {
			t.Errorf("name %q rendered as %q (C22)", name, out)
		}
		if out, _ = Render("Hello {{first_name}} and welcome", Vars{Name: name}); out != "Hello there and welcome" {
			t.Errorf("name %q mid-sentence rendered as %q (C22)", name, out)
		}
	}
	out, _ := Render("Hi {{first_name}} / {{name}}", Vars{Name: "Asha Verma"})
	if out != "Hi Asha / Asha Verma" {
		t.Errorf("got %q", out)
	}
}

func TestMissingAndUnknownVariables(t *testing.T) {
	out, missing := Render("Order {{order_id}} for {{business_name}}", Vars{Business: config.BusinessConfig{Name: "Shop"}})
	if len(missing) != 1 || missing[0] != "order_id" || !strings.Contains(out, "Shop") {
		t.Fatalf("missing=%v out=%q", missing, out)
	}
	if _, missing := Render("{{business_prices}}", Vars{}); len(missing) != 1 {
		t.Error("empty business details must be reported as missing")
	}
	if Validate("Hello {{first_name}} {{time}}") != nil {
		t.Error("known variables should validate")
	}
	if err := Validate("Code {{promo_link}}"); err == nil || !strings.Contains(err.Error(), "promo_link") {
		t.Errorf("unknown variable not reported: %v", err)
	}
	if Validate("Broken {{name") == nil {
		t.Error("unclosed braces should be reported")
	}
	if Validate("Order {{order_id}}", "order_id") != nil {
		t.Error("extra variables should be accepted")
	}
	out, missing = Render("Order {{ Order ID }}", Vars{Extra: map[string]string{"order_id": "A1"}})
	if out != "Order A1" || len(missing) != 0 {
		t.Errorf("extra variable not substituted: %q %v", out, missing)
	}
}

func TestGreetingUsesTimezone(t *testing.T) {
	loc := time.FixedZone("IST", 5*3600+1800)
	now := time.Date(2026, 1, 1, 3, 0, 0, 0, time.UTC) // 08:30 IST
	out, _ := Render("{{greeting}}", Vars{Now: now, Location: loc})
	if out != "Good morning" {
		t.Errorf("got %q", out)
	}
}

// {{message}} only exists in automatic replies: elsewhere it would be sent as a gap.
func TestMessageVariableOnlyInReplies(t *testing.T) {
	if err := ValidateOutgoing("You said: {{message}}"); err == nil {
		t.Fatal("campaigns and scheduled messages must refuse {{message}}")
	}
	if err := ValidateOutgoing("Hi {{first_name}}"); err != nil {
		t.Fatal(err)
	}
	if _, missing := Render("You said: {{message}}", Vars{Name: "Asha"}); len(missing) != 1 || missing[0] != "message" {
		t.Fatalf("an empty {{message}} must be reported, got %v", missing)
	}
	if out, missing := Render("You said: {{message}}", Vars{Message: "hello"}); len(missing) != 0 || out != "You said: hello" {
		t.Fatalf("in a reply it is filled: %q %v", out, missing)
	}
}

// C22: with no name a reply must not read "Thank you, there."
func TestNoNameReadsNaturally(t *testing.T) {
	for in, want := range map[string]string{
		"Thank you, {{first_name}}. A member of our team will reply.": "Thank you. A member of our team will reply.",
		"Goodbye {{first_name}}, have a good day.":                    "Goodbye, have a good day.",
		"You are welcome, {{first_name}}.":                            "You are welcome.",
		"Hello {{first_name}}!":                                       "Hello!",
		"Hello {{first_name}} and welcome":                            "Hello there and welcome",
	} {
		if got, _ := Render(in, Vars{}); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
	if got, _ := Render("Thank you, {{first_name}}.", Vars{Name: "Asha Verma"}); got != "Thank you, Asha." {
		t.Errorf("with a name: %q", got)
	}
}
