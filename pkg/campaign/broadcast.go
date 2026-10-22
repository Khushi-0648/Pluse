// Package campaign runs paced broadcast campaigns and keeps a report for each one.
package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/securestore"
	"whatsapp-automation/pkg/security"
)

// Sender sends text messages.
type Sender interface {
	SendTextMessage(ctx context.Context, target, text string) (string, error)
}

// Recipient status values.
const (
	RPending   = "pending"
	RSent      = "sent"
	RDelivered = "delivered"
	RRead      = "read"
	RFailed    = "failed"
	RSkipped   = "skipped"
	RSending   = "sending"
)

// Campaign status values.
const (
	CRunning   = "running"
	CPaused    = "paused"
	CCompleted = "completed"
	CCancelled = "cancelled"
	CStopped   = "stopped"
)

type Recipient struct {
	Phone     string            `json:"phone"`
	Name      string            `json:"name,omitempty"`
	Vars      map[string]string `json:"vars,omitempty"`
	Status    string            `json:"status"`
	MessageID string            `json:"message_id,omitempty"`
	Error     string            `json:"error,omitempty"`
	SentAt    *time.Time        `json:"sent_at,omitempty"`
	RepliedAt *time.Time        `json:"replied_at,omitempty"`
}

type Exclusion struct {
	Phone  string `json:"phone"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason"`
}

type Campaign struct {
	ID         string      `json:"id"`
	Name       string      `json:"name"`
	Message    string      `json:"message"`
	Audience   string      `json:"audience"`
	Status     string      `json:"status"`
	StopReason string      `json:"stop_reason,omitempty"`
	CreatedBy  string      `json:"created_by,omitempty"`
	CreatedAt  time.Time   `json:"created_at"`
	StartedAt  *time.Time  `json:"started_at,omitempty"`
	FinishedAt *time.Time  `json:"finished_at,omitempty"`
	Recipients []Recipient `json:"recipients"`
	Excluded   []Exclusion `json:"excluded,omitempty"`
	// Template goes to recipients who have not written in the last 24 hours.
	Template *Template `json:"template,omitempty"`
}

// Report is the per-campaign summary.
type Report struct {
	Total     int `json:"total"`
	Pending   int `json:"pending"`
	Sent      int `json:"sent"`
	Delivered int `json:"delivered"`
	Read      int `json:"read"`
	Replied   int `json:"replied"`
	Failed    int `json:"failed"`
	Skipped   int `json:"skipped"`
	Excluded  int `json:"excluded"`
}

// Summary is a campaign without its recipient list.
type Summary struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Message    string     `json:"message"`
	Audience   string     `json:"audience"`
	Status     string     `json:"status"`
	StopReason string     `json:"stop_reason,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	StartedAt  *time.Time `json:"started_at,omitempty"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Report     Report     `json:"report"`
}

func (c *Campaign) report() Report {
	r := Report{Total: len(c.Recipients), Excluded: len(c.Excluded)}
	for _, x := range c.Recipients {
		switch x.Status {
		case RPending, RSending:
			r.Pending++
		case RFailed:
			r.Failed++
		case RSkipped:
			r.Skipped++
		case RRead:
			r.Read++
			r.Delivered++
			r.Sent++
		case RDelivered:
			r.Delivered++
			r.Sent++
		case RSent:
			r.Sent++
		}
		if x.RepliedAt != nil {
			r.Replied++
		}
	}
	return r
}

func (c *Campaign) summary() Summary {
	return Summary{ID: c.ID, Name: c.Name, Message: c.Message, Audience: c.Audience, Status: c.Status, StopReason: c.StopReason, CreatedAt: c.CreatedAt, StartedAt: c.StartedAt, FinishedAt: c.FinishedAt, Report: c.report()}
}

