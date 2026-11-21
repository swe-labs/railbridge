package sms

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GreenwebDriver implements Driver for the Greenweb Bangladesh SMS gateway.
type GreenwebDriver struct {
	APIToken   string
	HTTPClient *http.Client
}

func NewGreenwebDriver(token string) *GreenwebDriver {
	return &GreenwebDriver{
		APIToken: token,
		HTTPClient: &http.Client{
			Timeout: 8 * time.Second,
		},
	}
}

func (g *GreenwebDriver) Name() string {
	return "greenweb"
}

func (g *GreenwebDriver) SendSMS(ctx context.Context, to, message string) (string, error) {
	if g.APIToken == "" {
		return "", fmt.Errorf("greenweb: api token not configured")
	}

	formData := url.Values{
		"token": {g.APIToken},
		"to":    {to},
		"msg":   {message},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.greenweb.com.bd/api.php", strings.NewReader(formData.Encode()))
	if err != nil {
		return "", fmt.Errorf("create greenweb request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := g.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrGatewayTimeout, err)
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	respStr := string(bodyBytes)

	if strings.Contains(respStr, "BALANCE") || strings.Contains(respStr, "LOW_CREDIT") {
		return "", ErrBalanceExhausted
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || strings.Contains(respStr, "Error") {
		return "", fmt.Errorf("greenweb error response: %s", respStr)
	}

	return fmt.Sprintf("gw_%d", time.Now().UnixNano()), nil
}

// MockDriver is a deterministic driver used for testing and fallback routing.
type MockDriver struct {
	DriverName string
	ShouldFail bool
	ErrToThrow error
}

func (m *MockDriver) Name() string {
	return m.DriverName
}

func (m *MockDriver) SendSMS(ctx context.Context, to, message string) (string, error) {
	if m.ShouldFail {
		if m.ErrToThrow != nil {
			return "", m.ErrToThrow
		}
		return "", ErrGatewayTimeout
	}
	return fmt.Sprintf("mock_%s_%d", m.DriverName, time.Now().UnixNano()), nil
}
