package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/store"
)

// Service coordinates outbound transactional emails and custom domain alias routing.
type Service struct {
	Store  *store.DB
	Driver Driver
}

func NewService(st *store.DB, d Driver) *Service {
	return &Service{
		Store:  st,
		Driver: d,
	}
}

// DispatchEmail sends a transactional email, checks idempotency, and records the audit log.
func (s *Service) DispatchEmail(ctx context.Context, tenantID, idempotencyKey, to, subject, htmlBody string) (*models.MessageLog, error) {
	cleanTo, err := ValidateEmailAddress(to)
	if err != nil {
		return nil, err
	}

	// 1. Idempotency Check
	if idempotencyKey != "" {
		existing, err := s.Store.GetMessageLogByIdempotency(ctx, tenantID, "email", idempotencyKey)
		if err == nil && existing != nil {
			slog.Info("Idempotent email replay detected", "tenant_id", tenantID, "key", idempotencyKey)
			return existing, nil
		}
	}

	// 2. Dispatch via Driver
	providerMsgID, sendErr := s.Driver.SendEmail(ctx, cleanTo, subject, htmlBody)

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

	// 3. Write to message_logs
	log := &models.MessageLog{
		ID:             store.GenerateUUID(),
		TenantID:       tenantID,
		IdempotencyKey: idempotencyKey,
		Rail:           "email",
		Recipient:      cleanTo,
		Subject:        subject,
		ProviderUsed:   s.Driver.Name(),
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

// InboundEmailPayload represents the JSON payload posted by a Cloudflare Email Routing Worker.
type InboundEmailPayload struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text,omitempty"`
	HTML    string `json:"html,omitempty"`
}

// ProcessInboundEmail looks up the virtual custom domain alias, logs the event, and relays to target inbox.
func (s *Service) ProcessInboundEmail(ctx context.Context, rawBody []byte) (*models.EmailAlias, error) {
	var payload InboundEmailPayload
	if err := json.Unmarshal(rawBody, &payload); err != nil {
		return nil, fmt.Errorf("malformed inbound email json: %w", err)
	}

	cleanTo, err := ValidateEmailAddress(payload.To)
	if err != nil {
		return nil, err
	}

	// Find registered alias
	alias, err := s.Store.GetEmailAlias(ctx, strings.ToLower(cleanTo))
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrAliasNotFound, cleanTo)
	}
	if err != nil {
		return nil, fmt.Errorf("database lookup alias %s: %w", cleanTo, err)
	}

	slog.Info("Inbound email routed successfully",
		"source", alias.SourceAddress,
		"target", alias.TargetAddress,
		"subject", payload.Subject,
	)

	return alias, nil
}