// Runner stores campaigns and sends at most one at a time.
type Runner struct {
	mu        sync.Mutex
	path      string
	box       *securestore.Box
	campaigns []*Campaign
	byMsg     map[string]*Recipient
	saver     *securestore.Saver

	sender   Sender
	cfg      *config.Config
	contacts *contacts.Store
	hist     *history.Store

	active *Campaign
	gen    int
	cancel context.CancelFunc
	wake   chan struct{}
}

const maxCampaigns = 100

// NewRunner loads saved campaigns from path. Campaigns that were running when the app
// stopped are marked paused so nothing is sent without the owner resuming.
func NewRunner(path string, s Sender, cfg *config.Config, cs *contacts.Store, hs *history.Store) (*Runner, error) {
	r := &Runner{path: path, box: securestore.Default(), sender: s, cfg: cfg, contacts: cs, hist: hs, byMsg: map[string]*Recipient{}, wake: make(chan struct{}, 1)}
	r.saver = securestore.NewSaver(time.Second, r.persist)
	if path != "" {
		data, err := r.box.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if len(data) > 0 {
			if err := json.Unmarshal(data, &r.campaigns); err != nil {
				return nil, fmt.Errorf("campaign file %s is damaged: %w", path, err)
			}
		}
	}
	for _, c := range r.campaigns {
		if c.Status == CRunning {
			c.Status, c.StopReason = CPaused, "Pulse was restarted while this campaign was sending. Press Resume to continue."
		}
		for i := range c.Recipients {
			if c.Recipients[i].Status == RSending {
				c.Recipients[i].Status, c.Recipients[i].Error = RFailed, "Interrupted while sending; check whether it arrived before sending again."
			}
		}
		r.indexLocked(c)
	}
	return r, nil
}

func (r *Runner) indexLocked(c *Campaign) {
	for i := range c.Recipients {
		if id := c.Recipients[i].MessageID; id != "" {
			r.byMsg[id] = &c.Recipients[i]
		}
	}
}

func (r *Runner) persist() error {
	if r.path == "" {
		return nil
	}
	r.mu.Lock()
	data, err := json.Marshal(r.campaigns)
	r.mu.Unlock()
	if err != nil {
		return err
	}
	return r.box.WriteFile(r.path, data)
}

// Flush writes pending changes.
func (r *Runner) Flush() error { return r.saver.Flush() }

// List returns campaign summaries, newest first.
func (r *Runner) List() []Summary {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Summary, 0, len(r.campaigns))
	for i := len(r.campaigns) - 1; i >= 0; i-- {
		out = append(out, r.campaigns[i].summary())
	}
	return out
}

// Get returns a copy of a campaign with its recipients.
func (r *Runner) Get(id string) (Campaign, Report, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.campaigns {
		if c.ID == id {
			cp := *c
			cp.Recipients = append([]Recipient{}, c.Recipients...)
			cp.Excluded = append([]Exclusion{}, c.Excluded...)
			return cp, c.report(), true
		}
	}
	return Campaign{}, Report{}, false
}

// Active returns the summary of the running or paused campaign, if any.
func (r *Runner) Active() (Summary, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		for i := len(r.campaigns) - 1; i >= 0; i-- {
			if r.campaigns[i].Status == CPaused {
				return r.campaigns[i].summary(), true
			}
		}
		return Summary{}, false
	}
	return r.active.summary(), true
}

// Audience selects who a campaign goes to.
type Audience struct {
	All    bool     `json:"all"`
	Tag    string   `json:"tag"`
	Phones []string `json:"phones"`
}

// Plan is the result of resolving an audience: who will receive the message and who is left out.
type Plan struct {
	Description string      `json:"description"`
	Recipients  []Recipient `json:"recipients"`
	Excluded    []Exclusion `json:"excluded"`
	Samples     []Sample    `json:"samples"`
	DailyLeft   int         `json:"daily_left"`
	// OutsideWindow counts recipients who have not written in the last 24 hours; they
	// can only receive Template.
	OutsideWindow int       `json:"outside_window"`
	Template      *Template `json:"template,omitempty"`
}

type Sample struct {
	Phone string `json:"phone"`
	Name  string `json:"name"`
	Text  string `json:"text"`
}

