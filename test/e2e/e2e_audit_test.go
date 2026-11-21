package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swe-labs/railbridge/internal/api"
	"github.com/swe-labs/railbridge/internal/config"
	"github.com/swe-labs/railbridge/internal/crypto"
	"github.com/swe-labs/railbridge/internal/email"
	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/sms"
	"github.com/swe-labs/railbridge/internal/store"
)

func setupTestEnvironment(t *testing.T) (*config.Config, *store.DB, *api.Server, *models.Tenant, string, *models.Device, string) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "e2e_audit.db")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config load: %v", err)
	}

	st, err := store.Open(ctx, "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}

	rawAPIKey := "rb_live_tenant_primary_test_key_123456"
	keyHash := crypto.HashSHA256([]byte(rawAPIKey))
	tenant := &models.Tenant{
		Name:         "Audit Test Tenant Primary",
		Environment:  models.EnvLive,
		APIKeyPrefix: "rb_live_",
		APIKeyHash:   keyHash,
		WebhookURL:   "https://primary-merchant.example/webhook",
		RateLimitRPM: 120,
		IsActive:     true,
	}
	if err := st.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("create primary tenant: %v", err)
	}

	rawDeviceToken := "rb_dev_android_bridge_primary_token_12345"
	devHash := crypto.HashSHA256([]byte(rawDeviceToken))
	device := &models.Device{
		TenantID:              tenant.ID,
		DeviceName:            "SimBridge Node 1",
		DeviceTokenHash:       devHash,
		DeviceSecretEncrypted: []byte("enc_secret"),
		PhoneNumbers:          []string{"+8801812345678"},
		SupportedChannels:     []string{"bkash", "nagad", "rocket"},
		Status:                "online",
	}
	if err := st.RegisterDevice(ctx, device); err != nil {
		t.Fatalf("create device: %v", err)
	}

	mockEmail := &email.MockEmailDriver{}
	mockSMS := &sms.MockDriver{DriverName: "test_sms_driver"}
	server := api.NewServer(cfg, st, sms.NewService(st, mockSMS, nil), email.NewService(st, mockEmail))

	return cfg, st, server, tenant, rawAPIKey, device, rawDeviceToken
}

