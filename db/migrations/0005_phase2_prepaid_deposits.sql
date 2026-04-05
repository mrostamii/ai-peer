-- Phase 2 prepaid on-chain deposit idempotency and audit table.

BEGIN;

CREATE TABLE IF NOT EXISTS prepaid_deposits (
    tx_hash TEXT PRIMARY KEY,
    network TEXT NOT NULL,
    consumer_id TEXT NOT NULL REFERENCES consumers(consumer_id) ON DELETE CASCADE,
    wallet_address TEXT NOT NULL,
    token_address TEXT NOT NULL,
    to_address TEXT NOT NULL,
    amount_atomic TEXT NOT NULL,
    amount_usdc NUMERIC(20, 6) NOT NULL,
    block_number BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_prepaid_deposits_consumer_created_at
    ON prepaid_deposits (consumer_id, created_at DESC);

COMMIT;