// DailyRemaining returns how many more messages may be sent today.
func DailyRemaining(cfg config.Config, hs *history.Store, now time.Time) int {
	limit := cfg.Automation.DailyLimit
	if cfg.Automation.WarmupMode && limit > 50 {
		limit = 50
	}
	if hs == nil {
		return limit
	}
	y, m, d := now.Date()
	left := limit - hs.SentSince(time.Date(y, m, d, 0, 0, 0, 0, now.Location()))
	if left < 0 {
		return 0
	}
	return left
}

// inFlight counts messages that passed the daily-limit check but are not yet in the
// history. Automatic replies, campaigns and scheduled messages send in parallel, so
// without it each could see "one left" and all three would send (F19).
var inFlight struct {
	mu sync.Mutex
	n  int
}

// ReserveDaily takes one message from today's limit before sending. ok is false when
// the limit is used up. release must be called once the send is recorded in the
// history (sent or failed), which is when the history count takes over.
func ReserveDaily(cfg config.Config, hs *history.Store, now time.Time) (release func(), ok bool) {
	inFlight.mu.Lock()
	defer inFlight.mu.Unlock()
	if DailyRemaining(cfg, hs, now)-inFlight.n <= 0 {
		return func() {}, false
	}
	inFlight.n++
	var once sync.Once
	return func() {
		once.Do(func() {
			inFlight.mu.Lock()
			inFlight.n--
			inFlight.mu.Unlock()
		})
	}, true
}

// BuildPlan resolves an audience against the contact list and the safety rules,
// giving a reason for every contact that is left out.
func BuildPlan(cfg config.Config, cs *contacts.Store, hs *history.Store, message string, aud Audience) (Plan, error) {
	if strings.TrimSpace(message) == "" {
		return Plan{}, errors.New("Write the message first.")
	}
	if err := msgtemplate.Validate(message, "email", "notes"); err != nil {
		return Plan{}, err
	}
	if cs == nil {
		return Plan{}, errors.New("The contact list is not available.")
	}
	var pool []contacts.Contact
	p := Plan{Recipients: []Recipient{}, Excluded: []Exclusion{}, Samples: []Sample{}}
	tag := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(aud.Tag), "tag:"))
	switch {
	case len(aud.Phones) > 0:
		p.Description = fmt.Sprintf("%d selected contacts", len(aud.Phones))
		for _, raw := range aud.Phones {
			if c, ok := cs.Get(raw); ok {
				pool = append(pool, *c)
			} else {
				p.Excluded = append(p.Excluded, Exclusion{Phone: contacts.CleanPhone(raw), Reason: "Not in your contact list"})
			}
		}
	case aud.All || strings.EqualFold(tag, "all"):
		p.Description = "All contacts"
		pool = cs.List()
	case tag != "":
		p.Description = "Tag: " + tag
		pool = cs.GetByTag(tag)
		if len(pool) == 0 {
			return p, fmt.Errorf("No contact has the tag %q. Tags in use: %s.", tag, strings.Join(cs.GetAllTags(), ", "))
		}
	default:
		return p, errors.New("Choose who should receive the campaign.")
	}

	a := cfg.Automation
	seen := map[string]bool{}
	for _, c := range pool {
		x := Exclusion{Phone: c.Phone, Name: c.Name}
		switch {
		case seen[c.Phone]:
			continue
		case len(c.Phone) < 7 || contacts.IsLID(c.Phone):
			x.Reason = "No usable phone number"
		case c.IsDND:
			x.Reason = "Opted out (replied STOP or was marked do-not-message)"
		case a.RequireOptIn && !c.Consent:
			x.Reason = "No opt-in recorded. Record consent on the contact, or import a consent column."
		case containsPhone(a.BlacklistPhones, c.Phone):
			x.Reason = "On the blocked numbers list"
		case a.AllowListOnly && !containsPhone(a.AllowList, c.Phone):
			x.Reason = "Test mode is on and this is not one of the test numbers"
		case a.Enforce24hWindow && !c.Consent && !c.InServiceWindow(time.Now()):
			x.Reason = "No opt-in and no message from them in the last 24 hours"
		}
		seen[c.Phone] = true
		if x.Reason != "" {
			p.Excluded = append(p.Excluded, x)
			continue
		}
		vars := map[string]string{"email": c.Email, "notes": c.Notes}
		text, missing := msgtemplate.Render(message, msgtemplate.Vars{Name: c.Name, Phone: c.Phone, Business: cfg.Business, Extra: vars})
		if len(missing) > 0 {
			p.Excluded = append(p.Excluded, Exclusion{Phone: c.Phone, Name: c.Name, Reason: "No value for " + strings.Join(missing, ", ")})
			continue
		}
		p.Recipients = append(p.Recipients, Recipient{Phone: c.Phone, Name: c.Name, Vars: vars, Status: RPending})
		if NeedsTemplate(&c, time.Now()) {
			p.OutsideWindow++
		}
		if len(p.Samples) < 3 {
			p.Samples = append(p.Samples, Sample{Phone: c.Phone, Name: c.DisplayName(), Text: text})
		}
	}
	p.DailyLeft = DailyRemaining(cfg, hs, time.Now())
	return p, nil
}

