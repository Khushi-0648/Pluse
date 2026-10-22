package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/security"
)

type configT = config.Config

type conversationView struct {
	Phone          string    `json:"phone"`
	Name           string    `json:"name"`
	LastText       string    `json:"last_text"`
	LastAt         time.Time `json:"last_at"`
	LastDirection  string    `json:"last_direction"`
	LastStatus     string    `json:"last_status"`
	Unread         int       `json:"unread"`
	NeedsAttention bool      `json:"needs_attention"`
	Reason         string    `json:"attention_reason,omitempty"`
	BotPaused      bool      `json:"bot_paused"`
	OptedOut       bool      `json:"opted_out"`
	Consent        bool      `json:"consent"`
	InWindow       bool      `json:"in_window"`
	Tags           []string  `json:"tags,omitempty"`
	AssignedTo     string    `json:"assigned_to,omitempty"`
	HasNote        bool      `json:"has_note"`
}

// handleChatMeta assigns a chat to a staff member and stores an internal note that
// customers never see (FT21).
func (s *Server) handleChatMeta(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone      string  `json:"phone"`
		AssignedTo *string `json:"assigned_to"`
		Notes      *string `json:"notes"`
	}
	if !decodeJSON(w, r, &req) || !s.needContacts(w) {
		return
	}
	before, _ := s.Contacts.Get(req.Phone)
	if err := s.Contacts.SetChatMeta(req.Phone, req.AssignedTo, req.Notes); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	c, _ := s.Contacts.Get(req.Phone)
	if req.AssignedTo != nil && (before == nil || before.AssignedTo != c.AssignedTo) {
		who := c.AssignedTo
		if who == "" {
			who = "nobody"
		}
		security.LogAudit("CHAT_ASSIGNED", clientIP(r), actor(r), "Chat with +"+c.Phone+" assigned to "+who, "success")
	}
	if req.Notes != nil && (before == nil || before.Notes != c.Notes) {
		security.LogAudit("CHAT_NOTE", clientIP(r), actor(r), "Internal note saved for +"+c.Phone, "success")
	}
	writeOK(w, map[string]any{"assigned_to": c.AssignedTo, "notes": c.Notes})
}

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("filter")
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("search")))
	qd := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, q)
	now := time.Now()
	out := []conversationView{}
	seen := map[string]bool{}
	if s.History != nil {
		for _, c := range s.History.Conversations() {
			v := conversationView{Phone: c.Phone, Name: c.Name, LastText: c.LastText, LastAt: c.LastAt, LastDirection: string(c.LastDirection), LastStatus: c.LastStatus}
			var lastRead *time.Time
			if s.Contacts != nil {
				if ct, ok := s.Contacts.Get(c.Phone); ok {
					if ct.Name != "" {
						v.Name = ct.Name
					}
					lastRead = ct.LastReadAt
					v.NeedsAttention, v.Reason, v.BotPaused = ct.NeedsAttention, ct.AttentionReason, ct.BotPaused(now)
					v.OptedOut, v.Consent, v.InWindow, v.Tags = ct.IsDND, ct.Consent, ct.InServiceWindow(now), ct.Tags
					v.AssignedTo, v.HasNote = ct.AssignedTo, ct.Notes != ""
				}
			}
			if msgtemplate.IsPlaceholderName(v.Name) {
				v.Name = ""
			}
			v.Unread = c.UnreadSince(lastRead)
			seen[c.Phone] = true
			out = append(out, v)
		}
	}
	if s.Contacts != nil {
		for _, ct := range s.Contacts.NeedsAttention() {
			if !seen[ct.Phone] {
				out = append(out, conversationView{Phone: ct.Phone, Name: ct.Name, NeedsAttention: true, Reason: ct.AttentionReason, BotPaused: ct.BotPaused(now), LastAt: derefTime(ct.AttentionAt), AssignedTo: ct.AssignedTo, HasNote: ct.Notes != ""})
			}
		}
	}
	textHits := map[string]bool{}
	if q != "" && s.History != nil {
		for _, m := range s.History.Query(history.Query{Search: q, Limit: 2000}) {
			textHits[m.Phone] = true
		}
	}
	filtered := out[:0]
	for _, v := range out {
		switch filter {
		case "unread":
			if v.Unread == 0 {
				continue
			}
		case "attention":
			if !v.NeedsAttention {
				continue
			}
		case "unassigned":
			if v.AssignedTo != "" {
				continue
			}
		}
		if who := r.URL.Query().Get("assigned"); who != "" && !strings.EqualFold(v.AssignedTo, who) {
			continue
		}
		if q != "" && !textHits[v.Phone] && !strings.Contains(strings.ToLower(v.Name+" "+v.LastText+" "+v.AssignedTo), q) && (qd == "" || !strings.Contains(v.Phone, qd)) {
			continue
		}
		filtered = append(filtered, v)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		if filter == "attention" || filtered[i].NeedsAttention == filtered[j].NeedsAttention {
			return filtered[i].LastAt.After(filtered[j].LastAt)
		}
		return filtered[i].NeedsAttention
	})
	if len(filtered) > 300 {
		filtered = filtered[:300]
	}
	writeJSON(w, http.StatusOK, filtered)
}

