package railbridge

import (
	"context"
	"fmt"
	"time"
)

type PaymentsService struct {
	client *Client
}

type CreateSessionParams struct {
	ReferenceID        string         `json:"external_reference_id"`
	AmountMinor        int64          `json:"amount_minor"`
	Currency           string         `json:"currency"`
	CustomerPhone      string         `json:"customer_phone,omitempty"`
	ReturnURL          string         `json:"return_url"`
	WebhookURLOverride string         `json:"webhook_url_override,omitempty"`
	Metadata           map[string]any `json:"metadata,omitempty"`
	IdempotencyKey     string         `json:"-"`
}

type PaymentSession struct {
	ID             string    `json:"id"`
	Status         string    `json:"status"`
	AmountMinor    int64     `json:"amount_minor"`
	Currency       string    `json:"currency"`
	CheckoutURL    string    `json:"checkout_url"`
	ExpiresAt      time.Time `json:"expires_at"`
	CreatedAt      time.Time `json:"created_at"`
	IdempotentHit  bool      `json:"idempotent,omitempty"`
}

// CreateSession initializes a new hosted checkout session on Railbridge.
func (s *PaymentsService) CreateSession(ctx context.Context, params *CreateSessionParams) (*PaymentSession, error) {
	if params.Currency == "" {
		params.Currency = "BDT"
	}
	var session PaymentSession
	err := s.client.request(ctx, "POST", "/v1/payments/sessions", params.IdempotencyKey, params, &session)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// GetSession retrieves status and details for an existing payment session.
func (s *PaymentsService) GetSession(ctx context.Context, sessionID string) (*PaymentSession, error) {
	var session PaymentSession
	path := fmt.Sprintf("/v1/payments/sessions/%s", sessionID)
	err := s.client.request(ctx, "GET", path, "", nil, &session)
	if err != nil {
		return nil, err
	}
	return &session, nil
}
