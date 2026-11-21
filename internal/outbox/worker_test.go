package outbox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/swe-labs/railbridge/internal/crypto"
	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/store"
)

func TestOutboxWorker_WebhookDispatch(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_outbox.db")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := store.Open(ctx, "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	var receivedHeaders http.Header
	var receivedBody []byte
	var wg sync.WaitGroup
	wg.Add(1)

	// Mock Client Webhook Server
	webhookServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		wg.Done()
	}))
	defer webhookServer.Close()

	masterKey := make([]byte, 32)
	for i := range masterKey {
		masterKey[i] = byte(i)
	}

	webhookSecretRaw := []byte("rb_whsec_test_client_secret_998")
	encryptedSecret, err := crypto.EncryptAESGCM(masterKey, webhookSecretRaw)
	if err != nil {
		t.Fatalf("encrypt secret: %v", err)
	}

	// Seed Tenant
	tenant := &models.Tenant{
		Name:                   "Test Merchant",
		Environment:            models.EnvLive,
		APIKeyPrefix:           "rb_live_",
		APIKeyHash:             crypto.HashSHA256([]byte("rb_live_key")),
		WebhookURL:             webhookServer.URL,
		WebhookSecretEncrypted: encryptedSecret,
		RateLimitRPM:           120,
		IsActive:               true,
	}
	if err := st.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// Enqueue Webhook Job
	payloadBytes, _ := json.Marshal(map[string]any{
		"event":          "payment.verified",
		"session_id":     "sess_12345",
		"amount_minor":   25000,
		"transaction_id": "9K72B8X9M1",
	})

	job := &models.OutboxJob{
		TenantID: tenant.ID,
		JobType:  "webhook",
		Payload:  payloadBytes,
	}
	if err := st.EnqueueOutboxJob(ctx, job); err != nil {
		t.Fatalf("enqueue outbox job: %v", err)
	}

	// Run Worker Process
	worker := NewWorker(st, masterKey)
	worker.processBatch(ctx)

	// Wait for mock server to receive HTTP POST
	wg.Wait()

	// Verify headers and signature
	sigHeader := receivedHeaders.Get("X-Railbridge-Signature")
	if sigHeader == "" {
		t.Fatal("expected X-Railbridge-Signature header on webhook request")
	}

	if err := crypto.VerifyTimestampedSignature(sigHeader, webhookSecretRaw, receivedBody, 300*time.Second); err != nil {
		t.Fatalf("webhook HMAC signature verification failed: %v", err)
	}

	// Verify job transitioned to 'completed' in DB
	pendingJobs, err := st.FetchPendingOutboxJobs(ctx, 10)
	if err != nil {
		t.Fatalf("fetch pending jobs: %v", err)
	}
	if len(pendingJobs) != 0 {
		t.Errorf("expected 0 pending jobs after successful dispatch, found %d", len(pendingJobs))
	}
}
