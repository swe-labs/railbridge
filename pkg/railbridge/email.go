package railbridge

import (
	"context"
	"time"
)

type EmailService struct {
	client *Client
}

type SendEmailParams struct {
	To             string `json:"to"`
	Subject        string `json:"subject"`
	HTMLBody       string `json:"html_body"`
	IdempotencyKey string `json:"-"`
}

type EmailResponse struct {
	JobID        string     `json:"job_id"`
	Status       string     `json:"status"`
	Recipient    string     `json:"recipient"`
	ProviderUsed string     `json:"provider_used"`
	SentAt       *time.Time `json:"sent_at,omitempty"`
}

// Send queues and dispatches a transactional email through Railbridge.
func (s *EmailService) Send(ctx context.Context, params *SendEmailParams) (*EmailResponse, error) {
	var resp EmailResponse
	err := s.client.request(ctx, "POST", "/v1/emails/messages", params.IdempotencyKey, params, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
