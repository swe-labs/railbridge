package sms

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrInvalidPhoneNumber = errors.New("sms: invalid destination phone number")
	ErrBalanceExhausted    = errors.New("sms: provider balance exhausted")
	ErrGatewayTimeout      = errors.New("sms: upstream gateway timeout")
)

// Driver defines the contract for outbound SMS dispatches.
type Driver interface {
	Name() string
	SendSMS(ctx context.Context, to, message string) (providerMsgID string, err error)
}

// NormalizeE164 validates and standardizes a phone number to standard E.164 (+8801...).
func NormalizeE164(raw string) (string, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ReplaceAll(cleaned, " ", "")

	if strings.HasPrefix(cleaned, "+8801") && len(cleaned) == 14 {
		return cleaned, nil
	}
	if strings.HasPrefix(cleaned, "8801") && len(cleaned) == 13 {
		return "+" + cleaned, nil
	}
	if strings.HasPrefix(cleaned, "01") && len(cleaned) == 11 {
		return "+88" + cleaned, nil
	}

	return "", fmt.Errorf("%w: %q (must be standard Bangladesh 11-digit or E.164 number)", ErrInvalidPhoneNumber, raw)
}
