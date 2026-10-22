// Package msgtemplate renders {{variable}} placeholders in message text.
package msgtemplate

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"whatsapp-automation/pkg/config"
)

// Variable documents one placeholder for the editor's variable picker.
type Variable struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Example string `json:"example"`
}

// Known lists every built-in variable.
var Known = []Variable{
	{"first_name", "Customer first name (\"there\" when unknown)", "Asha"},
	{"name", "Customer full name (\"there\" when unknown)", "Asha Verma"},
	{"phone", "Customer phone number", "+919876543210"},
	{"greeting", "Good morning / afternoon / evening", "Good morning"},
	{"time", "Current time", "14:05"},
	{"date", "Current date", "21 Oct 2026"},
	{"day", "Day of the week", "Wednesday"},
	{"message", "The customer's message (auto-replies only)", "hello"},
	{"business_name", "Business name", "Verma Dental Clinic"},
	{"business_hours", "Opening hours", "Mon-Sat 9:00-18:00"},
	{"business_address", "Address", "12 MG Road, Pune"},
	{"business_website", "Website", "https://example.com"},
	{"business_email", "Email", "hello@example.com"},
	{"business_services", "Services text", "Cleaning, fillings, braces"},
	{"business_prices", "Prices text", "Consultation from Rs 500"},
}

var aliases = map[string]string{"firstname": "first_name", "company": "business_name", "sender": "name", "recipient": "name", "short_time": "time", "full_date": "date"}

var tokenRe = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_ .\-]+?)\s*\}\}`)

func canonical(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	k = strings.NewReplacer(" ", "_", "-", "_", ".", "_").Replace(k)
	if a, ok := aliases[k]; ok {
		return a
	}
	return k
}

// Vars supplies values for rendering.
type Vars struct {
	Name     string
	Phone    string
	Message  string
	Business config.BusinessConfig
	Extra    map[string]string
	Now      time.Time
	Location *time.Location
}

// IsPlaceholderName reports whether a name is missing or is really a phone number,
// masked number or internal ID, in which case "there" is used instead.
func IsPlaceholderName(name string) bool {
	n := strings.TrimSpace(name)
	if n == "" || n == "-" {
		return true
	}
	lower := strings.ToLower(n)
	switch lower {
	case "there", "customer", "unknown", "null", "undefined", "user":
		return true
	}
	if strings.ContainsAny(n, "∙•*·") || strings.HasSuffix(lower, "@lid") || strings.HasSuffix(lower, "@s.whatsapp.net") {
		return true
	}
	letters := 0
	for _, r := range n {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters == 0
}

func (v Vars) values() map[string]string {
	now := v.Now
	if now.IsZero() {
		now = time.Now()
	}
	if v.Location != nil {
		now = now.In(v.Location)
	}
	name, first := "there", "there"
	if !IsPlaceholderName(v.Name) {
		name = strings.TrimSpace(v.Name)
		first = strings.Fields(name)[0]
	}
	greeting := "Hello"
	switch h := now.Hour(); {
	case h >= 5 && h < 12:
		greeting = "Good morning"
	case h >= 12 && h < 17:
		greeting = "Good afternoon"
	case h >= 17 && h < 22:
		greeting = "Good evening"
	}
	phone := ""
	if d := strings.TrimLeft(strings.TrimSpace(v.Phone), "+"); d != "" {
		phone = "+" + d
	}
	b := v.Business
	vals := map[string]string{
		"first_name": first, "name": name, "phone": phone, "greeting": greeting,
		"time": now.Format("15:04"), "date": now.Format("02 Jan 2006"), "day": now.Format("Monday"),
		"message":           v.Message,
		"business_name":     strings.TrimSpace(b.Name),
		"business_hours":    strings.TrimSpace(b.Hours),
		"business_address":  strings.TrimSpace(b.Address),
		"business_website":  strings.TrimSpace(b.Website),
		"business_email":    strings.TrimSpace(b.Email),
		"business_services": strings.TrimSpace(b.Services),
		"business_prices":   strings.TrimSpace(b.Prices),
	}
	for k, val := range v.Extra {
		ck := canonical(k)
		if _, builtin := vals[ck]; builtin && ck != "message" {
			if strings.TrimSpace(val) == "" {
				continue
			}
		}
		vals[ck] = strings.TrimSpace(val)
	}
	return vals
}

// Render substitutes placeholders. Missing lists variables that are unknown or empty;
// callers must not send text with missing variables.
func Render(text string, v Vars) (out string, missing []string) {
	vals := v.values()
	seen := map[string]bool{}
	out = tokenRe.ReplaceAllStringFunc(text, func(tok string) string {
		key := canonical(tokenRe.FindStringSubmatch(tok)[1])
		val, ok := vals[key]
		if !ok || val == "" {
			if key != "message" && !seen[key] {
				seen[key] = true
				missing = append(missing, key)
			}
			return ""
		}
		return val
	})
	return strings.TrimSpace(out), missing
}

// Unknown returns placeholders in text that are neither built in nor listed in extra.
func Unknown(text string, extra ...string) []string {
	allowed := map[string]bool{}
	for _, k := range Known {
		allowed[k.Key] = true
	}
	for _, e := range extra {
		allowed[canonical(e)] = true
	}
	var bad []string
	seen := map[string]bool{}
	for _, m := range tokenRe.FindAllStringSubmatch(text, -1) {
		k := canonical(m[1])
		if !allowed[k] && !seen[k] {
			seen[k] = true
			bad = append(bad, k)
		}
	}
	// Single-brace or unbalanced placeholders are almost always typos.
	stripped := tokenRe.ReplaceAllString(text, "")
	if strings.Contains(stripped, "{{") || strings.Contains(stripped, "}}") {
		bad = append(bad, "unclosed {{ }}")
	}
	return bad
}

// Used returns the canonical variable names used in text.
func Used(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range tokenRe.FindAllStringSubmatch(text, -1) {
		k := canonical(m[1])
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Validate returns a user-facing error when text uses unknown variables.
func Validate(text string, extra ...string) error {
	if bad := Unknown(text, extra...); len(bad) > 0 {
		return fmt.Errorf("Unknown variable %s. Use the variable list to insert one of: %s.", quoteList(bad), knownList(extra))
	}
	return nil
}

// MissingError formats a list of missing variables for users.
func MissingError(missing []string) error {
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("The message uses %s but no value is available. Fill in the detail or remove the variable.", quoteList(missing))
}

func quoteList(keys []string) string {
	q := make([]string, len(keys))
	for i, k := range keys {
		q[i] = "{{" + k + "}}"
	}
	return strings.Join(q, ", ")
}

func knownList(extra []string) string {
	keys := make([]string, 0, len(Known)+len(extra))
	for _, k := range Known {
		keys = append(keys, "{{"+k.Key+"}}")
	}
	for _, e := range extra {
		keys = append(keys, "{{"+canonical(e)+"}}")
	}
	return strings.Join(keys, ", ")
}
