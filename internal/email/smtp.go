package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// SMTPDriver sends email via standard SMTP (e.g., Alwaysdata SMTP, Postmark, Brevo).
type SMTPDriver struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

func NewSMTPDriver(host string, port int, user, pass, from string) *SMTPDriver {
	if from == "" {
		from = user
	}
	return &SMTPDriver{
		Host:     host,
		Port:     port,
		Username: user,
		Password: pass,
		From:     from,
	}
}

func (s *SMTPDriver) Name() string {
	return "smtp"
}

func (s *SMTPDriver) SendEmail(ctx context.Context, to, subject, htmlBody string) (string, error) {
	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)

	// Build MIME payload
	headers := make(map[string]string)
	headers["From"] = s.From
	headers["To"] = to
	headers["Subject"] = subject
	headers["MIME-Version"] = "1.0"
	headers["Content-Type"] = "text/html; charset=UTF-8"
	headers["Date"] = time.Now().Format(time.RFC1123Z)

	var messageBuilder strings.Builder
	for k, v := range headers {
		messageBuilder.WriteString(fmt.Sprintf("%s: %s\r\n", k, v))
	}
	messageBuilder.WriteString("\r\n")
	messageBuilder.WriteString(htmlBody)

	msgBytes := []byte(messageBuilder.String())

	// Connect with dialer obeying context deadline
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return "", fmt.Errorf("dial smtp %s: %w", addr, err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return "", fmt.Errorf("smtp client init: %w", err)
	}
	defer client.Quit()

	// Try STARTTLS if supported
	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsConfig := &tls.Config{
			ServerName: s.Host,
			MinVersion: tls.VersionTLS12,
		}
		if err = client.StartTLS(tlsConfig); err != nil {
			return "", fmt.Errorf("starttls: %w", err)
		}
	}

	if s.Username != "" && s.Password != "" {
		auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)
		if err = client.Auth(auth); err != nil {
			return "", fmt.Errorf("smtp auth: %w", err)
		}
	}

	if err = client.Mail(s.From); err != nil {
		return "", fmt.Errorf("smtp mail from: %w", err)
	}
	if err = client.Rcpt(to); err != nil {
		return "", fmt.Errorf("smtp rcpt to: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return "", fmt.Errorf("smtp data writer: %w", err)
	}
	_, err = w.Write(msgBytes)
	if err != nil {
		_ = w.Close()
		return "", fmt.Errorf("smtp write message: %w", err)
	}
	if err = w.Close(); err != nil {
		return "", fmt.Errorf("smtp close data: %w", err)
	}

	return fmt.Sprintf("smtp_%d", time.Now().UnixNano()), nil
}

// MockEmailDriver is used for testing and sandboxed local preview.
type MockEmailDriver struct {
	SentEmails []map[string]string
	ShouldFail bool
}

func (m *MockEmailDriver) Name() string {
	return "mock_email"
}

func (m *MockEmailDriver) SendEmail(ctx context.Context, to, subject, htmlBody string) (string, error) {
	if m.ShouldFail {
		return "", fmt.Errorf("simulated email provider failure")
	}
	m.SentEmails = append(m.SentEmails, map[string]string{
		"to":        to,
		"subject":   subject,
		"html_body": htmlBody,
	})
	return fmt.Sprintf("mock_msg_%d", time.Now().UnixNano()), nil
}