// TestSecurityAndIsolation verifies authentication, bad token handling, and multi-tenant data boundaries.
func TestSecurityAndIsolation(t *testing.T) {
	_, st, server, tenant1, key1, _, devToken := setupTestEnvironment(t)
	defer st.Close()
	defer server.Close()
	handler := server.Handler()
	ctx := context.Background()

	// 1. Unauthenticated request
	req1 := httptest.NewRequest(http.MethodPost, "/v1/payments/sessions", bytes.NewReader([]byte("{}")))
	w1 := httptest.NewRecorder()
	handler.ServeHTTP(w1, req1)
	if w1.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for unauthenticated request, got %d", w1.Code)
	}

	// 2. Malformed Authorization Header (No Bearer)
	req2 := httptest.NewRequest(http.MethodPost, "/v1/payments/sessions", bytes.NewReader([]byte("{}")))
	req2.Header.Set("Authorization", "Basic "+key1)
	w2 := httptest.NewRecorder()
	handler.ServeHTTP(w2, req2)
	if w2.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for non-Bearer auth, got %d", w2.Code)
	}

	// 3. Invalid API Key prefix
	req3 := httptest.NewRequest(http.MethodPost, "/v1/payments/sessions", bytes.NewReader([]byte("{}")))
	req3.Header.Set("Authorization", "Bearer sk_live_invalidprefix")
	w3 := httptest.NewRecorder()
	handler.ServeHTTP(w3, req3)
	if w3.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad key prefix, got %d", w3.Code)
	}

	// 4. Invalid Device Token
	req4 := httptest.NewRequest(http.MethodPost, "/v1/ingest/android-ping", nil)
	req4.Header.Set("Authorization", "Bearer rb_dev_nonexistenttoken")
	w4 := httptest.NewRecorder()
	handler.ServeHTTP(w4, req4)
	if w4.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for bad device token, got %d", w4.Code)
	}

	// 5. Valid Device Heartbeat Ping
	reqPing := httptest.NewRequest(http.MethodPost, "/v1/ingest/android-ping", nil)
	reqPing.Header.Set("Authorization", "Bearer "+devToken)
	wPing := httptest.NewRecorder()
	handler.ServeHTTP(wPing, reqPing)
	if wPing.Code != http.StatusOK {
		t.Errorf("expected 200 for valid device ping, got %d: %s", wPing.Code, wPing.Body.String())
	}

	// 6. Cross-Tenant Isolation: Create session under Tenant 1, verify Tenant 2 cannot access it
	session := &models.PaymentSession{
		ID:                  store.GenerateUUID(),
		TenantID:            tenant1.ID,
		ExternalReferenceID: "REF-ISOLATION-01",
		AmountMinor:         50000,
		Currency:            "BDT",
		Status:              models.PaymentPending,
		ReturnURL:           "https://example.com/return",
		ExpiresAt:           time.Now().Add(30 * time.Minute),
	}
	if err := st.CreatePaymentSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Seed Tenant 2
	key2 := "rb_live_tenant_secondary_key_abcdef"
	tenant2 := &models.Tenant{
		Name:         "Audit Test Tenant Secondary",
		Environment:  models.EnvLive,
		APIKeyPrefix: "rb_live_",
		APIKeyHash:   crypto.HashSHA256([]byte(key2)),
		WebhookURL:   "https://secondary.example/webhook",
		RateLimitRPM: 120,
		IsActive:     true,
	}
	if err := st.CreateTenant(ctx, tenant2); err != nil {
		t.Fatalf("create tenant 2: %v", err)
	}

	// Tenant 2 requests Tenant 1's session -> must return 404 Not Found
	reqCross := httptest.NewRequest(http.MethodGet, "/v1/payments/sessions/"+session.ID, nil)
	reqCross.Header.Set("Authorization", "Bearer "+key2)
	wCross := httptest.NewRecorder()
	handler.ServeHTTP(wCross, reqCross)
	if wCross.Code != http.StatusNotFound {
		t.Errorf("expected 404 cross-tenant isolation, got %d: %s", wCross.Code, wCross.Body.String())
	}
}

// TestMFSReconciliationRace verifies that concurrent requests verifying the same TrxID
// are completely atomic, yielding exactly 1 winner and 0 duplicate outbox webhooks.
func TestMFSReconciliationRace(t *testing.T) {
	_, st, server, tenant, apiKey, _, _ := setupTestEnvironment(t)
	defer st.Close()
	defer server.Close()
	handler := server.Handler()
	ctx := context.Background()

	// Seed payment session
	session := &models.PaymentSession{
		ID:                  store.GenerateUUID(),
		TenantID:            tenant.ID,
		ExternalReferenceID: "REF-RACE-001",
		AmountMinor:         100000, // 1000.00 BDT
		Currency:            "BDT",
		Status:              models.PaymentPending,
		CustomerPhone:       "01712345678",
		ReturnURL:           "https://example.com/return",
		ExpiresAt:           time.Now().Add(30 * time.Minute),
	}
	if err := st.CreatePaymentSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Pre-seed matching incoming SMS in inbox buffer
	if err := st.SaveUnmatchedSMS(ctx, &models.MFSUnmatchedSMS{
		TenantID:        tenant.ID,
		DeviceID:        "audit-test-device",
		Channel:         "bkash",
		SenderShortcode: "16247",
		AmountMinor:     100000,
		TransactionID:   "9K72B8X9M1",
		SenderPhone:     "01712345678",
		RawSMSBody:      "You have received Tk 1,000.00 from 01712345678. Fee Tk 0.00. Balance Tk 15,230.12. TrxID 9K72B8X9M1 at 21/11/2026 14:30",
		Status:          "unmatched",
		ExpiresAt:       time.Now().Add(30 * time.Minute),
	}); err != nil {
		t.Fatalf("seed inbox sms: %v", err)
	}

	// 20 concurrent goroutines racing to verify
	const concurrency = 20
	var wg sync.WaitGroup
	var verifiedCount int32
	var processingCount int32
	var alreadyVerifiedCount int32

	verifyBody, _ := json.Marshal(map[string]any{
		"channel":        "bkash",
		"transaction_id": "9K72B8X9M1",
		"customer_phone": "01712345678",
	})

	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/v1/payments/sessions/"+session.ID+"/verify", bytes.NewReader(verifyBody))
			req.Header.Set("Authorization", "Bearer "+apiKey)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code == http.StatusOK {
				var res map[string]any
				_ = json.Unmarshal(w.Body.Bytes(), &res)
				status, _ := res["status"].(string)
				msg, _ := res["message"].(string)

				if status == "verified" {
					if msg == "Session is already verified" {
						atomic.AddInt32(&alreadyVerifiedCount, 1)
					} else {
						atomic.AddInt32(&verifiedCount, 1)
					}
				} else if status == "processing" {
					atomic.AddInt32(&processingCount, 1)
				}
			}
		}()
	}
	wg.Wait()

	// Invariant Check 1: Exactly 1 goroutine matched and verified the transaction
	if verifiedCount != 1 {
		t.Errorf("expected exactly 1 goroutine to transition session to verified, got %d", verifiedCount)
	}

	// Invariant Check 2: Session is marked verified in DB
	finalSession, err := st.GetPaymentSession(ctx, tenant.ID, session.ID)
	if err != nil {
		t.Fatalf("get final session: %v", err)
	}
	if finalSession.Status != models.PaymentVerified {
		t.Errorf("expected session to be verified, got %s", finalSession.Status)
	}
	if finalSession.TransactionID != "9K72B8X9M1" {
		t.Errorf("expected TrxID 9K72B8X9M1, got %s", finalSession.TransactionID)
	}

	// Invariant Check 3: Exactly 1 outbox webhook job was generated
	jobs, err := st.FetchPendingOutboxJobs(ctx, 100)
	if err != nil {
		t.Fatalf("fetch outbox jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected exactly 1 outbox webhook job created, got %d", len(jobs))
	}
	if jobs[0].JobType != "webhook" {
		t.Errorf("expected job_type webhook, got %s", jobs[0].JobType)
	}
}

