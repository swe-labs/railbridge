-- ==============================================================================
-- Railbridge v1.0.0 Initial Schema Migration (PostgreSQL & SQLite Compatible)
-- ==============================================================================

-- 1. Tenants (Isolated Client Applications)
CREATE TABLE IF NOT EXISTS tenants (
    id VARCHAR(36) PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    environment VARCHAR(10) NOT NULL DEFAULT 'live',
    api_key_prefix VARCHAR(16) NOT NULL,
    api_key_hash BLOB NOT NULL UNIQUE,
    webhook_url VARCHAR(500),
    webhook_secret_encrypted BLOB,
    branding_config TEXT NOT NULL DEFAULT '{}',
    rate_limit_rpm INTEGER NOT NULL DEFAULT 120,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- 2. Payment Sessions & Ledger
CREATE TABLE IF NOT EXISTS payment_sessions (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    external_reference_id VARCHAR(128) NOT NULL,
    idempotency_key VARCHAR(128),
    amount_minor BIGINT NOT NULL,
    currency CHAR(3) NOT NULL DEFAULT 'BDT',
    status VARCHAR(24) NOT NULL DEFAULT 'pending',
    channel VARCHAR(32),
    customer_phone VARCHAR(24),
    transaction_id VARCHAR(80),
    return_url VARCHAR(500) NOT NULL,
    webhook_url_override VARCHAR(500),
    metadata TEXT NOT NULL DEFAULT '{}',
    provider_payload TEXT,
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    verified_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (tenant_id, idempotency_key)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_payments_unique_trx 
    ON payment_sessions (tenant_id, channel, transaction_id) 
    WHERE transaction_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_payment_sessions_tenant ON payment_sessions (tenant_id, created_at DESC);

-- 3. Unified Messaging Logs (SMS & Email Audit)
CREATE TABLE IF NOT EXISTS message_logs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) NOT NULL REFERENCES tenants(id) ON DELETE RESTRICT,
    idempotency_key VARCHAR(128),
    rail VARCHAR(10) NOT NULL,
    recipient VARCHAR(255) NOT NULL,
    subject VARCHAR(255),
    template_id VARCHAR(100),
    provider_used VARCHAR(50),
    provider_msg_id VARCHAR(128),
    status VARCHAR(24) NOT NULL DEFAULT 'queued',
    attempts SMALLINT NOT NULL DEFAULT 0,
    last_error TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    sent_at TIMESTAMP WITH TIME ZONE,
    UNIQUE (tenant_id, rail, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_message_logs_tenant ON message_logs (tenant_id, rail, created_at DESC);

-- 4. Custom Domain Email Forwarding Aliases
CREATE TABLE IF NOT EXISTS email_aliases (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    source_address VARCHAR(255) NOT NULL UNIQUE,
    target_address VARCHAR(255) NOT NULL,
    is_active BOOLEAN NOT NULL DEFAULT true,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_email_aliases_tenant ON email_aliases (tenant_id);

-- 5. Tenant Registered Android SIM Bridge Devices (BYOD)
CREATE TABLE IF NOT EXISTS devices (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    device_name VARCHAR(100) NOT NULL,
    device_token_hash BLOB NOT NULL UNIQUE,
    device_secret_encrypted BLOB NOT NULL,
    phone_numbers TEXT NOT NULL DEFAULT '[]',
    supported_channels TEXT NOT NULL DEFAULT '["bkash","nagad","rocket","sms"]',
    status VARCHAR(20) NOT NULL DEFAULT 'offline',
    last_heartbeat_at TIMESTAMP WITH TIME ZONE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_devices_heartbeat ON devices (tenant_id, status, last_heartbeat_at);

-- 6. Asymmetric Inbound MFS Buffer (Handles SMS arriving before customer enters TrxID)
CREATE TABLE IF NOT EXISTS mfs_unmatched_inbox (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    device_id VARCHAR(36) NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    channel VARCHAR(32) NOT NULL,
    sender_shortcode VARCHAR(16) NOT NULL,
    transaction_id VARCHAR(80) NOT NULL,
    amount_minor BIGINT NOT NULL,
    sender_phone VARCHAR(24) NOT NULL,
    raw_sms_body TEXT NOT NULL,
    matched_session_id VARCHAR(36) REFERENCES payment_sessions(id) ON DELETE SET NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'unmatched',
    expires_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_unmatched_trx ON mfs_unmatched_inbox (tenant_id, channel, transaction_id);
CREATE INDEX IF NOT EXISTS idx_unmatched_poll ON mfs_unmatched_inbox (tenant_id, channel, amount_minor, status) WHERE status = 'unmatched';

-- 7. Durable Outbox & Worker Queue
CREATE TABLE IF NOT EXISTS outbox_jobs (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(36) NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    job_type VARCHAR(32) NOT NULL,
    payload TEXT NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    attempts SMALLINT NOT NULL DEFAULT 0,
    max_attempts SMALLINT NOT NULL DEFAULT 6,
    next_run_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_error TEXT,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_outbox_poll ON outbox_jobs (next_run_at) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_outbox_tenant ON outbox_jobs (tenant_id, status);
