// Package cloudapi connects Pulse to Meta's WhatsApp Business Platform (Cloud API):
// it sends messages through the Graph API and turns webhook deliveries into incoming
// messages and delivery updates. Every install uses its owner's own Meta app, number
// and access token, read from the settings on every call so a change applies at once.
package cloudapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
)

// DefaultBaseURL is Meta's Graph API.
const DefaultBaseURL = "https://graph.facebook.com"

// checkEvery is how often the connection is checked in the background.
const checkEvery = 10 * time.Minute

// Status is the connection state shown in the dashboard.
type Status struct {
	// Configured is true once the phone number ID and access token are entered.
	Configured bool `json:"configured"`
	// Connected is true when the last check with Meta succeeded.
	Connected     bool       `json:"connected"`
	Phone         string     `json:"phone,omitempty"`
	DisplayPhone  string     `json:"display_phone,omitempty"`
	VerifiedName  string     `json:"verified_name,omitempty"`
	QualityRating string     `json:"quality_rating,omitempty"`
	LastCheck     *time.Time `json:"last_check,omitempty"`
	LastWebhook   *time.Time `json:"last_webhook,omitempty"`
	Problem       string     `json:"problem,omitempty"`
}

// PhoneInfo is what Meta reports about the business number.
type PhoneInfo struct {
	DisplayPhone  string `json:"display_phone_number"`
	VerifiedName  string `json:"verified_name"`
	QualityRating string `json:"quality_rating"`
}

// Message is one incoming customer message from the webhook.
type Message struct {
	ID        string // wamid, unique per message
	From      string // the customer's number, digits only
	Name      string // the customer's WhatsApp profile name
	Text      string
	Media     string // "Photo", "Document"... when the message is not plain text
	ContextID string // the message the customer replied to, if any
	Timestamp time.Time
}

// Receipt reports what happened to a message Pulse sent.
type Receipt struct {
	MessageID string
	Phone     string
	Status    string // "sent", "delivered", "read" or "failed"
	Error     string // plain-language reason when Status is "failed"
}

// Client talks to the Graph API for one business number.
type Client struct {
	cfg  *config.Config
	http *http.Client
	// BaseURL replaces the Graph API address (tests).
	BaseURL string
	// retryWait is the pause before each retry of a refused request (tests shorten it).
	retryWait []time.Duration

	mu          sync.RWMutex
	status      Status
	onMessage   []func(Message)
	onReceipt   []func(Receipt)
	seen        map[string]time.Time // webhook items already handled (Meta repeats deliveries)
	lastInbound map[string]string    // newest incoming message per customer, for the typing indicator
	refresh     chan struct{}
	cancel      context.CancelFunc
	stopOnce    sync.Once
}

// New creates a client that reads its settings from cfg.
func New(cfg *config.Config) *Client {
	return &Client{
		cfg:         cfg,
		http:        &http.Client{Timeout: 45 * time.Second},
		BaseURL:     DefaultBaseURL,
		retryWait:   []time.Duration{2 * time.Second, 6 * time.Second},
		seen:        map[string]time.Time{},
		lastInbound: map[string]string{},
		refresh:     make(chan struct{}, 1),
	}
}

// OnMessage registers a handler for incoming messages.
func (c *Client) OnMessage(h func(Message)) {
	c.mu.Lock()
	c.onMessage = append(c.onMessage, h)
	c.mu.Unlock()
}

// OnReceipt registers a handler for delivery updates.
func (c *Client) OnReceipt(h func(Receipt)) {
	c.mu.Lock()
	c.onReceipt = append(c.onReceipt, h)
	c.mu.Unlock()
}

// Start checks the connection now and every few minutes until Stop.
func (c *Client) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancel = cancel
	c.mu.Unlock()
	go func() {
		t := time.NewTicker(checkEvery)
		defer t.Stop()
		for {
			cctx, done := context.WithTimeout(ctx, 30*time.Second)
			_, _ = c.Check(cctx)
			done()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			case <-c.refresh:
			}
		}
	}()
}

// Stop ends the background checks. Calling it again does nothing.
func (c *Client) Stop() {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		cancel := c.cancel
		c.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
}

// Refresh checks the connection again soon, for example after the settings changed.
func (c *Client) Refresh() {
	select {
	case c.refresh <- struct{}{}:
	default:
	}
}

// Forget clears what is known about the number after the credentials were removed.
func (c *Client) Forget() {
	c.mu.Lock()
	c.status = Status{}
	c.lastInbound = map[string]string{}
	c.mu.Unlock()
}

