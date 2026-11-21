package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/store"
)

type CreateSessionRequest struct {
	ExternalReferenceID string          `json:"external_reference_id"`
	AmountMinor         int64           `json:"amount_minor"`
	Currency            string          `json:"currency"`
	CustomerPhone       string          `json:"customer_phone,omitempty"`
	ReturnURL           string          `json:"return_url"`
	WebhookURLOverride  string          `json:"webhook_url_override,omitempty"`
	Metadata            json.RawMessage `json:"metadata,omitempty"`
}

type VerifyPaymentRequest struct {
	Channel       string `json:"channel"`        // bkash | nagad | rocket
	TransactionID string `json:"transaction_id"` // TrxID
	CustomerPhone string `json:"customer_phone"`
}

func (s *Server) handleCreatePaymentSession(w http.ResponseWriter, r *http.Request) {
	tenant, _ := TenantFromContext(r.Context())
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))

	// Check Idempotency Guard
	if idempotencyKey != "" {
		existing, err := s.Store.GetPaymentSessionByIdempotency(r.Context(), tenant.ID, idempotencyKey)
		if err == nil && existing != nil {
			// Idempotent replay: return existing session without duplicate creation
			checkoutURL := fmt.Sprintf("%s/checkout/%s", s.Config.BaseURL, existing.ID)
			writeJSON(w, http.StatusOK, map[string]any{
				"id":           existing.ID,
				"status":       existing.Status,
				"amount_minor": existing.AmountMinor,
				"currency":     existing.Currency,
				"checkout_url": checkoutURL,
				"expires_at":   existing.ExpiresAt,
				"created_at":   existing.CreatedAt,
				"idempotent":   true,
			})
			return
		}
	}

	var req CreateSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json_payload", "Malformed JSON request body")
		return
	}

	if req.ExternalReferenceID == "" {
		writeError(w, http.StatusBadRequest, "missing_parameter", "external_reference_id is required")
		return
	}
	if req.AmountMinor <= 0 {
		writeError(w, http.StatusBadRequest, "invalid_amount", "amount_minor must be a positive integer greater than 0")
		return
	}
	if req.ReturnURL == "" {
		writeError(w, http.StatusBadRequest, "missing_parameter", "return_url is required")
		return
	}
	if req.Currency == "" {
		req.Currency = "BDT"
	}
	if len(req.Metadata) == 0 {
		req.Metadata = json.RawMessage("{}")
	}

	sessionID := store.GenerateUUID()
	session := &models.PaymentSession{
		ID:                  sessionID,
		TenantID:            tenant.ID,
		ExternalReferenceID: req.ExternalReferenceID,
		IdempotencyKey:      idempotencyKey,
		AmountMinor:         req.AmountMinor,
		Currency:            req.Currency,
		Status:              models.PaymentPending,
		CustomerPhone:       req.CustomerPhone,
		ReturnURL:           req.ReturnURL,
		WebhookURLOverride:  req.WebhookURLOverride,
		Metadata:            req.Metadata,
		ExpiresAt:           time.Now().UTC().Add(30 * time.Minute),
	}

	if err := s.Store.CreatePaymentSession(r.Context(), session); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "Failed to initialize payment session")
		return
	}

	checkoutURL := fmt.Sprintf("%s/checkout/%s", s.Config.BaseURL, sessionID)
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":           session.ID,
		"status":       session.Status,
		"amount_minor": session.AmountMinor,
		"currency":     session.Currency,
		"checkout_url": checkoutURL,
		"expires_at":   session.ExpiresAt,
		"created_at":   session.CreatedAt,
	})
}

func (s *Server) handleGetPaymentSession(w http.ResponseWriter, r *http.Request) {
	tenant, _ := TenantFromContext(r.Context())
	sessionID := r.PathValue("id")
	if sessionID == "" {
		writeError(w, http.StatusBadRequest, "missing_id", "Session ID parameter is required")
		return
	}

	session, err := s.Store.GetPaymentSession(r.Context(), tenant.ID, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session_not_found", "Payment session does not exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "Failed to retrieve session")
		return
	}

	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handleVerifyPaymentSession(w http.ResponseWriter, r *http.Request) {
	tenant, _ := TenantFromContext(r.Context())
	sessionID := r.PathValue("id")

	session, err := s.Store.GetPaymentSession(r.Context(), tenant.ID, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "session_not_found", "Payment session does not exist")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "Failed to retrieve session")
		return
	}

	if session.Status == models.PaymentVerified {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "verified",
			"verified_at": session.VerifiedAt,
			"session_id":  session.ID,
			"message":     "Session is already verified",
		})
		return
	}

	var req VerifyPaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json_payload", "Malformed JSON request body")
		return
	}

	cleanChannel := strings.ToLower(strings.TrimSpace(req.Channel))
	cleanTrxID := strings.ToUpper(strings.TrimSpace(req.TransactionID))
	cleanPhone := strings.TrimSpace(req.CustomerPhone)

	if cleanChannel == "" || cleanTrxID == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "channel and transaction_id are required")
		return
	}

	// Two-Way Asynchronous Reconciliation Branch:
	// Check if matching SMS has already arrived and is buffered in mfs_unmatched_inbox
	unmatchedSMS, err := s.Store.FindMatchingUnmatchedSMS(r.Context(), tenant.ID, cleanChannel, cleanTrxID, session.AmountMinor)
	if err == nil && unmatchedSMS != nil {
		// INSTANT MATCH FOUND: Transition session directly to verified!
		now := time.Now().UTC()
		if err := s.Store.UpdatePaymentSessionStatus(r.Context(), session.ID, models.PaymentVerified, cleanChannel, cleanTrxID, cleanPhone, &now); err != nil {
			writeError(w, http.StatusInternalServerError, "update_error", "Failed to confirm payment status")
			return
		}

		_ = s.Store.MarkUnmatchedSMSMatched(r.Context(), unmatchedSMS.ID, session.ID)

		// Enqueue Webhook Notification into Outbox
		webhookPayload, _ := json.Marshal(map[string]any{
			"event":                 "payment.verified",
			"session_id":            session.ID,
			"external_reference_id": session.ExternalReferenceID,
			"amount_minor":          session.AmountMinor,
			"currency":              session.Currency,
			"channel":               cleanChannel,
			"transaction_id":        cleanTrxID,
			"customer_phone":        cleanPhone,
			"verified_at":           now,
		})

		_ = s.Store.EnqueueOutboxJob(r.Context(), &models.OutboxJob{
			TenantID: tenant.ID,
			JobType:  "webhook",
			Payload:  webhookPayload,
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"status":         "verified",
			"session_id":     session.ID,
			"transaction_id": cleanTrxID,
			"verified_at":    now,
			"matched_source": "inbox_buffer",
		})
		return
	}

	// NO MATCH YET: Move session to 'processing' awaiting the incoming Android SIM SMS
	_ = s.Store.UpdatePaymentSessionStatus(r.Context(), session.ID, models.PaymentProcessing, cleanChannel, cleanTrxID, cleanPhone, nil)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "processing",
		"session_id":     session.ID,
		"transaction_id": cleanTrxID,
		"message":        "Transaction submitted. Awaiting automated telco verification.",
	})
}