func containsPhone(list []string, phone string) bool {
	for _, x := range list {
		if c := contacts.CleanPhone(x); c != "" && c == phone {
			return true
		}
	}
	return false
}

// Start creates a campaign from a plan and begins sending.
func (r *Runner) Start(name, message, createdBy string, plan Plan) (Summary, error) {
	if len(plan.Recipients) == 0 {
		reasons := map[string]int{}
		for _, x := range plan.Excluded {
			reasons[x.Reason]++
		}
		var parts []string
		for k, v := range reasons {
			parts = append(parts, fmt.Sprintf("%d: %s", v, k))
		}
		sort.Strings(parts)
		if len(parts) == 0 {
			return Summary{}, errors.New("The audience is empty.")
		}
		return Summary{}, errors.New("Nobody in this audience can receive the campaign. " + strings.Join(parts, "; "))
	}
	if plan.OutsideWindow > 0 && plan.Template == nil {
		return Summary{}, fmt.Errorf("%d of these customers have not written to you in the last 24 hours. WhatsApp only lets you reach them with a message template approved by Meta: fill in \"Official WhatsApp template\", or send only to customers who wrote recently.", plan.OutsideWindow)
	}
	cfg := r.cfg.Snapshot()
	if left := DailyRemaining(cfg, r.hist, time.Now()); len(plan.Recipients) > left {
		return Summary{}, fmt.Errorf("This campaign has %d recipients but only %d messages are left in today's limit. Send to fewer people or continue tomorrow.", len(plan.Recipients), left)
	}
	r.mu.Lock()
	if r.active != nil {
		r.mu.Unlock()
		return Summary{}, errors.New("Another campaign is sending. Pause or cancel it first.")
	}
	for _, c := range r.campaigns {
		if c.Status == CPaused {
			r.mu.Unlock()
			return Summary{}, fmt.Errorf("Campaign %q is paused. Resume or cancel it before starting a new one.", c.Name)
		}
	}
	if strings.TrimSpace(name) == "" {
		name = "Campaign " + time.Now().Format("02 Jan 15:04")
	}
	now := time.Now()
	c := &Campaign{ID: "cmp_" + security.RandomToken(6), Name: strings.TrimSpace(name), Message: message, Audience: plan.Description, Status: CRunning, CreatedBy: createdBy, CreatedAt: now, StartedAt: &now, Recipients: plan.Recipients, Excluded: plan.Excluded, Template: plan.Template}
	r.campaigns = append(r.campaigns, c)
	if len(r.campaigns) > maxCampaigns {
		r.campaigns = r.campaigns[len(r.campaigns)-maxCampaigns:]
	}
	r.startLocked(c)
	s := c.summary()
	r.mu.Unlock()
	r.saver.Trigger()
	security.LogAudit("BROADCAST_START", "local", createdBy, fmt.Sprintf("Campaign %q started for %d recipients (%d left out)", c.Name, len(c.Recipients), len(c.Excluded)), "success")
	return s, nil
}

