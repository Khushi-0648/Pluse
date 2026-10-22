package cloudapi

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"whatsapp-automation/pkg/contacts"
)

// VerifySignature checks Meta's X-Hub-Signature-256 header: "sha256=" followed by the
// hex HMAC-SHA256 of the raw body, keyed with the app secret. It runs in constant time.
func VerifySignature(appSecret string, body []byte, header string) bool {
	if appSecret == "" || !strings.HasPrefix(header, "sha256=") {
		return false
	}
	got, err := hex.DecodeString(strings.TrimPrefix(header, "sha256="))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(appSecret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// seenTTL is how long a delivered item is remembered. Meta repeats a webhook it thinks
// failed, for up to several days, but almost always within minutes.
const (
	seenTTL = 48 * time.Hour
	seenMax = 20000
)

type webhookBody struct {
	Object string `json:"object"`
	Entry  []struct {
		Changes []struct {
			Field string `json:"field"`
			Value struct {
				Metadata struct {
					PhoneNumberID string `json:"phone_number_id"`
				} `json:"metadata"`
				Contacts []struct {
					WaID    string `json:"wa_id"`
					Profile struct {
						Name string `json:"name"`
					} `json:"profile"`
				} `json:"contacts"`
				Messages []inboundMessage `json:"messages"`
				Statuses []struct {
					ID          string `json:"id"`
					Status      string `json:"status"`
					RecipientID string `json:"recipient_id"`
					Errors      []struct {
						Code    int    `json:"code"`
						Title   string `json:"title"`
						Message string `json:"message"`
					} `json:"errors"`
				} `json:"statuses"`
			} `json:"value"`
		} `json:"changes"`
	} `json:"entry"`
}

type caption struct {
	Caption  string `json:"caption"`
	Filename string `json:"filename"`
}

type inboundMessage struct {
	From      string `json:"from"`
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Text      struct {
		Body string `json:"body"`
	} `json:"text"`
	Button struct {
		Text string `json:"text"`
	} `json:"button"`
	Interactive struct {
		ButtonReply struct {
			Title string `json:"title"`
		} `json:"button_reply"`
		ListReply struct {
			Title string `json:"title"`
		} `json:"list_reply"`
	} `json:"interactive"`
	Image    caption `json:"image"`
	Video    caption `json:"video"`
	Document caption `json:"document"`
	Context  struct {
		ID string `json:"id"`
	} `json:"context"`
}

// mediaLabels name the kinds of messages that are not plain text, as the inbox shows them.
var mediaLabels = map[string]string{
	"image": "Photo", "video": "Video", "audio": "Voice message", "voice": "Voice message", "document": "Document",
	"sticker": "Sticker", "location": "Location", "contacts": "Contact card", "order": "Order",
}

// convert reduces a webhook message to what Pulse needs; ok is false for messages
// Pulse does not handle (reactions, system notices, unsupported types).
func (m inboundMessage) convert() (Message, bool) {
	out := Message{ID: m.ID, From: contacts.CleanPhone(m.From), ContextID: m.Context.ID}
	if sec, err := strconv.ParseInt(m.Timestamp, 10, 64); err == nil && sec > 0 {
		out.Timestamp = time.Unix(sec, 0)
	}
	switch m.Type {
	case "text":
		out.Text = m.Text.Body
	case "button":
		out.Text = m.Button.Text
	case "interactive":
		out.Text = m.Interactive.ButtonReply.Title
		if out.Text == "" {
			out.Text = m.Interactive.ListReply.Title
		}
	case "image":
		out.Text = m.Image.Caption
	case "video":
		out.Text = m.Video.Caption
	case "document":
		out.Text = m.Document.Caption
	}
	out.Text = strings.TrimSpace(out.Text)
	if l, ok := mediaLabels[m.Type]; ok {
		out.Media = l
	}
	if out.ID == "" || out.From == "" || (out.Text == "" && out.Media == "") {
		return Message{}, false
	}
	return out, true
}

// HandleWebhook processes a verified webhook body: incoming messages go to the
// OnMessage handlers and delivery updates to the OnReceipt handlers. Deliveries for
// another phone number ID and repeated deliveries are ignored. It returns how many
// items were handed on.
func (c *Client) HandleWebhook(body []byte) (int, error) {
	var b webhookBody
	if err := json.Unmarshal(body, &b); err != nil {
		return 0, errors.New("the body is not valid JSON")
	}
	own := c.cfg.Snapshot().CloudAPI.PhoneNumberID
	now := time.Now()
	c.mu.Lock()
	c.status.LastWebhook = &now
	msgH := append([]func(Message){}, c.onMessage...)
	recH := append([]func(Receipt){}, c.onReceipt...)
	c.mu.Unlock()
	handled := 0
	for _, e := range b.Entry {
		for _, ch := range e.Changes {
			v := ch.Value
			if ch.Field != "messages" || own == "" || v.Metadata.PhoneNumberID != own {
				continue
			}
			names := map[string]string{}
			for _, ct := range v.Contacts {
				names[contacts.CleanPhone(ct.WaID)] = strings.TrimSpace(ct.Profile.Name)
			}
			for _, raw := range v.Messages {
				m, ok := raw.convert()
				if !ok || !c.firstTime("m:"+m.ID) {
					continue
				}
				m.Name = names[m.From]
				c.mu.Lock()
				c.lastInbound[m.From] = m.ID
				c.mu.Unlock()
				for _, h := range msgH {
					h(m)
				}
				handled++
			}
			for _, s := range v.Statuses {
				if s.ID == "" || !c.firstTime("s:"+s.ID+":"+s.Status) {
					continue
				}
				r := Receipt{MessageID: s.ID, Phone: contacts.CleanPhone(s.RecipientID), Status: s.Status}
				switch s.Status {
				case "sent", "delivered", "read":
				case "failed":
					code, text := 0, ""
					if len(s.Errors) > 0 {
						code, text = s.Errors[0].Code, s.Errors[0].Message
						if text == "" {
							text = s.Errors[0].Title
						}
					}
					r.Error = plainError(code, text)
				default:
					continue
				}
				for _, h := range recH {
					h(r)
				}
				handled++
			}
		}
	}
	return handled, nil
}

// firstTime records key and reports whether it is new. The memory is bounded: old
// entries expire, and when it is still full the oldest half is dropped.
func (c *Client) firstTime(key string) bool {
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.seen[key]; dup {
		return false
	}
	if len(c.seen) >= seenMax {
		for k, at := range c.seen {
			if now.Sub(at) > seenTTL {
				delete(c.seen, k)
			}
		}
		if len(c.seen) >= seenMax {
			target := (seenMax * 3) / 4
			for k := range c.seen {
				delete(c.seen, k)
				if len(c.seen) <= target {
					break
				}
			}
		}
	}
	c.seen[key] = now
	return true
}