func derefTime(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

func (s *Server) handleMarkRead(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Phone string `json:"phone"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if s.Contacts != nil {
		phone := contacts.CleanPhone(req.Phone)
		if _, ok := s.Contacts.Get(phone); !ok {
			s.Contacts.Upsert(phone, "", "whatsapp")
		}
		s.Contacts.MarkRead(phone)
	}
	writeOK(w, nil)
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if s.History == nil {
		writeJSON(w, http.StatusOK, []history.MessageRecord{})
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := history.Query{Limit: limit, Phone: r.URL.Query().Get("phone"), Search: r.URL.Query().Get("search")}
	if b := r.URL.Query().Get("before"); b != "" {
		if t, err := time.Parse(time.RFC3339Nano, b); err == nil {
			q.Before = t
		}
	}
	writeJSON(w, http.StatusOK, s.History.Query(q))
}

func (s *Server) handleDeleteMessages(w http.ResponseWriter, r *http.Request) {
	phone := contacts.CleanPhone(r.URL.Query().Get("phone"))
	if phone == "" {
		writeError(w, http.StatusBadRequest, "Choose a conversation to clear. To delete everything use Settings > Privacy.")
		return
	}
	n := 0
	if s.History != nil {
		n = s.History.ClearPhone(phone)
	}
	security.LogAudit("CHAT_CLEARED", clientIP(r), actor(r), fmt.Sprintf("Deleted %d messages with +%s", n, phone), "success")
	writeOK(w, map[string]any{"deleted": n})
}

type sendRequest struct {
	To        string `json:"to"`
	Message   string `json:"message"`
	MediaData string `json:"media_data"`
	FileName  string `json:"file_name"`
}

func (s *Server) readSendRequest(w http.ResponseWriter, r *http.Request) (sendRequest, []byte, bool) {
	var req sendRequest
	var data []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, security.MaxAttachmentBytes+(1<<20))
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			uploadError(w, err, fmt.Sprintf("The file is larger than %d MB.", security.MaxAttachmentBytes>>20))
			return req, nil, false
		}
		defer r.MultipartForm.RemoveAll()
		req.To, req.Message = r.FormValue("to"), r.FormValue("message")
		if f, hdr, err := r.FormFile("file"); err == nil {
			defer f.Close()
			b, err := io.ReadAll(io.LimitReader(f, security.MaxAttachmentBytes+1))
			if err != nil {
				writeError(w, http.StatusBadRequest, "The file could not be read.")
				return req, nil, false
			}
			data, req.FileName = b, hdr.Filename
		}
		return req, data, true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 24<<20)
	if !decodeJSONLarge(w, r, &req) {
		return req, nil, false
	}
	if req.MediaData != "" {
		raw := req.MediaData
		if i := strings.Index(raw, ";base64,"); i >= 0 {
			raw = raw[i+8:]
		}
		b, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "The attachment is not valid base64.")
			return req, nil, false
		}
		data = b
	}
	return req, data, true
}

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	req, data, ok := s.readSendRequest(w, r)
	if !ok {
		return
	}
	phone := contacts.CleanPhone(req.To)
	if len(phone) < 7 || len(phone) > 15 {
		writeError(w, http.StatusBadRequest, "Enter a valid phone number with country code, for example +919876543210.")
		return
	}
	text := strings.TrimSpace(req.Message)
	if text == "" && len(data) == 0 {
		writeError(w, http.StatusBadRequest, "Write a message or attach a file.")
		return
	}
	if len(text) > 4000 {
		writeError(w, http.StatusBadRequest, "The message is too long (4000 characters at most).")
		return
	}
	var att security.AttachmentValidationResult
	if len(data) > 0 {
		att = security.ValidateAttachment(req.FileName, data, security.MaxAttachmentBytes)
		if !att.Valid {
			writeError(w, http.StatusBadRequest, att.Error)
			return
		}
	}
	snap := s.cfg.Snapshot()
	var ct contacts.Contact
	if s.Contacts != nil {
		if c, ok := s.Contacts.Get(phone); ok {
			ct = *c
			phone = c.Phone
		}
	}
	if ct.IsDND {
		writeError(w, http.StatusConflict, "This customer opted out of messages. Ask them to reply START before you message them again.")
		return
	}
	if inPhoneList(snap.Automation.BlacklistPhones, phone) {
		writeError(w, http.StatusConflict, "This number is on your blocked list (Settings > Safety).")
		return
	}
	if snap.Automation.AllowListOnly && !inPhoneList(snap.Automation.AllowList, phone) {
		writeError(w, http.StatusConflict, "Test mode is on: messages can only go to the test numbers in Settings > Safety.")
		return
	}
	// WhatsApp only delivers free text within 24 hours of the customer's last message;
	// after that only an approved template may go (from Campaigns or Scheduled).
	if campaign.NeedsTemplate(&ct, time.Now()) {
		writeError(w, http.StatusConflict, campaign.OutsideWindowMessage)
		return
	}
	rendered, missing := msgtemplate.Render(text, msgtemplate.Vars{Name: ct.Name, Phone: phone, Business: snap.Business, Extra: map[string]string{"email": ct.Email, "notes": ct.Notes}})
	if len(missing) > 0 {
		writeError(w, http.StatusBadRequest, msgtemplate.MissingError(missing).Error())
		return
	}
	releaseDaily, ok := campaign.ReserveDaily(snap, s.History, time.Now())
	if !ok {
		writeError(w, http.StatusTooManyRequests, "Today's sending limit has been reached, so nothing was sent. You can send again tomorrow, or raise the limit in Settings > Safety.")
		return
	}
	defer releaseDaily()
	rec := history.MessageRecord{Direction: history.DirectionOutgoing, Type: history.TypeDirectSend, Phone: phone, Text: rendered, SentBy: actor(r)}
	if len(data) > 0 {
		if att.IsImage {
			rec.Media = "Photo"
		} else {
			rec.Media = "Document: " + att.CleanName
		}
	}
	if snap.Automation.DryRun {
		// Practice mode: show it in the chat as "Test only", but send nothing.
		rec.ID, rec.Status = "dry_"+security.RandomToken(6), history.StatusDryRun
		if s.History != nil {
			s.History.Record(rec)
		}
		writeOK(w, map[string]any{"dry_run": true, "message_id": rec.ID, "to": phone, "text": rendered,
			"message": "Practice mode is on, so this message was not sent. Turn it off in Settings > Safety."})
		return
	}
	if s.Messenger == nil || !s.status().Connected {
		writeError(w, http.StatusServiceUnavailable, "WhatsApp is not connected. Check Settings > WhatsApp connection.")
		return
	}
	var id string
	var err error
	if len(data) > 0 {
		id, err = s.Messenger.SendMediaMessage(r.Context(), phone, data, att.CleanName, att.DetectedMIME, rendered)
	} else {
		id, err = s.Messenger.SendTextMessage(r.Context(), phone, rendered)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	rec.ID, rec.Status = id, history.StatusSent
	if s.History != nil {
		s.History.Record(rec)
	}
	if s.Contacts != nil {
		s.Contacts.Upsert(phone, "", "manual")
		s.Contacts.UpdateLastContacted(phone)
		s.Contacts.MarkRead(phone)
	}
	writeOK(w, map[string]any{"message_id": id, "to": phone, "text": rendered})
}

func inPhoneList(list []string, phone string) bool {
	for _, p := range list {
		if contacts.CleanPhone(p) == phone {
			return true
		}
	}
	return false
}

func decodeJSONLarge(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "The request is too large.")
		return false
	}
	if err := jsonUnmarshal(body, v); err != nil {
		writeError(w, http.StatusBadRequest, "Pulse could not read that request. Reload the page and try again.")
		return false
	}
	return true
}

func (s *Server) handleValidateMedia(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, security.MaxAttachmentBytes+(1<<20))
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		uploadError(w, err, fmt.Sprintf("The file is larger than %d MB.", security.MaxAttachmentBytes>>20))
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "Choose a file.")
		return
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, security.MaxAttachmentBytes+1))
	res := security.ValidateAttachment(hdr.Filename, data, security.MaxAttachmentBytes)
	code := http.StatusOK
	if !res.Valid {
		code = http.StatusBadRequest
	}
	writeJSON(w, code, res)
}