func (r *Runner) startLocked(c *Campaign) {
	ctx, cancel := context.WithCancel(context.Background())
	r.gen++
	r.active, r.cancel = c, cancel
	c.Status, c.StopReason = CRunning, ""
	go r.run(ctx, c, r.gen)
}

// Pause stops sending after the current message.
func (r *Runner) Pause(reason string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return false
	}
	r.active.Status = CPaused
	r.active.StopReason = reason
	r.cancel()
	r.active, r.cancel = nil, nil
	r.saver.Trigger()
	return true
}

// Resume continues a paused campaign.
func (r *Runner) Resume(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil {
		return errors.New("Another campaign is sending.")
	}
	for _, c := range r.campaigns {
		if c.ID == id && c.Status == CPaused {
			pending := 0
			for _, x := range c.Recipients {
				if x.Status == RPending {
					pending++
				}
			}
			if left := DailyRemaining(r.cfg.Snapshot(), r.hist, time.Now()); pending > left {
				return fmt.Errorf("%d messages are still to send but only %d are left in today's limit. Resume tomorrow.", pending, left)
			}
			r.startLocked(c)
			r.saver.Trigger()
			return nil
		}
	}
	return errors.New("No paused campaign with that ID.")
}

// Cancel stops a running or paused campaign permanently.
func (r *Runner) Cancel(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.campaigns {
		if (id == "" || c.ID == id) && (c.Status == CRunning || c.Status == CPaused) {
			if r.active == c {
				r.cancel()
				r.active, r.cancel = nil, nil
			}
			now := time.Now()
			c.Status, c.FinishedAt = CCancelled, &now
			if c.StopReason == "" {
				c.StopReason = "Cancelled"
			}
			r.saver.Trigger()
			return true
		}
	}
	return false
}

// finishRun ends the run identified by gen. A run that was paused and resumed meanwhile
// has a newer gen, so an old sending loop can never stop the new one.
func (r *Runner) finishRun(c *Campaign, gen int, status, reason string) {
	r.mu.Lock()
	if r.active != c || r.gen != gen {
		// Paused or cancelled by the user meanwhile; their choice stands.
		r.mu.Unlock()
		return
	}
	r.cancel()
	r.active, r.cancel = nil, nil
	c.Status, c.StopReason = status, reason
	if status != CPaused {
		now := time.Now()
		c.FinishedAt = &now
	}
	rep := c.report()
	r.mu.Unlock()
	_ = r.saver.Flush()
	security.LogAudit("BROADCAST_"+strings.ToUpper(status), "local", c.CreatedBy, fmt.Sprintf("Campaign %q: %d sent, %d failed. %s", c.Name, rep.Sent, rep.Failed, reason), "success")
}

// connectionChecker is implemented by the WhatsApp connection.
type connectionChecker interface{ Connected() bool }

const pausedOffline = "Paused because WhatsApp is not connected. Press Resume once Settings > WhatsApp connection shows it is working."

// sendSkipReason re-checks the safety rules just before a message goes out, because a
// paused campaign can be resumed days later and the contact or settings may have changed.
func sendSkipReason(cfg config.Config, ct *contacts.Contact, ok bool, phone string, now time.Time) string {
	a := cfg.Automation
	switch {
	case !ok:
		return "Contact was deleted before the message was sent"
	case ct.IsDND:
		return "Opted out before the message was sent"
	case containsPhone(a.BlacklistPhones, phone):
		return "On the blocked numbers list"
	case a.AllowListOnly && !containsPhone(a.AllowList, phone):
		return "Test mode is on and this is not one of the test numbers"
	case a.RequireOptIn && !ct.Consent:
		return "Opt-in was withdrawn"
	case a.Enforce24hWindow && !ct.Consent && !ct.InServiceWindow(now):
		return "No opt-in and no message from them in the last 24 hours"
	}
	return ""
}

