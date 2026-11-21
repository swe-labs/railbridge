package api

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/store"
)

// handleListDeadLetterJobs retrieves dead-letter outbox jobs for the authenticated tenant.
func (s *Server) handleListDeadLetterJobs(w http.ResponseWriter, r *http.Request) {
	tenant, _ := TenantFromContext(r.Context())

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	jobs, err := s.Store.FetchDeadLetterJobs(r.Context(), tenant.ID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db_error", "Failed to query dead letter queue: "+err.Error())
		return
	}

	if jobs == nil {
		jobs = make([]*models.OutboxJob, 0)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id": tenant.ID,
		"count":     len(jobs),
		"jobs":      jobs,
	})
}

// handleRetryDeadLetterJob moves a dead-letter outbox job back to pending status for retry.
func (s *Server) handleRetryDeadLetterJob(w http.ResponseWriter, r *http.Request) {
	tenant, _ := TenantFromContext(r.Context())
	jobID := r.PathValue("id")
	if jobID == "" {
		writeError(w, http.StatusBadRequest, "missing_parameter", "Job ID is required")
		return
	}

	if err := s.Store.RetryDeadLetterJob(r.Context(), tenant.ID, jobID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "job_not_found", "Dead-letter job not found or does not belong to this tenant")
			return
		}
		writeError(w, http.StatusInternalServerError, "db_error", "Failed to requeue job: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "requeued",
		"job_id":  jobID,
		"message": "Outbox job returned to pending state for immediate retry",
	})
}

// handleAdminRestart triggers a graceful termination so the upstream process supervisor re-launches with the latest binary.
func (s *Server) handleAdminRestart(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "restarting",
		"message": "Process supervisor restarting instance with latest binary",
	})
	go func() {
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	}()
}