// GetStatus returns the current connection state.
func (c *Client) GetStatus() Status {
	if c == nil {
		return Status{}
	}
	snap := c.cfg.Snapshot()
	c.mu.RLock()
	st := c.status
	c.mu.RUnlock()
	st.Configured = snap.CloudAPI.Ready()
	if !st.Configured {
		st.Connected = false
		st.Problem = ""
	}
	return st
}

// Connected reports whether messages can be sent right now.
func (c *Client) Connected() bool { return c != nil && c.GetStatus().Connected }

// OwnPhone returns the business number, digits only.
func (c *Client) OwnPhone() string { return c.GetStatus().Phone }

// ErrNotConnected means nothing was sent because the connection is not working; callers
// wait and try again instead of counting it as a failed message.
var ErrNotConnected = errors.New("WhatsApp is not connected. Check Settings > WhatsApp connection.")

// ErrNotConfigured means the owner has not entered the Business API details yet.
var ErrNotConfigured = errors.New("Connect your WhatsApp Business API first (Settings > WhatsApp connection).")

// credentials returns the base address, number ID and token for a call.
func (c *Client) credentials() (string, string, error) {
	snap := c.cfg.Snapshot()
	if !snap.CloudAPI.Ready() {
		return "", "", ErrNotConfigured
	}
	tok, err := c.cfg.CloudToken()
	if err != nil || tok == "" {
		return "", "", errors.New("The saved access token cannot be read on this Windows account. Paste it again in Settings > WhatsApp connection.")
	}
	return strings.TrimRight(c.BaseURL, "/") + "/" + snap.CloudAPI.APIVersion + "/" + snap.CloudAPI.PhoneNumberID, tok, nil
}

// Check asks Meta about the business number and records the result.
func (c *Client) Check(ctx context.Context) (Status, error) {
	snap := c.cfg.Snapshot()
	if !snap.CloudAPI.Ready() {
		return c.GetStatus(), ErrNotConfigured
	}
	tok, err := c.cfg.CloudToken()
	if err != nil {
		c.setProblem("The saved access token cannot be read on this Windows account. Paste it again in Settings > WhatsApp connection.")
		return c.GetStatus(), err
	}
	info, err := c.Probe(ctx, snap.CloudAPI.APIVersion, snap.CloudAPI.PhoneNumberID, tok)
	now := time.Now()
	c.mu.Lock()
	c.status.LastCheck = &now
	if err != nil {
		c.status.Connected = false
		c.status.Problem = err.Error()
	} else {
		c.status.Connected, c.status.Problem = true, ""
		c.status.DisplayPhone, c.status.VerifiedName, c.status.QualityRating = info.DisplayPhone, info.VerifiedName, info.QualityRating
		c.status.Phone = contacts.CleanPhone(info.DisplayPhone)
	}
	c.mu.Unlock()
	return c.GetStatus(), err
}

// Probe checks a phone number ID and token without saving anything ("Test connection").
func (c *Client) Probe(ctx context.Context, version, phoneID, token string) (PhoneInfo, error) {
	if version == "" {
		version = config.DefaultGraphVersion
	}
	u := strings.TrimRight(c.BaseURL, "/") + "/" + version + "/" + phoneID + "?fields=display_phone_number,verified_name,quality_rating"
	var info PhoneInfo
	err := c.do(ctx, http.MethodGet, u, token, "", nil, &info)
	return info, err
}

func (c *Client) setProblem(p string) {
	c.mu.Lock()
	c.status.Connected, c.status.Problem = false, p
	c.mu.Unlock()
}

// APIError is an error answer from the Graph API, with a plain-language explanation.
type APIError struct {
	HTTPStatus int
	Code       int
	Message    string // Meta's own text, for the console
	Plain      string // shown to the owner
}

func (e *APIError) Error() string { return e.Plain }

// plainError explains a Graph API error code in words a shop owner understands.
func plainError(code int, msg string) string {
	switch code {
	case 190:
		return "The access token has expired or is wrong. Paste a new one in Settings > WhatsApp connection."
	case 131047:
		return "More than 24 hours have passed since this customer last wrote to you. WhatsApp only allows an approved template message now."
	case 131026:
		return "This message could not be delivered to that number. Check that the number uses WhatsApp."
	case 130429, 131056, 4, 80007:
		return "Messages are being sent too fast. Wait a few minutes and try again."
	case 132000, 132001, 132005, 132007, 132012, 132015, 132016:
		return "WhatsApp refused the template. Check its name, language and number of variables in WhatsApp Manager, and that it is approved."
	case 131030:
		return "This number is not on your test number's list of allowed recipients. Add it in Meta's WhatsApp setup, or use your real business number."
	case 131031:
		return "Meta has locked or restricted this WhatsApp Business account. Check WhatsApp Manager."
	case 10, 200:
		return "The access token is missing a permission. Give it whatsapp_business_messaging and whatsapp_business_management."
	case 100:
		return "WhatsApp did not accept a value Pulse sent. Check the phone number ID and the customer's number."
	case 131000, 1, 2:
		return "WhatsApp had a temporary problem. Try again in a few minutes."
	}
	if msg != "" {
		return "WhatsApp did not accept the request: " + msg
	}
	return "WhatsApp did not accept the request."
}