// recipientIndex finds the recipient this run claimed. It looks the phone up again
// every time because erasure requests can shorten the list while a message is sending.
func recipientIndex(c *Campaign, phone string) int {
	for j := range c.Recipients {
		if c.Recipients[j].Phone == phone && c.Recipients[j].Status == RSending {
			return j
		}
	}
	return -1
}

func (r *Runner) run(ctx context.Context, c *Campaign, gen int) {
	defer func() {
		if p := recover(); p != nil {
			security.LogAudit("BROADCAST_ERROR", "local", "system", fmt.Sprintf("Campaign %q stopped after an internal error: %v", c.Name, p), "failed")
			r.finishRun(c, gen, CPaused, "Stopped after an internal error. Press Resume to continue.")
		}
	}()
	consecutiveFails := 0
	sentThisRun := 0
	for {
		if ctx.Err() != nil {
			return
		}
		// Claim the next recipient so a resumed run can never send to them twice.
		r.mu.Lock()
		phone, inFlight := "", false
		var rec Recipient
		for j := range c.Recipients {
			if c.Recipients[j].Status == RSending {
				inFlight = true
			}
			if phone == "" && c.Recipients[j].Status == RPending {
				c.Recipients[j].Status = RSending
				rec, phone = c.Recipients[j], c.Recipients[j].Phone
			}
		}
		r.mu.Unlock()
		if phone == "" {
			if inFlight {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Second):
				}
				continue
			}
			r.finishRun(c, gen, CCompleted, "")
			return
		}
		release := func() {
			r.mu.Lock()
			if j := recipientIndex(c, phone); j >= 0 {
				c.Recipients[j].Status = RPending
			}
			r.mu.Unlock()
		}
		cfg := r.cfg.Snapshot()
		a := cfg.Automation
		releaseDaily, ok := ReserveDaily(cfg, r.hist, time.Now())
		if !ok {
			release()
			r.finishRun(c, gen, CPaused, "Today's sending limit was reached. Press Resume tomorrow to send the rest.")
			return
		}
		unclaim := release
		release = func() { releaseDaily(); unclaim() }
		if reason := r.optOutSpike(c); reason != "" {
			release()
			r.finishRun(c, gen, CPaused, reason)
			return
		}
		if cc, ok := r.sender.(connectionChecker); ok && !a.DryRun && !cc.Connected() {
			release()
			r.finishRun(c, gen, CPaused, pausedOffline)
			return
		}

		status, errText, msgID := RSent, "", ""
		ct, found := r.contacts.Get(rec.Phone)
		if reason := sendSkipReason(cfg, ct, found, rec.Phone, time.Now()); reason != "" {
			status, errText = RSkipped, reason
		} else {
			vars := msgtemplate.Vars{Name: rec.Name, Phone: rec.Phone, Business: cfg.Business, Extra: rec.Vars}
			text, missing := msgtemplate.Render(c.Message, vars)
			// A customer who has not written in 24 hours can only get the approved template.
			var params []string
			useTemplate := NeedsTemplate(ct, time.Now())
			if useTemplate && c.Template != nil {
				params, missing = c.Template.Render(vars)
				text = c.Template.Describe(params)
			}
			switch {
			case useTemplate && c.Template == nil:
				status, errText = RSkipped, "Has not written in the last 24 hours, and this campaign has no approved template"
			case len(missing) > 0:
				status, errText = RSkipped, "No value for "+strings.Join(missing, ", ")
			case a.DryRun:
				status, errText, msgID = RSkipped, "Practice mode: not sent", ""
				if r.hist != nil {
					r.hist.Record(history.MessageRecord{ID: "dry_" + security.RandomToken(6), Direction: history.DirectionOutgoing, Type: history.TypeBroadcast, Phone: rec.Phone, Text: text, CampaignID: c.ID, Status: history.StatusDryRun})
				}
			default:
				// Pausing must not interrupt a message that is already on its way: the
				// server may have it even if the acknowledgement is cut off, and treating
				// it as unsent would send it twice on Resume.
				sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
				var id string
				var err error
				if useTemplate {
					id, err = SendTemplate(sctx, r.sender, rec.Phone, *c.Template, params)
				} else {
					id, err = r.sender.SendTextMessage(sctx, rec.Phone, text)
				}
				cancel()
				if cloudapi.IsNotConnected(err) {
					release()
					r.finishRun(c, gen, CPaused, pausedOffline)
					return
				}
				hrec := history.MessageRecord{ID: id, Direction: history.DirectionOutgoing, Type: history.TypeBroadcast, Phone: rec.Phone, Text: text, CampaignID: c.ID, Status: history.StatusSent}
				if err != nil {
					status, errText = RFailed, err.Error()
					hrec.ID, hrec.Status, hrec.Error = "fail_"+security.RandomToken(6), history.StatusFailed, err.Error()
				} else {
					msgID = id
					r.contacts.UpdateLastContacted(rec.Phone)
				}
				if r.hist != nil {
					r.hist.Record(hrec)
				}
			}
		}
		releaseDaily() // the history counts it from here on

		now := time.Now()
		r.mu.Lock()
		if j := recipientIndex(c, phone); j >= 0 {
			x := &c.Recipients[j]
			x.Status, x.Error, x.MessageID = status, errText, msgID
			if status == RSent {
				x.SentAt = &now
				r.byMsg[msgID] = x
			}
		}
		r.mu.Unlock()
		r.saver.Trigger()

		if status == RFailed {
			consecutiveFails++
			if consecutiveFails >= 5 {
				r.finishRun(c, gen, CPaused, "Paused after 5 failed messages in a row to protect the number. Check the WhatsApp connection, then resume.")
				return
			}
		} else if status == RSent {
			consecutiveFails = 0
			sentThisRun++
		}
		if status != RSent && status != RFailed {
			continue // nothing was sent, no need to wait
		}

		delay := Pace(a, sentThisRun)
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// paceOverride replaces the delay between messages when set (tests only).
var paceOverride time.Duration

