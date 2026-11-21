package outbox

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/swe-labs/railbridge/internal/crypto"
	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/store"
)

// BackoffDelays specifies the retry backoff duration per attempt (1 to 6).
var BackoffDelays = []time.Duration{
	10 * time.Second,  // Attempt 1 -> wait 10s
	1 * time.Minute,   // Attempt 2 -> wait 1m
	5 * time.Minute,   // Attempt 3 -> wait 5m
	30 * time.Minute,  // Attempt 4 -> wait 30m
	2 * time.Hour,     // Attempt 5 -> wait 2h
	12 * time.Hour,    // Attempt 6 -> wait 12h
}

// Worker handles background outbox queue processing with FOR UPDATE SKIP LOCKED.
type Worker struct {
	Store               *store.DB
	MasterEncryptionKey []byte
	HTTPClient          *http.Client
	PollInterval        time.Duration
	BatchSize           int
}

// NewWorker constructs a new outbox background worker.
func NewWorker(st *store.DB, masterKey []byte) *Worker {
	return &Worker{
		Store:               st,
		MasterEncryptionKey: masterKey,
		HTTPClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		PollInterval: 2 * time.Second,
		BatchSize:    10,
	}
}

// Start initiates the worker loop until the context is canceled.
func (w *Worker) Start(ctx context.Context) {
	slog.Info("Outbox worker loop active", "poll_interval", w.PollInterval, "batch_size", w.BatchSize)
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("Outbox worker received shutdown signal. Stopping...")
			return
		case <-ticker.C:
			w.processBatch(ctx)
		}
	}
}

func (w *Worker) processBatch(ctx context.Context) {
	jobs, err := w.Store.FetchPendingOutboxJobs(ctx, w.BatchSize)
	if err != nil {
		slog.Error("Failed to fetch pending outbox jobs", "error", err)
		return
	}

	for _, job := range jobs {
		w.dispatchJob(ctx, job)
	}
}

func (w *Worker) dispatchJob(ctx context.Context, job *models.OutboxJob) {
	var err error
	switch job.JobType {
	case "webhook":
		err = w.dispatchWebhook(ctx, job)
	default:
		slog.Warn("Unhandled outbox job type", "job_id", job.ID, "type", job.JobType)
		// Mark completed to avoid loop
		_ = w.Store.UpdateOutboxJob(ctx, job.ID, "completed", job.Attempts, time.Now(), nil)
		return
	}

	if err == nil {
		slog.Info("Outbox job completed successfully", "job_id", job.ID, "type", job.JobType)
		_ = w.Store.UpdateOutboxJob(ctx, job.ID, "completed", job.Attempts+1, time.Now(), nil)
		return
	}

	// Failure handling with exponential backoff & DLQ
	job.Attempts++
	errStr := err.Error()
	slog.Warn("Outbox job execution failed", "job_id", job.ID, "attempt", job.Attempts, "error", err)

	if job.Attempts >= job.MaxAttempts {
		slog.Error("Outbox job reached max attempts, moving to dead_letter", "job_id", job.ID)
		_ = w.Store.UpdateOutboxJob(ctx, job.ID, "dead_letter", job.Attempts, time.Now(), &errStr)
		return
	}

	delayIdx := job.Attempts - 1
	if delayIdx >= len(BackoffDelays) {
		delayIdx = len(BackoffDelays) - 1
	}
	nextRun := time.Now().Add(BackoffDelays[delayIdx])

	_ = w.Store.UpdateOutboxJob(ctx, job.ID, "pending", job.Attempts, nextRun, &errStr)
}

func (w *Worker) dispatchWebhook(ctx context.Context, job *models.OutboxJob) error {
	webhookURL, encryptedSecret, err := w.Store.GetTenantWebhookSecret(ctx, job.TenantID)
	if err != nil {
		return fmt.Errorf("retrieve webhook secret: %w", err)
	}

	if webhookURL == "" {
		return fmt.Errorf("tenant %s has no webhook_url configured", job.TenantID)
	}

	var secretBytes []byte
	if len(encryptedSecret) > 0 {
		secretBytes, err = crypto.DecryptAESGCM(w.MasterEncryptionKey, encryptedSecret)
		if err != nil {
			return fmt.Errorf("decrypt tenant webhook secret: %w", err)
		}
	} else {
		secretBytes = []byte("default_whsec")
	}

	now := time.Now().Unix()
	sigHeader := crypto.BuildTimestampedSignature(secretBytes, now, job.Payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(job.Payload))
	if err != nil {
		return fmt.Errorf("create webhook request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Railbridge-Webhook/v1.0.0")
	req.Header.Set("X-Railbridge-Event", "payment.verified")
	req.Header.Set("X-Railbridge-Delivery", "dlv_"+job.ID)
	req.Header.Set("X-Railbridge-Signature", sigHeader)

	resp, err := w.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("execute webhook HTTP POST: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook endpoint returned HTTP status %d", resp.StatusCode)
	}

	return nil
}
