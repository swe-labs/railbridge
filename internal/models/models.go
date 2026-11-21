package models

import (
	"encoding/json"
	"time"
)

// Environment represents tenant mode (live | test).
type Environment string

const (
	EnvLive Environment = "live"
	EnvTest Environment = "test"
)

// Tenant represents an isolated client application.
type Tenant struct {
	ID                     string          `json:"id"`
	Name                   string          `json:"name"`
	Environment            Environment     `json:"environment"`
	APIKeyPrefix           string          `json:"api_key_prefix"`
	APIKeyHash             []byte          `json:"-"`
	WebhookURL             string          `json:"webhook_url,omitempty"`
	WebhookSecretEncrypted []byte          `json:"-"`
	BrandingConfig         json.RawMessage `json:"branding_config"`
	RateLimitRPM           int             `json:"rate_limit_rpm"`
	IsActive               bool            `json:"is_active"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

// PaymentStatus represents the lifecycle of a payment session.
type PaymentStatus string

const (
	PaymentPending    PaymentStatus = "pending"
	PaymentProcessing PaymentStatus = "processing"
	PaymentVerified   PaymentStatus = "verified"
	PaymentFailed     PaymentStatus = "failed"
	PaymentExpired    PaymentStatus = "expired"
	PaymentRefunded   PaymentStatus = "refunded"
)

// PaymentSession represents a hosted checkout payment request.
type PaymentSession struct {
	ID                  string          `json:"id"`
	TenantID            string          `json:"tenant_id"`
	ExternalReferenceID string          `json:"external_reference_id"`
	IdempotencyKey      string          `json:"idempotency_key,omitempty"`
	AmountMinor         int64           `json:"amount_minor"`
	Currency            string          `json:"currency"`
	Status              PaymentStatus   `json:"status"`
	Channel             string          `json:"channel,omitempty"`
	CustomerPhone       string          `json:"customer_phone,omitempty"`
	TransactionID       string          `json:"transaction_id,omitempty"`
	ReturnURL           string          `json:"return_url"`
	WebhookURLOverride  string          `json:"webhook_url_override,omitempty"`
	Metadata            json.RawMessage `json:"metadata"`
	ProviderPayload     json.RawMessage `json:"provider_payload,omitempty"`
	ExpiresAt           time.Time       `json:"expires_at"`
	VerifiedAt          *time.Time      `json:"verified_at,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// Device represents an Android SIM Bridge hardware node.
type Device struct {
	ID                    string    `json:"id"`
	TenantID              string    `json:"tenant_id"`
	DeviceName            string    `json:"device_name"`
	DeviceTokenHash       []byte    `json:"-"`
	DeviceSecretEncrypted []byte    `json:"-"`
	PhoneNumbers          []string  `json:"phone_numbers"`
	SupportedChannels     []string  `json:"supported_channels"`
	Status                string    `json:"status"` // online | degraded | offline
	LastHeartbeatAt       *time.Time `json:"last_heartbeat_at,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
	UpdatedAt             time.Time `json:"updated_at"`
}

// MFSUnmatchedSMS represents an incoming MFS SMS buffered awaiting reconciliation.
type MFSUnmatchedSMS struct {
	ID               string    `json:"id"`
	TenantID         string    `json:"tenant_id"`
	DeviceID         string    `json:"device_id"`
	Channel          string    `json:"channel"`
	SenderShortcode  string    `json:"sender_shortcode"`
	TransactionID    string    `json:"transaction_id"`
	AmountMinor      int64     `json:"amount_minor"`
	SenderPhone      string    `json:"sender_phone"`
	RawSMSBody       string    `json:"raw_sms_body"`
	MatchedSessionID *string   `json:"matched_session_id,omitempty"`
	Status           string    `json:"status"` // unmatched | matched | expired
	ExpiresAt        time.Time `json:"expires_at"`
	CreatedAt        time.Time `json:"created_at"`
}

// MessageLog represents an audit log entry for SMS or email transmission.
type MessageLog struct {
	ID             string     `json:"id"`
	TenantID       string     `json:"tenant_id"`
	IdempotencyKey string     `json:"idempotency_key,omitempty"`
	Rail           string     `json:"rail"` // sms | email
	Recipient      string     `json:"recipient"`
	Subject        string     `json:"subject,omitempty"`
	TemplateID     string     `json:"template_id,omitempty"`
	ProviderUsed   string     `json:"provider_used,omitempty"`
	ProviderMsgID  string     `json:"provider_msg_id,omitempty"`
	Status         string     `json:"status"` // queued | sent | delivered | failed
	Attempts       int        `json:"attempts"`
	LastError      *string    `json:"last_error,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
}

// EmailAlias represents a custom domain virtual forwarder mapping.
type EmailAlias struct {
	ID            string    `json:"id"`
	TenantID      string    `json:"tenant_id"`
	SourceAddress string    `json:"source_address"` // e.g. business@swe-labs.com
	TargetAddress string    `json:"target_address"` // e.g. founder@gmail.com
	IsActive      bool      `json:"is_active"`
	CreatedAt     time.Time `json:"created_at"`
}

// OutboxJob represents an asynchronous task persisted in SQL.
type OutboxJob struct {
	ID          string          `json:"id"`
	TenantID    string          `json:"tenant_id"`
	JobType     string          `json:"job_type"` // webhook | sms | email
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status"` // pending | running | completed | dead_letter
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	NextRunAt   time.Time       `json:"next_run_at"`
	LastError   *string         `json:"last_error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}