// TestReverseReconciliationOrder verifies the case where SMS arrives before customer enters TrxID.
func TestReverseReconciliationOrder(t *testing.T) {
	_, st, server, tenant, apiKey, _, devToken := setupTestEnvironment(t)
	defer st.Close()
	defer server.Close()
	handler := server.Handler()
	ctx := context.Background()

	// 1. Android SIM Bridge ingests SMS first
	smsPayload := map[string]any{
		"sender": "16247",
		"body":   "You have received Tk 350.00 from 01899887766. Fee Tk 0.00. Balance Tk 4,100.00. TrxID BKASH350REV at 21/11/2026 15:00",
	}
	smsBytes, _ := json.Marshal(smsPayload)
	reqSMS := httptest.NewRequest(http.MethodPost, "/v1/ingest/android-sms", bytes.NewReader(smsBytes))
	reqSMS.Header.Set("Authorization", "Bearer "+devToken)
	reqSMS.Header.Set("Content-Type", "application/json")
	wSMS := httptest.NewRecorder()
	handler.ServeHTTP(wSMS, reqSMS)

	if wSMS.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from SMS ingest, got %d: %s", wSMS.Code, wSMS.Body.String())
	}
	var ingestRes map[string]any
	_ = json.Unmarshal(wSMS.Body.Bytes(), &ingestRes)
	if ingestRes["action"] != "unmatched_inbox_stored" && ingestRes["action"] != "buffered_inbox" {
		t.Fatalf("expected action 'unmatched_inbox_stored' or 'buffered_inbox', got %v", ingestRes["action"])
	}

	// 2. Customer creates payment session for 350 BDT
	session := &models.PaymentSession{
		ID:                  store.GenerateUUID(),
		TenantID:            tenant.ID,
		ExternalReferenceID: "REF-REV-001",
		AmountMinor:         35000, // 350.00 BDT
		Currency:            "BDT",
		Status:              models.PaymentPending,
		CustomerPhone:       "01899887766",
		ReturnURL:           "https://example.com/return",
		ExpiresAt:           time.Now().Add(30 * time.Minute),
	}
	if err := st.CreatePaymentSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// 3. Customer submits TrxID via verification endpoint -> should match immediately from buffer!
	verifyPayload := map[string]any{
		"channel":        "bkash",
		"transaction_id": "BKASH350REV",
		"customer_phone": "01899887766",
	}
	vBytes, _ := json.Marshal(verifyPayload)
	reqV := httptest.NewRequest(http.MethodPost, "/v1/payments/sessions/"+session.ID+"/verify", bytes.NewReader(vBytes))
	reqV.Header.Set("Authorization", "Bearer "+apiKey)
	reqV.Header.Set("Content-Type", "application/json")
	wV := httptest.NewRecorder()
	handler.ServeHTTP(wV, reqV)

	if wV.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", wV.Code, wV.Body.String())
	}
	var vRes map[string]any
	_ = json.Unmarshal(wV.Body.Bytes(), &vRes)
	if vRes["status"] != "verified" || vRes["matched_source"] != "inbox_buffer" {
		t.Fatalf("expected status verified with matched_source inbox_buffer, got: %v", vRes)
	}

	// 4. Verify buffer was cleared
	inbox, err := st.FindMatchingUnmatchedSMS(ctx, tenant.ID, "bkash", "BKASH350REV", 35000)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("error checking inbox: %v", err)
	}
	if inbox != nil {
		t.Errorf("expected inbox buffer to be consumed and deleted, but entry remained")
	}
}

