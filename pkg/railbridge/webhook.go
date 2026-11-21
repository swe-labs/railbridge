package railbridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

var (
	ErrSignatureInvalid = errors.New("railbridge: webhook signature verification failed")
	ErrTimestampExpired = errors.New("railbridge: webhook timestamp outside 300-second window")
)

// WebhookEvent represents a verified outbound notification dispatched by Railbridge.
type WebhookEvent struct {
	Type string           `json:"event"`
	Data WebhookEventData `json:",inline"`
}

type WebhookEventData struct {
	SessionID           string    `json:"session_id,omitempty"`
	ExternalReferenceID string    `json:"external_reference_id,omitempty"`
	AmountMinor         int64     `json:"amount_minor,omitempty"`
	Currency            string    `json:"currency,omitempty"`
	Channel             string    `json:"channel,omitempty"`
	TransactionID       string    `json:"transaction_id,omitempty"`
	CustomerPhone       string    `json:"customer_phone,omitempty"`
	Recipient           string    `json:"recipient,omitempty"`
	VerifiedAt          time.Time `json:"verified_at,omitempty"`
}

// VerifyWebhook validates the timestamped HMAC-SHA256 signature and returns the parsed event.
func VerifyWebhook(rawBody []byte, signatureHeader, webhookSecret string) (*WebhookEvent, error) {
	var timestampStr, signatureHex string
	parts := strings.Split(signatureHeader, ",")
	for _, part := range parts {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			switch kv[0] {
			case "t":
				timestampStr = kv[1]
			case "v1":
				signatureHex = kv[1]
			}
		}
	}

	if timestampStr == "" || signatureHex == "" {
		return nil, ErrSignatureInvalid
	}

	ts, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return nil, ErrSignatureInvalid
	}

	// 300-second replay protection window
	eventTime := time.Unix(ts, 0)
	if math.Abs(time.Since(eventTime).Seconds()) > 300 {
		return nil, ErrTimestampExpired
	}

	payload := fmt.Sprintf("%d.%s", ts, string(rawBody))
	mac := hmac.New(sha256.New, []byte(webhookSecret))
	mac.Write([]byte(payload))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	if subtle.ConstantTimeCompare([]byte(signatureHex), []byte(expectedSig)) != 1 {
		return nil, ErrSignatureInvalid
	}

	var event WebhookEvent
	if err := json.Unmarshal(rawBody, &event); err != nil {
		return nil, fmt.Errorf("railbridge: unmarshal event: %w", err)
	}
	_ = json.Unmarshal(rawBody, &event.Data)

	return &event, nil
}
