package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/swe-labs/railbridge/internal/models"
)

type SendEmailRequest struct {
	To       string `json:"to"`
	Subject  string `json:"subject"`
	HTMLBody string `json:"html_body"`
}

type CreateAliasRequest struct {
	SourceAddress string `json:"source_address"` // e.g. business@swe-labs.com
	TargetAddress string `json:"target_address"` // e.g. founder@gmail.com
}

func (s *Server) handleSendEmail(w http.ResponseWriter, r *http.Request) {
	tenant, _ := TenantFromContext(r.Context())
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))

	var req SendEmailRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json_payload", "Malformed JSON body")
		return
	}

	if req.To == "" || req.Subject == "" || req.HTMLBody == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "'to', 'subject', and 'html_body' are required")
		return
	}

	log, err := s.EmailService.DispatchEmail(r.Context(), tenant.ID, idempotencyKey, req.To, req.Subject, req.HTMLBody)
	if err != nil {
		writeError(w, http.StatusBadGateway, "email_dispatch_failed", err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":        log.ID,
		"status":        log.Status,
		"recipient":     log.Recipient,
		"provider_used": log.ProviderUsed,
		"sent_at":       log.SentAt,
	})
}

func (s *Server) handleCreateEmailAlias(w http.ResponseWriter, r *http.Request) {
	tenant, _ := TenantFromContext(r.Context())

	var req CreateAliasRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json_payload", "Malformed JSON body")
		return
	}

	if req.SourceAddress == "" || req.TargetAddress == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "source_address and target_address are required")
		return
	}

	alias := &models.EmailAlias{
		TenantID:      tenant.ID,
		SourceAddress: strings.ToLower(strings.TrimSpace(req.SourceAddress)),
		TargetAddress: strings.ToLower(strings.TrimSpace(req.TargetAddress)),
		IsActive:      true,
	}

	if err := s.Store.CreateEmailAlias(r.Context(), alias); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "Failed to save email alias")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"status":         "created",
		"source_address": alias.SourceAddress,
		"target_address": alias.TargetAddress,
	})
}

func (s *Server) handleInboundEmail(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read_error", "Failed to read inbound payload")
		return
	}

	alias, err := s.EmailService.ProcessInboundEmail(r.Context(), body)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "routing_error", err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "routed",
		"source_address": alias.SourceAddress,
		"target_address": alias.TargetAddress,
	})
}
