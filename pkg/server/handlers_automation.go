package server

import (
	"errors"
	"net/http"
	"strings"

	"whatsapp-automation/pkg/bot"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/msgtemplate"
	"whatsapp-automation/pkg/security"
	"whatsapp-automation/pkg/templates"
)

func (s *Server) handleVariables(w http.ResponseWriter, r *http.Request) {
	b := s.cfg.Snapshot().Business
	filled := map[string]bool{
		"business_name": b.Name != "", "business_hours": b.Hours != "", "business_address": b.Address != "",
		"business_website": b.Website != "", "business_email": b.Email != "", "business_services": b.Services != "", "business_prices": b.Prices != "",
	}
	type v struct {
		msgtemplate.Variable
		Empty bool `json:"empty"`
	}
	out := []v{}
	for _, k := range msgtemplate.Known {
		empty := false
		if f, ok := filled[k.Key]; ok {
			empty = !f
		}
		out = append(out, v{k, empty})
	}
	writeJSON(w, http.StatusOK, map[string]any{"variables": out, "contact_variables": templates.ContactVariables})
}

func (s *Server) handleListRules(w http.ResponseWriter, r *http.Request) {
	rules := s.cfg.GetRules()
	snap := s.cfg.Snapshot()
	type ruleView struct {
		config.Rule
		Missing []string `json:"missing,omitempty"`
	}
	out := make([]ruleView, 0, len(rules))
	for _, rule := range rules {
		_, missing := msgtemplate.Render(rule.Reply, msgtemplate.Vars{Name: "Asha", Phone: "919800000000", Business: snap.Business, Message: "x"})
		out = append(out, ruleView{rule, missing})
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": out, "conflicts": bot.RuleConflicts(rules)})
}

func (s *Server) handleSaveRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		config.Rule
		Enabled      *bool  `json:"enabled"`
		OriginalName string `json:"original_name"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	rule := req.Rule
	rule.Enabled = true
	if req.Enabled != nil {
		rule.Enabled = *req.Enabled
	}
	if err := msgtemplate.Validate(rule.Reply); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.cfg.AddOrUpdateRule(rule, req.OriginalName); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	security.LogAudit("RULE_SAVED", clientIP(r), actor(r), "Saved rule "+rule.Name, "success")
	writeOK(w, map[string]any{"conflicts": bot.RuleConflicts(s.cfg.GetRules())})
}

func (s *Server) handleToggleRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.cfg.SetRuleEnabled(req.Name, req.Enabled); err != nil {
		if errors.Is(err, config.ErrRuleNotFound) {
			writeError(w, http.StatusNotFound, "Rule not found.")
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	state := "off"
	if req.Enabled {
		state = "on"
	}
	security.LogAudit("RULE_TOGGLED", clientIP(r), actor(r), "Rule "+req.Name+" switched "+state, "success")
	writeOK(w, nil)
}

func (s *Server) handleReorderRules(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Names []string `json:"names"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if len(req.Names) == 0 {
		writeError(w, http.StatusBadRequest, "No rules given.")
		return
	}
	if err := s.cfg.ReorderRules(req.Names); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, nil)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.cfg.DeleteRule(name) {
		writeError(w, http.StatusNotFound, "Rule not found.")
		return
	}
	security.LogAudit("RULE_DELETED", clientIP(r), actor(r), "Deleted rule "+name, "success")
	writeOK(w, nil)
}

func (s *Server) handleResetRules(w http.ResponseWriter, r *http.Request) {
	if err := s.cfg.Update(func(c *configT) error { c.Rules = config.DefaultRules(); return nil }); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	security.LogAudit("RULES_RESET", clientIP(r), actor(r), "Rules reset to the defaults", "success")
	writeOK(w, nil)
}

func (s *Server) handleTestRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Message    string `json:"message"`
		Name       string `json:"name"`
		MenuActive bool   `json:"menu_active"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeError(w, http.StatusBadRequest, "Type a customer message to test.")
		return
	}
	eng := s.Engine
	if eng == nil {
		eng = bot.NewEngine(nil, s.cfg, nil, nil, nil, nil)
	}
	writeJSON(w, http.StatusOK, eng.Simulate(req.Message, req.Name, req.MenuActive))
}

func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	if s.Templates == nil {
		writeJSON(w, http.StatusOK, []templates.Template{})
		return
	}
	writeJSON(w, http.StatusOK, s.Templates.List())
}

func (s *Server) handleSaveTemplate(w http.ResponseWriter, r *http.Request) {
	if s.Templates == nil {
		writeError(w, http.StatusServiceUnavailable, "Templates are not available.")
		return
	}
	var t templates.Template
	if !decodeJSON(w, r, &t) {
		return
	}
	saved, err := s.Templates.Save(t)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	security.LogAudit("TEMPLATE_SAVED", clientIP(r), actor(r), "Saved template "+saved.Name, "success")
	writeOK(w, map[string]any{"template": saved})
}

func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	if s.Templates == nil || !s.Templates.Delete(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "Template not found.")
		return
	}
	security.LogAudit("TEMPLATE_DELETED", clientIP(r), actor(r), "Deleted template "+r.PathValue("id"), "success")
	writeOK(w, nil)
}

// handlePreviewText renders text with sample customer data so editors can see the result.
func (s *Server) handlePreviewText(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text  string            `json:"text"`
		Name  string            `json:"name"`
		Extra map[string]string `json:"extra"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	snap := s.cfg.Snapshot()
	name := req.Name
	if name == "" {
		name = "Asha Verma"
	}
	extra := map[string]string{"email": "asha@example.com", "notes": "Prefers mornings"}
	for k, v := range req.Extra {
		extra[k] = v
	}
	out, missing := msgtemplate.Render(req.Text, msgtemplate.Vars{Name: name, Phone: "919876543210", Message: "hello", Business: snap.Business, Extra: extra, Location: bot.Location(snap.BusinessHours.Timezone)})
	resp := map[string]any{"text": out, "missing": missing, "unknown": msgtemplate.Unknown(req.Text, templates.ContactVariables...), "length": len([]rune(out))}
	writeJSON(w, http.StatusOK, resp)
}
