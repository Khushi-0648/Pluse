package server

import (
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"whatsapp-automation/pkg/campaign"
	"whatsapp-automation/pkg/history"
	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/security"
)

type campaignRequest struct {
	Name       string            `json:"name"`
	Message    string            `json:"message"`
	TemplateID string            `json:"template_id"`
	Audience   campaign.Audience `json:"audience"`
	// Official is the Meta-approved template for customers outside the 24-hour window.
	Official *campaign.Template `json:"official_template"`
	// Phone receives "Send a test" (the business number cannot message itself).
	Phone string `json:"phone"`
}

func (s *Server) campaignMessage(req campaignRequest) string {
	msg := strings.TrimSpace(req.Message)
	if msg == "" && req.TemplateID != "" && s.Templates != nil {
		if t, ok := s.Templates.Get(req.TemplateID); ok {
			msg = t.Content
		}
	}
	return msg
}

func (s *Server) needCampaigns(w http.ResponseWriter) bool {
	if s.Campaigns == nil {
		writeError(w, http.StatusServiceUnavailable, "Campaigns are not available.")
		return false
	}
	return true
}

type exclusionGroup struct {
	Reason string               `json:"reason"`
	Count  int                  `json:"count"`
	People []campaign.Exclusion `json:"people"`
}

func groupExclusions(ex []campaign.Exclusion) []exclusionGroup {
	idx := map[string]int{}
	out := []exclusionGroup{}
	for _, x := range ex {
		i, ok := idx[x.Reason]
		if !ok {
			out = append(out, exclusionGroup{Reason: x.Reason})
			i = len(out) - 1
			idx[x.Reason] = i
		}
		out[i].Count++
		if len(out[i].People) < 20 {
			out[i].People = append(out[i].People, x)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func (s *Server) handlePreviewCampaign(w http.ResponseWriter, r *http.Request) {
	var req campaignRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	tmpl, err := req.Official.Clean()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := campaign.BuildPlan(s.cfg.Snapshot(), s.Contacts, s.History, s.campaignMessage(req), req.Audience)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"audience": plan.Description, "recipients": len(plan.Recipients), "excluded": len(plan.Excluded),
		"exclusions": groupExclusions(plan.Excluded), "samples": plan.Samples, "daily_left": plan.DailyLeft,
		"fits_today": len(plan.Recipients) <= plan.DailyLeft, "outside_window": plan.OutsideWindow,
		"needs_template": plan.OutsideWindow > 0 && tmpl == nil,
	})
}

func (s *Server) handleTestCampaign(w http.ResponseWriter, r *http.Request) {
	var req campaignRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	msg := s.campaignMessage(req)
	if err := msgtemplate.Validate(msg, "email", "notes"); err != nil || msg == "" {
		if err == nil {
			err = errors.New("Write the message first.")
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	phone, ok := s.testRecipient(w, req.Phone)
	if !ok {
		return
	}
	snap := s.cfg.Snapshot()
	text, missing := msgtemplate.Render(msg, msgtemplate.Vars{Name: "Asha Verma", Phone: phone, Business: snap.Business, Extra: map[string]string{"email": "asha@example.com", "notes": "sample note"}})
	if len(missing) > 0 {
		writeError(w, http.StatusBadRequest, msgtemplate.MissingError(missing).Error())
		return
	}
	text = "[Test] " + text
	id, err := s.Messenger.SendTextMessage(r.Context(), phone, text)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error()+testHint)
		return
	}
	if s.History != nil {
		s.History.Record(history.MessageRecord{ID: id, Direction: history.DirectionOutgoing, Type: history.TypeDirectSend, Phone: phone, Text: text, Status: history.StatusSent, SentBy: actor(r)})
	}
	writeOK(w, map[string]any{"message": "Test sent to +" + phone + "."})
}

func (s *Server) handleStartCampaign(w http.ResponseWriter, r *http.Request) {
	if !s.needCampaigns(w) {
		return
	}
	var req campaignRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if !s.status().Connected {
		writeError(w, http.StatusConflict, "WhatsApp is not connected. Check Settings > WhatsApp connection.")
		return
	}
	if s.health().Level == "risk" {
		writeError(w, http.StatusConflict, "Account health is at risk (see Home). Campaigns are blocked until it improves.")
		return
	}
	msg := s.campaignMessage(req)
	tmpl, err := req.Official.Clean()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan, err := campaign.BuildPlan(s.cfg.Snapshot(), s.Contacts, s.History, msg, req.Audience)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	plan.Template = tmpl
	sum, err := s.Campaigns.Start(req.Name, msg, actor(r), plan)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeOK(w, map[string]any{"campaign": sum})
}

func (s *Server) handleListCampaigns(w http.ResponseWriter, r *http.Request) {
	if !s.needCampaigns(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.Campaigns.List())
}

func (s *Server) handleGetCampaign(w http.ResponseWriter, r *http.Request) {
	if !s.needCampaigns(w) {
		return
	}
	c, rep, ok := s.Campaigns.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Campaign not found.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"campaign": c, "report": rep, "exclusions": groupExclusions(c.Excluded)})
}

