package email

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/store"
)

func TestEmail_DispatchAndAlias(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_email.db")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := store.Open(ctx, "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	mockDriver := &MockEmailDriver{}
	svc := NewService(st, mockDriver)

	// 1. Dispatch Email Test
	log, err := svc.DispatchEmail(ctx, "tenant_1", "email_idemp_1", "customer@example.com", "Receipt", "<p>Paid</p>")
	if err != nil {
		t.Fatalf("dispatch email failed: %v", err)
	}
	if log.Status != "sent" {
		t.Errorf("expected status 'sent', got %s", log.Status)
	}
	if len(mockDriver.SentEmails) != 1 {
		t.Fatalf("expected 1 email sent, got %d", len(mockDriver.SentEmails))
	}

	// 2. Register Email Alias
	alias := &models.EmailAlias{
		TenantID:      "tenant_1",
		SourceAddress: "business@swe-labs.com",
		TargetAddress: "founder@gmail.com",
		IsActive:      true,
	}
	if err := st.CreateEmailAlias(ctx, alias); err != nil {
		t.Fatalf("create alias: %v", err)
	}

	// 3. Process Inbound Webhook
	inboundJSON := []byte(`{
		"from": "partner@vendor.com",
		"to": "business@swe-labs.com",
		"subject": "Partnership Inquiry",
		"text": "Hello SWE Labs team"
	}`)

	resolvedAlias, err := svc.ProcessInboundEmail(ctx, inboundJSON)
	if err != nil {
		t.Fatalf("process inbound email failed: %v", err)
	}
	if resolvedAlias.TargetAddress != "founder@gmail.com" {
		t.Errorf("expected target 'founder@gmail.com', got %s", resolvedAlias.TargetAddress)
	}
}
