package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"whatsapp-automation/pkg/cloudapi"
	"whatsapp-automation/pkg/config"
	"whatsapp-automation/pkg/security"
)

// connectionView is the WhatsApp connection as the settings page shows it. The access
// token and app secret are never sent; only whether they are saved.
type connectionView struct {
	PhoneNumberID     string          `json:"phone_number_id"`
	BusinessAccountID string          `json:"business_account_id"`
	APIVersion        string          `json:"api_version"`
	PublicURL         string          `json:"public_url"`
	WebhookURL        string          `json:"webhook_url"`
	VerifyToken       string          `json:"verify_token"`
	HasAccessToken    bool            `json:"has_access_token"`
	HasAppSecret      bool            `json:"has_app_secret"`
	Status            cloudapi.Status `json:"status"`
}

func (s *Server) connectionView() connectionView {
	c := s.cfg.Snapshot().CloudAPI
	return connectionView{
		PhoneNumberID: c.PhoneNumberID, BusinessAccountID: c.BusinessAccountID, APIVersion: c.APIVersion,
		PublicURL: c.PublicURL, WebhookURL: c.WebhookURL(), VerifyToken: c.VerifyToken,
		HasAccessToken: c.AccessToken != "", HasAppSecret: c.AppSecret != "", Status: s.status(),
	}
}

func (s *Server) handleGetConnection(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.connectionView())
}

// connectionRequest is the WhatsApp connection form. An empty access token or app
// secret keeps the saved one.
type connectionRequest struct {
	Password             string `json:"password"`
	PhoneNumberID        string `json:"phone_number_id"`
	BusinessAccountID    string `json:"business_account_id"`
	AccessToken          string `json:"access_token"`
	AppSecret            string `json:"app_secret"`
	APIVersion           string `json:"api_version"`
	PublicURL            string `json:"public_url"`
	RegenerateVerifyCode bool   `json:"regenerate_verify_token"`
}

// handleSaveConnection stores this install's own WhatsApp Business API details. It
// needs the admin password, and the developer key cannot use it (roleOwner).
func (s *Server) handleSaveConnection(w http.ResponseWriter, r *http.Request) {
	var req connectionRequest
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password) {
		return
	}
	in := config.CloudCredentials{PhoneNumberID: req.PhoneNumberID, BusinessAccountID: req.BusinessAccountID, AccessToken: req.AccessToken,
		AppSecret: req.AppSecret, APIVersion: req.APIVersion, PublicURL: req.PublicURL, RegenerateVerifyToken: req.RegenerateVerifyCode}
	if err := s.cfg.SetCloudCredentials(in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tok, sec := strings.TrimSpace(req.AccessToken), strings.TrimSpace(req.AppSecret)
	detail := "WhatsApp Business API details saved"
	if tok != "" {
		detail += "; new access token"
	}
	if sec != "" {
		detail += "; new app secret"
	}
	if req.RegenerateVerifyCode {
		detail += "; new verify token"
	}
	security.LogAudit("CONNECTION_SAVED", clientIP(r), actor(r), detail, "success")
	if s.Conn != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		_, _ = s.Conn.Check(ctx)
		cancel()
	}
	writeOK(w, map[string]any{"connection": s.connectionView()})
}

// handleTestConnection checks the details in the form (or the saved ones) with Meta
// without saving anything.
func (s *Server) handleTestConnection(w http.ResponseWriter, r *http.Request) {
	var req connectionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if s.Conn == nil {
		writeError(w, http.StatusServiceUnavailable, "WhatsApp is not available.")
		return
	}
	snap := s.cfg.Snapshot().CloudAPI
	id, tok, ver := strings.TrimSpace(req.PhoneNumberID), strings.TrimSpace(req.AccessToken), strings.TrimSpace(req.APIVersion)
	if id == "" {
		id = snap.PhoneNumberID
	}
	if ver == "" || !config.ValidGraphVersion(ver) {
		ver = snap.APIVersion
	}
	if tok == "" {
		saved, err := s.cfg.CloudToken()
		if err != nil {
			writeError(w, http.StatusBadRequest, "The saved access token cannot be read on this Windows account. Paste it again.")
			return
		}
		tok = saved
	}
	if !config.ValidPhoneNumberID(id) || tok == "" {
		writeError(w, http.StatusBadRequest, "Enter the phone number ID and the access token first.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	info, err := s.Conn.Probe(ctx, ver, id, tok)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeOK(w, map[string]any{"display_phone": info.DisplayPhone, "verified_name": info.VerifiedName, "quality_rating": info.QualityRating,
		"message": "Connected to " + info.DisplayPhone + " (" + info.VerifiedName + ")."})
}

