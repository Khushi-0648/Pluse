package campaign

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/msgtemplate"
)

// Template is a message template approved by Meta in WhatsApp Manager. It is the only
// kind of message WhatsApp delivers to a customer who has not written in the last 24
// hours. Params fill the template's {{1}}, {{2}}... in order; each may use Pulse
// variables such as {{first_name}}.
type Template struct {
	Name     string   `json:"name"`
	Language string   `json:"language"`
	Params   []string `json:"params,omitempty"`
}

// TemplateSender sends approved templates (the WhatsApp Cloud API client).
type TemplateSender interface {
	SendTemplate(ctx context.Context, to, name, lang string, bodyParams []string) (string, error)
}

// OutsideWindowMessage explains why a free-text message cannot be sent.
const OutsideWindowMessage = "This customer has not written to you in the last 24 hours. WhatsApp then only allows a message template approved by Meta. Use a campaign or a scheduled message with an official template, or wait until the customer writes to you."

// NeedsTemplate reports whether only a template may be sent to ct now: WhatsApp's
// customer service window is open for 24 hours after the customer's last message.
func NeedsTemplate(ct *contacts.Contact, now time.Time) bool {
	return ct == nil || !ct.InServiceWindow(now)
}

var (
	templateNameRe = regexp.MustCompile(`^[a-z0-9_]{1,512}$`)
	templateLangRe = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?$`)
)

const maxTemplateParams = 20

// Clean trims the template and checks it. A nil or nameless template means "none".
func (t *Template) Clean() (*Template, error) {
	if t == nil || strings.TrimSpace(t.Name) == "" {
		return nil, nil
	}
	out := &Template{Name: strings.TrimSpace(t.Name), Language: strings.TrimSpace(t.Language)}
	if !templateNameRe.MatchString(out.Name) {
		return nil, errors.New("The template name is written exactly as in WhatsApp Manager: small letters, numbers and underscores, for example order_update.")
	}
	if out.Language == "" {
		out.Language = "en"
	}
	if !templateLangRe.MatchString(out.Language) {
		return nil, errors.New("The template language is a code such as en, en_US or hi, as shown in WhatsApp Manager.")
	}
	for _, p := range t.Params {
		if p = strings.TrimSpace(p); p != "" {
			out.Params = append(out.Params, p)
		}
	}
	if len(out.Params) > maxTemplateParams {
		return nil, fmt.Errorf("A template can have at most %d variables.", maxTemplateParams)
	}
	for i, p := range out.Params {
		if len(p) > 1000 {
			return nil, fmt.Errorf("Template value {{%d}} is too long.", i+1)
		}
		if err := msgtemplate.Validate(p, "email", "notes"); err != nil {
			return nil, fmt.Errorf("Template value {{%d}}: %v", i+1, err)
		}
	}
	return out, nil
}

// Render fills the template values for one customer. missing lists Pulse variables
// that are empty for them; WhatsApp refuses a template with an empty value.
func (t Template) Render(v msgtemplate.Vars) (params, missing []string) {
	for _, p := range t.Params {
		text, miss := msgtemplate.Render(p, v)
		missing = append(missing, miss...)
		if strings.TrimSpace(text) == "" && len(miss) == 0 {
			missing = append(missing, p)
		}
		params = append(params, strings.TrimSpace(text))
	}
	return params, missing
}

// Describe is how a template message appears in the inbox and reports.
func (t Template) Describe(params []string) string {
	s := "Template \"" + t.Name + "\""
	if len(params) > 0 {
		s += ": " + strings.Join(params, " | ")
	}
	return s
}

// SendTemplate sends t through s when it supports templates.
func SendTemplate(ctx context.Context, s any, to string, t Template, params []string) (string, error) {
	ts, ok := s.(TemplateSender)
	if !ok {
		return "", errors.New("This connection cannot send templates.")
	}
	return ts.SendTemplate(ctx, to, t.Name, t.Language, params)
}
