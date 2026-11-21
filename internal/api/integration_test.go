package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/swe-labs/railbridge/internal/config"
	"github.com/swe-labs/railbridge/internal/crypto"
	"github.com/swe-labs/railbridge/internal/email"
	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/store"
)

func TestPaymentLifecycle_E2E(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_e2e.db")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config load: %v", err)
	}

	st, err := store.Open(ctx, "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	// 1. Seed Tenant
	rawAPIKey := "rb_live_sec_1234567890abcdef12345678"
	keyHash := crypto.HashSHA256([]byte(rawAPIKey))
	tenant := &models.Tenant{
		Name:         "Acme E-Commerce",
		Environment:  models.EnvLive,
		APIKeyPrefix: "rb_live_",
		APIKeyHash:   keyHash,
		WebhookURL:   "https://acme-store.com/webhooks/railbridge",
		RateLimitRPM: 120,
		IsActive:     true,
	}
	if err := st.CreateTenant(ctx, tenant); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}

	// 2. Seed Device (Android SIM Bridge)
	rawDeviceToken := "rb_dev_phone_sim_secret_token_11"
	devTokenHash := crypto.HashSHA256([]byte(rawDeviceToken))
	device := &models.Device{
		TenantID:              tenant.ID,
		DeviceName:            "Warehouse bKash Android Node",
		DeviceTokenHash:       devTokenHash,
		DeviceSecretEncrypted: []byte("encrypted_secret"),
		PhoneNumbers:          []string{"+8801812345678"},
		SupportedChannels:     []string{"bkash", "nagad"},
		Status:                "online",
	}
	if err := st.RegisterDevice(ctx, device); err != nil {
		t.Fatalf("seed device: %v", err)
	}

	mockEmail := &email.MockEmailDriver{}
	emailSvc := email.NewService(st, mockEmail)
	server := NewServer(cfg, st, nil, emailSvc)
	handler := server.Handler()

	// 3. Create Payment Session
	createPayload := map[string]any{
		"external_reference_id": "ORD-9901",
		"amount_minor":          15000, // 150.00 BDT
		"currency":              "BDT",
		"customer_phone":        "+8801711223344",
		"return_url":            "https://acme-store.com/checkout/success",
	}
	payloadBytes, _ := json.Marshal(createPayload)

	reqCreate := httptest.NewRequest(http.MethodPost, "/v1/payments/sessions", bytes.NewReader(payloadBytes))
	reqCreate.Header.Set("Authorization", "Bearer "+rawAPIKey)
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("Idempotency-Key", "idemp_order_9901")
	rrCreate := httptest.NewRecorder()

	handler.ServeHTTP(rrCreate, reqCreate)
	if rrCreate.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created from session creation, got %d: %s", rrCreate.Code, rrCreate.Body.String())
	}

	var sessionResp map[string]any
	_ = json.Unmarshal(rrCreate.Body.Bytes(), &sessionResp)
	sessionID := sessionResp["id"].(string)

	// 4. Test Idempotency Guard (Repeated request returns 200 OK with identical session ID)
	reqReplay := httptest.NewRequest(http.MethodPost, "/v1/payments/sessions", bytes.NewReader(payloadBytes))
	reqReplay.Header.Set("Authorization", "Bearer "+rawAPIKey)
	reqReplay.Header.Set("Content-Type", "application/json")
	reqReplay.Header.Set("Idempotency-Key", "idemp_order_9901")
	rrReplay := httptest.NewRecorder()

	handler.ServeHTTP(rrReplay, reqReplay)
	if rrReplay.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on idempotent retry, got %d", rrReplay.Code)
	}
	var replayResp map[string]any
	_ = json.Unmarshal(rrReplay.Body.Bytes(), &replayResp)
	if replayResp["id"] != sessionID {
		t.Fatalf("expected identical session ID %s on replay, got %v", sessionID, replayResp["id"])
	}

	// 5. Customer submits TrxID first on hosted checkout -> transitions to 'processing'
	verifyPayload := map[string]any{
		"channel":        "bkash",
		"transaction_id": "9K72B8X9M1",
		"customer_phone": "+8801711223344",
	}
	verifyBytes, _ := json.Marshal(verifyPayload)
	reqVerify := httptest.NewRequest(http.MethodPost, "/v1/payments/sessions/"+sessionID+"/verify", bytes.NewReader(verifyBytes))
	reqVerify.Header.Set("Authorization", "Bearer "+rawAPIKey)
	reqVerify.Header.Set("Content-Type", "application/json")
	rrVerify := httptest.NewRecorder()

	handler.ServeHTTP(rrVerify, reqVerify)
	if rrVerify.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from customer verify, got %d: %s", rrVerify.Code, rrVerify.Body.String())
	}
	var verifyResp map[string]any
	_ = json.Unmarshal(rrVerify.Body.Bytes(), &verifyResp)
	if verifyResp["status"] != "processing" {
		t.Fatalf("expected status 'processing' while waiting for SMS, got %v", verifyResp["status"])
	}

	// 6. Android SIM Bridge receives and ingests the official operator SMS (from 16247)
	smsPayload := map[string]any{
		"sender": "16247",
		"body":   "You have received Tk 150.00 from 01711223344. Fee Tk 0.00. Balance Tk 5,230.12. TrxID 9K72B8X9M1 at 21/11/2026 14:30",
	}
	smsBytes, _ := json.Marshal(smsPayload)
	reqSMS := httptest.NewRequest(http.MethodPost, "/v1/ingest/android-sms", bytes.NewReader(smsBytes))
	reqSMS.Header.Set("Authorization", "Bearer "+rawDeviceToken)
	reqSMS.Header.Set("Content-Type", "application/json")
	rrSMS := httptest.NewRecorder()

	handler.ServeHTTP(rrSMS, reqSMS)
	if rrSMS.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from SMS ingest, got %d: %s", rrSMS.Code, rrSMS.Body.String())
	}

	var ingestResp map[string]any
	_ = json.Unmarshal(rrSMS.Body.Bytes(), &ingestResp)
	if ingestResp["status"] != "verified" || ingestResp["action"] != "session_matched" {
		t.Fatalf("expected matched verified response from SMS ingest, got: %v", ingestResp)
	}

	// 7. Verify session in database is now 'verified'
	verifiedSession, err := st.GetPaymentSession(ctx, tenant.ID, sessionID)
	if err != nil {
		t.Fatalf("failed to retrieve session: %v", err)
	}
	if verifiedSession.Status != models.PaymentVerified {
		t.Fatalf("expected session status to be 'verified', got %s", verifiedSession.Status)
	}
	if verifiedSession.TransactionID != "9K72B8X9M1" {
		t.Fatalf("expected TrxID 9K72B8X9M1, got %s", verifiedSession.TransactionID)
	}
	if verifiedSession.VerifiedAt == nil {
		t.Fatal("expected VerifiedAt timestamp to be populated")
	}

	// 8. Verify outbox job was queued for webhook dispatch
	jobs, err := st.FetchPendingOutboxJobs(ctx, 10)
	if err != nil {
		t.Fatalf("failed to fetch outbox jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("expected 1 outbox job queued for webhook, got %d", len(jobs))
	}
	if jobs[0].JobType != "webhook" {
		t.Errorf("expected job_type 'webhook', got %s", jobs[0].JobType)
	}

	// 9. Verify Device Heartbeat Ping
	reqPing := httptest.NewRequest(http.MethodPost, "/v1/ingest/android-ping", nil)
	reqPing.Header.Set("Authorization", "Bearer "+rawDeviceToken)
	rrPing := httptest.NewRecorder()
	handler.ServeHTTP(rrPing, reqPing)

	if rrPing.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from android-ping, got %d", rrPing.Code)
	}

	// 10. Dispatch Transactional SMS
	smsSendPayload := map[string]string{
		"to":      "01812345678",
		"message": "Your verification code is 9812.",
	}
	smsSendBytes, _ := json.Marshal(smsSendPayload)
	reqSendSMS := httptest.NewRequest(http.MethodPost, "/v1/sms/messages", bytes.NewReader(smsSendBytes))
	reqSendSMS.Header.Set("Authorization", "Bearer "+rawAPIKey)
	reqSendSMS.Header.Set("Content-Type", "application/json")
	rrSendSMS := httptest.NewRecorder()

	handler.ServeHTTP(rrSendSMS, reqSendSMS)
	if rrSendSMS.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted from send SMS, got %d: %s", rrSendSMS.Code, rrSendSMS.Body.String())
	}

	// 11. Dispatch Transactional Email
	emailSendPayload := map[string]string{
		"to":        "customer@example.com",
		"subject":   "Your Order Receipt",
		"html_body": "<p>Thank you for your order!</p>",
	}
	emailSendBytes, _ := json.Marshal(emailSendPayload)
	reqSendEmail := httptest.NewRequest(http.MethodPost, "/v1/emails/messages", bytes.NewReader(emailSendBytes))
	reqSendEmail.Header.Set("Authorization", "Bearer "+rawAPIKey)
	reqSendEmail.Header.Set("Content-Type", "application/json")
	rrSendEmail := httptest.NewRecorder()

	handler.ServeHTTP(rrSendEmail, reqSendEmail)
	if rrSendEmail.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted from send email, got %d: %s", rrSendEmail.Code, rrSendEmail.Body.String())
	}

	// 12. Register Custom Email Alias
	aliasPayload := map[string]string{
		"source_address": "business@swe-labs.com",
		"target_address": "founder@gmail.com",
	}
	aliasBytes, _ := json.Marshal(aliasPayload)
	reqAlias := httptest.NewRequest(http.MethodPost, "/v1/emails/aliases", bytes.NewReader(aliasBytes))
	reqAlias.Header.Set("Authorization", "Bearer "+rawAPIKey)
	reqAlias.Header.Set("Content-Type", "application/json")
	rrAlias := httptest.NewRecorder()

	handler.ServeHTTP(rrAlias, reqAlias)
	if rrAlias.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created from create alias, got %d", rrAlias.Code)
	}

	// 13. Ingest Inbound Email Routed from Cloudflare Email Worker
	inboundPayload := map[string]string{
		"from":    "partner@vendor.com",
		"to":      "business@swe-labs.com",
		"subject": "Contract Draft",
		"text":    "Please see attached.",
	}
	inboundBytes, _ := json.Marshal(inboundPayload)
	reqInbound := httptest.NewRequest(http.MethodPost, "/v1/emails/inbound", bytes.NewReader(inboundBytes))
	reqInbound.Header.Set("Content-Type", "application/json")
	rrInbound := httptest.NewRecorder()

	handler.ServeHTTP(rrInbound, reqInbound)
	if rrInbound.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from inbound email routing, got %d: %s", rrInbound.Code, rrInbound.Body.String())
	}

	// 14. Render Hosted Checkout Page (HTML)
	reqCheckout := httptest.NewRequest(http.MethodGet, "/checkout/"+sessionID, nil)
	rrCheckout := httptest.NewRecorder()

	handler.ServeHTTP(rrCheckout, reqCheckout)
	if rrCheckout.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from hosted checkout UI, got %d", rrCheckout.Code)
	}
	checkoutHTML := rrCheckout.Body.String()
	if !bytes.Contains([]byte(checkoutHTML), []byte("BDT 150.00")) {
		t.Fatalf("expected checkout page to render amount BDT 150.00, got: %s", checkoutHTML)
	}
	if !bytes.Contains([]byte(checkoutHTML), []byte("ORD-9901")) {
		t.Fatalf("expected checkout page to render reference ORD-9901")
	}
}
