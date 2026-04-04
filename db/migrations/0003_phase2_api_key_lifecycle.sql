-- Phase 2 API key lifecycle persistence.

BEGIN;

CREATE TABLE IF NOT EXISTS consumer_api_keys (
    id BIGSERIAL PRIMARY KEY,
    consumer_id TEXT NOT NULL REFERENCES consumers(consumer_id) ON DELETE CASCADE,
    api_key_hash TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'active',
    revoked_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    revoked_at TIMESTAMPTZ NULL,
    CONSTRAINT consumer_api_keys_status_chk CHECK (status IN ('active', 'revoked'))
);

CREATE INDEX IF NOT EXISTS idx_consumer_api_keys_consumer_status
    ON consumer_api_keys (consumer_id, status, created_at DESC);

COMMIT;
