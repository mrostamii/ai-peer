-- Phase 2.13.2: PostgreSQL schema v1 (system of record)
-- Apply with your migration tool of choice, or psql:
--   psql "$DATABASE_URL" -f db/migrations/0001_phase2_data_plane.sql

BEGIN;

CREATE TABLE IF NOT EXISTS providers (
    provider_id TEXT PRIMARY KEY,
    peer_id TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS provider_wallet_history (
    id BIGSERIAL PRIMARY KEY,
    provider_id TEXT NOT NULL REFERENCES providers(provider_id) ON DELETE CASCADE,
    wallet_address TEXT NOT NULL,
    active_from TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    active_to TIMESTAMPTZ NULL,
    rotation_proof TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS consumers (
    consumer_id TEXT PRIMARY KEY,
    api_key_hash TEXT NULL,
    wallet_address TEXT NULL,
    consumer_type TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT consumers_type_chk CHECK (consumer_type IN ('prepaid', 'x402'))
);

CREATE TABLE IF NOT EXISTS usage_events (
    request_id TEXT PRIMARY KEY,
    gateway_id TEXT NOT NULL,
    gateway_type TEXT NOT NULL,
    consumer_id TEXT NULL REFERENCES consumers(consumer_id) ON DELETE SET NULL,
    provider_id TEXT NULL REFERENCES providers(provider_id) ON DELETE SET NULL,
    model TEXT NOT NULL,
    tokens_in BIGINT NOT NULL DEFAULT 0,
    tokens_out BIGINT NOT NULL DEFAULT 0,
    cost_usdc NUMERIC(20, 6) NOT NULL DEFAULT 0,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    payment_method TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    settled_at TIMESTAMPTZ NULL,
    CONSTRAINT usage_events_gateway_type_chk CHECK (gateway_type IN ('official', 'community'))
);

CREATE TABLE IF NOT EXISTS balance_ledger (
    id BIGSERIAL PRIMARY KEY,
    consumer_id TEXT NOT NULL REFERENCES consumers(consumer_id) ON DELETE CASCADE,
    entry_type TEXT NOT NULL,
    amount_usdc NUMERIC(20, 6) NOT NULL,
    reference TEXT NOT NULL DEFAULT '',
    balance_after NUMERIC(20, 6) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT balance_ledger_entry_type_chk CHECK (entry_type IN ('credit', 'debit', 'reserve', 'release'))
);

CREATE TABLE IF NOT EXISTS settlements (
    settlement_id TEXT PRIMARY KEY,
    settlement_type TEXT NOT NULL,
    from_wallet TEXT NOT NULL,
    to_wallet TEXT NOT NULL,
    amount_usdc NUMERIC(20, 6) NOT NULL,
    tx_hash TEXT NULL,
    chain TEXT NOT NULL DEFAULT 'base',
    status TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    confirmed_at TIMESTAMPTZ NULL,
    CONSTRAINT settlements_type_chk CHECK (settlement_type IN ('x402_direct', 'prepaid_batch'))
);

CREATE TABLE IF NOT EXISTS settlement_items (
    settlement_id TEXT NOT NULL REFERENCES settlements(settlement_id) ON DELETE CASCADE,
    request_id TEXT NOT NULL REFERENCES usage_events(request_id) ON DELETE CASCADE,
    PRIMARY KEY (settlement_id, request_id)
);

CREATE TABLE IF NOT EXISTS reconciliation_reports (
    id BIGSERIAL PRIMARY KEY,
    period_start TIMESTAMPTZ NOT NULL,
    period_end TIMESTAMPTZ NOT NULL,
    usage_total_usdc NUMERIC(20, 6) NOT NULL DEFAULT 0,
    settled_total_usdc NUMERIC(20, 6) NOT NULL DEFAULT 0,
    onchain_total_usdc NUMERIC(20, 6) NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    notes TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_usage_events_created_at
    ON usage_events (created_at);
CREATE INDEX IF NOT EXISTS idx_usage_events_provider_created_at
    ON usage_events (provider_id, created_at);
CREATE INDEX IF NOT EXISTS idx_usage_events_consumer_created_at
    ON usage_events (consumer_id, created_at);
CREATE INDEX IF NOT EXISTS idx_balance_ledger_consumer_created_at
    ON balance_ledger (consumer_id, created_at);
CREATE INDEX IF NOT EXISTS idx_settlements_status_created_at
    ON settlements (status, created_at);

COMMIT;
