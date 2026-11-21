package config

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds validated application runtime configuration.
type Config struct {
	Env                 string
	BaseURL             string
	Host                string
	Port                int
	DatabaseURL         string
	MasterEncryptionKey []byte
	EmailDriver         string
	SMTPHost            string
	SMTPPort            int
	SMTPUser            string
	SMTPPass            string
	SMSPrimaryDriver    string
	SMSFallbackDriver   string
	AndroidBridgeSecret string
	GreenwebAPIToken    string
}

// Load reads and validates configuration from environment variables.
// It gracefully accommodates Alwaysdata's injected $IP and $PORT variables.
func Load() (*Config, error) {
	env := getEnv("APP_ENV", "development")
	baseURL := getEnv("APP_BASE_URL", "http://localhost:8080")

	// Alwaysdata injects $IP and $PORT directly into user program processes
	host := getEnv("IP", "0.0.0.0")
	portStr := getEnv("PORT", "8080")
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port value %q: %w", portStr, err)
	}

	dbURL := getEnv("DATABASE_URL", "sqlite://./data/railbridge.db?_journal_mode=WAL")

	keyHex := os.Getenv("MASTER_ENCRYPTION_KEY")
	if keyHex == "" && env == "development" {
		// Deterministic 32-byte key for local development only
		keyHex = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	}
	if len(keyHex) != 64 {
		return nil, errors.New("MASTER_ENCRYPTION_KEY must be a 64-character hex string (32 bytes)")
	}
	masterKey, err := hex.DecodeString(keyHex)
	if err != nil {
		return nil, fmt.Errorf("decode MASTER_ENCRYPTION_KEY: %w", err)
	}

	smtpPort, _ := strconv.Atoi(getEnv("SMTP_PORT", "587"))

	cfg := &Config{
		Env:                 env,
		BaseURL:             strings.TrimRight(baseURL, "/"),
		Host:                host,
		Port:                port,
		DatabaseURL:         dbURL,
		MasterEncryptionKey: masterKey,
		EmailDriver:         getEnv("EMAIL_DRIVER", "smtp"),
		SMTPHost:            getEnv("SMTP_HOST", "smtp-railbridge.alwaysdata.net"),
		SMTPPort:            smtpPort,
		SMTPUser:            getEnv("SMTP_USER", ""),
		SMTPPass:            getEnv("SMTP_PASS", ""),
		SMSPrimaryDriver:    getEnv("SMS_PRIMARY_DRIVER", "android_bridge"),
		SMSFallbackDriver:   getEnv("SMS_FALLBACK_DRIVER", "greenweb"),
		AndroidBridgeSecret: getEnv("ANDROID_BRIDGE_SECRET", ""),
		GreenwebAPIToken:    getEnv("GREENWEB_API_TOKEN", ""),
	}

	return cfg, nil
}

// Address returns the TCP network address formatted for net.Listen.
func (c *Config) Address() string {
	return fmt.Sprintf("%s:%d", c.Host, c.Port)
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