// Pace returns the wait after the sent-th message: a random delay within the configured
// pace, or the batch pause after every batch. The batch pause is never shorter than the
// normal delay, so a batch pause of 0 does not mean "no wait". Campaigns and scheduled
// messages share it.
func Pace(a config.AutomationConfig, sent int) time.Duration {
	if paceOverride > 0 {
		return paceOverride
	}
	d := normalDelay(a)
	if a.BatchSize > 0 && sent > 0 && sent%a.BatchSize == 0 {
		if bp := time.Duration(a.BatchPauseSeconds) * time.Second; bp > d {
			return bp
		}
	}
	return d
}

func normalDelay(a config.AutomationConfig) time.Duration {
	min, max := a.BroadcastMinDelay, a.BroadcastMaxDelay
	if min < 2 {
		min = 2
	}
	if max < min {
		max = min
	}
	sec := min + rand.Intn(max-min+1)
	return time.Duration(sec)*time.Second + time.Duration(rand.Intn(1000))*time.Millisecond
}

// optOutSpike pauses a campaign when recipients start opting out in numbers,
// which is a strong signal that WhatsApp will restrict the number.
func (r *Runner) optOutSpike(c *Campaign) string {
	r.mu.Lock()
	var sent []string
	for _, x := range c.Recipients {
		if x.SentAt != nil {
			sent = append(sent, x.Phone)
		}
	}
	r.mu.Unlock()
	if len(sent) < 10 {
		return ""
	}
	out := 0
	for _, p := range sent {
		if ct, ok := r.contacts.Get(p); ok && ct.IsDND {
			out++
		}
	}
	if out >= 3 && out*100/len(sent) >= 5 {
		return fmt.Sprintf("Paused automatically: %d of %d people who received this campaign opted out. Check the message before continuing.", out, len(sent))
	}
	return ""
}

