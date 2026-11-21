package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/swe-labs/railbridge/internal/models"
)

var (
	ErrNotFound       = errors.New("store: record not found")
	ErrConflict       = errors.New("store: unique constraint violation or conflict")
	ErrIdempotencyHit = errors.New("store: idempotency key matched existing session")
)

// ScannableTime implements sql.Scanner for cross-dialect timestamp compatibility (PostgreSQL & SQLite).
type ScannableTime struct {
	time.Time
}

func (st *ScannableTime) Scan(src any) error {
	if src == nil {
		st.Time = time.Time{}
		return nil
	}
	t, err := parseTime(src)
	if err != nil {
		return err
	}
	st.Time = t
	return nil
}

// ScannableNullTime implements sql.Scanner for nullable timestamps across dialects.
type ScannableNullTime struct {
	Time  time.Time
	Valid bool
}

func (snt *ScannableNullTime) Scan(src any) error {
	if src == nil {
		snt.Valid = false
		return nil
	}
	t, err := parseTime(src)
	if err != nil {
		return err
	}
	snt.Time = t
	snt.Valid = true
	return nil
}

func parseTime(val any) (time.Time, error) {
	switch v := val.(type) {
	case time.Time:
		return v, nil
	case string:
		str := strings.TrimSpace(v)
		if idx := strings.Index(str, " m="); idx != -1 {
			str = strings.TrimSpace(str[:idx])
		}
		layouts := []string{
			"2006-01-02 15:04:05.999999999 -0700 -0700",
			"2006-01-02 15:04:05.999999999 -0700 MST",
			"2006-01-02 15:04:05.999999999 -0700 -07",
			"2006-01-02 15:04:05.999999999 -0700",
			"2006-01-02 15:04:05.999999999",
			time.RFC3339Nano,
			time.RFC3339,
			"2006-01-02 15:04:05.999999999-07:00",
			"2006-01-02 15:04:05 -0700 -0700",
			"2006-01-02 15:04:05 -0700 MST",
			"2006-01-02 15:04:05 -0700 -07",
			"2006-01-02 15:04:05 -0700",
			"2006-01-02 15:04:05-07:00",
			"2006-01-02 15:04:05",
		}
		for _, l := range layouts {
			if t, err := time.Parse(l, str); err == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("unable to parse time string %q", v)
	case []byte:
		return parseTime(string(v))
	case nil:
		return time.Time{}, nil
	default:
		return time.Time{}, fmt.Errorf("unsupported time scan type %T", val)
	}
}

// GenerateUUID generates a random 36-character UUIDv4 string.
func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// CreateTenant inserts a new tenant record.
func (db *DB) CreateTenant(ctx context.Context, t *models.Tenant) error {
	if t.ID == "" {
		t.ID = GenerateUUID()
	}
	now := time.Now().UTC()
	t.CreatedAt = now
	t.UpdatedAt = now

	brandingJSON, _ := json.Marshal(t.BrandingConfig)
	query := `INSERT INTO tenants (
		id, name, environment, api_key_prefix, api_key_hash, webhook_url, 
		webhook_secret_encrypted, branding_config, rate_limit_rpm, is_active, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if db.Dialect == "postgres" {
		query = `INSERT INTO tenants (
			id, name, environment, api_key_prefix, api_key_hash, webhook_url, 
			webhook_secret_encrypted, branding_config, rate_limit_rpm, is_active, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	}

	_, err := db.ExecContext(ctx, query,
		t.ID, t.Name, string(t.Environment), t.APIKeyPrefix, t.APIKeyHash,
		t.WebhookURL, t.WebhookSecretEncrypted, string(brandingJSON),
		t.RateLimitRPM, t.IsActive, t.CreatedAt, t.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create tenant: %w", err)
	}
	return nil
}

// GetTenantByAPIKeyHash looks up a tenant using the SHA-256 hash of their API key.
func (db *DB) GetTenantByAPIKeyHash(ctx context.Context, hash []byte) (*models.Tenant, error) {
	query := `SELECT id, name, environment, api_key_prefix, webhook_url, 
		webhook_secret_encrypted, branding_config, rate_limit_rpm, is_active, created_at, updated_at 
		FROM tenants WHERE api_key_hash = ? AND is_active = true`
	if db.Dialect == "postgres" {
		query = `SELECT id, name, environment, api_key_prefix, webhook_url, 
			webhook_secret_encrypted, branding_config, rate_limit_rpm, is_active, created_at, updated_at 
			FROM tenants WHERE api_key_hash = $1 AND is_active = true`
	}

	var t models.Tenant
	var brandingStr string
	var webhookURL sql.NullString
	var createdAt, updatedAt ScannableTime
	t.APIKeyHash = hash

	err := db.QueryRowContext(ctx, query, hash).Scan(
		&t.ID, &t.Name, &t.Environment, &t.APIKeyPrefix, &webhookURL,
		&t.WebhookSecretEncrypted, &brandingStr, &t.RateLimitRPM,
		&t.IsActive, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query tenant by key: %w", err)
	}

	if webhookURL.Valid {
		t.WebhookURL = webhookURL.String
	}
	t.CreatedAt = createdAt.Time
	t.UpdatedAt = updatedAt.Time
	t.BrandingConfig = json.RawMessage(brandingStr)
	return &t, nil
}

// CreatePaymentSession inserts a new payment session.
func (db *DB) CreatePaymentSession(ctx context.Context, s *models.PaymentSession) error {
	if s.ID == "" {
		s.ID = GenerateUUID()
	}
	now := time.Now().UTC()
	s.CreatedAt = now
	s.UpdatedAt = now
	if s.ExpiresAt.IsZero() {
		s.ExpiresAt = now.Add(30 * time.Minute)
	} else {
		s.ExpiresAt = s.ExpiresAt.UTC().Round(0)
	}

	metadataJSON, _ := json.Marshal(s.Metadata)
	query := `INSERT INTO payment_sessions (
		id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
		status, channel, customer_phone, return_url, webhook_url_override, metadata,
		expires_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if db.Dialect == "postgres" {
		query = `INSERT INTO payment_sessions (
			id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
			status, channel, customer_phone, return_url, webhook_url_override, metadata,
			expires_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`
	}

	_, err := db.ExecContext(ctx, query,
		s.ID, s.TenantID, s.ExternalReferenceID, s.IdempotencyKey, s.AmountMinor, s.Currency,
		string(s.Status), s.Channel, s.CustomerPhone, s.ReturnURL, s.WebhookURLOverride,
		string(metadataJSON), s.ExpiresAt, s.CreatedAt, s.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert payment session: %w", err)
	}
	return nil
}

// GetPaymentSession retrieves a payment session by ID.
func (db *DB) GetPaymentSession(ctx context.Context, tenantID, id string) (*models.PaymentSession, error) {
	query := `SELECT id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
		status, channel, customer_phone, transaction_id, return_url, webhook_url_override, metadata,
		expires_at, verified_at, created_at, updated_at
		FROM payment_sessions WHERE tenant_id = ? AND id = ?`
	if db.Dialect == "postgres" {
		query = `SELECT id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
			status, channel, customer_phone, transaction_id, return_url, webhook_url_override, metadata,
			expires_at, verified_at, created_at, updated_at
			FROM payment_sessions WHERE tenant_id = $1 AND id = $2`
	}

	return db.scanPaymentSession(db.QueryRowContext(ctx, query, tenantID, id))
}

// GetPaymentSessionByID retrieves a payment session by ID across tenants (used for public hosted checkout).
func (db *DB) GetPaymentSessionByID(ctx context.Context, id string) (*models.PaymentSession, error) {
	query := `SELECT id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
		status, channel, customer_phone, transaction_id, return_url, webhook_url_override, metadata,
		expires_at, verified_at, created_at, updated_at
		FROM payment_sessions WHERE id = ?`
	if db.Dialect == "postgres" {
		query = `SELECT id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
			status, channel, customer_phone, transaction_id, return_url, webhook_url_override, metadata,
			expires_at, verified_at, created_at, updated_at
			FROM payment_sessions WHERE id = $1`
	}

	return db.scanPaymentSession(db.QueryRowContext(ctx, query, id))
}

// GetPaymentSessionByIdempotency retrieves a session by tenant ID and idempotency key.
func (db *DB) GetPaymentSessionByIdempotency(ctx context.Context, tenantID, key string) (*models.PaymentSession, error) {
	query := `SELECT id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
		status, channel, customer_phone, transaction_id, return_url, webhook_url_override, metadata,
		expires_at, verified_at, created_at, updated_at
		FROM payment_sessions WHERE tenant_id = ? AND idempotency_key = ?`
	if db.Dialect == "postgres" {
		query = `SELECT id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
			status, channel, customer_phone, transaction_id, return_url, webhook_url_override, metadata,
			expires_at, verified_at, created_at, updated_at
			FROM payment_sessions WHERE tenant_id = $1 AND idempotency_key = $2`
	}

	return db.scanPaymentSession(db.QueryRowContext(ctx, query, tenantID, key))
}

func (db *DB) scanPaymentSession(row *sql.Row) (*models.PaymentSession, error) {
	var s models.PaymentSession
	var statusStr string
	var idempotencyKey, channel, customerPhone, transactionID, webhookOverride, metadataStr sql.NullString
	var expiresAt, createdAt, updatedAt ScannableTime
	var verifiedAt ScannableNullTime

	err := row.Scan(
		&s.ID, &s.TenantID, &s.ExternalReferenceID, &idempotencyKey, &s.AmountMinor, &s.Currency,
		&statusStr, &channel, &customerPhone, &transactionID, &s.ReturnURL, &webhookOverride,
		&metadataStr, &expiresAt, &verifiedAt, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan payment session: %w", err)
	}

	s.Status = models.PaymentStatus(statusStr)
	s.ExpiresAt = expiresAt.Time
	s.CreatedAt = createdAt.Time
	s.UpdatedAt = updatedAt.Time
	if idempotencyKey.Valid {
		s.IdempotencyKey = idempotencyKey.String
	}
	if channel.Valid {
		s.Channel = channel.String
	}
	if customerPhone.Valid {
		s.CustomerPhone = customerPhone.String
	}
	if transactionID.Valid {
		s.TransactionID = transactionID.String
	}
	if webhookOverride.Valid {
		s.WebhookURLOverride = webhookOverride.String
	}
	if metadataStr.Valid {
		s.Metadata = json.RawMessage(metadataStr.String)
	}
	if verifiedAt.Valid {
		s.VerifiedAt = &verifiedAt.Time
	}

	return &s, nil
}

// UpdatePaymentSessionStatus updates status and transaction metadata atomically.
func (db *DB) UpdatePaymentSessionStatus(ctx context.Context, id string, status models.PaymentStatus, channel, trxID, phone string, verifiedAt *time.Time) error {
	now := time.Now().UTC()
	query := `UPDATE payment_sessions SET status = ?, channel = ?, transaction_id = ?, customer_phone = ?, verified_at = ?, updated_at = ? WHERE id = ?`
	if db.Dialect == "postgres" {
		query = `UPDATE payment_sessions SET status = $1, channel = $2, transaction_id = $3, customer_phone = $4, verified_at = $5, updated_at = $6 WHERE id = $7`
	}

	_, err := db.ExecContext(ctx, query, string(status), channel, trxID, phone, verifiedAt, now, id)
	if err != nil {
		return fmt.Errorf("update payment session status: %w", err)
	}
	return nil
}

// RegisterDevice registers an Android SIM Bridge hardware node for a tenant.
func (db *DB) RegisterDevice(ctx context.Context, d *models.Device) error {
	if d.ID == "" {
		d.ID = GenerateUUID()
	}
	now := time.Now().UTC()
	d.CreatedAt = now
	d.UpdatedAt = now

	phonesJSON, _ := json.Marshal(d.PhoneNumbers)
	channelsJSON, _ := json.Marshal(d.SupportedChannels)

	query := `INSERT INTO devices (
		id, tenant_id, device_name, device_token_hash, device_secret_encrypted, phone_numbers, 
		supported_channels, status, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if db.Dialect == "postgres" {
		query = `INSERT INTO devices (
			id, tenant_id, device_name, device_token_hash, device_secret_encrypted, phone_numbers, 
			supported_channels, status, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	}

	_, err := db.ExecContext(ctx, query,
		d.ID, d.TenantID, d.DeviceName, d.DeviceTokenHash, d.DeviceSecretEncrypted,
		string(phonesJSON), string(channelsJSON), d.Status, d.CreatedAt, d.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert device: %w", err)
	}
	return nil
}

// GetDeviceByTokenHash looks up a device by its token hash.
func (db *DB) GetDeviceByTokenHash(ctx context.Context, hash []byte) (*models.Device, error) {
	query := `SELECT id, tenant_id, device_name, device_secret_encrypted, phone_numbers, 
		supported_channels, status, last_heartbeat_at, created_at, updated_at
		FROM devices WHERE device_token_hash = ?`
	if db.Dialect == "postgres" {
		query = `SELECT id, tenant_id, device_name, device_secret_encrypted, phone_numbers, 
			supported_channels, status, last_heartbeat_at, created_at, updated_at
			FROM devices WHERE device_token_hash = $1`
	}

	var d models.Device
	var phonesStr, channelsStr string
	var heartbeat ScannableNullTime
	var createdAt, updatedAt ScannableTime
	d.DeviceTokenHash = hash

	err := db.QueryRowContext(ctx, query, hash).Scan(
		&d.ID, &d.TenantID, &d.DeviceName, &d.DeviceSecretEncrypted,
		&phonesStr, &channelsStr, &d.Status, &heartbeat, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query device by token: %w", err)
	}

	_ = json.Unmarshal([]byte(phonesStr), &d.PhoneNumbers)
	_ = json.Unmarshal([]byte(channelsStr), &d.SupportedChannels)
	d.CreatedAt = createdAt.Time
	d.UpdatedAt = updatedAt.Time
	if heartbeat.Valid {
		d.LastHeartbeatAt = &heartbeat.Time
	}
	return &d, nil
}

// UpdateDeviceHeartbeat records a ping and sets status to online.
func (db *DB) UpdateDeviceHeartbeat(ctx context.Context, deviceID string) error {
	now := time.Now().UTC()
	query := `UPDATE devices SET status = 'online', last_heartbeat_at = ?, updated_at = ? WHERE id = ?`
	if db.Dialect == "postgres" {
		query = `UPDATE devices SET status = 'online', last_heartbeat_at = $1, updated_at = $2 WHERE id = $3`
	}
	_, err := db.ExecContext(ctx, query, now, now, deviceID)
	return err
}

// SaveUnmatchedSMS inserts an incoming SMS into the buffer.
func (db *DB) SaveUnmatchedSMS(ctx context.Context, sms *models.MFSUnmatchedSMS) error {
	if sms.ID == "" {
		sms.ID = GenerateUUID()
	}
	now := time.Now().UTC()
	sms.CreatedAt = now
	if sms.ExpiresAt.IsZero() {
		sms.ExpiresAt = now.Add(30 * time.Minute)
	} else {
		sms.ExpiresAt = sms.ExpiresAt.UTC().Round(0)
	}

	query := `INSERT INTO mfs_unmatched_inbox (
		id, tenant_id, device_id, channel, sender_shortcode, transaction_id, amount_minor,
		sender_phone, raw_sms_body, status, expires_at, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if db.Dialect == "postgres" {
		query = `INSERT INTO mfs_unmatched_inbox (
			id, tenant_id, device_id, channel, sender_shortcode, transaction_id, amount_minor,
			sender_phone, raw_sms_body, status, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	}

	_, err := db.ExecContext(ctx, query,
		sms.ID, sms.TenantID, sms.DeviceID, sms.Channel, sms.SenderShortcode,
		sms.TransactionID, sms.AmountMinor, sms.SenderPhone, sms.RawSMSBody,
		sms.Status, sms.ExpiresAt, sms.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert unmatched sms: %w", err)
	}
	return nil
}

// FindMatchingUnmatchedSMS checks if a buffered SMS exists matching the TrxID and amount.
func (db *DB) FindMatchingUnmatchedSMS(ctx context.Context, tenantID, channel, trxID string, amountMinor int64) (*models.MFSUnmatchedSMS, error) {
	query := `SELECT id, tenant_id, device_id, channel, sender_shortcode, transaction_id, amount_minor,
		sender_phone, raw_sms_body, status, expires_at, created_at
		FROM mfs_unmatched_inbox 
		WHERE tenant_id = ? AND channel = ? AND transaction_id = ? AND amount_minor = ? AND status = 'unmatched' AND expires_at > ?`
	if db.Dialect == "postgres" {
		query = `SELECT id, tenant_id, device_id, channel, sender_shortcode, transaction_id, amount_minor,
			sender_phone, raw_sms_body, status, expires_at, created_at
			FROM mfs_unmatched_inbox 
			WHERE tenant_id = $1 AND channel = $2 AND transaction_id = $3 AND amount_minor = $4 AND status = 'unmatched' AND expires_at > $5`
	}

	var sms models.MFSUnmatchedSMS
	var expiresAt, createdAt ScannableTime
	now := time.Now().UTC()
	err := db.QueryRowContext(ctx, query, tenantID, channel, trxID, amountMinor, now).Scan(
		&sms.ID, &sms.TenantID, &sms.DeviceID, &sms.Channel, &sms.SenderShortcode,
		&sms.TransactionID, &sms.AmountMinor, &sms.SenderPhone, &sms.RawSMSBody,
		&sms.Status, &expiresAt, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query matching unmatched sms: %w", err)
	}
	sms.ExpiresAt = expiresAt.Time
	sms.CreatedAt = createdAt.Time
	return &sms, nil
}

// MarkUnmatchedSMSMatched marks a buffered SMS as matched to a session.
func (db *DB) MarkUnmatchedSMSMatched(ctx context.Context, smsID, sessionID string) error {
	query := `UPDATE mfs_unmatched_inbox SET status = 'matched', matched_session_id = ? WHERE id = ?`
	if db.Dialect == "postgres" {
		query = `UPDATE mfs_unmatched_inbox SET status = 'matched', matched_session_id = $1 WHERE id = $2`
	}
	_, err := db.ExecContext(ctx, query, sessionID, smsID)
	return err
}

// FindPendingSessionForSMS checks if there is a pending session matching incoming SMS credentials.
func (db *DB) FindPendingSessionForSMS(ctx context.Context, tenantID, channel, trxID string, amountMinor int64) (*models.PaymentSession, error) {
	query := `SELECT id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
		status, channel, customer_phone, transaction_id, return_url, webhook_url_override, metadata,
		expires_at, verified_at, created_at, updated_at
		FROM payment_sessions 
		WHERE tenant_id = ? AND channel = ? AND amount_minor = ? AND (transaction_id = ? OR transaction_id IS NULL) AND status IN ('pending', 'processing') AND expires_at > ?
		ORDER BY created_at DESC LIMIT 1`
	if db.Dialect == "postgres" {
		query = `SELECT id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
			status, channel, customer_phone, transaction_id, return_url, webhook_url_override, metadata,
			expires_at, verified_at, created_at, updated_at
			FROM payment_sessions 
			WHERE tenant_id = $1 AND channel = $2 AND amount_minor = $3 AND (transaction_id = $4 OR transaction_id IS NULL) AND status IN ('pending', 'processing') AND expires_at > $5
			ORDER BY created_at DESC LIMIT 1`
	}

	return db.scanPaymentSession(db.QueryRowContext(ctx, query, tenantID, channel, amountMinor, trxID, time.Now().UTC()))
}

// EnqueueOutboxJob inserts a new asynchronous task into the outbox queue.
func (db *DB) EnqueueOutboxJob(ctx context.Context, job *models.OutboxJob) error {
	if job.ID == "" {
		job.ID = GenerateUUID()
	}
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now
	if job.NextRunAt.IsZero() {
		job.NextRunAt = now
	}
	if job.MaxAttempts == 0 {
		job.MaxAttempts = 6
	}
	if job.Status == "" {
		job.Status = "pending"
	}

	query := `INSERT INTO outbox_jobs (
		id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if db.Dialect == "postgres" {
		query = `INSERT INTO outbox_jobs (
			id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	}

	_, err := db.ExecContext(ctx, query,
		job.ID, job.TenantID, job.JobType, string(job.Payload), job.Status,
		job.Attempts, job.MaxAttempts, job.NextRunAt, job.CreatedAt, job.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("enqueue outbox job: %w", err)
	}
	return nil
}

// FetchPendingOutboxJobs polls for ready outbox jobs using row locking in PostgreSQL.
func (db *DB) FetchPendingOutboxJobs(ctx context.Context, limit int) ([]*models.OutboxJob, error) {
	query := `SELECT id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, last_error, created_at, updated_at
		FROM outbox_jobs 
		WHERE status = 'pending' AND next_run_at <= ?
		ORDER BY next_run_at ASC LIMIT ?`
	if db.Dialect == "postgres" {
		query = `SELECT id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, last_error, created_at, updated_at
			FROM outbox_jobs 
			WHERE status = 'pending' AND next_run_at <= $1
			ORDER BY next_run_at ASC LIMIT $2
			FOR UPDATE SKIP LOCKED`
	}

	now := time.Now().UTC()
	rows, err := db.QueryContext(ctx, query, now, limit)
	
	if err != nil {
		return nil, fmt.Errorf("fetch pending outbox jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*models.OutboxJob
	for rows.Next() {
		var j models.OutboxJob
		var payloadStr string
		var lastErr sql.NullString
		var nextRunAt, createdAt, updatedAt ScannableTime

		if err := rows.Scan(
			&j.ID, &j.TenantID, &j.JobType, &payloadStr, &j.Status,
			&j.Attempts, &j.MaxAttempts, &nextRunAt, &lastErr,
			&createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan outbox job: %w", err)
		}
		j.Payload = json.RawMessage(payloadStr)
		j.NextRunAt = nextRunAt.Time
		j.CreatedAt = createdAt.Time
		j.UpdatedAt = updatedAt.Time
		if lastErr.Valid {
			j.LastError = &lastErr.String
		}
		jobs = append(jobs, &j)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox jobs: %w", err)
	}

	return jobs, nil
}

// UpdateOutboxJob updates the status, next retry time, and attempt counter for an outbox task.
func (db *DB) UpdateOutboxJob(ctx context.Context, jobID, status string, attempts int, nextRunAt time.Time, lastErr *string) error {
	now := time.Now().UTC()
	query := `UPDATE outbox_jobs SET status = ?, attempts = ?, next_run_at = ?, last_error = ?, updated_at = ? WHERE id = ?`
	if db.Dialect == "postgres" {
		query = `UPDATE outbox_jobs SET status = $1, attempts = $2, next_run_at = $3, last_error = $4, updated_at = $5 WHERE id = $6`
	}
	_, err := db.ExecContext(ctx, query, status, attempts, nextRunAt, lastErr, now, jobID)
	return err
}

// GetTenantWebhookSecret retrieves and decrypts the tenant's webhook signing secret.
func (db *DB) GetTenantWebhookSecret(ctx context.Context, tenantID string) (string, []byte, error) {
	query := `SELECT webhook_url, webhook_secret_encrypted FROM tenants WHERE id = ?`
	if db.Dialect == "postgres" {
		query = `SELECT webhook_url, webhook_secret_encrypted FROM tenants WHERE id = $1`
	}
	var url sql.NullString
	var encryptedSecret []byte
	err := db.QueryRowContext(ctx, query, tenantID).Scan(&url, &encryptedSecret)
	if err != nil {
		return "", nil, err
	}
	return url.String, encryptedSecret, nil
}

// CreateMessageLog inserts an audit log entry for outbound SMS or email.
func (db *DB) CreateMessageLog(ctx context.Context, log *models.MessageLog) error {
	if log.ID == "" {
		log.ID = GenerateUUID()
	}
	now := time.Now().UTC()
	log.CreatedAt = now

	query := `INSERT INTO message_logs (
		id, tenant_id, idempotency_key, rail, recipient, subject, template_id, 
		provider_used, provider_msg_id, status, attempts, last_error, created_at, sent_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if db.Dialect == "postgres" {
		query = `INSERT INTO message_logs (
			id, tenant_id, idempotency_key, rail, recipient, subject, template_id, 
			provider_used, provider_msg_id, status, attempts, last_error, created_at, sent_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`
	}

	_, err := db.ExecContext(ctx, query,
		log.ID, log.TenantID, log.IdempotencyKey, log.Rail, log.Recipient,
		log.Subject, log.TemplateID, log.ProviderUsed, log.ProviderMsgID,
		log.Status, log.Attempts, log.LastError, log.CreatedAt, log.SentAt,
	)
	if err != nil {
		return fmt.Errorf("insert message log: %w", err)
	}
	return nil
}

// GetMessageLogByIdempotency checks if a message with the given idempotency key was already recorded.
func (db *DB) GetMessageLogByIdempotency(ctx context.Context, tenantID, rail, key string) (*models.MessageLog, error) {
	query := `SELECT id, tenant_id, idempotency_key, rail, recipient, subject, template_id,
		provider_used, provider_msg_id, status, attempts, last_error, created_at, sent_at
		FROM message_logs WHERE tenant_id = ? AND rail = ? AND idempotency_key = ?`
	if db.Dialect == "postgres" {
		query = `SELECT id, tenant_id, idempotency_key, rail, recipient, subject, template_id,
			provider_used, provider_msg_id, status, attempts, last_error, created_at, sent_at
			FROM message_logs WHERE tenant_id = $1 AND rail = $2 AND idempotency_key = $3`
	}

	var m models.MessageLog
	var keyStr, subj, tmpl, prov, provMsg, lastErr sql.NullString
	var createdAt ScannableTime
	var sentAt ScannableNullTime

	err := db.QueryRowContext(ctx, query, tenantID, rail, key).Scan(
		&m.ID, &m.TenantID, &keyStr, &m.Rail, &m.Recipient, &subj, &tmpl,
		&prov, &provMsg, &m.Status, &m.Attempts, &lastErr, &createdAt, &sentAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query message log by idempotency: %w", err)
	}

	m.CreatedAt = createdAt.Time
	if keyStr.Valid {
		m.IdempotencyKey = keyStr.String
	}
	if subj.Valid {
		m.Subject = subj.String
	}
	if tmpl.Valid {
		m.TemplateID = tmpl.String
	}
	if prov.Valid {
		m.ProviderUsed = prov.String
	}
	if provMsg.Valid {
		m.ProviderMsgID = provMsg.String
	}
	if lastErr.Valid {
		m.LastError = &lastErr.String
	}
	if sentAt.Valid {
		m.SentAt = &sentAt.Time
	}

	return &m, nil
}

// CreateEmailAlias registers or updates an incoming email forwarding mapping.
func (db *DB) CreateEmailAlias(ctx context.Context, a *models.EmailAlias) error {
	if a.ID == "" {
		a.ID = GenerateUUID()
	}
	now := time.Now().UTC()
	a.CreatedAt = now

	query := `INSERT INTO email_aliases (id, tenant_id, source_address, target_address, is_active, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	if db.Dialect == "postgres" {
		query = `INSERT INTO email_aliases (id, tenant_id, source_address, target_address, is_active, created_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (source_address) DO UPDATE SET target_address = EXCLUDED.target_address, is_active = EXCLUDED.is_active`
	}

	_, err := db.ExecContext(ctx, query, a.ID, a.TenantID, a.SourceAddress, a.TargetAddress, a.IsActive, a.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert email alias: %w", err)
	}
	return nil
}

// GetEmailAlias looks up the active forwarding destination for a virtual custom domain email.
func (db *DB) GetEmailAlias(ctx context.Context, sourceAddress string) (*models.EmailAlias, error) {
	query := `SELECT id, tenant_id, source_address, target_address, is_active, created_at
		FROM email_aliases WHERE source_address = ? AND is_active = true`
	if db.Dialect == "postgres" {
		query = `SELECT id, tenant_id, source_address, target_address, is_active, created_at
			FROM email_aliases WHERE source_address = $1 AND is_active = true`
	}

	var a models.EmailAlias
	var createdAt ScannableTime
	err := db.QueryRowContext(ctx, query, sourceAddress).Scan(
		&a.ID, &a.TenantID, &a.SourceAddress, &a.TargetAddress, &a.IsActive, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query email alias: %w", err)
	}
	a.CreatedAt = createdAt.Time
	return &a, nil
}

// GenerateRandomHex generates a secure random hex string of n bytes.
func GenerateRandomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// AtomicReconcileCustomerTrxID executes atomic verification of customer-entered TrxID against buffered MFS SMS.
// It wraps payment session locking, SMS buffer claiming, and webhook outbox queuing in a single atomic SQL transaction.
func (db *DB) AtomicReconcileCustomerTrxID(ctx context.Context, tenantID, sessionID, channel, trxID, phone string) (*models.PaymentSession, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin atomic reconcile tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// 1. Fetch and lock payment session
	sessionQuery := `SELECT id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
		status, channel, customer_phone, transaction_id, return_url, webhook_url_override, metadata,
		expires_at, verified_at, created_at, updated_at
		FROM payment_sessions WHERE id = ? AND tenant_id = ?`
	if db.Dialect == "postgres" {
		sessionQuery = `SELECT id, tenant_id, external_reference_id, idempotency_key, amount_minor, currency,
			status, channel, customer_phone, transaction_id, return_url, webhook_url_override, metadata,
			expires_at, verified_at, created_at, updated_at
			FROM payment_sessions WHERE id = $1 AND tenant_id = $2 FOR UPDATE`
	}

	var s models.PaymentSession
	var statusStr string
	var idempKey, ch, custPhone, txID, whOverride, metaStr sql.NullString
	var expiresAt, createdAt, updatedAt ScannableTime
	var verifiedAt ScannableNullTime

	err = tx.QueryRowContext(ctx, sessionQuery, sessionID, tenantID).Scan(
		&s.ID, &s.TenantID, &s.ExternalReferenceID, &idempKey, &s.AmountMinor, &s.Currency,
		&statusStr, &ch, &custPhone, &txID, &s.ReturnURL, &whOverride, &metaStr,
		&expiresAt, &verifiedAt, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("lock payment session: %w", err)
	}

	s.Status = models.PaymentStatus(statusStr)
	s.ExpiresAt = expiresAt.Time
	s.CreatedAt = createdAt.Time
	s.UpdatedAt = updatedAt.Time
	if s.Status == models.PaymentVerified {
		_ = tx.Rollback()
		return &s, false, nil
	}

	// 2. Check if a matching SMS is buffered in mfs_unmatched_inbox
	now := time.Now().UTC()
	smsQuery := `SELECT id, sender_phone FROM mfs_unmatched_inbox
		WHERE tenant_id = ? AND channel = ? AND transaction_id = ? AND amount_minor = ? AND status = 'unmatched' AND expires_at > ?
		ORDER BY created_at ASC LIMIT 1`
	if db.Dialect == "postgres" {
		smsQuery = `SELECT id, sender_phone FROM mfs_unmatched_inbox
			WHERE tenant_id = $1 AND channel = $2 AND transaction_id = $3 AND amount_minor = $4 AND status = 'unmatched' AND expires_at > $5
			ORDER BY created_at ASC LIMIT 1 FOR UPDATE`
	}

	var unmatchedID, senderPhone string
	smsErr := tx.QueryRowContext(ctx, smsQuery, tenantID, channel, trxID, s.AmountMinor, now).Scan(&unmatchedID, &senderPhone)
	if smsErr == nil {
		// MATCH FOUND: Confirm payment session atomically
		if phone == "" {
			phone = senderPhone
		}
		updateSession := `UPDATE payment_sessions SET status = ?, channel = ?, transaction_id = ?, customer_phone = ?, verified_at = ?, updated_at = ? WHERE id = ?`
		if db.Dialect == "postgres" {
			updateSession = `UPDATE payment_sessions SET status = $1, channel = $2, transaction_id = $3, customer_phone = $4, verified_at = $5, updated_at = $6 WHERE id = $7`
		}
		if _, err := tx.ExecContext(ctx, updateSession, string(models.PaymentVerified), channel, trxID, phone, now, now, sessionID); err != nil {
			return nil, false, fmt.Errorf("update session to verified: %w", err)
		}

		// Mark unmatched SMS as matched
		updateSMS := `UPDATE mfs_unmatched_inbox SET status = 'matched', matched_session_id = ? WHERE id = ?`
		if db.Dialect == "postgres" {
			updateSMS = `UPDATE mfs_unmatched_inbox SET status = 'matched', matched_session_id = $1 WHERE id = $2`
		}
		if _, err := tx.ExecContext(ctx, updateSMS, sessionID, unmatchedID); err != nil {
			return nil, false, fmt.Errorf("mark unmatched sms matched: %w", err)
		}

		// Enqueue outbox webhook event in the SAME transaction
		webhookPayload, _ := json.Marshal(map[string]any{
			"event":                 "payment.verified",
			"session_id":            sessionID,
			"external_reference_id": s.ExternalReferenceID,
			"amount_minor":          s.AmountMinor,
			"currency":              s.Currency,
			"channel":               channel,
			"transaction_id":        trxID,
			"customer_phone":        phone,
			"verified_at":           now,
		})

		jobID := GenerateUUID()
		insertJob := `INSERT INTO outbox_jobs (id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, created_at, updated_at)
			VALUES (?, ?, 'webhook', ?, 'pending', 0, 6, ?, ?, ?)`
		if db.Dialect == "postgres" {
			insertJob = `INSERT INTO outbox_jobs (id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, created_at, updated_at)
				VALUES ($1, $2, 'webhook', $3, 'pending', 0, 6, $4, $5, $6)`
		}
		if _, err := tx.ExecContext(ctx, insertJob, jobID, tenantID, string(webhookPayload), now, now, now); err != nil {
			return nil, false, fmt.Errorf("enqueue outbox job in tx: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit atomic reconcile: %w", err)
		}

		s.Status = models.PaymentVerified
		s.Channel = channel
		s.TransactionID = trxID
		s.CustomerPhone = phone
		s.VerifiedAt = &now
		return &s, true, nil
	}

	// NO MATCH YET: Move session to 'processing' awaiting the incoming Android SIM SMS
	updateProcessing := `UPDATE payment_sessions SET status = ?, channel = ?, transaction_id = ?, customer_phone = ?, updated_at = ? WHERE id = ?`
	if db.Dialect == "postgres" {
		updateProcessing = `UPDATE payment_sessions SET status = $1, channel = $2, transaction_id = $3, customer_phone = $4, updated_at = $5 WHERE id = $6`
	}
	if _, err := tx.ExecContext(ctx, updateProcessing, string(models.PaymentProcessing), channel, trxID, phone, now, sessionID); err != nil {
		return nil, false, fmt.Errorf("update session to processing: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit session processing: %w", err)
	}

	s.Status = models.PaymentProcessing
	s.Channel = channel
	s.TransactionID = trxID
	s.CustomerPhone = phone
	return &s, false, nil
}

// AtomicReconcileIncomingSMS processes an incoming operator SMS from an Android bridge device atomically.
// If a matching pending session is found, it updates the session to verified and enqueues a webhook in the same transaction.
// If no matching session is waiting, it buffers the SMS into mfs_unmatched_inbox with a 30m TTL.
func (db *DB) AtomicReconcileIncomingSMS(
	ctx context.Context, tenantID, deviceID, channel, senderShortcode, trxID string,
	amountMinor int64, senderPhone, rawSMS string,
) (*models.PaymentSession, bool, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, fmt.Errorf("begin sms tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()

	// 1. Search and lock any pending/processing payment session waiting for this SMS
	findSession := `SELECT id, external_reference_id, amount_minor, currency
		FROM payment_sessions 
		WHERE tenant_id = ? AND channel = ? AND amount_minor = ? AND (transaction_id = ? OR transaction_id IS NULL) 
		  AND status IN ('pending', 'processing') AND expires_at > ?
		ORDER BY created_at DESC LIMIT 1`
	if db.Dialect == "postgres" {
		findSession = `SELECT id, external_reference_id, amount_minor, currency
			FROM payment_sessions 
			WHERE tenant_id = $1 AND channel = $2 AND amount_minor = $3 AND (transaction_id = $4 OR transaction_id IS NULL) 
			  AND status IN ('pending', 'processing') AND expires_at > $5
			ORDER BY created_at DESC LIMIT 1 FOR UPDATE`
	}

	var sessionID, extRef, currency string
	var sessionAmount int64
	err = tx.QueryRowContext(ctx, findSession, tenantID, channel, amountMinor, trxID, now).Scan(&sessionID, &extRef, &sessionAmount, &currency)
	if err == nil {
		// Session matched! Update to verified
		updateSession := `UPDATE payment_sessions SET status = ?, channel = ?, transaction_id = ?, customer_phone = ?, verified_at = ?, updated_at = ? WHERE id = ?`
		if db.Dialect == "postgres" {
			updateSession = `UPDATE payment_sessions SET status = $1, channel = $2, transaction_id = $3, customer_phone = $4, verified_at = $5, updated_at = $6 WHERE id = $7`
		}
		if _, err := tx.ExecContext(ctx, updateSession, string(models.PaymentVerified), channel, trxID, senderPhone, now, now, sessionID); err != nil {
			return nil, false, fmt.Errorf("update session to verified: %w", err)
		}

		// Enqueue outbox webhook event in the SAME transaction
		webhookPayload, _ := json.Marshal(map[string]any{
			"event":                 "payment.verified",
			"session_id":            sessionID,
			"external_reference_id": extRef,
			"amount_minor":          sessionAmount,
			"currency":              currency,
			"channel":               channel,
			"transaction_id":        trxID,
			"customer_phone":        senderPhone,
			"verified_at":           now,
		})

		jobID := GenerateUUID()
		insertJob := `INSERT INTO outbox_jobs (id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, created_at, updated_at)
			VALUES (?, ?, 'webhook', ?, 'pending', 0, 6, ?, ?, ?)`
		if db.Dialect == "postgres" {
			insertJob = `INSERT INTO outbox_jobs (id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, created_at, updated_at)
				VALUES ($1, $2, 'webhook', $3, 'pending', 0, 6, $4, $5, $6)`
		}
		if _, err := tx.ExecContext(ctx, insertJob, jobID, tenantID, string(webhookPayload), now, now, now); err != nil {
			return nil, false, fmt.Errorf("enqueue outbox job: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return nil, false, fmt.Errorf("commit match tx: %w", err)
		}

		matchedSession := &models.PaymentSession{
			ID:                  sessionID,
			TenantID:            tenantID,
			ExternalReferenceID: extRef,
			AmountMinor:         sessionAmount,
			Currency:            currency,
			Status:              models.PaymentVerified,
			Channel:             channel,
			TransactionID:       trxID,
			CustomerPhone:       senderPhone,
			VerifiedAt:          &now,
		}
		return matchedSession, true, nil
	}

	// 2. No matching session waiting: buffer SMS into mfs_unmatched_inbox with 30m TTL
	unmatchedID := GenerateUUID()
	expiresAt := now.Add(30 * time.Minute)
	insertInbox := `INSERT INTO mfs_unmatched_inbox (
		id, tenant_id, device_id, channel, sender_shortcode, transaction_id, amount_minor, sender_phone, raw_sms_body, status, expires_at, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'unmatched', ?, ?)`
	if db.Dialect == "postgres" {
		insertInbox = `INSERT INTO mfs_unmatched_inbox (
			id, tenant_id, device_id, channel, sender_shortcode, transaction_id, amount_minor, sender_phone, raw_sms_body, status, expires_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'unmatched', $10, $11)`
	}

	if _, err := tx.ExecContext(ctx, insertInbox,
		unmatchedID, tenantID, deviceID, channel, senderShortcode, trxID, amountMinor, senderPhone, rawSMS, expiresAt, now,
	); err != nil {
		return nil, false, fmt.Errorf("buffer unmatched sms: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, false, fmt.Errorf("commit buffer tx: %w", err)
	}

	return nil, false, nil
}

// FetchDeadLetterJobs returns all failed jobs in dead_letter status for administrative inspection.
func (db *DB) FetchDeadLetterJobs(ctx context.Context, tenantID string, limit int) ([]*models.OutboxJob, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	query := `SELECT id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, last_error, created_at, updated_at
		FROM outbox_jobs WHERE tenant_id = ? AND status = 'dead_letter' ORDER BY updated_at DESC LIMIT ?`
	if db.Dialect == "postgres" {
		query = `SELECT id, tenant_id, job_type, payload, status, attempts, max_attempts, next_run_at, last_error, created_at, updated_at
			FROM outbox_jobs WHERE tenant_id = $1 AND status = 'dead_letter' ORDER BY updated_at DESC LIMIT $2`
	}

	rows, err := db.QueryContext(ctx, query, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("query dead letter jobs: %w", err)
	}
	defer rows.Close()

	var jobs []*models.OutboxJob
	for rows.Next() {
		var j models.OutboxJob
		var payloadStr string
		var lastErr sql.NullString
		var nextRun, createdAt, updatedAt ScannableTime

		if err := rows.Scan(
			&j.ID, &j.TenantID, &j.JobType, &payloadStr, &j.Status, &j.Attempts,
			&j.MaxAttempts, &nextRun, &lastErr, &createdAt, &updatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan dead letter job: %w", err)
		}
		j.Payload = []byte(payloadStr)
		j.NextRunAt = nextRun.Time
		j.CreatedAt = createdAt.Time
		j.UpdatedAt = updatedAt.Time
		if lastErr.Valid {
			j.LastError = &lastErr.String
		}
		jobs = append(jobs, &j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dead letter rows: %w", err)
	}
	return jobs, nil
}

// RetryDeadLetterJob resets a dead-letter job back to pending status for immediate retry.
func (db *DB) RetryDeadLetterJob(ctx context.Context, tenantID, jobID string) error {
	now := time.Now().UTC()
	query := `UPDATE outbox_jobs SET status = 'pending', attempts = 0, next_run_at = ?, updated_at = ?
		WHERE id = ? AND tenant_id = ? AND status = 'dead_letter'`
	if db.Dialect == "postgres" {
		query = `UPDATE outbox_jobs SET status = 'pending', attempts = 0, next_run_at = $1, updated_at = $2
			WHERE id = $3 AND tenant_id = $4 AND status = 'dead_letter'`
	}

	res, err := db.ExecContext(ctx, query, now, now, jobID, tenantID)
	if err != nil {
		return fmt.Errorf("retry dead letter job: %w", err)
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}
