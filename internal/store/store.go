package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/swe-labs/railbridge/migrations"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// DB wraps the SQL database pool and dialect metadata.
type DB struct {
	*sql.DB
	Dialect string
}

// Open initializes a database connection, applies pooling parameters, and runs embedded migrations.
func Open(ctx context.Context, rawURL string) (*DB, error) {
	var driverName, dataSource string
	var dialect string

	if strings.HasPrefix(rawURL, "sqlite://") || strings.HasPrefix(rawURL, "file:") || strings.HasSuffix(rawURL, ".db") {
		driverName = "sqlite"
		dialect = "sqlite"
		path := strings.TrimPrefix(rawURL, "sqlite://")
		// Clean query parameters for file creation
		filePath := path
		if idx := strings.Index(filePath, "?"); idx != -1 {
			filePath = filePath[:idx]
		}
		dir := filepath.Dir(filePath)
		if dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0755); err != nil {
				return nil, fmt.Errorf("create sqlite directory %q: %w", dir, err)
			}
		}
		dataSource = path
	} else if strings.HasPrefix(rawURL, "postgres://") || strings.HasPrefix(rawURL, "postgresql://") {
		driverName = "pgx"
		dialect = "postgres"
		dataSource = rawURL
	} else {
		return nil, fmt.Errorf("unsupported database scheme in URL: %q", rawURL)
	}

	conn, err := sql.Open(driverName, dataSource)
	if err != nil {
		return nil, fmt.Errorf("open database driver %q: %w", driverName, err)
	}

	// Production connection pool tuning
	if dialect == "sqlite" {
		// SQLite handles concurrency via single writer in WAL mode
		conn.SetMaxOpenConns(1)
		conn.SetMaxIdleConns(1)
		conn.SetConnMaxLifetime(0)
	} else {
		conn.SetMaxOpenConns(25)
		conn.SetMaxIdleConns(10)
		conn.SetConnMaxLifetime(30 * time.Minute)
		conn.SetConnMaxIdleTime(5 * time.Minute)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := conn.PingContext(pingCtx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping database (%s): %w", dialect, err)
	}

	db := &DB{DB: conn, Dialect: dialect}

	// Execute embedded migrations on startup
	if err := db.Migrate(ctx); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("run schema migrations: %w", err)
	}

	return db, nil
}

// Migrate applies all unapplied embedded SQL migrations.
func (db *DB) Migrate(ctx context.Context) error {
	// Create migration tracking table
	trackDDL := `CREATE TABLE IF NOT EXISTS schema_migrations (
		version VARCHAR(255) PRIMARY KEY,
		applied_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.ExecContext(ctx, trackDDL); err != nil {
		return fmt.Errorf("create schema_migrations table: %w", err)
	}

	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("read embedded migrations dir: %w", err)
	}

	var filenames []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			filenames = append(filenames, entry.Name())
		}
	}
	sort.Strings(filenames)

	for _, filename := range filenames {
		var applied int
		checkQuery := `SELECT COUNT(1) FROM schema_migrations WHERE version = $1`
		if db.Dialect == "sqlite" {
			checkQuery = `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`
		}
		if err := db.QueryRowContext(ctx, checkQuery, filename).Scan(&applied); err != nil {
			return fmt.Errorf("check migration status for %s: %w", filename, err)
		}
		if applied > 0 {
			continue
		}

		slog.Info("Applying schema migration", "version", filename, "dialect", db.Dialect)
		content, err := migrations.FS.ReadFile(filename)
		if err != nil {
			return fmt.Errorf("read migration content %s: %w", filename, err)
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration tx: %w", err)
		}

		migrationSQL := string(content)
		if db.Dialect == "postgres" {
			migrationSQL = strings.ReplaceAll(migrationSQL, " BLOB", " BYTEA")
		}

		if _, err := tx.ExecContext(ctx, migrationSQL); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("execute migration %s: %w", filename, err)
		}

		recordQuery := `INSERT INTO schema_migrations (version) VALUES ($1)`
		if db.Dialect == "sqlite" {
			recordQuery = `INSERT INTO schema_migrations (version) VALUES (?)`
		}
		if _, err := tx.ExecContext(ctx, recordQuery, filename); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", filename, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", filename, err)
		}
		slog.Info("Migration applied successfully", "version", filename)
	}

	return nil
}

// Ready verifies database liveness and basic query readiness.
func (db *DB) Ready(ctx context.Context) error {
	if db == nil || db.DB == nil {
		return errors.New("database connection not initialized")
	}
	return db.PingContext(ctx)
}