// OnFailed records that WhatsApp could not deliver a message it had accepted.
func (r *Runner) OnFailed(id, reason string) {
	r.mu.Lock()
	x, ok := r.byMsg[id]
	if ok {
		x.Status, x.Error = RFailed, reason
	}
	r.mu.Unlock()
	if ok {
		r.saver.Trigger()
	}
}

// OnReceipt records delivered and read receipts.
func (r *Runner) OnReceipt(ids []string, status string) {
	rank := map[string]int{RSent: 1, RDelivered: 2, RRead: 3}
	changed := false
	r.mu.Lock()
	for _, id := range ids {
		if x, ok := r.byMsg[id]; ok && rank[status] > rank[x.Status] {
			x.Status = status
			changed = true
		}
	}
	r.mu.Unlock()
	if changed {
		r.saver.Trigger()
	}
}

// OnIncoming marks a recipient as having replied within three days of the campaign.
func (r *Runner) OnIncoming(phone string, at time.Time) {
	changed := false
	r.mu.Lock()
	for i := len(r.campaigns) - 1; i >= 0; i-- {
		c := r.campaigns[i]
		if c.StartedAt == nil || at.Sub(*c.StartedAt) > 4*24*time.Hour {
			break
		}
		for j := range c.Recipients {
			x := &c.Recipients[j]
			if x.Phone == phone && x.SentAt != nil && x.RepliedAt == nil && at.After(*x.SentAt) && at.Sub(*x.SentAt) < 72*time.Hour {
				t := at
				x.RepliedAt = &t
				changed = true
			}
		}
	}
	r.mu.Unlock()
	if changed {
		r.saver.Trigger()
	}
}

// Delete removes a finished campaign's report.
func (r *Runner) Delete(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, c := range r.campaigns {
		if c.ID == id && c.Status != CRunning {
			r.campaigns = append(r.campaigns[:i], r.campaigns[i+1:]...)
			r.saver.Trigger()
			return true
		}
	}
	return false
}

// ForgetPhone removes one customer from every campaign report (erasure requests).
func (r *Runner) ForgetPhone(phone string) {
	r.mu.Lock()
	for _, c := range r.campaigns {
		kept := c.Recipients[:0:0]
		for _, x := range c.Recipients {
			if x.Phone == phone {
				if x.MessageID != "" {
					delete(r.byMsg, x.MessageID)
				}
				continue
			}
			kept = append(kept, x)
		}
		// Safe for a running campaign too: its sending loop finds recipients again by
		// phone under this lock, so a removed recipient is simply never sent to.
		if len(kept) != len(c.Recipients) {
			c.Recipients = kept
		}
		ex := c.Excluded[:0:0]
		for _, x := range c.Excluded {
			if x.Phone != phone {
				ex = append(ex, x)
			}
		}
		c.Excluded = ex
	}
	r.byMsg = map[string]*Recipient{}
	for _, c := range r.campaigns {
		r.indexLocked(c)
	}
	r.mu.Unlock()
	r.saver.Trigger()
	_ = r.saver.Flush()
	securestore.RemoveBackup(r.path)
}

// DeleteAll stops any campaign and deletes every campaign report.
func (r *Runner) DeleteAll() {
	r.CancelAll("All customer data was deleted.")
	r.mu.Lock()
	if r.active != nil {
		r.cancel()
		r.active, r.cancel = nil, nil
	}
	r.campaigns, r.byMsg = nil, map[string]*Recipient{}
	r.mu.Unlock()
	r.saver.Trigger()
	_ = r.saver.Flush()
	securestore.RemoveBackup(r.path)
}

// CancelAll stops any running campaign (used when WhatsApp is disconnected).
func (r *Runner) CancelAll(reason string) {
	r.mu.Lock()
	active := r.active
	r.mu.Unlock()
	if active != nil {
		r.Pause(reason)
	}
}
