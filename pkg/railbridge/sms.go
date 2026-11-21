package railbridge

import (
	"context"
	"time"
)

type SMSService struct {
	client *Client
}

type SendSMSParams struct {
	To             string `json:"to"`
	Message        string `json:"message"`
	IdempotencyKey string `json:"-"`
}

type SMSResponse struct {
	JobID        string     `json:"job_id"`
	Status       string     `json:"status"`
	Recipient    string     `json:"recipient"`
	ProviderUsed string     `json:"provider_used"`
	SentAt       *time.Time `json:"sent_at,omitempty"`
}

// Send dispatches a transactional SMS or OTP through Railbridge.
func (s *SMSService) Send(ctx context.Context, params *SendSMSParams) (*SMSResponse, error) {
	var resp SMSResponse
	err := s.client.request(ctx, "POST", "/v1/sms/messages", params.IdempotencyKey, params, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