// TestPublicHostedCheckoutFlow verifies public UI rendering, TrxID submission, and polling.
func TestPublicHostedCheckoutFlow(t *testing.T) {
	_, st, server, tenant, _, _, _ := setupTestEnvironment(t)
	defer st.Close()
	defer server.Close()
	handler := server.Handler()
	ctx := context.Background()

	// 1. Create session
	session := &models.PaymentSession{
		ID:                  store.GenerateUUID(),
		TenantID:            tenant.ID,
		ExternalReferenceID: "REF-PUBLIC-UI",
		AmountMinor:         25000, // 250.00 BDT
		Currency:            "BDT",
		Status:              models.PaymentPending,
		CustomerPhone:       "01700112233",
		ReturnURL:           "https://merchant.example/success",
		ExpiresAt:           time.Now().Add(30 * time.Minute),
	}
	if err := st.CreatePaymentSession(ctx, session); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// 2. GET /checkout/{id} returns HTML page (no auth header needed)
	reqPage := httptest.NewRequest(http.MethodGet, "/checkout/"+session.ID, nil)
	wPage := httptest.NewRecorder()
	handler.ServeHTTP(wPage, reqPage)

	if wPage.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for hosted checkout HTML, got %d: %s", wPage.Code, wPage.Body.String())
	}
	bodyStr := wPage.Body.String()
	if !bytes.Contains([]byte(bodyStr), []byte("REF-PUBLIC-UI")) {
		t.Errorf("HTML does not contain order reference")
	}
	if !bytes.Contains([]byte(bodyStr), []byte("250.00")) {
		t.Errorf("HTML does not contain formatted amount")
	}

	// 3. POST /checkout/{id}/verify without API key (public customer submission)
	verifyPayload := map[string]any{
		"channel":        "bkash",
		"transaction_id": "PUBTRX8899",
		"customer_phone": "01700112233",
	}
	vBytes, _ := json.Marshal(verifyPayload)
	reqVerify := httptest.NewRequest(http.MethodPost, "/checkout/"+session.ID+"/verify", bytes.NewReader(vBytes))
	reqVerify.Header.Set("Content-Type", "application/json")
	wVerify := httptest.NewRecorder()
	handler.ServeHTTP(wVerify, reqVerify)

	if wVerify.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from public verify, got %d: %s", wVerify.Code, wVerify.Body.String())
	}
	var resVerify map[string]any
	_ = json.Unmarshal(wVerify.Body.Bytes(), &resVerify)
	if resVerify["status"] != "processing" {
		t.Errorf("expected status 'processing', got %v", resVerify["status"])
	}

	// 4. GET /checkout/{id}/status polling
	reqStatus := httptest.NewRequest(http.MethodGet, "/checkout/"+session.ID+"/status", nil)
	wStatus := httptest.NewRecorder()
	handler.ServeHTTP(wStatus, reqStatus)

	if wStatus.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from public status polling, got %d", wStatus.Code)
	}
	var resStatus map[string]any
	_ = json.Unmarshal(wStatus.Body.Bytes(), &resStatus)
	if resStatus["status"] != "processing" {
		t.Errorf("expected polling status 'processing', got %v", resStatus["status"])
	}
	if resStatus["transaction_id"] != "PUBTRX8899" {
		t.Errorf("expected transaction_id PUBTRX8899, got %v", resStatus["transaction_id"])
	}
}

