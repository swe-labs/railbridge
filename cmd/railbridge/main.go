package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/swe-labs/railbridge/internal/api"
	"github.com/swe-labs/railbridge/internal/config"
	"github.com/swe-labs/railbridge/internal/crypto"
	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/outbox"
	"github.com/swe-labs/railbridge/internal/store"
)

func main() {
	// CLI Flags
	devMode := flag.Bool("dev", false, "Run in local zero-config sandbox mode with SQLite WAL")
	role := flag.String("role", "all", "Service execution role: all | api | worker")
	flag.Parse()

	// Initialize structured logging (slog)
	logHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	slog.SetDefault(slog.New(logHandler))

	slog.Info("Starting Railbridge Engine",
		"version", "v1.0.0",
		"go_version", "1.27.4",
		"dev_mode", *devMode,
		"role", *role,
	)

	// Load and validate environment configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("Configuration failure", "error", err)
		os.Exit(1)
	}

	if *devMode {
		cfg.Env = "development"
		slog.Info("Development sandbox enabled. Using SQLite WAL mode.")
	}

	// Initialize database storage and embedded schema migrations
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbStore, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		slog.Error("Database initialization failed", "error", err)
		os.Exit(1)
	}
	defer dbStore.Close()

	slog.Info("Database connection established", "dialect", dbStore.Dialect)

	if *devMode {
		seedSandboxData(ctx, dbStore)
	}

	// Build HTTP server with Go 1.27 net/http ServeMux and production timeouts
	server := api.NewServer(cfg, dbStore, nil, nil)

	// Launch Outbox Background Worker if role is 'all' or 'worker'
	if *role == "all" || *role == "worker" {
		outboxWorker := outbox.NewWorker(dbStore, cfg.MasterEncryptionKey)
		go outboxWorker.Start(ctx)
	}

	httpServer := &http.Server{
		Addr:              cfg.Address(),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Channel to catch termination signals (SIGINT, SIGTERM)
	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, os.Interrupt, syscall.SIGTERM)

	// Start HTTP listener in background goroutine
	go func() {
		slog.Info("Railbridge HTTP listener active", "address", cfg.Address(), "url", cfg.BaseURL)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server crash", "error", err)
			os.Exit(1)
		}
	}()

	// Block until shutdown signal is received
	sig := <-stopChan
	slog.Info("Termination signal received. Initiating graceful shutdown...", "signal", sig.String())

	// Graceful shutdown context with 15-second deadline
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP graceful shutdown error", "error", err)
	}
	server.Close()

	slog.Info("Railbridge process halted cleanly.")
}

func seedSandboxData(ctx context.Context, db *store.DB) {
	sandboxKey := "rb_test_sandbox_secret_key_12345"
	keyHash := crypto.HashSHA256([]byte(sandboxKey))

	// Check if already seeded
	_, err := db.GetTenantByAPIKeyHash(ctx, keyHash)
	if err == nil {
		slog.Info("Sandbox credentials active",
			"api_key", sandboxKey,
			"device_token", "rb_dev_sandbox_device_token_12345",
		)
		return
	}

	tenant := &models.Tenant{
		ID:           "sandbox-tenant-001",
		Name:         "SWE Labs Sandbox Tenant",
		Environment:  models.EnvTest,
		APIKeyPrefix: "rb_test_",
		APIKeyHash:   keyHash,
		WebhookURL:   "http://localhost:8080/_mock_webhook",
		RateLimitRPM: 1000,
		IsActive:     true,
	}
	if err := db.CreateTenant(ctx, tenant); err != nil {
		slog.Warn("Failed to seed sandbox tenant", "error", err)
		return
	}

	rawDeviceToken := "rb_dev_sandbox_device_token_12345"
	devHash := crypto.HashSHA256([]byte(rawDeviceToken))
	device := &models.Device{
		ID:                    "sandbox-device-001",
		TenantID:              tenant.ID,
		DeviceName:            "Sandbox Android SIM Bridge",
		DeviceTokenHash:       devHash,
		DeviceSecretEncrypted: []byte("mock_device_secret"),
		PhoneNumbers:          []string{"+8801812345678", "+8801712345678"},
		SupportedChannels:     []string{"bkash", "nagad", "rocket", "sms"},
		Status:                "online",
	}
	if err := db.RegisterDevice(ctx, device); err != nil {
		slog.Warn("Failed to seed sandbox device", "error", err)
		return
	}

	slog.Info("Default sandbox credentials provisioned",
		"tenant_id", tenant.ID,
		"api_key", sandboxKey,
		"device_token", rawDeviceToken,
	)
}
