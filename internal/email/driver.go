package email

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

var (
	ErrInvalidEmailAddress = errors.New("email: invalid email address")
	ErrAliasNotFound       = errors.New("email: no active routing alias configured for recipient")
)

// Driver specifies the contract for outbound transactional email dispatchers.
type Driver interface {
	Name() string
	SendEmail(ctx context.Context, to, subject, htmlBody string) (providerMsgID string, err error)
}

// ValidateEmailAddress verifies that an address adheres to standard RFC 5322 format.
func ValidateEmailAddress(address string) (string, error) {
	trimmed := strings.TrimSpace(address)
	addr, err := mail.ParseAddress(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %q", ErrInvalidEmailAddress, address)
	}
	return addr.Address, nil
}
