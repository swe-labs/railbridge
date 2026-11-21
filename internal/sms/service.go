package sms

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/store"
)

// Service coordinates outbound SMS transmission with automated failover and audit logging.
type Service struct {
	Store          *store.DB
	Primary        Driver
	Fallback       Driver
	CircuitBreaker *CircuitBreaker
}

// NewService constructs an SMS service with 3-failure circuit tripping and 10-minute fallback cooldown.
func NewService(st *store.DB, primary, fallback Driver) *Service {
	return &Service{
		Store:          st,
		Primary:        primary,
		Fallback:       fallback,
		CircuitBreaker: NewCircuitBreaker(3, 10*time.Minute),
	}
}

// DispatchSMS normalizes phone, checks idempotency, executes primary or fallback send, and writes audit logs.
func (s *Service) DispatchSMS(ctx context.Context, tenantID, idempotencyKey, to, message string) (*models.MessageLog, error) {
	normalizedPhone, err := NormalizeE164(to)
	if err != nil {
		return nil, err
	}

	// 1. Idempotency Check
	if idempotencyKey != "" {
		existing, err := s.Store.GetMessageLogByIdempotency(ctx, tenantID, "sms", idempotencyKey)
		if err == nil && existing != nil {
			slog.Info("Idempotent SMS replay detected", "tenant_id", tenantID, "key", idempotencyKey)
			return existing, nil
		}
	}

	var providerUsed, providerMsgID string
	var sendErr error

	// 2. Attempt Send via Primary Driver with Circuit Breaker
	err = s.CircuitBreaker.Execute(func() error {
		providerUsed = s.Primary.Name()
		var pErr error
		providerMsgID, pErr = s.Primary.SendSMS(ctx, normalizedPhone, message)
		return pErr
	})

	// 3. Fallback to Secondary Driver if Circuit Breaker tripped or primary failed
	if err != nil {
		slog.Warn("Primary SMS driver failed, falling back to secondary",
			"primary", s.Primary.Name(),
			"fallback", s.Fallback.Name(),
			"error", err,
			"circuit_state", string(s.CircuitBreaker.State()),
		)

		providerUsed = s.Fallback.Name()
		providerMsgID, sendErr = s.Fallback.SendSMS(ctx, normalizedPhone, message)
		if sendErr != nil {
			sendErr = fmt.Errorf("both primary (%v) and fallback (%w) failed", err, sendErr)
		}
	}

	now := time.Now().UTC()
	status := "sent"
	var lastErrStr *string
	var sentAt *time.Time

	if sendErr != nil {
		status = "failed"
		msg := sendErr.Error()
		lastErrStr = &msg
	} else {
		sentAt = &now
	}

	// 4. Record in message_logs table
	log := &models.MessageLog{
		ID:             store.GenerateUUID(),
		TenantID:       tenantID,
		IdempotencyKey: idempotencyKey,
		Rail:           "sms",
		Recipient:      normalizedPhone,
		ProviderUsed:   providerUsed,
		ProviderMsgID:  providerMsgID,
		Status:         status,
		Attempts:       1,
		LastError:      lastErrStr,
		CreatedAt:      now,
		SentAt:         sentAt,
	}

	_ = s.Store.CreateMessageLog(ctx, log)

	if sendErr != nil {
		return log, sendErr
	}

	return log, nil
}
