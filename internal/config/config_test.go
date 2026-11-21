package config

import (
	"os"
	"testing"
)

func TestConfigLoad_Defaults(t *testing.T) {
	// Clear environment variables that might interfere
	os.Unsetenv("APP_ENV")
	os.Unsetenv("PORT")
	os.Unsetenv("IP")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("expected config to load defaults successfully, got error: %v", err)
	}

	if cfg.Env != "development" {
		t.Errorf("expected Env 'development', got %q", cfg.Env)
	}
	if cfg.Port != 8080 {
		t.Errorf("expected default Port 8080, got %d", cfg.Port)
	}
	if cfg.Address() != "0.0.0.0:8080" {
		t.Errorf("expected Address '0.0.0.0:8080', got %q", cfg.Address())
	}
	if len(cfg.MasterEncryptionKey) != 32 {
		t.Errorf("expected 32-byte master key, got %d bytes", len(cfg.MasterEncryptionKey))
	}
}

func TestConfigLoad_AlwaysdataBinding(t *testing.T) {
	os.Setenv("IP", "127.0.0.1")
	os.Setenv("PORT", "8100")
	defer func() {
		os.Unsetenv("IP")
		os.Unsetenv("PORT")
	}()

	cfg, err := Load()
	if err != nil {
		t.Fatalf("failed to load alwaysdata injected env: %v", err)
	}

	if cfg.Address() != "127.0.0.1:8100" {
		t.Errorf("expected '127.0.0.1:8100', got %q", cfg.Address())
	}
}
