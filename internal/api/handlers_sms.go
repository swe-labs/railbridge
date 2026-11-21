package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

type SendSMSRequest struct {
	To      string `json:"to"`
	Message string `json:"message"`
}

func (s *Server) handleSendSMS(w http.ResponseWriter, r *http.Request) {
	tenant, _ := TenantFromContext(r.Context())
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))

	var req SendSMSRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json_payload", "Malformed JSON body")
		return
	}

	if req.To == "" || req.Message == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "'to' and 'message' are required")
		return
	}

	log, err := s.SMSService.DispatchSMS(r.Context(), tenant.ID, idempotencyKey, req.To, req.Message)
	if err != nil {
		writeError(w, http.StatusBadGateway, "sms_dispatch_failed", err.Error())
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
