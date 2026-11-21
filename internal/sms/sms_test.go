package sms

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/swe-labs/railbridge/internal/store"
)

func TestNormalizeE164(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "01812345678", want: "+8801812345678", wantErr: false},
		{input: "8801712345678", want: "+8801712345678", wantErr: false},
		{input: "+8801912345678", want: "+8801912345678", wantErr: false},
		{input: "017-1234-5678", want: "+8801712345678", wantErr: false},
		{input: "12345", want: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := NormalizeE164(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NormalizeE164(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCircuitBreaker_Failover(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_sms.db")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := store.Open(ctx, "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	primary := &MockDriver{DriverName: "commercial_gateway", ShouldFail: true}
	fallback := &MockDriver{DriverName: "android_sim_bridge", ShouldFail: false}

	svc := NewService(st, primary, fallback)

	// Send 3 times with failing primary; should seamlessly failover to fallback
	for i := 1; i <= 3; i++ {
		log, err := svc.DispatchSMS(ctx, "tenant_001", "", "01712345678", "OTP 1234")
		if err != nil {
			t.Fatalf("dispatch %d failed unexpectedly despite fallback: %v", i, err)
		}
		if log.ProviderUsed != "android_sim_bridge" {
			t.Errorf("expected provider used 'android_sim_bridge', got %s", log.ProviderUsed)
		}
		if log.Status != "sent" {
			t.Errorf("expected status 'sent', got %s", log.Status)
		}
	}

	// Circuit should now be tripped to OPEN
	if svc.CircuitBreaker.State() != StateOpen {
		t.Fatalf("expected circuit breaker state OPEN after 3 failures, got %s", svc.CircuitBreaker.State())
	}
}
