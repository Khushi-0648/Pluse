package msgtemplate

import (
	"strings"
	"testing"
	"time"

	"whatsapp-automation/pkg/config"
)

func TestNameFallback(t *testing.T) {
	for _, name := range []string{"", "+91∙∙∙∙∙∙∙∙37", "+919876543210", "919876543210", "123456789012345@lid", "-"} {
		out, _ := Render("Hello {{first_name}}!", Vars{Name: name, Phone: "919876543210"})
		if out != "Hello there!" {
			t.Errorf("name %q rendered as %q (C22)", name, out)
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
