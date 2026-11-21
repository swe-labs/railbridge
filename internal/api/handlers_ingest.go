package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/swe-labs/railbridge/internal/mfs"
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

	// Atomic reconciliation branch:
	// Automatically claims matching pending session or buffers into inbox inside a single SQL transaction.
	matchedSession, matched, err := s.Store.AtomicReconcileIncomingSMS(
		r.Context(), device.TenantID, device.ID, parsed.Channel,
		strings.TrimSpace(req.Sender), parsed.TransactionID, parsed.AmountMinor,
		parsed.SenderPhone, parsed.RawBody,
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ingest_error", "Failed to process operator SMS: "+err.Error())
		return
	}

	if matched && matchedSession != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":         "verified",
			"action":         "session_matched",
			"session_id":     matchedSession.ID,
			"transaction_id": parsed.TransactionID,
			"amount_minor":   parsed.AmountMinor,
		})
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
