package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/swe-labs/railbridge/internal/mfs"
	"github.com/swe-labs/railbridge/internal/models"
)

type IngestSMSRequest struct {
	Sender string `json:"sender"` // 16247, 16167, 16216
	Body   string `json:"body"`   // Raw operator text
}

func (s *Server) handleAndroidPing(w http.ResponseWriter, r *http.Request) {
	device, _ := DeviceFromContext(r.Context())

	if err := s.Store.UpdateDeviceHeartbeat(r.Context(), device.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "Failed to update heartbeat")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "online",
		"device_id":   device.ID,
		"device_name": device.DeviceName,
		"synced_at":   time.Now().UTC(),
	})
}

func (s *Server) handleIngestAndroidSMS(w http.ResponseWriter, r *http.Request) {
	device, _ := DeviceFromContext(r.Context())

	var req IngestSMSRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json_payload", "Malformed JSON body")
		return
	}

	if req.Sender == "" || req.Body == "" {
		writeError(w, http.StatusBadRequest, "missing_fields", "sender and body are required")
		return
	}

	// Parse incoming MFS telecom notification SMS
	parsed, err := mfs.ParseSMS(req.Sender, req.Body)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "unparseable_sms", err.Error())
		return
	}

	// 1. Check if a customer payment session is already waiting in 'processing' or 'pending'
	session, err := s.Store.FindPendingSessionForSMS(r.Context(), device.TenantID, parsed.Channel, parsed.TransactionID, parsed.AmountMinor)
	if err == nil && session != nil {
		// Strict Exact Amount Invariant Check
		if session.AmountMinor != parsed.AmountMinor {
			writeJSON(w, http.StatusConflict, map[string]any{
				"status":          "amount_mismatch",
				"expected_minor":  session.AmountMinor,
				"received_minor":  parsed.AmountMinor,
				"detail":          "Received amount does not match session expectation",
			})
			return
		}

		// MATCH FOUND: Confirm payment session
		now := time.Now().UTC()
		_ = s.Store.UpdatePaymentSessionStatus(r.Context(), session.ID, models.PaymentVerified, parsed.Channel, parsed.TransactionID, parsed.SenderPhone, &now)

		// Queue Outbox Webhook Dispatch
		webhookPayload, _ := json.Marshal(map[string]any{
			"event":                 "payment.verified",
			"session_id":            session.ID,
			"external_reference_id": session.ExternalReferenceID,
			"amount_minor":          session.AmountMinor,
			"currency":              session.Currency,
			"channel":               parsed.Channel,
			"transaction_id":        parsed.TransactionID,
			"customer_phone":        parsed.SenderPhone,
			"verified_at":           now,
		})

		_ = s.Store.EnqueueOutboxJob(r.Context(), &models.OutboxJob{
			TenantID: device.TenantID,
			JobType:  "webhook",
			Payload:  webhookPayload,
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"status":         "verified",
			"action":         "session_matched",
			"session_id":     session.ID,
			"transaction_id": parsed.TransactionID,
			"amount_minor":   parsed.AmountMinor,
		})
		return
	}

	// 2. NO PENDING SESSION YET: Buffer incoming SMS in mfs_unmatched_inbox with 30-minute TTL
	unmatched := &models.MFSUnmatchedSMS{
		TenantID:        device.TenantID,
		DeviceID:        device.ID,
		Channel:         parsed.Channel,
		SenderShortcode: strings.TrimSpace(req.Sender),
		TransactionID:   parsed.TransactionID,
		AmountMinor:     parsed.AmountMinor,
		SenderPhone:     parsed.SenderPhone,
		RawSMSBody:      parsed.RawBody,
		Status:          "unmatched",
		ExpiresAt:       time.Now().UTC().Add(30 * time.Minute),
	}

	if err := s.Store.SaveUnmatchedSMS(r.Context(), unmatched); err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "Failed to buffer incoming SMS")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "buffered",
		"action":         "unmatched_inbox_stored",
		"transaction_id": parsed.TransactionID,
		"amount_minor":   parsed.AmountMinor,
		"expires_in":     "30m",
	})
}
