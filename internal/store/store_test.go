package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_SQLiteMigrations(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_railbridge.db")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := Open(ctx, "sqlite://"+dbPath+"?_journal_mode=WAL")
	if err != nil {
		t.Fatalf("failed to open sqlite store: %v", err)
	}
	defer db.Close()

	if err := db.Ready(ctx); err != nil {
		t.Fatalf("store readiness failed: %v", err)
	}

	// Verify all 7 core tables and schema_migrations exist in SQLite
	tables := []string{
		"schema_migrations",
		"tenants",
		"payment_sessions",
		"message_logs",
		"email_aliases",
		"devices",
		"mfs_unmatched_inbox",
		"outbox_jobs",
	}

	for _, tbl := range tables {
		var count int
		err := db.QueryRowContext(ctx, "SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&count)
		if err != nil {
			t.Fatalf("query sqlite_master for table %s: %v", tbl, err)
		}
		if count != 1 {
			t.Errorf("expected table %q to exist after migrations, count=%d", tbl, count)
		}
	}

	// Verify file was written to disk
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatalf("expected sqlite database file to exist on disk at %s", dbPath)
	}
}
