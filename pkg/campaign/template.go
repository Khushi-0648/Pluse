package campaign

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/msgtemplate"
)

// Template is a message template approved by Meta in WhatsApp Manager. It is the only
// kind of message WhatsApp delivers to a customer who has not written in the last 24
// hours. Params fill the template's {{1}}, {{2}}... in order; each may use Pulse
// variables such as {{first_name}}. A template with a header or link buttons also
// needs the header value (text or a media link) and the buttons' link endings.
type Template struct {
	Name         string   `json:"name"`
	Language     string   `json:"language"`
	Params       []string `json:"params,omitempty"`
	HeaderParams []string `json:"header_params,omitempty"` // the variable of a text header
	HeaderMedia  string   `json:"header_media,omitempty"`  // https link to a photo, video or document header
	// ButtonParams are the variable link endings of URL buttons, by button position; an
	// empty entry is a button without a variable.
	ButtonParams []string `json:"button_params,omitempty"`
}

// TemplateSender sends approved templates (the WhatsApp Cloud API client).
type TemplateSender interface {
	SendTemplate(ctx context.Context, to, name, lang string, parts cloudapi.TemplateParts) (string, error)
}

// OutsideWindowMessage explains why a free-text message cannot be sent.
const OutsideWindowMessage = "This customer has not written to you in the last 24 hours. WhatsApp then only allows a message template approved by Meta. Use a campaign or a scheduled message with an official template, or wait until the customer writes to you."

// NeedsTemplate reports whether only a template may be sent to ct now: WhatsApp's
// customer service window is open for 24 hours after the customer's last message, and
// Pulse stops sending free text half an hour before it closes (contacts.FreeTextWindow),
// so a message paced or retried at the end cannot reach Meta after it closed.
func NeedsTemplate(ct *contacts.Contact, now time.Time) bool {
	return ct == nil || !ct.CanSendFreeText(now)
}