func (s *Server) handleCampaignAction(w http.ResponseWriter, r *http.Request) {
	if !s.needCampaigns(w) {
		return
	}
	id := r.PathValue("id")
	switch r.PathValue("action") {
	case "pause":
		if act, ok := s.Campaigns.Active(); !ok || act.ID != id {
			writeError(w, http.StatusConflict, "This campaign is not sending.")
			return
		}
		if !s.Campaigns.Pause("Paused by " + actor(r)) {
			writeError(w, http.StatusConflict, "No campaign is sending.")
			return
		}
	case "resume":
		if !s.status().Connected {
			writeError(w, http.StatusConflict, "WhatsApp is not connected.")
			return
		}
		if err := s.Campaigns.Resume(id); err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
	case "cancel":
		if !s.Campaigns.Cancel(id) {
			writeError(w, http.StatusConflict, "This campaign is not running or paused.")
			return
		}
	default:
		writeError(w, http.StatusNotFound, "Unknown action.")
		return
	}
	security.LogAudit("BROADCAST_"+strings.ToUpper(r.PathValue("action")), clientIP(r), actor(r), "Campaign "+id, "success")
	writeOK(w, nil)
}

func (s *Server) handleDeleteCampaign(w http.ResponseWriter, r *http.Request) {
	if !s.needCampaigns(w) {
		return
	}
	if !s.Campaigns.Delete(r.PathValue("id")) {
		writeError(w, http.StatusConflict, "Only finished campaigns can be deleted.")
		return
	}
	writeOK(w, nil)
}

func (s *Server) handleCampaignCSV(w http.ResponseWriter, r *http.Request) {
	if !s.needCampaigns(w) {
		return
	}
	c, _, ok := s.Campaigns.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "Campaign not found.")
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="campaign-%s.csv"`, c.ID))
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"phone", "name", "status", "sent_at", "replied_at", "error"})
	ts := func(t *time.Time) string {
		if t == nil {
			return ""
		}
		return t.Format("2006-01-02 15:04")
	}
	for _, x := range c.Recipients {
		_ = cw.Write([]string{"+" + x.Phone, csvCell(x.Name), x.Status, ts(x.SentAt), ts(x.RepliedAt), csvCell(x.Error)})
	}
	for _, x := range c.Excluded {
		_ = cw.Write([]string{"+" + x.Phone, csvCell(x.Name), "not sent", "", "", csvCell(x.Reason)})
	}
	cw.Flush()
}

func csvCell(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

// parseWhen accepts the browser's datetime-local value or RFC 3339.
func parseWhen(v string, loc *time.Location) (time.Time, error) {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04", "02/01/2006 15:04"} {
		if layout == time.RFC3339 {
			if t, err := time.Parse(layout, v); err == nil {
				return t, nil
			}
			continue
		}
		if t, err := time.ParseInLocation(layout, v, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("Choose a date and time.")
}

func (s *Server) handleListScheduled(w http.ResponseWriter, r *http.Request) {
	if s.Scheduler == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	writeJSON(w, http.StatusOK, s.Scheduler.List())
}

func (s *Server) handleAddScheduled(w http.ResponseWriter, r *http.Request) {
	if s.Scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, "Scheduling is not available.")
		return
	}
	var req struct {
		Recipient    string `json:"recipient"`
		Name         string `json:"name"`
		Message      string `json:"message"`
		TemplateID   string `json:"template_id"`
		ScheduledAt  string `json:"scheduled_at"`
		ValidateOnly bool   `json:"validate_only"`
		// Official is sent instead when the customer has not written in the 24 hours before.
		Official *campaign.Template `json:"official_template"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	msg := strings.TrimSpace(req.Message)
	if msg == "" && req.TemplateID != "" && s.Templates != nil {
		if t, ok := s.Templates.Get(req.TemplateID); ok {
			msg = t.Content
		}
	}
	at, err := parseWhen(req.ScheduledAt, time.Local)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tmpl, err := req.Official.Clean()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ValidateOnly {
		_, warn, err := s.Scheduler.Validate(req.Recipient, msg, at, tmpl)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeOK(w, map[string]any{"warnings": warn})
		return
	}
	job, warn, err := s.Scheduler.Add(req.Recipient, msg, at, req.Name, actor(r), tmpl)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	security.LogAudit("SCHEDULED", clientIP(r), actor(r), fmt.Sprintf("Message to +%s scheduled for %s", job.Recipient, job.ScheduledAt.Format("2006-01-02 15:04")), "success")
	writeOK(w, map[string]any{"job": job, "warnings": warn})
}

func (s *Server) handleScheduledAction(w http.ResponseWriter, r *http.Request) {
	if s.Scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, "Scheduling is not available.")
		return
	}
	id := r.PathValue("id")
	var err error
	switch r.PathValue("action") {
	case "cancel":
		err = s.Scheduler.Cancel(id)
	case "retry":
		_, err = s.Scheduler.Retry(id)
	default:
		writeError(w, http.StatusNotFound, "Unknown action.")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeOK(w, nil)
}

func (s *Server) handleDeleteScheduled(w http.ResponseWriter, r *http.Request) {
	if s.Scheduler == nil {
		writeError(w, http.StatusServiceUnavailable, "Scheduling is not available.")
		return
	}
	if err := s.Scheduler.Delete(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeOK(w, nil)
}
