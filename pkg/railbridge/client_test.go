package railbridge

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func signHMAC(secret []byte, message string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

func TestSDK_ClientE2E(t *testing.T) {
	// Mock server mimicking Railbridge backend
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer rb_live_valid_key_12345" {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		switch r.URL.Path {
		case "/v1/payments/sessions":
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{
				"id": "sess_mock_001",
				"status": "pending",
				"amount_minor": 15000,
				"currency": "BDT",
				"checkout_url": "https://railbridge.alwaysdata.net/checkout/sess_mock_001"
			}`))
		case "/v1/sms/messages":
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{
				"job_id": "job_sms_001",
				"status": "sent",
				"recipient": "+8801812345678",
				"provider_used": "greenweb"
			}`))
		case "/v1/emails/messages":
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{
				"job_id": "job_email_001",
				"status": "sent",
				"recipient": "user@example.com"
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client := NewClient(
		"rb_live_valid_key_12345",
		WithBaseURL(server.URL),
		WithTimeout(3*time.Second),
	)

	// Test Payment Session
	session, err := client.Payments.CreateSession(ctx, &CreateSessionParams{
		ReferenceID: "ORD-1",
		AmountMinor: 15000,
		ReturnURL:   "https://client.com/return",
	})
	if err != nil {
		t.Fatalf("SDK CreateSession failed: %v", err)
	}
	if session.ID != "sess_mock_001" {
		t.Errorf("expected session ID 'sess_mock_001', got %s", session.ID)
	}

	// Test SMS Send
	smsResp, err := client.SMS.Send(ctx, &SendSMSParams{
		To:      "+8801812345678",
		Message: "Code 1234",
	})
	if err != nil {
		t.Fatalf("SDK SMS Send failed: %v", err)
	}
	if smsResp.JobID != "job_sms_001" {
		t.Errorf("expected job ID 'job_sms_001', got %s", smsResp.JobID)
	}

	// Test Email Send
	emailResp, err := client.Email.Send(ctx, &SendEmailParams{
		To:       "user@example.com",
		Subject:  "Welcome",
		HTMLBody: "<p>Hello</p>",
	})
	if err != nil {
		t.Fatalf("SDK Email Send failed: %v", err)
	}
	if emailResp.JobID != "job_email_001" {
		t.Errorf("expected job ID 'job_email_001', got %s", emailResp.JobID)
	}
}

func TestSDK_VerifyWebhook(t *testing.T) {
	secret := "whsec_test_secret_991823"
	body := []byte(`{"event":"payment.verified","session_id":"sess_mock_001","amount_minor":15000}`)
	now := time.Now().Unix()

	header := fmt.Sprintf("t=%d,v1=%s", now, signHMAC([]byte(secret), fmt.Sprintf("%d.%s", now, string(body))))

	event, err := VerifyWebhook(body, header, secret)
	if err != nil {
		t.Fatalf("VerifyWebhook failed on valid signature: %v", err)
	}
	if event.Type != "payment.verified" {
		t.Errorf("expected event type 'payment.verified', got %s", event.Type)
	}
	if event.Data.SessionID != "sess_mock_001" {
		t.Errorf("expected session_id 'sess_mock_001', got %s", event.Data.SessionID)
	}
}