var (
	templateNameRe = regexp.MustCompile(`^[a-z0-9_]{1,512}$`)
	templateLangRe = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?$`)
)

const (
	maxTemplateParams = 20
	maxHeaderParams   = 1  // WhatsApp allows one variable in a text header
	maxURLButtons     = 10 // buttons of a template
)

// checkValue validates one template value that may use Pulse variables.
func checkValue(label, p string) error {
	if len(p) > 1000 {
		return fmt.Errorf("Template value %s is too long.", label)
	}
	if err := msgtemplate.ValidateOutgoing(p, "email", "notes"); err != nil {
		return fmt.Errorf("Template value %s: %v", label, err)
	}
	return nil
}

// Clean trims the template and checks it. A nil or nameless template means "none".
func (t *Template) Clean() (*Template, error) {
	if t == nil || strings.TrimSpace(t.Name) == "" {
		return nil, nil
	}
	out := &Template{Name: strings.TrimSpace(t.Name), Language: strings.TrimSpace(t.Language), HeaderMedia: strings.TrimSpace(t.HeaderMedia)}
	if !templateNameRe.MatchString(out.Name) {
		return nil, errors.New("The template name is written exactly as in WhatsApp Manager: small letters, numbers and underscores, for example order_update.")
	}
	if out.Language == "" {
		out.Language = "en"
	}
	if !templateLangRe.MatchString(out.Language) {
		return nil, errors.New("The template language is a code such as en, en_US or hi, as shown in WhatsApp Manager.")
	}
	// Values fill {{1}}, {{2}}, ... by position: dropping an empty one in the middle would
	// move the next value into its place, so only empty ones at the end are dropped.
	for _, p := range t.Params {
		out.Params = append(out.Params, strings.TrimSpace(p))
	}
	for len(out.Params) > 0 && out.Params[len(out.Params)-1] == "" {
		out.Params = out.Params[:len(out.Params)-1]
	}
	for i, p := range out.Params {
		if p == "" {
			return nil, fmt.Errorf("Fill in the value for {{%d}}: WhatsApp needs every variable up to the last one you used.", i+1)
		}
	}
	if len(out.Params) > maxTemplateParams {
		return nil, fmt.Errorf("A template can have at most %d variables.", maxTemplateParams)
	}
	for i, p := range out.Params {
		if err := checkValue(fmt.Sprintf("{{%d}}", i+1), p); err != nil {
			return nil, err
		}
	}
	for _, p := range t.HeaderParams {
		if p = strings.TrimSpace(p); p != "" {
			out.HeaderParams = append(out.HeaderParams, p)
		}
	}
	switch {
	case len(out.HeaderParams) > maxHeaderParams:
		return nil, errors.New("A template header has at most one variable.")
	case len(out.HeaderParams) > 0 && out.HeaderMedia != "":
		return nil, errors.New("A template header is either text or a photo, video or document, not both.")
	}
	for _, p := range out.HeaderParams {
		if err := checkValue("in the header", p); err != nil {
			return nil, err
		}
	}
	if out.HeaderMedia != "" {
		// Meta downloads the header file itself, so it must be a public https link.
		if u, err := url.Parse(out.HeaderMedia); err != nil || u.Scheme != "https" || u.Host == "" || len(out.HeaderMedia) > 2000 {
			return nil, errors.New("The header photo, video or document must be a public https link, for example https://example.com/offer.jpg.")
		}
	}
	// Button endings keep their positions; only empty ones at the end are dropped.
	buttons := make([]string, 0, len(t.ButtonParams))
	for _, p := range t.ButtonParams {
		buttons = append(buttons, strings.TrimSpace(p))
	}
	for len(buttons) > 0 && buttons[len(buttons)-1] == "" {
		buttons = buttons[:len(buttons)-1]
	}
	if len(buttons) > maxURLButtons {
		return nil, fmt.Errorf("A template has at most %d buttons.", maxURLButtons)
	}
	for i, p := range buttons {
		if err := checkValue(fmt.Sprintf("of button %d", i+1), p); err != nil {
			return nil, err
		}
	}
	if len(buttons) > 0 {
		out.ButtonParams = buttons
	}
	return out, nil
}

// renderValues fills Pulse variables in each value; skipEmpty keeps an empty value as
// "no variable here" (button positions) instead of reporting it as missing.
func renderValues(values []string, v msgtemplate.Vars, skipEmpty bool) (out, missing []string) {
	for _, p := range values {
		if p == "" && skipEmpty {
			out = append(out, "")
			continue
		}
		text, miss := msgtemplate.Render(p, v)
		missing = append(missing, miss...)
		text = paramText(text)
		if text == "" && len(miss) == 0 {
			missing = append(missing, p)
		}
		out = append(out, text)
	}
	return out, missing
}

var longSpaces = regexp.MustCompile(` {5,}`)

// paramText makes a rendered value acceptable to WhatsApp, which refuses a template
// value with a line break, a tab or more than four spaces in a row (a customer's note
// or address often has them): those become single spaces.
func paramText(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ").Replace(s)
	return strings.TrimSpace(longSpaces.ReplaceAllString(s, " "))
}

// Render fills the template values for one customer. missing lists Pulse variables
// that are empty for them; WhatsApp refuses a template with an empty value.
func (t Template) Render(v msgtemplate.Vars) (parts cloudapi.TemplateParts, missing []string) {
	var m1, m2, m3 []string
	parts.Body, m1 = renderValues(t.Params, v, false)
	parts.Header, m2 = renderValues(t.HeaderParams, v, false)
	parts.URLButtons, m3 = renderValues(t.ButtonParams, v, true)
	parts.HeaderMedia = t.HeaderMedia
	return parts, append(append(m1, m2...), m3...)
}

// Describe is how a template message appears in the inbox and reports.
func (t Template) Describe(parts cloudapi.TemplateParts) string {
	s := "Template \"" + t.Name + "\""
	if parts.HeaderMedia != "" {
		s += " with a " + map[string]string{"image": "photo", "video": "video", "document": "document"}[cloudapi.HeaderMediaKind(parts.HeaderMedia)]
	}
	values := append(append([]string{}, parts.Header...), parts.Body...)
	if len(values) > 0 {
		s += ": " + strings.Join(values, " | ")
	}
	return s
}

// SendTemplate sends t, filled with parts, through s when it supports templates.
func SendTemplate(ctx context.Context, s any, to string, t Template, parts cloudapi.TemplateParts) (string, error) {
	ts, ok := s.(TemplateSender)
	if !ok {
		return "", errors.New("This connection cannot send templates.")
	}
	return ts.SendTemplate(ctx, to, t.Name, t.Language, parts)
}
