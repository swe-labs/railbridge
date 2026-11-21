package railbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is the entry point for interacting with the Railbridge REST API.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client

	Payments *PaymentsService
	SMS      *SMSService
	Email    *EmailService
}

// Option configures client settings.
type Option func(*Client)

// WithBaseURL overrides the default Railbridge API host.
func WithBaseURL(url string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(url, "/")
	}
}

// WithTimeout sets a custom HTTP client request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.httpClient.Timeout = d
	}
}

// WithHTTPClient sets a custom http.Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.httpClient = hc
	}
}

// NewClient initializes a new Railbridge API client with an API key (rb_live_... or rb_test_...).
func NewClient(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		baseURL: "https://railbridge.alwaysdata.net",
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
	for _, opt := range opts {
		opt(c)
	}

	c.Payments = &PaymentsService{client: c}
	c.SMS = &SMSService{client: c}
	c.Email = &EmailService{client: c}

	return c
}

func (c *Client) request(ctx context.Context, method, path string, idempotencyKey string, bodyIn, bodyOut any) error {
	var bodyReader io.Reader
	if bodyIn != nil {
		b, err := json.Marshal(bodyIn)
		if err != nil {
			return fmt.Errorf("railbridge: marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	reqURL := fmt.Sprintf("%s%s", c.baseURL, path)
	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return fmt.Errorf("railbridge: create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Railbridge-Go-SDK/v1.0.0")

	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("railbridge: execute HTTP request: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("railbridge: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("railbridge error (HTTP %d): %s", resp.StatusCode, string(respBytes))
	}

	if bodyOut != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, bodyOut); err != nil {
			return fmt.Errorf("railbridge: unmarshal response JSON: %w", err)
		}
	}

	return nil
}