// do sends one Graph API request and decodes the JSON answer into out. A request that
// WhatsApp refused with "too many requests" or a server error is tried again, because
// nothing was accepted. A request that may have reached Meta (a timeout or a dropped
// connection after sending) is never repeated, so a customer never gets a message twice.
func (c *Client) do(ctx context.Context, method, url, token, contentType string, body []byte, out any) error {
	for attempt := 0; ; attempt++ {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, rd)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			if notSent(err) && attempt < len(c.retryWait) && c.wait(ctx, attempt) {
				continue
			}
			fmt.Printf("[WhatsApp] Request failed: %v\n", err)
			if notSent(err) {
				return ErrNotConnected
			}
			return errors.New("WhatsApp took too long to answer. Check the chat before sending again, so the customer does not get it twice.")
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out != nil && len(data) > 0 {
				if err := json.Unmarshal(data, out); err != nil {
					return errors.New("WhatsApp sent an answer Pulse could not read.")
				}
			}
			return nil
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500) && attempt < len(c.retryWait) && c.wait(ctx, attempt) {
			continue
		}
		var e struct {
			Error struct {
				Message      string `json:"message"`
				Code         int    `json:"code"`
				ErrorSubcode int    `json:"error_subcode"`
				ErrorData    struct {
					Details string `json:"details"`
				} `json:"error_data"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		msg := e.Error.Message
		if e.Error.ErrorData.Details != "" {
			msg += " (" + e.Error.ErrorData.Details + ")"
		}
		fmt.Printf("[WhatsApp] Meta answered HTTP %d, code %d: %s\n", resp.StatusCode, e.Error.Code, msg)
		ae := &APIError{HTTPStatus: resp.StatusCode, Code: e.Error.Code, Message: msg, Plain: plainError(e.Error.Code, e.Error.Message)}
		if ae.Code == 190 {
			c.setProblem(ae.Plain)
		}
		return ae
	}
}

func (c *Client) wait(ctx context.Context, attempt int) bool {
	select {
	case <-time.After(c.retryWait[attempt]):
		return true
	case <-ctx.Done():
		return false
	}
}

// notSent reports whether a transport error proves the request never reached Meta:
// the connection could not be opened at all. Anything later (timeouts, dropped
// connections) may have delivered it.
func notSent(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return true
	}
	var dns *net.DNSError
	return errors.As(err, &dns)
}

// IsNotConnected reports whether err means nothing could be sent because of the
// connection, so the message should wait rather than count as failed.
func IsNotConnected(err error) bool {
	return errors.Is(err, ErrNotConnected) || errors.Is(err, ErrNotConfigured)
}

// digits returns a recipient as Meta expects it: country code and number, digits only.
func digits(to string) (string, error) {
	d := contacts.CleanPhone(to)
	if len(d) < 7 || len(d) > 15 {
		return "", fmt.Errorf("%q is not a phone number with country code.", to)
	}
	return d, nil
}

// send posts a message object and returns the new message's ID.
func (c *Client) send(ctx context.Context, payload map[string]any) (string, error) {
	base, tok, err := c.credentials()
	if err != nil {
		return "", err
	}
	payload["messaging_product"] = "whatsapp"
	body, _ := json.Marshal(payload)
	var out struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
	}
	if err := c.do(ctx, http.MethodPost, base+"/messages", tok, "application/json", body, &out); err != nil {
		return "", err
	}
	if len(out.Messages) == 0 || out.Messages[0].ID == "" {
		return "", errors.New("WhatsApp accepted the request but returned no message ID. Check the chat before sending again.")
	}
	return out.Messages[0].ID, nil
}

func (c *Client) message(to, kind string, content any) (map[string]any, error) {
	d, err := digits(to)
	if err != nil {
		return nil, err
	}
	return map[string]any{"recipient_type": "individual", "to": d, "type": kind, kind: content}, nil
}

// SendTextMessage sends plain text.
func (c *Client) SendTextMessage(ctx context.Context, to, text string) (string, error) {
	return c.SendReply(ctx, to, text, "")
}

// SendReply sends text, shown as a reply to quotedID when it is set.
func (c *Client) SendReply(ctx context.Context, to, text, quotedID string) (string, error) {
	p, err := c.message(to, "text", map[string]any{"body": text, "preview_url": false})
	if err != nil {
		return "", err
	}
	if quotedID != "" {
		p["context"] = map[string]string{"message_id": quotedID}
	}
	return c.send(ctx, p)
}

// SendTemplate sends a Meta-approved template; bodyParams fill {{1}}, {{2}}... in order.
// Templates are the only messages allowed more than 24 hours after the customer wrote.
func (c *Client) SendTemplate(ctx context.Context, to, name, lang string, bodyParams []string) (string, error) {
	tpl := map[string]any{"name": name, "language": map[string]string{"code": lang}}
	if len(bodyParams) > 0 {
		params := make([]map[string]string, 0, len(bodyParams))
		for _, v := range bodyParams {
			params = append(params, map[string]string{"type": "text", "text": v})
		}
		tpl["components"] = []map[string]any{{"type": "body", "parameters": params}}
	}
	p, err := c.message(to, "template", tpl)
	if err != nil {
		return "", err
	}
	return c.send(ctx, p)
}

// SendMediaMessage uploads a PNG or JPEG photo, or a document, and sends it with an
// optional caption. Other images (WebP) go as documents, which WhatsApp always accepts.
func (c *Client) SendMediaMessage(ctx context.Context, to string, data []byte, fileName, mimeType, caption string) (string, error) {
	if len(data) == 0 {
		return c.SendTextMessage(ctx, to, caption)
	}
	if _, err := digits(to); err != nil {
		return "", err
	}
	base, tok, err := c.credentials()
	if err != nil {
		return "", err
	}
	if mimeType == "" {
		mimeType = http.DetectContentType(data)
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("messaging_product", "whatsapp")
	_ = mw.WriteField("type", mimeType)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, safeName(fileName)))
	h.Set("Content-Type", mimeType)
	part, _ := mw.CreatePart(h)
	_, _ = part.Write(data)
	_ = mw.Close()
	var up struct {
		ID string `json:"id"`
	}
	// An upload is not a message: repeating it can never reach the customer twice.
	if err := c.do(ctx, http.MethodPost, base+"/media", tok, mw.FormDataContentType(), buf.Bytes(), &up); err != nil {
		return "", err
	}
	if up.ID == "" {
		return "", errors.New("WhatsApp did not accept the file. Try again.")
	}
	var p map[string]any
	if mimeType == "image/jpeg" || mimeType == "image/png" {
		img := map[string]any{"id": up.ID}
		if caption != "" {
			img["caption"] = caption
		}
		p, err = c.message(to, "image", img)
	} else {
		if fileName == "" {
			fileName = "document.pdf"
		}
		doc := map[string]any{"id": up.ID, "filename": safeName(fileName)}
		if caption != "" {
			doc["caption"] = caption
		}
		p, err = c.message(to, "document", doc)
	}
	if err != nil {
		return "", err
	}
	return c.send(ctx, p)
}

func safeName(n string) string {
	n = strings.Map(func(r rune) rune {
		if r < 32 || r == '"' || r == '\\' || r == '/' {
			return '_'
		}
		return r
	}, n)
	if n == "" {
		return "file"
	}
	return n
}

// MarkAsRead shows the customer's message as read (blue ticks).
func (c *Client) MarkAsRead(ctx context.Context, messageID string) error {
	if messageID == "" {
		return nil
	}
	_, err := c.postStatus(ctx, map[string]any{"status": "read", "message_id": messageID})
	return err
}

// ShowTyping marks the customer's newest message as read and shows "typing..." until
// the reply arrives (WhatsApp hides it after 25 seconds). It never fails a reply: with
// no known message it does nothing.
func (c *Client) ShowTyping(ctx context.Context, phone string) error {
	c.mu.RLock()
	id := c.lastInbound[contacts.CleanPhone(phone)]
	c.mu.RUnlock()
	if id == "" {
		return nil
	}
	_, err := c.postStatus(ctx, map[string]any{"status": "read", "message_id": id, "typing_indicator": map[string]string{"type": "text"}})
	return err
}

func (c *Client) postStatus(ctx context.Context, payload map[string]any) (bool, error) {
	base, tok, err := c.credentials()
	if err != nil {
		return false, err
	}
	payload["messaging_product"] = "whatsapp"
	body, _ := json.Marshal(payload)
	var out struct {
		Success bool `json:"success"`
	}
	err = c.do(ctx, http.MethodPost, base+"/messages", tok, "application/json", body, &out)
	return out.Success, err
}