// TestDeadLetterQueueAndReplay verifies admin dead-letter queries and manual replay operations.
func TestDeadLetterQueueAndReplay(t *testing.T) {
	_, st, server, tenant, apiKey, _, _ := setupTestEnvironment(t)
	defer st.Close()
	defer server.Close()
	handler := server.Handler()
	ctx := context.Background()

	// 1. Manually insert a dead_letter job for tenant
	deadJobID := store.GenerateUUID()
	deadQuery := `INSERT INTO outbox_jobs (id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, last_error, created_at, updated_at)
		VALUES (?, ?, 'webhook', ?, 'dead_letter', 5, 5, ?, 'connection refused', ?, ?)`
	now := time.Now().UTC()
	if _, err := st.ExecContext(ctx, deadQuery, deadJobID, tenant.ID, []byte(`{"event":"test"}`), now, now, now); err != nil {
		t.Fatalf("seed dead letter job: %v", err)
	}

	// 2. GET /v1/admin/outbox/dead-letter
	reqList := httptest.NewRequest(http.MethodGet, "/v1/admin/outbox/dead-letter", nil)
	reqList.Header.Set("Authorization", "Bearer "+apiKey)
	wList := httptest.NewRecorder()
	handler.ServeHTTP(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from dlq list, got %d: %s", wList.Code, wList.Body.String())
	}
	var listResp map[string]any
	_ = json.Unmarshal(wList.Body.Bytes(), &listResp)
	if count, ok := listResp["count"].(float64); !ok || int(count) != 1 {
		t.Fatalf("expected count 1 in DLQ, got %v", listResp["count"])
	}

	// 3. POST /v1/admin/outbox/dead-letter/{id}/retry
	reqRetry := httptest.NewRequest(http.MethodPost, "/v1/admin/outbox/dead-letter/"+deadJobID+"/retry", nil)
	reqRetry.Header.Set("Authorization", "Bearer "+apiKey)
	wRetry := httptest.NewRecorder()
	handler.ServeHTTP(wRetry, reqRetry)

	if wRetry.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from retry DLQ, got %d: %s", wRetry.Code, wRetry.Body.String())
	}

	// 4. Confirm job returned to 'pending' with attempts = 0
	pendingJobs, err := st.FetchPendingOutboxJobs(ctx, 10)
	if err != nil {
		t.Fatalf("fetch pending jobs: %v", err)
	}
	if len(pendingJobs) != 1 || pendingJobs[0].ID != deadJobID {
		t.Fatalf("expected requeued job %s in pending status, found %d jobs", deadJobID, len(pendingJobs))
	}
	if pendingJobs[0].Attempts != 0 {
		t.Errorf("expected attempts reset to 0, got %d", pendingJobs[0].Attempts)
	}
}

// TestRateLimiterEnforcement tests the in-memory token bucket rate limiter.
func TestRateLimiterEnforcement(t *testing.T) {
	_, st, server, tenant, apiKey, _, _ := setupTestEnvironment(t)
	defer st.Close()
	defer server.Close()
	handler := server.Handler()

	// Update tenant's RPM to a low limit for deterministic throttling test: 12 RPM (burst: 5)
	_, _ = st.ExecContext(context.Background(), "UPDATE tenants SET rate_limit_rpm = 12 WHERE id = ?", tenant.ID)

	var allowed, throttled int
	for i := 0; i < 20; i++ {
		// Test a tenant-authenticated route to exercise tenant rate limit:
		reqTenant := httptest.NewRequest(http.MethodGet, "/v1/payments/sessions/non-existent-id", nil)
		reqTenant.Header.Set("Authorization", "Bearer "+apiKey)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, reqTenant)

		if w.Code == http.StatusTooManyRequests {
			throttled++
		} else {
			allowed++
		}
	}

	if throttled == 0 {
		t.Errorf("expected rate limiter to throttle requests, but all were allowed (allowed: %d, throttled: %d)", allowed, throttled)
	}
}