// handleDisconnect removes this install's Business API details (token, app secret and
// number), after the admin password. Stored messages and contacts are kept unless asked.
func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password   string `json:"password"`
		DeleteData bool   `json:"delete_data"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password) {
		return
	}
	if s.Campaigns != nil {
		s.Campaigns.CancelAll("WhatsApp was disconnected.")
	}
	if err := s.cfg.ClearCloudCredentials(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.Conn != nil {
		s.Conn.Forget()
	}
	detail := "WhatsApp Business API details removed; local data kept"
	if req.DeleteData {
		s.deleteAllCustomerData()
		detail = "WhatsApp Business API details removed and all local messages, contacts and scheduled messages deleted"
	}
	if s.Engine != nil {
		s.Engine.Reset()
	}
	security.LogAudit("DISCONNECT", clientIP(r), actor(r), detail, "success")
	writeOK(w, map[string]any{"message": detail + "."})
}

// maxCloudWebhookBody is the largest webhook delivery accepted from Meta.
const maxCloudWebhookBody = 1 << 20

// handleCloudWebhook receives Meta's webhook. It is the one address that works without
// signing in and through a public tunnel, so everything it accepts must prove it came
// from Meta: the subscription check needs the verify token, and every delivery must be
// signed with the app secret. It answers 404 until the Business API is set up.
func (s *Server) handleCloudWebhook(w http.ResponseWriter, r *http.Request) {
	snap := s.cfg.Snapshot().CloudAPI
	if snap.PhoneNumberID == "" || s.Conn == nil {
		http.NotFound(w, r)
		return
	}
	switch r.Method {
	case http.MethodGet:
		q := r.URL.Query()
		if q.Get("hub.mode") != "subscribe" || !security.ConstantTimeEqual(q.Get("hub.verify_token"), snap.VerifyToken) {
			security.LogAudit("CLOUD_WEBHOOK_REJECTED", clientIP(r), "unknown", "Webhook subscription check with a wrong verify token", "failed")
			writeError(w, http.StatusForbidden, "Wrong verify token.")
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, q.Get("hub.challenge"))
		security.LogAudit("CLOUD_WEBHOOK_VERIFIED", clientIP(r), "meta", "Meta confirmed the webhook address", "success")
		s.markWebhookSeen()
	case http.MethodPost:
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCloudWebhookBody))
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "Body too large.")
			return
		}
		secret, _ := s.cfg.CloudAppSecret()
		if !cloudapi.VerifySignature(secret, body, r.Header.Get("X-Hub-Signature-256")) {
			reason := "Webhook delivery with a missing or wrong signature"
			if secret == "" {
				reason = "Webhook delivery refused because the app secret is not saved in Settings > WhatsApp connection"
			}
			security.LogAudit("CLOUD_WEBHOOK_REJECTED", clientIP(r), "unknown", reason, "failed")
			writeError(w, http.StatusUnauthorized, "Missing or wrong signature.")
			return
		}
		// Handling only records and queues (replies run in the background), so Meta gets
		// its answer quickly and stops repeating the delivery.
		if _, err := s.Conn.HandleWebhook(body); err != nil {
			writeError(w, http.StatusBadRequest, "The body is not valid JSON.")
			return
		}
		w.WriteHeader(http.StatusOK)
		s.markWebhookSeen()
	default:
		w.Header().Set("Allow", "GET, POST")
		writeError(w, http.StatusMethodNotAllowed, "Method not allowed.")
	}
}

// markWebhookSeen records, once, that Meta reached the webhook (a setup step).
func (s *Server) markWebhookSeen() {
	if s.cfg.Snapshot().Setup.WebhookSeenAt != nil {
		return
	}
	now := time.Now()
	_ = s.cfg.Update(func(c *configT) error { c.Setup.WebhookSeenAt = &now; return nil })
}

// oldSessionFiles lists the files of the linked-device versions (before 2.0) that still
// exist. They are no longer used and are only deleted when the owner asks.
func (s *Server) oldSessionFiles() []string {
	var out []string
	for _, p := range s.OldSessionFiles {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) handleDeleteOldSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &req) || !s.reauth(w, r, req.Password) {
		return
	}
	files := s.oldSessionFiles()
	if len(files) == 0 {
		writeOK(w, map[string]any{"message": "There are no old linked-device files."})
		return
	}
	var failed []string
	for _, p := range files {
		if err := os.Remove(p); err != nil {
			failed = append(failed, p)
		}
	}
	if len(failed) > 0 {
		writeError(w, http.StatusInternalServerError, "Some files could not be deleted (close any program using them): "+strings.Join(failed, ", "))
		return
	}
	security.LogAudit("OLD_SESSION_DELETED", clientIP(r), actor(r), fmt.Sprintf("Deleted %d files of the old linked-device connection", len(files)), "success")
	writeOK(w, map[string]any{"message": fmt.Sprintf("Deleted %d old linked-device files.", len(files))})
}
