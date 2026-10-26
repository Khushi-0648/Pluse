package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"whatsapp-automation/pkg/contacts"
	"whatsapp-automation/pkg/security"
)

func jsonUnmarshal(b []byte, v any) error {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if len(bytes.TrimSpace(b)) == 0 {
		b = []byte("{}")
	}
	return json.Unmarshal(b, v)
}

func (s *Server) needContacts(w http.ResponseWriter) bool {
	if s.Contacts == nil {
		writeError(w, http.StatusServiceUnavailable, "The contact list is not available.")
		return false
	}
	return true
}

func (s *Server) handleListContacts(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	q := r.URL.Query()
	var list []contacts.Contact
	if search := strings.TrimSpace(q.Get("search")); search != "" {
		list = s.Contacts.Search(search)
	} else {
		list = s.Contacts.List()
	}
	tag := strings.TrimSpace(q.Get("tag"))
	filter := q.Get("filter")
	now := time.Now()
	out := make([]contacts.Contact, 0, len(list))
	for _, c := range list {
		if tag != "" {
			found := false
			for _, t := range c.Tags {
				if strings.EqualFold(t, tag) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		switch filter {
		case "consent":
			if !c.Consent || c.IsDND {
				continue
			}
		case "no_consent":
			if c.Consent || c.IsDND {
				continue
			}
		case "opted_out":
			if !c.IsDND {
				continue
			}
		case "attention":
			if !c.NeedsAttention {
				continue
			}
		case "recent":
			if !c.InServiceWindow(now) {
				continue
			}
		}
		out = append(out, c)
	}
	page, _ := strconv.Atoi(q.Get("page"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 500 {
		limit = 100
	}
	total := len(out)
	start := (page - 1) * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": out[start:end], "total": total, "page": page, "limit": limit, "pages": (total + limit - 1) / limit})
}

func (s *Server) handleGetContact(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	phone, ok := s.requestPhone(w, r.PathValue("phone"))
	if !ok {
		return
	}
	// Exactly that number, and its own messages only: the Singapore contact 6591234567
	// never shows India's 916591234567 or that customer's chat.
	c, ok := s.Contacts.GetExact(phone)
	if !ok || phone == "" {
		writeError(w, http.StatusNotFound, "Contact not found.")
		return
	}
	resp := map[string]any{"contact": c, "in_window": c.InServiceWindow(time.Now())}
	if s.History != nil {
		resp["recent_messages"] = s.History.List(20, c.Phone, "")
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleSaveContact(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	var req struct {
		contacts.Contact
		OriginalPhone string `json:"original_phone"`
		// What the form showed when it was opened. If the customer opted in or out
		// meanwhile (STOP/START), the customer's choice wins over the older form.
		SeenDND     *bool `json:"seen_is_dnd"`
		SeenConsent *bool `json:"seen_consent"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	c := req.Contact
	// Both numbers are resolved once (resolvePhone) and then used exactly: the form sends
	// a saved contact's number as stored digits, and a typed one is read like any other.
	orig, ok := s.requestPhone(w, req.OriginalPhone)
	if !ok {
		return
	}
	if c.Phone, ok = s.requestPhone(w, c.Phone); !ok {
		return
	}
	if req.SeenDND != nil && req.SeenConsent != nil && orig != "" {
		if ex, ok := s.Contacts.GetExact(orig); ok && (ex.IsDND != *req.SeenDND || ex.Consent != *req.SeenConsent) {
			c.IsDND, c.Consent, c.ConsentSource = ex.IsDND, ex.Consent, ex.ConsentSource
		}
	}
	if !c.IsDND && authOf(r).role != "admin" && s.isOptedOut(orig, c.Phone) {
		writeError(w, http.StatusForbidden, optOutAdminOnly)
		return
	}
	if c.Consent && strings.TrimSpace(c.ConsentSource) == "" {
		if ex, ok := s.Contacts.GetExact(c.Phone); !ok || !ex.Consent {
			writeError(w, http.StatusBadRequest, "Say how this person agreed to receive messages (for example: signed up in store on 12 Oct).")
			return
		}
	}
	renamed := false
	if orig != "" && orig != c.Phone {
		if authOf(r).role != "admin" {
			// A new number replaces the old entry, which is a delete: admin only, like deleting.
			writeError(w, http.StatusForbidden, "Only the admin can change a contact's number.")
			return
		}
		if _, exists := s.Contacts.GetExact(c.Phone); exists {
			writeError(w, http.StatusConflict, "Another contact already has that number.")
			return
		}
		if old, ok := s.Contacts.GetExact(orig); ok {
			c.CreatedAt = old.CreatedAt
			c.IsDND = c.IsDND || old.IsDND // a new number must not undo an opt-out
			renamed = true
		}
	}
	// Save first: the old entry is only removed once the new number was accepted.
	if err := s.Contacts.SaveExact(c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if renamed {
		s.Contacts.Delete(orig)
	}
	saved, _ := s.Contacts.GetExact(c.Phone)
	security.LogAudit("CONTACT_SAVED", clientIP(r), actor(r), "Saved contact +"+saved.Phone, "success")
	writeOK(w, map[string]any{"contact": saved})
}

func (s *Server) handleDeleteContact(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	phone, ok := s.requestPhone(w, r.PathValue("phone"))
	if !ok {
		return
	}
	if phone == "" || !s.Contacts.Delete(phone) {
		writeError(w, http.StatusNotFound, "Contact not found.")
		return
	}
	security.LogAudit("CONTACT_DELETED", clientIP(r), actor(r), "Deleted contact +"+phone, "success")
	writeOK(w, nil)
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.Contacts.GetAllTags())
}

func (s *Server) handleBulkContacts(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	var req struct {
		Action   string   `json:"action"`
		Phones   []string `json:"phones"`
		Tag      string   `json:"tag"`
		Password string   `json:"password"` // deleting needs the admin password again
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Phones) == 0 || len(req.Phones) > 5000 {
		writeError(w, http.StatusBadRequest, "Select between 1 and 5000 contacts.")
		return
	}
	tag := strings.TrimSpace(req.Tag)
	if (req.Action == "tag" || req.Action == "untag") && (tag == "" || len(tag) > 40) {
		writeError(w, http.StatusBadRequest, "Enter a tag of up to 40 characters.")
		return
	}
	if req.Action == "delete" {
		if authOf(r).role != "admin" {
			writeError(w, http.StatusForbidden, "Only the admin can delete contacts.")
			return
		}
		// Deleting many contacts at once (and the opt-outs they hold) needs the admin
		// signed in with the password; reauth also refuses the developer API key.
		if !s.reauth(w, r, req.Password) {
			return
		}
	}
	n := 0
	for _, p := range req.Phones {
		phone, err := s.resolvePhone(p)
		if err != nil {
			continue
		}
		c, ok := s.Contacts.GetExact(phone)
		if !ok || phone == "" {
			continue
		}
		switch req.Action {
		case "tag", "untag":
			if s.Contacts.SetTag(c.Phone, tag, req.Action == "tag") {
				n++
			}
		case "opt_out":
			if s.Contacts.SetDND(c.Phone, true, "Marked do-not-message by staff on "+time.Now().Format("2006-01-02")) {
				n++
			}
		case "delete":
			if s.Contacts.Delete(c.Phone) {
				n++
			}
		default:
			writeError(w, http.StatusBadRequest, "Unknown action.")
			return
		}
	}
	security.LogAudit("CONTACTS_BULK", clientIP(r), actor(r), fmt.Sprintf("%s applied to %d contacts", req.Action, n), "success")
	writeOK(w, map[string]any{"updated": n})
}

func (s *Server) handleConsent(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	var req struct {
		Phone   string `json:"phone"`
		Consent bool   `json:"consent"`
		Source  string `json:"source"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	src := strings.TrimSpace(req.Source)
	if req.Consent && src == "" {
		writeError(w, http.StatusBadRequest, "Say how this person agreed to receive messages.")
		return
	}
	if len(src) > 200 {
		src = src[:200]
	}
	if !req.Consent && src == "" {
		src = "Withdrawn by staff on " + time.Now().Format("2006-01-02")
	}
	phone, ok := s.requestPhone(w, req.Phone)
	if !ok {
		return
	}
	if req.Consent && authOf(r).role != "admin" && s.isOptedOut(phone) {
		// Recording consent also lifts the opt-out, so staff cannot do it.
		writeError(w, http.StatusForbidden, optOutAdminOnly)
		return
	}
	if phone == "" || !s.Contacts.SetConsent(phone, req.Consent, src) {
		writeError(w, http.StatusNotFound, "Contact not found.")
		return
	}
	security.LogAudit("CONSENT_CHANGED", clientIP(r), actor(r), fmt.Sprintf("+%s consent=%v (%s)", phone, req.Consent, src), "success")
	writeOK(w, nil)
}

func (s *Server) handleDND(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	var req struct {
		Phone string `json:"phone"`
		DND   bool   `json:"dnd"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	phone, ok := s.requestPhone(w, req.Phone)
	if !ok {
		return
	}
	if !req.DND && authOf(r).role != "admin" && s.isOptedOut(phone) {
		writeError(w, http.StatusForbidden, optOutAdminOnly)
		return
	}
	reason := "Marked do-not-message by staff on " + time.Now().Format("2006-01-02")
	if phone == "" || !s.Contacts.SetDND(phone, req.DND, reason) {
		writeError(w, http.StatusNotFound, "Contact not found.")
		return
	}
	security.LogAudit("DND_CHANGED", clientIP(r), actor(r), fmt.Sprintf("+%s do-not-message=%v", phone, req.DND), "success")
	writeOK(w, nil)
}

func (s *Server) handleResumeBot(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	var req struct {
		Phone string `json:"phone"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	phone, ok := s.requestPhone(w, req.Phone)
	if !ok {
		return
	}
	if phone == "" || !s.Contacts.ResumeBot(phone) {
		writeError(w, http.StatusNotFound, "Contact not found.")
		return
	}
	security.LogAudit("BOT_RESUMED", clientIP(r), actor(r), "Automatic replies resumed for +"+phone, "success")
	writeOK(w, nil)
}

func (s *Server) handlePauseBot(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	var req struct {
		Phone string `json:"phone"`
		Hours int    `json:"hours"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Hours <= 0 || req.Hours > 720 {
		req.Hours = 24
	}
	phone, ok := s.requestPhone(w, req.Phone)
	if !ok {
		return
	}
	if _, ok := s.Contacts.GetExact(phone); !ok || phone == "" {
		writeError(w, http.StatusNotFound, "Contact not found.")
		return
	}
	s.Contacts.PauseBot(phone, time.Duration(req.Hours)*time.Hour, fmt.Sprintf("Staff took over this chat (%s)", actor(r)))
	s.Contacts.ResumeAttentionOnly(phone)
	security.LogAudit("BOT_PAUSED", clientIP(r), actor(r), fmt.Sprintf("Automatic replies paused for +%s for %d hours", phone, req.Hours), "success")
	writeOK(w, nil)
}

func (s *Server) handleExportContacts(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	var buf bytes.Buffer
	if err := s.Contacts.ExportCSV(&buf); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	security.LogAudit("CONTACTS_EXPORTED", clientIP(r), actor(r), fmt.Sprintf("Exported %d contacts", s.Contacts.Count()), "success")
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="contacts-%s.csv"`, time.Now().Format("20060102")))
	_, _ = w.Write(buf.Bytes())
}

func readUpload(w http.ResponseWriter, r *http.Request) ([]byte, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, 21<<20)
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			uploadError(w, err, "The file is larger than 20 MB.")
			return nil, "", false
		}
		defer r.MultipartForm.RemoveAll()
		f, _, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "Choose a CSV file.")
			return nil, "", false
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			writeError(w, http.StatusBadRequest, "The file could not be read.")
			return nil, "", false
		}
		return b, r.FormValue("mapping"), true
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "The file is larger than 20 MB.")
		return nil, "", false
	}
	return b, r.URL.Query().Get("mapping"), true
}

func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request) {
	data, _, ok := readUpload(w, r)
	if !ok {
		return
	}
	p, err := contacts.PreviewCSV(bytes.NewReader(data))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	if !s.needContacts(w) {
		return
	}
	data, mappingJSON, ok := readUpload(w, r)
	if !ok {
		return
	}
	var mapping *contacts.CSVMapping
	if strings.TrimSpace(mappingJSON) != "" {
		var m contacts.CSVMapping
		if err := json.Unmarshal([]byte(mappingJSON), &m); err != nil {
			writeError(w, http.StatusBadRequest, "The column mapping is not valid.")
			return
		}
		mapping = &m
	}
	res, err := s.Contacts.ImportCSV(bytes.NewReader(data), mapping)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	security.LogAudit("CONTACTS_IMPORTED", clientIP(r), actor(r), fmt.Sprintf("CSV import: %d added, %d updated, %d skipped", res.Added, res.Updated, res.Skipped), "success")
	writeOK(w, map[string]any{"result": res, "message": fmt.Sprintf("%d added, %d updated, %d skipped", res.Added, res.Updated, res.Skipped)})
}

const optOutAdminOnly = "This customer asked not to be messaged. Only the admin can undo that, or the customer can reply START."

// isOptedOut reports whether any of the given stored numbers (resolvePhone) belongs to an
// opted-out contact. Staff may mark a customer do-not-message, but only the admin may
// undo it.
func (s *Server) isOptedOut(phones ...string) bool {
	for _, p := range phones {
		if p == "" {
			continue
		}
		if c, ok := s.Contacts.GetExact(p); ok && c.IsDND {
			return true
		}
		if s.Contacts.WasOptedOut(p) {
			return true // opted out, then deleted
		}
	}
	return false
}
